package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/phucvin/terminal-use-01/internal/tmux"
)

// RegisterTerminalTools wires all tmux-backed terminal_* tools onto the MCP server.
func RegisterTerminalTools(s *Server, client *tmux.Client) {
	s.RegisterTool(ToolDefinition{
		Name: "terminal_start",
		Description: "Start a new persistent tmux-backed terminal session (prefixed with 'tu-'). " +
			"Returns the initial captured screen and exact tmux attach commands so the user can monitor live.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Optional session name (alphanumeric, '-', '_'; automatically prefixed with 'tu-'). Auto-generated if omitted.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Optional working directory for the terminal session.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Optional initial command to run in the pane (defaults to interactive shell).",
				},
				"cols": map[string]any{
					"type":        "integer",
					"description": "Pane width in columns (20-500, default 120).",
				},
				"rows": map[string]any{
					"type":        "integer",
					"description": "Pane height in rows (5-200, default 40).",
				},
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Optional environment variables to inject into the session.",
				},
				"settle_ms": map[string]any{
					"type":        "integer",
					"description": "Initial settle time in milliseconds before capturing (default 250).",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Maximum initial wait time in milliseconds (default 2000).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var opts tmux.StartOptions
		if err := json.Unmarshal(raw, &opts); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		cap, err := client.Start(ctx, opts)
		if err != nil {
			return errorResult(err)
		}
		return textResult(cap.FormatForLLM())
	})

	s.RegisterTool(ToolDefinition{
		Name: "terminal_send_keys",
		Description: "Send literal text and/or tmux special keys (e.g., Enter, C-c, Escape, Tab, Up, Down) to a managed terminal session. " +
			"Automatically waits for the screen to settle (or match expect_regex) and returns the updated screen capture.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name (with or without 'tu-' prefix).",
				},
				"tab": map[string]any{
					"type":        "string",
					"description": "Optional target tab index or name (defaults to the active tab).",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Literal text to type into the terminal (sent safely with -l or paste-buffer; never interpreted as key names).",
				},
				"keys": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Ordered list of tmux special keys to send after text (e.g. ['Enter'], ['C-c'], ['Escape', 'Enter'], ['Up', 'Up', 'Enter']).",
				},
				"press_enter": map[string]any{
					"type":        "boolean",
					"description": "If true, sends Enter after text and keys.",
				},
				"wait_ms": map[string]any{
					"type":        "integer",
					"description": "Fixed wait time in milliseconds before capturing (ignored if expect_regex is set; otherwise overrides settle detection).",
				},
				"settle_ms": map[string]any{
					"type":        "integer",
					"description": "Quiescence duration in milliseconds (default 350).",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Maximum wait duration in milliseconds (default 5000).",
				},
				"expect_regex": map[string]any{
					"type":        "string",
					"description": "Optional Go regular expression to wait for in the captured screen.",
				},
				"scrollback": map[string]any{
					"type":        "integer",
					"description": "Optional number of scrollback lines above the viewport to include in the capture.",
				},
				"include_ansi": map[string]any{
					"type":        "boolean",
					"description": "If true, preserve ANSI escape sequences in the captured screen (default false).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var opts tmux.SendKeysOptions
		if err := json.Unmarshal(raw, &opts); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		cap, err := client.SendKeys(ctx, opts)
		if err != nil {
			if cap != nil {
				return ToolResult{
					Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %s\n\n%s", err.Error(), cap.FormatForLLM())}},
					IsError: true,
				}
			}
			return errorResult(err)
		}
		return textResult(cap.FormatForLLM())
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_capture",
		Description: "Capture the current screen (and optional scrollback), cursor coordinates, active process, and exit status of a managed terminal session.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name (with or without 'tu-' prefix).",
				},
				"tab": map[string]any{
					"type":        "string",
					"description": "Optional target tab index or name (defaults to the active tab).",
				},
				"scrollback": map[string]any{
					"type":        "integer",
					"description": "Number of scrollback lines above the visible viewport to include (default 0).",
				},
				"include_ansi": map[string]any{
					"type":        "boolean",
					"description": "If true, preserve ANSI color/formatting escape sequences (default false).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var opts tmux.CaptureOptions
		if err := json.Unmarshal(raw, &opts); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		cap, err := client.Capture(ctx, opts)
		if err != nil {
			return errorResult(err)
		}
		return textResult(cap.FormatForLLM())
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_wait",
		Description: "Wait for a terminal session screen to stop changing (quiescence), match an expect_regex pattern, exit, or reach timeout_ms, then return the captured screen.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name (with or without 'tu-' prefix).",
				},
				"tab": map[string]any{
					"type":        "string",
					"description": "Optional target tab index or name (defaults to the active tab).",
				},
				"expect_regex": map[string]any{
					"type":        "string",
					"description": "Optional regular expression to wait for in the captured screen.",
				},
				"settle_ms": map[string]any{
					"type":        "integer",
					"description": "Milliseconds of unchanged screen+cursor required to consider settled (default 350).",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Maximum wait duration in milliseconds (default 5000).",
				},
				"scrollback": map[string]any{
					"type":        "integer",
					"description": "Optional scrollback lines to include when matching/capturing.",
				},
				"include_ansi": map[string]any{
					"type":        "boolean",
					"description": "Preserve ANSI escape codes in output (default false).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var opts tmux.WaitOptions
		if err := json.Unmarshal(raw, &opts); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		cap, err := client.Wait(ctx, opts)
		if err != nil {
			return errorResult(err)
		}
		return textResult(cap.FormatForLLM())
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_resize",
		Description: "Resize the window of a managed terminal session (cols x rows) and return the refreshed screen.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session", "cols", "rows"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name.",
				},
				"tab": map[string]any{
					"type":        "string",
					"description": "Optional target tab index or name (defaults to the active tab).",
				},
				"cols": map[string]any{
					"type":        "integer",
					"description": "New width in columns (20-500).",
				},
				"rows": map[string]any{
					"type":        "integer",
					"description": "New height in rows (5-200).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var params struct {
			Session string `json:"session"`
			Tab     string `json:"tab,omitempty"`
			Cols    int    `json:"cols"`
			Rows    int    `json:"rows"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		cap, err := client.ResizeTab(ctx, params.Session, params.Tab, params.Cols, params.Rows)
		if err != nil {
			return errorResult(err)
		}
		return textResult(cap.FormatForLLM())
	})

	s.RegisterTool(ToolDefinition{
		Name: "terminal_new_tab",
		Description: "Create a new tab (tmux window) inside an existing managed terminal session. " +
			"Switches to the new tab by default (unless background=true) and returns its initial screen capture.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name (with or without 'tu-' prefix).",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Optional tab name (alphanumeric, '-', '_').",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Optional working directory for the new tab.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Optional initial command to run in the new tab (defaults to interactive shell).",
				},
				"cols": map[string]any{
					"type":        "integer",
					"description": "Optional pane width in columns (20-500; defaults to session's active tab width).",
				},
				"rows": map[string]any{
					"type":        "integer",
					"description": "Optional pane height in rows (5-200; defaults to session's active tab height).",
				},
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Optional environment variables to inject into the new tab.",
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "If true, create the tab in the background without switching active focus to it (default false).",
				},
				"settle_ms": map[string]any{
					"type":        "integer",
					"description": "Initial settle time in milliseconds before capturing (default 250).",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Maximum initial wait time in milliseconds (default 2000).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var opts tmux.NewTabOptions
		if err := json.Unmarshal(raw, &opts); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		cap, err := client.NewTab(ctx, opts)
		if err != nil {
			return errorResult(err)
		}
		return textResult(cap.FormatForLLM())
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_switch_tab",
		Description: "Switch the active tab (tmux window) inside a managed terminal session by index, name, or 'next'/'prev'/'last', and return the newly active tab's screen.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session", "tab"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name (with or without 'tu-' prefix).",
				},
				"tab": map[string]any{
					"type":        "string",
					"description": "Target tab index (e.g. '0', '1'), tab name (e.g. 'server'), or 'next', 'prev', 'last'.",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var params struct {
			Session string `json:"session"`
			Tab     string `json:"tab"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		cap, err := client.SwitchTab(ctx, params.Session, params.Tab)
		if err != nil {
			return errorResult(err)
		}
		return textResult(cap.FormatForLLM())
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_close_tab",
		Description: "Close a tab (tmux window) inside a managed terminal session by index or name (defaults to the active tab). Closing the last tab also terminates the session.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name (with or without 'tu-' prefix).",
				},
				"tab": map[string]any{
					"type":        "string",
					"description": "Optional tab index or name to close (defaults to the currently active tab).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var params struct {
			Session string `json:"session"`
			Tab     string `json:"tab,omitempty"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		res, err := client.CloseTab(ctx, params.Session, params.Tab)
		if err != nil {
			return errorResult(err)
		}
		msg := fmt.Sprintf("Closed tab %d:%s in %s.", res.ClosedTab.Index, res.ClosedTab.Name, res.Session)
		if res.SessionClosed {
			return textResult(msg + " Session ended (last tab closed).")
		}
		if res.Capture != nil {
			return textResult(msg + "\n\n" + res.Capture.FormatForLLM())
		}
		return textResult(msg)
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_list_tabs",
		Description: "List all tabs (tmux windows) inside a managed terminal session with their index, name, active state, status, dimensions, and foreground command.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name (with or without 'tu-' prefix).",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var params struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		tabs, err := client.ListTabs(ctx, params.Session)
		if err != nil {
			return errorResult(err)
		}
		norm, _ := tmux.NormalizeSessionName(params.Session)
		var b strings.Builder
		fmt.Fprintf(&b, "Tabs in %s (%d):\n", norm, len(tabs))
		for _, t := range tabs {
			mark := " "
			if t.Active {
				mark = "*"
			}
			status := "alive"
			if !t.Alive {
				if t.ExitCode != nil {
					status = fmt.Sprintf("exited(%d)", *t.ExitCode)
				} else {
					status = "exited"
				}
			}
			fmt.Fprintf(&b, "%s %d:%s | %s | %dx%d | cmd=%s | cwd=%s\n",
				mark, t.Index, t.Name, status, t.Cols, t.Rows, t.CurrentCommand, t.CurrentPath)
		}
		return textResult(strings.TrimRight(b.String(), "\n"))
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_list",
		Description: "List all active managed ('tu-*') tmux sessions with their foreground command, tab count, dimensions, status, and attach commands.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		sessions, err := client.List(ctx)
		if err != nil {
			return errorResult(err)
		}
		if len(sessions) == 0 {
			return textResult("No active terminal-use (tu-*) sessions.")
		}
		var b strings.Builder
		for _, s := range sessions {
			status := "alive"
			if !s.Alive {
				if s.ExitCode != nil {
					status = fmt.Sprintf("exited(%d)", *s.ExitCode)
				} else {
					status = "exited"
				}
			}
			tabInfo := ""
			if s.TabCount > 1 {
				tabInfo = fmt.Sprintf(" | tabs=%d (%s)", s.TabCount, s.ActiveTab)
			}
			fmt.Fprintf(&b, "- %s | %s | %dx%d%s | cmd=%s | attached=%d | watch: %s\n",
				s.Session, status, s.Cols, s.Rows, tabInfo, s.CurrentCommand, s.AttachedClients, s.AttachCommand)
		}
		return textResult(strings.TrimRight(b.String(), "\n"))
	})

	s.RegisterTool(ToolDefinition{
		Name:        "terminal_close",
		Description: "Close and terminate a managed ('tu-*') tmux session.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session"},
			"properties": map[string]any{
				"session": map[string]any{
					"type":        "string",
					"description": "Managed session name to terminate.",
				},
			},
		},
	}, func(ctx context.Context, raw json.RawMessage) ToolResult {
		var params struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return errorResult(fmt.Errorf("invalid arguments: %w", err))
		}
		if err := client.Close(ctx, params.Session); err != nil {
			return errorResult(err)
		}
		norm, _ := tmux.NormalizeSessionName(params.Session)
		return textResult(fmt.Sprintf("Closed session %s.", norm))
	})
}
