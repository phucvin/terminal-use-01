package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var namedKeys = map[string]bool{
	"Enter":    true,
	"Escape":   true,
	"Tab":      true,
	"BTab":     true,
	"Space":    true,
	"BSpace":   true,
	"DC":       true,
	"IC":       true,
	"Home":     true,
	"End":      true,
	"PgUp":     true,
	"PageUp":   true,
	"PPage":    true,
	"PgDn":     true,
	"PageDown": true,
	"NPage":    true,
	"Up":       true,
	"Down":     true,
	"Left":     true,
	"Right":    true,
	"KPEnter":  true,
	"F1":       true,
	"F2":       true,
	"F3":       true,
	"F4":       true,
	"F5":       true,
	"F6":       true,
	"F7":       true,
	"F8":       true,
	"F9":       true,
	"F10":      true,
	"F11":      true,
	"F12":      true,
}

var modKeyRe = regexp.MustCompile(`^((C|S|M)-){1,3}([!-~]|Enter|Escape|Tab|BTab|Space|BSpace|DC|IC|Home|End|PgUp|PageUp|PPage|PgDn|PageDown|NPage|Up|Down|Left|Right|F1[0-2]|F[1-9])$`)

// ValidateKey checks whether a key token is a valid tmux key name (e.g. "Enter", "C-c", "Escape", "Up").
func ValidateKey(key string) error {
	k := strings.TrimSpace(key)
	if k == "" {
		return fmt.Errorf("empty key token")
	}
	if namedKeys[k] || modKeyRe.MatchString(k) {
		return nil
	}
	return fmt.Errorf("invalid tmux key %q (use 'text' for literal strings, or tmux key names like Enter, Escape, Tab, C-c, Up, Down, F1)", key)
}

// SendKeys sends literal text and/or special keys to a managed session, then waits for
// the screen to settle (or match ExpectRegex) and returns the updated PaneCapture.
func (c *Client) SendKeys(ctx context.Context, opts SendKeysOptions) (*PaneCapture, error) {
	session, err := NormalizeSessionName(opts.Session)
	if err != nil {
		return nil, err
	}

	// Validate all special keys up front before mutating session state.
	keys := make([]string, 0, len(opts.Keys)+1)
	for _, k := range opts.Keys {
		trimmed := strings.TrimSpace(k)
		if err := ValidateKey(trimmed); err != nil {
			return nil, err
		}
		keys = append(keys, trimmed)
	}
	if opts.PressEnter {
		keys = append(keys, "Enter")
	}

	// Verify session/tab exists and pane is still alive.
	pre, err := c.Capture(ctx, CaptureOptions{Session: session, Tab: opts.Tab})
	if err != nil {
		return nil, err
	}
	if !pre.Alive {
		return pre, fmt.Errorf("%w: session %q tab %d has already exited (capture it to inspect final output, or close it)", ErrPaneDead, session, pre.TabIndex)
	}

	target := fmt.Sprintf("=%s:%d", session, pre.TabIndex)
	tabSel := strconv.Itoa(pre.TabIndex)

	// Send literal text safely.
	if opts.Text != "" {
		if strings.ContainsAny(opts.Text, "\r\n") || len(opts.Text) > 512 {
			if err := c.pasteLiteral(ctx, target, opts.Text); err != nil {
				return nil, err
			}
		} else {
			if _, err := c.Run(ctx, "send-keys", "-t", target, "-l", "--", opts.Text); err != nil {
				return nil, err
			}
		}
	}

	// Send each validated special key in order.
	for _, k := range keys {
		if _, err := c.Run(ctx, "send-keys", "-t", target, k); err != nil {
			return nil, err
		}
	}

	c.record(session, "send_keys", map[string]any{
		"tab_index":    pre.TabIndex,
		"text":         opts.Text,
		"keys":         keys,
		"expect_regex": opts.ExpectRegex,
	})

	// Fixed explicit wait when WaitMs > 0 and no ExpectRegex was requested.
	if opts.WaitMs > 0 && opts.ExpectRegex == "" {
		timer := time.NewTimer(time.Duration(opts.WaitMs) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		return c.Capture(ctx, CaptureOptions{
			Session:     session,
			Tab:         tabSel,
			Scrollback:  opts.Scrollback,
			IncludeANSI: opts.IncludeANSI,
		})
	}

	return c.Wait(ctx, WaitOptions{
		Session:     session,
		Tab:         tabSel,
		ExpectRegex: opts.ExpectRegex,
		SettleMs:    opts.SettleMs,
		TimeoutMs:   opts.TimeoutMs,
		IncludeANSI: opts.IncludeANSI,
		Scrollback:  opts.Scrollback,
	})
}

func (c *Client) pasteLiteral(ctx context.Context, target, text string) error {
	var b [4]byte
	_, _ = rand.Read(b[:])
	bufName := "tu-buf-" + hex.EncodeToString(b[:])

	if _, err := c.RunWithStdin(ctx, text, "load-buffer", "-b", bufName, "-"); err != nil {
		return err
	}
	if _, err := c.Run(ctx, "paste-buffer", "-d", "-b", bufName, "-t", target); err != nil {
		_, _ = c.Run(ctx, "delete-buffer", "-b", bufName)
		return err
	}
	return nil
}
