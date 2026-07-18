package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
)

// runAdd implements the "known add" subcommand.
//
// Usage: known add [content words...] [flags]
//
//	known add -                      # read content from stdin
//	known add some fact without quotes
//	known add 'quoted fact' --scope myproject.api
//
// Flags:
//
//	--scope          Scope path (default: auto from cwd)
//	--title          Short label (2-5 words)
//	--source-type    Source type: file, url, conversation, manual (default: "manual")
//	--source-ref     Source reference (default: "cli")
//	--provenance     Provenance level: verified, inferred, uncertain (default: "inferred")
//	--ttl            Time-to-live duration (e.g., "24h", "168h")
//	--meta           Metadata key=value pairs (repeatable)
//	--label          Labels (repeatable, e.g. --label lang:go)
//	--link           Create edge: type:target-id (repeatable)
//	--supersedes     Content query for the entry this new entry supersedes (one-shot correction)
func runAdd(ctx context.Context, app *App, args []string) error {
	// Check for --batch before full flag parsing so we can delegate early.
	// Known limitation: unquoted content containing the literal token "--batch"
	// (e.g. `known add ... --batch ...`) will misroute to batch mode because this
	// pre-scan runs before pflag sees the args. This is acceptable given the
	// vanishing frequency of "--batch" appearing verbatim in stored facts; a user
	// intending to store such content should quote the argument or use stdin.
	for _, a := range args {
		if a == "--batch" {
			var filtered []string
			for _, arg := range args {
				if arg != "--batch" {
					filtered = append(filtered, arg)
				}
			}
			return runAddBatch(ctx, app, filtered)
		}
		if a == "--" {
			break
		}
	}

	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // suppress pflag's own error output; we format it ourselves
	title := fs.String("title", "", "short label for the entry (2-5 words)")
	scope := fs.String("scope", "", "scope path (default: auto from cwd)")
	sourceType := fs.String("source-type", "manual", "source type (file, url, conversation, manual)")
	sourceRef := fs.String("source-ref", "cli", "source reference")
	provenance := fs.String("provenance", "inferred", "provenance level (verified, inferred, uncertain)")
	observedBy := fs.String("observed-by", "", "who observed this fact (e.g., user, agent)")
	sourceHash := fs.String("source-hash", "", "fingerprint of source at observation time")
	ttl := fs.String("ttl", "", "time-to-live (e.g., 24h, 168h)")
	var metaFlags multiFlag
	fs.Var(&metaFlags, "meta", "metadata key=value (repeatable)")
	var labelFlags multiFlag
	fs.Var(&labelFlags, "label", "label (repeatable, e.g. --label lang:go --label topic:concurrency)")
	var linkFlags multiFlag
	fs.Var(&linkFlags, "link", "create edge: type:target-id (repeatable, e.g. --link elaborates:01KJ...)")
	supersedes := fs.String("supersedes", "", "content query for the entry this new entry supersedes")

	if err := fs.Parse(args); err != nil {
		return formatFlagError(err, fs)
	}

	if *scope == "" {
		*scope = app.Config.DefaultScope
	} else {
		*scope = app.Config.QualifyScope(*scope)
	}

	// Determine content: join remaining positional args, or read from stdin if "-".
	content, err := resolveContent(fs.Args())
	if err != nil {
		return err
	}

	// Content length check.
	if utf8.RuneCountInString(content) == 0 {
		return fmt.Errorf("content is required\nUsage: known add <content> [flags]\n       known add --help for full flag list")
	}
	if len(content) > app.Config.MaxContentLength {
		return fmt.Errorf("content exceeds maximum length (%d > %d bytes); split into smaller entries",
			len(content), app.Config.MaxContentLength)
	}

	// Build the entry.
	source := model.Source{
		Type:      model.SourceType(*sourceType),
		Reference: *sourceRef,
	}
	if err := source.Validate(); err != nil {
		return fmt.Errorf("invalid source: %w", err)
	}

	entry := model.NewEntry(content, source)
	entry.Title = *title
	entry.Scope = *scope
	entry.Provenance = model.Provenance{
		Level: model.ProvenanceLevel(*provenance),
	}
	if *observedBy != "" {
		entry.Freshness.ObservedBy = *observedBy
	}
	if *sourceHash != "" {
		entry.Freshness.SourceHash = *sourceHash
	}

	// TTL is opt-in: only apply when --ttl is explicitly provided.
	// --ttl 0 is accepted for compat (no-op: entry stays permanent).
	if *ttl != "" {
		if *ttl == "0" {
			entry.TTL = nil
			entry.ExpiresAt = nil
		} else {
			dur, err := time.ParseDuration(*ttl)
			if err != nil {
				return fmt.Errorf("invalid ttl %q: %w", *ttl, err)
			}
			entry.SetTTL(dur)
		}
	}

	// Parse metadata.
	if len(metaFlags) > 0 {
		meta := make(model.Metadata, len(metaFlags))
		for _, kv := range metaFlags {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("invalid meta format %q: expected key=value", kv)
			}
			meta[k] = v
		}
		entry.Meta = meta
	}

	// Set labels (filter empty strings).
	if len(labelFlags) > 0 {
		var labels []string
		for _, l := range labelFlags {
			if l != "" {
				labels = append(labels, l)
			}
		}
		entry.Labels = labels
	}

	// Generate embedding. Init noise goes to stderr via the embedder itself;
	// we do not print the model name on stdout.
	embedding, err := app.Embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	entry = entry.WithEmbedding(embedding, app.Embedder.ModelName())

	// Validate before persisting.
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("invalid entry: %w", err)
	}

	// Resolve --supersedes target BEFORE opening any transaction.
	// Resolution uses search (may read existing entries) and must not run inside a write tx.
	// Any failure here aborts before anything is written.
	var supersedesID model.ID
	var hasSupersedesID bool
	if *supersedes != "" {
		id, err := resolveEntryConfident(ctx, app, *supersedes)
		if err != nil {
			return fmt.Errorf("--supersedes: %w", err)
		}
		supersedesID = id
		hasSupersedesID = true
	}

	// Resolve --link targets BEFORE opening any transaction.
	// Resolution validates format, parses IDs, and checks target existence.
	// Any failure here aborts before anything is written.
	type resolvedLink struct {
		spec     string
		edgeType model.EdgeType
		targetID model.ID
	}
	var resolvedLinks []resolvedLink
	for _, linkSpec := range linkFlags {
		edgeType, targetIDStr, ok := strings.Cut(linkSpec, ":")
		if !ok {
			return fmt.Errorf("invalid --link format %q: expected type:target-id (e.g. elaborates:01KJ...)", linkSpec)
		}
		et := model.EdgeType(edgeType)
		if err := et.Validate(); err != nil {
			return fmt.Errorf("--link %q: invalid edge type: %w", linkSpec, err)
		}
		targetID, err := model.ParseID(targetIDStr)
		if err != nil {
			return fmt.Errorf("--link %q: invalid target ID: %w", linkSpec, err)
		}
		if _, err := app.Entries.Get(ctx, targetID); err != nil {
			return fmt.Errorf("--link %q: target entry %s: %w", linkSpec, targetID, err)
		}
		resolvedLinks = append(resolvedLinks, resolvedLink{spec: linkSpec, edgeType: et, targetID: targetID})
	}

	// Persist entry + all edges atomically.
	// If the tx fails, no entry and no edges are written.
	var result *model.Entry
	if err := app.DB.WithTx(ctx, func(txCtx context.Context) error {
		var txErr error
		result, txErr = app.Entries.CreateOrUpdate(txCtx, &entry)
		if txErr != nil {
			return fmt.Errorf("create entry: %w", txErr)
		}
		for _, rl := range resolvedLinks {
			edge := model.NewEdge(result.ID, rl.targetID, rl.edgeType)
			if txErr := app.Edges.Create(txCtx, &edge); txErr != nil {
				return fmt.Errorf("--link %q: create edge: %w", rl.spec, txErr)
			}
		}
		if hasSupersedesID {
			if result.ID == supersedesID {
				return fmt.Errorf("--supersedes: entry cannot supersede itself (%s)", result.ID)
			}
			edge := model.NewEdge(result.ID, supersedesID, model.EdgeSupersedes)
			if txErr := app.Edges.Create(txCtx, &edge); txErr != nil {
				return fmt.Errorf("--supersedes: create edge: %w", txErr)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	deduped := result.Version > 1

	// Fetch link suggestions from zv1.3's engine (non-fatal).
	var suggestions []query.LinkSuggestion
	if app.Engine != nil {
		sugg, err := app.Engine.SuggestLinks(ctx, *result, 3)
		if err != nil {
			fmt.Fprintf(app.Stderr, "warning: link suggestions: %v\n", err)
		} else {
			suggestions = sugg
		}
	}

	// Print confirmation — always, on stdout.
	app.Printer.PrintAddResult(*result, deduped, suggestions)

	// Report edges created.
	for _, rl := range resolvedLinks {
		app.Printer.PrintMessage("Linked %s -[%s]-> %s", result.ID, rl.edgeType, rl.targetID)
	}
	if hasSupersedesID {
		app.Printer.PrintMessage("Supersedes %s -[supersedes]-> %s", result.ID, supersedesID)
	}

	return nil
}

// resolveContent derives the content string from positional args.
// If args is ["-"], content is read from stdin.
// Otherwise, args are joined with a single space.
// Returns an error only on stdin read failure.
func resolveContent(args []string) (string, error) {
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	return strings.Join(args, " "), nil
}

// validAddFlags is the set of flag names accepted by runAdd.
// Used for error suggestion.
var validAddFlags = []string{
	"title", "scope", "source-type", "source-ref", "provenance",
	"observed-by", "source-hash", "ttl", "meta", "label", "link", "batch", "supersedes",
}

// formatFlagError wraps a pflag parse error with a nearest-valid-flag suggestion.
func formatFlagError(err error, _ *flag.FlagSet) error {
	msg := err.Error()
	// pflag formats unknown flags as "unknown flag: --name"
	const prefix = "unknown flag: --"
	if idx := strings.Index(msg, prefix); idx >= 0 {
		badFlag := strings.TrimSpace(msg[idx+len(prefix):])
		suggestion := nearestFlag(badFlag, validAddFlags)
		if suggestion != "" {
			return fmt.Errorf("unknown flag: --%s\n       Did you mean: --%s?\n       Usage: known add <content> [flags]\n              known add --help for full flag list",
				badFlag, suggestion)
		}
		return fmt.Errorf("unknown flag: --%s\n       Usage: known add <content> [flags]\n              known add --help for full flag list",
			badFlag)
	}
	return err
}

// nearestFlag returns the flag name from candidates with the smallest
// Levenshtein distance to name, or "" if there is no clear nearest match.
// The threshold scales with the input length (80% of the longer string),
// catching known synonyms like --confidence → --provenance without
// suggesting on unrelated garbage.
//
// Tie-break: when multiple candidates share the minimum distance, prefer
// any candidate that name is a prefix of (e.g. "source" → "source-ref"
// over "scope", both at distance 4). Among prefix matches, take the
// shortest candidate.
func nearestFlag(name string, candidates []string) string {
	bestDist := len(name) + 1 // worse-than-worst sentinel
	var tied []string
	for _, c := range candidates {
		d := levenshtein(name, c)
		if d < bestDist {
			bestDist = d
			tied = []string{c}
		} else if d == bestDist {
			tied = append(tied, c)
		}
	}
	if len(tied) == 0 {
		return ""
	}

	// Tie-break: prefer a candidate that name is a strict prefix of.
	best := tied[0]
	if len(tied) > 1 {
		for _, c := range tied {
			if strings.HasPrefix(c, name) {
				if !strings.HasPrefix(best, name) || len(c) < len(best) {
					best = c
				}
			}
		}
	}

	// Accept only when distance < 80% of max(len(name), len(best)).
	// This catches audit-identified synonyms without suggesting on garbage.
	maxLen := len(name)
	if len(best) > maxLen {
		maxLen = len(best)
	}
	threshold := (maxLen * 8) / 10
	if threshold < 1 {
		threshold = 1
	}
	if bestDist >= threshold {
		return ""
	}
	return best
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	// Two-row DP.
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// multiFlag accumulates repeated flag values into a slice.
type multiFlag []string

func (f *multiFlag) String() string { return strings.Join(*f, ", ") }
func (f *multiFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}
func (f *multiFlag) Type() string { return "string" }
