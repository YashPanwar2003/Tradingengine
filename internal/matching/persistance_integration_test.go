package matching_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"tradingengine/internal/db"
	"tradingengine/internal/matching"
	"tradingengine/internal/order"
	"tradingengine/internal/queue"
	"tradingengine/internal/testutil"
	"tradingengine/pkg/types"
)

// pgStack is one "process lifetime": fresh memory, book, engine and DB worker.
// Only Postgres survives between two stacks, exactly like a real restart.
type pgStack struct {
	storage *db.DBStorage
	mem     order.Store
	ps      *order.PersistentStore
	svc     *order.Service
	engine  *matching.Engine
}

func newPGStack(storage *db.DBStorage) *pgStack {
	mem := order.NewMemoryStore()
	ps := order.NewPersistentStore(mem, storage, 100)
	go ps.Run()
	svc := order.NewService(ps)
	return &pgStack{
		storage: storage,
		mem:     mem,
		ps:      ps,
		svc:     svc,
		engine:  matching.NewEngine(queue.NewOrderQueue(10), svc, 100),
	}
}

func (s *pgStack) place(t *testing.T, o *types.Order) {
	t.Helper()
	if err := s.svc.Place(o); err != nil {
		t.Fatal(err)
	}
	s.engine.HandlePlace(o)
}

// drainTrades only counts: the fill transaction has already persisted the trade.
func (s *pgStack) drainTrades() int {
	n := 0
	for {
		select {
		case <-s.engine.Trades():
			n++
		default:
			return n
		}
	}
}

func mkOrder(client string, side types.Side, price, qty int64, tif types.TimeInForce) *types.Order {
	return types.NewOrder(client, "BTC-USD", side, types.OrderTypeLimit,
		decimal.NewFromInt(price), decimal.NewFromInt(qty), tif)
}

func assertOrderRow(t *testing.T, s *db.DBStorage, id string, status types.OrderStatus, filled int64) {
	t.Helper()
	got, err := s.GetOrderByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != status || !got.Filled.Equal(decimal.NewFromInt(filled)) {
		t.Fatalf("order %s: want %s/filled=%d, got %s/filled=%s",
			id, status, filled, got.Status, got.Filled)
	}
}

func queryCount(t *testing.T, c *db.Client, sql string) int {
	t.Helper()
	var n int
	if err := c.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// assertFillsMatchTrades is THE invariant atomic fills guarantee:
// for every order, filled equals the sum of the trades it took part in.
func assertFillsMatchTrades(t *testing.T, c *db.Client) {
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

func TestPersistence_MatchAndRestart(t *testing.T) {
	client := testutil.NewDB(t)
	storage := db.NewDBStorage(client)

	// ---- run 1: rest a sell of 5, buy 2 against it ----
	run1 := newPGStack(storage)
	sell := mkOrder("seller", types.SideSell, 100, 5, types.TimeInForceGTC)
	buy1 := mkOrder("buyer", types.SideBuy, 100, 2, types.TimeInForceGTC)
	run1.place(t, sell)
	run1.place(t, buy1)

	if n := run1.drainTrades(); n != 1 {
		t.Fatalf("run 1: expected 1 trade, got %d", n)
	}
	run1.ps.Close() // flush, like a clean shutdown

	assertOrderRow(t, storage, sell.Id, types.OrderStatusPartial, 2)
	assertOrderRow(t, storage, buy1.Id, types.OrderStatusCompleted, 2)
	assertFillsMatchTrades(t, client)

	// ---- "restart": brand-new memory, book and engine ----
	run2 := newPGStack(storage)
	active, err := storage.ListActiveOrders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Id != sell.Id {
		t.Fatalf("expected only the partial sell to be active, got %d orders", len(active))
	}
	for _, o := range active {
		if err := run2.mem.Save(o); err != nil { // memory only, no DB insert
			t.Fatal(err)
		}
	}
	run2.engine.Restore(active)

	// ---- run 2: a buy of 3 must trade against the restored remainder ----
	buy2 := mkOrder("buyer", types.SideBuy, 100, 3, types.TimeInForceGTC)
	run2.place(t, buy2)

	if n := run2.drainTrades(); n != 1 {
		t.Fatalf("run 2: expected 1 trade, got %d", n)
	}
	run2.ps.Close()

	assertOrderRow(t, storage, sell.Id, types.OrderStatusCompleted, 5)
	assertOrderRow(t, storage, buy2.Id, types.OrderStatusCompleted, 3)
	if n := queryCount(t, client, "SELECT count(*) FROM trades"); n != 2 {
		t.Fatalf("expected 2 trades in total, got %d", n)
	}
	assertFillsMatchTrades(t, client)
}

// An IOC order fills what it can and the remainder is cancelled, never rested.
func TestPersistence_IOCRemainderIsCancelled(t *testing.T) {
	client := testutil.NewDB(t)
	storage := db.NewDBStorage(client)
	run := newPGStack(storage)

	sell := mkOrder("seller", types.SideSell, 100, 3, types.TimeInForceGTC)
	ioc := mkOrder("buyer", types.SideBuy, 100, 10, types.TimeInForceIOC)
	run.place(t, sell)
	run.place(t, ioc)

	if n := run.drainTrades(); n != 1 {
		t.Fatalf("expected 1 trade, got %d", n)
	}
	run.ps.Close()

	assertOrderRow(t, storage, sell.Id, types.OrderStatusCompleted, 3)
	assertOrderRow(t, storage, ioc.Id, types.OrderStatusCancelled, 3)
	active, _ := storage.ListActiveOrders(context.Background())
	if len(active) != 0 {
		t.Fatalf("nothing should be left resting, got %d active orders", len(active))
	}
	assertFillsMatchTrades(t, client)
}

// A fill-or-kill order that cannot fill completely must trade nothing at all.
func TestPersistence_FOKThatCannotFillMakesNoTrade(t *testing.T) {
	client := testutil.NewDB(t)
	storage := db.NewDBStorage(client)
	run := newPGStack(storage)

	sell := mkOrder("seller", types.SideSell, 100, 3, types.TimeInForceGTC)
	fok := mkOrder("buyer", types.SideBuy, 100, 10, types.TimeInForceFOK)
	run.place(t, sell)
	run.place(t, fok)

	if n := run.drainTrades(); n != 0 {
		t.Fatalf("expected 0 trades, got %d", n)
	}
	run.ps.Close()

	assertOrderRow(t, storage, sell.Id, types.OrderStatusNew, 0)
	assertOrderRow(t, storage, fok.Id, types.OrderStatusCancelled, 0)
	if n := queryCount(t, client, "SELECT count(*) FROM trades"); n != 0 {
		t.Fatalf("expected no trades in the database, got %d", n)
	}
	assertFillsMatchTrades(t, client)
}

// One incoming order that sweeps two price levels makes two fills, each its
// own transaction. The incoming order's total must still equal its trades.
func TestPersistence_MultiLevelSweepKeepsTheInvariant(t *testing.T) {
	client := testutil.NewDB(t)
	storage := db.NewDBStorage(client)
	run := newPGStack(storage)

	sell1 := mkOrder("s1", types.SideSell, 100, 2, types.TimeInForceGTC)
	sell2 := mkOrder("s2", types.SideSell, 101, 2, types.TimeInForceGTC)
	buy := mkOrder("buyer", types.SideBuy, 101, 3, types.TimeInForceGTC)
	run.place(t, sell1)
	run.place(t, sell2)
	run.place(t, buy)

	if n := run.drainTrades(); n != 2 {
		t.Fatalf("expected 2 trades, got %d", n)
	}
	run.ps.Close()

	assertOrderRow(t, storage, sell1.Id, types.OrderStatusCompleted, 2)
	assertOrderRow(t, storage, sell2.Id, types.OrderStatusPartial, 1)
	assertOrderRow(t, storage, buy.Id, types.OrderStatusCompleted, 3)

	// trades execute at the RESTING order's price
	if n := queryCount(t, client, "SELECT count(*) FROM trades WHERE price = 100 AND quantity = 2"); n != 1 {
		t.Fatalf("expected one trade of 2 @ 100, got %d", n)
	}
	if n := queryCount(t, client, "SELECT count(*) FROM trades WHERE price = 101 AND quantity = 1"); n != 1 {
		t.Fatalf("expected one trade of 1 @ 101, got %d", n)
	}
	assertFillsMatchTrades(t, client)
}
