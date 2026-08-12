// Package service — оркестрация работы с заказами: связывает хранилище и кэш.
// Зависит только от интерфейсов, объявленных здесь же.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/Alexandr001/wb_golang_school/demo_project/internal/domain"
)

// OrderRepository — то, что сервису нужно от хранилища. Интерфейс объявлен
// на стороне потребителя: реализация про него не знает.
type OrderRepository interface {
	Save(ctx context.Context, order *domain.Order) error
	GetByUID(ctx context.Context, uid string) (*domain.Order, error)
	ListRecent(ctx context.Context, limit int) ([]domain.Order, error)
}

// Cache — то, что сервису нужно от кэша. Ключ живёт внутри заказа, поэтому Set
// принимает заказ целиком. Set — для пути записи (наша версия заведомо свежайшая),
// SetIfAbsent — для пути чтения, где затирать лежащее в кэше нельзя.
type Cache interface {
	Get(uid string) (*domain.Order, bool)
	Set(order *domain.Order)
	SetIfAbsent(order *domain.Order) bool
	Len() int
}

// OrderService — единственное место, где решается, идти ли в БД или отдать из кэша.
type OrderService struct {
	repo  OrderRepository
	cache Cache
	log   *slog.Logger
}

// New собирает сервис из готовых зависимостей.
func New(repo OrderRepository, cache Cache, log *slog.Logger) *OrderService {
	return &OrderService{repo: repo, cache: cache, log: log}
}

// Save записывает заказ в БД и только потом кладёт в кэш: иначе при откате
// транзакции отдали бы клиенту заказ, которого в базе нет. Ошибку репозитория
// возвращаем как есть — решать, коммитить ли оффсет, дело consumer'а.
func (s *OrderService) Save(ctx context.Context, order *domain.Order) error {
	if err := s.repo.Save(ctx, order); err != nil {
		return err
	}

	// Именно Set: заказ только что записан в БД, свежее версии нет.
	s.cache.Set(order)

	return nil
}

// Get отдаёт заказ и признак попадания в кэш (для X-Cache). Промах поднимает
// заказ из БД и кладёт обратно. Заказа нет нигде — domain.ErrNotFound.
func (s *OrderService) Get(ctx context.Context, uid string) (*domain.Order, bool, error) {
	if order, ok := s.cache.Get(uid); ok {
		return order, true, nil
	}

	order, err := s.repo.GetByUID(ctx, uid)
	if err != nil {
		return nil, false, err
	}

	// SetIfAbsent, а не Set: пока мы ходили в БД, consumer мог положить в кэш
	// более свежую версию, и Set затёр бы её нашей устаревшей копией.
	s.cache.SetIfAbsent(order)

	return order, false, nil
}

// Warmup заполняет кэш последними заказами. Вызывается на старте до HTTP-сервера
// и consumer'а: иначе сервис начнёт отвечать с холодным кэшем.
func (s *OrderService) Warmup(ctx context.Context, limit int) error {
	orders, err := s.repo.ListRecent(ctx, limit)
	if err != nil {
		return fmt.Errorf("warm up cache: %w", err)
	}

	// Кладём от старых к свежим: последний Set — самый «свежий» в LRU, поэтому
	// при limit больше вместимости вытеснятся старые заказы, а не актуальные.
	// range отдаёт копию, так что &recent не держит ссылку внутрь orders —
	// иначе кэш удерживал бы весь массив на limit заказов.
	for _, recent := range slices.Backward(orders) {
		s.cache.Set(&recent)
	}

	s.log.Info("cache warmed up",
		slog.Int("loaded", len(orders)),
		slog.Int("cached", s.cache.Len()),
		slog.Int("limit", limit),
	)

	return nil
}
