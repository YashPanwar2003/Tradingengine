package matching_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradingengine/internal/matching"
	"tradingengine/internal/order"
	"tradingengine/internal/queue"
	"tradingengine/pkg/types"
)

func snapDec(n int64) decimal.Decimal { return decimal.NewFromInt(n) }

// snapEngine starts a real engine goroutine on an in-memory service.
func snapEngine(t *testing.T) (*matching.Engine, *order.Service, *queue.OrderQueue) {
	t.Helper()
	svc := order.NewService(order.NewMemoryStore())
	q := queue.NewOrderQueue(100)
	e := matching.NewEngine(q, svc, 100)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return e, svc, q
}

func snapSubmit(t *testing.T, svc *order.Service, q *queue.OrderQueue, side types.Side, price, qty int64) {
	t.Helper()
	o := types.NewOrder("c1", "BTC-USD", side, types.OrderTypeLimit,
		snapDec(price), snapDec(qty), types.TimeInForceGTC)
	if err := svc.Place(o); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(o); err != nil {
		t.Fatal(err)
	}
}

// The engine works asynchronously, so poll instead of sleeping a fixed time.
func snapEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// seedBook: asks 101 x5 (2 orders) and 102 x1, bids 99 x4 and 98 x1.
func seedBook(t *testing.T, e *matching.Engine, svc *order.Service, q *queue.OrderQueue) {
	t.Helper()
	snapSubmit(t, svc, q, types.SideSell, 101, 2)
	snapSubmit(t, svc, q, types.SideSell, 101, 3)
	snapSubmit(t, svc, q, types.SideSell, 102, 1)
	snapSubmit(t, svc, q, types.SideBuy, 99, 4)
	snapSubmit(t, svc, q, types.SideBuy, 98, 1)

	snapEventually(t, "all five orders on the book", func() bool { // the queue is FIFO
		s, err := e.Snapshot(context.Background(), "BTC-USD", 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(s.Bids) == 2
	})
}

func TestSnapshot_AggregatesAndSortsLevels(t *testing.T) {
	e, svc, q := snapEngine(t)
	seedBook(t, e, svc, q)

	snap, err := e.Snapshot(context.Background(), "BTC-USD", 10)
	if err != nil {
		t.Fatal(err)
	}
	asks, bids := snap.Asks, snap.Bids
	if len(asks) != 2 || !asks[0].Price.Equal(snapDec(101)) || !asks[0].Quantity.Equal(snapDec(5)) || asks[0].Orders != 2 {
		t.Fatalf("best ask level wrong: %+v", asks)
	}
	if !asks[1].Price.Equal(snapDec(102)) || !asks[1].Quantity.Equal(snapDec(1)) {
		t.Fatalf("second ask level wrong: %+v", asks)
	}
	if len(bids) != 2 || !bids[0].Price.Equal(snapDec(99)) || !bids[0].Quantity.Equal(snapDec(4)) {
		t.Fatalf("best bid level wrong: %+v", bids)
	}
	if !bids[1].Price.Equal(snapDec(98)) {
		t.Fatalf("bids must be sorted highest first: %+v", bids)
	}
}

func TestSnapshot_DepthLimitsLevelsPerSide(t *testing.T) {
	e, svc, q := snapEngine(t)
	seedBook(t, e, svc, q)

	snap, err := e.Snapshot(context.Background(), "BTC-USD", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Bids) != 1 || len(snap.Asks) != 1 ||
		!snap.Bids[0].Price.Equal(snapDec(99)) || !snap.Asks[0].Price.Equal(snapDec(101)) {
		t.Fatalf("depth=1 should return only the best level per side: %+v", snap)
	}
}

func TestSnapshot_ReflectsPartialFills(t *testing.T) {
	e, svc, q := snapEngine(t)
	snapSubmit(t, svc, q, types.SideSell, 100, 5)
	snapSubmit(t, svc, q, types.SideBuy, 100, 2) // trades 2, nothing rests

	var snap *types.BookSnapshot
	snapEventually(t, "the buy to match", func() bool {
		s, err := e.Snapshot(context.Background(), "BTC-USD", 10)
		if err != nil {
			t.Fatal(err)
		}
		snap = s
		return len(s.Asks) == 1 && s.Asks[0].Quantity.Equal(snapDec(3))
	})
	if len(snap.Bids) != 0 {
		t.Fatalf("a fully filled buy must not rest: %+v", snap.Bids)
	}
}

func TestSnapshot_UnknownSymbolIsEmptyNotNil(t *testing.T) {
	e, _, _ := snapEngine(t)

	snap, err := e.Snapshot(context.Background(), "ETH-USD", 10)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Symbol != "ETH-USD" || snap.Bids == nil || snap.Asks == nil || len(snap.Bids)+len(snap.Asks) != 0 {
		t.Fatalf("expected empty, non-nil sides, got %+v", snap)
	}
}

func TestSnapshot_GivesUpWhenTheEngineIsNotRunning(t *testing.T) {
	svc := order.NewService(order.NewMemoryStore())
	e := matching.NewEngine(queue.NewOrderQueue(1), svc, 1) // Run is never called

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.Snapshot(ctx, "BTC-USD", 5); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded instead of hanging, got %v", err)
	}
}
