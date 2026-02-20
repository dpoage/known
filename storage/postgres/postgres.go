// Package postgres implements the storage interfaces using PostgreSQL with pgvector and ltree.
package postgres

import (
	"context"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

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

	return &DB{Pool: pool}, nil
}

// Close releases all pool resources.
func (db *DB) Close() {
	db.Pool.Close()
}

// Migrate runs all pending database migrations.
func (db *DB) Migrate(dsn string) error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dsn)
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
func (db *DB) Entries() *EntryStore {
	return &EntryStore{pool: db.Pool}
}

// Edges returns the EdgeRepo implementation backed by this DB.
func (db *DB) Edges() *EdgeStore {
	return &EdgeStore{pool: db.Pool}
}

// Scopes returns the ScopeRepo implementation backed by this DB.
func (db *DB) Scopes() *ScopeStore {
	return &ScopeStore{pool: db.Pool}
}
