package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockKey serializes migrations across replicas (pg_advisory_lock).
const migrationLockKey = 727001

// migrate applies every embedded migration not yet recorded, in filename order, each in its own transaction.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrateFS(ctx, pool, migrationFiles)
}

// migrateFS is migrate with an explicit file set, so tests can simulate an older or altered release.
func migrateFS(ctx context.Context, pool *pgxpool.Pool, files fs.FS) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	// Applied migrations are immutable: the checksum catches an edited file that databases already ran.
	if _, err := conn.Exec(ctx, `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum text NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		var applied string
		var done bool
		if err := conn.QueryRow(ctx, "SELECT checksum, true FROM schema_migrations WHERE name=$1", e.Name()).Scan(&applied, &done); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		body, err := fs.ReadFile(files, "migrations/"+e.Name())
		if err != nil {
			return err
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(body))
		if done {
			if applied == "" {
				// Rows recorded before checksums existed adopt the current file; nothing else can be checked.
				if _, err := conn.Exec(ctx, `UPDATE schema_migrations SET checksum=$2 WHERE name=$1`, e.Name(), sum); err != nil {
					return err
				}
				continue
			}
			if applied != sum {
				return fmt.Errorf("migration %s changed after it was applied (checksum %s, file %s)", e.Name(), applied, sum)
			}
			continue
		}
		if err := applyMigration(ctx, conn, e.Name(), string(body), sum); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, name, sql, checksum string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (name, checksum) VALUES ($1, $2)", name, checksum); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
