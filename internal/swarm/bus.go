package swarm

import "sync"

// Bus provides bounded non-blocking in-process pub/sub for verified Swarm events.
type Bus struct {
	mu     sync.RWMutex
	nextID uint64
	subs   map[uint64]chan Event
	closed bool
}

// NewBus creates an empty Swarm event bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[uint64]chan Event)}
}

// Subscribe registers a 64-slot bounded listener and returns its channel + unsubscribe func.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	b.nextID++
	id := b.nextID
	ch := make(chan Event, DefaultBusBufferLen)
	b.subs[id] = ch
	unsub := func() {
		b.mu.Lock()
		if sub, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(sub)
		}
		b.mu.Unlock()
	}
	return ch, unsub
}

// Publish broadcasts an event without blocking on slow subscribers.
func (b *Bus) Publish(evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default:
		}
	}
}

// Close closes all active subscriber channels.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}
