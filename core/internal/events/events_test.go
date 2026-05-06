package events

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestBusDispatchesAll(t *testing.T) {
	b := NewBus()
	var got atomic.Int32
	b.Subscribe(func(_ context.Context, _ Event) { got.Add(1) })

	b.Publish(context.Background(), Event{Kind: KindWANStatus})
	b.Publish(context.Background(), Event{Kind: KindDeviceJoined})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got.Load() == 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("got %d events, want 2", got.Load())
}

func TestBusFiltersByKind(t *testing.T) {
	b := NewBus()
	var wan, dev atomic.Int32
	b.Subscribe(func(_ context.Context, _ Event) { wan.Add(1) }, KindWANStatus)
	b.Subscribe(func(_ context.Context, _ Event) { dev.Add(1) }, KindDeviceJoined)

	b.Publish(context.Background(), Event{Kind: KindWANStatus})
	b.Publish(context.Background(), Event{Kind: KindWANStatus})
	b.Publish(context.Background(), Event{Kind: KindDeviceJoined})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if wan.Load() == 2 && dev.Load() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("wan=%d dev=%d, want 2 + 1", wan.Load(), dev.Load())
}

func TestBusPanicInHandlerDoesNotCrash(t *testing.T) {
	b := NewBus()
	var bad, good atomic.Int32
	b.Subscribe(func(_ context.Context, _ Event) {
		bad.Add(1)
		panic("boom")
	})
	b.Subscribe(func(_ context.Context, _ Event) { good.Add(1) })
	b.Publish(context.Background(), Event{Kind: KindWANStatus})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if bad.Load() == 1 && good.Load() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("expected both handlers to fire (bad=%d good=%d)", bad.Load(), good.Load())
}

func TestBusUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBus()
	var n atomic.Int32
	id := b.Subscribe(func(_ context.Context, _ Event) { n.Add(1) })
	b.Publish(context.Background(), Event{Kind: KindWANStatus})
	time.Sleep(50 * time.Millisecond)
	b.Unsubscribe(id)
	b.Publish(context.Background(), Event{Kind: KindWANStatus})
	time.Sleep(50 * time.Millisecond)
	if n.Load() != 1 {
		t.Errorf("got %d, want 1 (post-unsubscribe should not deliver)", n.Load())
	}
}

func TestPublishStampsWhen(t *testing.T) {
	b := NewBus()
	got := make(chan Event, 1)
	b.Subscribe(func(_ context.Context, ev Event) { got <- ev })
	before := time.Now()
	b.Publish(context.Background(), Event{Kind: KindWANStatus})
	select {
	case ev := <-got:
		if ev.When.Before(before) {
			t.Errorf("When=%v, want >= %v", ev.When, before)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
}
