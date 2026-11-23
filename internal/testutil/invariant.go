package testutil

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"tradingengine/internal/db"
)

// AssertFillsMatchTrades checks the invariant atomic fills guarantee:
// for every order, filled equals the sum of the trades it took part in.
func AssertFillsMatchTrades(t *testing.T, c *db.Client) {
	t.Helper()
	rows, err := c.Query(context.Background(), `
		SELECT o.id::text, o.filled::text,
		       COALESCE((SELECT sum(quantity) FROM trades
		                 WHERE buy_order_id = o.id OR sell_order_id = o.id), 0)::text
		FROM orders o`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, filled, traded string
		if err := rows.Scan(&id, &filled, &traded); err != nil {
			t.Fatal(err)
		}
		if !decimal.RequireFromString(filled).Equal(decimal.RequireFromString(traded)) {
			t.Fatalf("order %s: filled=%s but its trades sum to %s", id, filled, traded)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
