package queue

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"
)

const (
	defaultBackoffBase = 5 * time.Second
	defaultBackoffMax  = time.Hour
	maxBackoff         = 30 * 24 * time.Hour
)

// Backoff is the retry delay policy: the ceiling doubles with every attempt
// from Base up to Max, and the delay is drawn uniformly from the upper half
// of the ceiling ("equal jitter"). Jitter spreads the retries of many jobs
// that failed together, and keeping half the ceiling guarantees a real pause,
// which full jitter does not.
type Backoff struct {
	Base time.Duration // default 5s
	Max  time.Duration // default 1h
}

func (b Backoff) withDefaults() Backoff {
	if b.Base <= 0 {
		b.Base = defaultBackoffBase
	}
	if b.Max <= 0 {
		b.Max = max(defaultBackoffMax, b.Base)
	}
	return b
}

func (b Backoff) validate() error {
	if b.Base < 0 || b.Max < 0 || b.Base > maxBackoff || b.Max > maxBackoff {
		return fmt.Errorf("queue: backoff durations must be between 0 and %s", maxBackoff)
	}
	if b.Base > 0 && b.Max > 0 && b.Max < b.Base {
		return errors.New("queue: backoff maximum must be at least its base")
	}
	return nil
}

// Ceiling is the upper bound of the delay after the given failed attempt
// (1-based).
func (b Backoff) Ceiling(attempt int) time.Duration {
	b = b.withDefaults()
	ceiling := b.Base
	for i := 1; i < attempt && ceiling < b.Max; i++ {
		if ceiling > b.Max/2 {
			return b.Max
		}
		ceiling *= 2
	}
	return min(ceiling, b.Max)
}

// Delay returns the jittered delay before the attempt that follows the given
// failed attempt (1-based).
func (b Backoff) Delay(attempt int) time.Duration {
	ceiling := b.Ceiling(attempt)
	half := ceiling / 2
	return half + jitter(ceiling-half)
}

// jitter returns a uniform duration in [0, n]. The cryptographic generator
// avoids correlated retries between processes seeded alike; if it fails the
// deterministic midpoint is still a valid delay.
func jitter(n time.Duration) time.Duration {
	if n <= 0 {
		return 0
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(n)+1))
	if err != nil {
		return n / 2
	}
	return time.Duration(value.Int64())
}

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as not worth retrying: the job moves straight to the
// dead state. Use it for invalid input and other failures a retry cannot fix.
// Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

type retryAfterError struct {
	err   error
	delay time.Duration
}

func (e *retryAfterError) Error() string { return e.err.Error() }
func (e *retryAfterError) Unwrap() error { return e.err }

// RetryAfter makes the next attempt wait d instead of the backoff, for
// example to honor a downstream Retry-After header. A job that used its last
// attempt still dies. RetryAfter(d, nil) is nil.
func RetryAfter(d time.Duration, err error) error {
	if err == nil {
		return nil
	}
	return &retryAfterError{err: err, delay: max(d, 0)}
}
