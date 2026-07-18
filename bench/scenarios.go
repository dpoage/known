//go:build bench

package bench

// Scenario defines a named benchmark scenario with one or more queries.
type Scenario struct {
	Name    string
	Queries []ScenarioQuery
}

// ScenarioQuery describes a single query and its expected outcomes.
// Entry references use content substrings rather than IDs, since IDs are
// ULIDs generated at seed time and not known in advance.
type ScenarioQuery struct {
	// Query parameters
	Text        string
	Scope       string
	Limit       int
	Threshold   float64
	Recency     float64
	ExpandDepth int
	TextSearch  bool
	Provenance  string // empty = all, or "verified"/"inferred"/"uncertain"

	// IncludeSuperseded disables the demotion of superseded entries
	// (known-5oq: normally Score*0.1 for entries with an incoming
	// supersedes edge). Used to falsify that the demotion is load-bearing.
	IncludeSuperseded bool

	// Expected outcomes — content substrings to identify entries
	MustIncludeContent      []string          // Content substrings that MUST appear in results
	MustExcludeContent      []string          // Content substrings that MUST NOT appear
	MustRankAboveContent    map[string]string // content_higher -> content_lower
	MustFlagConflictContent []string          // Content substrings that must have HasConflict=true
	ExpectReachContent      map[string]string // Content substring -> expected ReachMethod
}

// AblationConfig controls which features are disabled for an ablation run.
type AblationConfig struct {
	Name                  string
	DisableGraphExpansion bool // force ExpandDepth=0
	DisableTextSearch     bool // skip text search in hybrid
	DisableFreshness      bool // force RecencyWeight=0
}

// DefaultAblations returns the standard ablation configs.
// Scope filtering is not included because the query engine requires a scope
// and the seed data has no universal root scope. Scenario C (Scope Isolation)
// tests scoping effectiveness directly.
func DefaultAblations() []AblationConfig {
	return []AblationConfig{
		{Name: "Graph Expansion", DisableGraphExpansion: true},
		{Name: "FTS5 Fusion", DisableTextSearch: true},
		{Name: "Freshness Weighting", DisableFreshness: true},
	}
}

// AllScenarios returns the complete set of benchmark scenarios.
func AllScenarios() []Scenario {
	return []Scenario{
		scenarioA(),
		scenarioB(),
		scenarioC(),
		scenarioD(),
		scenarioE(),
		scenarioF(),
		scenarioG(),
		scenarioH(),
		scenarioI(),
		scenarioJ(),
	}
}

// scenarioA: Codebase Discovery Recall — basic vector recall of architecture knowledge.
func scenarioA() Scenario {
	return Scenario{
		Name: "A: Codebase Discovery Recall",
		Queries: []ScenarioQuery{
			{
				Text:      "How does authentication work?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"JWT tokens with RS256 signing",
					"RBAC with three roles: viewer, editor, and admin",
				},
				MustExcludeContent: []string{
					"project-beta uses API keys",
				},
			},
			{
				Text:      "Where are database migrations?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"Database migrations use embedded SQL files",
				},
			},
			{
				Text:      "How is the API structured?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"hexagonal architecture with ports and adapters",
				},
				MustExcludeContent: []string{
					"monolithic architecture with a single handler",
				},
			},
			{
				Text:      "What storage backend is used and how does vector search work?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"SQLite is the primary storage backend",
					"Embedding vectors are stored alongside entries",
				},
			},
		},
	}
}

// scenarioB: Contradiction Resolution — conflict detection with contradicting pairs.
// Tests that contradicts edges cause HasConflict=true on results, AND that
// the current entry outranks the stale one (known-5oq: superseded entries
// are demoted Score*0.1 after enrichSuperseded — each pair here also carries
// a supersedes edge, new -> old, per seedgen buildEdges).
func scenarioB() Scenario {
	return Scenario{
		Name: "B: Contradiction Resolution",
		Queries: []ScenarioQuery{
			{
				Text:        "What rate limiting algorithm does the API use?",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 1,
				MustIncludeContent: []string{
					"fixed-window algorithm with 60-second windows",
					"changed from fixed-window to token-bucket in v2.1",
				},
				MustFlagConflictContent: []string{
					"fixed-window algorithm with 60-second windows",
					"changed from fixed-window to token-bucket in v2.1",
				},
				MustRankAboveContent: map[string]string{
					"changed from fixed-window to token-bucket in v2.1": "fixed-window algorithm with 60-second windows",
				},
			},
			{
				Text:        "What format are authentication tokens?",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 1,
				MustIncludeContent: []string{
					"opaque session IDs stored in Redis",
					"migrated from opaque session tokens to stateless JWTs",
				},
				MustFlagConflictContent: []string{
					"opaque session IDs stored in Redis",
					"migrated from opaque session tokens to stateless JWTs",
				},
				MustRankAboveContent: map[string]string{
					"migrated from opaque session tokens to stateless JWTs": "opaque session IDs stored in Redis",
				},
			},
			{
				Text:        "What is the primary storage backend?",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 1,
				MustIncludeContent: []string{
					"PostgreSQL with pgvector for similarity search",
					"migrated from PostgreSQL to SQLite",
				},
				MustFlagConflictContent: []string{
					"PostgreSQL with pgvector for similarity search",
					"migrated from PostgreSQL to SQLite",
				},
				MustRankAboveContent: map[string]string{
					"migrated from PostgreSQL to SQLite": "PostgreSQL with pgvector for similarity search",
				},
			},
		},
	}
}

// scenarioC: Scope Isolation — scoped queries filter correctly.
func scenarioC() Scenario {
	return Scenario{
		Name: "C: Scope Isolation",
		Queries: []ScenarioQuery{
			{
				Text:      "How does authentication work?",
				Scope:     "project-alpha.auth",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"JWT tokens with RS256 signing",
				},
				MustExcludeContent: []string{
					"project-beta uses API keys",
					"Beta auth tokens use HMAC-SHA256",
					"OAuth2 with Google and GitHub",
				},
			},
			{
				Text:      "How does authentication work?",
				Scope:     "project-beta.auth",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"project-beta uses API keys",
				},
				MustExcludeContent: []string{
					"JWT tokens with RS256 signing",
					"RBAC with three roles: viewer, editor, and admin",
				},
			},
			{
				Text:      "How is the API structured?",
				Scope:     "project-beta.api",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"monolithic architecture with a single handler",
				},
				MustExcludeContent: []string{
					"hexagonal architecture with ports and adapters",
				},
			},
		},
	}
}

// scenarioD: Needle-in-Haystack with Graph — graph expansion surfaces entries
// that vector search alone would not return in the top results.
// The probe data shows that querying "deployment process" finds deploy entries
// directly, but expansion follows edges to related storage/config entries that
// vector search alone would not surface in the top 5.
func scenarioD() Scenario {
	return Scenario{
		Name: "D: Needle-in-Haystack with Graph",
		Queries: []ScenarioQuery{
			{
				// Query deployment; expansion should follow edges to surface
				// storage and config entries that are linked but semantically distant.
				Text:        "deployment process",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 2,
				MustIncludeContent: []string{
					"blue-green strategy with health check gating",
					// These are reached only via expansion edges, not vector similarity
					"SQLite is the primary storage backend",
				},
				ExpectReachContent: map[string]string{
					"blue-green strategy with health check gating": "direct",
					"SQLite is the primary storage backend":        "expansion",
				},
			},
			{
				// Same query without expansion — should NOT find storage entries.
				Text:      "deployment process",
				Scope:     "project-alpha",
				Limit:     5,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"blue-green strategy with health check gating",
				},
				MustExcludeContent: []string{
					"SQLite is the primary storage backend",
				},
			},
		},
	}
}

// scenarioE: FTS Rescue — text search finds keyword-specific entries that
// vector search misranks. The probe data confirms vector search for "ALPHA-4091"
// returns ALPHA-5002 as the top result (wrong!), while FTS finds the exact match.
func scenarioE() Scenario {
	return Scenario{
		Name: "E: FTS Rescue",
		Queries: []ScenarioQuery{
			{
				// With FTS enabled (hybrid), the correct entry should be found,
				// AND rank above at least one near-miss code that vector-only
				// search prefers over the true match.
				Text:       "ALPHA-4091",
				Scope:      "project-alpha",
				Limit:      5,
				TextSearch: true,
				MustIncludeContent: []string{
					"ALPHA-4091 indicates embedding dimension mismatch",
				},
				MustRankAboveContent: map[string]string{
					"ALPHA-4091 indicates embedding dimension mismatch": "ALPHA-5002 means the scope path contains invalid characters",
				},
			},
			{
				// Without FTS (vector only), the wrong entry ranks first.
				// This query should NOT find ALPHA-4091 in top 3 results.
				Text:      "ALPHA-4091",
				Scope:     "project-alpha",
				Limit:     3,
				Threshold: 0.0,
				MustExcludeContent: []string{
					"ALPHA-4091 indicates embedding dimension mismatch",
				},
			},
			{
				Text:       "ALPHA-5002",
				Scope:      "project-alpha",
				Limit:      5,
				TextSearch: true,
				MustIncludeContent: []string{
					"ALPHA-5002 means the scope path contains invalid characters",
				},
			},
			{
				Text:       "embed.cache.maxsize",
				Scope:      "project-alpha",
				Limit:      5,
				TextSearch: true,
				MustIncludeContent: []string{
					"embed.cache.maxsize controls the maximum number of cached",
				},
			},
			{
				// Distractor rescue: vector-only search for "ALPHA-4092" ranks
				// it 3rd behind two other near-miss codes (known-58u
				// distractor). FTS pinpoints the exact numeric token match and
				// puts it in a tight top-2, so the vector-only ablation both
				// misses it (Inclusion) and can't satisfy the rank-above
				// (Ranking) — a sharper degradation than a generous Limit.
				Text:       "ALPHA-4092",
				Scope:      "project-alpha",
				Limit:      5,
				TextSearch: true,
				MustIncludeContent: []string{
					"ALPHA-4092 indicates the embedding cache was evicted mid-request",
				},
				MustRankAboveContent: map[string]string{
					"ALPHA-4092 indicates the embedding cache was evicted mid-request": "ALPHA-4090 indicates a missing scope header on the request",
				},
			},
			{
				// Same pattern as query 1 (ALPHA-4091): vector-only search
				// for "ALPHA-4093" doesn't even surface it in the top 5 —
				// three other error codes and two architecture entries win on
				// raw similarity. Only the exact numeric FTS token rescues it.
				Text:       "ALPHA-4093",
				Scope:      "project-alpha",
				Limit:      5,
				TextSearch: true,
				MustIncludeContent: []string{
					"ALPHA-4093 indicates the tokenizer vocabulary file failed checksum validation",
				},
				MustRankAboveContent: map[string]string{
					"ALPHA-4093 indicates the tokenizer vocabulary file failed checksum validation": "ALPHA-5002 means the scope path contains invalid characters",
				},
			},
		},
	}
}

// scenarioF: Multi-Step Session — cross-session knowledge accumulation.
func scenarioF() Scenario {
	return Scenario{
		Name: "F: Multi-Step Session",
		Queries: []ScenarioQuery{
			{
				// Step 1: discover the storage architecture
				Text:      "What storage backend and database is used?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"SQLite is the primary storage backend",
				},
			},
			{
				// Step 2: learn about the deployment requirements
				Text:      "What environment variables are needed for deployment?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"KNOWN_DB_PATH and KNOWN_EMBEDDER environment variables",
				},
			},
			{
				// Step 3: combine knowledge — how do storage and deploy relate?
				Text:      "How does the deploy pipeline interact with the database?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"blue-green strategy with health check gating",
				},
			},
			{
				// Step 4: drill into CI/CD specifics
				Text:      "How are tests and CI configured?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"go test ./... with race detector enabled",
				},
			},
		},
	}
}

// scenarioG: Provenance Trust — provenance filtering.
func scenarioG() Scenario {
	return Scenario{
		Name: "G: Provenance Trust",
		Queries: []ScenarioQuery{
			{
				// Verified-only query should return only verified entries.
				Text:       "How does the CLI work?",
				Scope:      "project-alpha",
				Limit:      10,
				Threshold:  0.0,
				Provenance: "verified",
				MustIncludeContent: []string{
					"cobra and uses Viper for hierarchical configuration",
				},
				MustExcludeContent: []string{
					// Index 15 is inferred provenance: "CLI supports both interactive and batch modes"
					"CLI supports both interactive and batch modes",
				},
			},
			{
				// Unfiltered query should return more results including inferred.
				Text:      "How does the CLI work?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"cobra and uses Viper for hierarchical configuration",
				},
			},
			{
				// Verified-only query for API; should exclude inferred entries like rate limiter detail and CORS.
				Text:       "API middleware and configuration",
				Scope:      "project-alpha",
				Limit:      10,
				Threshold:  0.0,
				Provenance: "verified",
				MustExcludeContent: []string{
					// Index 24: "Rate limiting on the API uses a token bucket" — inferred
					"Rate limiting on the API uses a token bucket algorithm allowing 100 requests",
					// Index 30: "CORS is configured to allow origins" — inferred
					"CORS is configured to allow origins from localhost",
				},
			},
		},
	}
}

// scenarioH: Supersede Chains — a superseded entry must never outrank its
// successor, even transitively through a multi-hop chain (known-5oq demotes
// Score*0.1 for any entry with an incoming supersedes edge; known-qam is the
// CLI-side one-shot --supersedes flag that produces these edges in practice).
// Corpus: notification service polling -> long-polling -> SSE, two
// supersedes edges (v2->v1, v3->v2), so v1 is only demoted directly (no edge
// v3->v1) — this scenario proves the direct-chain demotion holds regardless.
// The bypass case (IncludeSuperseded=true, proving the demotion is load-
// bearing rather than accidental) is covered by the pipeline-level
// falsification test TestSupersedeDemotion_Falsification in bench_test.go.
func scenarioH() Scenario {
	return Scenario{
		Name: "H: Supersede Chains",
		Queries: []ScenarioQuery{
			{
				Text:        "How does the notification service deliver events?",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 1,
				MustIncludeContent: []string{
					"Server-Sent Events (SSE) for real-time push instead of polling",
				},
				MustRankAboveContent: map[string]string{
					"Server-Sent Events (SSE) for real-time push instead of polling": "used polling every 30 seconds to check for new events",
				},
			},
			{
				Text:        "How does the notification service deliver events?",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 1,
				MustIncludeContent: []string{
					"Server-Sent Events (SSE) for real-time push instead of polling",
				},
				MustRankAboveContent: map[string]string{
					"Server-Sent Events (SSE) for real-time push instead of polling": "changed to long-polling with a 25 second timeout",
				},
			},
		},
	}
}

// scenarioI: Weighted Expansion Ranking — an expansion result reached via a
// LOW-weight edge from a HIGH vector-relevance parent must still outrank one
// reached via a HIGH-weight edge from a LOW vector-relevance parent, because
// known-1so replaced raw edge.EffectiveWeight() with
// parentScore*edgeWeight*expansionDepthDecay. Under the old (pre-#39) weight-
// only formula this scenario inverts: the 1.0-weight edge from the weak
// parent (idx38, "Labels are stored...", weight 1.0) would outrank the
// 0.8-weight edge from the strong parent (idx24, rate limiting, weight 0.8) —
// the exact bug reported in known-1so. Probed against the real embedder
// (bench/cmd/seedgen): parent scores ~0.87 vs ~0.61 for this query, so
// 0.87*0.8 > 0.61*1.0 even though 0.8 < 1.0.
func scenarioI() Scenario {
	return Scenario{
		Name: "I: Weighted Expansion Ranking",
		Queries: []ScenarioQuery{
			{
				Text:        "What rate limiting algorithm does the API use?",
				Scope:       "project-alpha",
				Limit:       20,
				Threshold:   0.0,
				ExpandDepth: 1,
				MustIncludeContent: []string{
					"Office plants on the third floor are watered",
					"team lunch order rotation moves to a new restaurant",
				},
				MustRankAboveContent: map[string]string{
					"Office plants on the third floor are watered": "team lunch order rotation moves to a new restaurant",
				},
				ExpectReachContent: map[string]string{
					"Office plants on the third floor are watered":        "expansion",
					"team lunch order rotation moves to a new restaurant": "expansion",
				},
			},
		},
	}
}

// scenarioJ: Freshness / ObservedAt Preference — known-oj3 made freshness
// scoring prefer Freshness.ObservedAt over CreatedAt. The corpus pairs two
// near-duplicate facts (freshness-probe category) where raw vector
// similarity favors the STALE entry (spelled-out "six hours", observed 90
// days ago) over the CURRENT one (numeral "6 hours", observed 2 days ago).
// Only a nonzero RecencyWeight — driven by ObservedAt, not the uniform
// CreatedAt every seed entry gets at generation time — flips the ranking to
// favor the current fact. This is exactly what the "Freshness Weighting"
// ablation (DefaultAblations, forces Recency=0) is meant to catch, and
// previously had no query in the whole suite that exercised RecencyWeight>0.
func scenarioJ() Scenario {
	return Scenario{
		Name: "J: Freshness / ObservedAt Preference",
		Queries: []ScenarioQuery{
			{
				Text:        "How often are backups taken?",
				Scope:       "project-alpha.deploy",
				Limit:       10,
				Threshold:   0.0,
				Recency:     0.3,
				ExpandDepth: 1,
				MustIncludeContent: []string{
					"Backups run every six hours to S3",
					"Backups run every 6 hours to S3",
				},
				MustRankAboveContent: map[string]string{
					"Backups run every 6 hours to S3": "Backups run every six hours to S3",
				},
			},
		},
	}
}
