// Package lru is a fixed-size LRU map. It is not safe for concurrent use:
// callers hold their own lock around Get, Add and Remove.
package lru

import "container/list"

type entry[K comparable, V any] struct {
	key K
	val V
}

type Cache[K comparable, V any] struct {
	max   int
	ll    *list.List
	items map[K]*list.Element
}

func New[K comparable, V any](max int) *Cache[K, V] {
	if max < 1 {
		max = 1
	}
	return &Cache[K, V]{max: max, ll: list.New(), items: make(map[K]*list.Element, max)}
}

// Get returns the value and moves it to the front.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*entry[K, V]).val, true
	}
	var zero V
	return zero, false
}

// Add inserts or replaces a key and evicts the least recently used entry past max.
func (c *Cache[K, V]) Add(key K, val V) {
	if el, ok := c.items[key]; ok {
		el.Value.(*entry[K, V]).val = val
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&entry[K, V]{key, val})
	for c.ll.Len() > c.max {
		el := c.ll.Back()
		if el == nil {
			return
		}
		e := el.Value.(*entry[K, V])
		c.ll.Remove(el)
		delete(c.items, e.key)
	}
}

func (c *Cache[K, V]) Remove(key K) {
	if el, ok := c.items[key]; ok {
		c.ll.Remove(el)
		delete(c.items, key)
	}
}

func (c *Cache[K, V]) Len() int { return c.ll.Len() }
