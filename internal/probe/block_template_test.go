package probe

import (
	"testing"
	"time"
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
	connected := map[string]map[string]bool{"first": {"one": true}, "late": {"one": true}}
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	first := activeBlockForEvent(blocks, completed, connected, event{poolID: "first", prevHash: "block", at: started})
	if first == nil {
		t.Fatal("initial block event did not open a window")
	}
	completed["block"] = true
	delete(blocks, "block")
	if late := activeBlockForEvent(blocks, completed, connected, event{poolID: "late", prevHash: "block", at: started.Add(20 * time.Second)}); late != nil {
		t.Fatal("late job reopened a finalized block window")
	}
}

func TestPoolRemainsConnectedWhileAnyEndpointIsOnline(t *testing.T) {
	connected := map[string]map[string]bool{}
	online, offline := true, false
	recordConnectionState(connected, event{poolID: "pool", connectionID: "first", connected: &online})
	recordConnectionState(connected, event{poolID: "pool", connectionID: "second", connected: &online})
	recordConnectionState(connected, event{poolID: "pool", connectionID: "first", connected: &offline})

	block := activeBlockForEvent(map[string]*activeBlock{}, map[string]bool{}, connected, event{poolID: "other", prevHash: "block", at: time.Now()})
	if !block.eligible["pool"] {
		t.Fatal("one endpoint disconnect made a pool with another live endpoint ineligible")
	}
	recordConnectionState(connected, event{poolID: "pool", connectionID: "second", connected: &offline})
	if _, exists := connected["pool"]; exists {
		t.Fatal("pool remained connected after every endpoint disconnected")
	}
}
