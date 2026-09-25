// Package pgtest opens an ent client on a throwaway PostgreSQL schema for
// repository integration tests. Tests are skipped unless
// NEOBOX_TEST_POSTGRES_DSN is set, e.g.
//
//	NEOBOX_TEST_POSTGRES_DSN=postgres://neobox:neobox@localhost:5432/neobox?sslmode=disable
package pgtest

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"go.orx.me/apps/neo-box/internal/ent"
)

const dsnEnv = "NEOBOX_TEST_POSTGRES_DSN"

// NewClient creates a fresh schema, migrates it, and returns a client bound
// to it. The schema is dropped when the test ends.
func NewClient(t *testing.T) *ent.Client {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skip(dsnEnv + " is not set")
	}
	ctx := context.Background()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)

	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	return client
}
