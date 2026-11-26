package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ListTabs returns all tabs (tmux windows) in a managed session ordered by window index.
func (c *Client) ListTabs(ctx context.Context, rawSession string) ([]TabInfo, error) {
	session, err := NormalizeSessionName(rawSession)
	if err != nil {
		return nil, err
	}

	format := strings.Join([]string{
		"#{window_index}",
		"#{window_name}",
		"#{window_active}",
		"#{pane_width}",
		"#{pane_height}",
		"#{pane_current_command}",
		"#{pane_dead}",
		"#{pane_dead_status}",
		"#{pane_current_path}",
	}, fieldSep)

	out, err := c.Run(ctx, "list-windows", "-t", "="+session, "-F", format)
	if err != nil {
		return nil, err
	}

	var tabs []TabInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 9)
		if len(parts) < 9 {
			continue
		}
		idx, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		cols, _ := strconv.Atoi(parts[3])
		rows, _ := strconv.Atoi(parts[4])
		dead := parts[6] == "1"
		var exitCode *int
		if dead && parts[7] != "" {
			if code, convErr := strconv.Atoi(parts[7]); convErr == nil {
				exitCode = &code
			}
		}

		tabs = append(tabs, TabInfo{
			Index:          idx,
			Name:           parts[1],
			Active:         parts[2] == "1",
			Cols:           cols,
			Rows:           rows,
			CurrentCommand: parts[5],
			CurrentPath:    parts[8],
			Alive:          !dead,
			ExitCode:       exitCode,
		})
	}

	sort.Slice(tabs, func(i, j int) bool {
		return tabs[i].Index < tabs[j].Index
	})
	return tabs, nil
}

func resolveTabFromList(tabs []TabInfo, rawTab string, allowNav bool) (TabInfo, error) {
	if len(tabs) == 0 {
		return TabInfo{}, fmt.Errorf("%w: session has no tabs", ErrTabNotFound)
	}

	trimmed := strings.TrimSpace(rawTab)
	if trimmed == "" {
		for _, t := range tabs {
			if t.Active {
				return t, nil
			}
		}
		return tabs[0], nil
	}

	sel, err := ValidateTabSelector(trimmed)
	if err != nil {
		return TabInfo{}, err
	}

	// 1. Exact index match (e.g. "0", "1").
	if idx, convErr := strconv.Atoi(sel); convErr == nil {
		for _, t := range tabs {
			if t.Index == idx {
				return t, nil
			}
		}
	}

	// 2. Exact tab name match.
	var nameMatches []TabInfo
	for _, t := range tabs {
		if t.Name == sel {
			nameMatches = append(nameMatches, t)
		}
	}
	if len(nameMatches) == 1 {
		return nameMatches[0], nil
	}
	if len(nameMatches) > 1 {
		indices := make([]string, 0, len(nameMatches))
		for _, m := range nameMatches {
			indices = append(indices, strconv.Itoa(m.Index))
		}
		return TabInfo{}, fmt.Errorf("%w: multiple tabs are named %q (indices: %s); specify a tab index instead",
			ErrInvalidTab, sel, strings.Join(indices, ", "))
	}

	// 3. Relative navigation keywords when allowed (switch).
	if allowNav {
		activePos := 0
		for i, t := range tabs {
			if t.Active {
				activePos = i
				break
			}
		}
		switch strings.ToLower(sel) {
		case "next":
			return tabs[(activePos+1)%len(tabs)], nil
		case "prev", "previous":
			return tabs[(activePos-1+len(tabs))%len(tabs)], nil
		}
	}

	avail := make([]string, 0, len(tabs))
	for _, t := range tabs {
		avail = append(avail, fmt.Sprintf("%d:%s", t.Index, t.Name))
	}
	return TabInfo{}, fmt.Errorf("%w: %q (available tabs: %s)", ErrTabNotFound, sel, strings.Join(avail, ", "))
}

// NewTab creates a new tab (tmux window) inside an existing managed session, configures
// remain-on-exit and viewport dimensions atomically, waits for the new tab to settle,
// and returns its initial PaneCapture.
func (c *Client) NewTab(ctx context.Context, opts NewTabOptions) (*PaneCapture, error) {
	session, err := NormalizeSessionName(opts.Session)
	if err != nil {
		return nil, err
	}

	var tabName string
	if strings.TrimSpace(opts.Name) != "" {
		tabName, err = ValidateTabName(opts.Name)
		if err != nil {
			return nil, err
		}
	}

	tabs, err := c.ListTabs(ctx, session)
	if err != nil {
		return nil, err
	}

	nextIdx := 0
	cols := opts.Cols
	rows := opts.Rows
	for _, t := range tabs {
		if t.Index >= nextIdx {
			nextIdx = t.Index + 1
		}
		if t.Active {
			if cols <= 0 && t.Cols > 0 {
				cols = t.Cols
			}
			if rows <= 0 && t.Rows > 0 {
				rows = t.Rows
			}
		}
	}
	if cols <= 0 {
		cols = DefaultCols
	}
	if rows <= 0 {
		rows = DefaultRows
	}
	if err := validateDimensions(cols, rows); err != nil {
		return nil, err
	}

	winTarget := fmt.Sprintf("=%s:%d", session, nextIdx)
	args := []string{
		"new-window",
		"-P",
		"-F", "#{window_index}",
		"-t", winTarget,
	}
	if opts.Background {
		args = append(args, "-d")
	}
	if tabName != "" {
		args = append(args, "-n", tabName)
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

	// Atomically configure window options in the same tmux server turn so fast-exiting
	// commands are preserved and viewport size is locked immediately.
	args = append(args,
		";", "set-option", "-w", "-t", winTarget, "remain-on-exit", "on",
		";", "set-option", "-w", "-t", winTarget, "remain-on-exit-format", "",
		";", "set-option", "-w", "-t", winTarget, "window-size", "manual",
		";", "resize-window", "-t", winTarget, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows),
	)
	if tabName != "" {
		args = append(args, ";", "set-option", "-w", "-t", winTarget, "automatic-rename", "off")
	}

	out, err := c.Run(ctx, args...)
	if err != nil {
		return nil, err
	}

	createdIdx := nextIdx
	if parsed, convErr := strconv.Atoi(strings.TrimSpace(out)); convErr == nil {
		createdIdx = parsed
	}
	createdTarget := fmt.Sprintf("=%s:%d", session, createdIdx)

	if c.Recorder != nil {
		if logPath, ok := c.Recorder.RawLogPath(session); ok && logPath != "" {
			quoted := "'" + strings.ReplaceAll(logPath, "'", `'\''`) + "'"
			_, _ = c.Run(ctx, "pipe-pane", "-o", "-t", createdTarget, "cat >> "+quoted)
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
		Tab:       strconv.Itoa(createdIdx),
		SettleMs:  settleMs,
		TimeoutMs: timeoutMs,
	})
	if err != nil {
		return nil, err
	}

	c.record(session, "new_tab", map[string]any{
		"tab_index":  createdIdx,
		"tab_name":   cap.TabName,
		"cwd":        opts.Cwd,
		"command":    opts.Command,
		"background": opts.Background,
		"cols":       cols,
		"rows":       rows,
	})
	return cap, nil
}

// SwitchTab changes the active tab (tmux window) of a managed session and returns a capture
// of the newly active tab. Accepts a tab index, tab name, or "next" / "prev" / "last".
func (c *Client) SwitchTab(ctx context.Context, rawSession, rawTab string) (*PaneCapture, error) {
	session, err := NormalizeSessionName(rawSession)
	if err != nil {
		return nil, err
	}
	sel, err := ValidateTabSelector(rawTab)
	if err != nil {
		return nil, err
	}

	tabs, err := c.ListTabs(ctx, session)
	if err != nil {
		return nil, err
	}

	// Support tmux "last" (previously active tab) unless a tab is literally named "last".
	hasLiteralLast := false
	for _, t := range tabs {
		if t.Name == sel {
			hasLiteralLast = true
			break
		}
	}
	if strings.EqualFold(sel, "last") && !hasLiteralLast {
		if _, err := c.Run(ctx, "last-window", "-t", "="+session); err != nil {
			return nil, err
		}
		cap, err := c.Capture(ctx, CaptureOptions{Session: session})
		if err != nil {
			return nil, err
		}
		c.record(session, "switch_tab", map[string]any{
			"selector":  sel,
			"tab_index": cap.TabIndex,
			"tab_name":  cap.TabName,
		})
		return cap, nil
	}

	targetTab, err := resolveTabFromList(tabs, sel, true)
	if err != nil {
		return nil, err
	}

	winTarget := fmt.Sprintf("=%s:%d", session, targetTab.Index)
	if _, err := c.Run(ctx, "select-window", "-t", winTarget); err != nil {
		return nil, err
	}

	cap, err := c.Capture(ctx, CaptureOptions{Session: session})
	if err != nil {
		return nil, err
	}

	c.record(session, "switch_tab", map[string]any{
		"selector":  sel,
		"tab_index": cap.TabIndex,
		"tab_name":  cap.TabName,
	})
	return cap, nil
}

// CloseTab closes a tab (tmux window) inside a managed session. If rawTab is empty, closes
// the currently active tab. If it is the last remaining tab, the tmux session also terminates.
func (c *Client) CloseTab(ctx context.Context, rawSession, rawTab string) (*CloseTabResult, error) {
	session, err := NormalizeSessionName(rawSession)
	if err != nil {
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

	winTarget := fmt.Sprintf("=%s:%d", session, targetTab.Index)
	if _, err := c.Run(ctx, "kill-window", "-t", winTarget); err != nil {
		return nil, err
	}

	result := &CloseTabResult{
		Session:       session,
		ClosedTab:     targetTab,
		SessionClosed: len(tabs) <= 1,
	}

	if !result.SessionClosed {
		cap, capErr := c.Capture(ctx, CaptureOptions{Session: session})
		if capErr != nil {
			if errors.Is(capErr, ErrSessionNotFound) {
				result.SessionClosed = true
			} else {
				return nil, capErr
			}
		} else {
			result.Capture = cap
		}
	}

	c.record(session, "close_tab", map[string]any{
		"tab_index":      targetTab.Index,
		"tab_name":       targetTab.Name,
		"session_closed": result.SessionClosed,
	})
	return result, nil
}
