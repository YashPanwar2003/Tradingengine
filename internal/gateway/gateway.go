package gateway

import (
	"errors"
	"fmt"
	"log"

	"tradingengine/internal/order"
	"tradingengine/internal/queue"
	"tradingengine/pkg/types"

	"github.com/shopspring/decimal"
)

type Gateway struct {
	orderService *order.Service
	queue        *queue.OrderQueue
	books        BookReader
	trades       TradeReader
	health       Pinger
}

func NewGateway(orderService *order.Service, queue *queue.OrderQueue, readers Readers) *Gateway {
	return &Gateway{
		orderService: orderService,
		queue:        queue,
		books:        readers.Books,
		trades:       readers.Trades,
		health:       readers.Health,
	}
}
func (g *Gateway) PlaceOrder(req PlaceOrderRequest) (*PlaceOrderResponse, error) {
	if req.ClientId == "" {
		return nil, fmt.Errorf("clientId is required: %w", types.ErrInvalidRequest)
	}
	if req.Symbol == "" {
		return nil, fmt.Errorf("symbol is required: %w", types.ErrInvalidRequest)
	}
	switch req.Side {
	case types.SideBuy, types.SideSell:
	default:
		return nil, types.ErrUnknownSide
	}
	switch req.Type {
	case types.OrderTypeLimit, types.OrderTypeMarket:
	default:
		return nil, types.ErrUnknownOrderType
	}
	switch req.TimeInForce {
	case "", types.TimeInForceGTC, types.TimeInForceIOC, types.TimeInForceFOK:
	default:
		return nil, fmt.Errorf("unknown timeInForce %q: %w", req.TimeInForce, types.ErrInvalidRequest)
	}

	quantity, err := decimal.NewFromString(req.Quantity)
	if err != nil || !quantity.IsPositive() {
		return nil, fmt.Errorf("%w: %q", types.ErrInvalidQuantity, req.Quantity)
	}
	price := decimal.Zero // market orders may omit the price
	if req.Price != "" {
		price, err = decimal.NewFromString(req.Price)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", types.ErrInvalidPrice, req.Price)
		}
	}
	if req.Type == types.OrderTypeLimit && !price.IsPositive() {
		return nil, types.ErrInvalidPrice
	}
	if err := validateClientOrderId(req.ClientOrderId); err != nil {
		return nil, err
	}
	newOrder := types.NewOrder(
		req.ClientId, req.Symbol, req.Side, req.Type,
		price, quantity, req.TimeInForce,
	)
	newOrder.ClientOrderId = req.ClientOrderId
	if err := g.orderService.Place(newOrder); err != nil {
		var dup *order.DuplicateError
		if errors.As(err, &dup) {
			return replayResponse(dup.Existing, newOrder)
		}
		return nil, fmt.Errorf("place order: %w", err)
	}

	// From the moment it is enqueued the engine owns newOrder and mutates it,
	// so the response must carry a snapshot taken BEFORE that.
	snapshot := newOrder.Clone()

	if err := g.queue.Enqueue(newOrder); err != nil {
		if _, rerr := g.orderService.Reject(newOrder.Id); rerr != nil {
			log.Printf("reject order %s after enqueue failure: %v", newOrder.Id, rerr)
		}
		return nil, fmt.Errorf("queue request: %w", err)
	}
	return &PlaceOrderResponse{
		Order:   snapshot,
		Message: "Order placed Successfully",
	}, nil
}

func (g *Gateway) CancelOrder(req CancelOrderRequest) (*CancelOrderResponse, error) {
	order, err := g.orderService.Get(req.OrderId)
	if err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}
	if order.ClientId != req.ClientId {
		return nil, fmt.Errorf("cancel order: %w", types.ErrForbidden)
	}
	if !order.IsActive() {
		return nil, fmt.Errorf("cancel order: %w", types.ErrOrderAlreadyFilled)
	}
	if err := g.queue.EnqueueCancel(req.OrderId, order); err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}
	return &CancelOrderResponse{
		OrderId: order.Id,
		Message: "order cancel request accepted",
	}, nil
}

func (g *Gateway) GetOrder(orderId string) (*types.Order, error) {
	o, err := g.orderService.Get(orderId)
	if err != nil {
		return nil, fmt.Errorf("Get order:  %w", err)
	}
	return o, nil
}

func (g *Gateway) GetClientOrders(clientId string) ([]*types.Order, error) {
	orders, err := g.orderService.GetClientOrders(clientId)
	if err != nil {
		return nil, fmt.Errorf("Get order: %w", err)

	}
	return orders, nil
}

// A retry must repeat the original request exactly.
func replayResponse(existing, requested *types.Order) (*PlaceOrderResponse, error) {
	if !sameRequest(existing, requested) {
		return nil, fmt.Errorf("%w: %q", types.ErrIdempotencyConflict, requested.ClientOrderId)
	}
	return &PlaceOrderResponse{
		Order:    existing,
		Message:  "Duplicate request: returning the original order",
		Replayed: true,
	}, nil
}

func sameRequest(a, b *types.Order) bool {
	return a.Symbol == b.Symbol && a.Side == b.Side && a.Type == b.Type &&
		a.TimeInForce == b.TimeInForce &&
		a.Price.Equal(b.Price) && a.Quantity.Equal(b.Quantity)
}

func validateClientOrderId(id string) error {
	if id == "" {
		return nil // the key is optional
	}
	if len(id) > 64 {
		return fmt.Errorf("clientOrderId must be at most 64 characters: %w", types.ErrInvalidRequest)
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == ':'
		if !ok {
			return fmt.Errorf("clientOrderId may only contain letters, digits, '-', '_', '.' and ':': %w",
				types.ErrInvalidRequest)
		}
	}
	return nil
}
