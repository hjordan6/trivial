// Package db owns the PostgreSQL connection pool and schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DBTX is the subset of pgx used by store functions. Both *pgxpool.Pool and
// pgx.Tx satisfy it, which lets every store function run either against the
// pool or inside a test transaction that is rolled back.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Direction selects which way Migrate moves the schema.
type Direction int

const (
	// Up applies all pending migrations.
	Up Direction = iota
	// Down rolls back the most recent migration.
	Down
)

// Open creates a connection pool and verifies it can reach the database.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies or rolls back schema migrations. It takes a Postgres
// advisory lock first so concurrent callers — several test binaries starting
// at once, or two application instances booting together — serialize instead
// of racing goose's version table.
func Migrate(ctx context.Context, pool *pgxpool.Pool, direction Direction) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	const lockID = 4815162342
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID)
	}()

	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	switch direction {
	case Up:
		if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	case Down:
		if err := goose.DownContext(ctx, sqlDB, "migrations"); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
	default:
		return fmt.Errorf("unknown migration direction %d", direction)
	}
	return nil
}
