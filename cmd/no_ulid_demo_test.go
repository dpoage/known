package cmd

// TestNoULIDDemo is the acceptance proof for known-zv1.3.
//
// Scenario: an agent works in a store with 10+ existing entries, adds two new
// knowledge entries (original + correction), and creates a supersedes edge
// between them — without typing any ULID at any step.
//
// This directly replays the Mode 4 failure from the friction audit:
//   - Agent had: known search ..., regex'd '"id":[0-9]*', got empty (ULIDs
//     don't match integers), stored correction without linking to original.
//   - Fix: content-addressable resolution with confidence rules (exact match
//     or top-1 dominance) means the exact full content string of an entry
//     resolves to that entry even in a 10+ entry store.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/query"
	"github.com/dpoage/known/storage"
)

func TestNoULIDDemo(t *testing.T) {
	ctx := context.Background()

	// --- setup: in-memory DB + stub engine (no real embedder needed) ----------
	db, err := newBackend(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	stub := &demoEmbedder{}
	eng := query.New(db.Entries(), db.Edges(), stub)

	var buf bytes.Buffer
	app := &App{
		DB:      db,
		Entries: db.Entries(),
		Edges:   db.Edges(),
		Engine:  eng,
		Printer: NewPrinter(&buf, false, false),
		Stderr:  &bytes.Buffer{},
		Config:  &AppConfig{DefaultScope: model.RootScope},
	}

	// --- populate 10 noise entries with orthogonal embeddings ----------------
	//
	// These entries exist to ensure a real-world store size. Resolution must
	// still work confidently when these are present.
	noiseTopics := []struct {
		content string
		vec     []float32
	}{
		{"Rate limiter uses token bucket algorithm", []float32{0, 0, 0, 1}},
		{"Database connection pool maximum size is 50", []float32{0, 0, 1, 0}},
		{"Auth middleware validates Bearer tokens", []float32{0, 1, 0, 0}},
		{"Cache TTL for user sessions is 24 hours", []float32{0, 0.7, 0.7, 0}},
		{"WebSocket handler uses pub/sub pattern", []float32{0, 0.5, 0, 0.5}},
		{"Dependency injection container is lazy", []float32{0, 0, 0.5, 0.5}},
		{"Health check endpoint returns 200 OK", []float32{0, 0.3, 0.3, 0.3}},
		{"Metrics exported via Prometheus scrape", []float32{0, 0.6, 0.4, 0}},
		{"Service discovery uses Consul", []float32{0, 0.4, 0.6, 0}},
		{"Log level configurable via environment variable", []float32{0, 0.2, 0.2, 0.6}},
	}

	src := model.Source{Type: model.SourceManual}
	for _, n := range noiseTopics {
		e := model.NewEntry(n.content, src)
		e.Scope = model.RootScope
		e = e.WithEmbedding(n.vec, "demo")
		if _, err := app.Entries.CreateOrUpdate(ctx, &e); err != nil {
			t.Fatalf("noise entry %q: %v", n.content, err)
		}
	}

	// --- step 1: agent stores the original architecture decision --------------
	//
	// Command (no ULID): known add 'Renderer architecture decision: ...'
	const origContent = "Renderer architecture decision: 3D will be a sibling renderer under RendererInterface"
	origVec := []float32{1, 0, 0, 0}

	eOrig := model.NewEntry(origContent, src)
	eOrig.Scope = model.RootScope
	eOrig = eOrig.WithEmbedding(origVec, "demo")
	storedOrig, err := app.Entries.CreateOrUpdate(ctx, &eOrig)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("original ID: %s (agent never types this)", storedOrig.ID)

	// --- step 2: agent stores the correction ----------------------------------
	//
	// Command (no ULID): known add 'CORRECTION: renderer will be a plugin...'
	const corrContent = "CORRECTION: renderer will be a plugin, not a sibling renderer under RendererInterface"
	corrVec := []float32{0.95, 0.31, 0, 0}

	eCorr := model.NewEntry(corrContent, src)
	eCorr.Scope = model.RootScope
	eCorr = eCorr.WithEmbedding(corrVec, "demo")
	storedCorr, err := app.Entries.CreateOrUpdate(ctx, &eCorr)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("correction ID: %s (agent never types this)", storedCorr.ID)

	// --- step 3: agent links via full content query — no ULID typed -----------
	//
	// Command: known link 'Renderer architecture decision: ...' 'CORRECTION: ...' --type supersedes
	//
	// The exact content strings are used as queries. The confidence resolver's
	// exact-content-match rule fires: the query IS the stored content, so it
	// resolves immediately regardless of the 10 noise entries.
	err = runLink(ctx, app, []string{
		corrContent,
		origContent,
		"--type", "supersedes",
	})
	if err != nil {
		t.Fatalf("content-query link (supersedes) failed: %v", err)
	}

	// Verify the supersedes edge was created with the correct direction.
	edges, err := app.Edges.EdgesFrom(ctx, storedCorr.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) == 0 {
		t.Fatal("expected supersedes edge from correction → original, got none")
	}
	var found bool
	for _, edge := range edges {
		if edge.ToID == storedOrig.ID && edge.Type == model.EdgeSupersedes {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected edge correction --[supersedes]--> original; edges from correction: %+v", edges)
	}

	// --- step 4: agent uses link accept with 1-based index — no ULID typed ---
	//
	// Command: known link accept 'Renderer architecture decision: ...' 1
	//
	// Suggestion engine will now exclude the correction (already linked) and
	// propose the next-closest entry. We accept index 1.
	//
	// Add one more related entry as a candidate target.
	relContent := "Graphics pipeline initialization: deferred vs forward shading"
	relVec := []float32{0.85, 0.52, 0, 0}
	eRel := model.NewEntry(relContent, src)
	eRel.Scope = model.RootScope
	eRel = eRel.WithEmbedding(relVec, "demo")
	storedRel, err := app.Entries.CreateOrUpdate(ctx, &eRel)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("related ID: %s (agent never types this)", storedRel.ID)

	err = runLinkAccept(ctx, app, []string{origContent, "1"})
	if err != nil {
		t.Fatalf("link accept index 1 failed: %v", err)
	}

	// Verify a new edge was created from original to the related entry.
	origEdges, err := app.Edges.EdgesFrom(ctx, storedOrig.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(origEdges) == 0 {
		t.Fatal("expected at least one edge from original after accept")
	}
	// The accepted target must be storedRel (correction is already linked in the other direction).
	var foundRel bool
	for _, edge := range origEdges {
		if edge.ToID == storedRel.ID {
			foundRel = true
			break
		}
	}
	if !foundRel {
		t.Errorf("expected accept to link original → related; edges from original: %+v", origEdges)
	}

	// --- step 5: repeat accept is idempotent — no duplicate edges ------------
	//
	// Running accept again on the same entry+index must not create a duplicate.
	edgesBefore := len(origEdges)
	_ = runLinkAccept(ctx, app, []string{origContent, "1"})

	origEdgesAfter, err := app.Edges.EdgesFrom(ctx, storedOrig.ID, storage.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	// Either the same count (idempotent) or one more if there is a new suggestion
	// at index 1 now that relContent is excluded. We just verify no pure duplicate.
	for _, e1 := range origEdgesAfter {
		count := 0
		for _, e2 := range origEdgesAfter {
			if e1.ToID == e2.ToID && e1.Type == e2.Type {
				count++
			}
		}
		if count > 1 {
			t.Errorf("duplicate edge found: from=%s to=%s type=%s (count=%d)", e1.FromID, e1.ToID, e1.Type, count)
		}
	}
	_ = edgesBefore // used in narrative above

	// --- invariant: no ULID appears in any agent-issued command string --------
	agentCommands := []string{
		origContent,
		corrContent,
		corrContent, origContent, "--type", "supersedes", // link args
		origContent, "1",                                 // accept args
	}
	for _, cmd := range agentCommands {
		if looksLikeULID(cmd) {
			t.Errorf("agent command contains what looks like a ULID: %q", cmd)
		}
	}

	output := buf.String()
	t.Logf("CLI output:\n%s", output)
}

// looksLikeULID returns true if s looks like a 26-character Crockford base32
// ULID (all uppercase alphanumeric, no hyphens).
func looksLikeULID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 26 {
		return false
	}
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for _, c := range s {
		if !strings.ContainsRune(crockford, c) {
			return false
		}
	}
	return true
}

// demoEmbedder returns a zero vector. SuggestLinks uses stored embedding
// vectors directly (SearchSimilar on the repo), so this embedder is only
// consulted when the engine needs to embed a text query (content-addressable
// resolution fallback). Zero vectors sort equally, which exercises the exact-
// match path rather than vector dominance.
type demoEmbedder struct{}

func (d *demoEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0, 0, 0, 0}, nil
}
func (d *demoEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0, 0, 0, 0}
	}
	return out, nil
}
func (d *demoEmbedder) Dimensions() int  { return 4 }
func (d *demoEmbedder) ModelName() string { return "demo" }
