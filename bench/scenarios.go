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
func scenarioB() Scenario {
	return Scenario{
		Name: "B: Contradiction Resolution",
		Queries: []ScenarioQuery{
			{
				// Rate limiter contradiction
				Text:      "What rate limiting algorithm does the API use?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				Recency:   0.5, // high recency to prefer newer entry
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
				// Auth token format contradiction
				Text:      "What format are authentication tokens?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				Recency:   0.5,
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
				// Storage backend contradiction
				Text:      "What is the primary storage backend?",
				Scope:     "project-alpha",
				Limit:     10,
				Threshold: 0.0,
				Recency:   0.5,
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

// scenarioD: Needle-in-Haystack with Graph — graph expansion surfaces linked entries.
func scenarioD() Scenario {
	return Scenario{
		Name: "D: Needle-in-Haystack with Graph",
		Queries: []ScenarioQuery{
			{
				// Query JWT auth; with expansion, elaboration chain entries should appear.
				// Entry 56: "JWT authentication flow..." has elaborations:
				//   57 -> 56 (elaborates), 58 -> 57 (elaborates)
				// Also 20 -> 1 (JWT expiration elaborates auth architecture)
				Text:        "JWT authentication flow and token handling",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 2,
				MustIncludeContent: []string{
					"JWT authentication flow: client sends credentials",
					"RSA-2048 key pair stored in KNOWN_JWT_PRIVATE_KEY",
				},
				ExpectReachContent: map[string]string{
					"JWT authentication flow: client sends credentials": "direct",
				},
			},
			{
				// Query vector search; expansion should find elaboration chain 59->60->61
				Text:        "How does vector similarity search work?",
				Scope:       "project-alpha",
				Limit:       10,
				Threshold:   0.0,
				ExpandDepth: 2,
				MustIncludeContent: []string{
					"Vector similarity search first filters entries by scope",
					"Cosine similarity is computed as 1 minus the cosine distance",
				},
				ExpectReachContent: map[string]string{
					"Vector similarity search first filters entries by scope": "direct",
				},
			},
			{
				// Without expansion, elaboration targets should NOT appear as easily.
				Text:        "JWT authentication flow and token handling",
				Scope:       "project-alpha",
				Limit:       5,
				Threshold:   0.0,
				ExpandDepth: 0,
				MustIncludeContent: []string{
					"JWT authentication flow: client sends credentials",
				},
			},
		},
	}
}

// scenarioE: FTS Rescue — text search finds keyword-specific needle entries.
func scenarioE() Scenario {
	return Scenario{
		Name: "E: FTS Rescue",
		Queries: []ScenarioQuery{
			{
				Text:       "ALPHA-4091",
				Scope:      "project-alpha",
				Limit:      10,
				TextSearch: true,
				MustIncludeContent: []string{
					"ALPHA-4091 indicates embedding dimension mismatch",
				},
			},
			{
				Text:       "ALPHA-5002",
				Scope:      "project-alpha",
				Limit:      10,
				TextSearch: true,
				MustIncludeContent: []string{
					"ALPHA-5002 means the scope path contains invalid characters",
				},
			},
			{
				Text:       "embed.cache.maxsize",
				Scope:      "project-alpha",
				Limit:      10,
				TextSearch: true,
				MustIncludeContent: []string{
					"embed.cache.maxsize controls the maximum number of cached",
				},
			},
			{
				Text:       "KALPHA trace ID format",
				Scope:      "project-alpha",
				Limit:      10,
				TextSearch: true,
				MustIncludeContent: []string{
					"KALPHA-{timestamp}-{random6}",
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
