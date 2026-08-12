// Package postgres — хранилище заказов: пул, миграции и репозиторий.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// maxConnectBackoff — потолок паузы: без него при 10 попытках последняя
// ждала бы больше минуты.
const maxConnectBackoff = 10 * time.Second

// PoolConfig — параметры пула. Отдельный тип, а не config.Postgres: репозиторий
// не должен зависеть от того, откуда взялись настройки.
type PoolConfig struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	ConnectTimeout  time.Duration
	ConnectAttempts int
	ConnectBackoff  time.Duration
}

// NewPool поднимает пул и дожидается готовности БД. Ретраи нужны из-за compose:
// сервис стартует раньше, чем PostgreSQL начинает принимать соединения.
func NewPool(ctx context.Context, cfg PoolConfig, log *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.MaxConnIdleTime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute

	// timezone=UTC — только для текстового вывода (psql, ручные запросы). На то,
	// что pgx возвращает в Go, он не влияет: за это отвечает scanOrder.
	poolCfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "orders-service"

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	if err := waitReady(ctx, pool, cfg, log); err != nil {
		pool.Close()

		return nil, err
	}

	return pool, nil
}

// waitReady пингует БД с экспоненциальным backoff, пока не ответит.
func waitReady(ctx context.Context, pool *pgxpool.Pool, cfg PoolConfig, log *slog.Logger) error {
	backoff := cfg.ConnectBackoff

	var lastErr error

	for attempt := 1; attempt <= cfg.ConnectAttempts; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
		err := pool.Ping(pingCtx)
		cancel()

		if err == nil {
			log.Info("postgres is ready", slog.Int("attempt", attempt))

			return nil
		}

		lastErr = err

		if attempt == cfg.ConnectAttempts {
			break
		}

		log.Warn("postgres is not ready, retrying",
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", cfg.ConnectAttempts),
			slog.Duration("backoff", backoff),
			slog.Any("error", err),
		)

		select {
		case <-ctx.Done():
			return fmt.Errorf("connect postgres: %w", ctx.Err())
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, maxConnectBackoff)
	}

	return fmt.Errorf("connect postgres after %d attempts: %w", cfg.ConnectAttempts, lastErr)
}
