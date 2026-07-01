package matching

import (
	"log"
	"time"
	"tradingengine/internal/order"
	"tradingengine/internal/orderbook"
	"tradingengine/pkg/types"

	"github.com/shopspring/decimal"
)

type Matcher struct {
	orderService *order.Service
	tradeCh      chan *types.Trade
}

func NewMatcher(orderService *order.Service, tradeCh chan *types.Trade) *Matcher {
	return &Matcher{
		orderService: orderService,
		tradeCh:      tradeCh,
	}
}

func (m *Matcher) Match(incoming *types.Order, book *orderbook.Book) {
	switch incoming.Type {
	case types.OrderTypeLimit:
		m.MatchLimit(incoming, book)
	case types.OrderTypeMarket:
		m.MatchMarket(incoming, book)
	}
}

func (m *Matcher) MatchMarket(incoming *types.Order, book *orderbook.Book) {
	for !incoming.IsFilled() {
		var best *orderbook.Level
		if incoming.Side == types.SideBuy {
			best = book.BestAsk()
		} else {
			best = book.BestBid()
		}
		if best == nil {
			break
		}
		m.FillAgainstLevel(incoming, best, book) 
	}
	if !incoming.IsFilled() {
		_ , err := m.orderService.Reject(incoming.Id)
		if err != nil {
			log.Printf("reject market order %s failed: %v", incoming.Id, err)
		}
	}
}
func (m *Matcher) MatchLimit(incoming *types.Order, book *orderbook.Book) {
	for !incoming.IsFilled() {
		var best *orderbook.Level
		if incoming.Side == types.SideBuy {
			best = book.BestAsk()
			if best == nil || best.Price.GreaterThan(incoming.Price) {
				break
			}
		} else {
			best = book.BestBid()
			if best == nil || best.Price.LessThan(incoming.Price) {
				break
			}
		}
		m.FillAgainstLevel(incoming, best, book)
		if incoming.IsActive() && !incoming.IsFilled() {
			book.Add(incoming)
		}
	}
}


func (m *Matcher) FillAgainstLevel(incoming *types.Order, level *orderbook.Level, book *orderbook.Book) {
	for !incoming.IsFilled() || !level.IsEmpty() {
		resting := level.Front()
		fillQuantity := decimal.Min(
			incoming.RemainingQuanitity(),
			resting.RemainingQuanitity(),
		)
		tradePrice := resting.Price
		_, err := m.orderService.ApplyFill(incoming.Id, fillQuantity)
		if err != nil {
			log.Printf("apply fill incoming %s: %v", incoming.Id, err)
			return
		}
		_, err1 := m.orderService.ApplyFill(resting.Id, fillQuantity)
		if err1 != nil {
			log.Printf("apply fill resting %s: %v", resting.Id, err1)
			return
		}
		trade := types.NewTrade(
			incoming.Symbol,
			getBuyOrderId(incoming, resting),
			getSellOrderId(incoming, resting),
			tradePrice,
			fillQuantity,
			time.Now().UTC(),
		)
		m.tradeCh <- trade
		log.Printf("trade executed: %s qty=%s price=%s",
			trade.Symbol, fillQuantity.String(), tradePrice.String())

		if resting.IsFilled() {
			level.PopFront()
		}
	}
	if level.IsEmpty() {
		if incoming.Side == types.SideBuy {
			book.CleanLevel(types.SideSell, level.Price)
		} else {
			book.CleanLevel(types.SideBuy, level.Price)
		}
	}
}
func getBuyOrderId(incoming, resting *types.Order) string {
	if incoming.Side == types.SideBuy {
		return incoming.Id
	}
	return resting.Id
}

func getSellOrderId(incoming, resting *types.Order) string {
	if incoming.Side == types.SideSell {
		return incoming.Id
	}
	return resting.Id
}
