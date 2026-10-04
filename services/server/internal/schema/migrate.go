package schema

import (
	"context"
	_ "embed"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

//go:embed baseline.sql
var baseline string

//go:embed budgets.sql
var rateBudgets string

// Migrate bootstraps the original relational contract without a Python runtime.
// Existing databases are adopted; no table or user data is dropped or reset.
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951208)`); err != nil {
		return err
	}
	var exists bool
	var searchPath string
	if err = tx.QueryRow(ctx, `SELECT current_setting('search_path')`).Scan(&searchPath); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT to_regclass('accounts_user') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		// pg_dump's session settings must not leak into a reused serving
		// connection after bootstrap commits or rolls back.
		localBaseline := strings.ReplaceAll(baseline, "\nSET ", "\nSET LOCAL ")
		// PostGIS images initialize their extension schemas before the app
		// bootstrap. Adopt those schemas without recreating or resetting them.
		localBaseline = strings.ReplaceAll(localBaseline, "CREATE SCHEMA ", "CREATE SCHEMA IF NOT EXISTS ")
		localBaseline = strings.Replace(localBaseline, "set_config('search_path', '', false)", "set_config('search_path', '', true)", 1)
		if _, err = tx.Exec(ctx, localBaseline, pgx.QueryExecModeSimpleProtocol); err != nil {
			return err
		}
		// PostGIS extension scripts can set search_path at session scope even
		// when our dump settings are local. Restore the original session value
		// before commit; transaction rollback restores it on any failure.
		if _, err = tx.Exec(ctx, `SELECT pg_catalog.set_config('search_path',$1,false)`, searchPath); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS go_backend_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO go_backend_migrations(version) VALUES('django-477d43a-contract-v1') ON CONFLICT DO NOTHING`, pgx.QueryExecModeSimpleProtocol); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, rateBudgets, pgx.QueryExecModeSimpleProtocol); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO go_backend_migrations(version) VALUES('go-shared-rate-budgets-v1') ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
