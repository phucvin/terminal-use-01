package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const fieldSep = "|TU|"

// Start creates a new managed tmux session (prefixed with `tu-`), configures monitoring options,
// waits briefly for the initial screen to settle, and returns the initial PaneCapture.
func (c *Client) Start(ctx context.Context, opts StartOptions) (*PaneCapture, error) {
	var session string
	var err error
	if strings.TrimSpace(opts.Name) == "" {
		session = generateSessionName()
	} else {
		session, err = NormalizeSessionName(opts.Name)
		if err != nil {
			return nil, err
		}
	}

	// Refuse to collide with an existing session.
	if exists, err := c.HasSession(ctx, session); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("session %q already exists; capture/send to it or close it first", session)
	}

	cols := opts.Cols
	if cols <= 0 {
		cols = DefaultCols
	}
	rows := opts.Rows
	if rows <= 0 {
		rows = DefaultRows
	}
	if err := validateDimensions(cols, rows); err != nil {
		return nil, err
	}

	args := []string{
		"new-session",
		"-d",
		"-s", session,
		"-x", strconv.Itoa(cols),
		"-y", strconv.Itoa(rows),
	}

	if cwd := strings.TrimSpace(opts.Cwd); cwd != "" {
		if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("invalid cwd %q: must be an existing directory", cwd)
		}
		args = append(args, "-c", cwd)
	}

	if len(opts.Env) > 0 {
		keys := make([]string, 0, len(opts.Env))
		for k := range opts.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, opts.Env[k]))
		}
	}

	if cmd := strings.TrimSpace(opts.Command); cmd != "" {
		args = append(args, cmd)
	}

	// Atomically enable remain-on-exit (with empty format so tmux doesn't scroll crash output
	// off-screen) and window-size manual in the same tmux server turn so fast-exiting commands
	// are never reaped before configureSession runs.
	winTarget := "=" + session + ":"
	args = append(args,
		";", "set-option", "-w", "-t", winTarget, "remain-on-exit", "on",
		";", "set-option", "-w", "-t", winTarget, "remain-on-exit-format", "",
		";", "set-option", "-w", "-t", winTarget, "window-size", "manual",
	)

	if _, err := c.Run(ctx, args...); err != nil {
		return nil, err
	}

	// Configure session options for clean human observation & viewport stability.
	c.configureSession(ctx, session, cols, rows)

	// Optional raw output pipe-pane logging.
	if c.Recorder != nil {
		if logPath, ok := c.Recorder.RawLogPath(session); ok && logPath != "" {
			// pipe-pane takes a shell command; quote the path safely.
			quoted := "'" + strings.ReplaceAll(logPath, "'", `'\''`) + "'"
			_, _ = c.Run(ctx, "pipe-pane", "-o", "-t", "="+session+":", "cat >> "+quoted)
		}
	}

	settleMs := opts.SettleMs
	if settleMs <= 0 {
		settleMs = 250
	}
	timeoutMs := opts.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}

	cap, err := c.Wait(ctx, WaitOptions{
		Session:   session,
		SettleMs:  settleMs,
		TimeoutMs: timeoutMs,
	})
	if err != nil {
		return nil, err
	}

	c.record(session, "start", map[string]any{
		"cwd":     opts.Cwd,
		"command": opts.Command,
		"cols":    cols,
		"rows":    rows,
	})
	return cap, nil
}

func (c *Client) configureSession(ctx context.Context, session string, cols, rows int) {
	sessTarget := "=" + session
	winTarget := "=" + session + ":"
	options := [][]string{
		{"set-option", "-w", "-t", winTarget, "remain-on-exit", "on"},
		{"set-option", "-w", "-t", winTarget, "remain-on-exit-format", ""},
		{"set-option", "-t", sessTarget, "history-limit", strconv.Itoa(DefaultHistoryLimit)},
		{"set-option", "-t", sessTarget, "mouse", "on"},
		{"set-option", "-t", sessTarget, "status", "on"},
		{"set-option", "-t", sessTarget, "status-style", "bg=colour24,fg=colour255"},
		{"set-option", "-t", sessTarget, "status-left", " [terminal-use: #S] "},
		{"set-option", "-t", sessTarget, "status-left-length", "40"},
		{"set-option", "-t", sessTarget, "window-status-format", " #I:#W "},
		{"set-option", "-t", sessTarget, "window-status-current-format", " #I:#W* "},
		{"set-option", "-t", sessTarget, "window-status-current-style", "bg=colour39,fg=colour0,bold"},
		{"set-option", "-t", sessTarget, "status-right", "#{?pane_dead, [EXITED:#{pane_dead_status}] |,} Detach: Ctrl-b d | #{pane_width}x#{pane_height} "},
		{"set-option", "-t", sessTarget, "status-right-length", "60"},
		// Prevent a smaller attached human terminal from shrinking the agent's viewport.
		{"set-option", "-w", "-t", winTarget, "window-size", "manual"},
		{"resize-window", "-t", winTarget, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)},
	}
	for _, opt := range options {
		_, _ = c.Run(ctx, opt...)
	}
}

// HasSession returns true if the managed tmux session currently exists.
func (c *Client) HasSession(ctx context.Context, rawSession string) (bool, error) {
	session, err := NormalizeSessionName(rawSession)
	if err != nil {
		return false, err
	}
	_, err = c.Run(ctx, "has-session", "-t", "="+session)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// List returns all active managed (`tu-*`) tmux sessions.
func (c *Client) List(ctx context.Context) ([]SessionInfo, error) {
	format := strings.Join([]string{
		"#{session_name}",
		"#{window_index}",
		"#{window_name}",
		"#{window_active}",
		"#{pane_active}",
		"#{session_windows}",
		"#{pane_width}",
		"#{pane_height}",
		"#{pane_current_command}",
		"#{pane_dead}",
		"#{pane_dead_status}",
		"#{session_attached}",
		"#{session_created}",
		"#{pane_current_path}",
	}, fieldSep)

	out, err := c.Run(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return []SessionInfo{}, nil
		}
		return nil, err
	}

	bySession := make(map[string]SessionInfo)
	hasActive := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 14)
		if len(parts) < 14 {
			continue
		}
		name := parts[0]
		if !strings.HasPrefix(name, SessionPrefix) {
			continue
		}
		isActive := parts[3] == "1" && parts[4] == "1"
		if _, exists := bySession[name]; exists && (hasActive[name] || !isActive) {
			continue
		}

		winCount, _ := strconv.Atoi(parts[5])
		cols, _ := strconv.Atoi(parts[6])
		rows, _ := strconv.Atoi(parts[7])
		dead := parts[9] == "1"
		var exitCode *int
		if dead && parts[10] != "" {
			if code, convErr := strconv.Atoi(parts[10]); convErr == nil {
				exitCode = &code
			}
		}
		attached, _ := strconv.Atoi(parts[11])
		createdAt := ""
		if ts, convErr := strconv.ParseInt(parts[12], 10, 64); convErr == nil && ts > 0 {
			createdAt = time.Unix(ts, 0).UTC().Format(time.RFC3339)
		}
		activeTab := parts[1]
		if parts[2] != "" {
			activeTab = parts[1] + ":" + parts[2]
		}

		bySession[name] = SessionInfo{
			Session:                  name,
			TabCount:                 winCount,
			ActiveTab:                activeTab,
			Cols:                     cols,
			Rows:                     rows,
			CurrentCommand:           parts[8],
			CurrentPath:              parts[13],
			Alive:                    !dead,
			ExitCode:                 exitCode,
			AttachedClients:          attached,
			CreatedAt:                createdAt,
			AttachCommand:            c.AttachCommand(name, true),
			InteractiveAttachCommand: c.AttachCommand(name, false),
		}
		if isActive {
			hasActive[name] = true
		}
	}

	sessions := make([]SessionInfo, 0, len(bySession))
	for _, s := range bySession {
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Session < sessions[j].Session
	})
	return sessions, nil
}

// Resize changes the dimensions of a managed session's active tab and returns an updated capture.
func (c *Client) Resize(ctx context.Context, rawSession string, cols, rows int) (*PaneCapture, error) {
	return c.ResizeTab(ctx, rawSession, "", cols, rows)
}

// ResizeTab changes the dimensions of a specific tab (or active tab if rawTab is empty)
// in a managed session and returns an updated capture.
func (c *Client) ResizeTab(ctx context.Context, rawSession, rawTab string, cols, rows int) (*PaneCapture, error) {
	session, err := NormalizeSessionName(rawSession)
	if err != nil {
		return nil, err
	}
	if err := validateDimensions(cols, rows); err != nil {
		return nil, err
	}
	tabs, err := c.ListTabs(ctx, session)
	if err != nil {
		return nil, err
	}
	targetTab, err := resolveTabFromList(tabs, rawTab, false)
	if err != nil {
		return nil, err
	}
	target := fmt.Sprintf("=%s:%d", session, targetTab.Index)
	if _, err := c.Run(ctx, "set-option", "-w", "-t", target, "window-size", "manual"); err != nil {
		return nil, err
	}
	if _, err := c.Run(ctx, "resize-window", "-t", target, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)); err != nil {
		return nil, err
	}
	c.record(session, "resize", map[string]any{"tab_index": targetTab.Index, "cols": cols, "rows": rows})
	return c.Capture(ctx, CaptureOptions{Session: session, Tab: strconv.Itoa(targetTab.Index)})
}

// Close terminates a managed (`tu-*`) tmux session.
func (c *Client) Close(ctx context.Context, rawSession string) error {
	session, err := NormalizeSessionName(rawSession)
	if err != nil {
		return err
	}
	if _, err := c.Run(ctx, "kill-session", "-t", "="+session); err != nil {
		return err
	}
	c.record(session, "close", nil)
	return nil
}

func validateDimensions(cols, rows int) error {
	if cols < 20 || cols > 500 {
		return fmt.Errorf("cols %d out of supported range [20, 500]", cols)
	}
	if rows < 5 || rows > 200 {
		return fmt.Errorf("rows %d out of supported range [5, 200]", rows)
	}
	return nil
}

func generateSessionName() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s%s-%s", SessionPrefix, time.Now().UTC().Format("150405"), hex.EncodeToString(b[:]))
}
