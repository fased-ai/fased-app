package marketdata

import (
	"sync"
	"testing"
	"time"
)

func TestBusFilteringBackpressureAndRaceSafety(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	btcCh, cancelBTC := bus.Subscribe(Filter{Venue: VenueHyperliquid, Symbol: "BTC"})
	allCh, cancelAll := bus.Subscribe(Filter{})

	// Publish matching and non-matching events.
	bus.Publish(Event{Type: "candle", Venue: VenueBinance, Symbol: "ETHUSDT", Timestamp: 1})
	bus.Publish(Event{Type: "candle", Venue: VenueHyperliquid, Symbol: "BTC", Timestamp: 2})

	select {
	case evt := <-btcCh:
		if evt.Symbol != "BTC" {
			t.Fatalf("unexpected filtered event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for filtered BTC event")
	}
	select {
	case <-btcCh:
		t.Fatal("non-matching ETHUSDT event leaked to BTC filter")
	default:
	}
	for i := 0; i < 2; i++ {
		select {
		case <-allCh:
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for unfiltered event %d", i)
		}
	}

	// Verify backpressure drop-oldest (>64 bursts never block publisher).
	for i := 0; i < 120; i++ {
		bus.Publish(Event{Type: "candle", Venue: VenueHyperliquid, Symbol: "BTC", Timestamp: int64(100 + i)})
	}
	if len(btcCh) != subscriberBufferSize {
		t.Fatalf("expected buffer len %d, got %d", subscriberBufferSize, len(btcCh))
	}
	// Oldest queued items must have been dropped; last drained must be Timestamp=219.
	var last Event
	for i := 0; i < subscriberBufferSize; i++ {
		last = <-btcCh
	}
	if last.Timestamp != 219 {
		t.Fatalf("expected latest event timestamp 219 after drop-oldest backpressure, got %d", last.Timestamp)
	}

	cancelBTC()
	cancelAll()

	// Concurrent publishers & short-lived subscribers under -race.
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ch, unsub := bus.Subscribe(Filter{})
			go func() {
				for range ch {
				}
			}()
			for i := 0; i < 30; i++ {
				bus.Publish(Event{Type: "trade", Venue: VenueHyperliquid, Symbol: "SOL", Timestamp: int64(id*100 + i)})
			}
			unsub()
		}(g)
	}
	wg.Wait()
	bus.Close()
}

func TestBusIntervalStyleAndSubscribeMulti(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	multiCh, cancelMulti := bus.SubscribeMulti([]Filter{
		{Venue: VenueHyperliquid, Symbol: "BTC", Interval: "1h", Style: StyleStandard},
		{Venue: VenueKraken, Symbol: "ETHUSD", Interval: "15m", Style: StyleHeikinAshi},
	})
	defer cancelMulti()

	// Wrong interval & wrong style must be dropped.
	bus.Publish(Event{Type: "candle", Venue: VenueHyperliquid, Symbol: "BTC", Interval: "15m", Style: StyleRenko, Timestamp: 1})
	select {
	case leak := <-multiCh:
		t.Fatalf("mismatched interval/style event leaked to multi filter: %+v", leak)
	default:
	}

	// Matching second tuple arrives cleanly.
	bus.Publish(Event{Type: "candle", Venue: VenueKraken, Symbol: "ETHUSD", Interval: "15m", Style: StyleHeikinAshi, Timestamp: 2})
	select {
	case got := <-multiCh:
		if got.Venue != VenueKraken || got.Symbol != "ETHUSD" || got.Interval != "15m" || got.Style != StyleHeikinAshi {
			t.Fatalf("unexpected multi event: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SubscribeMulti event")
	}
}
