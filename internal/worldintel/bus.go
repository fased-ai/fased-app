package worldintel

import "sync"

const subscriberBufferSize = 64

type subscriber struct {
	id     uint64
	filter StreamFilter
	ch     chan StreamEvent
}

// Bus provides an in-process pub/sub channel with bounded per-subscriber buffers
// and non-blocking drop-oldest backpressure.
type Bus struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]*subscriber
	closed bool
}

// NewBus constructs a new empty WorldIntel event bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[uint64]*subscriber)}
}

// Subscribe registers a filtered listener and returns the receive channel plus unsubscribe callback.
func (b *Bus) Subscribe(filter StreamFilter) (<-chan StreamEvent, func()) {
	filter.Category = NormalizeCategory(filter.Category)
	filter.Region = NormalizeRegion(filter.Region)
	filter.Ticker = NormalizeTicker(filter.Ticker)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		closedCh := make(chan StreamEvent)
		close(closedCh)
		return closedCh, func() {}
	}
	b.nextID++
	id := b.nextID
	sub := &subscriber{
		id:     id,
		filter: filter,
		ch:     make(chan StreamEvent, subscriberBufferSize),
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

func matchesStreamFilter(f StreamFilter, evt StreamEvent) bool {
	if f.Category == "" && f.Region == "" && f.Ticker == "" {
		return true
	}
	if evt.Event != nil {
		if f.Category != "" && evt.Event.Category != f.Category {
			return false
		}
		if f.Region != "" && evt.Event.Region != f.Region {
			return false
		}
		if f.Ticker != "" && !containsString(evt.Event.Tickers, f.Ticker) {
			return false
		}
		return true
	}
	if evt.Prediction != nil {
		if f.Category != "" && evt.Prediction.Category != f.Category {
			return false
		}
		if f.Region != "" && evt.Prediction.Region != f.Region {
			return false
		}
		if f.Ticker != "" && !containsString(evt.Prediction.Tickers, f.Ticker) {
			return false
		}
		return true
	}
	return true
}

func containsString(items []string, needle string) bool {
	for _, it := range items {
		if it == needle {
			return true
		}
	}
	return false
}

// Publish dispatches an event to matching subscribers without blocking.
func (b *Bus) Publish(evt StreamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, sub := range b.subs {
		if !matchesStreamFilter(sub.filter, evt) {
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
