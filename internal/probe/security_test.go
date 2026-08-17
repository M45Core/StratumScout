package probe

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestReadStratumMessageRejectsOversizedInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(strings.Repeat("x", maxStratumMessageSize+1) + "\n"))
	if _, _, err := readStratumMessage(reader); !errors.Is(err, errStratumMessageTooLarge) {
		t.Fatalf("oversized message error = %v", err)
	}
}

type stagedStratumReader struct {
	reads             int
	secondReadStarted chan struct{}
	releaseSecondRead chan struct{}
}

func (reader *stagedStratumReader) Read(buffer []byte) (int, error) {
	reader.reads++
	switch reader.reads {
	case 1:
		buffer[0] = '{'
		return 1, nil
	case 2:
		close(reader.secondReadStarted)
		<-reader.releaseSecondRead
		return copy(buffer, `"id":null}`+"\n"), nil
	default:
		return 0, io.EOF
	}
}

func TestReadStratumMessageTimestampsFirstReadableByte(t *testing.T) {
	source := &stagedStratumReader{
		secondReadStarted: make(chan struct{}),
		releaseSecondRead: make(chan struct{}),
	}
	type result struct {
		line       []byte
		receivedAt time.Time
		err        error
	}
	resultReady := make(chan result, 1)
	go func() {
		line, receivedAt, err := readStratumMessage(bufio.NewReader(source))
		resultReady <- result{line: line, receivedAt: receivedAt, err: err}
	}()

	<-source.secondReadStarted
	secondReadStartedAt := time.Now()
	close(source.releaseSecondRead)
	got := <-resultReady
	if got.err != nil || string(got.line) != `{"id":null}`+"\n" {
		t.Fatalf("message=%q error=%v", got.line, got.err)
	}
	if !got.receivedAt.Before(secondReadStartedAt) {
		t.Fatalf("receive timestamp %s was not captured before the remaining bytes at %s", got.receivedAt, secondReadStartedAt)
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
		line, _, err := readStratumMessage(reader)
		if err != nil || len(line) != len(message) {
			b.Fatalf("read %d bytes: %v", len(line), err)
		}
	}
}
