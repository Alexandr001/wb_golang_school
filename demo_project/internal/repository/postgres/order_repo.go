package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexandr001/wb_golang_school/demo_project/internal/domain"
)

// Класс ошибки PostgreSQL — первые две цифры SQLSTATE.
const sqlStateClassLen = 2

// Колонки перечислены явно: при SELECT * первая же миграция молча разъедет Scan.
const selectOrderColumns = `
	o.order_uid, o.track_number, o.entry, o.locale, o.internal_signature,
	o.customer_id, o.delivery_service, o.shardkey, o.sm_id, o.date_created, o.oof_shard,
	d.name, d.phone, d.zip, d.city, d.address, d.region, d.email,
	p.transaction, p.request_id, p.currency, p.provider, p.amount, p.payment_dt,
	p.bank, p.delivery_cost, p.goods_total, p.custom_fee`

// deliveries/payments пишутся той же транзакцией, что и orders: сирот не бывает,
// INNER JOIN безопасен.
const selectOrderFrom = `
	FROM orders o
	JOIN deliveries d ON d.order_uid = o.order_uid
	JOIN payments p ON p.order_uid = o.order_uid`

const (
	getOrderQuery = `SELECT` + selectOrderColumns + selectOrderFrom + `
	WHERE o.order_uid = $1`

	listRecentQuery = `SELECT` + selectOrderColumns + selectOrderFrom + `
	ORDER BY o.date_created DESC
	LIMIT $1`

	// Одним запросом на все заказы: иначе прогрев кэша выродится в N+1.
	selectItemsQuery = `
	SELECT ` + itemColumns + `
	FROM items
	WHERE order_uid = ANY($1)
	ORDER BY order_uid, id`

	upsertOrderQuery = `
	INSERT INTO orders (
		order_uid, track_number, entry, locale, internal_signature,
		customer_id, delivery_service, shardkey, sm_id, date_created, oof_shard
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (order_uid) DO UPDATE SET
		track_number       = EXCLUDED.track_number,
		entry              = EXCLUDED.entry,
		locale             = EXCLUDED.locale,
		internal_signature = EXCLUDED.internal_signature,
		customer_id        = EXCLUDED.customer_id,
		delivery_service   = EXCLUDED.delivery_service,
		shardkey           = EXCLUDED.shardkey,
		sm_id              = EXCLUDED.sm_id,
		date_created       = EXCLUDED.date_created,
		oof_shard          = EXCLUDED.oof_shard,
		updated_at         = now()`

	upsertDeliveryQuery = `
	INSERT INTO deliveries (order_uid, name, phone, zip, city, address, region, email)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT (order_uid) DO UPDATE SET
		name    = EXCLUDED.name,
		phone   = EXCLUDED.phone,
		zip     = EXCLUDED.zip,
		city    = EXCLUDED.city,
		address = EXCLUDED.address,
		region  = EXCLUDED.region,
		email   = EXCLUDED.email`

	upsertPaymentQuery = `
	INSERT INTO payments (
		order_uid, transaction, request_id, currency, provider, amount,
		payment_dt, bank, delivery_cost, goods_total, custom_fee
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (order_uid) DO UPDATE SET
		transaction   = EXCLUDED.transaction,
		request_id    = EXCLUDED.request_id,
		currency      = EXCLUDED.currency,
		provider      = EXCLUDED.provider,
		amount        = EXCLUDED.amount,
		payment_dt    = EXCLUDED.payment_dt,
		bank          = EXCLUDED.bank,
		delivery_cost = EXCLUDED.delivery_cost,
		goods_total   = EXCLUDED.goods_total,
		custom_fee    = EXCLUDED.custom_fee`

	deleteItemsQuery = `DELETE FROM items WHERE order_uid = $1`

	insertItemQuery = `
	INSERT INTO items (` + itemColumns + `)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
)

// itemColumns — колонки позиции в одном порядке для чтения и для вставки:
// две копии списка разъедутся, а поймается это уже в рантайме.
const itemColumns = `order_uid, chrt_id, track_number, price, rid, name, sale, size,
	total_price, nm_id, brand, status`

// readOnlySnapshot — один снимок на оба запроса (шапка + позиции). В read committed
// между ними прошла бы запись consumer'а, и заказ склеился бы из двух версий.
var readOnlySnapshot = pgx.TxOptions{
	IsoLevel:   pgx.RepeatableRead,
	AccessMode: pgx.ReadOnly,
}

// OrderRepository — доступ к заказам в PostgreSQL.
type OrderRepository struct {
	pool *pgxpool.Pool
}

// NewOrderRepository создаёт репозиторий поверх готового пула.
func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

// Save идемпотентно сохраняет заказ в одной транзакции: повторная доставка
// сообщения не должна плодить дубли.
//
// items перезаписываются (DELETE + вставка), а не upsert'ятся: естественного ключа
// у позиции нет, и upsert по (order_uid, chrt_id) оставил бы удалённые позиции.
// Запросы уходят одной пачкой — отдельными Exec'ами это стоило бы четырёх
// round-trip'ов вместо одного; порядок выполнения пачка сохраняет.
func (r *OrderRepository) Save(ctx context.Context, order *domain.Order) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}

		queueExec(batch, "upsert order", upsertOrderQuery,
			order.OrderUID, order.TrackNumber, order.Entry, order.Locale, order.InternalSignature,
			order.CustomerID, order.DeliveryService, order.Shardkey, order.SmID,
			order.DateCreated, order.OofShard,
		)

		delivery := order.Delivery
		queueExec(batch, "upsert delivery", upsertDeliveryQuery,
			order.OrderUID, delivery.Name, delivery.Phone, delivery.Zip,
			delivery.City, delivery.Address, delivery.Region, delivery.Email,
		)

		payment := order.Payment
		queueExec(batch, "upsert payment", upsertPaymentQuery,
			order.OrderUID, payment.Transaction, payment.RequestID, payment.Currency,
			payment.Provider, payment.Amount, payment.PaymentDt, payment.Bank,
			payment.DeliveryCost, payment.GoodsTotal, payment.CustomFee,
		)

		queueExec(batch, "delete items", deleteItemsQuery, order.OrderUID)

		for _, item := range order.Items {
			queueExec(batch, "insert item", insertItemQuery,
				order.OrderUID, item.ChrtID, item.TrackNumber, item.Price, item.Rid,
				item.Name, item.Sale, item.Size, item.TotalPrice, item.NmID,
				item.Brand, item.Status,
			)
		}

		// Close возвращает первую ошибку из пачки.
		return tx.SendBatch(ctx, batch).Close()
	})
	if err != nil {
		return classifyError(fmt.Errorf("save order %s: %w", order.OrderUID, err))
	}

	return nil
}

// queueExec ставит запрос в пачку и подписывает его ошибку: иначе по Close
// не понять, какая из вставок упала.
func queueExec(batch *pgx.Batch, what, query string, args ...any) {
	batch.Queue(query, args...).Fn = func(br pgx.BatchResults) error {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}

		return nil
	}
}

// GetByUID возвращает заказ целиком. Если заказа нет — domain.ErrNotFound.
func (r *OrderRepository) GetByUID(ctx context.Context, uid string) (*domain.Order, error) {
	var order domain.Order

	err := pgx.BeginTxFunc(ctx, r.pool, readOnlySnapshot, func(tx pgx.Tx) error {
		if err := scanOrder(tx.QueryRow(ctx, getOrderQuery, uid), &order); err != nil {
			return err
		}

		items, err := loadItems(ctx, tx, []string{uid})
		if err != nil {
			return err
		}

		order.Items = items[uid]

		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("get order %s: %w", uid, domain.ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("get order %s: %w", uid, err)
	}

	return &order, nil
}

// ListRecent отдаёт limit самых свежих заказов — из них прогревается кэш.
func (r *OrderRepository) ListRecent(ctx context.Context, limit int) ([]domain.Order, error) {
	if limit <= 0 {
		return nil, nil
	}

	var orders []domain.Order

	err := pgx.BeginTxFunc(ctx, r.pool, readOnlySnapshot, func(tx pgx.Tx) error {
		var txErr error

		orders, txErr = selectRecent(ctx, tx, limit)

		return txErr
	})
	if err != nil {
		return nil, fmt.Errorf("list recent orders: %w", err)
	}

	return orders, nil
}

func selectRecent(ctx context.Context, tx pgx.Tx, limit int) ([]domain.Order, error) {
	rows, err := tx.Query(ctx, listRecentQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("select recent orders: %w", err)
	}
	defer rows.Close()

	orders := make([]domain.Order, 0, limit)

	for rows.Next() {
		var order domain.Order

		if scanErr := scanOrder(rows, &order); scanErr != nil {
			return nil, fmt.Errorf("scan recent order: %w", scanErr)
		}

		orders = append(orders, order)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("select recent orders: %w", err)
	}

	if len(orders) == 0 {
		return nil, nil
	}

	uids := make([]string, 0, len(orders))
	for i := range orders {
		uids = append(uids, orders[i].OrderUID)
	}

	items, err := loadItems(ctx, tx, uids)
	if err != nil {
		return nil, err
	}

	for i := range orders {
		orders[i].Items = items[orders[i].OrderUID]
	}

	return orders, nil
}

// loadItems достаёт позиции сразу для всех переданных заказов. Заказ без позиций
// получает пустой срез, а не nil: items в ответе всегда массив, и держать этот
// контракт должно одно место.
func loadItems(ctx context.Context, tx pgx.Tx, uids []string) (map[string][]domain.Item, error) {
	rows, err := tx.Query(ctx, selectItemsQuery, uids)
	if err != nil {
		return nil, fmt.Errorf("select items: %w", err)
	}
	defer rows.Close()

	items := make(map[string][]domain.Item, len(uids))
	for _, uid := range uids {
		items[uid] = []domain.Item{}
	}

	for rows.Next() {
		var (
			uid  string
			item domain.Item
		)

		if err := rows.Scan(
			&uid, &item.ChrtID, &item.TrackNumber, &item.Price, &item.Rid, &item.Name,
			&item.Sale, &item.Size, &item.TotalPrice, &item.NmID, &item.Brand, &item.Status,
		); err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}

		items[uid] = append(items[uid], item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select items: %w", err)
	}

	return items, nil
}

// scanOrder читает строку в заказ и приводит время к UTC: бинарный декодер pgx
// собирает timestamptz в локальной зоне процесса (параметр сессии timezone на это
// не влияет), и без нормализации JSON заказа зависел бы от таймзоны машины.
func scanOrder(row pgx.Row, order *domain.Order) error {
	if err := row.Scan(orderScanTargets(order)...); err != nil {
		return err
	}

	order.DateCreated = order.DateCreated.UTC()

	return nil
}

// orderScanTargets задаёт порядок Scan один раз для всех запросов,
// читающих selectOrderColumns.
func orderScanTargets(order *domain.Order) []any {
	return []any{
		&order.OrderUID, &order.TrackNumber, &order.Entry, &order.Locale,
		&order.InternalSignature, &order.CustomerID, &order.DeliveryService,
		&order.Shardkey, &order.SmID, &order.DateCreated, &order.OofShard,

		&order.Delivery.Name, &order.Delivery.Phone, &order.Delivery.Zip,
		&order.Delivery.City, &order.Delivery.Address, &order.Delivery.Region,
		&order.Delivery.Email,

		&order.Payment.Transaction, &order.Payment.RequestID, &order.Payment.Currency,
		&order.Payment.Provider, &order.Payment.Amount, &order.Payment.PaymentDt,
		&order.Payment.Bank, &order.Payment.DeliveryCost, &order.Payment.GoodsTotal,
		&order.Payment.CustomFee,
	}
}

// classifyError помечает permanent'ом ошибки, которые не лечатся повтором:
// данные нарушили constraint или не влезли в тип.
//
// Класс 42 (ошибка в SQL, нет прав) сюда осознанно не входит: это баг в коде,
// и лучше застрять с громкими ошибками в логе, чем тихо потерять заказы.
func classifyError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || len(pgErr.Code) < sqlStateClassLen {
		return err
	}

	switch pgErr.Code[:sqlStateClassLen] {
	case "22", "23": // data exception, integrity constraint violation
		return domain.Permanent(err)
	default:
		return err
	}
}
