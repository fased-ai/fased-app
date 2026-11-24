package automation

import "sync"

const subscriberBufferSize = 64

type subscriber struct {
	id uint64
	ch chan StreamEvent
}

// Bus provides an in-process pub/sub channel with bounded 64-slot buffers
// and non-blocking drop-oldest backpressure.
type Bus struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]*subscriber
	closed bool
}

// NewBus constructs a new empty Automation event bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[uint64]*subscriber)}
}

// Subscribe registers a listener and returns the receive channel and cancel callback.
func (b *Bus) Subscribe() (<-chan StreamEvent, func()) {
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
		id: id,
		ch: make(chan StreamEvent, subscriberBufferSize),
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

// Publish dispatches an event to all subscribers without blocking.
func (b *Bus) Publish(evt StreamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, sub := range b.subs {
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

// SubscriberCount reports currently active listeners.
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
