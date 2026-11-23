package orderbook

import (
	"github.com/shopspring/decimal"
	"tradingengine/pkg/types"
)

type Level struct {
	Price  decimal.Decimal
	Orders []*types.Order
}

func NewLevel(price decimal.Decimal) *Level {
	return &Level{
		Price:  price,
		Orders: make([]*types.Order, 0),
	}
}

func (l *Level) Add(order *types.Order) {
	l.Orders = append(l.Orders, order)
}

func (l *Level) Front() *types.Order {
	if len(l.Orders) == 0 {
		return nil
	}
	return l.Orders[0]
}
func (l *Level) Remove(orderId string) bool {
	for i, o := range l.Orders {
		if o.Id == orderId {
			l.Orders = append(l.Orders[:i], l.Orders[i+1:]...)
			return true
		}
	}
	return false
}

func (l *Level) PopFront() *types.Order {
	if len(l.Orders) == 0 {
		return nil
	}
	order := l.Orders[0]
	l.Orders = l.Orders[1:]
	return order
}

func (l *Level) Len() int {
	return len(l.Orders)
}
func (l *Level) IsEmpty() bool {
	return l.Len() == 0
}

// Quantity is the total unfilled quantity resting at this price.
func (l *Level) Quantity() decimal.Decimal {
	total := decimal.Zero
	for _, o := range l.Orders {
		total = total.Add(o.RemainingQuantity())
	}
	return total
}
