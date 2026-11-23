package matching

import (
	"context"
	"time"

	"tradingengine/internal/orderbook"
	"tradingengine/pkg/types"
)

type snapshotRequest struct {
	symbol string
	depth  int
	reply  chan *types.BookSnapshot // buffered, so the engine never blocks on a caller that gave up
}

// Snapshot asks the engine goroutine for a copy of one book. It is safe to
// call from any goroutine, and it gives up when ctx ends.
func (e *Engine) Snapshot(ctx context.Context, symbol string, depth int) (*types.BookSnapshot, error) {
	if depth < 1 {
		depth = 1
	}
	req := snapshotRequest{symbol: symbol, depth: depth, reply: make(chan *types.BookSnapshot, 1)}

	select {
	case e.snapshots <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case snap := <-req.reply:
		return snap, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// buildSnapshot runs on the engine goroutine, the only one allowed to touch books.
func (e *Engine) buildSnapshot(symbol string, depth int) *types.BookSnapshot {
	snap := &types.BookSnapshot{
		Symbol: symbol,
		Bids:   []types.PriceLevel{}, // never nil, so JSON says [] and not null
		Asks:   []types.PriceLevel{},
		At:     time.Now().UTC(),
	}
	book, ok := e.books[symbol]
	if !ok {
		return snap // reading must never create a book for an arbitrary symbol
	}
	snap.Bids = levelsOf(book.Bids, depth)
	snap.Asks = levelsOf(book.Asks, depth)
	return snap
}

func levelsOf(side *orderbook.Side, depth int) []types.PriceLevel {
	out := make([]types.PriceLevel, 0, depth)
	for _, l := range side.Levels() { // best price first
		if len(out) == depth {
			break
		}
		if l.IsEmpty() {
			continue
		}
		out = append(out, types.PriceLevel{Price: l.Price, Quantity: l.Quantity(), Orders: l.Len()})
	}
	return out
}
