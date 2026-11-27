package matching

import (
	"log"

	"tradingengine/internal/order"
	"tradingengine/internal/orderbook"
	"tradingengine/pkg/types"

	"github.com/shopspring/decimal"
)

type Matcher struct {
	orderService *order.Service
	tradeCh      chan *types.Trade
}

type fillResult int

const (
	fillContinue  fillResult = iota 
	fillAborted                     
	fillSelfTrade                   
)
func NewMatcher(orderService *order.Service, tradeCh chan *types.Trade) *Matcher {
	return &Matcher{
		orderService: orderService,
		tradeCh:      tradeCh,
	}
}

// Match runs an incoming order against the book. What happens to the part
// that cannot trade right away depends on the order's type and time-in-force:
//
//	LIMIT  + GTC    rest the remainder on the book
//	LIMIT  + IOC    cancel the remainder
//	LIMIT  + FOK    all or nothing: if the whole quantity cannot trade at the
//	                limit price or better, cancel without trading at all
//	MARKET + FOK    the same all-or-nothing rule, at any price
//	MARKET + other  a market order never rests, so the remainder is cancelled
//
// Status convention: REJECTED means the order was invalid and never touched
// the book. CANCELLED means a valid order ended with quantity left unfilled
// (Filled may be zero or more).
func (m *Matcher) Match(incoming *types.Order, book *orderbook.Book) {
	if !isValid(incoming) {
		m.reject(incoming)
		return
	}

	// FOK is all or nothing, so decide before the book is touched.
	if incoming.TimeInForce == types.TimeInForceFOK && !canFillCompletely(incoming, book) {
		m.cancel(incoming)
		return
	}

	switch incoming.Type {
	case types.OrderTypeLimit:
		m.MatchLimit(incoming, book)
	case types.OrderTypeMarket:
		m.MatchMarket(incoming, book)
	}
}

func (m *Matcher) MatchMarket(incoming *types.Order, book *orderbook.Book) {
	loop:
	for !incoming.IsFilled() {
		best := bestOpposite(incoming, book)
		if best == nil {
			break
		}
		switch m.FillAgainstLevel(incoming, best, book) {
		case fillSelfTrade:
			m.preventSelfTrade(incoming)
			return
		case fillAborted:
			break loop
		}
	}
	if !incoming.IsFilled() {
		m.cancel(incoming) // a market order never rests
	}
}

func (m *Matcher) MatchLimit(incoming *types.Order, book *orderbook.Book) {
	loop:
	for !incoming.IsFilled() {
		best := bestOpposite(incoming, book)
		if best == nil || !crosses(incoming, best.Price) {
			break
		}
		switch m.FillAgainstLevel(incoming, best, book) {
		case fillSelfTrade:
			m.preventSelfTrade(incoming)
			return
		case fillAborted:
			break loop
		}
	}
	if incoming.IsFilled() {
		return
	}
	if incoming.TimeInForce == types.TimeInForceGTC {
		book.Add(incoming) // rests with whatever is left, exactly once
		return
	}
	m.cancel(incoming) // IOC remainder (or a FOK that fell short after a failed fill)
}

// FillAgainstLevel trades the incoming order against the orders resting at one
// price level, oldest first. It returns false if it had to stop early because
// a fill could not be applied, so callers never loop forever on the same level.
func (m *Matcher) FillAgainstLevel(incoming *types.Order, level *orderbook.Level, book *orderbook.Book) fillResult {
	for !incoming.IsFilled() && !level.IsEmpty() {
		resting := level.Front()
		if resting.ClientId == incoming.ClientId{
		    return fillSelfTrade
		}
		fillQuantity := decimal.Min(
			incoming.RemainingQuantity(),
			resting.RemainingQuantity(),
		)
		tradePrice := resting.Price

		trade, err := m.orderService.ExecuteFill(incoming, resting, fillQuantity, tradePrice)
		if err != nil {
			log.Printf("execute fill incoming=%s resting=%s: %v", incoming.Id, resting.Id, err)
			return fillAborted
		}
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
	return fillContinue
}

func (m *Matcher) cancel(o *types.Order) {
	if _, err := m.orderService.Cancel(o.Id); err != nil {
		log.Printf("cancel order %s failed: %v", o.Id, err)
	}
}

func (m *Matcher) reject(o *types.Order) {
	if _, err := m.orderService.Reject(o.Id); err != nil {
		log.Printf("reject order %s failed: %v", o.Id, err)
	}
}

// isValid is a last line of defence; the gateway should already have refused
// these. An order that fails it is rejected instead of silently doing nothing.
func isValid(o *types.Order) bool {
	if o.Side != types.SideBuy && o.Side != types.SideSell {
		return false
	}
	if o.TimeInForce != types.TimeInForceGTC &&
		o.TimeInForce != types.TimeInForceIOC &&
		o.TimeInForce != types.TimeInForceFOK {
		return false
	}
	if !o.Quantity.IsPositive() {
		return false
	}
	switch o.Type {
	case types.OrderTypeLimit:
		return o.Price.IsPositive()
	case types.OrderTypeMarket:
		return true
	}
	return false
}

// canFillCompletely reports whether the book can absorb the order's whole
// remaining quantity at acceptable prices. It does not change the book.
func canFillCompletely(incoming *types.Order, book *orderbook.Book) bool {
	var limit *decimal.Decimal
	if incoming.Type == types.OrderTypeLimit {
		limit = &incoming.Price
	}
	need := incoming.RemainingQuantity()
	return book.Liquidity(incoming.Side, limit, need, incoming.ClientId).GreaterThanOrEqual(need)
}

func bestOpposite(incoming *types.Order, book *orderbook.Book) *orderbook.Level {
	if incoming.Side == types.SideBuy {
		return book.BestAsk()
	}
	return book.BestBid()
}

// crosses reports whether a limit order is willing to trade at the level's price.
func crosses(incoming *types.Order, levelPrice decimal.Decimal) bool {
	if incoming.Side == types.SideBuy {
		return levelPrice.LessThanOrEqual(incoming.Price)
	}
	return levelPrice.GreaterThanOrEqual(incoming.Price)
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

func (m *Matcher) preventSelfTrade(o *types.Order){
	log.Printf("self-trade prevented: cancelling remainder of order %s (client %s)", o.Id, o.ClientId)
	m.cancel(o)
}

