package marketdata

import (
	"sync"
)

const subscriberBufferSize = 64

type subscriber struct {
	id      uint64
	filters []Filter
	ch      chan Event
}

// Bus provides an in-process pub/sub channel with bounded per-subscriber
// buffers and non-blocking drop-oldest backpressure.
type Bus struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]*subscriber
	closed bool
}

// NewBus constructs a new empty event bus.
func NewBus() *Bus {
	return &Bus{
		subs: make(map[uint64]*subscriber),
	}
}

// Subscribe registers a listener matching filter and returns an event channel and cancel function.
func (b *Bus) Subscribe(filter Filter) (<-chan Event, func()) {
	return b.SubscribeMulti([]Filter{filter})
}

// SubscribeMulti registers a listener matching any of filters (or all events if filters is empty).
func (b *Bus) SubscribeMulti(filters []Filter) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		closedCh := make(chan Event)
		close(closedCh)
		return closedCh, func() {}
	}
	b.nextID++
	id := b.nextID
	copied := make([]Filter, len(filters))
	copy(copied, filters)
	sub := &subscriber{
		id:      id,
		filters: copied,
		ch:      make(chan Event, subscriberBufferSize),
	}
	b.subs[id] = sub
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(existing.ch)
		}
	}
	return sub.ch, cancel
}

func matchesFilters(filters []Filter, evt Event) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if matchesFilter(f, evt) {
			return true
		}
	}
	return false
}

func matchesFilter(f Filter, evt Event) bool {
	if f.Venue != "" && evt.Venue != "" && f.Venue != evt.Venue {
		return false
	}
	if f.Symbol != "" && evt.Symbol != "" && f.Symbol != evt.Symbol {
		return false
	}
	if f.Interval != "" && evt.Interval != "" && f.Interval != evt.Interval {
		return false
	}
	if f.Style != "" && evt.Style != "" && f.Style != evt.Style {
		return false
	}
	if f.Type != "" && f.Type != evt.Type {
		return false
	}
	return true
}

// Publish dispatches an event to all matching subscribers without blocking.
// Slow subscribers have their oldest queued event dropped before enqueuing the newest.
func (b *Bus) Publish(evt Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, sub := range b.subs {
		if !matchesFilters(sub.filters, evt) {
			continue
		}
		select {
		case sub.ch <- evt:
		default:
			select {
			case <-sub.ch:
			default:
			}
			select {
			case sub.ch <- evt:
			default:
			}
		}
	}
}

// SubscriberCount reports active connected listeners.
func (b *Bus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Close closes all active subscriber channels deterministically.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, sub := range b.subs {
		close(sub.ch)
		delete(b.subs, id)
	}
}
