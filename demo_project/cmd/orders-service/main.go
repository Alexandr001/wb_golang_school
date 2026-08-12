// Команда orders-service: читает заказы из Kafka, складывает в PostgreSQL,
// кэширует в памяти и отдаёт по HTTP.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Alexandr001/wb_golang_school/demo_project/internal/broker/kafka"
	"github.com/Alexandr001/wb_golang_school/demo_project/internal/cache"
	"github.com/Alexandr001/wb_golang_school/demo_project/internal/config"
	"github.com/Alexandr001/wb_golang_school/demo_project/internal/domain"
	"github.com/Alexandr001/wb_golang_school/demo_project/internal/logger"
	"github.com/Alexandr001/wb_golang_school/demo_project/internal/repository/postgres"
	"github.com/Alexandr001/wb_golang_school/demo_project/internal/service"
	httptransport "github.com/Alexandr001/wb_golang_school/demo_project/internal/transport/http"
)

// shutdownGrace — запас поверх ShutdownTimeout: компоненты гасятся по тому же
// таймауту, и без него верхний select записал бы «shutdown timed out» вместо
// настоящей причины остановки.
const shutdownGrace = time.Second

// Значения подставляются линковщиком: -ldflags "-X main.version=..."
var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := run(); err != nil {
		// Логгер может быть ещё не настроен — пишем в stderr напрямую.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(os.Stdout, cfg.App.LogLevel, cfg.App.LogFormat)
	slog.SetDefault(log)

	log.Info("starting orders-service",
		slog.String("version", version),
		slog.String("commit", commit),
		slog.Any("config", cfg),
	)

	// Один context на всё приложение: по SIGINT/SIGTERM компоненты начинают
	// останавливаться.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{
		DSN:             cfg.Postgres.DSN(),
		MaxConns:        cfg.Postgres.MaxConns,
		MinConns:        cfg.Postgres.MinConns,
		ConnectTimeout:  cfg.Postgres.ConnectTimeout,
		ConnectAttempts: cfg.Postgres.ConnectAttempts,
		ConnectBackoff:  cfg.Postgres.ConnectBackoff,
	}, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Схема приезжает вместе с сервисом: отдельного шага деплоя нет.
	if err = postgres.Migrate(ctx, pool, log); err != nil {
		return err
	}

	orderCache, err := cache.New(cfg.Cache.Capacity)
	if err != nil {
		return err
	}

	orders := service.New(postgres.NewOrderRepository(pool), orderCache, log)

	// Строго до старта HTTP и consumer'а: иначе отвечаем с холодным кэшем.
	if err = orders.Warmup(ctx, cfg.Cache.WarmupLimit); err != nil {
		return err
	}

	// Конвертация, а не перечисление полей: расхождение типов станет ошибкой
	// компиляции, а не молча забытым полем со значением по умолчанию.
	consumer := kafka.New(kafka.Config(cfg.Kafka), orders, domain.NewValidator(), log)

	router := httptransport.NewRouter(
		httptransport.NewHandler(orders, pool, orderCache, log),
		cfg.HTTP.HandlerTimeout,
		log,
	)

	httpServer := httptransport.New(httptransport.Config{
		Addr:              cfg.HTTP.Addr,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ShutdownTimeout:   cfg.App.ShutdownTimeout,
	}, router, log)

	// errgroup связывает жизни компонентов: падение любого останавливает остальных.
	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return consumer.Run(groupCtx)
	})

	group.Go(func() error {
		return httpServer.Run(groupCtx)
	})

	// Ждём сигнала или падения компонента — groupCtx отменяется в обоих случаях.
	<-groupCtx.Done()

	// Различаем причины: ctx отменяется только сигналом. Писать «получен сигнал»
	// на падении значило бы врать в логе там, где разбирают инцидент.
	if ctx.Err() != nil {
		log.Info("shutdown signal received, stopping",
			slog.Duration("timeout", cfg.App.ShutdownTimeout))
	} else {
		log.Warn("component stopped on its own, shutting down service",
			slog.Duration("timeout", cfg.App.ShutdownTimeout))
	}

	stop() // повторный Ctrl+C теперь убьёт процесс

	// Ждём компоненты, но не дольше ShutdownTimeout: зависший компонент иначе
	// добьёт оркестратор SIGKILL'ом, не оставив в логе причины.
	waitErr := make(chan error, 1)
	go func() { waitErr <- group.Wait() }()

	select {
	case err = <-waitErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("component stopped with error", slog.Any("error", err))

			return err
		}

	case <-time.After(cfg.App.ShutdownTimeout + shutdownGrace):
		log.Error("shutdown timed out, exiting anyway",
			slog.Duration("timeout", cfg.App.ShutdownTimeout+shutdownGrace))

		return fmt.Errorf("shutdown timed out after %s", cfg.App.ShutdownTimeout+shutdownGrace)
	}

	stats := orderCache.Stats()
	log.Info("cache stats",
		slog.Uint64("hits", stats.Hits),
		slog.Uint64("misses", stats.Misses),
		slog.Int("size", stats.Len),
	)

	log.Info("orders-service stopped")

	return nil
}
