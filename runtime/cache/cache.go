// Package cache provides a thread-safe, content-addressed LRU cache for
// operation results. Keys are derived from the operation's identity and the
// fingerprints of its resolved inputs, so a hit is only ever returned for an
// identical computation. Only operations declared deterministic, cacheable, and
// side-effect-free are eligible — enforced by the executor, not here.
package cache

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"neuroforge/backends"
	"neuroforge/core/ir"
)

// Cache is a bounded LRU of operation output sets.
type Cache struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List               // front = most recently used
	items    map[string]*list.Element // key -> element
	hits     int64
	misses   int64
	evicts   int64
}

type entry struct {
	key     string
	outputs map[string]backends.Value
}

// New returns an LRU cache holding at most capacity entries (<=0 means a
// default of 256).
func New(capacity int) *Cache {
	if capacity <= 0 {
		capacity = 256
	}
	return &Cache{
		capacity: capacity,
		ll:       list.New(),
		items:    map[string]*list.Element{},
	}
}

// Key computes the content-addressed cache key for an operation and its
// resolved inputs. It incorporates operator version-relevant identity (type,
// backend, params) and input value fingerprints.
func Key(op *ir.Operation, inputs map[string]backends.Value) string {
	h := sha256.New()
	fmt.Fprintf(h, "op=%s\x00type=%s\x00backend=%s\x00", op.ID, op.Type, op.Backend)
	if pb, err := json.Marshal(op.Params); err == nil {
		h.Write(pb)
	}
	// deterministic input ordering
	for _, b := range op.Bindings {
		v := inputs[b.Port]
		h.Write([]byte("\x00" + b.Port + "="))
		if vb, err := json.Marshal(v); err == nil {
			h.Write(vb)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns cached outputs for a key, recording a hit or miss.
func (c *Cache) Get(key string) (map[string]backends.Value, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		c.hits++
		return el.Value.(*entry).outputs, true
	}
	c.misses++
	return nil, false
}

// Put stores outputs under a key, evicting the least-recently-used entry if the
// cache is over capacity.
func (c *Cache) Put(key string, outputs map[string]backends.Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*entry).outputs = outputs
		return
	}
	el := c.ll.PushFront(&entry{key: key, outputs: outputs})
	c.items[key] = el
	for c.ll.Len() > c.capacity {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.ll.Remove(back)
		delete(c.items, back.Value.(*entry).key)
		c.evicts++
	}
}

// Stats is a snapshot of cache counters.
type Stats struct {
	Hits     int64   `json:"hits"`
	Misses   int64   `json:"misses"`
	Evicts   int64   `json:"evictions"`
	Size     int     `json:"size"`
	Capacity int     `json:"capacity"`
	HitRate  float64 `json:"hit_rate"`
}

// Stats returns a snapshot of cache performance.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := c.hits + c.misses
	rate := 0.0
	if total > 0 {
		rate = float64(c.hits) / float64(total)
	}
	return Stats{
		Hits:     c.hits,
		Misses:   c.misses,
		Evicts:   c.evicts,
		Size:     c.ll.Len(),
		Capacity: c.capacity,
		HitRate:  rate,
	}
}
