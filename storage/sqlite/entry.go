package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// EntryStore implements storage.EntryRepo using SQLite.
type EntryStore struct {
	db *sql.DB
}

func (s *EntryStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.db)
}

func (s *EntryStore) withTx(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if err := fn(txCtx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

const entryColumns = `
	id, title, content, content_hash, embedding, embedding_dim, embedding_model,
	source_type, source_ref, source_meta,
	confidence,
	scope, ttl_seconds, expires_at,
	meta, version, created_at, updated_at,
	observed_at, observed_by, source_hash
`

// Create persists a new entry. It automatically ensures the scope hierarchy exists.
func (s *EntryStore) Create(ctx context.Context, entry *model.Entry) error {
	if _, ok := ctx.Value(txKey{}).(*sql.Tx); !ok {
		return s.withTx(ctx, func(txCtx context.Context) error {
			return s.createInner(txCtx, entry)
		})
	}
	return s.createInner(ctx, entry)
}

func (s *EntryStore) createInner(ctx context.Context, entry *model.Entry) error {
	if entry.ContentHash == "" {
		entry.ContentHash = model.ComputeContentHash(entry.Content)
	}
	if entry.Version == 0 {
		entry.Version = 1
	}

	scopeStore := &ScopeStore{db: s.db}
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

	var embeddingBlob []byte
	if len(entry.Embedding) > 0 {
		embeddingBlob, err = serializeFloat32(entry.Embedding)
		if err != nil {
			return fmt.Errorf("serialize embedding: %w", err)
		}
	}

	var ttlSeconds *int64
	if entry.TTL != nil {
		secs := int64(entry.TTL.Duration.Seconds())
		ttlSeconds = &secs
	}

	_, err = s.conn(ctx).ExecContext(ctx, `
		INSERT INTO entries (
			id, title, content, content_hash, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence,
			scope, ttl_seconds, expires_at,
			meta, version, created_at, updated_at,
			observed_at, observed_by, source_hash
		) VALUES (
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?,
			?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?
		)
	`,
		entry.ID.String(), entry.Title, entry.Content, entry.ContentHash,
		embeddingBlob, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Provenance.Level),
		entry.Scope, ttlSeconds, formatNullableTime(entry.ExpiresAt),
		metaJSON, entry.Version, formatTime(entry.CreatedAt), formatTime(entry.UpdatedAt),
		formatTime(entry.Freshness.ObservedAt), nullableString(entry.Freshness.ObservedBy), nullableString(entry.Freshness.SourceHash),
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("%w: content_hash+scope", storage.ErrDuplicateContent)
		}
		return fmt.Errorf("create entry: %w", err)
	}

	if len(entry.Labels) > 0 {
		if err := saveLabels(ctx, s.conn(ctx), entry.ID.String(), entry.Labels); err != nil {
			return fmt.Errorf("save labels: %w", err)
		}
	}
	return nil
}

// CreateOrUpdate inserts a new entry or updates an existing one with the same
// content hash and scope.
func (s *EntryStore) CreateOrUpdate(ctx context.Context, entry *model.Entry) (*model.Entry, error) {
	if _, ok := ctx.Value(txKey{}).(*sql.Tx); !ok {
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
	if entry.ContentHash == "" {
		entry.ContentHash = model.ComputeContentHash(entry.Content)
	}
	if entry.Version == 0 {
		entry.Version = 1
	}

	scopeStore := &ScopeStore{db: s.db}
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

	var embeddingBlob []byte
	if len(entry.Embedding) > 0 {
		embeddingBlob, err = serializeFloat32(entry.Embedding)
		if err != nil {
			return nil, fmt.Errorf("serialize embedding: %w", err)
		}
	}

	var ttlSeconds *int64
	if entry.TTL != nil {
		secs := int64(entry.TTL.Duration.Seconds())
		ttlSeconds = &secs
	}

	// SQLite doesn't have RETURNING with ON CONFLICT in the same way as Postgres.
	// Use INSERT OR IGNORE then check if it was a conflict.
	conn := s.conn(ctx)

	// Try to insert first.
	result, err := conn.ExecContext(ctx, `
		INSERT INTO entries (
			id, title, content, content_hash, embedding, embedding_dim, embedding_model,
			source_type, source_ref, source_meta,
			confidence,
			scope, ttl_seconds, expires_at,
			meta, version, created_at, updated_at,
			observed_at, observed_by, source_hash
		) VALUES (
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?,
			?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?
		)
		ON CONFLICT (content_hash, scope) DO UPDATE SET
			title = excluded.title,
			content = excluded.content,
			embedding = excluded.embedding,
			embedding_dim = excluded.embedding_dim,
			embedding_model = excluded.embedding_model,
			source_type = excluded.source_type,
			source_ref = excluded.source_ref,
			source_meta = excluded.source_meta,
			confidence = excluded.confidence,
			ttl_seconds = excluded.ttl_seconds,
			expires_at = excluded.expires_at,
			meta = excluded.meta,
			version = entries.version + 1,
			updated_at = excluded.updated_at,
			observed_at = excluded.observed_at,
			observed_by = excluded.observed_by,
			source_hash = excluded.source_hash
	`,
		entry.ID.String(), entry.Title, entry.Content, entry.ContentHash,
		embeddingBlob, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Provenance.Level),
		entry.Scope, ttlSeconds, formatNullableTime(entry.ExpiresAt),
		metaJSON, entry.Version, formatTime(entry.CreatedAt), formatTime(entry.UpdatedAt),
		formatTime(entry.Freshness.ObservedAt), nullableString(entry.Freshness.ObservedBy), nullableString(entry.Freshness.SourceHash),
	)
	if err != nil {
		return nil, fmt.Errorf("create or update entry: %w", err)
	}

	if len(entry.Labels) > 0 {
		// Fetch the ID of the resulting row first (may differ from entry.ID on conflict).
		var resultID string
		if err := conn.QueryRowContext(ctx, `SELECT id FROM entries WHERE content_hash = ? AND scope = ?`,
			entry.ContentHash, entry.Scope).Scan(&resultID); err != nil {
			return nil, fmt.Errorf("fetch result id: %w", err)
		}
		if err := saveLabels(ctx, conn, resultID, entry.Labels); err != nil {
			return nil, fmt.Errorf("save labels: %w", err)
		}
	}

	// Fetch the resulting row.
	_ = result
	row := conn.QueryRowContext(ctx, `
		SELECT `+entryColumns+`
		FROM entries
		WHERE content_hash = ? AND scope = ?
	`, entry.ContentHash, entry.Scope)

	resultEntry, err := scanEntry(row)
	if err != nil {
		return nil, err
	}

	resultEntry.Labels, err = loadLabels(ctx, conn, resultEntry.ID.String())
	if err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	return resultEntry, nil
}

// Get retrieves an entry by ID.
func (s *EntryStore) Get(ctx context.Context, id model.ID) (*model.Entry, error) {
	row := s.conn(ctx).QueryRowContext(ctx, `
		SELECT `+entryColumns+`
		FROM entries
		WHERE id = ?
	`, id.String())

	entry, err := scanEntry(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get entry: %w", err)
	}

	entry.Labels, err = loadLabels(ctx, s.conn(ctx), entry.ID.String())
	if err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	return entry, nil
}

// Update replaces an existing entry with optimistic concurrency control.
func (s *EntryStore) Update(ctx context.Context, entry *model.Entry) error {
	entry.ContentHash = model.ComputeContentHash(entry.Content)

	scopeStore := &ScopeStore{db: s.db}
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

	var embeddingBlob []byte
	if len(entry.Embedding) > 0 {
		embeddingBlob, err = serializeFloat32(entry.Embedding)
		if err != nil {
			return fmt.Errorf("serialize embedding: %w", err)
		}
	}

	var ttlSeconds *int64
	if entry.TTL != nil {
		secs := int64(entry.TTL.Duration.Seconds())
		ttlSeconds = &secs
	}

	conn := s.conn(ctx)

	result, err := conn.ExecContext(ctx, `
		UPDATE entries SET
			title = ?,
			content = ?,
			content_hash = ?,
			embedding = ?,
			embedding_dim = ?,
			embedding_model = ?,
			source_type = ?,
			source_ref = ?,
			source_meta = ?,
			confidence = ?,
			scope = ?,
			ttl_seconds = ?,
			expires_at = ?,
			meta = ?,
			version = ? + 1,
			updated_at = ?,
			observed_at = ?,
			observed_by = ?,
			source_hash = ?
		WHERE id = ? AND version = ?
	`,
		entry.Title, entry.Content, entry.ContentHash,
		embeddingBlob, nullableInt(entry.EmbeddingDim), nullableString(entry.EmbeddingModel),
		string(entry.Source.Type), entry.Source.Reference, sourceMetaJSON,
		string(entry.Provenance.Level),
		entry.Scope, ttlSeconds, formatNullableTime(entry.ExpiresAt),
		metaJSON, entry.Version, formatTime(entry.UpdatedAt),
		formatTime(entry.Freshness.ObservedAt), nullableString(entry.Freshness.ObservedBy), nullableString(entry.Freshness.SourceHash),
		entry.ID.String(), entry.Version,
	)
	if err != nil {
		return fmt.Errorf("update entry: %w", err)
	}

	n, _ := result.RowsAffected()
	if n == 1 {
		entry.Version++
		if err := saveLabels(ctx, conn, entry.ID.String(), entry.Labels); err != nil {
			return fmt.Errorf("save labels: %w", err)
		}
		return nil
	}

	// Zero rows affected — distinguish not-found from version mismatch.
	var actualVersion int
	probeErr := conn.QueryRowContext(ctx, `SELECT version FROM entries WHERE id = ?`, entry.ID.String()).Scan(&actualVersion)
	if probeErr == sql.ErrNoRows {
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
	result, err := s.conn(ctx).ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("delete entry: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// List returns entries matching the given filter.
func (s *EntryStore) List(ctx context.Context, filter storage.EntryFilter) ([]model.Entry, error) {
	query := `SELECT ` + entryColumns + ` FROM entries WHERE 1=1`
	var args []any

	if filter.Scope != "" {
		query += " AND scope = ?"
		args = append(args, filter.Scope)
	}

	if filter.ScopePrefix != "" {
		query += " AND (scope = ? OR scope LIKE ?)"
		args = append(args, filter.ScopePrefix, filter.ScopePrefix+".%")
	}

	if filter.SourceType != "" {
		query += " AND source_type = ?"
		args = append(args, string(filter.SourceType))
	}

	if filter.ProvenanceLevel != "" {
		query += " AND confidence = ?"
		args = append(args, string(filter.ProvenanceLevel))
	}

	if filter.StalerThan > 0 {
		cutoff := formatTime(time.Now().Add(-filter.StalerThan))
		query += " AND observed_at IS NOT NULL AND observed_at < ?"
		args = append(args, cutoff)
	}

	for _, label := range filter.Labels {
		query += " AND EXISTS (SELECT 1 FROM entry_labels WHERE entry_labels.entry_id = entries.id AND entry_labels.label = ?)"
		args = append(args, label)
	}

	if !filter.IncludeExpired {
		now := formatTime(time.Now())
		query += " AND (expires_at IS NULL OR expires_at > ?)"
		args = append(args, now)
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.conn(ctx).QueryContext(ctx, query, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := loadLabelsForEntries(ctx, s.conn(ctx), entries); err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	return entries, nil
}

// SearchSimilar finds entries with embeddings similar to the query vector.
//
// Uses a two-pass approach to avoid reading full rows for every entry:
//  1. Scan only (id, embedding) for all candidates; rank by distance in Go.
//  2. Fetch full rows for the top-K winners by ID.
func (s *EntryStore) SearchSimilar(ctx context.Context, query []float32, scope string, metric storage.SimilarityMetric, limit int) ([]storage.SimilarityResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query vector must not be empty")
	}
	if limit <= 0 {
		limit = 10
	}

	now := formatTime(time.Now())
	conn := s.conn(ctx)

	// Pass 1: read only id + embedding BLOB to rank candidates.
	rows, err := conn.QueryContext(ctx, `
		SELECT id, embedding
		FROM entries
		WHERE embedding IS NOT NULL
		  AND embedding_dim = ?
		  AND (scope = ? OR scope LIKE ?)
		  AND (expires_at IS NULL OR expires_at > ?)
	`, len(query), scope, scope+".%", now)
	if err != nil {
		return nil, fmt.Errorf("search similar (scan): %w", err)
	}

	distFn := cosineDistance
	if metric == storage.L2 {
		distFn = l2Distance
	}

	type candidate struct {
		id   string
		dist float64
	}
	var candidates []candidate
	for rows.Next() {
		var idStr string
		var embBlob []byte
		if err := rows.Scan(&idStr, &embBlob); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		emb := deserializeFloat32(embBlob)
		if len(emb) != len(query) {
			continue
		}
		candidates = append(candidates, candidate{id: idStr, dist: distFn(query, emb)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates: %w", err)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Sort and take top-K.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].dist < candidates[j].dist
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	// Pass 2: fetch full rows for the winners.
	placeholders := make([]string, len(candidates))
	args := make([]any, len(candidates))
	distByID := make(map[string]float64, len(candidates))
	for i, c := range candidates {
		placeholders[i] = "?"
		args[i] = c.id
		distByID[c.id] = c.dist
	}

	fetchQuery := `SELECT ` + entryColumns + ` FROM entries WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	fetchRows, err := conn.QueryContext(ctx, fetchQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("search similar (fetch): %w", err)
	}
	defer fetchRows.Close()

	results := make([]storage.SimilarityResult, 0, len(candidates))
	for fetchRows.Next() {
		entry, err := scanEntryFromRows(fetchRows)
		if err != nil {
			return nil, fmt.Errorf("scan result entry: %w", err)
		}
		results = append(results, storage.SimilarityResult{
			Entry:    *entry,
			Distance: distByID[entry.ID.String()],
		})
	}
	if err := fetchRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate results: %w", err)
	}

	// Re-sort results by distance (IN query doesn't preserve order).
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	// Batch-load labels for result entries.
	if len(results) > 0 {
		resultEntries := make([]model.Entry, len(results))
		for i := range results {
			resultEntries[i] = results[i].Entry
		}
		if err := loadLabelsForEntries(ctx, s.conn(ctx), resultEntries); err != nil {
			return nil, fmt.Errorf("load labels: %w", err)
		}
		for i := range results {
			results[i].Entry = resultEntries[i]
		}
	}

	return results, nil
}

// DeleteExpired removes entries whose ExpiresAt is in the past.
func (s *EntryStore) DeleteExpired(ctx context.Context) (int64, error) {
	now := formatTime(time.Now())
	result, err := s.conn(ctx).ExecContext(ctx, `
		DELETE FROM entries
		WHERE expires_at IS NOT NULL AND expires_at <= ?
	`, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// scanEntry scans a single entry from a *sql.Row.
func scanEntry(row *sql.Row) (*model.Entry, error) {
	var (
		entry          model.Entry
		idStr          string
		contentHash    string
		embBlob        []byte
		embDim         *int
		embMod         *string
		srcType        string
		srcRef         string
		srcMeta        []byte
		conf           string
		ttlSecs        *int64
		expiresStr     *string
		metaJ          []byte
		version        int
		createdStr     string
		updatedStr     string
		observedAtStr  *string
		observedBy     *string
		sourceHash     *string
	)

	if err := row.Scan(
		&idStr, &entry.Title, &entry.Content, &contentHash, &embBlob, &embDim, &embMod,
		&srcType, &srcRef, &srcMeta,
		&conf,
		&entry.Scope, &ttlSecs, &expiresStr,
		&metaJ, &version, &createdStr, &updatedStr,
		&observedAtStr, &observedBy, &sourceHash,
	); err != nil {
		return nil, err
	}

	return populateEntry(entry, idStr, contentHash, embBlob, embDim, embMod,
		srcType, srcRef, srcMeta, conf, ttlSecs, expiresStr, metaJ, version, createdStr, updatedStr,
		observedAtStr, observedBy, sourceHash)
}

// scanEntryFromRows scans a single entry from *sql.Rows.
func scanEntryFromRows(rows *sql.Rows) (*model.Entry, error) {
	var (
		entry          model.Entry
		idStr          string
		contentHash    string
		embBlob        []byte
		embDim         *int
		embMod         *string
		srcType        string
		srcRef         string
		srcMeta        []byte
		conf           string
		ttlSecs        *int64
		expiresStr     *string
		metaJ          []byte
		version        int
		createdStr     string
		updatedStr     string
		observedAtStr  *string
		observedBy     *string
		sourceHash     *string
	)

	if err := rows.Scan(
		&idStr, &entry.Title, &entry.Content, &contentHash, &embBlob, &embDim, &embMod,
		&srcType, &srcRef, &srcMeta,
		&conf,
		&entry.Scope, &ttlSecs, &expiresStr,
		&metaJ, &version, &createdStr, &updatedStr,
		&observedAtStr, &observedBy, &sourceHash,
	); err != nil {
		return nil, err
	}

	return populateEntry(entry, idStr, contentHash, embBlob, embDim, embMod,
		srcType, srcRef, srcMeta, conf, ttlSecs, expiresStr, metaJ, version, createdStr, updatedStr,
		observedAtStr, observedBy, sourceHash)
}

func populateEntry(
	entry model.Entry,
	idStr, contentHash string,
	embBlob []byte, embDim *int, embMod *string,
	srcType, srcRef string, srcMeta []byte,
	conf string,
	ttlSecs *int64, expiresStr *string,
	metaJ []byte, version int,
	createdStr, updatedStr string,
	observedAtStr *string, observedBy *string, sourceHash *string,
) (*model.Entry, error) {
	id, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse id: %w", err)
	}
	entry.ID = id
	entry.ContentHash = contentHash
	entry.Version = version

	entry.Embedding = deserializeFloat32(embBlob)
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

	entry.ExpiresAt = parseNullableTime(expiresStr)
	if ttlSecs != nil {
		entry.TTL = &model.Duration{Duration: time.Duration(*ttlSecs) * time.Second}
	}

	if err := unmarshalNullableJSON(metaJ, &entry.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}

	entry.CreatedAt = parseTime(createdStr)
	entry.UpdatedAt = parseTime(updatedStr)

	// Freshness fields.
	if observedAtStr != nil {
		entry.Freshness.ObservedAt = parseTime(*observedAtStr)
	} else {
		entry.Freshness.ObservedAt = entry.CreatedAt
	}
	if observedBy != nil {
		entry.Freshness.ObservedBy = *observedBy
	}
	if sourceHash != nil {
		entry.Freshness.SourceHash = *sourceHash
	}

	return &entry, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableInt(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

// isUniqueConstraintError checks if an error is a SQLite UNIQUE constraint violation.
func isUniqueConstraintError(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "constraint failed")
}

// saveLabels replaces all labels for an entry.
func saveLabels(ctx context.Context, conn DBTX, entryID string, labels []string) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM entry_labels WHERE entry_id = ?`, entryID); err != nil {
		return fmt.Errorf("delete labels: %w", err)
	}
	for _, label := range labels {
		if _, err := conn.ExecContext(ctx, `INSERT INTO entry_labels (entry_id, label) VALUES (?, ?)`, entryID, label); err != nil {
			return fmt.Errorf("insert label: %w", err)
		}
	}
	return nil
}

// loadLabels returns all labels for a single entry.
func loadLabels(ctx context.Context, conn DBTX, entryID string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT label FROM entry_labels WHERE entry_id = ? ORDER BY label`, entryID)
	if err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

// loadLabelsForEntries batch-loads labels for a slice of entries to avoid N+1 queries.
func loadLabelsForEntries(ctx context.Context, conn DBTX, entries []model.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	placeholders := make([]string, len(entries))
	args := make([]any, len(entries))
	idxMap := make(map[string]int, len(entries))
	for i, e := range entries {
		placeholders[i] = "?"
		idStr := e.ID.String()
		args[i] = idStr
		idxMap[idStr] = i
	}
	rows, err := conn.QueryContext(ctx,
		`SELECT entry_id, label FROM entry_labels WHERE entry_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY entry_id, label`,
		args...)
	if err != nil {
		return fmt.Errorf("batch load labels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entryID, label string
		if err := rows.Scan(&entryID, &label); err != nil {
			return err
		}
		if idx, ok := idxMap[entryID]; ok {
			entries[idx].Labels = append(entries[idx].Labels, label)
		}
	}
	return rows.Err()
}
