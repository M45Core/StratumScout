package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
)

func TestCollectFinishesOverlappingBlocksBeforeRequestedRefresh(t *testing.T) {
	allowLocalEndpointDial(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), blockWindow+10*time.Second)
	defer cancel()
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		reader := bufio.NewReader(conn)
		encoder := json.NewEncoder(conn)
		for i, method := range []string{"mining.subscribe", "mining.authorize"} {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				serverErr <- err
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				serverErr <- err
				return
			}
			if req.Method != method {
				serverErr <- fmt.Errorf("method=%s want=%s", req.Method, method)
				return
			}
			var result any = true
			if i == 0 {
				result = []any{[]any{}, "01020304", 4}
			}
			if err := encoder.Encode(map[string]any{"id": req.ID, "result": result, "error": nil}); err != nil {
				serverErr <- err
				return
			}
		}
		// The first hash seeds the session. The next two open overlapping windows.
		for _, hash := range []string{"0", "a", "b"} {
			if err := encoder.Encode(map[string]any{"id": nil, "method": "mining.notify", "params": []any{
				"job-" + hash, strings.Repeat(hash, 64), "00", "00", []any{}, "20000000", "17034219", "66ad0000", true,
			}}); err != nil {
				serverErr <- err
				return
			}
		}
		_, err = io.Copy(io.Discard, reader)
		serverErr <- err
	}()
	address := listener.Addr().(*net.TCPAddr)
	var blocks []string
	err = Collect(ctx, []model.Pool{{ID: "pool", Endpoints: []model.Endpoint{{Host: "127.0.0.1", Port: address.Port}}}}, func(sample model.BlockSample) error {
		blocks = append(blocks, sample.BlockID)
		return ErrConnectionRefresh
	})
	if !errors.Is(err, ErrConnectionRefresh) {
		t.Fatalf("Collect returned %v", err)
	}
	if len(blocks) != 2 || blocks[0] != strings.Repeat("a", 64) || blocks[1] != strings.Repeat("b", 64) {
		t.Fatalf("refresh discarded or repeated an overlapping block: %v", blocks)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
