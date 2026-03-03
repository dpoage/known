package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
)

const (
	// sessionExpiry is the maximum idle time before a session is considered expired.
	sessionExpiry = 30 * time.Minute
)

// sessionFilePath returns the path to ~/.known/session.
func sessionFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".known", "session")
}

// readSessionID returns the active session ID from the session file or
// KNOWN_SESSION env var. Returns "" if no active session.
func readSessionID() string {
	// Env var override.
	if id := os.Getenv("KNOWN_SESSION"); id != "" {
		return id
	}

	path := sessionFilePath()
	if path == "" {
		return ""
	}

	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	// Check expiry based on file mtime.
	if time.Since(info.ModTime()) > sessionExpiry {
		return ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(data)
}

// writeSessionFile writes the session ID and sets mtime to now.
func writeSessionFile(id string) error {
	path := sessionFilePath()
	if path == "" {
		return fmt.Errorf("cannot determine home directory")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return os.WriteFile(path, []byte(id), 0o644)
}

// touchSessionFile updates the mtime of the session file to keep it alive.
func touchSessionFile() {
	path := sessionFilePath()
	if path == "" {
		return
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

func runSession(ctx context.Context, app *App, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, `Usage: known session <subcommand>

Subcommands:
  start   Start or resume an agent session
  end     End the current session
`)
		return nil
	}

	switch args[0] {
	case "start":
		return runSessionStart(ctx, app, args[1:])
	case "end":
		return runSessionEnd(ctx, app, args[1:])
	default:
		return fmt.Errorf("unknown session subcommand: %s", args[0])
	}
}

func runSessionStart(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("session start", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Check for existing active session.
	existingID := readSessionID()
	if existingID != "" {
		// Reuse active session.
		app.Printer.PrintMessage("%s", existingID)
		return nil
	}

	// Create new session.
	session := &model.Session{
		ID:        model.NewID(),
		StartedAt: time.Now(),
		Scope:     app.Config.DefaultScope,
	}

	if err := app.Sessions.CreateSession(ctx, session); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	idStr := session.ID.String()
	if err := writeSessionFile(idStr); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	app.Printer.PrintMessage("%s", idStr)
	return nil
}

func runSessionEnd(ctx context.Context, app *App, args []string) error {
	fs := flag.NewFlagSet("session end", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	idStr := readSessionID()
	if idStr == "" {
		app.Printer.PrintMessage("No active session.")
		return nil
	}

	id, err := model.ParseID(idStr)
	if err != nil {
		return fmt.Errorf("parse session id: %w", err)
	}

	if err := app.Sessions.EndSession(ctx, id); err != nil {
		return fmt.Errorf("end session: %w", err)
	}

	// Remove session file.
	if path := sessionFilePath(); path != "" {
		_ = os.Remove(path)
	}

	app.Printer.PrintMessage("Session %s ended.", idStr)
	return nil
}

// logSessionEvent logs a session event after a successful command.
// Best-effort: errors are silently ignored to never fail the command.
func logSessionEvent(ctx context.Context, app *App, subcmd string, args []string) {
	if app.SessionID == "" {
		return
	}

	eventType := commandToEventType(subcmd)
	if eventType == "" {
		return
	}

	sessionID, err := model.ParseID(app.SessionID)
	if err != nil {
		return
	}

	event := &model.SessionEvent{
		ID:        model.NewID(),
		SessionID: sessionID,
		EventType: eventType,
		CreatedAt: time.Now(),
	}

	// Extract entry IDs from positional args for relevant commands.
	switch subcmd {
	case "show", "update", "delete":
		if len(args) > 0 {
			if id, err := model.ParseID(args[0]); err == nil {
				event.EntryIDs = []model.ID{id}
			}
		}
	case "link":
		// link takes two IDs as positional args.
		for _, arg := range args {
			if id, err := model.ParseID(arg); err == nil {
				event.EntryIDs = append(event.EntryIDs, id)
			}
		}
	case "recall", "search":
		// Extract query text from positional args.
		event.Query = extractQuery(args)
	}

	_ = app.Sessions.LogEvent(ctx, event)

	// Touch session file to keep it alive.
	touchSessionFile()
}

// commandToEventType maps CLI command names to event types.
func commandToEventType(subcmd string) model.EventType {
	switch subcmd {
	case "recall":
		return model.EventRecall
	case "search":
		return model.EventSearch
	case "show":
		return model.EventShow
	case "add":
		return model.EventAdd
	case "update":
		return model.EventUpdate
	case "link":
		return model.EventLink
	case "delete":
		return model.EventDelete
	default:
		return ""
	}
}

// extractQuery extracts the query string from command args, skipping flags.
// Flags that take values (e.g., --scope foo) consume the next arg.
func extractQuery(args []string) string {
	skip := false
	for _, arg := range args {
		if skip {
			skip = false
			continue
		}
		if arg == "--" {
			continue
		}
		if len(arg) > 0 && arg[0] == '-' {
			// Assume flags starting with -- that don't contain = take a value arg.
			if !containsEquals(arg) {
				skip = true
			}
			continue
		}
		return arg
	}
	return ""
}

func containsEquals(s string) bool {
	for _, c := range s {
		if c == '=' {
			return true
		}
	}
	return false
}
