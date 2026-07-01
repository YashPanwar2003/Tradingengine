package matching

import (
	"context"
	"log"
	"tradingengine/internal/order"
	"tradingengine/internal/orderbook"
	"tradingengine/internal/queue"
	"tradingengine/pkg/types"
)

type Engine struct{
	queue *queue.OrderQueue
	books map[string]*orderbook.Book
	orderService *order.Service
	matcher *Matcher
	tradeCh chan *types.Trade
	
}
func NewEngine(
	q *queue.OrderQueue,
	orderService *order.Service,
	tradeBuffer int,
)*Engine{
	e:=&Engine{
		queue: q,
		orderService: orderService,
		books:make(map[string]*orderbook.Book),
		tradeCh: make(chan *types.Trade,tradeBuffer),
	}
	e.matcher=NewMatcher(orderService,e.tradeCh)
	return e
}

func (e *Engine) Run(ctx context.Context){
	log.Println("Matching Engine started")
	for{
		select{
		case req,ok:=<-e.queue.Consume():
			if !ok{
				log.Println("matching engine is shutting down")
				return
			}
			e.Handle(req)

		case <-ctx.Done():
			log.Println("matching engine context cancelled")
			return
		}
		
	}
}

func (e *Engine) Handle(request queue.OrderRequest){
	switch request.Type{
	case queue.RequestTypePlace:
		e.HandlePlace(request.Order)
	case queue.RequestTypeCancel:
		e.HandleCancel(request.Order)
	}
}

func (e *Engine) HandlePlace(order *types.Order){
    book:=e.GetOrCreateBook(order.Symbol)
	e.matcher.Match(order,book)
}

func (e *Engine) HandleCancel(order *types.Order){
	book,exists:=e.books[order.Symbol]
	if !exists {
		return
	}
	book.CancelOrder(order)
	_, err := e.orderService.Cancel(order.Id)
	if err != nil {
		log.Printf("cancel order %s failed: %v", order.Id, err)
	}
}

func (e *Engine) GetOrCreateBook(symbol string) *orderbook.Book{

	book,exists:=e.books[symbol]
	if !exists{
		book = orderbook.NewBook(symbol)
		e.books[symbol]=book
	}
	return book

}

func (e *Engine) Trades() <-chan *types.Trade{
	return e.tradeCh
}