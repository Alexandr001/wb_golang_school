// Package config загружает конфигурацию сервиса из переменных окружения.
package config

import (
	"cmp"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/Alexandr001/wb_golang_school/demo_project/internal/logger"
)

// Config — полная конфигурация сервиса.
type Config struct {
	App      App
	HTTP     HTTP
	Postgres Postgres
	Kafka    Kafka
	Cache    Cache
}

// App — общие параметры приложения.
type App struct {
	Env             string        `env:"APP_ENV" envDefault:"local"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat       string        `env:"LOG_FORMAT" envDefault:"json"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// HTTP — параметры сервера.
type HTTP struct {
	Addr              string        `env:"HTTP_ADDR" envDefault:":8081"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"10s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"15s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
	HandlerTimeout    time.Duration `env:"HTTP_HANDLER_TIMEOUT" envDefault:"5s"`
}

// Postgres — параметры подключения к БД.
type Postgres struct {
	Host            string        `env:"POSTGRES_HOST" envDefault:"localhost"`
	Port            int           `env:"POSTGRES_PORT" envDefault:"5432"`
	User            string        `env:"POSTGRES_USER,required,notEmpty"`
	Password        string        `env:"POSTGRES_PASSWORD,required,notEmpty"`
	Database        string        `env:"POSTGRES_DB,required,notEmpty"`
	SSLMode         string        `env:"POSTGRES_SSLMODE" envDefault:"disable"`
	MaxConns        int32         `env:"POSTGRES_MAX_CONNS" envDefault:"10"`
	MinConns        int32         `env:"POSTGRES_MIN_CONNS" envDefault:"2"`
	ConnectTimeout  time.Duration `env:"POSTGRES_CONNECT_TIMEOUT" envDefault:"5s"`
	ConnectAttempts int           `env:"POSTGRES_CONNECT_ATTEMPTS" envDefault:"10"`
	ConnectBackoff  time.Duration `env:"POSTGRES_CONNECT_BACKOFF" envDefault:"1s"`
}

// Kafka — параметры consumer'а.
type Kafka struct {
	Brokers      []string      `env:"KAFKA_BROKERS" envSeparator:"," envDefault:"localhost:9092"`
	Topic        string        `env:"KAFKA_TOPIC" envDefault:"orders"`
	GroupID      string        `env:"KAFKA_GROUP_ID" envDefault:"orders-service"`
	MinBytes     int           `env:"KAFKA_MIN_BYTES" envDefault:"1"`
	MaxBytes     int           `env:"KAFKA_MAX_BYTES" envDefault:"10485760"`
	MaxWait      time.Duration `env:"KAFKA_MAX_WAIT" envDefault:"500ms"`
	MaxAttempts  int           `env:"KAFKA_MAX_ATTEMPTS" envDefault:"5"`
	RetryBackoff time.Duration `env:"KAFKA_RETRY_BACKOFF" envDefault:"500ms"`
}

// Cache — параметры in-memory кэша заказов.
type Cache struct {
	Capacity    int `env:"CACHE_CAPACITY" envDefault:"10000"`
	WarmupLimit int `env:"CACHE_WARMUP_LIMIT" envDefault:"0"` // 0 → берём Capacity
}

// Load читает конфигурацию из окружения и проверяет её на осмысленность.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}

	cfg.normalize()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// normalize приводит значения к каноничному виду до валидации.
func (c *Config) normalize() {
	// KAFKA_BROKERS="" даёт []string{""}, а не пустой срез: без чистки сервис
	// стартует со списком брокеров из одного пустого адреса.
	for i, broker := range c.Kafka.Brokers {
		c.Kafka.Brokers[i] = strings.TrimSpace(broker)
	}

	c.Kafka.Brokers = slices.DeleteFunc(c.Kafka.Brokers, func(broker string) bool {
		return broker == ""
	})

	c.Kafka.Topic = strings.TrimSpace(c.Kafka.Topic)
	c.Kafka.GroupID = strings.TrimSpace(c.Kafka.GroupID)

	// 0 → берём Capacity.
	c.Cache.WarmupLimit = cmp.Or(c.Cache.WarmupLimit, c.Cache.Capacity)
}

func (c *Config) validate() error {
	// Список уровней не дублируем: его знает logger, а тот — slog.
	if _, err := logger.ParseLevel(c.App.LogLevel); err != nil {
		return fmt.Errorf("LOG_LEVEL is invalid: %w", err)
	}

	validFormats := []string{"json", "text"}
	if !slices.Contains(validFormats, strings.ToLower(c.App.LogFormat)) {
		return fmt.Errorf("LOG_FORMAT must be one of %v, got %q", validFormats, c.App.LogFormat)
	}

	// Нулевой таймаут — уже истёкший дедлайн у Shutdown: запросы «в полёте»
	// оборвались бы ровно там, где сервис обязан их дождаться.
	if c.App.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be > 0, got %s", c.App.ShutdownTimeout)
	}

	if err := c.validateHTTP(); err != nil {
		return err
	}

	if c.Postgres.Port <= 0 || c.Postgres.Port > 65535 {
		return fmt.Errorf("POSTGRES_PORT must be in 1..65535, got %d", c.Postgres.Port)
	}

	if c.Postgres.MaxConns < 1 {
		return fmt.Errorf("POSTGRES_MAX_CONNS must be >= 1, got %d", c.Postgres.MaxConns)
	}

	if c.Postgres.MinConns < 0 || c.Postgres.MinConns > c.Postgres.MaxConns {
		return fmt.Errorf("POSTGRES_MIN_CONNS must be in 0..%d, got %d", c.Postgres.MaxConns, c.Postgres.MinConns)
	}

	// Иначе ноль попыток подключения и бессмысленное
	// "connect postgres after 0 attempts" в логе.
	if c.Postgres.ConnectAttempts < 1 {
		return fmt.Errorf("POSTGRES_CONNECT_ATTEMPTS must be >= 1, got %d", c.Postgres.ConnectAttempts)
	}

	if c.Postgres.ConnectTimeout <= 0 {
		return fmt.Errorf("POSTGRES_CONNECT_TIMEOUT must be > 0, got %s", c.Postgres.ConnectTimeout)
	}

	if c.Postgres.ConnectBackoff < 0 {
		return fmt.Errorf("POSTGRES_CONNECT_BACKOFF must be >= 0, got %s", c.Postgres.ConnectBackoff)
	}

	if len(c.Kafka.Brokers) == 0 {
		return fmt.Errorf("KAFKA_BROKERS must not be empty")
	}

	if c.Kafka.Topic == "" {
		return fmt.Errorf("KAFKA_TOPIC must not be empty")
	}

	if c.Kafka.GroupID == "" {
		return fmt.Errorf("KAFKA_GROUP_ID must not be empty")
	}

	if c.Kafka.MaxAttempts < 1 {
		return fmt.Errorf("KAFKA_MAX_ATTEMPTS must be >= 1, got %d", c.Kafka.MaxAttempts)
	}

	if c.Cache.Capacity < 1 {
		return fmt.Errorf("CACHE_CAPACITY must be >= 1, got %d", c.Cache.Capacity)
	}

	if c.Cache.WarmupLimit < 0 {
		return fmt.Errorf("CACHE_WARMUP_LIMIT must be >= 0, got %d", c.Cache.WarmupLimit)
	}

	return nil
}

// validateHTTP проверяет параметры сервера: http.Server не считает ошибкой ни
// пустой адрес (слушает порт 80), ни неположительный таймаут (отключает защиту).
// Обе опечатки дешевле поймать на старте.
func (c *Config) validateHTTP() error {
	if strings.TrimSpace(c.HTTP.Addr) == "" {
		return fmt.Errorf("HTTP_ADDR must not be empty")
	}

	// Именно этот таймаут закрывает slowloris: без него можно бесконечно слать
	// заголовки по байту.
	if c.HTTP.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must be > 0, got %s", c.HTTP.ReadHeaderTimeout)
	}

	timeouts := []struct {
		name  string
		value time.Duration
	}{
		{"HTTP_READ_TIMEOUT", c.HTTP.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", c.HTTP.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", c.HTTP.IdleTimeout},
		{"HTTP_HANDLER_TIMEOUT", c.HTTP.HandlerTimeout},
	}

	// Ноль — осознанное «без ограничения», отрицательное значение всегда опечатка.
	for _, timeout := range timeouts {
		if timeout.value < 0 {
			return fmt.Errorf("%s must be >= 0, got %s", timeout.name, timeout.value)
		}
	}

	return nil
}

// DSN собирает строку подключения. Пользователь и пароль экранируются:
// в них могут быть спецсимволы.
func (p Postgres) DSN() string {
	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(p.User, p.Password),
		Host:     fmt.Sprintf("%s:%d", p.Host, p.Port),
		Path:     p.Database,
		RawQuery: url.Values{"sslmode": {p.SSLMode}}.Encode(),
	}

	return dsn.String()
}

// LogValue реализует slog.LogValuer, чтобы пароль не утёк в логи.
func (p Postgres) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", p.Host),
		slog.Int("port", p.Port),
		slog.String("user", p.User),
		slog.String("database", p.Database),
		slog.String("password", "[REDACTED]"),
		slog.String("sslmode", p.SSLMode),
		slog.Int("max_conns", int(p.MaxConns)),
	)
}

// LogValue отдаёт конфигурацию в лог целиком, но без секретов.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.App.Env),
		slog.String("log_level", c.App.LogLevel),
		slog.String("http_addr", c.HTTP.Addr),
		slog.Any("postgres", c.Postgres),
		slog.Any("kafka_brokers", c.Kafka.Brokers),
		slog.String("kafka_topic", c.Kafka.Topic),
		slog.String("kafka_group_id", c.Kafka.GroupID),
		slog.Int("cache_capacity", c.Cache.Capacity),
	)
}
