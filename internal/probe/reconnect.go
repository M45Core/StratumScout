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

// watch keeps a pool endpoint represented through ordinary transient network
// failures. Backoff is bounded so a recovered endpoint rejoins promptly.
func watch(ctx context.Context, poolID string, endpoint model.Endpoint, out chan<- event) error {
	backoff := time.Second
	for {
		err := watchSession(ctx, poolID, endpoint, out)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !shouldRetry(err) {
			return fmt.Errorf("not retrying after permanent rejection: %w", err)
		}
		log.Printf("probe pool=%s endpoint=%s disconnected category=%s retry_in=%s", poolID, net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)), connectionErrorCategory(err), backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
		if backoff > time.Minute {
			backoff = time.Minute
		}
	}
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
