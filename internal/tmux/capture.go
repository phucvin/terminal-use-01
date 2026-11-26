package tmux

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ansiEscapeRe matches standard ANSI CSI, OSC, and 2-byte escape sequences.
var ansiEscapeRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)

// Capture reads the current pane content and metadata from a managed tmux session (active tab or opts.Tab).
func (c *Client) Capture(ctx context.Context, opts CaptureOptions) (*PaneCapture, error) {
	session, err := NormalizeSessionName(opts.Session)
	if err != nil {
		return nil, err
	}

	tabs, err := c.ListTabs(ctx, session)
	if err != nil {
		return nil, err
	}
	targetTab, err := resolveTabFromList(tabs, opts.Tab, false)
	if err != nil {
		return nil, err
	}
	paneTarget := fmt.Sprintf("=%s:%d", session, targetTab.Index)

	metaFmt := strings.Join([]string{
		"#{pane_active}",
		"#{pane_width}",
		"#{pane_height}",
		"#{cursor_x}",
		"#{cursor_y}",
		"#{pane_current_command}",
		"#{pane_dead}",
		"#{pane_dead_status}",
		"#{session_attached}",
		"#{pane_current_path}",
	}, fieldSep)

	metaOut, err := c.Run(ctx, "list-panes", "-t", paneTarget, "-F", metaFmt)
	if err != nil {
		return nil, err
	}

	var chosen []string
	for _, line := range strings.Split(strings.TrimSpace(metaOut), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 10)
		if len(parts) < 10 {
			continue
		}
		if chosen == nil || parts[0] == "1" {
			chosen = parts
			if parts[0] == "1" {
				break
			}
		}
	}
	if len(chosen) < 10 {
		return nil, fmt.Errorf("unexpected tmux metadata format for %q: %q", session, strings.TrimSpace(metaOut))
	}

	cols, _ := strconv.Atoi(chosen[1])
	rows, _ := strconv.Atoi(chosen[2])
	cursorX, _ := strconv.Atoi(chosen[3])
	cursorY, _ := strconv.Atoi(chosen[4])
	dead := chosen[6] == "1"
	var exitCode *int
	if dead && chosen[7] != "" {
		if code, convErr := strconv.Atoi(chosen[7]); convErr == nil {
			exitCode = &code
		}
	}
	attached, _ := strconv.Atoi(chosen[8])

	capArgs := []string{"capture-pane", "-p", "-t", paneTarget}
	if opts.IncludeANSI {
		capArgs = append(capArgs, "-e")
	}
	if opts.Scrollback > 0 {
		capArgs = append(capArgs, "-S", fmt.Sprintf("-%d", opts.Scrollback))
	}

	rawContent, err := c.Run(ctx, capArgs...)
	if err != nil {
		return nil, err
	}

	content := cleanScreen(rawContent, opts.IncludeANSI)

	return &PaneCapture{
		Session:                  session,
		TabIndex:                 targetTab.Index,
		TabName:                  targetTab.Name,
		Tabs:                     tabs,
		Cols:                     cols,
		Rows:                     rows,
		CursorX:                  cursorX,
		CursorY:                  cursorY,
		CurrentCommand:           chosen[5],
		CurrentPath:              chosen[9],
		Alive:                    !dead,
		ExitCode:                 exitCode,
		AttachedClients:          attached,
		AttachCommand:            c.AttachCommand(session, true),
		InteractiveAttachCommand: c.AttachCommand(session, false),
		Content:                  content,
	}, nil
}

// Wait polls capture-pane until ExpectRegex matches, the screen settles (no content/cursor change
// for SettleMs), the pane exits, or TimeoutMs elapses.
func (c *Client) Wait(ctx context.Context, opts WaitOptions) (*PaneCapture, error) {
	session, err := NormalizeSessionName(opts.Session)
	if err != nil {
		return nil, err
	}

	var expectRe *regexp.Regexp
	if opts.ExpectRegex != "" {
		expectRe, err = regexp.Compile(opts.ExpectRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid expect_regex %q: %w", opts.ExpectRegex, err)
		}
	}

	settleMs := opts.SettleMs
	if settleMs <= 0 {
		settleMs = DefaultSettleMs
	}
	timeoutMs := opts.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	pollMs := opts.PollMs
	if pollMs <= 0 {
		pollMs = DefaultPollMs
	}

	capOpts := CaptureOptions{
		Session:     session,
		Tab:         opts.Tab,
		Scrollback:  opts.Scrollback,
		IncludeANSI: opts.IncludeANSI,
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	settleDur := time.Duration(settleMs) * time.Millisecond
	pollDur := time.Duration(pollMs) * time.Millisecond

	var (
		lastSig      string
		lastChangeAt time.Time
	)

	for {
		cap, err := c.Capture(ctx, capOpts)
		if err != nil {
			return nil, err
		}

		if expectRe != nil && expectRe.MatchString(cap.Content) {
			cap.MatchedPattern = true
			return cap, nil
		}

		if !cap.Alive {
			return cap, nil
		}

		sig := fmt.Sprintf("%d:%d:%v:%s", cap.CursorX, cap.CursorY, cap.Alive, cap.Content)
		now := time.Now()
		if lastChangeAt.IsZero() || sig != lastSig {
			lastSig = sig
			lastChangeAt = now
		} else if expectRe == nil && now.Sub(lastChangeAt) >= settleDur {
			return cap, nil
		}

		if now.After(deadline) {
			cap.TimedOut = true
			return cap, nil
		}

		sleepDur := pollDur
		if rem := time.Until(deadline); rem < sleepDur {
			sleepDur = rem
		}
		if sleepDur <= 0 {
			cap.TimedOut = true
			return cap, nil
		}

		timer := time.NewTimer(sleepDur)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// cleanScreen normalizes line endings, strips ANSI unless requested, trims trailing spaces per
// line, and strips empty trailing lines at the bottom while preserving leading indentation.
func cleanScreen(raw string, keepANSI bool) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	if !keepANSI {
		s = ansiEscapeRe.ReplaceAllString(s, "")
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
