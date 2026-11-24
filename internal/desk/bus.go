package desk

import "sync"

const subscriberBufferSize = 64

type subscriber struct {
	id uint64
	ch chan DeskEvent
}

// Bus dispatches live desk events with bounded 64-slot buffers and non-blocking drop-oldest semantics.
type Bus struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]*subscriber
	closed bool
}

// NewBus constructs a ready-to-use desk event bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[uint64]*subscriber)}
}

// Subscribe registers a listener and returns its event channel plus unsubscribe cleanup.
func (b *Bus) Subscribe() (<-chan DeskEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		closedCh := make(chan DeskEvent)
		close(closedCh)
		return closedCh, func() {}
	}
	b.nextID++
	id := b.nextID
	sub := &subscriber{
		id: id,
		ch: make(chan DeskEvent, subscriberBufferSize),
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

// Publish broadcasts an event without blocking callers.
func (b *Bus) Publish(evt DeskEvent) {
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
