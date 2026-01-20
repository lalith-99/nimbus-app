package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	migrationsDir := os.Getenv("MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "/migrations"
	}

	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol // allow multi-statement migrations
	cfg.ConnConfig.RuntimeParams["application_name"] = "nimbus-migrator"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	if err := ensureSchemaTable(ctx, pool); err != nil {
		log.Fatalf("ensure schema_migrations: %v", err)
	}

	applied, skipped, err := applyMigrations(ctx, pool, migrationsDir)
	if err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	log.Printf("migrations complete (applied=%d, skipped=%d)", applied, skipped)
}

func ensureSchemaTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS schema_migrations (
            name TEXT PRIMARY KEY,
            applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        );
    `)
	return err
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) (int, int, error) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return 0, 0, fmt.Errorf("read migrations dir %s: %w", migrationsDir, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	applied := 0
	skipped := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		name := entry.Name()

		alreadyApplied, err := isApplied(ctx, pool, name)
		if err != nil {
			return applied, skipped, fmt.Errorf("check applied %s: %w", name, err)
		}
		if alreadyApplied {
			log.Printf("skip %s (already applied)", name)
			skipped++
			continue
		}

		contents, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return applied, skipped, fmt.Errorf("read %s: %w", name, err)
		}

		log.Printf("applying %s", name)
		start := time.Now()

		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			return applied, skipped, fmt.Errorf("execute %s: %w", name, err)
		}

		if err := markApplied(ctx, pool, name); err != nil {
			return applied, skipped, fmt.Errorf("mark applied %s: %w", name, err)
		}

		applied++
		log.Printf("applied %s in %s", name, time.Since(start).Round(time.Millisecond))
	}

	return applied, skipped, nil
}

func isApplied(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)", name).Scan(&exists)
	return exists, err
}

func markApplied(ctx context.Context, pool *pgxpool.Pool, name string) error {
	_, err := pool.Exec(ctx, "INSERT INTO schema_migrations(name) VALUES($1) ON CONFLICT DO NOTHING", name)
	return err
}
