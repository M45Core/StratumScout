package probe

import "testing"

func TestNotifyWindowIgnoresStartupBlockUpdates(t *testing.T) {
	var window notifyWindow
	if window.accept("startup", true, false) {
		t.Fatal("initial current-block job was accepted")
	}
	if window.accept("startup", true, true) {
		t.Fatal("transaction update for startup block was accepted")
	}
	if !window.accept("next", true, false) {
		t.Fatal("first job after a clean block transition was rejected")
	}
	if !window.accept("next", true, true) {
		t.Fatal("first transaction-bearing update for active block was rejected")
	}
	if window.accept("next", true, true) {
		t.Fatal("duplicate transaction-bearing update was accepted")
	}
}

func TestNotifyWindowRequiresCleanTransition(t *testing.T) {
	var window notifyWindow
	window.accept("startup", true, false)
	if window.accept("next", false, true) {
		t.Fatal("non-clean previous-hash transition was accepted")
	}
	if !window.accept("next", true, true) {
		t.Fatal("clean retry of previous-hash transition was rejected")
	}
}
