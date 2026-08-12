// Package cache — in-memory кэш заказов с вытеснением по LRU.
package cache

import (
	"fmt"
	"sync"

	"github.com/Alexandr001/wb_golang_school/demo_project/internal/domain"
)

// Stats — снимок состояния кэша.
type Stats struct {
	Hits   uint64
	Misses uint64
	Len    int
}

// LRU — потокобезопасный кэш заказов с вытеснением давно не запрашиваемых.
//
// Заказ в кэше неизменяем: Get отдаёт указатель внутрь кэша без копии, и мутация
// испортит данные всем читателям. Мьютекс обычный, а не RWMutex, потому что
// чтение переставляет запись в начало списка, то есть меняет структуру.
type LRU struct {
	mu       sync.Mutex
	capacity int
	nodes    map[string]*node
	head     *node // самый свежий по обращению
	tail     *node // кандидат на вытеснение
	hits     uint64
	misses   uint64
}

// node — элемент двусвязного списка обращений. Свой список вместо container/list:
// там значение — any, и каждое чтение стоило бы приведения типа.
type node struct {
	uid   string
	order *domain.Order
	prev  *node
	next  *node
}

// New создаёт кэш на capacity заказов.
func New(capacity int) (*LRU, error) {
	if capacity < 1 {
		return nil, fmt.Errorf("cache capacity must be >= 1, got %d", capacity)
	}

	return &LRU{
		capacity: capacity,
		nodes:    make(map[string]*node, capacity),
	}, nil
}

// Get возвращает заказ и признак попадания в кэш.
func (c *LRU) Get(uid string) (*domain.Order, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current, ok := c.nodes[uid]
	if !ok {
		c.misses++

		return nil, false
	}

	c.moveToFront(current)
	c.hits++

	return current.order, true
}

// Set кладёт заказ в кэш; ключ берётся из самого заказа. Повторный Set того же
// uid обновляет заказ и освежает его позицию.
func (c *LRU) Set(order *domain.Order) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if current, ok := c.nodes[order.OrderUID]; ok {
		current.order = order
		c.moveToFront(current)

		return
	}

	c.insert(order)
}

// SetIfAbsent кладёт заказ, только если его ещё нет, и сообщает, положил ли.
// Для пути чтения: пока шли в БД, consumer мог записать версию свежее нашей.
func (c *LRU) SetIfAbsent(order *domain.Order) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.nodes[order.OrderUID]; ok {
		return false
	}

	c.insert(order)

	return true
}

// insert добавляет заведомо новый заказ. Вызывается под уже взятым c.mu.
func (c *LRU) insert(order *domain.Order) {
	fresh := &node{uid: order.OrderUID, order: order}
	c.nodes[fresh.uid] = fresh
	c.pushFront(fresh)

	if len(c.nodes) > c.capacity {
		c.evict()
	}
}

// Len — сколько заказов сейчас в кэше.
func (c *LRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.nodes)
}

// Stats отдаёт счётчики попаданий и промахов.
func (c *LRU) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return Stats{Hits: c.hits, Misses: c.misses, Len: len(c.nodes)}
}

// evict выбрасывает самый давний по обращению заказ.
func (c *LRU) evict() {
	victim := c.tail
	if victim == nil {
		return
	}

	c.unlink(victim)
	delete(c.nodes, victim.uid)
}

// Дальше — операции со списком. Все вызываются под уже взятым c.mu.

func (c *LRU) pushFront(target *node) {
	target.prev = nil
	target.next = c.head

	if c.head != nil {
		c.head.prev = target
	}

	c.head = target

	if c.tail == nil {
		c.tail = target
	}
}

func (c *LRU) unlink(target *node) {
	if target.prev != nil {
		target.prev.next = target.next
	} else {
		c.head = target.next
	}

	if target.next != nil {
		target.next.prev = target.prev
	} else {
		c.tail = target.prev
	}

	target.prev = nil
	target.next = nil
}

func (c *LRU) moveToFront(target *node) {
	if c.head == target {
		return
	}

	c.unlink(target)
	c.pushFront(target)
}
