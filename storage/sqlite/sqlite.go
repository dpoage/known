// Package sqlite implements the storage interfaces using SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/dpoage/known/storage"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Compile-time check that DB satisfies storage.Backend.
var _ storage.Backend = (*DB)(nil)

// txKey is the context key for storing a *sql.Tx.
type txKey struct{}

// DB wraps a database/sql.DB and provides access to repository implementations.
type DB struct {
	db  *sql.DB
	dsn string
}

// New creates a new SQLite DB. The DSN is a file path (or ":memory:" for testing).
func New(ctx context.Context, dsn string) (*DB, error) {
	pragmas := "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"

	// The ncruces driver requires file: URI format.
	var uri string
	if dsn == ":memory:" {
		uri = "file::memory:?" + pragmas
	} else {
		uri = "file:" + dsn + "?" + pragmas
	}

	db, err := sql.Open("sqlite3", uri)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite single-writer constraint.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return &DB{db: db, dsn: dsn}, nil
}

// Close releases all database resources.
func (d *DB) Close() error {
	return d.db.Close()
}

// WithTx runs fn within a database transaction. If a transaction is already
// active in the context, fn runs within the existing transaction.
func (d *DB) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return fn(ctx)
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	txCtx := context.WithValue(ctx, txKey{}, tx)

	if err := fn(txCtx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx error: %w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Entries returns the EntryRepo implementation.
func (d *DB) Entries() storage.EntryRepo {
	return &EntryStore{db: d.db}
}

// Edges returns the EdgeRepo implementation.
func (d *DB) Edges() storage.EdgeRepo {
	return &EdgeStore{db: d.db}
}

// Scopes returns the ScopeRepo implementation.
func (d *DB) Scopes() storage.ScopeRepo {
	return &ScopeStore{db: d.db}
}

// Migrate runs all pending database migrations.
func (d *DB) Migrate() error {
	// Create migration tracking table.
	_, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Get current version.
	var current int
	row := d.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("query schema version: %w", err)
	}

	// Read and apply pending migrations.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for i, entry := range entries {
		version := i + 1
		if version <= current {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %d: %w", version, err)
		}

		tx, err := d.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("run migration %d: %w", version, err)
		}

		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}

	return nil
}

// DBTX is the common interface between *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// connFromContext returns the transaction from the context if present,
// otherwise falls back to the database connection.
func connFromContext(ctx context.Context, db *sql.DB) DBTX {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}
	return db
}
