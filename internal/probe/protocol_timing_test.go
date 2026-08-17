package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

func TestWatchSessionMeasuresProtocolResponses(t *testing.T) {
	allowLocalEndpointDial(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		readMethod := func(want string) (int, error) {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return 0, err
			}
			var request struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(line, &request); err != nil {
				return 0, err
			}
			if request.Method != want {
				return 0, fmt.Errorf("method=%q want=%q", request.Method, want)
			}
			return request.ID, nil
		}
		respond := func(value any) error {
			if err := json.NewEncoder(w).Encode(value); err != nil {
				return err
			}
			return w.Flush()
		}

		id, err := readMethod(model.ProtocolSubscribe)
		if err != nil {
			serverErr <- err
			return
		}
		time.Sleep(12 * time.Millisecond)
		if err := respond(map[string]any{"id": id, "result": []any{[]any{}, "01020304", 4}, "error": nil}); err != nil {
			serverErr <- err
			return
		}

		id, err = readMethod(model.ProtocolAuthorize)
		if err != nil {
			serverErr <- err
			return
		}
		time.Sleep(8 * time.Millisecond)
		if err := respond(map[string]any{"id": id, "result": true, "error": nil}); err != nil {
			serverErr <- err
			return
		}
		for index, previousHash := range []string{strings.Repeat("0", 64), strings.Repeat("1", 64)} {
			if err := respond(map[string]any{
				"id": nil, "method": "mining.notify",
				"params": []any{fmt.Sprintf("job-%d", index), previousHash, "00", "00", []any{}, "20000000", "17034219", "66ad0000", true},
			}); err != nil {
				serverErr <- err
				return
			}
		}

		serverErr <- nil
	}()

	address := listener.Addr().(*net.TCPAddr)
	out := make(chan event, 16)
	err = watchSession(context.Background(), "test-pool", model.Endpoint{Host: "127.0.0.1", Port: address.Port}, out)
	if err == nil {
		t.Fatal("session should end when the fixture closes its connection")
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	close(out)

	records := map[string]model.ProtocolSample{}
	var forwarded *event
	for e := range out {
		if e.protocol != nil {
			records[e.protocolMethod] = *e.protocol
		}
		if e.prevHash != "" {
			candidate := e
			forwarded = &candidate
		}
	}
	for _, method := range []string{model.ProtocolConnect, model.ProtocolSubscribe, model.ProtocolAuthorize} {
		record, ok := records[method]
		if !ok {
			t.Errorf("missing %s timing", method)
			continue
		}
		if record.ResponseStatus != model.ProtocolStatusOK {
			t.Errorf("%s record=%+v", method, record)
		}
		if record.DurationMS < 0 {
			t.Errorf("%s duration=%v", method, record.DurationMS)
		}
	}
	if got := records[model.ProtocolSubscribe].DurationMS; got < 8 {
		t.Errorf("subscribe timing did not include response delay: %v", got)
	}
	if got := records[model.ProtocolAuthorize].DurationMS; got < 5 {
		t.Errorf("authorize timing did not include response delay: %v", got)
	}
	if forwarded == nil || forwarded.prevHash != strings.Repeat("1", 64) || forwarded.at.IsZero() {
		t.Fatalf("forwarded block candidate=%+v", forwarded)
	}
}

func TestRequestWireStyles(t *testing.T) {
	for _, test := range []struct {
		style stratumWireStyle
		want  string
	}{
		{stratumWireCompact, "{\"id\":1,\"method\":\"mining.subscribe\",\"params\":[\"bitaxe/BM1370/v2.14.2\"]}\n"},
		{stratumWireSpaced, "{\"id\": 1, \"method\": \"mining.subscribe\", \"params\": [\"NerdQAxe++/BM1370/V1.0.37.2-LTS\"]}\n"},
	} {
		var output bytes.Buffer
		writer := bufio.NewWriter(&output)
		agent := "bitaxe/BM1370/v2.14.2"
		if test.style == stratumWireSpaced {
			agent = "NerdQAxe++/BM1370/V1.0.37.2-LTS"
		}
		if err := request(writer, 1, model.ProtocolSubscribe, []string{agent}, test.style); err != nil {
			t.Fatal(err)
		}
		if output.String() != test.want {
			t.Errorf("wire request = %q, want %q", output.String(), test.want)
		}
	}
}

func TestResponseIDRejectsMalformedValues(t *testing.T) {
	for value, want := range map[any]int{
		float64(3):   3,
		"4":          4,
		float64(1.5): -1,
		float64(-1):  -1,
		"invalid":    -1,
		"-2":         -1,
	} {
		if got := responseID(value); got != want {
			t.Errorf("responseID(%v)=%d, want %d", value, got, want)
		}
	}
}

func TestWatchSessionReportsInvalidTLSCertificate(t *testing.T) {
	allowLocalEndpointDial(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()

	address := server.Listener.Addr().(*net.TCPAddr)
	out := make(chan event, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := watchSession(ctx, "test-pool", model.Endpoint{Host: "127.0.0.1", Port: address.Port, TLS: true}, out); err == nil {
		t.Fatal("session unexpectedly accepted an untrusted certificate")
	}
	close(out)

	var tlsRecord *model.ProtocolSample
	for e := range out {
		if e.protocol != nil && e.protocolMethod == model.ProtocolTLSHandshake {
			record := *e.protocol
			tlsRecord = &record
		}
	}
	if tlsRecord == nil {
		t.Fatal("TLS failure observation was not published")
	}
	if tlsRecord.ResponseStatus != model.ProtocolStatusError || tlsRecord.ErrorCategory != model.ProtocolErrorTLSCertificateInvalid {
		t.Fatalf("TLS failure record=%+v", *tlsRecord)
	}
}

func TestWatchSessionBoundsTLSHandshake(t *testing.T) {
	allowLocalEndpointDial(t)
	originalTimeout := requestTimeout
	requestTimeout = 50 * time.Millisecond
	t.Cleanup(func() { requestTimeout = originalTimeout })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(io.Discard, connection)
	}()

	address := listener.Addr().(*net.TCPAddr)
	out := make(chan event, 4)
	started := time.Now()
	err = watchSession(context.Background(), "test-pool", model.Endpoint{Host: "127.0.0.1", Port: address.Port, TLS: true}, out)
	if err == nil {
		t.Fatal("stalled TLS handshake unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("TLS timeout took %s", elapsed)
	}
	<-serverDone
	close(out)
	for event := range out {
		if event.protocol != nil && event.protocolMethod == model.ProtocolTLSHandshake {
			if event.protocol.ResponseStatus != model.ProtocolStatusTimeout {
				t.Fatalf("TLS timeout record=%+v", *event.protocol)
			}
			return
		}
	}
	t.Fatal("missing TLS timeout observation")
}

func TestPublishProtocolUsesWireCompletionTime(t *testing.T) {
	started := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	finished := started.Add(1234 * time.Microsecond)
	out := make(chan event, 1)
	if err := publishProtocolAt(context.Background(), out, "pool", model.Endpoint{Host: "pool.example", Port: 3333}, model.ProtocolConnect, started, finished, model.ProtocolStatusOK, ""); err != nil {
		t.Fatal(err)
	}
	record := (<-out).protocol
	if record == nil || record.DurationMS != 1.234 {
		t.Fatalf("protocol record=%+v, want exact wire duration", record)
	}
	if !record.ObservedAt.Equal(finished) {
		t.Fatalf("observed at=%v, want wire completion %v", record.ObservedAt, finished)
	}
}

func TestWatchSessionRedactsPoolRejection(t *testing.T) {
	allowLocalEndpointDial(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	const sensitiveResponse = "generated-worker-credential-must-not-escape"
	serverErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			serverErr <- err
			return
		}
		var request struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(line, &request); err != nil {
			serverErr <- err
			return
		}
		serverErr <- json.NewEncoder(connection).Encode(map[string]any{
			"id": request.ID, "result": nil, "error": []any{20, sensitiveResponse, nil},
		})
	}()

	address := listener.Addr().(*net.TCPAddr)
	out := make(chan event, 8)
	err = watchSession(context.Background(), "test-pool", model.Endpoint{Host: "127.0.0.1", Port: address.Port}, out)
	if serverErr := <-serverErr; serverErr != nil {
		t.Fatal(serverErr)
	}
	if !errors.Is(err, errPoolRejected) {
		t.Fatalf("watchSession error = %v", err)
	}
	if strings.Contains(err.Error(), sensitiveResponse) {
		t.Fatal("pool response escaped into returned error")
	}
}

func allowLocalEndpointDial(t *testing.T) {
	t.Helper()
	original := dialEndpoint
	dialer := net.Dialer{Timeout: time.Second}
	dialEndpoint = dialer.DialContext
	t.Cleanup(func() {
		dialEndpoint = original
	})
}
