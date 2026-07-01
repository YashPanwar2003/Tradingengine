package orderbook
import (
	"github.com/shopspring/decimal"
	"tradingengine/pkg/types"
)


type Book struct{
	Symbol string
	Bids *Side
	Asks *Side
}

func NewBook(symbol string) *Book{
	return &Book{
		Symbol: symbol,
		Bids:NewSide(true),
		Asks:NewSide(false),
	}
}

func (b *Book) Add(order *types.Order){
	switch order.Side{
	case types.SideBuy:
		b.Bids.Add(order)
	case types.SideSell:
		b.Asks.Add(order)
	}
}


func (b *Book) BestBid() *Level{
   return b.Bids.Best()
}
func (b *Book) BestAsk() *Level{
	return b.Asks.Best()
}

func (b *Book) CancelOrder(order *types.Order) bool{
	switch order.Side{
	case types.SideBuy:
		return b.Bids.CancelOrder(order.Id,order.Price)
	case types.SideSell:
		return b.Asks.CancelOrder(order.Id,order.Price)
	}
	return false

}

func (b *Book) CanMatch() bool{
	bid:=b.BestBid()
	ask:=b.BestAsk()
	if bid == nil || ask == nil{
		return false
	}
	return bid.Price.GreaterThanOrEqual(ask.Price)
}

func (b *Book) CleanLevel(side types.Side, price decimal.Decimal) {
	switch side {
	case types.SideBuy:
		b.Bids.RemoveLevel(price)
	case types.SideSell:
		b.Asks.RemoveLevel(price)
	}
}