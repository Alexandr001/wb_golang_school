-- +goose Up
-- +goose StatementBegin
-- Достаём items пачкой по списку заказов
CREATE INDEX idx_items_order_uid ON items (order_uid);
-- +goose StatementEnd

-- +goose StatementBegin
-- Прогрев кэша
CREATE INDEX idx_orders_date_created ON orders (date_created DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_date_created;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_items_order_uid;
-- +goose StatementEnd
