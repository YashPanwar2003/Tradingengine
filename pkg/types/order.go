package types

import (
	"time"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	
)

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeMarket OrderType = "MARKET"
)

type OrderStatus string	

const (
	OrderStatusNew       OrderStatus = "NEW"
	OrderStatusPartial   OrderStatus = "PARTIAL"
	OrderStatusCompleted OrderStatus = "COMPLETED"
	OrderStatusCancelled OrderStatus = "CANCELLED"
	OrderStatusRejected  OrderStatus = "REJECTED"
)
type TimeInForce string

const (
    TimeInForceGTC TimeInForce = "GTC"
    TimeInForceIOC TimeInForce = "IOC" 
    TimeInForceFOK TimeInForce = "FOK" 
)

type Order struct {
	Id string 
	ClientId string
	Symbol string
	Side Side
	Type OrderType
	Status OrderStatus
	TimeInForce TimeInForce
	Price  decimal.Decimal
	Quantity decimal.Decimal
	Filled decimal.Decimal
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (o *Order) RemainingQuanitity() decimal.Decimal{
	return o.Quantity.Sub(o.Filled)
}

func (o *Order) IsFilled() bool{
	return o.Filled.GreaterThanOrEqual(o.Quantity)
}

func (o *Order) IsActive() bool{
	return o.Status == OrderStatusNew || o.Status == OrderStatusPartial
}

func NewOrder(clientId , symbol string,side Side ,orderType OrderType , price , quantity decimal.Decimal, tif TimeInForce) *Order{
	now:= time.Now().UTC()
	return &Order{
		Id: uuid.New().String(),
		ClientId: clientId,
		TimeInForce: tif,
		Symbol: symbol,
		Side: side,
		Price: price,
		Quantity: quantity,
		Status: OrderStatusNew,
		Filled: decimal.Zero,
        CreatedAt: now,
		UpdatedAt: now,
	}
}