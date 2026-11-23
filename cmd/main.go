package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tradingengine/internal/db"
	"tradingengine/internal/gateway"
	"tradingengine/internal/matching"
	"tradingengine/internal/order"
	"tradingengine/internal/queue"
	"tradingengine/internal/server"
	"tradingengine/pkg/types"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, using environment variables")
	}

	// Fail fast, before anything is started.
	apiKey := os.Getenv("ENGINE_API_KEY")
	if apiKey == "" {
		log.Fatal("ENGINE_API_KEY is not set")
	}

	log.Println("Starting trading engine")
	ctx := context.Background()

	// --- Postgres ---
	pool, err := db.NewPostgresPool(ctx)
	if err != nil {
		log.Fatal(err)
	}
	postgresClient := db.NewClient(pool)
	defer postgresClient.Close() // runs last, after everything below has flushed

	migrateCtx, migrateCancel := context.WithTimeout(ctx, 30*time.Second)
	err = postgresClient.Migrate(migrateCtx)
	migrateCancel()
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// --- Stores and services ---
	dbStorage := db.NewDBStorage(postgresClient)
	memStore := order.NewMemoryStore()
	persistentStore := order.NewPersistentStore(memStore, dbStorage, 1000)
	go persistentStore.Run()
	orderService := order.NewService(persistentStore)

	orderQueue := queue.NewOrderQueue(100)
	engine := matching.NewEngine(orderQueue, orderService, 100)

	// Separate contexts so shutdown can stop the engine first, then the
	// trade consumer once the engine can no longer produce trades.
	engineCtx, stopEngine := context.WithCancel(ctx)
	tradesCtx, stopTrades := context.WithCancel(ctx)

	// 1. trade consumer first (restore may produce trades)
	tradesDone := make(chan struct{})
	go func() {
		defer close(tradesDone)
		consumeTrades(tradesCtx, engine)
	}()

	// 2. rebuild the book before the engine goroutine exists
	if err := restoreOrders(ctx, dbStorage, memStore, engine); err != nil {
		log.Fatalf("restore: %v", err)
	}

	// 3. start matching
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		engine.Run(engineCtx)
	}()

	// 4. only now accept traffic
	gw := gateway.NewGateway(orderService, orderQueue, gateway.Readers{
		Books:  engine,
		Trades: dbStorage,
		Health: postgresClient,
	})
	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           server.NewRouter(gw, apiKey),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("http server listening on %s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server error: %v", err)
		}
	}()

	log.Println("trading engine running, gateway ready")

	WaitForShutDown()
	log.Println("shutdown signal received")

	// Shutdown, in dependency order:
	// stop intake -> stop engine -> flush trades -> flush order updates.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown error: %v", err)
	}

	stopEngine()
	<-engineDone // no more trades or order updates can be produced after this

	stopTrades()
	<-tradesDone // every trade has been written

	persistentStore.Close() // every order update has been written

	log.Println("trading engine stopped")
}

func consumeTrades(ctx context.Context, engine *matching.Engine) {
	for {
		select {
		case trade, ok := <-engine.Trades():
			if !ok {
				return
			}
			logTrade(trade)
		case <-ctx.Done():
			for {
				select {
				case trade, ok := <-engine.Trades():
					if !ok {
						return
					}
					logTrade(trade)
				default:
					return
				}
			}
		}
	}
}

func logTrade(trade *types.Trade) {
	log.Printf("[TRADE] symbol=%s price=%s qty=%s buy=%s sell=%s",
		trade.Symbol, trade.Price.String(), trade.Quantity.String(),
		trade.BuyOrderId, trade.SellOrderId)
}

func WaitForShutDown() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}

func restoreOrders(
	ctx context.Context,
	dbStorage *db.DBStorage,
	memStore order.Store,
	engine *matching.Engine,
) error {
	restoreCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	orders, err := dbStorage.ListActiveOrders(restoreCtx)
	if err != nil {
		return fmt.Errorf("load active orders: %w", err)
	}

	// Into memory only: going through persistentStore.Save would try to
	// INSERT rows that already exist.
	for _, o := range orders {
		if err := memStore.Save(o); err != nil {
			return fmt.Errorf("restore order %s into memory: %w", o.Id, err)
		}
	}

	engine.Restore(orders)
	return nil
}

func handleTrade(store *db.DBStorage, trade *types.Trade) {
	log.Printf("[TRADE] symbol=%s price=%s qty=%s buy=%s sell=%s",
		trade.Symbol, trade.Price.String(), trade.Quantity.String(),
		trade.BuyOrderId, trade.SellOrderId)

	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = store.InsertTrade(ctx, trade)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	log.Printf("PERSISTENCE FAILED trade=%s: %v", trade.Id, err)
}
