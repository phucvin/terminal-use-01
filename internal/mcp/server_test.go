package mcp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/phucvin/terminal-use-01/internal/tmux"
)

func TestMCPServerEndToEnd(t *testing.T) {
	var b [4]byte
	_, _ = rand.Read(b[:])
	sock := "tu-mcp-test-" + hex.EncodeToString(b[:])
	client := &tmux.Client{Binary: "tmux", Socket: sock}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = client.Run(ctx, "kill-server")
	})

	srv := NewServer()
	RegisterTerminalTools(srv, client)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, inR, outW)
	}()

	scanner := bufio.NewScanner(outR)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	send := func(msg any) {
		t.Helper()
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		if _, err := inW.Write(append(data, '\n')); err != nil {
			t.Fatalf("write request: %v", err)
		}
	}

	recv := func() map[string]any {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("unexpected EOF reading response (err=%v)", scanner.Err())
		}
		var resp map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v (raw=%s)", err, scanner.Text())
		}
		return resp
	}

	// 1. Initialize handshake.
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-03-26"},
	})
	initResp := recv()
	res := initResp["result"].(map[string]any)
	if res["protocolVersion"] != "2025-03-26" {
		t.Fatalf("unexpected protocolVersion: %v", res["protocolVersion"])
	}

	// Notification (must not produce a response).
	send(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	// 2. Ping.
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ping"})
	pingResp := recv()
	if pingResp["id"].(float64) != 2 {
		t.Fatalf("expected ping id 2, got %v", pingResp["id"])
	}

	// 3. List tools.
	send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"})
	listResp := recv()
	tools := listResp["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(tools))
	}

	callTool := func(id int, name string, args map[string]any) (string, bool) {
		t.Helper()
		send(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      name,
				"arguments": args,
			},
		})
		resp := recv()
		if resp["error"] != nil {
			t.Fatalf("unexpected JSON-RPC error for %s: %v", name, resp["error"])
		}
		r := resp["result"].(map[string]any)
		isErr, _ := r["isError"].(bool)
		content := r["content"].([]any)
		text := content[0].(map[string]any)["text"].(string)
		return text, isErr
	}

	// 4. Start -> Send -> Capture -> Resize -> List -> Close.
	text, isErr := callTool(4, "terminal_start", map[string]any{
		"name":    "mcpdemo",
		"command": "sh",
		"cols":    90,
		"rows":    25,
	})
	if isErr || !strings.Contains(text, "session: tu-mcpdemo") {
		t.Fatalf("terminal_start failed: isErr=%v text=%s", isErr, text)
	}

	text, isErr = callTool(5, "terminal_send_keys", map[string]any{
		"session":      "mcpdemo",
		"text":         "echo MCP_WORKS_123",
		"press_enter":  true,
		"expect_regex": "MCP_WORKS_123",
		"timeout_ms":   4000,
	})
	if isErr || !strings.Contains(text, "MCP_WORKS_123") {
		t.Fatalf("terminal_send_keys failed: isErr=%v text=%s", isErr, text)
	}

	text, isErr = callTool(6, "terminal_capture", map[string]any{"session": "tu-mcpdemo"})
	if isErr || !strings.Contains(text, "MCP_WORKS_123") {
		t.Fatalf("terminal_capture failed: isErr=%v text=%s", isErr, text)
	}

	text, isErr = callTool(7, "terminal_resize", map[string]any{"session": "tu-mcpdemo", "cols": 110, "rows": 35})
	if isErr || !strings.Contains(text, "size: 110x35") {
		t.Fatalf("terminal_resize failed: isErr=%v text=%s", isErr, text)
	}

	text, isErr = callTool(8, "terminal_list", map[string]any{})
	if isErr || !strings.Contains(text, "tu-mcpdemo") {
		t.Fatalf("terminal_list failed: isErr=%v text=%s", isErr, text)
	}

	text, isErr = callTool(9, "terminal_close", map[string]any{"session": "tu-mcpdemo"})
	if isErr || !strings.Contains(text, "Closed session tu-mcpdemo") {
		t.Fatalf("terminal_close failed: isErr=%v text=%s", isErr, text)
	}

	// Clean shutdown on EOF.
	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Serve did not exit after stdin EOF")
	}
}
