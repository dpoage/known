//go:build bench

package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
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

	results := make(map[Condition]*EffectivenessResult)

	for _, cond := range conditions {
		if cfg.Log != nil {
			fmt.Fprintf(cfg.Log, "\n--- Condition: %s (answerer: %s) ---\n", cond, cfg.Answerer.Name())
		}

		answers := make(map[string]string)
		for _, sess := range questions.Sessions {
			for _, q := range sess.Questions {
				prompt, err := buildPrompt(q, cond, codebaseDump, fileListing, cfg.RecallCommand)
				if err != nil {
					if cfg.Log != nil {
						fmt.Fprintf(cfg.Log, "  [%s] prompt error: %v\n", q.ID, err)
					}
					continue
				}

				answer, err := cfg.Answerer.Answer(ctx, prompt)
				if err != nil {
					if cfg.Log != nil {
						fmt.Fprintf(cfg.Log, "  [%s] answer error: %v\n", q.ID, err)
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
					fmt.Fprintf(cfg.Log, "  [%s] %s got=%q\n", q.ID, mark, answer)
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
	var context string

	switch cond {
	case ConditionNoMemory:
		context = fmt.Sprintf("You have access to a project with these files:\n%s\n\nYou have no other context about this project.", fileListing)

	case ConditionWithMemory:
		if recallCmd == "" {
			return "", fmt.Errorf("recall command not configured")
		}
		recallOutput, err := runRecall(recallCmd, q.RecallQuery)
		if err != nil {
			// If recall fails, provide empty context rather than erroring.
			recallOutput = "(no recall results)"
		}
		context = fmt.Sprintf("You have a knowledge memory tool. Here is what it returned for your query:\n\n%s", recallOutput)

	case ConditionFullDump:
		context = fmt.Sprintf("Here is the complete source code of the project:\n\n%s", codebaseDump)
	}

	return fmt.Sprintf(`You are answering questions about a Go codebase called "pipeliner".
Answer with ONLY the exact value requested — a file path, function name, config key, type name, etc.
No explanation, no surrounding text, no quotes. Just the answer.

%s

Question: %s
Answer:`, context, q.Question), nil
}

func runRecall(cmdTemplate, query string) (string, error) {
	cmd := strings.Replace(cmdTemplate, "{query}", query, 1)
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty recall command")
	}
	out, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
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
type AnthropicAnswerer struct {
	APIKey      string
	Model       string // e.g., "claude-haiku-4-5-20251001"
	MaxTokens   int
	Temperature float64
	HTTPClient  *http.Client
}

// anthropicRequest is the API request body.
type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
	Messages    []anthropicMessage `json:"messages"`
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

func NewAnthropicAnswerer(model string) *AnthropicAnswerer {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	return &AnthropicAnswerer{
		APIKey:      apiKey,
		Model:       model,
		MaxTokens:   64,
		Temperature: 0,
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *AnthropicAnswerer) Name() string {
	return fmt.Sprintf("anthropic/%s", a.Model)
}

func (a *AnthropicAnswerer) Answer(ctx context.Context, prompt string) (string, error) {
	if a.APIKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	reqBody := anthropicRequest{
		Model:       a.Model,
		MaxTokens:   a.MaxTokens,
		Temperature: a.Temperature,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("API error: %s: %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("empty response content")
	}

	return strings.TrimSpace(apiResp.Content[0].Text), nil
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
