package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	var b [4]byte
	_, _ = rand.Read(b[:])
	sock := "tu-test-" + hex.EncodeToString(b[:])
	c := &Client{
		Binary: "tmux",
		Socket: sock,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = c.Run(ctx, "kill-server")
	})
	return c
}

func TestDefaultClientUsesDefaultServer(t *testing.T) {
	t.Setenv("TERMINAL_USE_SOCKET", "")
	c := NewClient()
	if c.Socket != "" {
		t.Fatalf("expected empty socket for default tmux server, got %q", c.Socket)
	}
	if cmd := c.AttachCommand("tu-demo", true); cmd != "tmux attach -r -t tu-demo" {
		t.Fatalf("unexpected read-only attach command: %q", cmd)
	}
	if cmd := c.AttachCommand("tu-demo", false); cmd != "tmux attach -t tu-demo" {
		t.Fatalf("unexpected interactive attach command: %q", cmd)
	}
}

func TestValidationAndPrefixSafety(t *testing.T) {
	if got, err := NormalizeSessionName("demo"); err != nil || got != "tu-demo" {
		t.Fatalf("NormalizeSessionName(demo) = %q, %v", got, err)
	}
	if got, err := NormalizeSessionName("tu-demo"); err != nil || got != "tu-demo" {
		t.Fatalf("NormalizeSessionName(tu-demo) = %q, %v", got, err)
	}

	for _, bad := range []string{"", "a b", "foo:0.0", "foo.bar", "../x"} {
		if _, err := NormalizeSessionName(bad); err == nil {
			t.Fatalf("expected error for invalid session name %q", bad)
		}
	}

	for _, okKey := range []string{"Enter", "Escape", "C-c", "C-d", "M-b", "S-Tab", "Up", "F12"} {
		if err := ValidateKey(okKey); err != nil {
			t.Fatalf("expected valid key %q, got err: %v", okKey, err)
		}
	}
	for _, badKey := range []string{"", "rm -rf /", "hello", "C-cc", "; kill-server"} {
		if err := ValidateKey(badKey); err == nil {
			t.Fatalf("expected invalid key %q to fail", badKey)
		}
	}
}

func TestSessionLifecycleLiteralTextAndResize(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Create an unrelated personal session directly on the test tmux server.
	if _, err := c.Run(ctx, "new-session", "-d", "-s", "personal-work", "sh"); err != nil {
		t.Fatalf("creating personal session: %v", err)
	}

	cap, err := c.Start(ctx, StartOptions{
		Name:    "shell",
		Command: "sh",
		Cols:    100,
		Rows:    30,
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if cap.Session != "tu-shell" || cap.Cols != 100 || cap.Rows != 30 || !cap.Alive {
		t.Fatalf("unexpected initial capture: %+v", cap)
	}

	// List must only show managed tu-* sessions (never personal-work).
	sessions, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Session != "tu-shell" {
		t.Fatalf("expected only [tu-shell], got %+v", sessions)
	}

	// Attempting to close "personal-work" must resolve to "tu-personal-work" and NOT kill "personal-work".
	if err := c.Close(ctx, "personal-work"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for personal-work, got %v", err)
	}
	if _, err := c.Run(ctx, "has-session", "-t", "=personal-work"); err != nil {
		t.Fatalf("personal-work session was unexpectedly affected: %v", err)
	}

	// Send literal text containing leading '-' and tmux key words ("Enter", "Space") without misinterpretation.
	cap, err = c.SendKeys(ctx, SendKeysOptions{
		Session:     "shell",
		Text:        `printf '%s\n' -flag Enter Space`,
		PressEnter:  true,
		ExpectRegex: `Space`,
		TimeoutMs:   4000,
	})
	if err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}
	if !cap.MatchedPattern || !strings.Contains(cap.Content, "-flag") || !strings.Contains(cap.Content, "Enter") {
		t.Fatalf("literal text output missing expected content:\n%s", cap.Content)
	}

	// Resize window.
	cap, err = c.Resize(ctx, "tu-shell", 80, 24)
	if err != nil {
		t.Fatalf("Resize failed: %v", err)
	}
	if cap.Cols != 80 || cap.Rows != 24 {
		t.Fatalf("expected 80x24 after resize, got %dx%d", cap.Cols, cap.Rows)
	}

	// Close managed session.
	if err := c.Close(ctx, "tu-shell"); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	exists, err := c.HasSession(ctx, "tu-shell")
	if err != nil || exists {
		t.Fatalf("expected tu-shell to be closed, exists=%v err=%v", exists, err)
	}
}

func TestInteractivePromptAndMultilinePaste(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Interactive prompt test.
	_, err := c.Start(ctx, StartOptions{
		Name:    "prompt",
		Cwd:     tmpDir,
		Command: `sh -c 'printf "ENTER_NAME> "; read name; echo "GREET:$name"; exec sh'`,
	})
	if err != nil {
		t.Fatalf("Start prompt failed: %v", err)
	}
	cap, err := c.Wait(ctx, WaitOptions{
		Session:     "prompt",
		ExpectRegex: `ENTER_NAME>`,
		TimeoutMs:   3000,
	})
	if err != nil || !cap.MatchedPattern {
		t.Fatalf("expected prompt, err=%v cap=%+v", err, cap)
	}

	cap, err = c.SendKeys(ctx, SendKeysOptions{
		Session:     "prompt",
		Text:        "Antigravity",
		PressEnter:  true,
		ExpectRegex: `GREET:Antigravity`,
		TimeoutMs:   3000,
	})
	if err != nil || !cap.MatchedPattern {
		t.Fatalf("interactive reply failed: err=%v content:\n%s", err, cap.Content)
	}

	// Multiline paste test via paste-buffer.
	outFile := filepath.Join(tmpDir, "multi.txt")
	_, err = c.SendKeys(ctx, SendKeysOptions{
		Session:    "prompt",
		Text:       "cat > " + outFile,
		PressEnter: true,
		WaitMs:     150,
	})
	if err != nil {
		t.Fatalf("starting cat failed: %v", err)
	}
	_, err = c.SendKeys(ctx, SendKeysOptions{
		Session: "prompt",
		Text:    "line-one\nline-two\nline-three\n",
		Keys:    []string{"C-d"},
		WaitMs:  250,
	})
	if err != nil {
		t.Fatalf("multiline paste failed: %v", err)
	}
	gotBytes, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading pasted file failed: %v", err)
	}
	if string(gotBytes) != "line-one\nline-two\nline-three\n" {
		t.Fatalf("unexpected multiline file contents: %q", string(gotBytes))
	}
}

func TestCtrlCInterruptAndRemainOnExit(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// 1. Ctrl-C interrupts a long-running command.
	_, err := c.Start(ctx, StartOptions{
		Name:    "ctrlc",
		Command: "sh",
	})
	if err != nil {
		t.Fatalf("Start ctrlc failed: %v", err)
	}
	_, err = c.SendKeys(ctx, SendKeysOptions{
		Session:    "ctrlc",
		Text:       "sleep 60",
		PressEnter: true,
		WaitMs:     250,
	})
	if err != nil {
		t.Fatalf("SendKeys sleep failed: %v", err)
	}

	_, err = c.SendKeys(ctx, SendKeysOptions{
		Session: "ctrlc",
		Keys:    []string{"C-c"},
		WaitMs:  250,
	})
	if err != nil {
		t.Fatalf("SendKeys C-c failed: %v", err)
	}

	cap, err := c.SendKeys(ctx, SendKeysOptions{
		Session:     "ctrlc",
		Text:        "echo RECOVERED_OK",
		PressEnter:  true,
		ExpectRegex: `RECOVERED_OK`,
		TimeoutMs:   3000,
	})
	if err != nil || !cap.MatchedPattern {
		t.Fatalf("shell did not recover after C-c: err=%v content:\n%s", err, cap.Content)
	}

	// 2. remain-on-exit preserves crash output and exit status.
	cap, err = c.Start(ctx, StartOptions{
		Name:      "crash",
		Command:   `sh -c 'echo CRASH_MARKER; exit 42'`,
		TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("Start crash failed: %v", err)
	}
	if cap.Alive {
		cap, err = c.Wait(ctx, WaitOptions{Session: "crash", TimeoutMs: 3000})
		if err != nil {
			t.Fatalf("Wait crash failed: %v", err)
		}
	}
	if cap.Alive {
		t.Fatalf("expected crash pane to be exited")
	}
	if cap.ExitCode == nil || *cap.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %v", cap.ExitCode)
	}
	if !strings.Contains(cap.Content, "CRASH_MARKER") {
		t.Fatalf("expected CRASH_MARKER in dead pane output, got:\n%s", cap.Content)
	}
	if _, err := c.SendKeys(ctx, SendKeysOptions{Session: "crash", Text: "hi"}); !errors.Is(err, ErrPaneDead) {
		t.Fatalf("expected ErrPaneDead when sending to exited pane, got %v", err)
	}
}
