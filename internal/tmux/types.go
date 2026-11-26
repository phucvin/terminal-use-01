package tmux

import (
	"fmt"
	"strings"
)

const (
	SessionPrefix       = "tu-"
	DefaultCols         = 120
	DefaultRows         = 40
	DefaultSettleMs     = 350
	DefaultTimeoutMs    = 5000
	DefaultPollMs       = 100
	DefaultHistoryLimit = 10000
)

// StartOptions configures a new managed tmux session.
type StartOptions struct {
	Name      string            `json:"name,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Command   string            `json:"command,omitempty"`
	Cols      int               `json:"cols,omitempty"`
	Rows      int               `json:"rows,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	SettleMs  int               `json:"settle_ms,omitempty"`
	TimeoutMs int               `json:"timeout_ms,omitempty"`
}

// SendKeysOptions configures sending text and/or special keys to a session.
type SendKeysOptions struct {
	Session     string   `json:"session"`
	Text        string   `json:"text,omitempty"`
	Keys        []string `json:"keys,omitempty"`
	PressEnter  bool     `json:"press_enter,omitempty"`
	WaitMs      int      `json:"wait_ms,omitempty"`
	SettleMs    int      `json:"settle_ms,omitempty"`
	TimeoutMs   int      `json:"timeout_ms,omitempty"`
	ExpectRegex string   `json:"expect_regex,omitempty"`
	IncludeANSI bool     `json:"include_ansi,omitempty"`
	Scrollback  int      `json:"scrollback,omitempty"`
}

// CaptureOptions configures capturing the terminal screen.
type CaptureOptions struct {
	Session     string `json:"session"`
	Scrollback  int    `json:"scrollback,omitempty"`
	IncludeANSI bool   `json:"include_ansi,omitempty"`
}

// WaitOptions configures waiting for screen quiescence or a regex match.
type WaitOptions struct {
	Session     string `json:"session"`
	ExpectRegex string `json:"expect_regex,omitempty"`
	SettleMs    int    `json:"settle_ms,omitempty"`
	TimeoutMs   int    `json:"timeout_ms,omitempty"`
	PollMs      int    `json:"poll_ms,omitempty"`
	IncludeANSI bool   `json:"include_ansi,omitempty"`
	Scrollback  int    `json:"scrollback,omitempty"`
}

// PaneCapture holds a snapshot of a tmux pane and its metadata.
type PaneCapture struct {
	Session                  string `json:"session"`
	Cols                     int    `json:"cols"`
	Rows                     int    `json:"rows"`
	CursorX                  int    `json:"cursor_x"`
	CursorY                  int    `json:"cursor_y"`
	CurrentCommand           string `json:"current_command"`
	CurrentPath              string `json:"current_path"`
	Alive                    bool   `json:"alive"`
	ExitCode                 *int   `json:"exit_code,omitempty"`
	AttachedClients          int    `json:"attached_clients"`
	AttachCommand            string `json:"attach_command"`
	InteractiveAttachCommand string `json:"interactive_attach_command"`
	Content                  string `json:"content"`
	TimedOut                 bool   `json:"timed_out,omitempty"`
	MatchedPattern           bool   `json:"matched_pattern,omitempty"`
}

// FormatForLLM formats a PaneCapture into a compact, structured text block suitable for LLM tool responses.
func (p *PaneCapture) FormatForLLM() string {
	var b strings.Builder

	status := "alive"
	if !p.Alive {
		if p.ExitCode != nil {
			status = fmt.Sprintf("exited (code %d)", *p.ExitCode)
		} else {
			status = "exited"
		}
	}

	fmt.Fprintf(&b, "session: %s | status: %s | size: %dx%d | cursor: (%d,%d) | cmd: %s | attached: %d\n",
		p.Session, status, p.Cols, p.Rows, p.CursorX, p.CursorY, p.CurrentCommand, p.AttachedClients)
	if p.CurrentPath != "" {
		fmt.Fprintf(&b, "cwd: %s\n", p.CurrentPath)
	}
	fmt.Fprintf(&b, "watch (read-only): %s\n", p.AttachCommand)
	fmt.Fprintf(&b, "attach (interactive): %s\n", p.InteractiveAttachCommand)
	if p.MatchedPattern {
		b.WriteString("wait: matched expect_regex\n")
	} else if p.TimedOut {
		b.WriteString("wait: timed out before settling/matching\n")
	}
	b.WriteString("--- SCREEN ---\n")
	b.WriteString(p.Content)
	if p.Content != "" && !strings.HasSuffix(p.Content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("--- END SCREEN ---")
	return b.String()
}

// SessionInfo describes an active managed tmux session.
type SessionInfo struct {
	Session                  string `json:"session"`
	Cols                     int    `json:"cols"`
	Rows                     int    `json:"rows"`
	CurrentCommand           string `json:"current_command"`
	CurrentPath              string `json:"current_path"`
	Alive                    bool   `json:"alive"`
	ExitCode                 *int   `json:"exit_code,omitempty"`
	AttachedClients          int    `json:"attached_clients"`
	CreatedAt                string `json:"created_at,omitempty"`
	AttachCommand            string `json:"attach_command"`
	InteractiveAttachCommand string `json:"interactive_attach_command"`
}
