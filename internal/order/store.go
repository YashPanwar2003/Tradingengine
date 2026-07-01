package order

import (
	"fmt"
	"sync"
	"tradingengine/pkg/types"
)
type Store interface{
	Save(o *types.Order) error
	GetById(OrderId string)(*types.Order, error)
	GetByClient(clientID string) ([]*types.Order, error)
	GetActiveBySymbol(symbol string) ([]*types.Order, error)
	Update(order *types.Order) error
	Delete(orderID string) error
}
type MemoryStore struct {
	mu     sync.RWMutex
	orders map[string]*types.Order // orderID → Order
}

func NewMemoryStore() *MemoryStore{
	return &MemoryStore{
		orders: make(map[string]*types.Order),
	}
}
func (s *MemoryStore) Save(order *types.Order) error {
  s.mu.Lock()
  defer s.mu.Unlock()
  _,exists:=s.orders[order.Id]
  if exists{
	return fmt.Errorf("Save order: Order already exists")
  }
  s.orders[order.Id]=order
  return nil
}
func (s *MemoryStore) GetById(orderId string) (*types.Order,error){
    s.mu.RLock()
	defer s.mu.RUnlock()

	order, exists := s.orders[orderId]
	if !exists {
		return nil, types.ErrOrderNotFound
	}
	return order, nil
}
func ( s *MemoryStore) GetByClient(clientId string)([]*types.Order , error){
   s.mu.RLock()
   defer s.mu.RUnlock()

   var result []*types.Order
   for _,o:= range s.orders{
      if o.ClientId == clientId{
		result = append(result,o)
	  }
   }
   return result , nil
}

func (s *MemoryStore) Update(order *types.Order) error{
	s.mu.Lock()
	defer s.mu.Unlock()

	if _,exists:= s.orders[order.Id] ; !exists{
        return types.ErrOrderNotFound
	}
	s.orders[order.Id] = order
	return nil
}
func (s *MemoryStore) Delete(orderId string) error{
	s.mu.Lock()
	defer s.mu.Unlock()
	if _,exists := s.orders[orderId]; !exists{
		return types.ErrOrderNotFound
	}
	delete(s.orders,orderId)
	return nil

}
func (s *MemoryStore) GetActiveBySymbol(symbol string) ([]*types.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*types.Order
	for _, o := range s.orders {
		if o.Symbol == symbol && o.IsActive() {
			result = append(result, o)
		}
	}
	return result, nil
}

