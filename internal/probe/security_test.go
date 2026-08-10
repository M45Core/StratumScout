package probe

import (
	"bufio"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestReadStratumMessageRejectsOversizedInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(strings.Repeat("x", maxStratumMessageSize+1) + "\n"))
	if _, err := readStratumMessage(reader); !errors.Is(err, errStratumMessageTooLarge) {
		t.Fatalf("oversized message error = %v", err)
	}
}

func TestPublicEndpointAddressFilter(t *testing.T) {
	tests := map[string]bool{
		"8.8.8.8":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"10.0.0.1":        false,
		"100.64.0.1":      false,
		"169.254.169.254": false,
		"::1":             false,
		"fc00::1":         false,
		"fe80::1":         false,
	}
	for raw, want := range tests {
		address := netip.MustParseAddr(raw)
		if got := isPublicEndpointAddress(address); got != want {
			t.Errorf("isPublicEndpointAddress(%s) = %t, want %t", raw, got, want)
		}
	}
}
