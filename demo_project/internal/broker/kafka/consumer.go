// Package kafka — чтение заказов из Kafka с ручным коммитом оффсета.
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/Alexandr001/wb_golang_school/demo_project/internal/domain"
)

const (
	// maxRetryBackoff — потолок паузы между попытками записи.
	maxRetryBackoff = 30 * time.Second

	// payloadLogLimit — сколько байт «ядовитого» сообщения писать в лог.
	payloadLogLimit = 512
)

// OrderSaver — то, что consumer'у нужно от сервиса.
type OrderSaver interface {
	Save(ctx context.Context, order *domain.Order) error
}

// Config — параметры чтения из Kafka.
type Config struct {
	Brokers      []string
	Topic        string
	GroupID      string
	MinBytes     int
	MaxBytes     int
	MaxWait      time.Duration
	MaxAttempts  int
	RetryBackoff time.Duration
}

// Consumer читает заказы из топика и отдаёт их сервису.
type Consumer struct {
	reader       *kafka.Reader
	saver        OrderSaver
	validator    *domain.Validator
	log          *slog.Logger
	maxAttempts  int
	retryBackoff time.Duration
}

// New собирает consumer'а. К брокеру reader идёт только с первого FetchMessage.
func New(cfg Config, saver OrderSaver, validator *domain.Validator, log *slog.Logger) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		Topic:    cfg.Topic,
		GroupID:  cfg.GroupID,
		MinBytes: cfg.MinBytes,
		MaxBytes: cfg.MaxBytes,
		MaxWait:  cfg.MaxWait,

		// С LastOffset (по умолчанию) заказы, отправленные до первого старта
		// сервиса, никогда бы не прочитались.
		StartOffset: kafka.FirstOffset,

		// Коммит только явным CommitMessages: с автокоммитом оффсет мог бы
		// уехать вперёд записи в БД.
		CommitInterval: 0,

		ErrorLogger: kafka.LoggerFunc(func(msg string, args ...any) {
			log.Warn("kafka reader: "+fmt.Sprintf(msg, args...), slog.String("topic", cfg.Topic))
		}),
	})

	return &Consumer{
		reader:    reader,
		saver:     saver,
		validator: validator,
		log:       log,

		// При нуле цикл ретраев не выполнился бы ни разу: save вернул бы nil,
		// не позвав репозиторий, а оффсет уехал бы вперёд — заказ потерян.
		// Конфиг такое значение отсекает, но цена ошибки слишком велика.
		maxAttempts:  max(cfg.MaxAttempts, 1),
		retryBackoff: max(cfg.RetryBackoff, 0),
	}
}

// Run читает сообщения, пока не отменят context.
//
// Порядок строгий: FetchMessage → обработка → CommitMessages. Оффсет коммитим
// только после успешного COMMIT в БД, отсюда at-least-once: падение между
// записью и коммитом даст повторную доставку, а запись идемпотентна.
func (c *Consumer) Run(ctx context.Context) error {
	defer func() {
		if err := c.reader.Close(); err != nil {
			c.log.Warn("close kafka reader", slog.Any("error", err))
		}
	}()

	c.log.Info("kafka consumer started",
		slog.String("topic", c.reader.Config().Topic),
		slog.String("group_id", c.reader.Config().GroupID),
	)

	for {
		message, err := c.reader.FetchMessage(ctx)
		if err != nil {
			// Отмена контекста и закрытый reader — штатное завершение, не ошибка.
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				c.log.Info("kafka consumer stopped")

				return nil
			}

			return fmt.Errorf("fetch message: %w", err)
		}

		if err := c.handle(ctx, message); err != nil {
			// Transient-ошибка после всех попыток: оффсет не коммитим, сообщение
			// перечитается. Продолжать нельзя — kafka-go уже сдвинул чтение, и
			// коммит следующего сообщения неявно закоммитил бы это.
			return fmt.Errorf("handle message at offset %d: %w", message.Offset, err)
		}

		if err := c.reader.CommitMessages(ctx, message); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			return fmt.Errorf("commit offset %d: %w", message.Offset, err)
		}
	}
}

// handle обрабатывает одно сообщение и возвращает ошибку только тогда, когда
// оффсет коммитить нельзя. Битые данные логируем и пропускаем, иначе consumer
// встанет в вечном цикле на «ядовитом» сообщении.
func (c *Consumer) handle(ctx context.Context, message kafka.Message) error {
	log := c.log.With(
		slog.String("topic", message.Topic),
		slog.Int("partition", message.Partition),
		slog.Int64("offset", message.Offset),
		slog.String("key", string(message.Key)),
	)

	order, err := c.decode(message.Value)
	if err != nil {
		log.Warn("invalid message skipped",
			slog.Any("error", err),
			slog.String("payload", truncate(message.Value)),
		)

		return nil
	}

	start := time.Now()

	err = c.save(ctx, log, order)

	switch {
	case err == nil:
		log.Info("order saved",
			slog.String("order_uid", order.OrderUID),
			slog.Int("items", len(order.Items)),
			slog.Duration("took", time.Since(start)),
		)

		return nil

	case domain.IsPermanent(err):
		log.Warn("permanent error, message skipped",
			slog.String("order_uid", order.OrderUID),
			slog.Any("error", err),
			slog.String("payload", truncate(message.Value)),
		)

		return nil

	default:
		return err
	}
}

// decode разбирает и валидирует заказ. Любая ошибка здесь permanent:
// повторное чтение тех же байт даст тот же результат.
func (c *Consumer) decode(value []byte) (*domain.Order, error) {
	var order domain.Order

	if err := json.Unmarshal(value, &order); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %w", domain.ErrInvalidMessage, err)
	}

	if err := c.validator.ValidateOrder(&order); err != nil {
		return nil, err
	}

	return &order, nil
}

// save пытается сохранить заказ, повторяя transient-ошибки с backoff.
func (c *Consumer) save(ctx context.Context, log *slog.Logger, order *domain.Order) error {
	backoff := c.retryBackoff

	var err error

	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		err = c.saver.Save(ctx, order)

		switch {
		case err == nil:
			return nil
		case domain.IsPermanent(err):
			return err
		case ctx.Err() != nil:
			// Останавливаемся — оффсет не закоммитится, сообщение перечитается.
			return err
		case attempt == c.maxAttempts:
			return fmt.Errorf("save after %d attempts: %w", c.maxAttempts, err)
		}

		log.Warn("save failed, retrying",
			slog.String("order_uid", order.OrderUID),
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", c.maxAttempts),
			slog.Duration("backoff", backoff),
			slog.Any("error", err),
		)

		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, maxRetryBackoff)
	}

	return err
}

// truncate обрезает payload, чтобы в лог не уехало мегабайтное сообщение.
func truncate(value []byte) string {
	if len(value) <= payloadLogLimit {
		return string(value)
	}

	return string(value[:payloadLogLimit]) + "...(truncated)"
}
