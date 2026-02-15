package heartbeat

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHeartbeat_FiresOnInterval(t *testing.T) {
	var mu sync.Mutex
	var sent []string

	send := func(ctx context.Context, text string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, text)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	hb := New(Config{
		Interval:  100 * time.Millisecond,
		SessionID: "telegram:123",
		SendFn:    send,
		Prompt:    "Check in with the user.",
	})

	go hb.Run(ctx)
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond) // Let goroutine finish.

	mu.Lock()
	defer mu.Unlock()
	if len(sent) < 2 {
		t.Errorf("expected at least 2 heartbeats in 350ms at 100ms interval, got %d", len(sent))
	}
}

func TestHeartbeat_StopsOnCancel(t *testing.T) {
	var mu sync.Mutex
	count := 0

	send := func(ctx context.Context, text string) error {
		mu.Lock()
		defer mu.Unlock()
		count++
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	hb := New(Config{
		Interval:  50 * time.Millisecond,
		SessionID: "telegram:123",
		SendFn:    send,
		Prompt:    "heartbeat",
	})

	go hb.Run(ctx)
	time.Sleep(130 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	final := count
	mu.Unlock()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	after := count
	mu.Unlock()

	if after != final {
		t.Errorf("heartbeat continued after cancel: %d → %d", final, after)
	}
}
