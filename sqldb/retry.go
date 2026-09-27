package sqldb

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	retryBaseDelay = 10 * time.Millisecond
	retryMaxDelay  = 500 * time.Millisecond
)

// Retry runs fn up to attempts times while it fails with an error retryable
// reports true for, backing off exponentially between runs. Adapters use it
// to rerun whole transactions after serialization failures and deadlocks.
func Retry(ctx context.Context, attempts int, retryable func(error) bool, fn func() error) error {
	attempts = max(attempts, 1)
	delay := retryBaseDelay
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil || attempt >= attempts || !retryable(err) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		delay = min(delay*2, retryMaxDelay)
	}
}

// CloseWithin runs closeFn but stops waiting when ctx ends, reporting how
// many connections were still in use. Pool closes wait for borrowed
// connections, and a stuck request must not block process exit.
func CloseWithin(ctx context.Context, closeFn func() error, inUse func() int) error {
	result := make(chan error, 1)
	go func() { result <- closeFn() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("close database pool: %w (%d connections still in use)", ctx.Err(), inUse())
	}
}
