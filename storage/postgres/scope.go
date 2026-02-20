package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ScopeStore implements storage.ScopeRepo using PostgreSQL with ltree.
type ScopeStore struct {
	pool *pgxpool.Pool
}

// conn returns the transaction-aware connection for this store.
func (s *ScopeStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.pool)
}

// dotToLtree converts a dot-separated scope path to ltree format.
// Ltree uses dots as separators too, but segment characters are restricted
// to alphanumeric and underscores. We replace hyphens with double underscores
// to preserve them through the ltree round-trip.
func dotToLtree(dotPath string) string {
	return strings.ReplaceAll(dotPath, "-", "__")
}

// Upsert creates or updates a scope.
func (s *ScopeStore) Upsert(ctx context.Context, scope *model.Scope) error {
	metaJSON, err := marshalNullableJSON(scope.Meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	ltreePath := dotToLtree(scope.Path)

	_, err = s.conn(ctx).Exec(ctx, `
		INSERT INTO scopes (path, ltree_path, meta, created_at)
		VALUES ($1, $2::ltree, $3, $4)
		ON CONFLICT (path) DO UPDATE SET
			meta = EXCLUDED.meta
	`, scope.Path, ltreePath, metaJSON, scope.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert scope: %w", err)
	}
	return nil
}

// EnsureHierarchy ensures that all ancestor scopes exist for a given scope path.
// For example, given "project.auth.oauth", it ensures "project", "project.auth",
// and "project.auth.oauth" all exist.
func (s *ScopeStore) EnsureHierarchy(ctx context.Context, path string) error {
	segments := strings.Split(path, ".")
	for i := range segments {
		ancestorPath := strings.Join(segments[:i+1], ".")
		ltreePath := dotToLtree(ancestorPath)
		_, err := s.conn(ctx).Exec(ctx, `
			INSERT INTO scopes (path, ltree_path, created_at)
			VALUES ($1, $2::ltree, $3)
			ON CONFLICT (path) DO NOTHING
		`, ancestorPath, ltreePath, time.Now())
		if err != nil {
			return fmt.Errorf("ensure scope %q: %w", ancestorPath, err)
		}
	}
	return nil
}

// Get retrieves a scope by path.
func (s *ScopeStore) Get(ctx context.Context, path string) (*model.Scope, error) {
	row := s.conn(ctx).QueryRow(ctx, `
		SELECT path, meta, created_at
		FROM scopes
		WHERE path = $1
	`, path)

	var scope model.Scope
	var metaJSON []byte
	if err := row.Scan(&scope.Path, &metaJSON, &scope.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get scope: %w", err)
	}

	if err := unmarshalNullableJSON(metaJSON, &scope.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}

	return &scope, nil
}

// Delete removes a scope by path.
func (s *ScopeStore) Delete(ctx context.Context, path string) error {
	tag, err := s.conn(ctx).Exec(ctx, `DELETE FROM scopes WHERE path = $1`, path)
	if err != nil {
		return fmt.Errorf("delete scope: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// List returns all scopes ordered by path.
func (s *ScopeStore) List(ctx context.Context) ([]model.Scope, error) {
	rows, err := s.conn(ctx).Query(ctx, `
		SELECT path, meta, created_at
		FROM scopes
		ORDER BY path
	`)
	if err != nil {
		return nil, fmt.Errorf("list scopes: %w", err)
	}
	defer rows.Close()

	return scanScopes(rows)
}

// ListChildren returns direct child scopes of the given parent path.
func (s *ScopeStore) ListChildren(ctx context.Context, parentPath string) ([]model.Scope, error) {
	ltreePath := dotToLtree(parentPath)

	rows, err := s.conn(ctx).Query(ctx, `
		SELECT path, meta, created_at
		FROM scopes
		WHERE ltree_path ~ ($1 || '.*{1}')::lquery
		ORDER BY path
	`, ltreePath)
	if err != nil {
		return nil, fmt.Errorf("list children: %w", err)
	}
	defer rows.Close()

	return scanScopes(rows)
}

// ListDescendants returns all descendant scopes of the given ancestor path.
func (s *ScopeStore) ListDescendants(ctx context.Context, ancestorPath string) ([]model.Scope, error) {
	ltreePath := dotToLtree(ancestorPath)

	rows, err := s.conn(ctx).Query(ctx, `
		SELECT path, meta, created_at
		FROM scopes
		WHERE ltree_path <@ $1::ltree
		  AND path <> $2
		ORDER BY path
	`, ltreePath, ancestorPath)
	if err != nil {
		return nil, fmt.Errorf("list descendants: %w", err)
	}
	defer rows.Close()

	return scanScopes(rows)
}

func scanScopes(rows pgx.Rows) ([]model.Scope, error) {
	var scopes []model.Scope
	for rows.Next() {
		var scope model.Scope
		var metaJSON []byte
		if err := rows.Scan(&scope.Path, &metaJSON, &scope.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan scope: %w", err)
		}
		if err := unmarshalNullableJSON(metaJSON, &scope.Meta); err != nil {
			return nil, fmt.Errorf("unmarshal meta: %w", err)
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

// marshalNullableJSON marshals a value to JSON, returning nil for nil/empty maps.
func marshalNullableJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	// Check if it's an empty Metadata map
	if m, ok := v.(model.Metadata); ok && len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(v)
}

// unmarshalNullableJSON unmarshals JSON data, handling nil gracefully.
func unmarshalNullableJSON(data []byte, dest any) error {
	if data == nil {
		return nil
	}
	return json.Unmarshal(data, dest)
}
