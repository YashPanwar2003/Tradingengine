ALTER TABLE orders ADD COLUMN IF NOT EXISTS client_order_id TEXT;

ALTER TABLE orders
    ADD CONSTRAINT orders_client_order_id_len
    CHECK (client_order_id IS NULL OR length(client_order_id) BETWEEN 1 AND 64);

-- NULL keys are exempt, so orders without a key can repeat freely
CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_client_order_id
    ON orders (client_id, client_order_id)
    WHERE client_order_id IS NOT NULL;