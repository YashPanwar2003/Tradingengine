package gateway

import (
	"context"
	"fmt"

	"tradingengine/pkg/types"
)

type BookReader interface {
	Snapshot(ctx context.Context, symbol string, depth int) (*types.BookSnapshot, error)
}

type TradeReader interface {
	ListTrades(ctx context.Context, symbol string, limit int) ([]*types.Trade, error)
	ListTradesByOrder(ctx context.Context, orderID string) ([]*types.Trade, error)
}

type Pinger interface {
	Ping(ctx context.Context) error
}

// Readers are the read-side dependencies. *matching.Engine, *db.DBStorage and
// *db.Client satisfy them, so the gateway never imports those packages.
type Readers struct {
	Books  BookReader
	Trades TradeReader
	Health Pinger
}

func (g *Gateway) GetBook(ctx context.Context, symbol string, depth int) (*types.BookSnapshot, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required: %w", types.ErrInvalidRequest)
	}
	return g.books.Snapshot(ctx, symbol, depth)
}

func (g *Gateway) ListTrades(ctx context.Context, symbol string, limit int) ([]*types.Trade, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required: %w", types.ErrInvalidRequest)
	}
	return g.trades.ListTrades(ctx, symbol, limit)
}

// GetOrderTrades answers 404 for an unknown order, and an empty list for a
// known order that has not traded yet.
func (g *Gateway) GetOrderTrades(ctx context.Context, orderID string) ([]*types.Trade, error) {
	if _, err := g.orderService.Get(orderID); err != nil {
		return nil, fmt.Errorf("get order trades: %w", err)
	}
	return g.trades.ListTradesByOrder(ctx, orderID)
}

func (g *Gateway) Ready(ctx context.Context) error {
	return g.health.Ping(ctx)
}
