package order_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"tradingengine/internal/order"
	"tradingengine/pkg/types"
)

// An empty MemoryStore plays the part of a freshly restarted engine.

func TestGetById_FallsBackToTheDatabase(t *testing.T) {
	done := newOrder()
	done.Status = types.OrderStatusCompleted
	done.Filled = done.Quantity
	fake := &fakePersister{stored: []types.Order{*done}}
	ps := order.NewPersistentStore(order.NewMemoryStore(), fake, 10)

	got, err := ps.GetById(done.Id)
	if err != nil || got.Status != types.OrderStatusCompleted {
		t.Fatalf("expected the completed order from the database, got %v / %v", got, err)
	}
}

func TestGetById_UnknownEverywhereIsNotFound(t *testing.T) {
	ps := order.NewPersistentStore(order.NewMemoryStore(), &fakePersister{}, 10)

	if _, err := ps.GetById("nope"); !errors.Is(err, types.ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
}

func TestGetById_PrefersMemoryOverTheDatabase(t *testing.T) {
	o := newOrder()
	fake := &fakePersister{stored: []types.Order{*o}} // stale: still NEW

	mem := order.NewMemoryStore()
	live := o.Clone()
	live.Status = types.OrderStatusPartial
	live.Filled = decimal.NewFromInt(2)
	_ = mem.Save(live)

	ps := order.NewPersistentStore(mem, fake, 10)
	got, _ := ps.GetById(o.Id)
	if got.Status != types.OrderStatusPartial {
		t.Fatalf("memory is fresher than the async database, got %s", got.Status)
	}
}

func TestGetByClient_OverlaysFreshMemoryOnDatabaseHistory(t *testing.T) {
	old := newOrder() // ended before the restart: only in the database
	old.Status = types.OrderStatusCompleted
	cur := newOrder() // live: the database copy is stale
	other := newOrder()
	other.ClientId = "someone-else"

	fake := &fakePersister{stored: []types.Order{*old, *cur, *other}}
	mem := order.NewMemoryStore()
	live := cur.Clone()
	live.Status = types.OrderStatusPartial
	_ = mem.Save(live)

	ps := order.NewPersistentStore(mem, fake, 10)
	got, err := ps.GetByClient("c1")
	if err != nil || len(got) != 2 {
		t.Fatalf("expected 2 orders for c1, got %d / %v", len(got), err)
	}
	status := map[string]types.OrderStatus{}
	for _, o := range got {
		status[o.Id] = o.Status
	}
	if status[old.Id] != types.OrderStatusCompleted || status[cur.Id] != types.OrderStatusPartial {
		t.Fatalf("wrong overlay: %v", status)
	}
}

func TestGetByClient_FallsBackToMemoryWhenTheDatabaseFails(t *testing.T) {
	fake := &fakePersister{listErr: errors.New("db down")}
	mem := order.NewMemoryStore()
	_ = mem.Save(newOrder())

	ps := order.NewPersistentStore(mem, fake, 10)
	got, err := ps.GetByClient("c1")
	if err != nil || len(got) != 1 {
		t.Fatalf("expected the memory copy, got %d / %v", len(got), err)
	}
}
