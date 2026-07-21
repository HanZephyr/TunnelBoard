package application

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	defaultCommandCacheTTL      = 15 * time.Minute
	defaultCommandCacheCapacity = 1024
)

// CommandCacheOptions 配置应用生命周期内的命令结果缓存。零值使用安全默认值。
type CommandCacheOptions struct {
	TTL      time.Duration
	Capacity int
}

type cachedCommand struct {
	commandID     string
	operation     string
	payloadDigest [sha256.Size]byte
	resultJSON    []byte
	expiresAt     time.Time
}

type recentCommandCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	entries  map[string]*list.Element
	order    *list.List
}

func newRecentCommandCache(options CommandCacheOptions) *recentCommandCache {
	ttl := options.TTL
	if ttl <= 0 {
		ttl = defaultCommandCacheTTL
	}
	capacity := options.Capacity
	if capacity <= 0 {
		capacity = defaultCommandCacheCapacity
	}
	return &recentCommandCache{ttl: ttl, capacity: capacity, entries: make(map[string]*list.Element, capacity), order: list.New()}
}

func commandPayloadDigest(operation string, payload any) ([sha256.Size]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("application: marshal %s command payload: %w", operation, err)
	}
	input := make([]byte, 0, len(operation)+1+len(raw))
	input = append(input, operation...)
	input = append(input, 0)
	input = append(input, raw...)
	return sha256.Sum256(input), nil
}

func (c *recentCommandCache) lookup(commandID, operation string, digest [sha256.Size]byte, now time.Time) ([]byte, bool, error) {
	if commandID == "" {
		return nil, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpired(now)
	element, ok := c.entries[commandID]
	if !ok {
		return nil, false, nil
	}
	entry := element.Value.(*cachedCommand)
	if entry.operation != operation || entry.payloadDigest != digest {
		return nil, false, &CommandIDConflictError{CommandID: commandID, ExistingOperation: entry.operation, RequestedOperation: operation}
	}
	c.order.MoveToBack(element)
	return append([]byte(nil), entry.resultJSON...), true, nil
}

func (c *recentCommandCache) store(commandID, operation string, digest [sha256.Size]byte, resultJSON []byte, now time.Time) {
	if commandID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpired(now)
	if existing, ok := c.entries[commandID]; ok {
		c.order.Remove(existing)
	}
	entry := &cachedCommand{commandID: commandID, operation: operation, payloadDigest: digest, resultJSON: append([]byte(nil), resultJSON...), expiresAt: now.Add(c.ttl)}
	c.entries[commandID] = c.order.PushBack(entry)
	for c.order.Len() > c.capacity {
		oldest := c.order.Front()
		delete(c.entries, oldest.Value.(*cachedCommand).commandID)
		c.order.Remove(oldest)
	}
}

func (c *recentCommandCache) pruneExpired(now time.Time) {
	for element := c.order.Front(); element != nil; {
		next := element.Next()
		entry := element.Value.(*cachedCommand)
		if !entry.expiresAt.After(now) {
			delete(c.entries, entry.commandID)
			c.order.Remove(element)
		}
		element = next
	}
}

func lookupCommandResult[T any](cache *recentCommandCache, commandID, operation string, payload any) (T, [sha256.Size]byte, bool, error) {
	var zero T
	digest, err := commandPayloadDigest(operation, payload)
	if err != nil {
		return zero, digest, false, err
	}
	raw, ok, err := cache.lookup(commandID, operation, digest, time.Now())
	if err != nil || !ok {
		return zero, digest, false, err
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return zero, digest, false, fmt.Errorf("application: decode cached %s result: %w", operation, err)
	}
	return result, digest, true, nil
}

func storeCommandResult[T any](cache *recentCommandCache, commandID, operation string, digest [sha256.Size]byte, result T) error {
	if commandID == "" {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("application: encode cached %s result: %w", operation, err)
	}
	cache.store(commandID, operation, digest, raw, time.Now())
	return nil
}
