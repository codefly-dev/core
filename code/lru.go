package code

import (
	"container/list"
	"sync"
)

// ByteLRU is a byte-slice LRU cache with a configurable memory budget.
// Thread-safe. Used by CachedVFS to cache file contents in RAM.
type ByteLRU struct {
	mu       sync.Mutex
	capacity int64 // max bytes
	size     int64 // current bytes used
	items    map[string]*list.Element
	order    *list.List // front = most recently used

	// MaxEntrySize is the maximum size of a single entry (default 1MB).
	// Files larger than this are not cached.
	MaxEntrySize int64
}

type lruEntry struct {
	key  string
	data []byte
}

// NewByteLRU creates a new LRU cache with the given capacity in bytes.
func NewByteLRU(capacity int64) *ByteLRU {
	return &ByteLRU{
		capacity:     capacity,
		items:        make(map[string]*list.Element),
		order:        list.New(),
		MaxEntrySize: 1 << 20, // 1MB default
	}
}

// Get returns the cached data for key, or nil if not present.
// Moves the entry to the front (most recently used).
func (c *ByteLRU) Get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*lruEntry).data
}

// Put stores data under key. If the entry exceeds MaxEntrySize, it is not cached.
// Evicts least recently used entries to stay within capacity.
func (c *ByteLRU) Put(key string, data []byte) {
	entrySize := int64(len(data))
	if entrySize > c.MaxEntrySize {
		return
	}
	// Skip binary files (null byte in first 512 bytes).
	if isBinary(data) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry.
	if elem, ok := c.items[key]; ok {
		old := elem.Value.(*lruEntry)
		c.size -= int64(len(old.data))
		old.data = data
		c.size += entrySize
		c.order.MoveToFront(elem)
		c.evict()
		return
	}

	// New entry.
	entry := &lruEntry{key: key, data: data}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
	c.size += entrySize
	c.evict()
}

// Invalidate removes a key from the cache.
func (c *ByteLRU) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return
	}
	c.removeElement(elem)
}

// Clear removes all entries.
func (c *ByteLRU) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.order.Init()
	c.size = 0
}

// Size returns the current memory usage in bytes.
func (c *ByteLRU) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// Len returns the number of cached entries.
func (c *ByteLRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// evict removes LRU entries until size <= capacity. Must be called with mu held.
func (c *ByteLRU) evict() {
	for c.size > c.capacity && c.order.Len() > 0 {
		tail := c.order.Back()
		if tail == nil {
			break
		}
		c.removeElement(tail)
	}
}

// removeElement removes a list element. Must be called with mu held.
func (c *ByteLRU) removeElement(elem *list.Element) {
	entry := c.order.Remove(elem).(*lruEntry)
	delete(c.items, entry.key)
	c.size -= int64(len(entry.data))
}

// isBinary checks if data looks like a binary file (null byte in first 512 bytes).
func isBinary(data []byte) bool {
	limit := 512
	if len(data) < limit {
		limit = len(data)
	}
	for i := range limit {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
