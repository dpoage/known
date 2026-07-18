//go:build ignore

// seedgen deterministically generates a seeded SQLite database for benchmarks.
//
// Usage:
//
//	go run bench/cmd/seedgen/main.go
//
// Output: bench/testdata/seed.db
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage/sqlite"
)

// referenceTime is the fixed point all entry timestamps are relative to.
var referenceTime = time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

// day returns a time that is d days before referenceTime (positive d = past).
func day(d int) time.Time {
	return referenceTime.AddDate(0, 0, -d)
}

// entrySpec describes one entry to be created.
type entrySpec struct {
	content    string
	scope      string
	daysAgo    int
	source     model.Source
	provenance model.ProvenanceLevel
	labels     []string
	category   string // used only for summary
}

// edgeSpec describes one edge to be created (indexes into the entries slice).
type edgeSpec struct {
	fromIdx int
	toIdx   int
	typ     model.EdgeType
}

func main() {
	ctx := context.Background()

	// Resolve the output path relative to the repo root.
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	dbPath := filepath.Join(repoRoot, "bench", "testdata", "seed.db")

	// 1. Remove existing DB.
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("remove old db: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	fmt.Printf("Creating seed database at %s\n", dbPath)

	// 2. Open SQLite and migrate.
	db, err := sqlite.New(ctx, dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	fmt.Println("Migrations applied.")

	// 3. Create scopes.
	scopes := []string{
		"project-alpha",
		"project-alpha.api",
		"project-alpha.auth",
		"project-alpha.storage",
		"project-alpha.cli",
		"project-alpha.deploy",
		"project-beta",
		"project-beta.auth",
		"project-beta.api",
	}
	for _, path := range scopes {
		s := model.NewScope(path)
		if err := db.Scopes().Upsert(ctx, &s); err != nil {
			log.Fatalf("upsert scope %q: %v", path, err)
		}
	}
	fmt.Printf("Created %d scopes.\n", len(scopes))

	// 4. Build entry specs.
	entries := buildEntries()
	fmt.Printf("Defined %d entries.\n", len(entries))

	// 5. Initialise the hugot embedder.
	cfg := embed.Config{
		Embedder:     "hugot",
		Model:        "sentence-transformers/all-MiniLM-L6-v2",
		CacheEnabled: true,
	}
	embedder, err := embed.NewEmbedder(cfg)
	if err != nil {
		log.Fatalf("create embedder: %v", err)
	}
	fmt.Println("Embedder ready.")

	// 6. Embed all entries in a batch.
	texts := make([]string, len(entries))
	for i, e := range entries {
		texts[i] = e.content
	}
	fmt.Printf("Embedding %d entries (batch)...\n", len(texts))
	vectors, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		log.Fatalf("embed batch: %v", err)
	}
	fmt.Printf("Embeddings generated (dim=%d).\n", len(vectors[0]))

	// 7. Create entries in the database.
	modelName := embedder.ModelName()
	manualSource := model.Source{Type: model.SourceManual, Reference: "seedgen"}
	createdEntries := make([]model.Entry, len(entries))

	for i, spec := range entries {
		src := spec.source
		if src.Reference == "" {
			src = manualSource
		}
		e := model.NewEntry(spec.content, src).
			WithScope(spec.scope).
			WithEmbedding(vectors[i], modelName).
			WithFreshness(model.Freshness{
				ObservedAt: day(spec.daysAgo),
				ObservedBy: "seedgen",
			}).
			WithProvenance(model.Provenance{Level: spec.provenance})

		if len(spec.labels) > 0 {
			e = e.WithLabels(spec.labels)
		}

		if err := db.Entries().Create(ctx, &e); err != nil {
			log.Fatalf("create entry %d (%s): %v", i, spec.category, err)
		}
		createdEntries[i] = e

		if (i+1)%20 == 0 || i == len(entries)-1 {
			fmt.Printf("  entries: %d/%d\n", i+1, len(entries))
		}
	}

	// 8. Create edges.
	edges := buildEdges()
	for i, spec := range edges {
		edge := model.NewEdge(createdEntries[spec.fromIdx].ID, createdEntries[spec.toIdx].ID, spec.typ)
		if err := db.Edges().Create(ctx, &edge); err != nil {
			log.Fatalf("create edge %d (%s %d->%d): %v", i, spec.typ, spec.fromIdx, spec.toIdx, err)
		}
	}
	fmt.Printf("Created %d edges.\n", len(edges))

	// 9. Summary.
	cats := map[string]int{}
	for _, e := range entries {
		cats[e.category]++
	}
	edgeTypes := map[model.EdgeType]int{}
	for _, e := range edges {
		edgeTypes[e.typ]++
	}

	fmt.Println("\n=== Seed DB Summary ===")
	fmt.Printf("Scopes:  %d\n", len(scopes))
	fmt.Printf("Entries: %d\n", len(entries))
	for cat, n := range cats {
		fmt.Printf("  %-30s %d\n", cat, n)
	}
	fmt.Printf("Edges:   %d\n", len(edges))
	for typ, n := range edgeTypes {
		fmt.Printf("  %-20s %d\n", typ, n)
	}
	fmt.Println("Done.")
}

// ---------------------------------------------------------------------------
// Entry definitions
// ---------------------------------------------------------------------------

func buildEntries() []entrySpec {
	manual := model.Source{Type: model.SourceManual, Reference: "seedgen"}
	file := func(ref string) model.Source { return model.Source{Type: model.SourceFile, Reference: ref} }
	conv := func(ref string) model.Source { return model.Source{Type: model.SourceConversation, Reference: ref} }

	var e []entrySpec

	// --- Architecture facts (20) — indices 0..19 ---
	e = append(e, []entrySpec{
		{content: "The project-alpha API layer uses a hexagonal architecture with ports and adapters", scope: "project-alpha.api", daysAgo: 45, source: file("docs/architecture.md"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "Authentication is handled via JWT tokens with RS256 signing using the golang-jwt/jwt/v5 library", scope: "project-alpha.auth", daysAgo: 50, source: file("auth/jwt.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The storage layer uses the repository pattern with interfaces defined in storage/storage.go", scope: "project-alpha.storage", daysAgo: 55, source: file("storage/storage.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "SQLite is the primary storage backend with WAL mode enabled for concurrent readers", scope: "project-alpha.storage", daysAgo: 40, source: file("storage/sqlite/sqlite.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The CLI is built with cobra and uses Viper for hierarchical configuration", scope: "project-alpha.cli", daysAgo: 35, source: file("cmd/root.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "All entity IDs are ULIDs which encode creation time and sort lexicographically", scope: "project-alpha.storage", daysAgo: 60, source: file("model/types.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "Embedding vectors are stored alongside entries and support cosine similarity search", scope: "project-alpha.storage", daysAgo: 42, source: file("storage/sqlite/entries.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The graph edges are typed and directional with support for weighted relationships", scope: "project-alpha.storage", daysAgo: 48, source: file("model/edge.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "Scopes are dot-separated hierarchical namespaces for organizing knowledge entries", scope: "project-alpha", daysAgo: 55, source: file("model/scope.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The auth module enforces RBAC with three roles: viewer, editor, and admin", scope: "project-alpha.auth", daysAgo: 38, source: file("auth/rbac.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "API routes are registered via a central router with middleware for logging and auth", scope: "project-alpha.api", daysAgo: 33, source: file("api/router.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The entry content hash is SHA-256 and used for deduplication within a scope", scope: "project-alpha.storage", daysAgo: 52, source: file("model/entry.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "Database migrations use embedded SQL files applied in numeric order at startup", scope: "project-alpha.storage", daysAgo: 58, source: file("storage/sqlite/migrations/"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The deploy pipeline uses a blue-green strategy with health check gating", scope: "project-alpha.deploy", daysAgo: 30, source: file("deploy/pipeline.yaml"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "Optimistic concurrency control is implemented via a version column on entries", scope: "project-alpha.storage", daysAgo: 44, source: file("storage/sqlite/entries.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The CLI supports both interactive and batch modes for knowledge entry management", scope: "project-alpha.cli", daysAgo: 31, source: file("cmd/add.go"), provenance: model.ProvenanceInferred, category: "architecture"},
		{content: "Full-text search uses SQLite FTS5 with BM25 ranking for relevance scoring", scope: "project-alpha.storage", daysAgo: 36, source: file("storage/sqlite/fts.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The API exposes both REST endpoints and a gRPC service for programmatic access", scope: "project-alpha.api", daysAgo: 41, source: file("api/grpc.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "Session tracking records agent interactions for reinforcement learning feedback", scope: "project-alpha", daysAgo: 37, source: file("model/session.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "The knowledge graph supports five edge types: depends-on, contradicts, supersedes, elaborates, and related-to", scope: "project-alpha", daysAgo: 53, source: file("model/edge.go"), provenance: model.ProvenanceVerified, category: "architecture"},
	}...)

	// --- Implementation details (20) — indices 20..39 ---
	e = append(e, []entrySpec{
		{content: "JWT token expiration is set to 15 minutes with a 7-day refresh token window", scope: "project-alpha.auth", daysAgo: 18, source: file("auth/config.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The SQLite busy timeout is configured to 5000ms to handle write contention", scope: "project-alpha.storage", daysAgo: 22, source: file("storage/sqlite/sqlite.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Entry validation enforces a maximum content length of 4096 bytes", scope: "project-alpha.storage", daysAgo: 15, source: file("model/entry.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The hugot embedder uses the all-MiniLM-L6-v2 model producing 384-dimensional vectors", scope: "project-alpha.storage", daysAgo: 20, source: file("embed/hugot.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Rate limiting on the API uses a token bucket algorithm allowing 100 requests per second burst", scope: "project-alpha.api", daysAgo: 12, source: file("api/middleware.go"), provenance: model.ProvenanceInferred, category: "implementation"},
		{content: "The scope path regex requires segments to start with a letter followed by alphanumeric, hyphens, or underscores", scope: "project-alpha", daysAgo: 25, source: file("model/scope.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Edge weights are optional float64 values between 0 and 1 defaulting to 1.0 when unset", scope: "project-alpha.storage", daysAgo: 19, source: file("model/edge.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The CLI add command supports --batch flag for importing multiple entries from a JSON file", scope: "project-alpha.cli", daysAgo: 10, source: conv("session-2026-03-05"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Scope hierarchy is automatically ensured when creating entries via EnsureHierarchy", scope: "project-alpha.storage", daysAgo: 28, source: file("storage/sqlite/scopes.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The FTS5 tokenizer is configured with unicode61 for proper international text handling", scope: "project-alpha.storage", daysAgo: 14, source: file("storage/sqlite/migrations/003_fts.sql"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "CORS is configured to allow origins from localhost:3000 and the production domain", scope: "project-alpha.api", daysAgo: 16, source: file("api/cors.go"), provenance: model.ProvenanceInferred, category: "implementation"},
		{content: "The refresh token is stored as an HTTP-only secure cookie with SameSite=Strict", scope: "project-alpha.auth", daysAgo: 21, source: file("auth/token.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Vector similarity search uses a brute-force scan with optional pre-filtering by scope", scope: "project-alpha.storage", daysAgo: 17, source: file("storage/sqlite/vector.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The content hash deduplication check runs within the same transaction as the insert", scope: "project-alpha.storage", daysAgo: 24, source: file("storage/sqlite/entries.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Session events are stored in a separate table with foreign key to the sessions table", scope: "project-alpha.storage", daysAgo: 13, source: file("storage/sqlite/sessions.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The import command supports --re-embed flag to regenerate embeddings for all imported entries", scope: "project-alpha.cli", daysAgo: 11, source: conv("session-2026-03-04"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Health check endpoint at /healthz returns 200 with JSON body containing version and uptime", scope: "project-alpha.api", daysAgo: 26, source: file("api/health.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The Viper config supports YAML, TOML, and environment variable overrides with KNOWN_ prefix", scope: "project-alpha.cli", daysAgo: 23, source: file("cmd/config.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "Labels are stored as a JSON array column and indexed for efficient filtering", scope: "project-alpha.storage", daysAgo: 15, source: file("storage/sqlite/entries.go"), provenance: model.ProvenanceInferred, category: "implementation"},
		{content: "The gRPC service uses protocol buffers v3 with reflection enabled for debugging", scope: "project-alpha.api", daysAgo: 27, source: file("api/proto/known.proto"), provenance: model.ProvenanceVerified, category: "implementation"},
	}...)

	// --- Operational knowledge (10) — indices 40..49 ---
	e = append(e, []entrySpec{
		{content: "Production deployment requires KNOWN_DB_PATH and KNOWN_EMBEDDER environment variables", scope: "project-alpha.deploy", daysAgo: 8, source: conv("deploy-runbook"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "The CI pipeline runs go test ./... with race detector enabled on every push", scope: "project-alpha.deploy", daysAgo: 5, source: file(".github/workflows/ci.yaml"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "Database backups are taken every 6 hours using SQLite backup API to S3", scope: "project-alpha.deploy", daysAgo: 12, source: conv("ops-channel"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "The staging environment uses :memory: SQLite for fast integration test cycles", scope: "project-alpha.deploy", daysAgo: 7, source: conv("dev-standup"), provenance: model.ProvenanceInferred, category: "operational"},
		{content: "Log level is controlled by KNOWN_LOG_LEVEL defaulting to info in production", scope: "project-alpha.deploy", daysAgo: 10, source: file("deploy/env.yaml"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "The Docker image is based on distroless/static and weighs approximately 35MB", scope: "project-alpha.deploy", daysAgo: 6, source: file("Dockerfile"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "Metrics are exposed on /metrics in Prometheus format including query latency histograms", scope: "project-alpha.api", daysAgo: 9, source: file("api/metrics.go"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "TLS termination happens at the load balancer level; the app listens on plain HTTP port 8080", scope: "project-alpha.deploy", daysAgo: 14, source: conv("infra-review"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "The ONNX model for hugot is downloaded on first run to ~/.known/models/ and cached thereafter", scope: "project-alpha.deploy", daysAgo: 11, source: conv("setup-guide"), provenance: model.ProvenanceVerified, category: "operational"},
		{content: "Connection pool max idle connections is set to 2 to minimize SQLite lock contention", scope: "project-alpha.deploy", daysAgo: 13, source: file("storage/sqlite/sqlite.go"), provenance: model.ProvenanceInferred, category: "operational"},
	}...)

	// --- Contradicting pairs (6 = 3 pairs) — indices 50..55 ---
	// Pair 1: rate limiter algorithm
	e = append(e, []entrySpec{
		{content: "The API rate limiter uses a fixed-window algorithm with 60-second windows", scope: "project-alpha.api", daysAgo: 60, source: conv("design-review-v1"), provenance: model.ProvenanceVerified, labels: []string{"stale"}, category: "contradiction-old"},
		{content: "The API rate limiter was changed from fixed-window to token-bucket in v2.1", scope: "project-alpha.api", daysAgo: 2, source: conv("migration-notes"), provenance: model.ProvenanceVerified, labels: []string{"current"}, category: "contradiction-new"},
	}...)
	// Pair 2: auth token format
	e = append(e, []entrySpec{
		{content: "Authentication tokens are opaque session IDs stored in Redis", scope: "project-alpha.auth", daysAgo: 60, source: conv("design-review-v1"), provenance: model.ProvenanceVerified, labels: []string{"stale"}, category: "contradiction-old"},
		{content: "Authentication was migrated from opaque session tokens to stateless JWTs in v2.0", scope: "project-alpha.auth", daysAgo: 2, source: conv("migration-notes"), provenance: model.ProvenanceVerified, labels: []string{"current"}, category: "contradiction-new"},
	}...)
	// Pair 3: storage backend
	e = append(e, []entrySpec{
		{content: "The primary storage backend is PostgreSQL with pgvector for similarity search", scope: "project-alpha.storage", daysAgo: 60, source: conv("design-review-v1"), provenance: model.ProvenanceVerified, labels: []string{"stale"}, category: "contradiction-old"},
		{content: "Storage was migrated from PostgreSQL to SQLite to eliminate the external dependency", scope: "project-alpha.storage", daysAgo: 2, source: conv("migration-notes"), provenance: model.ProvenanceVerified, labels: []string{"current"}, category: "contradiction-new"},
	}...)

	// --- Elaboration chains (10) — indices 56..65 ---
	// Chain 1: JWT auth details (3 entries)
	e = append(e, []entrySpec{
		{content: "JWT authentication flow: client sends credentials to /auth/login and receives a signed token", scope: "project-alpha.auth", daysAgo: 20, source: file("auth/handler.go"), provenance: model.ProvenanceVerified, category: "elaboration"},
		{content: "The JWT signing key is an RSA-2048 key pair stored in KNOWN_JWT_PRIVATE_KEY and KNOWN_JWT_PUBLIC_KEY env vars", scope: "project-alpha.auth", daysAgo: 19, source: file("auth/keys.go"), provenance: model.ProvenanceVerified, category: "elaboration"},
		{content: "JWT claims include sub (user ID), scope (permission scopes), and exp (expiration) following RFC 7519", scope: "project-alpha.auth", daysAgo: 18, source: file("auth/claims.go"), provenance: model.ProvenanceVerified, category: "elaboration"},
	}...)
	// Chain 2: vector search pipeline (3 entries)
	e = append(e, []entrySpec{
		{content: "Vector similarity search first filters entries by scope prefix then computes distances", scope: "project-alpha.storage", daysAgo: 16, source: file("storage/sqlite/vector.go"), provenance: model.ProvenanceVerified, category: "elaboration"},
		{content: "Cosine similarity is computed as 1 minus the cosine distance between normalized vectors", scope: "project-alpha.storage", daysAgo: 15, source: file("storage/sqlite/vector.go"), provenance: model.ProvenanceVerified, category: "elaboration"},
		{content: "Search results are capped at the requested limit and returned in ascending distance order", scope: "project-alpha.storage", daysAgo: 14, source: file("storage/sqlite/vector.go"), provenance: model.ProvenanceVerified, category: "elaboration"},
	}...)
	// Chain 3: deploy pipeline (2 entries)
	e = append(e, []entrySpec{
		{content: "The blue-green deploy first spins up the green environment and runs smoke tests", scope: "project-alpha.deploy", daysAgo: 12, source: file("deploy/pipeline.yaml"), provenance: model.ProvenanceVerified, category: "elaboration"},
		{content: "Traffic cutover from blue to green uses weighted DNS with a 5-minute bake period", scope: "project-alpha.deploy", daysAgo: 11, source: file("deploy/cutover.sh"), provenance: model.ProvenanceVerified, category: "elaboration"},
	}...)
	// Chain 4: FTS details (2 entries)
	e = append(e, []entrySpec{
		{content: "FTS5 index covers the content column and uses porter stemming for English text", scope: "project-alpha.storage", daysAgo: 13, source: file("storage/sqlite/fts.go"), provenance: model.ProvenanceVerified, category: "elaboration"},
		{content: "FTS5 hyphen handling requires special tokenizer configuration to avoid splitting compound terms", scope: "project-alpha.storage", daysAgo: 10, source: conv("bugfix-session"), provenance: model.ProvenanceVerified, category: "elaboration"},
	}...)

	// --- Cross-project distractors (10) — indices 66..75 ---
	e = append(e, []entrySpec{
		{content: "The project-beta API uses a monolithic architecture with a single handler package", scope: "project-beta.api", daysAgo: 30, source: file("api/handler.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "Authentication in project-beta uses API keys stored in a PostgreSQL database", scope: "project-beta.auth", daysAgo: 25, source: file("auth/apikey.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "Project-beta stores embeddings using pgvector extension in PostgreSQL", scope: "project-beta.api", daysAgo: 28, source: file("storage/pgvector.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "The beta API rate limiter uses a sliding-window algorithm with Redis backing", scope: "project-beta.api", daysAgo: 20, source: file("api/ratelimit.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "Project-beta deploys as a Kubernetes deployment with horizontal pod autoscaling", scope: "project-beta", daysAgo: 22, source: file("deploy/k8s.yaml"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "Beta auth tokens use HMAC-SHA256 signing with symmetric keys rotated monthly", scope: "project-beta.auth", daysAgo: 18, source: file("auth/hmac.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "The beta API exposes a GraphQL endpoint via gqlgen with dataloaders for N+1 prevention", scope: "project-beta.api", daysAgo: 15, source: file("api/graphql.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "Project-beta uses Redis for caching with a 5-minute default TTL on query results", scope: "project-beta", daysAgo: 12, source: file("cache/redis.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "The beta auth module supports OAuth2 with Google and GitHub identity providers", scope: "project-beta.auth", daysAgo: 16, source: file("auth/oauth.go"), provenance: model.ProvenanceVerified, category: "distractor"},
		{content: "Project-beta migrations are managed by golang-migrate with sequential versioning", scope: "project-beta", daysAgo: 26, source: file("storage/migrations/"), provenance: model.ProvenanceVerified, category: "distractor"},
	}...)

	// --- Needle entries (4) — indices 76..79 ---
	e = append(e, []entrySpec{
		{content: "Error code ALPHA-4091 indicates embedding dimension mismatch between stored and query vectors", scope: "project-alpha.storage", daysAgo: 8, source: conv("support-ticket-4091"), provenance: model.ProvenanceVerified, labels: []string{"error-code"}, category: "needle"},
		{content: "Error code ALPHA-5002 means the scope path contains invalid characters violating the segment regex", scope: "project-alpha", daysAgo: 6, source: conv("support-ticket-5002"), provenance: model.ProvenanceVerified, labels: []string{"error-code"}, category: "needle"},
		{content: "Configuration key embed.cache.maxsize controls the maximum number of cached embedding vectors (default 10000)", scope: "project-alpha.cli", daysAgo: 9, source: file("embed/cache.go"), provenance: model.ProvenanceVerified, labels: []string{"config-key"}, category: "needle"},
		{content: "The internal trace ID format is KALPHA-{timestamp}-{random6} used in structured log correlation", scope: "project-alpha.api", daysAgo: 7, source: file("api/trace.go"), provenance: model.ProvenanceVerified, labels: []string{"trace-format"}, category: "needle"},
	}...)

	// --- Additional entries to reach ~85 total (5 more) — indices 80..84 ---
	e = append(e, []entrySpec{
		{content: "The search recall CLI command supports --scope flag for filtering results to a specific namespace", scope: "project-alpha.cli", daysAgo: 9, source: manual, provenance: model.ProvenanceInferred, category: "implementation"},
		{content: "Entry TTL is optional and when set, expired entries are cleaned up by a background goroutine", scope: "project-alpha.storage", daysAgo: 20, source: file("storage/sqlite/cleanup.go"), provenance: model.ProvenanceVerified, category: "implementation"},
		{content: "The provenance level determines trust ranking: verified > inferred > uncertain", scope: "project-alpha", daysAgo: 35, source: file("model/types.go"), provenance: model.ProvenanceVerified, category: "architecture"},
		{content: "Project-beta plans to migrate from PostgreSQL to CockroachDB for multi-region support", scope: "project-beta", daysAgo: 5, source: conv("beta-planning"), provenance: model.ProvenanceUncertain, category: "distractor"},
		{content: "The fuzzy scope resolver uses Levenshtein distance to suggest corrections for mistyped scope paths", scope: "project-alpha.cli", daysAgo: 4, source: conv("feature-review"), provenance: model.ProvenanceVerified, category: "implementation"},
	}...)

	return e
}

// ---------------------------------------------------------------------------
// Edge definitions
// ---------------------------------------------------------------------------

func buildEdges() []edgeSpec {
	return []edgeSpec{
		// --- elaborates (12) ---
		// JWT chain: 56 -> 57 -> 58
		{fromIdx: 57, toIdx: 56, typ: model.EdgeElaborates},
		{fromIdx: 58, toIdx: 57, typ: model.EdgeElaborates},
		// Vector search chain: 59 -> 60 -> 61
		{fromIdx: 60, toIdx: 59, typ: model.EdgeElaborates},
		{fromIdx: 61, toIdx: 60, typ: model.EdgeElaborates},
		// Deploy chain: 62 -> 63
		{fromIdx: 63, toIdx: 62, typ: model.EdgeElaborates},
		// FTS chain: 64 -> 65
		{fromIdx: 65, toIdx: 64, typ: model.EdgeElaborates},
		// More elaborations on architecture entries
		{fromIdx: 20, toIdx: 1, typ: model.EdgeElaborates},  // JWT expiration elaborates auth architecture
		{fromIdx: 21, toIdx: 3, typ: model.EdgeElaborates},  // busy timeout elaborates SQLite architecture
		{fromIdx: 23, toIdx: 6, typ: model.EdgeElaborates},  // hugot model elaborates embedding architecture
		{fromIdx: 29, toIdx: 16, typ: model.EdgeElaborates}, // FTS tokenizer elaborates FTS architecture
		{fromIdx: 31, toIdx: 20, typ: model.EdgeElaborates}, // refresh token elaborates JWT expiration
		{fromIdx: 32, toIdx: 6, typ: model.EdgeElaborates},  // vector scan elaborates embedding architecture

		// --- contradicts (3) ---
		{fromIdx: 50, toIdx: 51, typ: model.EdgeContradicts}, // old rate limiter vs new
		{fromIdx: 52, toIdx: 53, typ: model.EdgeContradicts}, // old auth tokens vs new
		{fromIdx: 54, toIdx: 55, typ: model.EdgeContradicts}, // old storage vs new

		// --- supersedes (3) ---
		{fromIdx: 51, toIdx: 50, typ: model.EdgeSupersedes}, // new rate limiter supersedes old
		{fromIdx: 53, toIdx: 52, typ: model.EdgeSupersedes}, // new auth supersedes old
		{fromIdx: 55, toIdx: 54, typ: model.EdgeSupersedes}, // new storage supersedes old

		// --- depends-on (8) ---
		{fromIdx: 1, toIdx: 0, typ: model.EdgeDependsOn},   // auth depends on API layer
		{fromIdx: 6, toIdx: 3, typ: model.EdgeDependsOn},    // embeddings depend on SQLite
		{fromIdx: 7, toIdx: 2, typ: model.EdgeDependsOn},    // edges depend on storage pattern
		{fromIdx: 10, toIdx: 9, typ: model.EdgeDependsOn},   // router depends on RBAC
		{fromIdx: 16, toIdx: 3, typ: model.EdgeDependsOn},   // FTS depends on SQLite
		{fromIdx: 13, toIdx: 3, typ: model.EdgeDependsOn},   // deploy depends on SQLite (DB path)
		{fromIdx: 24, toIdx: 10, typ: model.EdgeDependsOn},  // rate limiter depends on router
		{fromIdx: 17, toIdx: 0, typ: model.EdgeDependsOn},   // gRPC/REST depends on hex architecture

		// --- related-to (6) ---
		{fromIdx: 4, toIdx: 37, typ: model.EdgeRelatedTo},   // CLI architecture related to viper config
		{fromIdx: 14, toIdx: 11, typ: model.EdgeRelatedTo},  // OCC related to content hash dedup
		{fromIdx: 40, toIdx: 13, typ: model.EdgeRelatedTo},  // deploy env vars related to deploy pipeline
		{fromIdx: 46, toIdx: 17, typ: model.EdgeRelatedTo},  // metrics related to API endpoints
		{fromIdx: 76, toIdx: 6, typ: model.EdgeRelatedTo},   // error ALPHA-4091 related to embeddings
		{fromIdx: 77, toIdx: 25, typ: model.EdgeRelatedTo},  // error ALPHA-5002 related to scope regex
	}
}
