package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Client struct {
	pool *pgxpool.Pool
}

func NewClient(pool *pgxpool.Pool) *Client {
	return &Client{
		pool: pool,
	}
}
func (c *Client) Exec(
	ctx context.Context,
	query string,
	args ...any,
) (pgconn.CommandTag, error) {
	return c.pool.Exec(ctx, query, args...)
}

func (c *Client) Query(
	ctx context.Context,
	query string,
	args ...any,
) (pgx.Rows, error) {
	return c.pool.Query(ctx, query, args...)
}

func (c *Client) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) pgx.Row {
	return c.pool.QueryRow(ctx, query, args...)
}

func (c *Client) BeginTx(
	ctx context.Context,
	opts pgx.TxOptions,
) (pgx.Tx, error) {
	return c.pool.BeginTx(ctx, opts)
}

func (c *Client) Ping(ctx context.Context) error {
	return c.pool.Ping(ctx)
}

func (c *Client) Close() {
	c.pool.Close()
}
