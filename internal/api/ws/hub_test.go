package ws_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
)

func TestHub_PublishReceivesOnSubscriber(t *testing.T) {
	h := ws.NewHub()
	defer func() {
		if ch, ok := h.(interface{ Close() }); ok {
			ch.Close()
		}
	}()

	ch, unsub := h.Subscribe("u1", "tasks")
	defer unsub()

	go h.Publish(context.Background(), ws.Event{Topic: "tasks", Body: "hello"})

	select {
	case ev := <-ch:
		assert.Equal(t, "tasks", ev.Topic)
		assert.Equal(t, "hello", ev.Body)
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive event")
	}
}

func TestHub_PublishDoesNotReceiveOnOtherTopic(t *testing.T) {
	h := ws.NewHub()
	defer func() {
		if ch, ok := h.(interface{ Close() }); ok {
			ch.Close()
		}
	}()

	ch, unsub := h.Subscribe("u1", "tasks")
	defer unsub()

	go h.Publish(context.Background(), ws.Event{Topic: "other", Body: "x"})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %v", ev)
	case <-time.After(100 * time.Millisecond):
		// good
	}
}

func TestHub_PublishFiltersByUserID(t *testing.T) {
	h := ws.NewHub()
	defer func() {
		if ch, ok := h.(interface{ Close() }); ok {
			ch.Close()
		}
	}()

	ch1, unsub1 := h.Subscribe("u1", "tasks")
	defer unsub1()
	ch2, unsub2 := h.Subscribe("u2", "tasks")
	defer unsub2()

	go h.Publish(context.Background(), ws.Event{
		Topic: "tasks",
		Body:  map[string]any{"user_id": "u1", "msg": "for u1"},
	})

	select {
	case ev := <-ch1:
		assert.Equal(t, "u1", ev.Body.(map[string]any)["user_id"])
	case <-time.After(1 * time.Second):
		t.Fatal("u1 didn't receive")
	}
	select {
	case ev := <-ch2:
		t.Fatalf("u2 received u1's event: %v", ev)
	case <-time.After(100 * time.Millisecond):
		// good
	}
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	h := ws.NewHub()
	defer func() {
		if ch, ok := h.(interface{ Close() }); ok {
			ch.Close()
		}
	}()

	ch, unsub := h.Subscribe("u1", "tasks")
	unsub()

	// After unsubscribe the channel is closed; reading should yield zero value and ok=false.
	_, ok := <-ch
	assert.False(t, ok, "channel should be closed after Unsubscribe")
}

func TestHub_PublishDropsOnFullSubscriber(t *testing.T) {
	h := ws.NewHub()
	defer func() {
		if ch, ok := h.(interface{ Close() }); ok {
			ch.Close()
		}
	}()

	ch, unsub := h.Subscribe("u1", "tasks")
	defer unsub()

	// Fill the buffer (size 32) + one extra; the extra must be dropped.
	for i := 0; i < 64; i++ {
		h.Publish(context.Background(), ws.Event{Topic: "tasks", Body: i})
	}

	// Drain what we can; we don't care if some are dropped, only that the
	// channel doesn't deadlock.
	drained := 0
	timeout := time.After(200 * time.Millisecond)
loop:
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				break loop
			}
			drained++
		case <-timeout:
			break loop
		}
	}
	require.Greater(t, drained, 0)
	// We don't assert `drained <= 32` because a buffered channel does not cap
	// the total delivered — it only caps the in-flight queue. As long as the
	// filter goroutine keeps draining raw into out and the consumer keeps
	// reading, drained can approach 64 (all publishes). What we DO assert is
	// that some events were dropped (i.e. drained < 64). The buffer-full drop
	// itself is exercised by the Publish path (raw is non-blocking); here we
	// just guarantee no deadlock and that not everything is silently dropped.
	assert.Less(t, drained, 64, "expected drop-on-full to take effect under load")
}

func TestHub_MultipleSubscribersFanout(t *testing.T) {
	h := ws.NewHub()
	defer func() {
		if ch, ok := h.(interface{ Close() }); ok {
			ch.Close()
		}
	}()

	const n = 5
	var wg sync.WaitGroup
	wg.Add(n)
	chans := make([]<-chan ws.Event, n)
	for i := 0; i < n; i++ {
		var unsub ws.Unsubscribe
		chans[i], unsub = h.Subscribe("u", "tasks")
		defer unsub()
		go func(idx int) {
			defer wg.Done()
			ev := <-chans[idx]
			assert.Equal(t, "tasks", ev.Topic)
		}(i)
	}

	h.Publish(context.Background(), ws.Event{Topic: "tasks", Body: "x"})
	wg.Wait()
}

func TestNopHub(t *testing.T) {
	var h ws.Hub = ws.NopHub{}
	h.Publish(context.Background(), ws.Event{Topic: "x", Body: 1}) // must not panic
	ch, unsub := h.Subscribe("u", "x")
	_, ok := <-ch
	assert.False(t, ok, "NopHub channel should be closed")
	unsub()
}
