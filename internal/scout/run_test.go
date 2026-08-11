package scout

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

func TestRunContinuouslyStartsFreshRunsUntilCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	err := runContinuously(ctx, Config{}, func(context.Context, Config) error {
		if calls.Add(1) == 2 {
			cancel()
		}
		return nil
	}, time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("runs=%d, want 2", calls.Load())
	}
}

func TestHasFinalizedBlock(t *testing.T) {
	protocol := model.Observation{RecordType: model.RecordTypeProtocol, BlockID: "ignored"}
	if hasFinalizedBlock([]model.Observation{protocol}) {
		t.Fatal("protocol record treated as a finalized block")
	}
	if !hasFinalizedBlock([]model.Observation{{BlockID: "block-1"}}) {
		t.Fatal("block observation was not recognized")
	}
}

func TestRunContinuouslyRetriesFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	err := runContinuously(ctx, Config{}, func(context.Context, Config) error {
		if calls.Add(1) == 3 {
			cancel()
		}
		return errors.New("test failure")
	}, time.Millisecond, 2*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("runs=%d, want 3", calls.Load())
	}
}
