// Package http — транспорт: JSON API заказа, проверки живости и веб-страница.
package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Alexandr001/wb_golang_school/demo_project/internal/domain"
)

const (
	// readinessTimeout — сколько ждём ответа БД в /readyz: проверку дёргает
	// оркестратор, и отвечать она должна быстро.
	readinessTimeout = 2 * time.Second

	cacheHit  = "HIT"
	cacheMiss = "MISS"
)

// OrderGetter — то, что транспорту нужно от сервиса. Второе значение —
// признак попадания в кэш, он уезжает в X-Cache.
type OrderGetter interface {
	Get(ctx context.Context, uid string) (*domain.Order, bool, error)
}

// DBPinger — проверка доступности БД для /readyz.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// CacheSizer отдаёт число заказов в кэше — для /readyz.
type CacheSizer interface {
	Len() int
}

// Handler — обработчики запросов; роутинг и middleware живут в router.go.
type Handler struct {
	orders OrderGetter
	db     DBPinger
	cache  CacheSizer
	log    *slog.Logger
}

// NewHandler собирает обработчики из зависимостей.
func NewHandler(orders OrderGetter, db DBPinger, cache CacheSizer, log *slog.Logger) *Handler {
	return &Handler{orders: orders, db: db, cache: cache, log: log}
}

// GetOrder отдаёт заказ по order_uid: 200 с JSON, 404 если заказа нет,
// 400 на заведомо непригодный идентификатор.
func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	log := h.logger(r)
	uid := r.PathValue("order_uid")

	// Предел длины держит домен: своё число здесь молча разошлось бы с моделью.
	if uid == "" || len(uid) > domain.MaxOrderUIDLen {
		writeError(w, log, http.StatusBadRequest,
			fmt.Sprintf("order_uid must be 1..%d characters", domain.MaxOrderUIDLen))

		return
	}

	order, fromCache, err := h.orders.Get(r.Context(), uid)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			writeError(w, log, http.StatusNotFound, "order not found")

		case errors.Is(err, context.Canceled):
			// Клиент закрыл соединение: отвечать некому, а отменённый запрос —
			// не поломка сервиса.
			log.Debug("request canceled by client", slog.String("order_uid", uid))

		default:
			log.Error("get order",
				slog.String("order_uid", uid),
				slog.Any("error", err),
			)
			writeError(w, log, http.StatusInternalServerError, "internal error")
		}

		return
	}

	// Заголовок — до тела: после WriteHeader он уже не уедет.
	w.Header().Set("X-Cache", cacheStatus(fromCache))
	writeJSON(w, log, http.StatusOK, order)
}

// Healthz — проверка живости. Всегда 200 и никаких походов в зависимости:
// упавшая БД не повод перезапускать исправный процесс.
func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	// Логгер с request_id не собираем: probe'ы ходят чаще всех, а сами не логируют.
	writeJSON(w, h.log, http.StatusOK, healthResponse{Status: "ok"})
}

// Readyz — проверка готовности; проверяем только то, что реально отваливается
// в рантайме, — соединение с БД.
//
// Прогрев кэша не проверяем намеренно: он идёт до старта сервера, а пустой кэш
// на пустой базе законен — на 503 сервис никогда бы не поднялся с нуля.
func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	log := h.logger(r)

	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		log.Warn("readiness check failed", slog.Any("error", err))
		writeError(w, log, http.StatusServiceUnavailable, "database unavailable")

		return
	}

	writeJSON(w, log, http.StatusOK, readyResponse{
		Status:    "ready",
		CacheSize: h.cache.Len(),
	})
}

// logger добавляет request_id, чтобы связать запись обработчика с access-логом.
func (h *Handler) logger(r *http.Request) *slog.Logger {
	return h.log.With(slog.String("request_id", requestIDFrom(r.Context())))
}

func cacheStatus(fromCache bool) string {
	if fromCache {
		return cacheHit
	}

	return cacheMiss
}
