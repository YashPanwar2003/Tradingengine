package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradingengine/internal/db"
	"tradingengine/internal/testutil"
	"tradingengine/pkg/types"
)

func limit(side types.Side, price, qty int64) *types.Order {
	return types.NewOrder("c1", "BTC-USD", side, types.OrderTypeLimit,
		decimal.NewFromInt(price), decimal.NewFromInt(qty), types.TimeInForceGTC)
}

func count(t *testing.T, c *db.Client, table string) int {
	t.Helper()
	var n int
	if err := c.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// applyFill mimics what the service does to an order before it is persisted.
func applyFill(o *types.Order, filled int64, status types.OrderStatus) {
	o.Filled = decimal.NewFromInt(filled)
	o.Status = status
	o.UpdatedAt = time.Now().UTC()
}

func tradeFor(buy, sell *types.Order, qty int64) *types.Trade {
	return types.NewTrade("BTC-USD", buy.Id, sell.Id,
		decimal.NewFromInt(100), decimal.NewFromInt(qty), time.Now().UTC())
}

// ---- orders ----

func TestOrder_RoundTrip_Limit(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	o := types.NewOrder("c1", "BTC-USD", types.SideBuy, types.OrderTypeLimit,
		decimal.RequireFromString("100.12345678"), decimal.RequireFromString("2.5"), types.TimeInForceIOC)
	if err := s.InsertOrder(ctx, o); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetOrderByID(ctx, o.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != o.Id || got.ClientId != "c1" || got.Symbol != "BTC-USD" ||
		got.Side != types.SideBuy || got.Type != types.OrderTypeLimit ||
		got.Status != types.OrderStatusNew || got.TimeInForce != types.TimeInForceIOC {
		t.Fatalf("fields did not round-trip: %+v", got)
	}
	if !got.Price.Equal(o.Price) || !got.Quantity.Equal(o.Quantity) || !got.Filled.IsZero() {
		t.Fatalf("decimals did not round-trip: %+v", got)
	}
	if d := got.CreatedAt.Sub(o.CreatedAt); d > time.Millisecond || d < -time.Millisecond {
		t.Fatalf("created_at drifted by %v", d)
	}
}

func TestOrder_RoundTrip_MarketStoresNullPrice(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	o := types.NewOrder("c1", "BTC-USD", types.SideSell, types.OrderTypeMarket,
		decimal.Zero, decimal.NewFromInt(1), types.TimeInForceIOC)
	if err := s.InsertOrder(ctx, o); err != nil {
		t.Fatal(err)
	}

	var isNull bool
	if err := c.QueryRow(ctx, "SELECT price IS NULL FROM orders WHERE id = $1::uuid", o.Id).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if !isNull {
		t.Fatal("market order price should be stored as NULL")
	}
	got, err := s.GetOrderByID(ctx, o.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Price.IsZero() {
		t.Fatalf("NULL price should read back as zero, got %s", got.Price)
	}
}

func TestUpdateOrderFill(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	o := limit(types.SideBuy, 100, 5)
	_ = s.InsertOrder(ctx, o)

	applyFill(o, 2, types.OrderStatusPartial)
	if err := s.UpdateOrderFill(ctx, o); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetOrderByID(ctx, o.Id)
	if got.Status != types.OrderStatusPartial || !got.Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("update not persisted: %+v", got)
	}
}

func TestUpdateOrderFill_UnknownOrder(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)

	ghost := limit(types.SideBuy, 100, 5)
	ghost.Id = uuid.New().String()
	err := s.UpdateOrderFill(context.Background(), ghost)
	if !errors.Is(err, db.ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
}

func TestListActiveOrders_OnlyActiveOldestFirst(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	base := time.Now().UTC().Add(-time.Hour)
	mk := func(status types.OrderStatus, offset time.Duration) *types.Order {
		o := limit(types.SideSell, 100, 5)
		o.Status = status
		o.CreatedAt = base.Add(offset)
		o.UpdatedAt = o.CreatedAt
		if err := s.InsertOrder(ctx, o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	second := mk(types.OrderStatusPartial, 2*time.Minute)
	first := mk(types.OrderStatusNew, 1*time.Minute)
	mk(types.OrderStatusCompleted, 3*time.Minute)
	mk(types.OrderStatusCancelled, 4*time.Minute)
	mk(types.OrderStatusRejected, 5*time.Minute)

	got, err := s.ListActiveOrders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Id != first.Id || got[1].Id != second.Id {
		t.Fatalf("expected [first, second] in time order, got %d orders", len(got))
	}
}

func TestConstraints_RejectImpossibleOrders(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	overfilled := limit(types.SideBuy, 100, 5)
	overfilled.Filled = decimal.NewFromInt(6)
	if err := s.InsertOrder(ctx, overfilled); err == nil {
		t.Fatal("filled > quantity should violate a CHECK constraint")
	}

	badSide := limit(types.SideBuy, 100, 5)
	badSide.Side = "SIDEWAYS"
	if err := s.InsertOrder(ctx, badSide); err == nil {
		t.Fatal("unknown side should violate a CHECK constraint")
	}
}

// ---- trades ----

func TestInsertTrade_IsIdempotent(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	buy, sell := limit(types.SideBuy, 100, 2), limit(types.SideSell, 100, 2)
	_ = s.InsertOrder(ctx, buy)
	_ = s.InsertOrder(ctx, sell)

	tr := tradeFor(buy, sell, 2)
	for i := 0; i < 2; i++ { // second call simulates a retry after a timeout
		if err := s.InsertTrade(ctx, tr); err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	if n := count(t, c, "trades"); n != 1 {
		t.Fatalf("expected 1 trade row, got %d", n)
	}
}

func TestInsertTrade_UnknownOrderViolatesForeignKey(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)

	tr := types.NewTrade("BTC-USD", uuid.New().String(), uuid.New().String(),
		decimal.NewFromInt(100), decimal.NewFromInt(1), time.Now().UTC())
	if err := s.InsertTrade(context.Background(), tr); err == nil {
		t.Fatal("a trade pointing at missing orders must be rejected")
	}
}

func TestInsertTrade_SelfTradeIsRejected(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	o := limit(types.SideBuy, 100, 2)
	_ = s.InsertOrder(ctx, o)

	if err := s.InsertTrade(ctx, tradeFor(o, o, 1)); err == nil {
		t.Fatal("buy_order_id = sell_order_id should violate a CHECK constraint")
	}
}

// ---- atomic fills ----

func TestRecordFill_WritesBothOrdersAndTradeAtomically(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	buy, sell := limit(types.SideBuy, 100, 2), limit(types.SideSell, 100, 5)
	_ = s.InsertOrder(ctx, buy)
	_ = s.InsertOrder(ctx, sell)

	applyFill(buy, 2, types.OrderStatusCompleted)
	applyFill(sell, 2, types.OrderStatusPartial)
	tr := tradeFor(buy, sell, 2)

	for i := 0; i < 2; i++ { // the second call simulates a retry after a timeout
		if err := s.RecordFill(ctx, buy, sell, tr); err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}

	gb, _ := s.GetOrderByID(ctx, buy.Id)
	gs, _ := s.GetOrderByID(ctx, sell.Id)
	if gb.Status != types.OrderStatusCompleted || !gb.Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("buy: %s / %s", gb.Status, gb.Filled)
	}
	if gs.Status != types.OrderStatusPartial || !gs.Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("sell: %s / %s", gs.Status, gs.Filled)
	}
	if n := count(t, c, "trades"); n != 1 {
		t.Fatalf("expected exactly 1 trade after a retry, got %d", n)
	}
}

// The core guarantee: if the LAST statement fails, the two order updates
// that already ran inside the transaction must be rolled back.
func TestRecordFill_RollsBackEverythingWhenTheTradeFails(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	buy, sell := limit(types.SideBuy, 100, 2), limit(types.SideSell, 100, 5)
	_ = s.InsertOrder(ctx, buy)
	_ = s.InsertOrder(ctx, sell)

	applyFill(buy, 2, types.OrderStatusCompleted)
	applyFill(sell, 2, types.OrderStatusPartial)

	// quantity 0 violates the CHECK on trades, so the third statement fails
	bad := tradeFor(buy, sell, 0)
	if err := s.RecordFill(ctx, buy, sell, bad); err == nil {
		t.Fatal("expected RecordFill to fail")
	}

	for _, id := range []string{buy.Id, sell.Id} {
		got, _ := s.GetOrderByID(ctx, id)
		if got.Status != types.OrderStatusNew || !got.Filled.IsZero() {
			t.Fatalf("order %s changed despite the rollback: %s / %s", id, got.Status, got.Filled)
		}
	}
	if n := count(t, c, "trades"); n != 0 {
		t.Fatalf("expected 0 trades, got %d", n)
	}
}

// If one of the two orders does not exist, the other must not be touched either.
func TestRecordFill_UnknownOrderChangesNothing(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	buy := limit(types.SideBuy, 100, 2)
	_ = s.InsertOrder(ctx, buy)
	ghost := limit(types.SideSell, 100, 5) // never inserted

	applyFill(buy, 2, types.OrderStatusCompleted)
	applyFill(ghost, 2, types.OrderStatusPartial)

	err := s.RecordFill(ctx, buy, ghost, tradeFor(buy, ghost, 2))
	if !errors.Is(err, db.ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}

	got, _ := s.GetOrderByID(ctx, buy.Id)
	if got.Status != types.OrderStatusNew || !got.Filled.IsZero() {
		t.Fatalf("buy changed despite the rollback: %s / %s", got.Status, got.Filled)
	}
	if n := count(t, c, "trades"); n != 0 {
		t.Fatalf("expected 0 trades, got %d", n)
	}
}

// ---- migrations ----

func TestMigrate_IsIdempotent(t *testing.T) {
	c := testutil.NewDB(t) // already ran Migrate once
	before := count(t, c, "schema_migrations")

	if err := c.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if after := count(t, c, "schema_migrations"); after != before || before < 2 {
		t.Fatalf("expected migrations recorded once (>=2 files), before=%d after=%d", before, after)
	}
}
func TestGetOrderByID_UnknownAndMalformedIDsAreNotFound(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)

	for _, id := range []string{uuid.New().String(), "not-a-uuid", ""} {
		_, err := s.GetOrderByID(context.Background(), id)
		if !errors.Is(err, types.ErrOrderNotFound) {
			t.Fatalf("id %q: expected ErrOrderNotFound, got %v", id, err)
		}
	}
}

func TestListOrdersByClient_NewestFirstLimitedAndScoped(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	base := time.Now().UTC().Add(-time.Hour)
	mk := func(client string, offset time.Duration) *types.Order {
		o := types.NewOrder(client, "BTC-USD", types.SideBuy, types.OrderTypeLimit,
			decimal.NewFromInt(100), decimal.NewFromInt(1), types.TimeInForceGTC)
		o.CreatedAt = base.Add(offset)
		o.UpdatedAt = o.CreatedAt
		if err := s.InsertOrder(ctx, o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	mk("c1", 1*time.Minute)
	middle := mk("c1", 2*time.Minute)
	newest := mk("c1", 3*time.Minute)
	mk("c2", 4*time.Minute) // someone else's order must not appear

	got, err := s.ListOrdersByClient(ctx, "c1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Id != newest.Id || got[1].Id != middle.Id {
		t.Fatalf("expected [newest, middle], got %d orders", len(got))
	}
}
func TestListTrades_NewestFirstLimitedAndScopedToSymbol(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	buy, sell := limit(types.SideBuy, 100, 10), limit(types.SideSell, 100, 10)
	_ = s.InsertOrder(ctx, buy)
	_ = s.InsertOrder(ctx, sell)

	base := time.Now().UTC().Add(-time.Hour)
	mk := func(symbol string, offset time.Duration) *types.Trade {
		tr := types.NewTrade(symbol, buy.Id, sell.Id, decimal.NewFromInt(100), decimal.NewFromInt(1), base.Add(offset))
		if err := s.InsertTrade(ctx, tr); err != nil {
			t.Fatal(err)
		}
		return tr
	}
	mk("BTC-USD", 1*time.Minute)
	middle := mk("BTC-USD", 2*time.Minute)
	newest := mk("BTC-USD", 3*time.Minute)
	mk("ETH-USD", 4*time.Minute) // another symbol must not appear

	got, err := s.ListTrades(ctx, "BTC-USD", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Id != newest.Id || got[1].Id != middle.Id {
		t.Fatalf("expected [newest, middle], got %d trades", len(got))
	}
	if !got[0].Price.Equal(decimal.NewFromInt(100)) || got[0].BuyOrderId != buy.Id {
		t.Fatalf("trade fields did not round-trip: %+v", got[0])
	}

	none, err := s.ListTrades(ctx, "NOPE-USD", 10)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("expected an empty, non-nil slice, got %v / %v", none, err)
	}
}

func TestListTradesByOrder_BothSidesOldestFirst(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	buy1, buy2 := limit(types.SideBuy, 100, 5), limit(types.SideBuy, 100, 5)
	sell := limit(types.SideSell, 100, 10)
	for _, o := range []*types.Order{buy1, buy2, sell} {
		if err := s.InsertOrder(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	base := time.Now().UTC().Add(-time.Hour)
	mk := func(buy *types.Order, offset time.Duration) *types.Trade {
		tr := types.NewTrade("BTC-USD", buy.Id, sell.Id, decimal.NewFromInt(100), decimal.NewFromInt(1), base.Add(offset))
		if err := s.InsertTrade(ctx, tr); err != nil {
			t.Fatal(err)
		}
		return tr
	}
	t1 := mk(buy1, 1*time.Minute)
	t2 := mk(buy2, 2*time.Minute)
	t3 := mk(buy1, 3*time.Minute)

	ids := func(trades []*types.Trade) []string {
		out := make([]string, 0, len(trades))
		for _, tr := range trades {
			out = append(out, tr.Id)
		}
		return out
	}
	check := func(name string, got []*types.Trade, want ...*types.Trade) {
		t.Helper()
		g := ids(got)
		if len(g) != len(want) {
			t.Fatalf("%s: expected %d trades, got %d", name, len(want), len(g))
		}
		for i := range want {
			if g[i] != want[i].Id {
				t.Fatalf("%s: wrong order at %d", name, i)
			}
		}
	}

	forSell, err := s.ListTradesByOrder(ctx, sell.Id)
	if err != nil {
		t.Fatal(err)
	}
	check("sell", forSell, t1, t2, t3) // it sits on the sell side of every trade

	forBuy1, _ := s.ListTradesByOrder(ctx, buy1.Id)
	check("buy1", forBuy1, t1, t3) // and on the buy side here

	forBuy2, _ := s.ListTradesByOrder(ctx, buy2.Id)
	check("buy2", forBuy2, t2)

	for _, id := range []string{"not-a-uuid", ""} {
		if _, err := s.ListTradesByOrder(ctx, id); !errors.Is(err, types.ErrOrderNotFound) {
			t.Fatalf("id %q: expected ErrOrderNotFound, got %v", id, err)
		}
	}
}
func TestClientOrderId_UniquePerClient(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	mk := func(client, key string) *types.Order {
		o := limit(types.SideBuy, 100, 1)
		o.ClientId, o.ClientOrderId = client, key
		return o
	}

	if err := s.InsertOrder(ctx, mk("c1", "k1")); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertOrder(ctx, mk("c1", "k1")); !errors.Is(err, types.ErrDuplicateClientOrderId) {
		t.Fatalf("same client and key must be a duplicate, got %v", err)
	}
	if err := s.InsertOrder(ctx, mk("c2", "k1")); err != nil {
		t.Fatalf("another client may reuse the key: %v", err)
	}
	if err := s.InsertOrder(ctx, mk("c1", "k2")); err != nil {
		t.Fatalf("another key is fine: %v", err)
	}
	for i := 0; i < 3; i++ { // identical orders WITHOUT a key are all accepted
		if err := s.InsertOrder(ctx, mk("c1", "")); err != nil {
			t.Fatalf("keyless order %d: %v", i, err)
		}
	}
	if n := count(t, c, "orders"); n != 6 {
		t.Fatalf("expected 6 orders, got %d", n)
	}
}

func TestInsertOrder_SameIdIsNotReportedAsADuplicateKey(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	o := limit(types.SideBuy, 100, 1) // no key
	_ = s.InsertOrder(ctx, o)
	err := s.InsertOrder(ctx, o) // primary key clash
	if err == nil || errors.Is(err, types.ErrDuplicateClientOrderId) {
		t.Fatalf("expected a plain insert error, got %v", err)
	}
}

func TestFindByClientOrderID(t *testing.T) {
	c := testutil.NewDB(t)
	s := db.NewDBStorage(c)
	ctx := context.Background()

	o := limit(types.SideBuy, 100, 1)
	o.ClientOrderId = "k1"
	_ = s.InsertOrder(ctx, o)

	got, err := s.FindByClientOrderID(ctx, "c1", "k1")
	if err != nil || got.Id != o.Id || got.ClientOrderId != "k1" {
		t.Fatalf("expected the original order, got %+v / %v", got, err)
	}
	if _, err := s.FindByClientOrderID(ctx, "c1", "missing"); !errors.Is(err, types.ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
}
