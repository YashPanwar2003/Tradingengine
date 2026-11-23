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
	Id          string          `json:"id"`
	ClientId    string          `json:"clientId"`
	Symbol      string          `json:"symbol"`
	Side        Side            `json:"side"`
	Type        OrderType       `json:"type"`
	Status      OrderStatus     `json:"status"`
	TimeInForce TimeInForce     `json:"timeInForce"`
	Price       decimal.Decimal `json:"price"`
	Quantity    decimal.Decimal `json:"quantity"`
	Filled      decimal.Decimal `json:"filled"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

func (o *Order) RemainingQuantity() decimal.Decimal {
	return o.Quantity.Sub(o.Filled)
}

func (o *Order) IsFilled() bool {
	return o.Filled.GreaterThanOrEqual(o.Quantity)
}

func (o *Order) IsActive() bool {
	return o.Status == OrderStatusNew || o.Status == OrderStatusPartial
}

func NewOrder(clientId, symbol string, side Side, orderType OrderType, price, quantity decimal.Decimal, tif TimeInForce) *Order {
	now := time.Now().UTC()
	if tif == "" {
		tif = TimeInForceGTC // no time-in-force given means good-til-cancelled
	}
	return &Order{
		Id:          uuid.New().String(),
		ClientId:    clientId,
		TimeInForce: tif,
		Symbol:      symbol,
		Side:        side,
		Type:        orderType, // was missing: every order had Type "" and the matcher ignored it
		Price:       price,
		Quantity:    quantity,
		Status:      OrderStatusNew,
		Filled:      decimal.Zero,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
func (o *Order) Clone() *Order {
	c := *o
	return &c
}
