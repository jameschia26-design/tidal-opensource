package main

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMigrationUpgradeMatchesFresh covers B12: an applied migration file is never edited, an upgraded
// database matches a fresh one, and a changed applied file is refused.
func TestMigrationUpgradeMatchesFresh(t *testing.T) {
	_, pool := testApp(t)
	ctx := context.Background()

	// The released shape: everything up to the last shipped migration, then the current files on top.
	released := fstest.MapFS{}
	all, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.Name() > "098" {
			continue
		}
		body, err := fs.ReadFile(migrationFiles, "migrations/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		released["migrations/"+e.Name()] = &fstest.MapFile{Data: body}
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := migrateFS(ctx, pool, released); err != nil {
		t.Fatalf("released schema: %v", err)
	}
	if err := migrateFS(ctx, pool, migrationFiles); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	upgraded := schemaSignature(t, pool)

	// A fresh database built from the same files must look identical.
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := migrateFS(ctx, pool, migrationFiles); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	fresh := schemaSignature(t, pool)
	if upgraded != fresh {
		t.Fatalf("upgraded schema differs from fresh\nupgraded:\n%s\nfresh:\n%s", upgraded, fresh)
	}

	// Changing an applied migration is refused instead of silently ignored.
	tampered := fstest.MapFS{}
	for _, e := range all {
		body, err := fs.ReadFile(migrationFiles, "migrations/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == "001_init.sql" {
			body = append(body, []byte("\n-- edited after release\n")...)
		}
		tampered["migrations/"+e.Name()] = &fstest.MapFile{Data: body}
	}
	if err := migrateFS(ctx, pool, tampered); err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("edited applied migration was not refused: %v", err)
	}
}

// schemaSignature lists every column and constraint so two databases can be compared exactly.
func schemaSignature(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	parts := []string{}
	rows, err := pool.Query(ctx, `SELECT table_name||'.'||column_name||':'||data_type||':'||is_nullable||':'||COALESCE(column_default,'')
		FROM information_schema.columns WHERE table_schema='public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		parts = append(parts, line)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	crows, err := pool.Query(ctx, `SELECT c.conname FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace
		WHERE n.nspname='public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for crows.Next() {
		var name string
		if err := crows.Scan(&name); err != nil {
			crows.Close()
			t.Fatal(err)
		}
		parts = append(parts, "constraint "+name)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}
