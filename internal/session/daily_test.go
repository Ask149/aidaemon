package session_test

import (
	"context"
	"testing"
	"time"
)

func TestStartDailyRotation_StopsCleanly(t *testing.T) {
	mgr, _ := newTestManager(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())

	stop := mgr.StartDailyRotation(ctx)

	// Let it tick a couple times.
	time.Sleep(50 * time.Millisecond)

	// Both stop mechanisms should work.
	stop()
	cancel()
}
