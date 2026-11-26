package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	ServerName             = "terminal-use"
	ServerVersion          = "0.1.0"
	DefaultProtocolVersion = "2025-03-26"
)

const serverInstructions = `You have access to tmux-backed interactive terminal sessions via terminal_* tools.
- Use terminal_start to create a persistent PTY session (for interactive TUIs, REPLs, SSH, debuggers, or long-running processes). Always share the returned watch/attach command with the user so they can monitor progress live.
- Use terminal_send_keys to type literal text (via "text") and/or press control/navigation keys (via "keys" or "press_enter"). It automatically waits for the screen to settle and returns the updated screen capture.
- Use terminal_wait when waiting for slow commands, builds, or a regex prompt ("expect_regex") instead of blind polling.
- Use terminal_capture to inspect the current viewport or scrollback, and terminal_close when finished.`

// JSON-RPC 2.0 structures
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolDefinition describes an MCP tool exposed in tools/list.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolContent represents a content item returned by tools/call.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolResult represents the response payload for tools/call.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolHandler handles a single MCP tool invocation.
type ToolHandler func(ctx context.Context, args json.RawMessage) ToolResult

type registeredTool struct {
	def     ToolDefinition
	handler ToolHandler
}

// Server implements a newline-delimited JSON-RPC 2.0 MCP server over stdio.
type Server struct {
	tools   []registeredTool
	toolMap map[string]ToolHandler
	writeMu sync.Mutex
}

// NewServer creates a new MCP Server.
func NewServer() *Server {
	return &Server{
		toolMap: make(map[string]ToolHandler),
	}
}

// RegisterTool adds a tool to the MCP server.
func (s *Server) RegisterTool(def ToolDefinition, handler ToolHandler) {
	s.tools = append(s.tools, registeredTool{def: def, handler: handler})
	s.toolMap[def.Name] = handler
}

// ServeStdio serves MCP requests over os.Stdin and os.Stdout.
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

// Serve reads newline-delimited JSON-RPC messages from in and writes responses to out.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Allow up to 10 MiB per JSON-RPC message for large text pastes.
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = s.writeResponse(out, rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}

		// Notifications have no ID and must not receive a response.
		isNotification := len(req.ID) == 0 || string(req.ID) == "null"
		if isNotification && strings.HasPrefix(req.Method, "notifications/") {
			continue
		}

		resp := s.handleRequest(ctx, req)
		if isNotification {
			continue
		}
		if err := s.writeResponse(out, resp); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		proto := DefaultProtocolVersion
		if params.ProtocolVersion != "" {
			proto = params.ProtocolVersion
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": proto,
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    ServerName,
					"version": ServerVersion,
				},
				"instructions": serverInstructions,
			},
		}

	case "ping":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		defs := make([]ToolDefinition, 0, len(s.tools))
		for _, t := range s.tools {
			defs = append(defs, t.def)
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": defs,
			},
		}

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "invalid tools/call params: " + err.Error()},
			}
		}
		handler, ok := s.toolMap[params.Name]
		if !ok {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool: %q", params.Name)},
			}
		}
		args := params.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		res := handler(ctx, args)
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  res,
		}

	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %q", req.Method)},
		}
	}
}

func (s *Server) writeResponse(out io.Writer, resp rpcResponse) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = out.Write(append(data, '\n'))
	return err
}

func textResult(text string) ToolResult {
	return ToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
	}
}

func errorResult(err error) ToolResult {
	return ToolResult{
		Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}},
		IsError: true,
	}
}
