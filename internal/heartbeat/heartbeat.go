// Package heartbeat manages periodic check-in messages to channels.
//
// A heartbeat Runner fires a SendFunc callback at a fixed interval,
// delivering a prompt (e.g., "Check in with the user.") to a specific
// session. The runner blocks until its context is cancelled.
//
// Currently uses a simple SendFunc callback; this will evolve to use
// the Channel interface from internal/channel once routing is wired up.
package heartbeat

import (
	"context"
	"log"
	"time"
)

// SendFunc delivers a heartbeat message to a channel.
// This is a simple callback; it will evolve to use the Channel interface later.
type SendFunc func(ctx context.Context, text string) error

// Config configures a heartbeat runner.
type Config struct {
	Interval  time.Duration // How often to fire (e.g., 30m).
	SessionID string        // Session to send heartbeats to.
	SendFn    SendFunc      // Callback to deliver messages.
	Prompt    string        // What to send (the heartbeat prompt/check-in text).
}

// Runner manages periodic heartbeat delivery.
type Runner struct {
	cfg Config
}

// New creates a heartbeat runner.
func New(cfg Config) *Runner {
	return &Runner{cfg: cfg}
}

// Run starts the heartbeat loop. Blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	if r.cfg.Interval <= 0 {
		log.Printf("[heartbeat] disabled (interval=0)")
		return
	}

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	log.Printf("[heartbeat] started for %s (interval=%s)", r.cfg.SessionID, r.cfg.Interval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[heartbeat] stopped for %s", r.cfg.SessionID)
			return
		case <-ticker.C:
			if err := r.cfg.SendFn(ctx, r.cfg.Prompt); err != nil {
				if ctx.Err() != nil {
					return // Context cancelled, don't log.
				}
				log.Printf("[heartbeat] send error for %s: %v", r.cfg.SessionID, err)
			}
		}
	}
}
