CREATE TABLE IF NOT EXISTS orders (
    id            UUID PRIMARY KEY,
    client_id     TEXT           NOT NULL,
    symbol        TEXT           NOT NULL,
    side          TEXT           NOT NULL CHECK (side IN ('BUY', 'SELL')),
    type          TEXT           NOT NULL CHECK (type IN ('LIMIT', 'MARKET')),
    status        TEXT           NOT NULL CHECK (status IN ('NEW', 'PARTIAL', 'COMPLETED', 'CANCELLED', 'REJECTED')),
    time_in_force TEXT           NOT NULL CHECK (time_in_force IN ('GTC', 'IOC', 'FOK')),
    price         NUMERIC(20, 8),
    quantity      NUMERIC(20, 8) NOT NULL CHECK (quantity > 0),
    filled        NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (filled >= 0),
    created_at    TIMESTAMPTZ    NOT NULL,
    updated_at    TIMESTAMPTZ    NOT NULL,

    CHECK (filled <= quantity),
    CHECK (type = 'MARKET' OR price IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_orders_client_created ON orders (client_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_active ON orders (symbol, created_at) WHERE status IN ('NEW', 'PARTIAL');