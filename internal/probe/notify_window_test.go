package probe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNotifyWindowIgnoresStartupBlockUpdates(t *testing.T) {
	var window notifyWindow
	if window.accept("startup", true) {
		t.Fatal("initial current-block job was accepted")
	}
	if window.accept("startup", true) {
		t.Fatal("update for startup block was accepted")
	}
	if !window.accept("next", true) {
		t.Fatal("first job after a clean block transition was rejected")
	}
	if window.accept("next", true) {
		t.Fatal("same-block transaction update was accepted")
	}
}

func TestStratumNotificationExtractsOnlyBlockTransitionFields(t *testing.T) {
	blockID, message := largeNotifyMessage()
	var got stratumNotification
	if err := json.Unmarshal([]byte(message), &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != "mining.notify" || got.Params.previousHash != blockID || !got.Params.clean || got.Params.count != 9 {
		t.Fatalf("notification=%+v params=%+v", got, got.Params)
	}
}

func BenchmarkDecodeStratumNotification(b *testing.B) {
	_, message := largeNotifyMessage()
	raw := []byte(message)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		var got stratumNotification
		if err := json.Unmarshal(raw, &got); err != nil {
			b.Fatal(err)
		}
	}
}

func largeNotifyMessage() (string, string) {
	blockID := strings.Repeat("a", 64)
	message := `{"id":null,"method":"mining.notify","params":["job",` +
		`"` + blockID + `","` + strings.Repeat("ab", 32_000) + `","coinbase2",` +
		`["` + strings.Repeat("cd", 32) + `"],"version","bits","ntime",true]}`
	return blockID, message
}

func TestNotifyWindowRequiresCleanTransition(t *testing.T) {
	var window notifyWindow
	window.accept("startup", true)
	if window.accept("next", false) {
		t.Fatal("non-clean previous-hash transition was accepted")
	}
	if !window.accept("next", true) {
		t.Fatal("clean retry of previous-hash transition was rejected")
	}
}
