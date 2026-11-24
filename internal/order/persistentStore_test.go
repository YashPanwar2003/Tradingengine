package order_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradingengine/internal/order"
	"tradingengine/pkg/types"
)

type fillCall struct {
	incoming types.Order
	resting  types.Order
	trade    types.Trade
}

// fakePersister stands in for Postgres and records what it was asked to do.
type fakePersister struct {
	mu          sync.Mutex
	inserted    []types.Order
	updated     []types.Order
	fills       []fillCall
	ops         []string // "update" / "fill", in the order they reached the "database"
	insertErr   error
	failUpdates int           // fail this many UpdateOrderFill calls before succeeding
	failFills   int           // fail this many RecordFill calls before succeeding
	stored      []types.Order // what "Postgres" holds, for the read fallbacks
	listErr     error
	duplicateOf *types.Order // when set, InsertOrder reports a duplicate clientOrderId
}

func (f *fakePersister) InsertOrder(_ context.Context, o *types.Order) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.duplicateOf != nil {
		return types.ErrDuplicateClientOrderId
	}
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, *o)
	return nil
}

func (f *fakePersister) FindByClientOrderID(_ context.Context, clientID, key string) (*types.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.duplicateOf == nil {
		return nil, types.ErrOrderNotFound
	}
	c := *f.duplicateOf
	return &c, nil
}

func (f *fakePersister) UpdateOrderFill(_ context.Context, o *types.Order) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failUpdates > 0 {
		f.failUpdates--
		return errors.New("boom")
	}
	f.updated = append(f.updated, *o)
	f.ops = append(f.ops, "update")
	return nil
}

func (f *fakePersister) RecordFill(_ context.Context, in, rest *types.Order, tr *types.Trade) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFills > 0 {
		f.failFills--
		return errors.New("boom")
	}
	f.fills = append(f.fills, fillCall{incoming: *in, resting: *rest, trade: *tr})
	f.ops = append(f.ops, "fill")
	return nil
}

func (f *fakePersister) GetOrderByID(_ context.Context, id string) (*types.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.stored {
		if o.Id == id {
			c := o
			return &c, nil
		}
	}
	return nil, types.ErrOrderNotFound
}

func (f *fakePersister) ListOrdersByClient(_ context.Context, clientID string, limit int) ([]*types.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]*types.Order, 0)
	for _, o := range f.stored {
		if o.ClientId == clientID && len(out) < limit {
			c := o
			out = append(out, &c)
		}
	}
	return out, nil
}

func (f *fakePersister) updates() []types.Order {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]types.Order(nil), f.updated...)
}

func (f *fakePersister) fillCalls() []fillCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fillCall(nil), f.fills...)
}

func (f *fakePersister) opLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ops...)
}

// ---- helpers shared by the tests in this package ----

func newOrder() *types.Order { // BUY 5 @ 100, limit, GTC
	return types.NewOrder("c1", "BTC-USD", types.SideBuy, types.OrderTypeLimit,
		decimal.NewFromInt(100), decimal.NewFromInt(5), types.TimeInForceGTC)
}

func newSell() *types.Order { // SELL 5 @ 100, limit, GTC
	return types.NewOrder("c2", "BTC-USD", types.SideSell, types.OrderTypeLimit,
		decimal.NewFromInt(100), decimal.NewFromInt(5), types.TimeInForceGTC)
}

func newTrade(buy, sell *types.Order, qty int64) *types.Trade {
	return types.NewTrade("BTC-USD", buy.Id, sell.Id,
		decimal.NewFromInt(100), decimal.NewFromInt(qty), time.Now().UTC())
}

// ---- order saves ----

func TestSave_PersistsAndStores(t *testing.T) {
	fake := &fakePersister{}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	o := newOrder()
	if err := ps.Save(o); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(fake.inserted) != 1 || fake.inserted[0].Id != o.Id {
		t.Fatalf("expected order %s inserted, got %+v", o.Id, fake.inserted)
	}
	if _, err := ps.GetById(o.Id); err != nil {
		t.Fatalf("order should be in memory: %v", err)
	}
}

func TestSave_DBFailureLeavesMemoryUntouched(t *testing.T) {
	fake := &fakePersister{insertErr: errors.New("db down")}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	o := newOrder()
	if err := ps.Save(o); err == nil {
		t.Fatal("expected Save to fail when the database fails")
	}
	if _, err := ps.GetById(o.Id); err == nil {
		t.Fatal("an order the database rejected must not exist in memory")
	}
}

// ---- duplicate clientOrderId ----

func TestSave_DuplicateKeyReturnsTheExistingOrderAndStoresNothing(t *testing.T) {
	existing := newOrder()
	existing.ClientOrderId = "k1"
	ps := order.NewPersistentStore(order.NewMemoryStore(), &fakePersister{duplicateOf: existing}, 10)

	again := newOrder()
	again.ClientOrderId = "k1"
	err := ps.Save(again)

	var dup *order.DuplicateError
	if !errors.As(err, &dup) || dup.Existing.Id != existing.Id {
		t.Fatalf("expected a DuplicateError pointing at the original, got %v", err)
	}
	if _, err := ps.GetById(again.Id); err == nil {
		t.Fatal("the duplicate must not be stored in memory")
	}
}

func TestSave_DuplicatePrefersTheFresherMemoryCopy(t *testing.T) {
	stale := newOrder() // what the database still shows
	stale.ClientOrderId = "k1"

	mem := order.NewMemoryStore()
	live := stale.Clone()
	live.Status = types.OrderStatusPartial
	live.Filled = decimal.NewFromInt(2)
	_ = mem.Save(live)

	ps := order.NewPersistentStore(mem, &fakePersister{duplicateOf: stale}, 10)
	again := newOrder()
	again.ClientOrderId = "k1"

	var dup *order.DuplicateError
	if err := ps.Save(again); !errors.As(err, &dup) || dup.Existing.Status != types.OrderStatusPartial {
		t.Fatalf("a retry should see the live state, got %v", err)
	}
}

// ---- single-order updates (cancel / reject) ----

// The matcher keeps mutating the order after Update returns. The DB worker
// must see the values as they were at Update time.
func TestUpdate_QueuesASnapshotNotThePointer(t *testing.T) {
	fake := &fakePersister{}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	o := newOrder()
	_ = ps.Save(o)

	o.Filled = decimal.NewFromInt(2)
	o.Status = types.OrderStatusPartial
	if err := ps.Update(o); err != nil {
		t.Fatalf("Update: %v", err)
	}
	o.Filled = decimal.NewFromInt(4) // mutated after queueing

	go ps.Run() // the worker starts only now, so the copy is what it sees
	ps.Close()

	got := fake.updates()
	if len(got) != 1 || !got[0].Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("expected snapshot with filled=2, got %+v", got)
	}
}

func TestClose_FlushesEverythingInOrder(t *testing.T) {
	fake := &fakePersister{}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 100)

	o := newOrder()
	_ = ps.Save(o)
	for i := 1; i <= 50; i++ {
		o.Filled = decimal.NewFromInt(int64(i)).Div(decimal.NewFromInt(10))
		_ = ps.Update(o)
	}

	go ps.Run()
	ps.Close()

	got := fake.updates()
	if len(got) != 50 {
		t.Fatalf("expected 50 flushed updates, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Filled.LessThan(got[i-1].Filled) {
			t.Fatalf("updates applied out of order at index %d", i)
		}
	}
}

func TestUpdate_RetriesThenSucceeds(t *testing.T) {
	fake := &fakePersister{failUpdates: 2}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	o := newOrder()
	_ = ps.Save(o)
	_ = ps.Update(o)

	go ps.Run()
	ps.Close() // takes about 600ms because of the retry backoff

	if len(fake.updates()) != 1 {
		t.Fatal("update should succeed on the third attempt")
	}
}

// ---- fills ----

// One fill is ONE job holding snapshots of both orders and the trade, and it
// keeps its place in the queue relative to later single-order updates.
func TestRecordFill_IsOneJob_SnapshottedAndOrderedWithUpdates(t *testing.T) {
	fake := &fakePersister{}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	buy, sell := newOrder(), newSell()
	_ = ps.Save(buy)
	_ = ps.Save(sell)

	buy.Filled, sell.Filled = decimal.NewFromInt(2), decimal.NewFromInt(2)
	if err := ps.RecordFill(buy, sell, newTrade(buy, sell, 2)); err != nil {
		t.Fatal(err)
	}

	buy.Filled = decimal.NewFromInt(4) // mutated after queueing: must not leak in
	buy.Status = types.OrderStatusCancelled
	_ = ps.Update(buy) // a later event

	go ps.Run()
	ps.Close()

	ops := fake.opLog()
	if len(ops) != 2 || ops[0] != "fill" || ops[1] != "update" {
		t.Fatalf("expected [fill update], got %v", ops)
	}
	call := fake.fillCalls()[0]
	if !call.incoming.Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("fill job must hold a snapshot, got filled=%s", call.incoming.Filled)
	}
	if call.trade.BuyOrderId != buy.Id || call.trade.SellOrderId != sell.Id {
		t.Fatalf("trade ids wrong: %+v", call.trade)
	}
}

func TestRecordFill_RetriesTheWholeFill(t *testing.T) {
	fake := &fakePersister{failFills: 2}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	buy, sell := newOrder(), newSell()
	_ = ps.Save(buy)
	_ = ps.Save(sell)
	_ = ps.RecordFill(buy, sell, newTrade(buy, sell, 1))

	go ps.Run()
	ps.Close() // about 600ms of backoff

	if n := len(fake.fillCalls()); n != 1 {
		t.Fatalf("fill should succeed on the third attempt, got %d writes", n)
	}
}

// If memory refuses the fill, nothing may reach the database.
func TestRecordFill_UnknownOrderQueuesNothing(t *testing.T) {
	fake := &fakePersister{}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	buy, sell := newOrder(), newSell() // never saved
	err := ps.RecordFill(buy, sell, newTrade(buy, sell, 1))
	if !errors.Is(err, types.ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}

	go ps.Run()
	ps.Close()

	if ops := fake.opLog(); len(ops) != 0 {
		t.Fatalf("nothing should be queued, got %v", ops)
	}
}
