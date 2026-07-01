package gateway

import (
	"fmt"

	"tradingengine/internal/order"
	"tradingengine/internal/queue"
	"tradingengine/pkg/types"

	"github.com/shopspring/decimal"
)

type Gateway struct{
	orderService *order.Service
	queue *queue.OrderQueue
}

func NewGateway(orderService *order.Service, queue *queue.OrderQueue) *Gateway{
	return &Gateway{
		orderService: orderService,
		queue: queue,
	}
}

func (g *Gateway) PlaceOrder(req PlaceOrderRequest)(*PlaceOrderResponse,error){
	price,err:=decimal.NewFromString(req.Price)
	if err!=nil{
		return nil , fmt.Errorf("invalid price %q: %w", req.Price, err)
	}
	quantity,err:= decimal.NewFromString(req.Quantity)
	if err!=nil{
		return nil , fmt.Errorf("invalid quantity %q: %w", req.Quantity, err)
	}
	if quantity.LessThanOrEqual(decimal.Zero){
		return nil,types.ErrInvalidQuantity
	}
	if req.Type == types.OrderTypeLimit && price.LessThanOrEqual(decimal.Zero){
		return nil ,types.ErrInvalidPrice
	}
	newOrder := types.NewOrder(
		req.ClientId,
		req.Symbol,
		req.Side,
		req.Type,
		price,
		quantity,
		req.TimeInForce,
	)
	if err:= g.orderService.Place(newOrder);err!=nil{
		return nil , fmt.Errorf("place order: %w", err)
	}
	if err:= g.queue.Enqueue(newOrder);err!=nil{
		_,err:=g.orderService.Reject(newOrder.Id)
		return nil, fmt.Errorf("Queue request: %w",err)
	}
	return &PlaceOrderResponse{
		Order: newOrder,
		Message: "Order placed Successfully",
	},nil

}

func (g *Gateway) CancelOrder(req CacelOrderRequest)(*CancelOrderReponse,error){
	order,err:=g.orderService.Get(req.OrderId)
	if err!=nil{
		return nil,fmt.Errorf("Cancel order: %w",err)
	}
	if order.ClientId!=req.ClientId {
       return nil , fmt.Errorf("Cancel order: %w",err)
	}
	if !order.IsActive() {
		return nil, fmt.Errorf("Cancel order: %w",types.ErrOrderAlreadyFilled)
	}
	if err:=g.queue.EnqueueCancel(req.OrderId,order); err!=nil{
		return nil , fmt.Errorf("Cancel order: %w",err)
	}

	return &CancelOrderReponse{
		OrderId: order.Id,
		Message: "order cancel request accepted",
	},nil
}

func (g *Gateway) GetOrder(orderId string)(*types.Order,error){
	o,err:=g.orderService.Get(orderId)
	if err!=nil{
		return nil , fmt.Errorf("Get order:  %w",err)
	}
	return o,nil
}

func (g *Gateway) GetClientOrders(clientId string)([]*types.Order,error){
	orders,err:=g.orderService.GetClientOrders(clientId)
	if err!=nil{
		return nil , fmt.Errorf("Get order: %w",err)

	}
	return orders,nil
}