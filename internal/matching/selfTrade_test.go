package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradingengine/internal/order"
	"tradingengine/internal/queue"
	"tradingengine/pkg/types"
)

// stpOrder submits an order and returns only its id: from the moment it is
// queued the engine owns the object.
func stpOrder(t *testing.T, svc *order.Service, q *queue.OrderQueue, client string,
	side types.Side, typ types.OrderType, tif types.TimeInForce, price, qty int64) string {
	t.Helper()
	o := types.NewOrder(client, "BTC-USD", side, typ, decimal.NewFromInt(price), decimal.NewFromInt(qty), tif)
	if err := svc.Place(o); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(o); err != nil {
		t.Fatal(err)
	}
	return o.Id
}

func stpLimit(t *testing.T, svc *order.Service, q *queue.OrderQueue, client string, side types.Side, price, qty int64) string {
	t.Helper()
	return stpOrder(t, svc, q, client, side, types.OrderTypeLimit, types.TimeInForceGTC, price, qty)
}

func stpWait(t *testing.T, svc *order.Service, id string, status types.OrderStatus, filled int64) {
	t.Helper()
	var last *types.Order
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		o, err := svc.Get(id)
		if err == nil && o.Status == status && o.Filled.Equal(decimal.NewFromInt(filled)) {
			return
		}
		last = o
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("order %s: want %s/filled=%d, last seen %+v", id, status, filled, last)
}

func TestSelfTrade_IncomingOrderIsCancelledNotMatched(t *testing.T) {
	e, svc, q := snapEngine(t)
	sell := stpLimit(t, svc, q, "c1", types.SideSell, 100, 5)
	buy := stpLimit(t, svc, q, "c1", types.SideBuy, 100, 5)

	stpWait(t, svc, buy, types.OrderStatusCancelled, 0)
	stpWait(t, svc, sell, types.OrderStatusNew, 0)
	if n := len(e.Trades()); n != 0 {
		t.Fatalf("expected no trade, got %d", n)
	}
	snap, err := e.Snapshot(context.Background(), "BTC-USD", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Bids) != 0 || len(snap.Asks) != 1 || !snap.Asks[0].Quantity.Equal(snapDec(5)) {
		t.Fatalf("only the original sell may rest: %+v", snap)
	}
}

func TestSelfTrade_OtherClientsStillTrade(t *testing.T) {
	e, svc, q := snapEngine(t)
	sell := stpLimit(t, svc, q, "c1", types.SideSell, 100, 5)
	buy := stpLimit(t, svc, q, "c2", types.SideBuy, 100, 5)

	stpWait(t, svc, buy, types.OrderStatusCompleted, 5)
	stpWait(t, svc, sell, types.OrderStatusCompleted, 5)
	if n := len(e.Trades()); n != 1 {
		t.Fatalf("expected 1 trade, got %d", n)
	}
}

func TestSelfTrade_FillsBeforeTheOwnOrderAreKept(t *testing.T) {
	e, svc, q := snapEngine(t)
	other := stpLimit(t, svc, q, "c2", types.SideSell, 100, 2) // better price, other client
	own := stpLimit(t, svc, q, "c1", types.SideSell, 101, 3)
	buy := stpLimit(t, svc, q, "c1", types.SideBuy, 101, 5)

	stpWait(t, svc, other, types.OrderStatusCompleted, 2)
	stpWait(t, svc, buy, types.OrderStatusCancelled, 2) // 2 traded, then it reached its own sell
	stpWait(t, svc, own, types.OrderStatusNew, 0)
	if n := len(e.Trades()); n != 1 {
		t.Fatalf("expected 1 trade, got %d", n)
	}
}

// Pins down the chosen policy: the incoming order stops at its own order even
// when another client's order is queued behind it at the same price.
func TestSelfTrade_OwnOrderAheadBlocksTheOrdersBehindIt(t *testing.T) {
	e, svc, q := snapEngine(t)
	own := stpLimit(t, svc, q, "c1", types.SideSell, 100, 2)
	other := stpLimit(t, svc, q, "c2", types.SideSell, 100, 3)
	buy := stpLimit(t, svc, q, "c1", types.SideBuy, 100, 5)

	stpWait(t, svc, buy, types.OrderStatusCancelled, 0)
	stpWait(t, svc, own, types.OrderStatusNew, 0)
	stpWait(t, svc, other, types.OrderStatusNew, 0)
	if n := len(e.Trades()); n != 0 {
		t.Fatalf("expected no trade, got %d", n)
	}
}

func TestSelfTrade_MarketOrderIsCancelled(t *testing.T) {
	e, svc, q := snapEngine(t)
	sell := stpLimit(t, svc, q, "c1", types.SideSell, 100, 5)
	buy := stpOrder(t, svc, q, "c1", types.SideBuy, types.OrderTypeMarket, types.TimeInForceIOC, 0, 3)

	stpWait(t, svc, buy, types.OrderStatusCancelled, 0)
	stpWait(t, svc, sell, types.OrderStatusNew, 0)
	if n := len(e.Trades()); n != 0 {
		t.Fatalf("expected no trade, got %d", n)
	}
}

// Without the Liquidity change this order would trade 2 with c2, hit its own
// order and end up partially filled, which breaks all-or-nothing.
func TestSelfTrade_FillOrKillTradesNothingWhenItWouldHitItsOwnOrder(t *testing.T) {
	e, svc, q := snapEngine(t)
	other := stpLimit(t, svc, q, "c2", types.SideSell, 100, 2)
	own := stpLimit(t, svc, q, "c1", types.SideSell, 100, 3)
	fok := stpOrder(t, svc, q, "c1", types.SideBuy, types.OrderTypeLimit, types.TimeInForceFOK, 100, 5)

	stpWait(t, svc, fok, types.OrderStatusCancelled, 0)
	stpWait(t, svc, other, types.OrderStatusNew, 0)
	stpWait(t, svc, own, types.OrderStatusNew, 0)
	if n := len(e.Trades()); n != 0 {
		t.Fatalf("FOK must not trade at all, got %d trades", n)
	}
}

func TestSelfTrade_NonCrossingOrdersOfOneClientCoexist(t *testing.T) {
	e, svc, q := snapEngine(t)
	buy := stpLimit(t, svc, q, "c1", types.SideBuy, 99, 5)
	sell := stpLimit(t, svc, q, "c1", types.SideSell, 101, 5)

	stpWait(t, svc, buy, types.OrderStatusNew, 0)
	stpWait(t, svc, sell, types.OrderStatusNew, 0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, _ := e.Snapshot(context.Background(), "BTC-USD", 5)
		if len(snap.Bids) == 1 && len(snap.Asks) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("both orders should rest, since they do not cross")
}