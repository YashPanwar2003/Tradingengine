package order_test

import (
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"tradingengine/internal/order"
	"tradingengine/pkg/types"
)

// The store must not alias the object it was given, nor hand out its own.
func TestMemoryStore_HoldsPrivateCopies(t *testing.T) {
	mem := order.NewMemoryStore()
	live := newOrder()
	_ = mem.Save(live)

	// the engine keeps mutating its live object: readers must not see it
	live.Filled = decimal.NewFromInt(3)
	got, _ := mem.GetById(live.Id)
	if !got.Filled.IsZero() {
		t.Fatal("store must not alias the order it was given")
	}

	// a caller changing what it read must not change the store
	got.Status = types.OrderStatusCancelled
	again, _ := mem.GetById(live.Id)
	if again.Status != types.OrderStatusNew {
		t.Fatal("store must hand out copies")
	}

	list, _ := mem.GetByClient(live.ClientId)
	list[0].Status = types.OrderStatusCancelled
	again, _ = mem.GetById(live.Id)
	if again.Status != types.OrderStatusNew {
		t.Fatal("GetByClient must hand out copies")
	}
}

func TestMemoryStore_RecordFillMirrorsLiveState(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	buy, sell := newOrder(), newSell()
	_ = mem.Save(buy)
	_ = mem.Save(sell)

	if _, err := svc.ExecuteFill(buy, sell, decimal.NewFromInt(2), decimal.NewFromInt(100)); err != nil {
		t.Fatal(err)
	}
	got, _ := mem.GetById(buy.Id) // readers now see the new state
	if got.Status != types.OrderStatusPartial || !got.Filled.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("store not updated by the fill: %s / %s", got.Status, got.Filled)
	}
}

func TestMemoryStore_GetByClientReturnsEmptySliceNotNil(t *testing.T) {
	list, err := order.NewMemoryStore().GetByClient("nobody")
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("expected an empty, non-nil slice, got %v / %v", list, err)
	}
}

// Only meaningful with -race: before the copy-on-read change this reports a
// DATA RACE, because readers touch the same objects the "engine" is filling.
func TestStore_ConcurrentReadersWhileEngineFills(t *testing.T) {
	mem := order.NewMemoryStore()
	svc := order.NewService(mem)

	buy, sell := newOrder(), newSell()
	buy.Quantity = decimal.NewFromInt(1000)
	sell.Quantity = decimal.NewFromInt(1000)
	_ = mem.Save(buy)
	_ = mem.Save(sell)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if o, err := svc.Get(buy.Id); err == nil {
					_ = o.Status
					_ = o.Filled.String()
					_ = o.IsActive()
				}
				_, _ = svc.GetClientOrders("c1")
			}
		}()
	}

	for i := 0; i < 1000; i++ { // the "engine": 1000 one-lot fills
		if _, err := svc.ExecuteFill(buy, sell, decimal.NewFromInt(1), decimal.NewFromInt(100)); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
