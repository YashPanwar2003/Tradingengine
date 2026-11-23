package order

import (
	"fmt"
	"sync"

	"tradingengine/pkg/types"
)

type Store interface {
	Save(o *types.Order) error
	GetById(OrderId string) (*types.Order, error)
	GetByClient(clientID string) ([]*types.Order, error)
	GetActiveBySymbol(symbol string) ([]*types.Order, error)
	Update(order *types.Order) error
	Delete(orderID string) error
	// RecordFill stores the new state of both orders produced by one fill.
	RecordFill(incoming, resting *types.Order, trade *types.Trade) error
}

// MemoryStore keeps PRIVATE COPIES of orders. The engine mutates its own live
// objects and mirrors each change here through Update / RecordFill, so readers
// on other goroutines only ever see consistent snapshots.
type MemoryStore struct {
	mu     sync.RWMutex
	orders map[string]*types.Order // orderID -> private copy
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		orders: make(map[string]*types.Order),
	}
}

func (s *MemoryStore) Save(order *types.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orders[order.Id]; exists {
		return fmt.Errorf("Save order: Order already exists")
	}
	s.orders[order.Id] = order.Clone()
	return nil
}

func (s *MemoryStore) GetById(orderId string) (*types.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	order, exists := s.orders[orderId]
	if !exists {
		return nil, types.ErrOrderNotFound
	}
	return order.Clone(), nil
}

func (s *MemoryStore) GetByClient(clientId string) ([]*types.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*types.Order, 0) // empty, not nil, so JSON says [] not null
	for _, o := range s.orders {
		if o.ClientId == clientId {
			result = append(result, o.Clone())
		}
	}
	return result, nil
}

func (s *MemoryStore) GetActiveBySymbol(symbol string) ([]*types.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*types.Order, 0)
	for _, o := range s.orders {
		if o.Symbol == symbol && o.IsActive() {
			result = append(result, o.Clone())
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(order *types.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orders[order.Id]; !exists {
		return types.ErrOrderNotFound
	}
	s.orders[order.Id] = order.Clone()
	return nil
}

// RecordFill updates both orders under a single lock.
func (s *MemoryStore) RecordFill(incoming, resting *types.Order, trade *types.Trade) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orders[incoming.Id]; !ok {
		return types.ErrOrderNotFound
	}
	if _, ok := s.orders[resting.Id]; !ok {
		return types.ErrOrderNotFound
	}
	s.orders[incoming.Id] = incoming.Clone()
	s.orders[resting.Id] = resting.Clone()
	return nil
}

func (s *MemoryStore) Delete(orderId string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orders[orderId]; !exists {
		return types.ErrOrderNotFound
	}
	delete(s.orders, orderId)
	return nil
}
