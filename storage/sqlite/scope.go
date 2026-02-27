package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dpoage/known/model"
	"github.com/dpoage/known/storage"
)

// ScopeStore implements storage.ScopeRepo using SQLite.
type ScopeStore struct {
	db *sql.DB
}

func (s *ScopeStore) conn(ctx context.Context) DBTX {
	return connFromContext(ctx, s.db)
}

// Upsert creates or updates a scope.
func (s *ScopeStore) Upsert(ctx context.Context, scope *model.Scope) error {
	metaJSON, err := marshalNullableJSON(scope.Meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	_, err = s.conn(ctx).ExecContext(ctx, `
		INSERT INTO scopes (path, meta, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			meta = excluded.meta
	`, scope.Path, metaJSON, formatTime(scope.CreatedAt))
	if err != nil {
		return fmt.Errorf("upsert scope: %w", err)
	}
	return nil
}

// EnsureHierarchy ensures that all ancestor scopes exist for a given scope path.
func (s *ScopeStore) EnsureHierarchy(ctx context.Context, path string) error {
	segments := strings.Split(path, ".")
	for i := range segments {
		ancestorPath := strings.Join(segments[:i+1], ".")
		_, err := s.conn(ctx).ExecContext(ctx, `
			INSERT INTO scopes (path, created_at)
			VALUES (?, ?)
			ON CONFLICT (path) DO NOTHING
		`, ancestorPath, formatTime(time.Now()))
		if err != nil {
			return fmt.Errorf("ensure scope %q: %w", ancestorPath, err)
		}
	}
	return nil
}

// Get retrieves a scope by path.
func (s *ScopeStore) Get(ctx context.Context, path string) (*model.Scope, error) {
	row := s.conn(ctx).QueryRowContext(ctx, `
		SELECT path, meta, created_at
		FROM scopes
		WHERE path = ?
	`, path)

	var scope model.Scope
	var metaJSON []byte
	var createdStr string
	if err := row.Scan(&scope.Path, &metaJSON, &createdStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("get scope: %w", err)
	}

	scope.CreatedAt = parseTime(createdStr)
	if err := unmarshalNullableJSON(metaJSON, &scope.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}

	return &scope, nil
}

// Delete removes a scope by path.
func (s *ScopeStore) Delete(ctx context.Context, path string) error {
	result, err := s.conn(ctx).ExecContext(ctx, `DELETE FROM scopes WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("delete scope: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// List returns all scopes ordered by path.
func (s *ScopeStore) List(ctx context.Context) ([]model.Scope, error) {
	rows, err := s.conn(ctx).QueryContext(ctx, `
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
// Uses LIKE pattern matching: parent.X but NOT parent.X.Y
func (s *ScopeStore) ListChildren(ctx context.Context, parentPath string) ([]model.Scope, error) {
	rows, err := s.conn(ctx).QueryContext(ctx, `
		SELECT path, meta, created_at
		FROM scopes
		WHERE path LIKE ? AND path NOT LIKE ?
		ORDER BY path
	`, parentPath+".%", parentPath+".%.%")
	if err != nil {
		return nil, fmt.Errorf("list children: %w", err)
	}
	defer rows.Close()
	return scanScopes(rows)
}

// ListDescendants returns all descendant scopes of the given ancestor path.
func (s *ScopeStore) ListDescendants(ctx context.Context, ancestorPath string) ([]model.Scope, error) {
	rows, err := s.conn(ctx).QueryContext(ctx, `
		SELECT path, meta, created_at
		FROM scopes
		WHERE path LIKE ? AND path <> ?
		ORDER BY path
	`, ancestorPath+".%", ancestorPath)
	if err != nil {
		return nil, fmt.Errorf("list descendants: %w", err)
	}
	defer rows.Close()
	return scanScopes(rows)
}

// DeleteEmpty removes scopes that have no entries and no descendant entries.
func (s *ScopeStore) DeleteEmpty(ctx context.Context) (int64, error) {
	result, err := s.conn(ctx).ExecContext(ctx, `
		DELETE FROM scopes
		WHERE path NOT IN (SELECT DISTINCT scope FROM entries)
		  AND NOT EXISTS (SELECT 1 FROM entries WHERE entries.scope LIKE scopes.path || '.%')
	`)
	if err != nil {
		return 0, fmt.Errorf("delete empty scopes: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func scanScopes(rows *sql.Rows) ([]model.Scope, error) {
	var scopes []model.Scope
	for rows.Next() {
		var scope model.Scope
		var metaJSON []byte
		var createdStr string
		if err := rows.Scan(&scope.Path, &metaJSON, &createdStr); err != nil {
			return nil, fmt.Errorf("scan scope: %w", err)
		}
		scope.CreatedAt = parseTime(createdStr)
		if err := unmarshalNullableJSON(metaJSON, &scope.Meta); err != nil {
			return nil, fmt.Errorf("unmarshal meta: %w", err)
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

// Time formatting helpers for ISO 8601 storage in SQLite.

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		// Fallback: try RFC3339
		t, _ = time.Parse(time.RFC3339Nano, s)
	}
	return t
}

func formatNullableTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}

func parseNullableTime(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t := parseTime(*s)
	return &t
}

// JSON helpers.

func marshalNullableJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(model.Metadata); ok && len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(v)
}

func unmarshalNullableJSON(data []byte, dest any) error {
	if data == nil {
		return nil
	}
	return json.Unmarshal(data, dest)
}
