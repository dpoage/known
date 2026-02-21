package cmd

import (
	"context"
	"strings"

	"github.com/dpoage/known/storage"
	"github.com/dpoage/known/storage/postgres"
	"github.com/dpoage/known/storage/sqlite"
)

// newBackend creates a storage backend based on the DSN scheme.
// Lives in cmd/ to avoid circular imports (storage → storage/postgres → storage).
func newBackend(ctx context.Context, dsn string) (storage.Backend, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return postgres.New(ctx, postgres.Config{DSN: dsn})
	}
	// Default: treat the DSN as a SQLite file path (or ":memory:").
	return sqlite.New(ctx, dsn)
}
