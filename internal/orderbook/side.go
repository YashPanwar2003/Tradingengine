package orderbook

import (
	"container/heap"

	"tradingengine/pkg/types"

	"github.com/shopspring/decimal"
)

type LevelHeap struct {
	levels    []*Level
	isMaxHeap bool
}

func (h *LevelHeap) Len() int { return len(h.levels) }
func (h *LevelHeap) Less(i, j int) bool {
	if h.isMaxHeap {
		return h.levels[i].Price.GreaterThan(h.levels[j].Price)
	}
	return h.levels[i].Price.LessThan(h.levels[i].Price)
}

func (h *LevelHeap) Swap(i, j int) {
	h.levels[i], h.levels[j] = h.levels[j], h.levels[i]
}

func (h *LevelHeap) Push(x any) {
	h.levels = append(h.levels, x.(*Level))

}

func (h *LevelHeap) Top()  *Level{
	if h.Len()==0 {
		return nil
	}
	return h.levels[0]
}

func (h *LevelHeap) Pop() any {
	old := h.levels
	n := len(old)
	level := old[n-1]
	h.levels = old[:n-1]
	return level
}

type Side struct {
	heap       *LevelHeap
	priceIndex map[string]*Level
}

func NewSide(isMaxHeap bool) *Side {
	h := &LevelHeap{
		levels:    make([]*Level, 0),
		isMaxHeap: isMaxHeap,
	}
	heap.Init(h)
	return &Side{
		heap:       h,
		priceIndex: make(map[string]*Level),
	}
}

func (s *Side) Add(order *types.Order) {
	key := order.Price.String()
	level, exists := s.priceIndex[key]
	if !exists {
		level = NewLevel(order.Price)
		s.priceIndex[key] = level
		heap.Push(s.heap, level)
	}
	level.Add(order)
}
func (s *Side) Best() *Level {
	if s.heap.Len() == 0 {
		return nil
	}
	return s.heap.Top()
}

func (s *Side) RemoveLevel(price decimal.Decimal) {
	key := price.String()
	delete(s.priceIndex, key)
	for i, l := range s.heap.levels {
		if l.Price.Equal(price) {
			heap.Remove(s.heap, i)
			return
		}
	}
}
func (s *Side) CancelOrder(orderId string, price decimal.Decimal) bool {
	key := price.String()
	level, exists := s.priceIndex[key]
	if !exists {
		return false
	}
	removed := level.Remove(orderId)
	if removed && level.IsEmpty() {
		s.RemoveLevel(price)
	}
	return removed
}
func (s *Side) IsEmpty() bool {
	return s.heap.Len() == 0
}
