package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Config — параметры сервера. Таймауты обязательны: без них http.Server
// держит соединение сколько угодно и сдаётся первому же slowloris.
type Config struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// Server — HTTP-сервер с graceful shutdown по отмене context.
type Server struct {
	srv             *http.Server
	log             *slog.Logger
	shutdownTimeout time.Duration
}

// New собирает сервер. Слушать порт начинает только Run.
func New(cfg Config, handler http.Handler, log *slog.Logger) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,

			// Без моста ошибки net/http уедут в stderr текстом мимо структурного лога.
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		log:             log,
		shutdownTimeout: cfg.ShutdownTimeout,
	}
}

// Run слушает порт, пока не отменят context, затем гасит сервер аккуратно:
// запросы «в полёте» дожидаются, но не дольше ShutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	listenErr := make(chan error, 1)

	go func() {
		s.log.Info("http server started", slog.String("addr", s.srv.Addr))

		listenErr <- s.srv.ListenAndServe()
	}()

	select {
	case err := <-listenErr:
		// Сервер упал сам: занятый порт, нет прав.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return fmt.Errorf("listen %s: %w", s.srv.Addr, err)

	case <-ctx.Done():
	}

	s.log.Info("http server shutting down",
		slog.Duration("timeout", s.shutdownTimeout))

	// WithoutCancel обязателен: ctx уже отменён, производный от него закрыл бы
	// Shutdown мгновенно, оборвав запросы «в полёте».
	shutdownCtx := context.WithoutCancel(ctx)

	// Неположительный таймаут дал бы истёкший дедлайн, то есть тот же обрыв.
	// Трактуем его как «без ограничения»: верхний предел ставит run() в main.
	if s.shutdownTimeout > 0 {
		var cancel context.CancelFunc

		shutdownCtx, cancel = context.WithTimeout(shutdownCtx, s.shutdownTimeout)
		defer cancel()
	}

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	// Дожидаемся ListenAndServe, чтобы «http server stopped» в логе означало,
	// что порт действительно отпущен.
	<-listenErr

	s.log.Info("http server stopped")

	return nil
}
