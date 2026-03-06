// Package postgres implements the storage interfaces using PostgreSQL with pgvector and ltree.
package postgres

import (
	"context"
	"embed"
	"fmt"

	"github.com/dpoage/known/storage"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time check that DB satisfies storage.Backend.
var _ storage.Backend = (*DB)(nil)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// txKey is the context key for storing a pgx.Tx.
type txKey struct{}

// DBTX is the common interface between *pgxpool.Pool and pgx.Tx,
// allowing repo methods to operate within or outside a transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// connFromContext returns the transaction from the context if present,
// otherwise falls back to the pool.
func connFromContext(ctx context.Context, pool *pgxpool.Pool) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

// Config holds PostgreSQL connection parameters.
type Config struct {
	// DSN is the PostgreSQL connection string.
	// Example: "postgres://user:pass@localhost:5432/known?sslmode=disable"
	DSN string

	// MaxConns sets the maximum number of connections in the pool.
	// Zero uses pgxpool's default.
	MaxConns int32
}

// DB wraps a pgxpool and provides access to repository implementations.
type DB struct {
	Pool *pgxpool.Pool
	dsn  string
}

// New creates a new DB with a connection pool. The caller must call Close when finished.
func New(ctx context.Context, cfg Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &DB{Pool: pool, dsn: cfg.DSN}, nil
}

// Close releases all pool resources.
func (db *DB) Close() error {
	db.Pool.Close()
	return nil
}

// WithTx runs fn within a database transaction. The transaction is stored in
// the context, so any repo method called with the returned context will
// participate in the same transaction. The transaction is committed if fn
// returns nil, and rolled back otherwise.
//
// If a transaction is already active in the context, fn runs within the
// existing transaction (no nesting).
func (db *DB) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	// If already in a transaction, just run fn directly.
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	txCtx := context.WithValue(ctx, txKey{}, tx)

	if err := fn(txCtx); err != nil {
		// Attempt rollback; if it also fails, wrap both errors.
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("tx error: %w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Migrate runs all pending database migrations.
func (db *DB) Migrate() error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, db.dsn)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

// Entries returns the EntryRepo implementation backed by this DB.
func (db *DB) Entries() storage.EntryRepo {
	return &EntryStore{pool: db.Pool}
}

// Edges returns the EdgeRepo implementation backed by this DB.
func (db *DB) Edges() storage.EdgeRepo {
	return &EdgeStore{pool: db.Pool}
}

// Scopes returns the ScopeRepo implementation backed by this DB.
func (db *DB) Scopes() storage.ScopeRepo {
	return &ScopeStore{pool: db.Pool}
}

// Sessions returns the SessionRepo implementation backed by this DB.
func (db *DB) Sessions() storage.SessionRepo {
	return &SessionStore{pool: db.Pool}
}

// Labels returns the LabelLister implementation backed by this DB.
func (db *DB) Labels() storage.LabelLister {
	return &EntryStore{pool: db.Pool}
}
