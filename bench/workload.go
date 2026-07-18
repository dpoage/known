//go:build bench

package bench

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/dpoage/known/embed"
	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// See the design decision on known-5qr for why this corpus is a
// deterministic synthetic generator rather than Go-stdlib-doc-derived: it
// keeps generation fully offline/hermetic, lets ground-truth relevance
// grades be derived programmatically from generation metadata instead of
// hand-labeled, and lets us bake in "hard distractor" documents that a
// weak (or strong) embedder can plausibly confuse with the target topic --
// which is what gives the resulting metrics headroom instead of saturating.

// workloadSeed is the fixed base seed for all corpus RNG draws. Changing it
// changes every generated document; the labeled_queries.json testdata file
// must be regenerated (via cmd/workloadgen) if it ever changes.
const workloadSeed = 20260718

// workloadReferenceTime is the fixed point all generated doc timestamps are
// relative to, matching the pattern in cmd/seedgen/main.go.
var workloadReferenceTime = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

// labelHorizon bounds how many leading docs BuildLabeledQueries considers.
// Because GenerateCorpus assigns doc i to topic (i % len(workloadTopics))
// and seeds its RNG from i alone (see docAt), doc i's content and edges are
// identical no matter how large n is. Capping labels at the first 1000
// docs means the same fixed label set stays valid ground truth for the
// 1K/5K/10K scale corpora alike.
const labelHorizon = 1000

// -----------------------------------------------------------------------
// Topic taxonomy
// -----------------------------------------------------------------------

// topicQuery is one hand-authored, natural-language query anchored to a
// specific fact within its topic. anchorFact indexes into topic.facts: a
// doc that used that exact fact when composing its content is graded
// higher than a doc that merely shares the topic.
type topicQuery struct {
	text       string
	anchorFact int
}

// topic describes one documentation domain used to synthesize topically
// coherent, technically concrete entry content. related lists other topic
// IDs that plausibly share vocabulary with this one -- used both to pick a
// "hard distractor" secondary topic for mixed docs and to grade
// topic-adjacent (but not on-topic) docs as marginally relevant.
type topic struct {
	id      string
	display string
	facts   []string
	queries []topicQuery
	related []string
}

var workloadTopics = []topic{
	{
		id: "auth", display: "authentication",
		facts: []string{
			"Session tokens are signed JWTs with a 15 minute expiry and a rotating HS256 secret.",
			"Failed login attempts are rate limited to 5 per minute per account before a temporary lockout.",
			"OAuth2 refresh tokens are stored hashed (SHA-256) in the sessions table, never in plaintext.",
			"Password resets require a one-time link that expires after 30 minutes and is single-use.",
			"Multi-factor auth via TOTP is enforced for any account with the admin role.",
			"The login endpoint returns a generic 401 for both unknown users and bad passwords to avoid enumeration.",
			"Service-to-service calls use mTLS client certificates instead of shared API keys.",
			"Account lockouts after repeated failures are cleared automatically after a 15 minute cooldown.",
		},
		queries: []topicQuery{
			{text: "how are session tokens signed and how long do they last", anchorFact: 0},
			{text: "what happens after repeated failed login attempts", anchorFact: 1},
		},
		related: []string{"secrets"},
	},
	{
		id: "secrets", display: "secrets management",
		facts: []string{
			"All API keys and database credentials are stored in Vault under the app/prod namespace.",
			"Secrets are injected into containers as environment variables at pod startup, never baked into images.",
			"Vault leases for dynamic database credentials are renewed every hour and revoked on pod termination.",
			"The secrets rotation job re-encrypts the KMS data key every 90 days.",
			"Local development uses a .env file that is explicitly gitignored and never committed.",
			"Access to the production Vault namespace requires a signed approval from two on-call engineers.",
			"Audit logs record every secret read, including the requesting service identity and timestamp.",
			"Compromised credentials are revoked immediately via the Vault admin API, not by waiting for rotation.",
		},
		queries: []topicQuery{
			{text: "where are api keys and database credentials stored", anchorFact: 0},
			{text: "how often are dynamic database credential leases renewed", anchorFact: 2},
		},
		related: []string{"auth"},
	},
	{
		id: "caching", display: "caching layer",
		facts: []string{
			"The read-through cache uses Redis with a default TTL of 5 minutes for entity lookups.",
			"Cache keys are namespaced by tenant ID to prevent cross-tenant data leakage.",
			"Writes invalidate the cache entry synchronously before the database transaction commits.",
			"A stampede guard uses single-flight locking so only one goroutine repopulates a hot key.",
			"The cache hit ratio dashboard alerts when hit rate drops below 80 percent for 10 minutes.",
			"Large objects over 1MB bypass the cache entirely and are served directly from storage.",
			"Cache eviction uses LRU with a memory ceiling of 4GB per Redis shard.",
			"A local in-process cache sits in front of Redis for the hottest 1000 keys to shave off network hops.",
		},
		queries: []topicQuery{
			{text: "what ttl and eviction policy does the read cache use", anchorFact: 0},
			{text: "how do we prevent a cache stampede on a hot key", anchorFact: 3},
		},
		related: []string{"connpool"},
	},
	{
		id: "connpool", display: "connection pooling",
		facts: []string{
			"The database connection pool caps at 20 connections per service instance.",
			"Idle connections are recycled after 10 minutes to avoid stale TCP sessions behind the load balancer.",
			"Pool exhaustion returns a fast-fail error after a 500ms acquire timeout instead of queuing indefinitely.",
			"Health checks borrow a connection every 30 seconds to detect a wedged pool early.",
			"Each pooled connection is validated with a lightweight SELECT 1 before being handed to a caller.",
			"Connection pool metrics (in-use, idle, wait time) are exported to Prometheus every 15 seconds.",
			"During a failover, the pool is drained and re-established against the new primary within 5 seconds.",
			"Long-running analytical queries use a separate, smaller pool so they cannot starve request-path queries.",
		},
		queries: []topicQuery{
			{text: "what happens when the connection pool is exhausted", anchorFact: 2},
			{text: "how many connections does the pool allow per instance", anchorFact: 0},
		},
		related: []string{"caching"},
	},
	{
		id: "migrations", display: "database migrations",
		facts: []string{
			"Schema migrations run automatically on deploy via a versioned migration table.",
			"Every migration must be backward compatible with the previous release to support rolling deploys.",
			"Destructive migrations (drop column, drop table) require a two-step deprecate-then-remove process.",
			"Migrations are wrapped in a single transaction where the database engine supports DDL transactions.",
			"A dry-run mode prints the generated SQL without applying it, used in CI to catch drift.",
			"Large backfills are chunked into batches of 10,000 rows to avoid long table locks.",
			"Migration files are named with a monotonic timestamp prefix to guarantee deterministic ordering.",
			"Rollback scripts are required for every migration before it can be merged.",
		},
		queries: []topicQuery{
			{text: "how are destructive schema changes like dropping a column handled", anchorFact: 2},
			{text: "how are large data backfills chunked during a migration", anchorFact: 5},
		},
		related: []string{"serialization"},
	},
	{
		id: "serialization", display: "data serialization",
		facts: []string{
			"Internal service-to-service payloads are encoded as protobuf with backward-compatible field numbers.",
			"Field deprecation reserves the field number instead of reusing it, to avoid silent misinterpretation.",
			"JSON is used only at the public API boundary; internal RPCs never use JSON for performance reasons.",
			"Enum values are never renumbered after release, only appended, to keep old clients compatible.",
			"Unknown fields are preserved on round-trip (not dropped) so proxies can forward messages untouched.",
			"Large binary blobs are stored out-of-band and referenced by a content hash, not inlined into the message.",
			"Schema compatibility is checked in CI using a bufbreaking check against the previous commit.",
			"Timestamps are always serialized as RFC3339 UTC strings, never as naive local time.",
		},
		queries: []topicQuery{
			{text: "how are protobuf fields deprecated without breaking old clients", anchorFact: 1},
			{text: "is json or protobuf used for internal service calls", anchorFact: 2},
		},
		related: []string{"migrations"},
	},
	{
		id: "logging", display: "logging",
		facts: []string{
			"All log lines are structured JSON with a mandatory trace_id field for correlation.",
			"Log level defaults to INFO in production and DEBUG in staging, controlled by an env var.",
			"Secrets and PII are redacted from log output by a field-name denylist applied at the logging middleware.",
			"Logs are shipped to the central pipeline via a sidecar, buffered locally if the shipper is unavailable.",
			"Log retention is 30 days hot storage, 1 year cold storage, per the compliance policy.",
			"Panic recovery logs the full stack trace at ERROR level before returning a 500 to the client.",
			"Sampling drops 90 percent of DEBUG-level lines in production to control ingestion cost.",
			"Each request gets a single correlation ID propagated through every downstream log line.",
		},
		queries: []topicQuery{
			{text: "how are secrets and pii redacted from logs", anchorFact: 2},
			{text: "what is the log retention policy", anchorFact: 4},
		},
		related: []string{"observability"},
	},
	{
		id: "observability", display: "observability",
		facts: []string{
			"Every service exports RED metrics (rate, errors, duration) to Prometheus with a 15 second scrape interval.",
			"Distributed traces use W3C traceparent propagation across every internal HTTP and gRPC hop.",
			"SLO dashboards track p50/p95/p99 latency per endpoint with a 30-day rolling window.",
			"Alert thresholds are defined as error budget burn rate, not raw error count, to avoid noisy paging.",
			"On-call runbooks are linked directly from each alert's annotation for faster triage.",
			"Synthetic canary requests probe the critical path every minute from three regions.",
			"Trace sampling is adaptive: 100 percent for errors, 1 percent for successful requests.",
			"A single pane-of-glass dashboard correlates deploy markers with latency and error-rate shifts.",
		},
		queries: []topicQuery{
			{text: "how is distributed tracing propagated across services", anchorFact: 1},
			{text: "how are alert thresholds defined to avoid noisy paging", anchorFact: 3},
		},
		related: []string{"logging"},
	},
	{
		id: "testing", display: "testing strategy",
		facts: []string{
			"Unit tests must run in under 2 minutes for the full suite to keep local iteration fast.",
			"Integration tests spin up a real Postgres via testcontainers instead of mocking the driver.",
			"Flaky tests are quarantined into a separate CI job and tracked with an owning issue, not deleted.",
			"Contract tests verify every public API response against its OpenAPI schema on every PR.",
			"Mutation testing runs nightly to catch assertions that pass regardless of implementation changes.",
			"Golden-file tests snapshot serialized output and fail loudly on any unreviewed diff.",
			"Test data builders use sensible defaults so each test only overrides the fields it cares about.",
			"Coverage is tracked but never gated on a hard percentage threshold, only on trend regressions.",
		},
		queries: []topicQuery{
			{text: "how are flaky tests handled instead of just deleting them", anchorFact: 2},
			{text: "do integration tests use a real database or a mock", anchorFact: 1},
		},
		related: []string{"circuitbreaker"},
	},
	{
		id: "circuitbreaker", display: "circuit breaking",
		facts: []string{
			"The circuit breaker trips open after 5 consecutive failures within a 10 second window.",
			"A half-open probe request is allowed every 30 seconds while the circuit is open.",
			"Breaker state transitions are logged and exported as a gauge metric per downstream dependency.",
			"Fallback responses return cached or default data rather than propagating the failure further upstream.",
			"Each downstream dependency gets its own independent breaker so one bad service can't cascade.",
			"The breaker's failure threshold is tuned per dependency based on its historical error rate.",
			"Manual override lets an on-call engineer force a breaker closed during a known false-positive incident.",
			"Breaker trips automatically page the owning team if the circuit stays open for more than 5 minutes.",
		},
		queries: []topicQuery{
			{text: "how many consecutive failures trip the circuit breaker open", anchorFact: 0},
			{text: "how does the half-open probe work while a circuit is open", anchorFact: 1},
		},
		related: []string{"testing"},
	},
	{
		id: "deployment", display: "deployment process",
		facts: []string{
			"Deploys use a canary rollout: 5 percent of traffic for 10 minutes before full promotion.",
			"Automatic rollback triggers if the error rate exceeds 2 percent above baseline during canary.",
			"Every deploy is tagged with the git SHA and visible in the deploy history dashboard.",
			"Database migrations always deploy one release ahead of the code that depends on the new schema.",
			"Feature branches deploy to an ephemeral preview environment for manual QA before merge.",
			"Blue-green deploys are used for services that cannot tolerate a rolling-restart blip.",
			"Deploy freezes are enforced automatically during declared incident windows.",
			"Rollbacks are a single command that repoints traffic to the previous known-good revision.",
		},
		queries: []topicQuery{
			{text: "what triggers an automatic rollback during a canary deploy", anchorFact: 1},
			{text: "how does the canary rollout percentage ramp up", anchorFact: 0},
		},
		related: []string{"servicediscovery"},
	},
	{
		id: "servicediscovery", display: "service discovery",
		facts: []string{
			"Services register themselves with Consul on startup and deregister on graceful shutdown.",
			"DNS-based discovery resolves service names to the current healthy instance set every 5 seconds.",
			"Stale registrations are pruned automatically if a heartbeat is missed for 3 consecutive intervals.",
			"Cross-region discovery falls back to the nearest healthy region if the local one has no capacity.",
			"Service metadata (version, region, capacity tags) is attached to each registration entry.",
			"Client-side load balancing picks from the discovery result set rather than routing through a proxy.",
			"A discovery outage degrades gracefully to the last-known-good cached instance list.",
			"New service versions are tagged in discovery so canary traffic can be routed by version tag.",
		},
		queries: []topicQuery{
			{text: "how do services register and deregister with discovery", anchorFact: 0},
			{text: "what happens to discovery during a cross-region outage", anchorFact: 3},
		},
		related: []string{"deployment"},
	},
	{
		id: "ratelimit", display: "rate limiting",
		facts: []string{
			"The public API enforces a token-bucket rate limit of 100 requests per minute per API key.",
			"Rate limit state is stored in Redis with a sliding window to avoid bucket-boundary bursts.",
			"Exceeding the limit returns HTTP 429 with a Retry-After header computed from the bucket refill rate.",
			"Internal service-to-service calls are exempt from the public rate limiter but have their own quota.",
			"Burst allowance permits a short spike up to 150 requests before throttling kicks in.",
			"Rate limit overrides can be granted per customer for approved high-volume integrations.",
			"Limiter metrics track the throttled-request rate per API key to spot abusive clients early.",
			"A shadow-mode limiter logs would-be throttles for two weeks before a new limit is enforced.",
		},
		queries: []topicQuery{
			{text: "what http status and header are returned when a client is rate limited", anchorFact: 2},
			{text: "how is the token bucket rate limit state stored", anchorFact: 1},
		},
		related: []string{"messagequeue"},
	},
	{
		id: "messagequeue", display: "message queue",
		facts: []string{
			"Async jobs are published to Kafka with a partition key derived from the tenant ID.",
			"Consumers commit offsets only after the message is fully processed, giving at-least-once delivery.",
			"A dead-letter topic captures messages that fail processing after 5 retry attempts.",
			"Consumer lag is monitored per partition and pages if it exceeds 10,000 messages for 5 minutes.",
			"Producers batch messages for up to 10ms to improve throughput without materially hurting latency.",
			"Schema evolution on queue messages follows the same backward-compatibility rules as serialization.",
			"Poison messages are quarantined rather than retried indefinitely, to avoid blocking the partition.",
			"Queue depth alerts are tuned per topic based on its expected steady-state throughput.",
		},
		queries: []topicQuery{
			{text: "what happens to a message that fails processing repeatedly", anchorFact: 2},
			{text: "how is consumer lag monitored on the message queue", anchorFact: 3},
		},
		related: []string{"ratelimit"},
	},
	{
		id: "concurrency", display: "concurrency control",
		facts: []string{
			"Optimistic locking uses a version column and rejects a write if the version has changed since read.",
			"Long-running background work is bounded by a worker pool sized to the available CPU cores.",
			"Goroutine leaks are caught in CI by a leak detector that runs after every integration test package.",
			"Shared mutable state is protected by narrowly scoped mutexes, never a single global lock.",
			"Context cancellation propagates through every downstream call so a client abort frees resources promptly.",
			"Idempotency keys deduplicate retried writes so a network retry can never double-apply a mutation.",
			"Fan-out work uses an errgroup so the first failure cancels the remaining in-flight goroutines.",
			"Deadlock-prone lock ordering is prevented by always acquiring locks in a fixed, documented order.",
		},
		queries: []topicQuery{
			{text: "how does optimistic locking detect a concurrent write conflict", anchorFact: 0},
			{text: "how are idempotency keys used to prevent double-applied retries", anchorFact: 5},
		},
		related: []string{"retries"},
	},
	{
		id: "retries", display: "retry policy",
		facts: []string{
			"Retries use exponential backoff with jitter, starting at 100ms and capping at 5 seconds.",
			"Only idempotent operations are retried automatically; mutating calls require an idempotency key first.",
			"A maximum of 3 retry attempts is allowed before the call fails and surfaces the error to the caller.",
			"Retry budgets cap the fraction of traffic that may be retries, to avoid amplifying an outage.",
			"Non-retryable errors (4xx except 429) fail fast without consuming a retry attempt.",
			"Client libraries expose retry configuration per call site rather than a single global default.",
			"Retries are logged with the attempt count so persistent failures are visible in traces.",
			"A retry storm is dampened by the same circuit breaker that protects the downstream dependency.",
		},
		queries: []topicQuery{
			{text: "what backoff strategy is used between retry attempts", anchorFact: 0},
			{text: "how many retry attempts are allowed before giving up", anchorFact: 2},
		},
		related: []string{"concurrency"},
	},
	{
		id: "healthcheck", display: "health checks",
		facts: []string{
			"The liveness probe checks process responsiveness only, never a downstream dependency.",
			"The readiness probe checks database connectivity and marks the pod unready if it fails twice in a row.",
			"Startup probes give slow-booting services up to 60 seconds before liveness checks begin.",
			"A degraded-but-serving state is reported separately from fully healthy, for partial outages.",
			"Health check endpoints are excluded from authentication so the orchestrator can always reach them.",
			"Deep health checks (checking every dependency) run on a separate, less frequent internal endpoint.",
			"A failed readiness probe removes the pod from the load balancer without restarting it.",
			"Health check history is retained for 24 hours to help diagnose flapping pods after the fact.",
		},
		queries: []topicQuery{
			{text: "what is the difference between the liveness and readiness probes", anchorFact: 1},
			{text: "what happens when a readiness probe fails", anchorFact: 6},
		},
		related: []string{"loadbalancing"},
	},
	{
		id: "loadbalancing", display: "load balancing",
		facts: []string{
			"The load balancer uses least-outstanding-requests to route traffic instead of round robin.",
			"Unhealthy backends are removed from rotation within one failed health check interval.",
			"Session affinity is avoided by design so any instance can serve any request statelessly.",
			"Weighted routing lets a canary release receive a small, explicitly configured traffic percentage.",
			"Connection draining gives an outgoing instance 30 seconds to finish in-flight requests before removal.",
			"Cross-zone load balancing is enabled but weighted to prefer same-zone backends for latency.",
			"Load balancer metrics export per-backend latency so a single slow instance is easy to spot.",
			"A sudden backend removal triggers automatic redistribution without dropping in-flight connections.",
		},
		queries: []topicQuery{
			{text: "what routing algorithm does the load balancer use", anchorFact: 0},
			{text: "how long does connection draining wait before removing a backend", anchorFact: 4},
		},
		related: []string{"healthcheck"},
	},
}

// topicByID indexes workloadTopics by id for O(1) lookup.
var topicByID = func() map[string]int {
	m := make(map[string]int, len(workloadTopics))
	for i, t := range workloadTopics {
		m[t.id] = i
	}
	return m
}()

// -----------------------------------------------------------------------
// Corpus generation
// -----------------------------------------------------------------------

// docSpec is one generated corpus entry, addressable by a stable Key that
// does not depend on the storage-assigned ULID, so ground-truth relevance
// labels in testdata/labeled_queries.json survive regeneration and scale
// changes.
type docSpec struct {
	Key         string
	Content     string
	Scope       string
	TopicID     string
	Mixed       bool   // true if this doc also draws facts from a second, related topic
	SecondaryID string // set only when Mixed
	UsedFacts   []int  // indices into the primary topic's facts actually used
	DaysAgo     int
	Labels      []string
}

// edgeSpec is one generated corpus edge, referencing docs by Key.
type edgeSpec struct {
	fromKey string
	toKey   string
	typ     model.EdgeType
}

// Corpus is a deterministic, self-contained benchmark workload: entries,
// edges, and the scopes they live in, plus a lookup from doc key to entry
// index for tooling convenience.
type Corpus struct {
	Docs   []docSpec
	Edges  []edgeSpec
	Scopes []string
}

func docKey(i int) string {
	return fmt.Sprintf("doc-%05d", i)
}

// introTemplates give doc content some lexical variety beyond the raw
// facts, mimicking the range of real internal documentation genres.
var introTemplates = []string{
	"Runbook note on %s: %s",
	"Incident review, %s subsystem -- %s",
	"Design doc excerpt (%s): %s",
	"On-call handoff notes, %s: %s",
	"Architecture decision record, %s: %s",
	"Postmortem follow-up for %s: %s",
}

// GenerateCorpus deterministically builds a corpus of n documents plus
// their edges and scopes. Every document's content, scope, and edges are a
// pure function of its index and workloadSeed -- generating n=10000 and
// then looking at doc 500 yields byte-identical output to generating
// n=1000 and looking at doc 500. This is what lets a single fixed labeled
// query set stay valid ground truth across every corpus scale.
func GenerateCorpus(n int) Corpus {
	docs := make([]docSpec, n)
	for i := range n {
		docs[i] = docAt(i)
	}

	var edges []edgeSpec
	numTopics := len(workloadTopics)
	for i := range n {
		d := &docs[i]
		// Same-topic edge: the previous occurrence of this doc's topic is
		// always exactly numTopics positions back, by construction of
		// docAt's topic assignment (i % numTopics).
		if i >= numTopics {
			edges = append(edges, edgeSpec{fromKey: d.Key, toKey: docKey(i - numTopics), typ: model.EdgeElaborates})
		}
		// Related-topic edge for mixed (distractor) docs: link to the
		// nearest earlier occurrence of the secondary topic.
		if d.Mixed {
			if j, ok := nearestEarlierTopicDoc(i, d.SecondaryID, numTopics); ok {
				edges = append(edges, edgeSpec{fromKey: d.Key, toKey: docKey(j), typ: model.EdgeRelatedTo})
			}
		}
	}

	scopeSet := make(map[string]struct{}, numTopics+1)
	scopeSet["workload"] = struct{}{}
	for _, t := range workloadTopics {
		scopeSet["workload."+t.id] = struct{}{}
	}
	scopes := make([]string, 0, len(scopeSet))
	for s := range scopeSet {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)

	return Corpus{Docs: docs, Edges: edges, Scopes: scopes}
}

// nearestEarlierTopicDoc returns the largest index j < i such that doc j's
// primary topic is topicID, using the closed-form (i % numTopics) topic
// assignment -- no scan required, so the result is stable regardless of
// corpus size.
func nearestEarlierTopicDoc(i int, topicID string, numTopics int) (int, bool) {
	idx, ok := topicByID[topicID]
	if !ok {
		return 0, false
	}
	base := i - (i % numTopics) + idx
	if base >= i {
		base -= numTopics
	}
	if base < 0 {
		return 0, false
	}
	return base, true
}

// docAt deterministically generates the document at index i. Every random
// draw is seeded from (workloadSeed, i) alone, never from shared state, so
// the result does not depend on how many total documents are generated.
func docAt(i int) docSpec {
	numTopics := len(workloadTopics)
	primary := workloadTopics[i%numTopics]
	r := rand.New(rand.NewPCG(uint64(workloadSeed), uint64(i)))

	// Roughly one doc in six is a "mixed" hard distractor that also draws
	// facts from a related topic.
	mixed := len(primary.related) > 0 && i%6 == 5
	var secondary topic
	if mixed {
		secondary = workloadTopics[topicByID[primary.related[r.IntN(len(primary.related))]]]
	}

	factCount := 3 + r.IntN(2) // 3 or 4 primary facts
	usedFacts := r.Perm(len(primary.facts))[:factCount]
	sort.Ints(usedFacts)

	var sb strings.Builder
	for idx, fi := range usedFacts {
		if idx > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(primary.facts[fi])
	}
	if mixed {
		secFacts := r.Perm(len(secondary.facts))[:1+r.IntN(2)]
		for _, fi := range secFacts {
			sb.WriteByte(' ')
			sb.WriteString(secondary.facts[fi])
		}
	}

	tmpl := introTemplates[r.IntN(len(introTemplates))]
	body := fmt.Sprintf(tmpl, primary.display, sb.String())
	content := fmt.Sprintf("%s (ref RB-%05d)", body, i)
	if len(content) > model.MaxContentLength {
		content = content[:model.MaxContentLength]
	}

	labels := []string{primary.id}
	scope := "workload." + primary.id
	secondaryID := ""
	if mixed {
		secondaryID = secondary.id
		labels = append(labels, secondary.id, "distractor")
	}

	return docSpec{
		Key:         docKey(i),
		Content:     content,
		Scope:       scope,
		TopicID:     primary.id,
		Mixed:       mixed,
		SecondaryID: secondaryID,
		UsedFacts:   usedFacts,
		DaysAgo:     r.IntN(180),
		Labels:      labels,
	}
}

// -----------------------------------------------------------------------
// Labeled queries
// -----------------------------------------------------------------------

// LabeledQuery is one graded-relevance ground-truth query, keyed by the
// corpus's stable doc Keys (not storage-assigned ULIDs) so it survives
// regeneration. This is the JSON shape persisted to
// bench/testdata/labeled_queries.json by cmd/workloadgen.
type LabeledQuery struct {
	Query     string         `json:"query"`
	TopicID   string         `json:"topic_id"`
	Judgments map[string]int `json:"judgments"` // doc key -> graded relevance
}

// BuildLabeledQueries derives graded relevance judgments for every
// topicQuery in workloadTopics against the leading labelHorizon documents
// of corpus (see labelHorizon's doc comment for why the horizon is fixed).
//
// Deliberately NOT graded: merely sharing a topic with the query. An
// earlier version granted grade 2 to every same-topic doc regardless of
// content, which a strong embedder saturates trivially -- ~1/18th of the
// corpus is "the right topic" and any competent model finds one in the
// top 5 every time, making P@5 and MRR read ~1.000 against the real
// hugot embedder (see known-5qr notes). Relevance now requires the doc to
// actually contain the fact the query asks about, matching how a real
// user query is answered by a specific passage, not by "the right
// general area". Grades:
//
//	3 - same topic AND contains the query's anchor fact, in a focused
//	    (non-mixed) doc
//	2 - same topic AND contains the query's anchor fact, but the doc is a
//	    mixed/distractor doc whose content is diluted with a second topic
//	1 - a mixed/distractor doc naming the query's topic as its secondary
//	    topic (a plausible false positive), or a doc from a topic-adjacent
//	    (`related`) topic
//	0 - everything else, INCLUDING same-topic docs that never mention the
//	    anchor fact (omitted from the Judgments map)
func BuildLabeledQueries(corpus Corpus) []LabeledQuery {
	docs := corpus.Docs
	if len(docs) > labelHorizon {
		docs = docs[:labelHorizon]
	}

	var out []LabeledQuery
	for _, t := range workloadTopics {
		relatedSet := make(map[string]struct{}, len(t.related))
		for _, r := range t.related {
			relatedSet[r] = struct{}{}
		}
		for _, q := range t.queries {
			judgments := make(map[string]int)
			for _, d := range docs {
				switch {
				case d.TopicID == t.id && containsInt(d.UsedFacts, q.anchorFact):
					if d.Mixed {
						judgments[d.Key] = 2
					} else {
						judgments[d.Key] = 3
					}
				case d.Mixed && d.SecondaryID == t.id:
					judgments[d.Key] = 1
				default:
					if _, ok := relatedSet[d.TopicID]; ok {
						judgments[d.Key] = 1
					}
				}
			}
			out = append(out, LabeledQuery{Query: q.text, TopicID: t.id, Judgments: judgments})
		}
	}
	return out
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------
// Storage population
// -----------------------------------------------------------------------

// PopulateCorpus creates every scope, entry, and edge in corpus against
// be, embedding entry content with embedder. It mirrors the population
// pattern in cmd/seedgen/main.go (batch-embed, then create), and returns a
// map from each doc's stable Key to its storage-assigned model.ID so
// callers can translate LabeledQuery judgments (keyed by doc Key) into
// Judgments (keyed by ID string) for use with metrics.go.
func PopulateCorpus(ctx context.Context, be storage.Backend, corpus Corpus, embedder embed.Embedder) (map[string]model.ID, error) {
	for _, path := range corpus.Scopes {
		s := model.NewScope(path)
		if err := be.Scopes().Upsert(ctx, &s); err != nil {
			return nil, fmt.Errorf("upsert scope %q: %w", path, err)
		}
	}

	texts := make([]string, len(corpus.Docs))
	for i, d := range corpus.Docs {
		texts[i] = d.Content
	}
	vectors, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed corpus: %w", err)
	}
	if len(vectors) != len(corpus.Docs) {
		return nil, fmt.Errorf("embedder returned %d vectors for %d docs", len(vectors), len(corpus.Docs))
	}

	modelName := embedder.ModelName()
	src := model.Source{Type: model.SourceManual, Reference: "workloadgen"}
	ids := make(map[string]model.ID, len(corpus.Docs))

	for i, d := range corpus.Docs {
		e := model.NewEntry(d.Content, src).
			WithScope(d.Scope).
			WithEmbedding(vectors[i], modelName).
			WithFreshness(model.Freshness{
				ObservedAt: workloadReferenceTime.AddDate(0, 0, -d.DaysAgo),
				ObservedBy: "workloadgen",
			}).
			WithProvenance(model.Provenance{Level: model.ProvenanceInferred}).
			WithLabels(d.Labels)

		if err := be.Entries().Create(ctx, &e); err != nil {
			return nil, fmt.Errorf("create entry %s: %w", d.Key, err)
		}
		ids[d.Key] = e.ID
	}

	for _, es := range corpus.Edges {
		fromID, ok := ids[es.fromKey]
		if !ok {
			return nil, fmt.Errorf("edge references unknown doc key %q", es.fromKey)
		}
		toID, ok := ids[es.toKey]
		if !ok {
			return nil, fmt.Errorf("edge references unknown doc key %q", es.toKey)
		}
		edge := model.NewEdge(fromID, toID, es.typ)
		if err := be.Edges().Create(ctx, &edge); err != nil {
			return nil, fmt.Errorf("create edge %s->%s: %w", es.fromKey, es.toKey, err)
		}
	}

	return ids, nil
}

// -----------------------------------------------------------------------
// FakeEmbedder: deterministic, model-free embed.Embedder for hermetic runs
// -----------------------------------------------------------------------

// FakeEmbedder is a deterministic, model-free embed.Embedder used for
// hermetic benchmark and quality-eval runs. It hashes whitespace-tokenized,
// lowercased words into a fixed-size vector using the hashing trick (a la
// scikit-learn's HashingVectorizer) with a hashed sign bit, then
// L2-normalizes. Being a pure function of the input text, it needs no
// network call, model cache, or API key, and produces byte-identical
// output on every machine and every run.
//
// Cosine similarity over these vectors approximates lexical (bag-of-words)
// overlap -- much weaker than a real semantic embedder, but structured
// enough to produce a genuinely non-random ranking, which is exactly what
// the metrics and discrimination self-tests need to be meaningful
// hermetically.
type FakeEmbedder struct {
	dim int
}

// NewFakeEmbedder returns a FakeEmbedder producing dim-dimensional vectors.
func NewFakeEmbedder(dim int) *FakeEmbedder {
	return &FakeEmbedder{dim: dim}
}

// Dimensions implements embed.Embedder.
func (f *FakeEmbedder) Dimensions() int { return f.dim }

// ModelName implements embed.Embedder.
func (f *FakeEmbedder) ModelName() string { return "fake-hashing-v1" }

// Embed implements embed.Embedder.
func (f *FakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return hashEmbed(text, f.dim), nil
}

// EmbedBatch implements embed.Embedder.
func (f *FakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = hashEmbed(t, f.dim)
	}
	return out, nil
}

// hashEmbed computes a deterministic, L2-normalized hashing-trick
// bag-of-words vector for text.
func hashEmbed(text string, dim int) []float32 {
	acc := make([]float64, dim)
	for _, tok := range tokenize(text) {
		idx := int(fnvHash(tok) % uint32(dim))
		sign := 1.0
		if fnvHash(tok+"#sign")%2 == 0 {
			sign = -1.0
		}
		acc[idx] += sign
	}
	var norm float64
	for _, v := range acc {
		norm += v * v
	}
	out := make([]float32, dim)
	if norm == 0 {
		return out
	}
	norm = math.Sqrt(norm)
	for i, v := range acc {
		out[i] = float32(v / norm)
	}
	return out
}

func fnvHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// tokenize lowercases text and splits it into runs of letters/digits.
func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
