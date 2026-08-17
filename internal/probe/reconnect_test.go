package probe

import (
	"errors"
	"testing"
	"time"
)

func TestExplicitPoolRejectionIsNotRetried(t *testing.T) {
	if shouldRetry(errors.New("connection reset")) != true {
		t.Fatal("transient network error should be retried")
	}
	if shouldRetry(errPoolRejected) {
		t.Fatal("explicit pool rejection should not be retried")
	}
	if shouldRetry(errors.Join(errors.New("authorization failed"), errPoolRejected)) {
		t.Fatal("wrapped pool rejection should not be retried")
	}
}

func TestReconnectBackoffResetsOnlyAfterStableSession(t *testing.T) {
	delay, next := advanceReconnectBackoff(reconnectBackoffMax, true)
	if delay != reconnectBackoffMin || next != 2*reconnectBackoffMin {
		t.Fatalf("reset backoff delay=%s next=%s", delay, next)
	}
	delay, next = advanceReconnectBackoff(30*time.Second, false)
	if delay != 30*time.Second || next != time.Minute {
		t.Fatalf("continued backoff delay=%s next=%s", delay, next)
	}
	delay, next = advanceReconnectBackoff(reconnectBackoffMax, false)
	if delay != reconnectBackoffMax || next != reconnectBackoffMax {
		t.Fatalf("capped backoff delay=%s next=%s", delay, next)
	}
}
