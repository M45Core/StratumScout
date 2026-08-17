package probe

import (
	"bufio"
	"bytes"
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

func BenchmarkReadStratumMessage(b *testing.B) {
	message := []byte(`{"id":null,"method":"mining.notify","params":["job","0000000000000000000000000000000000000000000000000000000000000000"]}` + "\n")
	var source bytes.Reader
	reader := bufio.NewReaderSize(&source, 4096)
	b.ReportAllocs()
	for b.Loop() {
		source.Reset(message)
		reader.Reset(&source)
		line, err := readStratumMessage(reader)
		if err != nil || len(line) != len(message) {
			b.Fatalf("read %d bytes: %v", len(line), err)
		}
	}
}
