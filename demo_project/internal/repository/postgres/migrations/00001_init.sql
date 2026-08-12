-- +goose Up
-- +goose StatementBegin
CREATE TABLE orders (
    order_uid          TEXT PRIMARY KEY,
    track_number       TEXT NOT NULL,
    entry              TEXT NOT NULL,
    locale             TEXT NOT NULL,
    internal_signature TEXT NOT NULL DEFAULT '',
    customer_id        TEXT NOT NULL,
    delivery_service   TEXT NOT NULL,
    shardkey           TEXT NOT NULL,
    sm_id              INTEGER NOT NULL,
    date_created       TIMESTAMPTZ NOT NULL,
    oof_shard          TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE deliveries (
    order_uid TEXT PRIMARY KEY REFERENCES orders (order_uid) ON DELETE CASCADE,
    name      TEXT NOT NULL,
    phone     TEXT NOT NULL,
    zip       TEXT NOT NULL,
    city      TEXT NOT NULL,
    address   TEXT NOT NULL,
    region    TEXT NOT NULL,
    email     TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE payments (
    order_uid     TEXT PRIMARY KEY REFERENCES orders (order_uid) ON DELETE CASCADE,
    transaction   TEXT NOT NULL,
    request_id    TEXT NOT NULL DEFAULT '',
    currency      TEXT NOT NULL,
    provider      TEXT NOT NULL,
    amount        BIGINT NOT NULL CHECK (amount >= 0),
    payment_dt    BIGINT NOT NULL,
    bank          TEXT NOT NULL,
    delivery_cost BIGINT NOT NULL CHECK (delivery_cost >= 0),
    goods_total   BIGINT NOT NULL CHECK (goods_total >= 0),
    custom_fee    BIGINT NOT NULL DEFAULT 0 CHECK (custom_fee >= 0)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE items (
    id           BIGSERIAL PRIMARY KEY,
    order_uid    TEXT NOT NULL REFERENCES orders (order_uid) ON DELETE CASCADE,
    chrt_id      BIGINT NOT NULL,
    track_number TEXT NOT NULL,
    price        BIGINT NOT NULL CHECK (price >= 0),
    rid          TEXT NOT NULL,
    name         TEXT NOT NULL,
    sale         INTEGER NOT NULL,
    size         TEXT NOT NULL,
    total_price  BIGINT NOT NULL CHECK (total_price >= 0),
    nm_id        BIGINT NOT NULL,
    brand        TEXT NOT NULL,
    status       INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS items;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS payments;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS deliveries;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS orders;
-- +goose StatementEnd
