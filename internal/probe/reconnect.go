package probe

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

const (
	reconnectBackoffMin  = time.Second
	reconnectBackoffMax  = 15 * time.Minute
	reconnectStableAfter = 10 * time.Minute
)

// watch keeps a pool endpoint represented through ordinary transient network
// failures. Backoff is bounded so a recovered endpoint rejoins promptly.
func watch(ctx context.Context, poolID string, endpoint model.Endpoint, out chan<- event) error {
	backoff := reconnectBackoffMin
	for {
		var establishedAt time.Time
		err := watchSessionWithReady(ctx, poolID, endpoint, out, func() { establishedAt = time.Now() })
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !shouldRetry(err) {
			return fmt.Errorf("not retrying after permanent rejection: %w", err)
		}
		stable := !establishedAt.IsZero() && time.Since(establishedAt) >= reconnectStableAfter
		delay, nextBackoff := advanceReconnectBackoff(backoff, stable)
		log.Printf("probe pool=%s endpoint=%s disconnected category=%s retry_in=%s", poolID, net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)), connectionErrorCategory(err), delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = nextBackoff
	}
}

func advanceReconnectBackoff(current time.Duration, established bool) (time.Duration, time.Duration) {
	if established || current < reconnectBackoffMin {
		current = reconnectBackoffMin
	}
	if current > reconnectBackoffMax {
		current = reconnectBackoffMax
	}
	next := current * 2
	if next > reconnectBackoffMax {
		next = reconnectBackoffMax
	}
	return current, next
}

func shouldRetry(err error) bool {
	return !errors.Is(err, errPoolRejected)
}

func connectionErrorCategory(err error) string {
	switch {
	case errors.Is(err, errPoolRejected):
		return "pool_rejected"
	case errors.Is(err, errStratumMessageTooLarge):
		return "message_too_large"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "connection_error"
}
