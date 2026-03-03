package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// EdgeStore implements storage.EdgeRepo using SQLite.
type EdgeStore struct {
	db *sql.DB
}

func (s *EdgeStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.db)
}

// Create persists a new edge.
func (s *EdgeStore) Create(ctx context.Context, edge *model.Edge) error {
	metaJSON, err := marshalNullableJSON(edge.Meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	conn := s.conn(ctx)

	// Ensure the edge type exists in the registry.
	_, err = conn.ExecContext(ctx, `
		INSERT INTO edge_types (name, predefined) VALUES (?, 0)
		ON CONFLICT (name) DO NOTHING
	`, string(edge.Type))
	if err != nil {
		return fmt.Errorf("ensure edge type: %w", err)
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO edges (id, from_id, to_id, type, weight, meta, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		edge.ID.String(), edge.FromID.String(), edge.ToID.String(),
		string(edge.Type), edge.Weight, metaJSON, formatTime(edge.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("create edge: %w", err)
	}
	return nil
}

// Get retrieves an edge by ID.
func (s *EdgeStore) Get(ctx context.Context, id model.ID) (*model.Edge, error) {
	row := s.conn(ctx).QueryRowContext(ctx, `
		SELECT id, from_id, to_id, type, weight, meta, created_at
		FROM edges
		WHERE id = ?
	`, id.String())

	edge, err := scanEdge(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get edge: %w", err)
	}
	return edge, nil
}

// Update modifies an existing edge's weight and metadata.
func (s *EdgeStore) Update(ctx context.Context, edge *model.Edge) error {
	metaJSON, err := marshalNullableJSON(edge.Meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	result, err := s.conn(ctx).ExecContext(ctx, `
		UPDATE edges SET weight = ?, meta = ? WHERE id = ?
	`, edge.Weight, metaJSON, edge.ID.String())
	if err != nil {
		return fmt.Errorf("update edge: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// Delete removes an edge by ID.
func (s *EdgeStore) Delete(ctx context.Context, id model.ID) error {
	result, err := s.conn(ctx).ExecContext(ctx, `DELETE FROM edges WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
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
	rows, err := s.conn(ctx).QueryContext(ctx, `
		SELECT id, from_id, to_id, type, weight, meta, created_at
		FROM edges
		WHERE from_id = ? AND to_id = ?
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
	rows, err := s.conn(ctx).QueryContext(ctx, `
		SELECT DISTINCT
			e.id, e.title, e.content, e.content_hash, e.embedding, e.embedding_dim, e.embedding_model,
			e.source_type, e.source_ref, e.source_meta,
			e.confidence,
			e.scope, e.ttl_seconds, e.expires_at,
			e.meta, e.version, e.created_at, e.updated_at,
			e.observed_at, e.observed_by, e.source_hash
		FROM entries e
		INNER JOIN edges eg ON (
			(eg.from_id = ? AND eg.to_id = e.id)
			OR
			(eg.to_id = ? AND eg.from_id = e.id)
		)
		WHERE eg.type = 'contradicts'
		  AND e.id <> ?
		ORDER BY e.created_at
	`, entryID.String(), entryID.String(), entryID.String())
	if err != nil {
		return nil, fmt.Errorf("find conflicts: %w", err)
	}
	defer rows.Close()

	var entries []model.Entry
	for rows.Next() {
		entry, err := scanEntryFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scan conflict entry: %w", err)
		}
		entries = append(entries, *entry)
	}
	return entries, rows.Err()
}

func (s *EdgeStore) queryEdges(ctx context.Context, column string, entryID model.ID, filter storage.EdgeFilter) ([]model.Edge, error) {
	query := fmt.Sprintf(`
		SELECT id, from_id, to_id, type, weight, meta, created_at
		FROM edges
		WHERE %s = ?
	`, column)
	args := []any{entryID.String()}

	if filter.Type != "" {
		query += " AND type = ?"
		args = append(args, string(filter.Type))
	}

	query += " ORDER BY created_at"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.conn(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()
	return scanEdges(rows)
}

func scanEdge(row *sql.Row) (*model.Edge, error) {
	var (
		edge    model.Edge
		idStr   string
		fromStr string
		toStr   string
		edgeTp  string
		metaJ   []byte
		created string
	)

	if err := row.Scan(&idStr, &fromStr, &toStr, &edgeTp, &edge.Weight, &metaJ, &created); err != nil {
		return nil, err
	}

	return populateEdge(edge, idStr, fromStr, toStr, edgeTp, metaJ, created)
}

func scanEdges(rows *sql.Rows) ([]model.Edge, error) {
	var edges []model.Edge
	for rows.Next() {
		var (
			edge    model.Edge
			idStr   string
			fromStr string
			toStr   string
			edgeTp  string
			metaJ   []byte
			created string
		)

		if err := rows.Scan(&idStr, &fromStr, &toStr, &edgeTp, &edge.Weight, &metaJ, &created); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}

		e, err := populateEdge(edge, idStr, fromStr, toStr, edgeTp, metaJ, created)
		if err != nil {
			return nil, err
		}
		edges = append(edges, *e)
	}
	return edges, rows.Err()
}

func populateEdge(edge model.Edge, idStr, fromStr, toStr, edgeTp string, metaJ []byte, created string) (*model.Edge, error) {
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
	edge.CreatedAt = parseTime(created)

	if err := unmarshalNullableJSON(metaJ, &edge.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal edge meta: %w", err)
	}

	return &edge, nil
}
