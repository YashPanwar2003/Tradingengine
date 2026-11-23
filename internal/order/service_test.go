package order_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"tradingengine/internal/order"
	"tradingengine/pkg/types"
)

func TestExecuteFill_AppliesBothSidesAndBuildsTrade(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	incoming := newOrder() // BUY 5 @ 100
	resting := newSell()   // SELL, shrunk to 2 so it completes
	resting.Quantity = decimal.NewFromInt(2)
	_ = mem.Save(incoming)
	_ = mem.Save(resting)

	tr, err := svc.ExecuteFill(incoming, resting, decimal.NewFromInt(2), decimal.NewFromInt(100))
	if err != nil {
		t.Fatal(err)
	}
	if incoming.Status != types.OrderStatusPartial || !incoming.Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("incoming: %s / %s", incoming.Status, incoming.Filled)
	}
	if resting.Status != types.OrderStatusCompleted || !resting.Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("resting: %s / %s", resting.Status, resting.Filled)
	}
	if tr.BuyOrderId != incoming.Id || tr.SellOrderId != resting.Id {
		t.Fatalf("buy/sell ids wrong: %+v", tr)
	}
	if !tr.Quantity.Equal(decimal.NewFromInt(2)) || !tr.Price.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("trade price/quantity wrong: %+v", tr)
	}
}

func TestExecuteFill_BuyerAndSellerWhenIncomingIsSell(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	incoming := newSell() // SELL 5
	resting := newOrder() // BUY 5
	_ = mem.Save(incoming)
	_ = mem.Save(resting)

	tr, err := svc.ExecuteFill(incoming, resting, decimal.NewFromInt(5), decimal.NewFromInt(100))
	if err != nil {
		t.Fatal(err)
	}
	if tr.BuyOrderId != resting.Id || tr.SellOrderId != incoming.Id {
		t.Fatalf("buyer must be the resting BUY, got %+v", tr)
	}
	if incoming.Status != types.OrderStatusCompleted || resting.Status != types.OrderStatusCompleted {
		t.Fatalf("both should be COMPLETED, got %s / %s", incoming.Status, resting.Status)
	}
}

// The validate-first guarantee: if either side cannot take the fill,
// NEITHER order may be changed.
func TestExecuteFill_ChangesNothingWhenOneSideCannotFill(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	incoming := newOrder() // BUY 5
	resting := newSell()
	resting.Quantity = decimal.NewFromInt(1) // only 1 left, cannot give 2
	_ = mem.Save(incoming)
	_ = mem.Save(resting)

	if _, err := svc.ExecuteFill(incoming, resting, decimal.NewFromInt(2), decimal.NewFromInt(100)); err == nil {
		t.Fatal("expected an error")
	}
	if !incoming.Filled.IsZero() || incoming.Status != types.OrderStatusNew {
		t.Fatalf("incoming must be untouched, got %s / %s", incoming.Status, incoming.Filled)
	}
	if !resting.Filled.IsZero() || resting.Status != types.OrderStatusNew {
		t.Fatalf("resting must be untouched, got %s / %s", resting.Status, resting.Filled)
	}
}

func TestExecuteFill_RejectsInactiveOrder(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	incoming, resting := newOrder(), newSell()
	resting.Status = types.OrderStatusCancelled
	_ = mem.Save(incoming)
	_ = mem.Save(resting)

	if _, err := svc.ExecuteFill(incoming, resting, decimal.NewFromInt(1), decimal.NewFromInt(100)); err == nil {
		t.Fatal("a cancelled order must not be fillable")
	}
	if !incoming.Filled.IsZero() {
		t.Fatalf("incoming must be untouched, got filled=%s", incoming.Filled)
	}
}

func TestExecuteFill_RejectsUnknownOrder(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	incoming, resting := newOrder(), newSell()
	_ = mem.Save(incoming) // resting is never saved

	_, err := svc.ExecuteFill(incoming, resting, decimal.NewFromInt(1), decimal.NewFromInt(100))
	if !errors.Is(err, types.ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
	if !incoming.Filled.IsZero() {
		t.Fatalf("incoming must be untouched, got filled=%s", incoming.Filled)
	}
}

func TestExecuteFill_RejectsNonPositiveQuantity(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	incoming, resting := newOrder(), newSell()
	_ = mem.Save(incoming)
	_ = mem.Save(resting)

	for _, q := range []decimal.Decimal{decimal.Zero, decimal.NewFromInt(-1)} {
		if _, err := svc.ExecuteFill(incoming, resting, q, decimal.NewFromInt(100)); !errors.Is(err, types.ErrInvalidQuantity) {
			t.Fatalf("qty %s: expected ErrInvalidQuantity, got %v", q, err)
		}
	}
}

// Through the real PersistentStore a fill must reach the database as exactly
// one job, not as two order updates plus a trade.
func TestExecuteFill_ThroughPersistentStore_IsOneJobNotThree(t *testing.T) {
	fake := &fakePersister{}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)
	svc := order.NewService(ps)

	buy, sell := newOrder(), newSell()
	if err := svc.Place(buy); err != nil {
		t.Fatal(err)
	}
	if err := svc.Place(sell); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteFill(buy, sell, decimal.NewFromInt(5), decimal.NewFromInt(100)); err != nil {
		t.Fatal(err)
	}

	go ps.Run()
	ps.Close()

	ops := fake.opLog()
	if len(ops) != 1 || ops[0] != "fill" {
		t.Fatalf("a fill must be exactly one database job, got %v", ops)
	}
	call := fake.fillCalls()[0]
	if call.incoming.Status != types.OrderStatusCompleted || call.resting.Status != types.OrderStatusCompleted {
		t.Fatalf("snapshots should both be COMPLETED, got %s / %s", call.incoming.Status, call.resting.Status)
	}
}

func TestCancel_MarksOrderCancelledOnce(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	o := newOrder()
	_ = mem.Save(o)

	got, err := svc.Cancel(o.Id)
	if err != nil || got.Status != types.OrderStatusCancelled {
		t.Fatalf("cancel: %v / %+v", err, got)
	}
	if _, err := svc.Cancel(o.Id); !errors.Is(err, types.ErrOrderAlreadyFilled) {
		t.Fatalf("second cancel should report the order is no longer active, got %v", err)
	}
}

// Fails until Service.Reject sets OrderStatusRejected (it used to set Cancelled).
func TestReject_SetsRejectedStatus(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	o := newOrder()
	_ = mem.Save(o)

	got, err := svc.Reject(o.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != types.OrderStatusRejected {
		t.Fatalf("expected REJECTED, got %s", got.Status)
	}
}
