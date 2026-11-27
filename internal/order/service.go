package order

import (
	"fmt"
	"time"

	"tradingengine/pkg/types"

	"github.com/shopspring/decimal"
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

func (s *Service) Cancel(orderId string) (*types.Order, error) {
	order, err := s.store.GetById(orderId)
	if err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	if !order.IsActive() {
		return nil, fmt.Errorf("cancel order: %w", types.ErrOrderAlreadyFilled)
	}

	order.Status = types.OrderStatusCancelled
	order.UpdatedAt = time.Now().UTC()
	s.store.Update(order)

	if err := s.store.Update(order); err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	return order, nil
}

func (s *Service) ApplyFill(orderId string, filledQuantity decimal.Decimal) (*types.Order, error) {
	order, err := s.store.GetById(orderId)
	if err != nil {
		return nil, fmt.Errorf("Get order:%w", err)
	}
	if !order.IsActive() {
		return nil, fmt.Errorf("apply fill , order %s is not active", orderId)
	}
	order.Filled = order.Filled.Add(filledQuantity)
	order.UpdatedAt = time.Now().UTC()

	if order.IsFilled() {
		order.Status = types.OrderStatusCompleted
	} else {
		order.Status = types.OrderStatusPartial
	}
	if err := s.store.Update(order); err != nil {
		return nil, fmt.Errorf("Apply fill : %w", err)
	}
	return order, nil
}

func (s *Service) Reject(orderId string) (*types.Order, error) {
	order, err := s.store.GetById(orderId)
	if err != nil {
		return nil, fmt.Errorf("Order not found: %w", err)
	}
	order.Status = types.OrderStatusRejected
	order.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(order); err != nil {
		return nil, fmt.Errorf("Apply fill : %w", err)
	}
	return order, nil
}

func (s *Service) GetClientOrders(clientId string) ([]*types.Order, error) {
	orders, err := s.store.GetByClient(clientId)
	if err != nil {
		return nil, fmt.Errorf("Get orders by client: %w", err)
	}
	return orders, nil
}
func (s *Service) ExecuteFill(incoming, resting *types.Order, qty, price decimal.Decimal) (*types.Trade, error) {
	if !qty.IsPositive() {
		return nil, fmt.Errorf("execute fill: %w", types.ErrInvalidQuantity)
	}
	for _, o := range []*types.Order{incoming, resting} {
		if _, err := s.store.GetById(o.Id); err != nil {
			return nil, fmt.Errorf("execute fill: order %s: %w", o.Id, err)
		}
		if !o.IsActive() {
			return nil, fmt.Errorf("execute fill: order %s is not active", o.Id)
		}
		if qty.GreaterThan(o.RemainingQuantity()) {
			return nil, fmt.Errorf("execute fill: quantity %s exceeds remaining %s on order %s",
				qty, o.RemainingQuantity(), o.Id)
		}
		if incoming.ClientId == resting.ClientId {
			return nil, fmt.Errorf("execute fill: %w", types.ErrSelfTrade)
		}
	}

	now := time.Now().UTC()
	applyFill(incoming, qty, now)
	applyFill(resting, qty, now)

	buyId, sellId := incoming.Id, resting.Id
	if incoming.Side == types.SideSell {
		buyId, sellId = resting.Id, incoming.Id
	}
	trade := types.NewTrade(incoming.Symbol, buyId, sellId, price, qty, now)

	if err := s.store.RecordFill(incoming, resting, trade); err != nil {
		return nil, fmt.Errorf("execute fill: %w", err)
	}
	return trade, nil
}

func applyFill(o *types.Order, qty decimal.Decimal, now time.Time) {
	o.Filled = o.Filled.Add(qty)
	o.UpdatedAt = now
	if o.IsFilled() {
		o.Status = types.OrderStatusCompleted

	} else {
		o.Status = types.OrderStatusPartial

	}
}
