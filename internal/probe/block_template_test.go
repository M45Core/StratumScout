package probe

import (
	"strconv"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

func TestFirstValidCoinbaseOnlyTemplateCountsAsArrival(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	block := &activeBlock{
		arrivals: map[string]time.Time{},
		empty:    map[string]bool{},
		tls:      map[string]bool{},
		invalid:  map[string]bool{},
		payout:   map[string]event{},
	}

	recordBlockEvent(block, event{poolID: "pool", at: started, verified: true})
	recordBlockEvent(block, event{poolID: "pool", at: started.Add(2 * time.Second), verified: true, hasTransactions: true})

	if got := block.arrivals["pool"]; !got.Equal(started) {
		t.Fatalf("arrival = %v, want first valid template at %v", got, started)
	}
	if !block.empty["pool"] {
		t.Fatal("coinbase-only first template was not retained as raw evidence")
	}
}

func TestInvalidTemplateDoesNotBecomeArrival(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	block := &activeBlock{
		arrivals: map[string]time.Time{},
		empty:    map[string]bool{},
		tls:      map[string]bool{},
		invalid:  map[string]bool{},
		payout:   map[string]event{},
	}

	recordBlockEvent(block, event{poolID: "pool", at: started, hasTransactions: true})
	recordBlockEvent(block, event{poolID: "pool", at: started.Add(time.Second), verified: true, hasTransactions: true})

	if got := block.arrivals["pool"]; !got.Equal(started.Add(time.Second)) {
		t.Fatalf("arrival = %v, want first valid template", got)
	}
	if !block.invalid["pool"] {
		t.Fatal("invalid template evidence was not retained")
	}
}

func TestFinalizedBlockCannotBeReopenedByLateJob(t *testing.T) {
	blocks := map[string]*activeBlock{}
	completed := map[string]bool{}
	configured := map[string]endpointTarget{
		"first": {poolID: "first", address: "first.example:3333"},
		"late":  {poolID: "late", address: "late.example:3333"},
	}
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	first := activeBlockForEvent(blocks, completed, configured, event{poolID: "first", connectionID: "first", prevHash: "block", at: started, verified: true})
	if first == nil {
		t.Fatal("initial block event did not open a window")
	}
	completed["block"] = true
	delete(blocks, "block")
	if late := activeBlockForEvent(blocks, completed, configured, event{poolID: "late", connectionID: "late", prevHash: "block", at: started.Add(20 * time.Second)}); late != nil {
		t.Fatal("late job reopened a finalized block window")
	}
}

func TestCompletedBlockDeduplicationIsBounded(t *testing.T) {
	completed := map[string]bool{}
	order := make([]string, 0, completedBlockLimit)
	for index := 0; index < completedBlockLimit+10; index++ {
		order = rememberCompletedBlock(completed, order, strconv.Itoa(index))
	}
	if len(completed) != completedBlockLimit || len(order) != completedBlockLimit {
		t.Fatalf("completed=%d order=%d, want both capped at %d", len(completed), len(order), completedBlockLimit)
	}
	if completed["0"] {
		t.Fatal("oldest completed block was not evicted")
	}
	newest := strconv.Itoa(completedBlockLimit + 9)
	before := append([]string(nil), order...)
	order = rememberCompletedBlock(completed, order, newest)
	if len(completed) != completedBlockLimit || len(order) != completedBlockLimit {
		t.Fatal("duplicate insertion changed bounded sizes")
	}
	for index := range order {
		if order[index] != before[index] {
			t.Fatal("duplicate insertion changed eviction order")
		}
	}
}

func TestEveryConfiguredEndpointRemainsEligibleWhileDisconnected(t *testing.T) {
	configured := map[string]endpointTarget{
		"plain": {poolID: "pool", address: "pool.example:3333"},
		"tls":   {poolID: "pool", address: "pool.example:443", tls: true},
	}
	block := activeBlockForEvent(map[string]*activeBlock{}, map[string]bool{}, configured, event{poolID: "other", prevHash: "block", at: time.Now(), verified: true})
	if len(block.eligible) != 2 || block.eligible["plain"].address == "" || !block.eligible["tls"].tls {
		t.Fatalf("eligible endpoints = %+v, want every configured endpoint", block.eligible)
	}
}

func TestActiveBlockWindowsAreBounded(t *testing.T) {
	blocks := map[string]*activeBlock{}
	configured := map[string]endpointTarget{"pool": {poolID: "pool", address: "pool.example:3333"}}
	for index := 0; index < activeBlockLimit; index++ {
		event := event{prevHash: strconv.Itoa(index), at: time.Now(), verified: true}
		if activeBlockForEvent(blocks, map[string]bool{}, configured, event) == nil {
			t.Fatalf("valid window %d was rejected before the limit", index)
		}
	}
	if block := activeBlockForEvent(blocks, map[string]bool{}, configured, event{prevHash: "overflow", at: time.Now(), verified: true}); block != nil {
		t.Fatal("active block window limit was not enforced")
	}
	if block := activeBlockForEvent(map[string]*activeBlock{}, map[string]bool{}, configured, event{prevHash: "invalid", at: time.Now()}); block != nil {
		t.Fatal("invalid template opened a new block window")
	}
}

func TestBlockObservationsAreEmittedPerEndpointIncludingMisses(t *testing.T) {
	started := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	block := &activeBlock{
		id:      "block",
		started: started,
		eligible: map[string]endpointTarget{
			"a": {poolID: "pool", address: "one.example:3333"},
			"b": {poolID: "pool", address: "two.example:443", tls: true},
			"c": {poolID: "other", address: "three.example:3333"},
		},
		arrivals: map[string]time.Time{"a": started.Add(50 * time.Millisecond), "c": started.Add(150 * time.Millisecond)},
		empty:    map[string]bool{},
		invalid:  map[string]bool{},
		payout:   map[string]event{},
	}

	observations := blockObservations(block, "us-west")
	if len(observations) != 3 {
		t.Fatalf("observations = %d, want one per configured endpoint", len(observations))
	}
	plain, secure, other := observations[0], observations[1], observations[2]
	if plain.Version != model.ObservationVersion || plain.PoolID != "pool" || plain.Endpoint != "one.example:3333" || plain.TLS || !plain.Arrived || plain.OffsetMS != 0 {
		t.Fatalf("plain observation = %+v", plain)
	}
	if secure.PoolID != "pool" || secure.Endpoint != "two.example:443" || !secure.TLS || secure.Arrived || secure.OffsetMS != 0 {
		t.Fatalf("missed TLS observation = %+v", secure)
	}
	if other.Endpoint != "three.example:3333" || !other.Arrived || other.OffsetMS != 100 {
		t.Fatalf("other observation = %+v", other)
	}
}
