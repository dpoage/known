package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EdgeStore implements storage.EdgeRepo using PostgreSQL.
type EdgeStore struct {
	pool *pgxpool.Pool
}

// conn returns the transaction-aware connection for this store.
func (s *EdgeStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.pool)
}

// Create persists a new edge. When called within a transaction (via context),
// both the edge type registration and edge insert use the same transaction.
// Without a caller-provided transaction, each operation runs independently,
// which is acceptable since edge type registration uses ON CONFLICT DO NOTHING.
func (s *EdgeStore) Create(ctx context.Context, edge *model.Edge) error {
	metaJSON, err := marshalNullableJSON(edge.Meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	conn := s.conn(ctx)

	// Ensure the edge type exists in the registry (insert if custom)
	_, err = conn.Exec(ctx, `
		INSERT INTO edge_types (name, predefined) VALUES ($1, FALSE)
		ON CONFLICT (name) DO NOTHING
	`, string(edge.Type))
	if err != nil {
		return fmt.Errorf("ensure edge type: %w", err)
	}

	_, err = conn.Exec(ctx, `
		INSERT INTO edges (id, from_id, to_id, type, weight, meta, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		edge.ID.String(), edge.FromID.String(), edge.ToID.String(),
		string(edge.Type), edge.Weight, metaJSON, edge.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create edge: %w", err)
	}
	return nil
}

// Get retrieves an edge by ID.
func (s *EdgeStore) Get(ctx context.Context, id model.ID) (*model.Edge, error) {
	row := s.conn(ctx).QueryRow(ctx, `
		SELECT id, from_id, to_id, type, weight, meta, created_at
		FROM edges
		WHERE id = $1
	`, id.String())

	edge, err := scanEdge(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get edge: %w", err)
	}
	return edge, nil
}

// Delete removes an edge by ID.
func (s *EdgeStore) Delete(ctx context.Context, id model.ID) error {
	tag, err := s.conn(ctx).Exec(ctx, `DELETE FROM edges WHERE id = $1`, id.String())
	if err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// EdgesFrom returns all outgoing edges from the given entry.
func (s *EdgeStore) EdgesFrom(ctx context.Context, entryID model.ID, filter storage.EdgeFilter) ([]model.Edge, error) {
	return s.queryEdges(ctx, "from_id", entryID, filter)
}

// EdgesTo returns all incoming edges to the given entry.
func (s *EdgeStore) EdgesTo(ctx context.Context, entryID model.ID, filter storage.EdgeFilter) ([]model.Edge, error) {
	return s.queryEdges(ctx, "to_id", entryID, filter)
}

// EdgesBetween returns edges from source to target.
func (s *EdgeStore) EdgesBetween(ctx context.Context, fromID, toID model.ID) ([]model.Edge, error) {
	rows, err := s.conn(ctx).Query(ctx, `
		SELECT id, from_id, to_id, type, weight, meta, created_at
		FROM edges
		WHERE from_id = $1 AND to_id = $2
		ORDER BY created_at
	`, fromID.String(), toID.String())
	if err != nil {
		return nil, fmt.Errorf("edges between: %w", err)
	}
	defer rows.Close()

	return scanEdges(rows)
}

// FindConflicts returns entries that have a "contradicts" edge involving the given entry.
func (s *EdgeStore) FindConflicts(ctx context.Context, entryID model.ID) ([]model.Entry, error) {
	rows, err := s.conn(ctx).Query(ctx, `
		SELECT DISTINCT
			e.id, e.content, e.content_hash, e.embedding, e.embedding_dim, e.embedding_model,
			e.source_type, e.source_ref, e.source_meta,
			e.confidence, e.verified_at, e.verified_by,
			e.scope, e.ttl_seconds, e.expires_at,
			e.meta, e.version, e.created_at, e.updated_at
		FROM entries e
		INNER JOIN edges eg ON (
			(eg.from_id = $1 AND eg.to_id = e.id)
			OR
			(eg.to_id = $1 AND eg.from_id = e.id)
		)
		WHERE eg.type = 'contradicts'
		  AND e.id <> $1
		ORDER BY e.created_at
	`, entryID.String())
	if err != nil {
		return nil, fmt.Errorf("find conflicts: %w", err)
	}
	defer rows.Close()

	var entries []model.Entry
	for rows.Next() {
		entry, err := scanEntryFromRowsV2(rows)
		if err != nil {
			return nil, fmt.Errorf("scan conflict entry: %w", err)
		}
		entries = append(entries, *entry)
	}
	return entries, rows.Err()
}

// queryEdges is a helper for EdgesFrom and EdgesTo.
func (s *EdgeStore) queryEdges(ctx context.Context, column string, entryID model.ID, filter storage.EdgeFilter) ([]model.Edge, error) {
	query := fmt.Sprintf(`
		SELECT id, from_id, to_id, type, weight, meta, created_at
		FROM edges
		WHERE %s = $1
	`, column)
	args := []any{entryID.String()}
	argIdx := 2

	if filter.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, string(filter.Type))
		argIdx++
	}

	query += " ORDER BY created_at"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}

	rows, err := s.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	return scanEdges(rows)
}

// scanEdge scans a single edge from a pgx.Row.
func scanEdge(row pgx.Row) (*model.Edge, error) {
	var (
		edge    model.Edge
		idStr   string
		fromStr string
		toStr   string
		edgeTp  string
		metaJ   []byte
	)

	if err := row.Scan(&idStr, &fromStr, &toStr, &edgeTp, &edge.Weight, &metaJ, &edge.CreatedAt); err != nil {
		return nil, err
	}

	return populateEdge(edge, idStr, fromStr, toStr, edgeTp, metaJ)
}

// scanEdges scans multiple edges from pgx.Rows.
func scanEdges(rows pgx.Rows) ([]model.Edge, error) {
	var edges []model.Edge
	for rows.Next() {
		var (
			edge    model.Edge
			idStr   string
			fromStr string
			toStr   string
			edgeTp  string
			metaJ   []byte
		)

		if err := rows.Scan(&idStr, &fromStr, &toStr, &edgeTp, &edge.Weight, &metaJ, &edge.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}

		e, err := populateEdge(edge, idStr, fromStr, toStr, edgeTp, metaJ)
		if err != nil {
			return nil, err
		}
		edges = append(edges, *e)
	}
	return edges, rows.Err()
}

// populateEdge fills in an Edge from scanned values.
func populateEdge(edge model.Edge, idStr, fromStr, toStr, edgeTp string, metaJ []byte) (*model.Edge, error) {
	id, err := model.ParseID(idStr)
	if err != nil {
		return nil, fmt.Errorf("parse edge id: %w", err)
	}
	edge.ID = id

	fromID, err := model.ParseID(fromStr)
	if err != nil {
		return nil, fmt.Errorf("parse from id: %w", err)
	}
	edge.FromID = fromID

	toID, err := model.ParseID(toStr)
	if err != nil {
		return nil, fmt.Errorf("parse to id: %w", err)
	}
	edge.ToID = toID

	edge.Type = model.EdgeType(edgeTp)

	if err := unmarshalNullableJSON(metaJ, &edge.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal edge meta: %w", err)
	}

	return &edge, nil
}
