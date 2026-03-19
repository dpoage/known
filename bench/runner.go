//go:build bench

package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Environment variable names used for benchmark configuration.
const (
	envBenchAPIKey   = "BENCH_API_KEY"
	envBenchModel    = "BENCH_MODEL"
	envBenchBaseURL  = "BENCH_BASE_URL"
	envBenchThinking = "BENCH_THINKING" // set to "1" to enable extended thinking
	envAnthropicKey  = "ANTHROPIC_API_KEY"
)

// Answerer takes a prompt and returns a short answer.
type Answerer interface {
	Answer(ctx context.Context, prompt string) (string, error)
	Name() string
}

// RunnerConfig configures the effectiveness runner.
type RunnerConfig struct {
	// Answerer is the LLM (or other agent) that answers questions.
	Answerer Answerer

	// QuestionsPath is the path to questions.yaml.
	QuestionsPath string

	// CodebasePath is the path to the synthetic codebase directory.
	CodebasePath string

	// RecallCommand is the command template for `known recall`.
	// The recall query replaces {query}. Example: "known recall '{query}' --scope pipeliner"
	// If empty, the with_memory condition is skipped.
	RecallCommand string

	// Conditions to evaluate. If nil, all three are run.
	Conditions []Condition

	// Log is an optional writer for progress output.
	Log io.Writer
}

// RunEffectiveness executes the full effectiveness evaluation.
func RunEffectiveness(ctx context.Context, cfg RunnerConfig) (*EffectivenessReport, error) {
	questions, err := LoadQuestions(cfg.QuestionsPath)
	if err != nil {
		return nil, fmt.Errorf("load questions: %w", err)
	}

	codebaseDump, err := LoadCodebaseDump(cfg.CodebasePath)
	if err != nil {
		return nil, fmt.Errorf("load codebase: %w", err)
	}

	// File listing for the no_memory condition.
	fileListing := extractFileListing(codebaseDump)

	conditions := cfg.Conditions
	if len(conditions) == 0 {
		conditions = []Condition{ConditionNoMemory, ConditionWithMemory, ConditionFullDump}
	}

	// Count total questions for progress display.
	totalQuestions := 0
	for _, sess := range questions.Sessions {
		totalQuestions += len(sess.Questions)
	}

	results := make(map[Condition]*EffectivenessResult)

	for _, cond := range conditions {
		if cfg.Log != nil {
			fmt.Fprintf(cfg.Log, "\n--- Condition: %s (answerer: %s) [%d questions] ---\n", cond, cfg.Answerer.Name(), totalQuestions)
		}

		answers := make(map[string]string)
		qNum := 0
		for _, sess := range questions.Sessions {
			for _, q := range sess.Questions {
				qNum++
				prompt, err := buildPrompt(q, cond, codebaseDump, fileListing, cfg.RecallCommand)
				if err != nil {
					if cfg.Log != nil {
						fmt.Fprintf(cfg.Log, "  [%d/%d] [%s] prompt error: %v\n", qNum, totalQuestions, q.ID, err)
					}
					continue
				}

				answer, err := cfg.Answerer.Answer(ctx, prompt)
				if err != nil {
					if cfg.Log != nil {
						fmt.Fprintf(cfg.Log, "  [%d/%d] [%s] answer error: %v\n", qNum, totalQuestions, q.ID, err)
					}
					continue
				}

				answer = strings.TrimSpace(answer)
				answers[q.ID] = answer

				correct := CheckAnswer(q, answer)
				if cfg.Log != nil {
					mark := "x"
					if correct {
						mark = "✓"
					}
					fmt.Fprintf(cfg.Log, "  [%d/%d] [%s] %s got=%q\n", qNum, totalQuestions, q.ID, mark, answer)
				}
			}
		}

		result := ScoreEffectiveness(questions, answers)
		result.Condition = cond
		results[cond] = result

		if cfg.Log != nil {
			fmt.Fprintf(cfg.Log, "  => overall: %.2f\n", result.OverallScore)
		}
	}

	report := CompareEffectiveness(results)
	return report, nil
}

func buildPrompt(q EffectivenessQuestion, cond Condition, codebaseDump, fileListing, recallCmd string) (string, error) {
	var promptContext string

	switch cond {
	case ConditionNoMemory:
		promptContext = fmt.Sprintf("You have access to a project with these files:\n%s\n\nYou have no other context about this project.", fileListing)

	case ConditionWithMemory:
		if recallCmd == "" {
			return "", fmt.Errorf("recall command not configured")
		}
		recallOutput, err := runRecall(recallCmd, q.RecallQuery)
		if err != nil {
			// If recall fails, provide empty context rather than erroring.
			recallOutput = "(no recall results)"
		}
		promptContext = fmt.Sprintf("You have a knowledge memory tool. Here is what it returned for your query:\n\n%s", recallOutput)

	case ConditionFullDump:
		promptContext = fmt.Sprintf("Here is the complete source code of the project:\n\n%s", codebaseDump)
	}

	return fmt.Sprintf(`You are answering questions about a Go codebase called "pipeliner".
Answer with ONLY the exact value requested — a file path, function name, config key, type name, etc.
No explanation, no surrounding text, no quotes. Just the answer.

%s

Question: %s
Answer:`, promptContext, q.Question), nil
}

func runRecall(cmdTemplate, query string) (string, error) {
	// Use shell execution to handle quoting properly. The {query} placeholder
	// is replaced and the whole command is passed to sh -c, so single quotes
	// in the template protect multi-word queries.
	cmd := strings.Replace(cmdTemplate, "{query}", query, 1)
	if cmd == "" {
		return "", fmt.Errorf("empty recall command")
	}
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("recall command failed: %w", err)
	}
	return string(out), nil
}

func extractFileListing(dump string) string {
	var files []string
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "=== ") && strings.HasSuffix(line, " ===") {
			file := strings.TrimPrefix(line, "=== ")
			file = strings.TrimSuffix(file, " ===")
			files = append(files, file)
		}
	}
	return strings.Join(files, "\n")
}

// --- Answerer implementations ---

// AnthropicAnswerer calls the Anthropic Messages API directly.
// Works with Anthropic and any Anthropic-compatible provider (e.g., MiniMax).
type AnthropicAnswerer struct {
	APIKey       string
	Model        string // e.g., "claude-haiku-4-5-20251001", "MiniMax-M2.7"
	BaseURL      string // e.g., "https://api.anthropic.com", "https://api.minimax.io/anthropic"
	MaxTokens    int
	BudgetTokens int // thinking budget for extended-thinking models (0 = no thinking)
	Temperature  float64
	HTTPClient   *http.Client
}

// anthropicThinking configures extended thinking.
type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// anthropicRequest is the API request body.
type anthropicRequest struct {
	Model       string              `json:"model"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature float64             `json:"temperature"`
	Thinking    *anthropicThinking  `json:"thinking,omitempty"`
	Messages    []anthropicMessage  `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse is the API response body.
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewAnthropicAnswerer creates an answerer for the Anthropic Messages API.
// Also works with Anthropic-compatible providers like MiniMax.
//
// When thinking is true, enables extended thinking with a 4096-token budget
// and 8192 max tokens (budget must be < max). This is required for thinking
// models like MiniMax M2.7 which return only thinking blocks without it.
func NewAnthropicAnswerer(apiKey, model, baseURL string, thinking bool) *AnthropicAnswerer {
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	maxTokens := 4096
	budgetTokens := 0
	if thinking {
		budgetTokens = 4096
		maxTokens = 8192 // must be > budget_tokens
	}
	return &AnthropicAnswerer{
		APIKey:       apiKey,
		Model:        model,
		BaseURL:      strings.TrimRight(baseURL, "/"),
		MaxTokens:    maxTokens,
		BudgetTokens: budgetTokens,
		Temperature:  0,
		HTTPClient:   &http.Client{Timeout: 5 * time.Minute},
	}
}

func (a *AnthropicAnswerer) Name() string {
	if strings.Contains(a.BaseURL, "anthropic.com") {
		return fmt.Sprintf("anthropic/%s", a.Model)
	}
	return fmt.Sprintf("anthropic-compat/%s", a.Model)
}

func (a *AnthropicAnswerer) Answer(ctx context.Context, prompt string) (string, error) {
	if a.APIKey == "" {
		return "", fmt.Errorf("%s not set", envAnthropicKey)
	}

	reqBody := anthropicRequest{
		Model:       a.Model,
		MaxTokens:   a.MaxTokens,
		Temperature: a.Temperature,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}
	if a.BudgetTokens > 0 {
		reqBody.Thinking = &anthropicThinking{
			Type:         "enabled",
			BudgetTokens: a.BudgetTokens,
		}
		reqBody.Temperature = 1 // thinking models require temperature=1
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := a.BaseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshal response (body=%s): %w", truncBody(respBody, 500), err)
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("API error: %s: %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("empty response content (body=%s)", truncBody(respBody, 500))
	}

	// Find the first text block — some providers include thinking blocks
	// before the actual text response.
	for _, block := range apiResp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return strings.TrimSpace(block.Text), nil
		}
	}

	return "", fmt.Errorf("no text content in response (body=%s)", truncBody(respBody, 500))
}

func truncBody(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// OpenAIAnswerer calls any OpenAI-compatible chat completions API.
// Works with OpenAI, Minimax, Together, Groq, local vLLM, etc.
type OpenAIAnswerer struct {
	APIKey      string
	Model       string  // e.g., "MiniMax-M1-80k"
	BaseURL     string  // e.g., "https://api.minimaxi.chat/v1"
	MaxTokens   int
	Temperature float64
	HTTPClient  *http.Client
}

type openaiRequest struct {
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature float64           `json:"temperature"`
	Messages    []openaiMessage   `json:"messages"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// NewOpenAIAnswerer creates an answerer for any OpenAI-compatible API.
// The apiKey, model, and baseURL parameters are used directly — environment
// variable resolution is the caller's responsibility.
func NewOpenAIAnswerer(apiKey, model, baseURL string) *OpenAIAnswerer {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIAnswerer{
		APIKey:      apiKey,
		Model:       model,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		MaxTokens:   4096,
		Temperature: 0,
		HTTPClient:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *OpenAIAnswerer) Name() string {
	return fmt.Sprintf("openai-compat/%s", o.Model)
}

func (o *OpenAIAnswerer) Answer(ctx context.Context, prompt string) (string, error) {
	if o.APIKey == "" {
		return "", fmt.Errorf("%s not set", envBenchAPIKey)
	}

	reqBody := openaiRequest{
		Model:       o.Model,
		MaxTokens:   o.MaxTokens,
		Temperature: o.Temperature,
		Messages: []openaiMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := o.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("empty response choices")
	}

	return strings.TrimSpace(apiResp.Choices[0].Message.Content), nil
}

// CommandAnswerer shells out to an arbitrary command.
// The prompt is passed on stdin, answer read from stdout.
type CommandAnswerer struct {
	Command string   // e.g., "claude", "ollama"
	Args    []string // e.g., ["--model", "haiku", "-p"]
	label   string
}

func NewCommandAnswerer(name string, command string, args ...string) *CommandAnswerer {
	return &CommandAnswerer{
		Command: command,
		Args:    args,
		label:   name,
	}
}

func (c *CommandAnswerer) Name() string {
	return c.label
}

func (c *CommandAnswerer) Answer(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("command %s failed: %w", c.Command, err)
	}
	return strings.TrimSpace(string(out)), nil
}
