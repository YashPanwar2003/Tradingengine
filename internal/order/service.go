package order

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"tradingengine/pkg/types"
)

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{
		store: store,
	}
}

func (s *Service) Place(order *types.Order) error {
	if err := s.store.Save(order); err != nil {
		return fmt.Errorf("Place order: %w", err)
	}
	return nil
}
func (s *Service) Get(orderId string) (*types.Order, error) {
	order, err := s.store.GetById(orderId)
	if err != nil {
		return nil, fmt.Errorf("Get order: %w", err)
	}
	return order, nil
}

func (s *Service) Cancel(orderId string) (*types.Order,error) {
	order, err := s.store.GetById(orderId)
	if err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	if !order.IsActive() {
		return nil, fmt.Errorf("cancel order: %w", types.ErrOrderAlreadyFilled)
	}

	order.Status = types.OrderStatusCancelled
	order.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(order); err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	return order, nil
}

func (s *Service)ApplyFill(orderId string ,filledQuantity decimal.Decimal)(*types.Order, error){
	order,err:= s.store.GetById(orderId)
	if err!=nil{
		return nil , fmt.Errorf("Get order:%w",err)
	}
	if !order.IsActive(){
		return nil , fmt.Errorf("apply fill , order %w is not active",orderId)
	}
	order.Filled = order.Filled.Add(filledQuantity)
	order.UpdatedAt = time.Now().UTC()

	if order.IsFilled(){
		order.Status = types.OrderStatusCompleted
	}else{
		order.Status=types.OrderStatusPartial
	}
	if err:= s.store.Update(order);err!=nil{
		return nil,fmt.Errorf("Apply fill : %w",err)
	}
	return order,nil
}

func (s *Service) Reject(orderId string)(*types.Order,error){
	order, err:= s.store.GetById(orderId)
	if err!=nil{
		return nil , fmt.Errorf("Order not found: %w",err)
	}
	order.Status=types.OrderStatusCancelled
	order.UpdatedAt=time.Now().UTC()
    if err:=s.store.Update(order);err!=nil{
		return nil , fmt.Errorf("Apply fill : %w",err)
	}
	return order,nil
}

func (s *Service) GetClientOrders(clientId string)([]*types.Order,error){
	orders ,err:= s.store.GetByClient(clientId)
	if err!=nil{
		return nil , fmt.Errorf("Get orders by client: %w",err)
	}
	return orders,nil
}



