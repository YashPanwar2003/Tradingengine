package gateway

import "tradingengine/pkg/types"


type PlaceOrderRequest struct{
	ClientId string
	Symbol string
	Side   types.Side
	Type types.OrderType
	Price string
	Quantity string
	TimeInForce types.TimeInForce
}

type PlaceOrderResponse struct{
	Order *types.Order
	Message string
}

type CacelOrderRequest struct{
	ClientId string
	OrderId string
}

type  CancelOrderReponse struct{
	OrderId string
	Message string
}