package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tradingengine/internal/gateway"
	"tradingengine/internal/matching"
	"tradingengine/internal/order"
	"tradingengine/internal/queue"
)


func main(){
	log.Println("Starting trading engine")
	ctx,cancel:=context.WithCancel(context.Background())

	defer cancel()

	orderStore:= order.NewMemoryStore()
	orderService:=order.NewService(orderStore)

	orderQueue:=queue.NewOrderQueue(100)

	engine:=matching.NewEngine(orderQueue,orderService,100)
    go engine.Run(ctx)
	gw:=gateway.NewGateway(orderService,orderQueue)

	go consumeTrades(ctx,engine)
	log.Println("trading engine running, gateway ready")

	_=gw

	WaitForShutDown()
	log.Println("trading engine running, gateway ready")
	log.Println("shutdown signal received")

	cancel() 

	log.Println("trading engine stopped")

}


func consumeTrades(ctx context.Context,engine *matching.Engine){
	for{
		select{
		case trade,ok := <-engine.Trades():
			if !ok{
				return
			}
			log.Printf("[TRADE] symbol=%s price=%s qty=%s buy=%s sell=%s",
				trade.Symbol, trade.Price.String(), trade.Quantity.String(),
				trade.BuyOrderId, trade.SellOrderId)
		case <-ctx.Done():
			return
		}
	}
}
func WaitForShutDown(){
	sigCh:=make(chan os.Signal,1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}