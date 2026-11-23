package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"tradingengine/pkg/types"
)

var ErrOrderNotFound = types.ErrOrderNotFound

// DBStorage is the Postgres persistence layer. It is NOT the in-memory
// OrderStore the matching engine uses; it only records and recovers state.
type DBStorage struct {
	client *Client
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const updateOrderSQL = `
	UPDATE orders
	SET filled = $2::numeric, status = $3, updated_at = $4
	WHERE id = $1::uuid`

const insertTradeSQL = `
	INSERT INTO trades
		(id, symbol, buy_order_id, sell_order_id, price, quantity, executed_at)
	VALUES
		($1::uuid, $2, $3::uuid, $4::uuid, $5::numeric, $6::numeric, $7)
	ON CONFLICT (id) DO NOTHING`

func updateOrder(ctx context.Context, e execer, o *types.Order) error {
	tag, err := e.Exec(ctx, updateOrderSQL, o.Id, o.Filled.String(), string(o.Status), o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update order %s: %w", o.Id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOrderNotFound
	}
	return nil
}

func insertTrade(ctx context.Context, e execer, t *types.Trade) error {
	_, err := e.Exec(ctx, insertTradeSQL,
		t.Id, t.Symbol, t.BuyOrderId, t.SellOrderId,
		t.Price.String(), t.Quantity.String(), t.ExecutedAt)
	if err != nil {
		return fmt.Errorf("insert trade %s: %w", t.Id, err)
	}
	return nil
}

func NewDBStorage(client *Client) *DBStorage {
	return &DBStorage{client: client}
}

const orderColumns = `id::text, client_id, symbol, side, type, status, time_in_force,
	price::text, quantity::text, filled::text, created_at, updated_at`

// InsertOrder persists a newly created order.
func (s *DBStorage) InsertOrder(ctx context.Context, o *types.Order) error {
	const q = `
		INSERT INTO orders
			(id, client_id, symbol, side, type, status, time_in_force,
			 price, quantity, filled, created_at, updated_at)
		VALUES
			($1::uuid, $2, $3, $4, $5, $6, $7,
			 $8::numeric, $9::numeric, $10::numeric, $11, $12)`

	// Market orders have no price: store NULL, not 0.
	var price *string
	if o.Type == types.OrderTypeLimit {
		p := o.Price.String()
		price = &p
	}

	_, err := s.client.Exec(ctx, q,
		o.Id, o.ClientId, o.Symbol,
		string(o.Side), string(o.Type), string(o.Status), string(o.TimeInForce),
		price, o.Quantity.String(), o.Filled.String(),
		o.CreatedAt, o.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert order %s: %w", o.Id, err)
	}
	return nil
}

// UpdateOrderFill persists the mutable parts of an order after a match or cancel.
func (s *DBStorage) UpdateOrderFill(ctx context.Context, o *types.Order) error {
	return updateOrder(ctx, s.client, o)
}

func (s *DBStorage) GetOrderByID(ctx context.Context, id string) (*types.Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrOrderNotFound
	}
	q := `SELECT ` + orderColumns + ` FROM orders WHERE id = $1::uuid`

	o, err := scanOrder(s.client.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order %s: %w", id, err)
	}
	return o, nil
}

func (s *DBStorage) ListActiveOrders(ctx context.Context) ([]*types.Order, error) {
	q := `SELECT ` + orderColumns + `
		FROM orders
		WHERE status IN ('NEW', 'PARTIAL')
		ORDER BY created_at ASC`

	rows, err := s.client.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list active orders: %w", err)
	}
	defer rows.Close()

	var out []*types.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan active order: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active orders: %w", err)
	}
	return out, nil
}
func (s *DBStorage) InsertTrade(ctx context.Context, t *types.Trade) error {
	return insertTrade(ctx, s.client, t)
}

func (s *DBStorage) RecordFill(ctx context.Context, incoming, resting *types.Order, trade *types.Trade) error {
	tx, err := s.client.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin fill tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful Commit

	// Touch rows in a fixed order so two writers can never deadlock.
	first, second := incoming, resting
	if second.Id < first.Id {
		first, second = second, first
	}
	if err := updateOrder(ctx, tx, first); err != nil {
		return err
	}
	if err := updateOrder(ctx, tx, second); err != nil {
		return err
	}
	if err := insertTrade(ctx, tx, trade); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fill: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanOrder(sc scanner) (*types.Order, error) {
	var (
		o                      types.Order
		side, typ, status, tif string
		price                  *string
		qty, filled            string
	)

	if err := sc.Scan(
		&o.Id, &o.ClientId, &o.Symbol, &side, &typ, &status, &tif,
		&price, &qty, &filled, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return nil, err
	}

	var err error
	if o.Quantity, err = decimal.NewFromString(qty); err != nil {
		return nil, fmt.Errorf("parse quantity %q: %w", qty, err)
	}
	if o.Filled, err = decimal.NewFromString(filled); err != nil {
		return nil, fmt.Errorf("parse filled %q: %w", filled, err)
	}
	o.Price = decimal.Zero
	if price != nil {
		if o.Price, err = decimal.NewFromString(*price); err != nil {
			return nil, fmt.Errorf("parse price %q: %w", *price, err)
		}
	}

	o.Side = types.Side(side)
	o.Type = types.OrderType(typ)
	o.Status = types.OrderStatus(status)
	o.TimeInForce = types.TimeInForce(tif)
	o.CreatedAt = o.CreatedAt.UTC()
	o.UpdatedAt = o.UpdatedAt.UTC()
	return &o, nil
}
func (s *DBStorage) ListOrdersByClient(ctx context.Context, clientID string, limit int) ([]*types.Order, error) {
	q := `SELECT ` + orderColumns + `
		FROM orders
		WHERE client_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := s.client.Query(ctx, q, clientID, limit)
	if err != nil {
		return nil, fmt.Errorf("list orders for client %s: %w", clientID, err)
	}
	defer rows.Close()

	out := make([]*types.Order, 0)
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan client order: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate client orders: %w", err)
	}
	return out, nil
}

const tradeColumns = `id::text, symbol, buy_order_id::text, sell_order_id::text,
	price::text, quantity::text, executed_at`

func scanTrade(sc scanner) (*types.Trade, error) {
	var (
		t          types.Trade
		price, qty string
	)
	if err := sc.Scan(&t.Id, &t.Symbol, &t.BuyOrderId, &t.SellOrderId, &price, &qty, &t.ExecutedAt); err != nil {
		return nil, err
	}
	var err error
	if t.Price, err = decimal.NewFromString(price); err != nil {
		return nil, fmt.Errorf("parse price %q: %w", price, err)
	}
	if t.Quantity, err = decimal.NewFromString(qty); err != nil {
		return nil, fmt.Errorf("parse quantity %q: %w", qty, err)
	}
	t.ExecutedAt = t.ExecutedAt.UTC()
	return &t, nil
}

func (s *DBStorage) queryTrades(ctx context.Context, q string, args ...any) ([]*types.Trade, error) {
	rows, err := s.client.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query trades: %w", err)
	}
	defer rows.Close()

	out := make([]*types.Trade, 0)
	for rows.Next() {
		t, err := scanTrade(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trade: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trades: %w", err)
	}
	return out, nil
}

// ListTrades returns the newest trades of one symbol, served by idx_trades_symbol_time.
func (s *DBStorage) ListTrades(ctx context.Context, symbol string, limit int) ([]*types.Trade, error) {
	q := `SELECT ` + tradeColumns + `
		FROM trades
		WHERE symbol = $1
		ORDER BY executed_at DESC, id DESC
		LIMIT $2`
	return s.queryTrades(ctx, q, symbol, limit)
}

// ListTradesByOrder returns every fill the order took part in, oldest first.
func (s *DBStorage) ListTradesByOrder(ctx context.Context, orderID string) ([]*types.Trade, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrOrderNotFound
	}
	q := `SELECT ` + tradeColumns + `
		FROM trades
		WHERE buy_order_id = $1::uuid OR sell_order_id = $1::uuid
		ORDER BY executed_at ASC, id ASC`
	return s.queryTrades(ctx, q, orderID)
}
