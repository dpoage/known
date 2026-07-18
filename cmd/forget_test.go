package cmd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	flag "github.com/spf13/pflag"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
)

// newForgetTestApp sets up an in-memory App with stub embedder for hermetic tests.
func newForgetTestApp(t *testing.T) (*App, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	var buf bytes.Buffer
	stub := &stubSuggestEmbedder{}
	app := &App{
		DB:       db,
		Entries:  db.Entries(),
		Edges:    db.Edges(),
		Embedder: stub,
		Engine:   query.New(db.Entries(), db.Edges(), stub),
		Printer:  NewPrinter(&buf, false, false),
		Config:   &AppConfig{DefaultScope: model.RootScope},
	}
	return app, func() { db.Close() }
}

func addForgetTestEntry(t *testing.T, app *App, content string) model.Entry {
	t.Helper()
	entry := model.NewEntry(content, model.Source{Type: model.SourceManual})
	entry.Scope = model.RootScope
	if err := app.Entries.Create(context.Background(), &entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

// TestForgetByULID verifies that `known forget <ULID> --force` deletes the entry.
func TestForgetByULID(t *testing.T) {
	app, cleanup := newForgetTestApp(t)
	defer cleanup()
	ctx := context.Background()

	entry := addForgetTestEntry(t, app, "The staging endpoint is api.staging.example.com")

	err := runDelete(ctx, app, []string{entry.ID.String(), "--force"}, "forget")
	if err != nil {
		t.Fatalf("runDelete (forget by ULID): %v", err)
	}

	// Entry should be gone.
	_, err = app.Entries.Get(ctx, entry.ID)
	if err == nil {
		t.Fatal("expected entry to be deleted, but Get succeeded")
	}
}

// TestForgetByExactContent verifies content-addressable deletion by exact match.
func TestForgetByExactContent(t *testing.T) {
	app, cleanup := newForgetTestApp(t)
	defer cleanup()
	ctx := context.Background()

	entry := addForgetTestEntry(t, app, "The canary deploy threshold is 5%")

	err := runDelete(ctx, app, []string{"The canary deploy threshold is 5%", "--force"}, "forget")
	if err != nil {
		t.Fatalf("runDelete (forget by exact content): %v", err)
	}

	_, err = app.Entries.Get(ctx, entry.ID)
	if err == nil {
		t.Fatal("expected entry to be deleted, but Get succeeded")
	}
}

// TestForgetAmbiguousRefuses verifies that ambiguous content queries refuse to delete.
func TestForgetAmbiguousRefuses(t *testing.T) {
	app, cleanup := newForgetTestApp(t)
	defer cleanup()
	ctx := context.Background()

	entry1 := addForgetTestEntry(t, app, "The rate limit is 100 req/s for public API")
	entry2 := addForgetTestEntry(t, app, "The rate limit is 50 req/s for internal API")

	err := runDelete(ctx, app, []string{"rate limit", "--force"}, "forget")
	if err == nil {
		t.Fatal("expected error for ambiguous query, got nil")
	}
	if strings.Contains(err.Error(), "Deleted") {
		t.Errorf("unexpected delete success in error message: %v", err)
	}

	// Both entries must still exist.
	for _, e := range []model.Entry{entry1, entry2} {
		if _, err2 := app.Entries.Get(ctx, e.ID); err2 != nil {
			t.Errorf("entry %s should still exist but Get failed: %v", e.ID, err2)
		}
	}
}

// TestRememberHelpNonEmpty verifies that `known remember --help` prints non-empty help.
func TestRememberHelpNonEmpty(t *testing.T) {
	// Capture stderr by redirecting through printAddHelp directly (it writes to os.Stderr).
	// We test printAddHelp output via a buffer by verifying the help text for both names.
	var addBuf, rememberBuf bytes.Buffer

	// printAddHelp writes to os.Stderr; we test the content logic by calling a wrapper.
	// Since printAddHelp is unexported and writes to os.Stderr, we verify:
	// 1) runAdd returns flag.ErrHelp on --help
	// 2) the help text contains the invoked command name

	// Verify runAdd returns ErrHelp for both "add" and "remember".
	for _, name := range []string{"add", "remember"} {
		_ = addBuf
		_ = rememberBuf
		// We cannot easily capture os.Stderr in a unit test, but we CAN verify
		// the return value is flag.ErrHelp (not nil, not a different error).
		// The integration of help output is verified by the content of printAddHelp.
		ctx := context.Background()
		app, cleanup := newForgetTestApp(t)

		err := runAdd(ctx, app, []string{"--help"}, name)
		cleanup()
		if err == nil {
			t.Errorf("runAdd(%q, --help): expected ErrHelp, got nil", name)
			continue
		}
		if !isErrHelp(err) {
			t.Errorf("runAdd(%q, --help): expected ErrHelp, got %v", name, err)
		}
	}
}

func isErrHelp(err error) bool {
	return err == flag.ErrHelp
}

// TestAddHelpEqualsRememberHelpModuloName verifies symmetry: the only difference
// between add help and remember help is the command name in usage lines.
func TestAddHelpEqualsRememberHelpModuloName(t *testing.T) {
	// Capture printAddHelp output by intercepting os.Stderr.
	// We use a pipe to capture writes to stderr.
	addHelp := captureStderr(t, func() { printAddHelp("add") })
	rememberHelp := captureStderr(t, func() { printAddHelp("remember") })

	if addHelp == "" {
		t.Fatal("add help output is empty")
	}
	if rememberHelp == "" {
		t.Fatal("remember help output is empty")
	}

	// Normalize: replace "remember" with "add" in the remember help; should be identical.
	normalized := strings.ReplaceAll(rememberHelp, "remember", "add")
	if normalized != addHelp {
		t.Errorf("help texts differ beyond command name:\nadd:\n%s\nremember (normalized):\n%s", addHelp, normalized)
	}
}

// captureStderr captures output written to os.Stderr during fn() and returns it as a string.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = oldStderr
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
