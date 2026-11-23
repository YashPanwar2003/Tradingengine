package testutil

import (
	"context"
	"os"
	"strings"
	"testing"

	"tradingengine/internal/db"
)

// NewDB returns a migrated, empty database. Tests are skipped when
// TEST_DATABASE_URL is not set, so plain `go test` still works without Postgres.
func NewDB(t *testing.T) *db.Client {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if !strings.Contains(dsn, "_test") {
		t.Fatalf("refusing to wipe %q: database name must contain _test", dsn)
	}
	t.Setenv("DATABASE_URL", dsn) // NewPostgresPool reads this variable

	ctx := context.Background()
	pool, err := db.NewPostgresPool(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	client := db.NewClient(pool)
	t.Cleanup(client.Close)

	if err := client.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := client.Exec(ctx, "TRUNCATE trades, orders"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return client
}
