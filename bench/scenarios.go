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

	// Expected outcomes — content substrings to identify entries
	MustIncludeContent      []string          // Content substrings that MUST appear in results
	MustExcludeContent      []string          // Content substrings that MUST NOT appear
	MustRankAboveContent    map[string]string  // content_higher -> content_lower
	MustFlagConflictContent []string           // Content substrings that must have HasConflict=true
	ExpectReachContent      map[string]string  // Content substring -> expected ReachMethod
}

// AblationConfig controls which features are disabled for an ablation run.
type AblationConfig struct {
	Name                  string
	DisableGraphExpansion bool // force ExpandDepth=0
	DisableTextSearch     bool // skip text search in hybrid
	DisableFreshness      bool // force RecencyWeight=0
	DisableScoping        bool // force Scope=""
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
// Tests that contradicts edges cause HasConflict=true on results. Ranking by
// recency is tested separately in the freshness ablation; here we only check
// that both entries appear and both are flagged.
func scenarioB() Scenario {
	return Scenario{
		Name: "B: Contradiction Resolution",
		Queries: []ScenarioQuery{
			{
				Text:      "What rate limiting algorithm does the API use?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"fixed-window algorithm with 60-second windows",
					"changed from fixed-window to token-bucket in v2.1",
				},
				MustFlagConflictContent: []string{
					"fixed-window algorithm with 60-second windows",
					"changed from fixed-window to token-bucket in v2.1",
				},
			},
			{
				Text:      "What format are authentication tokens?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"opaque session IDs stored in Redis",
					"migrated from opaque session tokens to stateless JWTs",
				},
				MustFlagConflictContent: []string{
					"opaque session IDs stored in Redis",
					"migrated from opaque session tokens to stateless JWTs",
				},
			},
			{
				Text:      "What is the primary storage backend?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				MustIncludeContent: []string{
					"PostgreSQL with pgvector for similarity search",
					"migrated from PostgreSQL to SQLite",
				},
				MustFlagConflictContent: []string{
					"PostgreSQL with pgvector for similarity search",
					"migrated from PostgreSQL to SQLite",
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
				// With FTS enabled (hybrid), the correct entry should be found.
				Text:       "ALPHA-4091",
				Scope:      "project-alpha",
				Limit:      5,
				TextSearch: true,
				MustIncludeContent: []string{
					"ALPHA-4091 indicates embedding dimension mismatch",
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
