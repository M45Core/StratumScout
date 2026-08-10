package probe

import (
	"errors"
	"testing"
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
