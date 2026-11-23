package gateway

import "tradingengine/pkg/types"

type PlaceOrderRequest struct {
	ClientId    string            `json:"clientId"`
	Symbol      string            `json:"symbol"`
	Side        types.Side        `json:"side"`
	Type        types.OrderType   `json:"type"`
	Price       string            `json:"price"`
	Quantity    string            `json:"quantity"`
	TimeInForce types.TimeInForce `json:"timeInForce"`
}

type PlaceOrderResponse struct {
	Order   *types.Order `json:"order"`
	Message string       `json:"message"`
}

type CancelOrderRequest struct {
	ClientId string
	OrderId  string
}

type CancelOrderResponse struct {
	OrderId string `json:"orderId"`
	Message string `json:"message"`
}
