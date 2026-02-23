package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// EntryStore implements storage.EntryRepo using PostgreSQL with pgvector.
type EntryStore struct {
	pool *pgxpool.Pool
}

// conn returns the transaction-aware connection for this store.
func (s *EntryStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.pool)
}

// withTx runs fn within a transaction using the store's pool.
func (s *EntryStore) withTx(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if err := fn(txCtx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// entryColumns is the standard column list for entry queries.
const entryColumns = `
	id, title, content, content_hash, embedding, embedding_dim, embedding_model,
	source_type, source_ref, source_meta,
	confidence,
	scope, ttl_seconds, expires_at,
	meta, version, created_at, updated_at,
	observed_at, observed_by, source_hash
`

// Create persists a new entry. It automatically ensures the scope hierarchy exists.
// If not already within a transaction, wraps both operations in one for atomicity.
func (s *EntryStore) Create(ctx context.Context, entry *model.Entry) error {
	// If not already in a transaction, wrap in one to keep EnsureHierarchy
	// and the entry insert atomic.
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); !ok {
		return s.withTx(ctx, func(txCtx context.Context) error {
			return s.createInner(txCtx, entry)
		})
	}
	return s.createInner(ctx, entry)
}

func (s *EntryStore) createInner(ctx context.Context, entry *model.Entry) error {
	// Ensure content hash is set.
	if entry.ContentHash == "" {
		entry.ContentHash = model.ComputeContentHash(entry.Content)
	}
	if entry.Version == 0 {
		entry.Version = 1
	}

	// Auto-upsert the scope hierarchy.
	scopeStore := &ScopeStore{pool: s.pool}
	if err := scopeStore.EnsureHierarchy(ctx, entry.Scope); err != nil {
		return fmt.Errorf("ensure scope hierarchy: %w", err)
	}

	metaJSON, err := marshalNullableJSON(entry.Meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	sourceMetaJSON, err := marshalNullableJSON(entry.Source.Meta)
	if err != nil {
		return fmt.Errorf("marshal source meta: %w", err)
	}

	var embeddingVal any
	if len(entry.Embedding) > 0 {
		embeddingVal = pgvector.NewVector(entry.Embedding)
	}

	var ttlSeconds *int64
	if entry.TTL != nil {
		secs := int64(entry.TTL.Duration.Seconds())
		ttlSeconds = &secs
	}

	_, err = s.conn(ctx).Exec(ctx, `
		INSERT INTO entries (
			id, title, content, content_hash, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence,
			scope, ttl_seconds, expires_at,
			meta, version, created_at, updated_at,
			observed_at, observed_by, source_hash
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10,
			$11,
			$12, $13, $14,
			$15, $16, $17, $18,
			$19, $20, $21
		)
	`,
		entry.ID.String(), entry.Title, entry.Content, entry.ContentHash,
		embeddingVal, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Provenance.Level),
		entry.Scope, ttlSeconds, entry.ExpiresAt,
		metaJSON, entry.Version, entry.CreatedAt, entry.UpdatedAt,
		entry.Freshness.ObservedAt, nullableString(entry.Freshness.ObservedBy), nullableString(entry.Freshness.SourceHash),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %s", storage.ErrDuplicateContent, pgErr.Detail)
		}
		return fmt.Errorf("create entry: %w", err)
	}
	return nil
}

// CreateOrUpdate inserts a new entry or updates an existing one with the same
// content hash and scope. Uses ON CONFLICT (content_hash, scope) for idempotent upserts.
// If not already within a transaction, wraps both operations in one for atomicity.
func (s *EntryStore) CreateOrUpdate(ctx context.Context, entry *model.Entry) (*model.Entry, error) {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); !ok {
		var result *model.Entry
		err := s.withTx(ctx, func(txCtx context.Context) error {
			var innerErr error
			result, innerErr = s.createOrUpdateInner(txCtx, entry)
			return innerErr
		})
		return result, err
	}
	return s.createOrUpdateInner(ctx, entry)
}

func (s *EntryStore) createOrUpdateInner(ctx context.Context, entry *model.Entry) (*model.Entry, error) {
	// Ensure content hash is set.
	if entry.ContentHash == "" {
		entry.ContentHash = model.ComputeContentHash(entry.Content)
	}
	if entry.Version == 0 {
		entry.Version = 1
	}

	// Auto-upsert the scope hierarchy.
	scopeStore := &ScopeStore{pool: s.pool}
	if err := scopeStore.EnsureHierarchy(ctx, entry.Scope); err != nil {
		return nil, fmt.Errorf("ensure scope hierarchy: %w", err)
	}

	metaJSON, err := marshalNullableJSON(entry.Meta)
	if err != nil {
		return nil, fmt.Errorf("marshal meta: %w", err)
	}
	sourceMetaJSON, err := marshalNullableJSON(entry.Source.Meta)
	if err != nil {
		return nil, fmt.Errorf("marshal source meta: %w", err)
	}

	var embeddingVal any
	if len(entry.Embedding) > 0 {
		embeddingVal = pgvector.NewVector(entry.Embedding)
	}

	var ttlSeconds *int64
	if entry.TTL != nil {
		secs := int64(entry.TTL.Duration.Seconds())
		ttlSeconds = &secs
	}

	row := s.conn(ctx).QueryRow(ctx, `
		INSERT INTO entries (
			id, title, content, content_hash, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence,
			scope, ttl_seconds, expires_at,
			meta, version, created_at, updated_at,
			observed_at, observed_by, source_hash
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10,
			$11,
			$12, $13, $14,
			$15, $16, $17, $18,
			$19, $20, $21
		)
		ON CONFLICT (content_hash, scope) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding,
			embedding_dim = EXCLUDED.embedding_dim,
			embedding_model = EXCLUDED.embedding_model,
			source_type = EXCLUDED.source_type,
			source_ref = EXCLUDED.source_ref,
			source_meta = EXCLUDED.source_meta,
			confidence = EXCLUDED.confidence,
			observed_at = EXCLUDED.observed_at,
			observed_by = EXCLUDED.observed_by,
			source_hash = EXCLUDED.source_hash,
			ttl_seconds = EXCLUDED.ttl_seconds,
			expires_at = EXCLUDED.expires_at,
			meta = EXCLUDED.meta,
			version = entries.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING `+entryColumns,
		entry.ID.String(), entry.Title, entry.Content, entry.ContentHash,
		embeddingVal, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Provenance.Level),
		entry.Scope, ttlSeconds, entry.ExpiresAt,
		metaJSON, entry.Version, entry.CreatedAt, entry.UpdatedAt,
		entry.Freshness.ObservedAt, nullableString(entry.Freshness.ObservedBy), nullableString(entry.Freshness.SourceHash),
	)

	result, err := scanEntryV2(row)
	if err != nil {
		return nil, fmt.Errorf("create or update entry: %w", err)
	}
	return result, nil
}

// Get retrieves an entry by ID.
func (s *EntryStore) Get(ctx context.Context, id model.ID) (*model.Entry, error) {
	row := s.conn(ctx).QueryRow(ctx, `
		SELECT `+entryColumns+`
		FROM entries
		WHERE id = $1
	`, id.String())

	entry, err := scanEntryV2(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get entry: %w", err)
	}
	return entry, nil
}

// Update replaces an existing entry with optimistic concurrency control.
// The update only succeeds if the entry's version matches. On success,
// the version is incremented both in the database and on the entry struct.
func (s *EntryStore) Update(ctx context.Context, entry *model.Entry) error {
	// Recompute content hash in case content changed.
	entry.ContentHash = model.ComputeContentHash(entry.Content)

	// Auto-create scope hierarchy if the scope changed.
	scopeStore := &ScopeStore{pool: s.pool}
	if err := scopeStore.EnsureHierarchy(ctx, entry.Scope); err != nil {
		return fmt.Errorf("ensure scope hierarchy: %w", err)
	}

	metaJSON, err := marshalNullableJSON(entry.Meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	sourceMetaJSON, err := marshalNullableJSON(entry.Source.Meta)
	if err != nil {
		return fmt.Errorf("marshal source meta: %w", err)
	}

	var embeddingVal any
	if len(entry.Embedding) > 0 {
		embeddingVal = pgvector.NewVector(entry.Embedding)
	}

	var ttlSeconds *int64
	if entry.TTL != nil {
		secs := int64(entry.TTL.Duration.Seconds())
		ttlSeconds = &secs
	}

	// Use RETURNING to detect a successful update in a single round-trip.
	var newVersion int
	err = s.conn(ctx).QueryRow(ctx, `
		UPDATE entries SET
			title = $2,
			content = $3,
			content_hash = $4,
			embedding = $5,
			embedding_dim = $6,
			embedding_model = $7,
			source_type = $8,
			source_ref = $9,
			source_meta = $10,
			confidence = $11,
			scope = $12,
			ttl_seconds = $13,
			expires_at = $14,
			meta = $15,
			version = $16 + 1,
			updated_at = $17,
			observed_at = $18,
			observed_by = $19,
			source_hash = $20
		WHERE id = $1 AND version = $16
		RETURNING version
	`,
		entry.ID.String(), entry.Title, entry.Content, entry.ContentHash,
		embeddingVal, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Provenance.Level),
		entry.Scope, ttlSeconds, entry.ExpiresAt,
		metaJSON, entry.Version, entry.UpdatedAt,
		entry.Freshness.ObservedAt, nullableString(entry.Freshness.ObservedBy), nullableString(entry.Freshness.SourceHash),
	).Scan(&newVersion)

	if err == nil {
		// Update succeeded — set the new version on the struct.
		entry.Version = newVersion
		return nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("update entry: %w", err)
	}

	// Zero rows returned. Distinguish not-found from version mismatch.
	var actualVersion int
	probeErr := s.conn(ctx).QueryRow(ctx, `SELECT version FROM entries WHERE id = $1`, entry.ID.String()).Scan(&actualVersion)
	if errors.Is(probeErr, pgx.ErrNoRows) {
		return storage.ErrNotFound
	}
	if probeErr != nil {
		return fmt.Errorf("probe entry version: %w", probeErr)
	}
	return &storage.ConcurrentModificationError{
		ID:              entry.ID,
		ExpectedVersion: entry.Version,
		ActualVersion:   actualVersion,
	}
}

// Delete removes an entry by ID.
func (s *EntryStore) Delete(ctx context.Context, id model.ID) error {
	tag, err := s.conn(ctx).Exec(ctx, `DELETE FROM entries WHERE id = $1`, id.String())
	if err != nil {
		return fmt.Errorf("delete entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// List returns entries matching the given filter.
func (s *EntryStore) List(ctx context.Context, filter storage.EntryFilter) ([]model.Entry, error) {
	query := `
		SELECT ` + entryColumns + `
		FROM entries
		WHERE 1=1
	`
	args := make([]any, 0)
	argIdx := 1

	if filter.Scope != "" {
		query += fmt.Sprintf(" AND scope = $%d", argIdx)
		args = append(args, filter.Scope)
		argIdx++
	}

	if filter.ScopePrefix != "" {
		// Match the exact scope or any scope that starts with the prefix followed by a dot
		query += fmt.Sprintf(" AND (scope = $%d OR scope LIKE $%d)", argIdx, argIdx+1)
		args = append(args, filter.ScopePrefix, filter.ScopePrefix+".%")
		argIdx += 2
	}

	if filter.SourceType != "" {
		query += fmt.Sprintf(" AND source_type = $%d", argIdx)
		args = append(args, string(filter.SourceType))
		argIdx++
	}

	if filter.ProvenanceLevel != "" {
		query += fmt.Sprintf(" AND confidence = $%d", argIdx)
		args = append(args, string(filter.ProvenanceLevel))
		argIdx++
	}

	if filter.StalerThan > 0 {
		query += fmt.Sprintf(" AND observed_at < NOW() - $%d::interval", argIdx)
		args = append(args, filter.StalerThan.String())
		argIdx++
	}

	if !filter.IncludeExpired {
		query += " AND (expires_at IS NULL OR expires_at > NOW())"
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}

	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
		argIdx++
	}

	rows, err := s.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	var entries []model.Entry
	for rows.Next() {
		entry, err := scanEntryFromRowsV2(rows)
		if err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		entries = append(entries, *entry)
	}
	return entries, rows.Err()
}

// SearchSimilar finds entries with embeddings similar to the query vector.
func (s *EntryStore) SearchSimilar(ctx context.Context, query []float32, scope string, metric storage.SimilarityMetric, limit int) ([]storage.SimilarityResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query vector must not be empty")
	}
	if limit <= 0 {
		limit = 10
	}

	vec := pgvector.NewVector(query)

	// Select distance operator based on metric
	var distExpr string
	switch metric {
	case storage.Cosine:
		distExpr = "embedding <=> $1::vector"
	case storage.L2:
		distExpr = "embedding <-> $1::vector"
	case storage.InnerProduct:
		distExpr = "embedding <#> $1::vector"
	default:
		distExpr = "embedding <=> $1::vector"
	}

	sqlQuery := fmt.Sprintf(`
		SELECT
			id, title, content, content_hash, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence,
			scope, ttl_seconds, expires_at,
			meta, version, created_at, updated_at,
			observed_at, observed_by, source_hash,
			%s AS distance
		FROM entries
		WHERE embedding IS NOT NULL
		  AND embedding_dim = $2
		  AND (scope = $3 OR scope LIKE $4)
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY %s
		LIMIT $5
	`, distExpr, distExpr)

	rows, err := s.conn(ctx).Query(ctx, sqlQuery, vec, len(query), scope, scope+".%", limit)
	if err != nil {
		return nil, fmt.Errorf("search similar: %w", err)
	}
	defer rows.Close()

	var results []storage.SimilarityResult
	for rows.Next() {
		var (
			entry       model.Entry
			idStr       string
			contentHash string
			embVec      pgvector.Vector
			embDim      *int
			embMod      *string
			srcType     string
			srcRef      string
			srcMeta     []byte
			conf        string
			ttlSecs     *int64
			metaJ       []byte
			version     int
			observedAt  time.Time
			observedBy  *string
			sourceHash  *string
			dist        float64
		)

		if err := rows.Scan(
			&idStr, &entry.Title, &entry.Content, &contentHash, &embVec, &embDim, &embMod,
			&srcType, &srcRef, &srcMeta,
			&conf,
			&entry.Scope, &ttlSecs, &entry.ExpiresAt,
			&metaJ, &version, &entry.CreatedAt, &entry.UpdatedAt,
			&observedAt, &observedBy, &sourceHash,
			&dist,
		); err != nil {
			return nil, fmt.Errorf("scan similarity result: %w", err)
		}

		id, err := model.ParseID(idStr)
		if err != nil {
			return nil, fmt.Errorf("parse id: %w", err)
		}
		entry.ID = id
		entry.ContentHash = contentHash
		entry.Version = version
		entry.Embedding = embVec.Slice()
		if embDim != nil {
			entry.EmbeddingDim = *embDim
		}
		if embMod != nil {
			entry.EmbeddingModel = *embMod
		}
		entry.Source = model.Source{Type: model.SourceType(srcType), Reference: srcRef}
		if err := unmarshalNullableJSON(srcMeta, &entry.Source.Meta); err != nil {
			return nil, fmt.Errorf("unmarshal source meta: %w", err)
		}
		entry.Provenance.Level = model.ProvenanceLevel(conf)
		entry.Freshness.ObservedAt = observedAt
		if observedBy != nil {
			entry.Freshness.ObservedBy = *observedBy
		}
		if sourceHash != nil {
			entry.Freshness.SourceHash = *sourceHash
		}
		if ttlSecs != nil {
			entry.TTL = &model.Duration{Duration: time.Duration(*ttlSecs) * time.Second}
		}
		if err := unmarshalNullableJSON(metaJ, &entry.Meta); err != nil {
			return nil, fmt.Errorf("unmarshal meta: %w", err)
		}

		results = append(results, storage.SimilarityResult{Entry: entry, Distance: dist})
	}
	return results, rows.Err()
}

// DeleteExpired removes entries whose ExpiresAt is in the past.
func (s *EntryStore) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := s.conn(ctx).Exec(ctx, `
		DELETE FROM entries
		WHERE expires_at IS NOT NULL AND expires_at <= NOW()
	`)
	if err != nil {
		return 0, fmt.Errorf("delete expired: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanEntryV2 scans a single entry from a pgx.Row with the v2 column set
// (includes content_hash and version).
func scanEntryV2(row pgx.Row) (*model.Entry, error) {
	var (
		entry       model.Entry
		idStr       string
		contentHash string
		embVec      pgvector.Vector
		embDim      *int
		embMod      *string
		srcType     string
		srcRef      string
		srcMeta     []byte
		conf        string
		ttlSecs     *int64
		metaJ       []byte
		version     int
		observedAt  time.Time
		observedBy  *string
		sourceHash  *string
	)

	if err := row.Scan(
		&idStr, &entry.Title, &entry.Content, &contentHash, &embVec, &embDim, &embMod,
		&srcType, &srcRef, &srcMeta,
		&conf,
		&entry.Scope, &ttlSecs, &entry.ExpiresAt,
		&metaJ, &version, &entry.CreatedAt, &entry.UpdatedAt,
		&observedAt, &observedBy, &sourceHash,
	); err != nil {
		return nil, err
	}

	return populateEntryV2(entry, idStr, contentHash, embVec, embDim, embMod,
		srcType, srcRef, srcMeta, conf, observedAt, observedBy, sourceHash, ttlSecs, metaJ, version)
}

// scanEntryFromRowsV2 scans a single entry from pgx.Rows with the v2 column set.
func scanEntryFromRowsV2(rows pgx.Rows) (*model.Entry, error) {
	var (
		entry       model.Entry
		idStr       string
		contentHash string
		embVec      pgvector.Vector
		embDim      *int
		embMod      *string
		srcType     string
		srcRef      string
		srcMeta     []byte
		conf        string
		ttlSecs     *int64
		metaJ       []byte
		version     int
		observedAt  time.Time
		observedBy  *string
		sourceHash  *string
	)

	if err := rows.Scan(
		&idStr, &entry.Title, &entry.Content, &contentHash, &embVec, &embDim, &embMod,
		&srcType, &srcRef, &srcMeta,
		&conf,
		&entry.Scope, &ttlSecs, &entry.ExpiresAt,
		&metaJ, &version, &entry.CreatedAt, &entry.UpdatedAt,
		&observedAt, &observedBy, &sourceHash,
	); err != nil {
		return nil, err
	}

	return populateEntryV2(entry, idStr, contentHash, embVec, embDim, embMod,
		srcType, srcRef, srcMeta, conf, observedAt, observedBy, sourceHash, ttlSecs, metaJ, version)
}

// populateEntryV2 fills in an Entry from scanned values (v2 column set).
func populateEntryV2(
	entry model.Entry,
	idStr string,
	contentHash string,
	embVec pgvector.Vector,
	embDim *int, embMod *string,
	srcType, srcRef string,
	srcMeta []byte,
	conf string,
	observedAt time.Time, observedBy *string, sourceHash *string,
	ttlSecs *int64,
	metaJ []byte,
	version int,
) (*model.Entry, error) {
	id, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse id: %w", err)
	}
	entry.ID = id
	entry.ContentHash = contentHash
	entry.Version = version

	entry.Embedding = embVec.Slice()
	if embDim != nil {
		entry.EmbeddingDim = *embDim
	}
	if embMod != nil {
		entry.EmbeddingModel = *embMod
	}

	entry.Source = model.Source{Type: model.SourceType(srcType), Reference: srcRef}
	if err := unmarshalNullableJSON(srcMeta, &entry.Source.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal source meta: %w", err)
	}

	entry.Provenance.Level = model.ProvenanceLevel(conf)
	entry.Freshness.ObservedAt = observedAt
	if observedBy != nil {
		entry.Freshness.ObservedBy = *observedBy
	}
	if sourceHash != nil {
		entry.Freshness.SourceHash = *sourceHash
	}

	if ttlSecs != nil {
		entry.TTL = &model.Duration{Duration: time.Duration(*ttlSecs) * time.Second}
	}

	if err := unmarshalNullableJSON(metaJ, &entry.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}

	return &entry, nil
}

// nullableString returns nil for empty strings.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullableInt returns nil for zero values.
func nullableInt(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}
