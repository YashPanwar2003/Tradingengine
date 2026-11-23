CREATE TABLE IF NOT EXISTS trades (
    id            UUID PRIMARY KEY,
    symbol        TEXT           NOT NULL,
    buy_order_id  UUID           NOT NULL REFERENCES orders (id),
    sell_order_id UUID           NOT NULL REFERENCES orders (id),
    price         NUMERIC(20, 8) NOT NULL CHECK (price > 0),
    quantity      NUMERIC(20, 8) NOT NULL CHECK (quantity > 0),
    executed_at   TIMESTAMPTZ    NOT NULL,

    CHECK (buy_order_id <> sell_order_id)
);

CREATE INDEX IF NOT EXISTS idx_trades_symbol_time ON trades (symbol, executed_at DESC);
CREATE INDEX IF NOT EXISTS idx_trades_buy_order   ON trades (buy_order_id);
CREATE INDEX IF NOT EXISTS idx_trades_sell_order  ON trades (sell_order_id);