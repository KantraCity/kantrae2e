// Package db opens the shared Postgres instance and applies a service's
// migrations inside its own schema (one instance, logically split by schema).
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open connects to dsn with search_path set to schema and runs every *.sql
// file of migrations (in lexical order) that has not been applied yet.
func Open(ctx context.Context, dsn, schema string, migrations fs.FS) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, pool, schema, migrations); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func migrate(ctx context.Context, pool *pgxpool.Pool, schema string, migrations fs.FS) error {
	files, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Serialize concurrent starts of the same service.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", schema); err != nil {
			return err
		}
		ident := pgx.Identifier{schema}.Sanitize()
		if _, err := tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+ident); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+ident+
			".schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"); err != nil {
			return err
		}
		for _, f := range files {
			var done bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+ident+".schema_migrations WHERE name=$1)", f).Scan(&done); err != nil {
				return err
			}
			if done {
				continue
			}
			sql, err := fs.ReadFile(migrations, f)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+ident+"; "+string(sql)); err != nil {
				return fmt.Errorf("migration %s: %w", f, err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO "+ident+".schema_migrations (name) VALUES ($1)", f); err != nil {
				return err
			}
		}
		return nil
	})
}

// IsUniqueViolation reports a unique constraint violation (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
