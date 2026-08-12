// Package domain содержит модели заказа и доменные ошибки.
// Ничего не знает про Kafka, HTTP и SQL.
package domain

import "time"

// MaxOrderUIDLen — предельная длина order_uid; тег validate ниже обязан её
// повторять (константу в тег не подставить).
const MaxOrderUIDLen = 64

// Order — заказ целиком, по модели данных из ТЗ. Имена и порядок json-полей
// совпадают с исходным сообщением: round-trip Kafka → БД → HTTP не меняет заказ.
//
// Два правила при правке (подробности — в CLAUDE.md):
//   - validate-теги не слабее CHECK'ов в БД, иначе битые данные примут за
//     transient-ошибку и consumer уйдёт в вечный ретрай;
//   - типы повторяют схему: где INTEGER — там int32, чтобы переполнение ловил
//     json.Unmarshal, а не кодировщик параметров pgx (он отдаёт ошибку без SQLSTATE).
type Order struct {
	OrderUID          string    `json:"order_uid"          validate:"required,max=64"`
	TrackNumber       string    `json:"track_number"       validate:"required,max=64"`
	Entry             string    `json:"entry"              validate:"required,max=32"`
	Delivery          Delivery  `json:"delivery"`
	Payment           Payment   `json:"payment"`
	Items             []Item    `json:"items"              validate:"required,min=1,dive"`
	Locale            string    `json:"locale"             validate:"required,max=8"`
	InternalSignature string    `json:"internal_signature" validate:"max=256"`
	CustomerID        string    `json:"customer_id"        validate:"required,max=64"`
	DeliveryService   string    `json:"delivery_service"   validate:"required,max=64"`
	Shardkey          string    `json:"shardkey"           validate:"max=16"`
	SmID              int32     `json:"sm_id"              validate:"gte=0"`
	DateCreated       time.Time `json:"date_created"       validate:"required"`
	OofShard          string    `json:"oof_shard"          validate:"max=16"`
}

// Delivery — данные получателя, 1:1 к заказу. Телефон проверяем только на длину:
// строгий e164 отсёк бы легитимные номера, а потерять заказ хуже.
type Delivery struct {
	Name    string `json:"name"    validate:"required,max=128"`
	Phone   string `json:"phone"   validate:"required,max=32"`
	Zip     string `json:"zip"     validate:"required,max=16"`
	City    string `json:"city"    validate:"required,max=128"`
	Address string `json:"address" validate:"required,max=256"`
	Region  string `json:"region"  validate:"max=128"`
	Email   string `json:"email"   validate:"required,email,max=128"`
}

// Payment — оплата, 1:1 к заказу. Суммы целые, как BIGINT в БД; PaymentDt —
// unix-секунды числом, а не time.Time, иначе round-trip JSON изменил бы поле.
type Payment struct {
	Transaction  string `json:"transaction"   validate:"required,max=64"`
	RequestID    string `json:"request_id"    validate:"max=64"`
	Currency     string `json:"currency"      validate:"required,iso4217"`
	Provider     string `json:"provider"      validate:"required,max=64"`
	Amount       int64  `json:"amount"        validate:"gte=0"`
	PaymentDt    int64  `json:"payment_dt"    validate:"gt=0"`
	Bank         string `json:"bank"          validate:"required,max=64"`
	DeliveryCost int64  `json:"delivery_cost" validate:"gte=0"`
	GoodsTotal   int64  `json:"goods_total"   validate:"gte=0"`
	CustomFee    int64  `json:"custom_fee"    validate:"gte=0"`
}

// Item — товарная позиция заказа, N к заказу.
type Item struct {
	ChrtID      int64  `json:"chrt_id"      validate:"gt=0"`
	TrackNumber string `json:"track_number" validate:"required,max=64"`
	Price       int64  `json:"price"        validate:"gte=0"`
	Rid         string `json:"rid"          validate:"required,max=64"`
	Name        string `json:"name"         validate:"required,max=256"`
	Sale        int32  `json:"sale"         validate:"gte=0,lte=100"`
	Size        string `json:"size"         validate:"max=32"`
	TotalPrice  int64  `json:"total_price"  validate:"gte=0"`
	NmID        int64  `json:"nm_id"        validate:"gt=0"`
	Brand       string `json:"brand"        validate:"max=128"`
	Status      int32  `json:"status"       validate:"gte=0"`
}
