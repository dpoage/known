package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// EntryStore implements storage.EntryRepo using PostgreSQL with pgvector.
type EntryStore struct {
	pool *pgxpool.Pool
}

// Create persists a new entry.
func (s *EntryStore) Create(ctx context.Context, entry *model.Entry) error {
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

	_, err = s.pool.Exec(ctx, `
		INSERT INTO entries (
			id, content, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence, verified_at, verified_by,
			scope, ttl_seconds, expires_at,
			meta, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11,
			$12, $13, $14,
			$15, $16, $17
		)
	`,
		entry.ID.String(), entry.Content, embeddingVal, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Confidence.Level), entry.Confidence.VerifiedAt, nullableString(entry.Confidence.VerifiedBy),
		entry.Scope, ttlSeconds, entry.ExpiresAt,
		metaJSON, entry.CreatedAt, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}
	return nil
}

// Get retrieves an entry by ID.
func (s *EntryStore) Get(ctx context.Context, id model.ID) (*model.Entry, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
			id, content, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence, verified_at, verified_by,
			scope, ttl_seconds, expires_at,
			meta, created_at, updated_at
		FROM entries
		WHERE id = $1
	`, id.String())

	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get entry: %w", err)
	}
	return entry, nil
}

// Update replaces an existing entry.
func (s *EntryStore) Update(ctx context.Context, entry *model.Entry) error {
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

	tag, err := s.pool.Exec(ctx, `
		UPDATE entries SET
			content = $2,
			embedding = $3,
			embedding_dim = $4,
			embedding_model = $5,
			source_type = $6,
			source_ref = $7,
			source_meta = $8,
			confidence = $9,
			verified_at = $10,
			verified_by = $11,
			scope = $12,
			ttl_seconds = $13,
			expires_at = $14,
			meta = $15,
			updated_at = $16
		WHERE id = $1
	`,
		entry.ID.String(), entry.Content, embeddingVal, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Confidence.Level), entry.Confidence.VerifiedAt, nullableString(entry.Confidence.VerifiedBy),
		entry.Scope, ttlSeconds, entry.ExpiresAt,
		metaJSON, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// Delete removes an entry by ID.
func (s *EntryStore) Delete(ctx context.Context, id model.ID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM entries WHERE id = $1`, id.String())
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
		SELECT
			id, content, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence, verified_at, verified_by,
			scope, ttl_seconds, expires_at,
			meta, created_at, updated_at
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

	if filter.ConfidenceLevel != "" {
		query += fmt.Sprintf(" AND confidence = $%d", argIdx)
		args = append(args, string(filter.ConfidenceLevel))
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

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	var entries []model.Entry
	for rows.Next() {
		entry, err := scanEntryFromRows(rows)
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
			id, content, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence, verified_at, verified_by,
			scope, ttl_seconds, expires_at,
			meta, created_at, updated_at,
			%s AS distance
		FROM entries
		WHERE embedding IS NOT NULL
		  AND embedding_dim = $2
		  AND (scope = $3 OR scope LIKE $4)
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY %s
		LIMIT $5
	`, distExpr, distExpr)

	rows, err := s.pool.Query(ctx, sqlQuery, vec, len(query), scope, scope+".%", limit)
	if err != nil {
		return nil, fmt.Errorf("search similar: %w", err)
	}
	defer rows.Close()

	var results []storage.SimilarityResult
	for rows.Next() {
		var (
			entry   model.Entry
			idStr   string
			embVec  pgvector.Vector
			embDim  *int
			embMod  *string
			srcType string
			srcRef  string
			srcMeta []byte
			conf    string
			verAt   *time.Time
			verBy   *string
			ttlSecs *int64
			metaJ   []byte
			dist    float64
		)

		if err := rows.Scan(
			&idStr, &entry.Content, &embVec, &embDim, &embMod,
			&srcType, &srcRef, &srcMeta,
			&conf, &verAt, &verBy,
			&entry.Scope, &ttlSecs, &entry.ExpiresAt,
			&metaJ, &entry.CreatedAt, &entry.UpdatedAt,
			&dist,
		); err != nil {
			return nil, fmt.Errorf("scan similarity result: %w", err)
		}

		id, err := model.ParseID(idStr)
		if err != nil {
			return nil, fmt.Errorf("parse id: %w", err)
		}
		entry.ID = id
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
		entry.Confidence.Level = model.ConfidenceLevel(conf)
		entry.Confidence.VerifiedAt = verAt
		if verBy != nil {
			entry.Confidence.VerifiedBy = *verBy
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
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM entries
		WHERE expires_at IS NOT NULL AND expires_at <= NOW()
	`)
	if err != nil {
		return 0, fmt.Errorf("delete expired: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanEntry scans a single entry from a pgx.Row.
func scanEntry(row pgx.Row) (*model.Entry, error) {
	var (
		entry   model.Entry
		idStr   string
		embVec  pgvector.Vector
		embDim  *int
		embMod  *string
		srcType string
		srcRef  string
		srcMeta []byte
		conf    string
		verAt   *time.Time
		verBy   *string
		ttlSecs *int64
		metaJ   []byte
	)

	if err := row.Scan(
		&idStr, &entry.Content, &embVec, &embDim, &embMod,
		&srcType, &srcRef, &srcMeta,
		&conf, &verAt, &verBy,
		&entry.Scope, &ttlSecs, &entry.ExpiresAt,
		&metaJ, &entry.CreatedAt, &entry.UpdatedAt,
	); err != nil {
		return nil, err
	}

	return populateEntry(entry, idStr, embVec, embDim, embMod, srcType, srcRef, srcMeta, conf, verAt, verBy, ttlSecs, metaJ)
}

// scanEntryFromRows scans a single entry from pgx.Rows (same fields, different interface).
func scanEntryFromRows(rows pgx.Rows) (*model.Entry, error) {
	var (
		entry   model.Entry
		idStr   string
		embVec  pgvector.Vector
		embDim  *int
		embMod  *string
		srcType string
		srcRef  string
		srcMeta []byte
		conf    string
		verAt   *time.Time
		verBy   *string
		ttlSecs *int64
		metaJ   []byte
	)

	if err := rows.Scan(
		&idStr, &entry.Content, &embVec, &embDim, &embMod,
		&srcType, &srcRef, &srcMeta,
		&conf, &verAt, &verBy,
		&entry.Scope, &ttlSecs, &entry.ExpiresAt,
		&metaJ, &entry.CreatedAt, &entry.UpdatedAt,
	); err != nil {
		return nil, err
	}

	return populateEntry(entry, idStr, embVec, embDim, embMod, srcType, srcRef, srcMeta, conf, verAt, verBy, ttlSecs, metaJ)
}

// populateEntry fills in an Entry from scanned values.
func populateEntry(
	entry model.Entry,
	idStr string,
	embVec pgvector.Vector,
	embDim *int, embMod *string,
	srcType, srcRef string,
	srcMeta []byte,
	conf string,
	verAt *time.Time, verBy *string,
	ttlSecs *int64,
	metaJ []byte,
) (*model.Entry, error) {
	id, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse id: %w", err)
	}
	entry.ID = id

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

	entry.Confidence.Level = model.ConfidenceLevel(conf)
	entry.Confidence.VerifiedAt = verAt
	if verBy != nil {
		entry.Confidence.VerifiedBy = *verBy
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
