package probe

import (
	"strconv"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

func TestFirstCandidateCountsAsArrival(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	block := &activeBlock{
		id:       "block",
		arrivals: map[string]time.Time{},
	}

	recordBlockEvent(block, event{poolID: "pool", prevHash: "block", at: started})
	recordBlockEvent(block, event{poolID: "pool", prevHash: "block", at: started.Add(2 * time.Second)})

	if got := block.arrivals["pool"]; !got.Equal(started) {
		t.Fatalf("arrival = %v, want first candidate at %v", got, started)
	}
}

func TestEventWithoutBlockHashDoesNotBecomeArrival(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	block := &activeBlock{
		id:       "block",
		arrivals: map[string]time.Time{},
	}

	recordBlockEvent(block, event{poolID: "pool", at: started})
	recordBlockEvent(block, event{poolID: "pool", prevHash: "block", at: started.Add(time.Second)})

	if got := block.arrivals["pool"]; !got.Equal(started.Add(time.Second)) {
		t.Fatalf("arrival = %v, want first candidate with a job", got)
	}
}

func TestBufferedArrivalIsDrainedBeforeBlockFinalization(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	block := &activeBlock{
		id:      "block",
		started: started,
		eligible: map[string]endpointTarget{
			"first":  {poolID: "first", address: "first.example:3333"},
			"second": {poolID: "second", address: "second.example:3333"},
		},
		arrivals: map[string]time.Time{"first": started},
	}
	events := make(chan event, 1)
	events <- event{connectionID: "second", prevHash: "block", at: started.Add(blockWindow)}

	closed := drainBufferedEvents(events, func(e event) {
		recordBlockEvent(block, e)
	})
	if closed {
		t.Fatal("drain reported a closed event channel")
	}
	sample, ok := blockSample(block, nil)
	if !ok || len(sample.EndpointSamples) != 2 || sample.EndpointSamples[1].ReceivedAt == nil {
		t.Fatalf("buffered arrival was omitted: %+v", sample)
	}
}

func TestBlockSampleAcceptsSingleArrival(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	sample, ok := blockSample(&activeBlock{
		id: "block",
		eligible: map[string]endpointTarget{
			"only": {poolID: "pool", address: "pool.example:3333"},
		},
		arrivals: map[string]time.Time{"only": started},
	}, nil)
	if !ok || len(sample.EndpointSamples) != 1 || sample.EndpointSamples[0].ReceivedAt == nil {
		t.Fatalf("single-arrival sample=%+v ok=%t", sample, ok)
	}
}

func TestNextBlockDeadlineSleepsUntilEarliestOpenWindow(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	deadline, ok := nextBlockDeadline(map[string]*activeBlock{
		"later":   {started: started.Add(2 * time.Second)},
		"earlier": {started: started},
	})
	if !ok || !deadline.Equal(started.Add(blockWindow)) {
		t.Fatalf("deadline=%v ok=%t", deadline, ok)
	}
	if _, ok := nextBlockDeadline(nil); ok {
		t.Fatal("empty block set scheduled a wakeup")
	}
}

func TestBlockWindowUsesEarliestWireTimeAndRejectsLateJobs(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	block := &activeBlock{
		id:       "block",
		started:  started.Add(time.Second),
		arrivals: map[string]time.Time{},
	}
	recordBlockEvent(block, event{connectionID: "earlier", prevHash: "block", at: started})
	recordBlockEvent(block, event{connectionID: "boundary", prevHash: "block", at: started.Add(blockWindow)})
	recordBlockEvent(block, event{connectionID: "late", prevHash: "block", at: started.Add(blockWindow + time.Nanosecond)})

	if !block.started.Equal(started) {
		t.Fatalf("block started=%v, want earliest wire time %v", block.started, started)
	}
	if _, ok := block.arrivals["boundary"]; !ok {
		t.Fatal("arrival exactly at the window boundary was rejected")
	}
	if _, ok := block.arrivals["late"]; ok {
		t.Fatal("arrival after the block window was retained")
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

	first := activeBlockForEvent(blocks, completed, configured, event{poolID: "first", connectionID: "first", prevHash: "block", at: started})
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
	block := activeBlockForEvent(map[string]*activeBlock{}, map[string]bool{}, configured, event{poolID: "other", prevHash: "block", at: time.Now()})
	if len(block.eligible) != 2 || block.eligible["plain"].address == "" || !block.eligible["tls"].tls {
		t.Fatalf("eligible endpoints = %+v, want every configured endpoint", block.eligible)
	}
}

func TestActiveBlockWindowsAreBounded(t *testing.T) {
	blocks := map[string]*activeBlock{}
	configured := map[string]endpointTarget{"pool": {poolID: "pool", address: "pool.example:3333"}}
	for index := 0; index < activeBlockLimit; index++ {
		event := event{prevHash: strconv.Itoa(index), at: time.Now()}
		if activeBlockForEvent(blocks, map[string]bool{}, configured, event) == nil {
			t.Fatalf("valid window %d was rejected before the limit", index)
		}
	}
	if block := activeBlockForEvent(blocks, map[string]bool{}, configured, event{prevHash: "overflow", at: time.Now()}); block != nil {
		t.Fatal("active block window limit was not enforced")
	}
	if block := activeBlockForEvent(map[string]*activeBlock{}, map[string]bool{}, configured, event{at: time.Now()}); block != nil {
		t.Fatal("event without a block hash opened a new window")
	}
}

func TestBlockSampleContainsOnlyEndpointDataActuallyObserved(t *testing.T) {
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
	}
	duration := 12.5
	pending := map[string]model.EndpointSetup{
		"b": {Connect: &model.ProtocolSample{ObservedAt: started, DurationMS: duration, ResponseStatus: model.ProtocolStatusOK}},
	}

	sample, ok := blockSample(block, pending)
	if !ok || sample.BlockID != "block" || len(sample.EndpointSamples) != 3 {
		t.Fatalf("block sample = %+v", sample)
	}
	plain, secure, other := sample.EndpointSamples[0], sample.EndpointSamples[1], sample.EndpointSamples[2]
	if plain.PoolID != "pool" || plain.Endpoint != "one.example:3333" || plain.TLS || plain.ReceivedAt == nil || !plain.ReceivedAt.Equal(started.Add(50*time.Millisecond)) {
		t.Fatalf("plain endpoint sample = %+v", plain)
	}
	if secure.PoolID != "pool" || secure.Endpoint != "two.example:443" || !secure.TLS || secure.ReceivedAt != nil || secure.Setup == nil || secure.Setup.Connect == nil {
		t.Fatalf("setup-only endpoint sample = %+v", secure)
	}
	if other.Endpoint != "three.example:3333" || other.ReceivedAt == nil || !other.ReceivedAt.Equal(started.Add(150*time.Millisecond)) {
		t.Fatalf("other endpoint sample = %+v", other)
	}
}
