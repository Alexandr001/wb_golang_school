package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Миграции вшиты в бинарь: в distroless-образе внешний каталог с .sql взять негде.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate накатывает непримененные миграции. Вызывается на старте до всего
// остального: без схемы прогревать кэш не из чего.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer func() {
		// Закрываем только обёртку: пул принадлежит вызвавшему.
		if closeErr := db.Close(); closeErr != nil {
			log.Warn("close migration db handle", slog.Any("error", closeErr))
		}
	}()

	// Если инстансов сервиса несколько, миграции накатит только один.
	sessionLocker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration locker: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrationsDir,
		goose.WithSessionLocker(sessionLocker),
		goose.WithSlog(log),
	)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	for _, result := range results {
		log.Info("migration applied",
			slog.Int64("version", result.Source.Version),
			slog.String("source", result.Source.Path),
			slog.Duration("duration", result.Duration),
		)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}

	log.Info("database schema is up to date",
		slog.Int64("version", version),
		slog.Int("applied", len(results)),
	)

	return nil
}
