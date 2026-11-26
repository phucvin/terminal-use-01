package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	validSessionCharsRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	ErrSessionNotFound = errors.New("tmux session not found")
	ErrInvalidSession  = errors.New("invalid tmux session name")
	ErrPaneDead        = errors.New("tmux pane has exited")
)

// EventRecorder records structured actions and optional raw pane output paths.
type EventRecorder interface {
	Record(session, action string, data any)
	RawLogPath(session string) (string, bool)
}

// Client wraps invocations of the tmux CLI using argument arrays (no shell interpolation).
type Client struct {
	Binary   string
	Socket   string
	Recorder EventRecorder
}

// NewClient creates a tmux Client. By default it uses the default tmux server unless
// TERMINAL_USE_SOCKET is set in the environment.
func NewClient() *Client {
	return &Client{
		Binary: "tmux",
		Socket: strings.TrimSpace(os.Getenv("TERMINAL_USE_SOCKET")),
	}
}

// BaseArgs returns global tmux flags (such as -L <socket> when configured).
func (c *Client) BaseArgs() []string {
	if c.Socket != "" {
		return []string{"-L", c.Socket}
	}
	return nil
}

// Run executes tmux with the given arguments and returns stdout.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	return c.RunWithStdin(ctx, "", args...)
}

// RunWithStdin executes tmux with optional stdin data and returns stdout.
func (c *Client) RunWithStdin(ctx context.Context, stdin string, args ...string) (string, error) {
	bin := c.Binary
	if bin == "" {
		bin = "tmux"
	}
	fullArgs := append(c.BaseArgs(), args...)
	cmd := exec.CommandContext(ctx, bin, fullArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	err := cmd.Run()
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if isNoSessionErr(errMsg) {
			return "", fmt.Errorf("%w: %s", ErrSessionNotFound, errMsg)
		}
		if errMsg != "" {
			return "", fmt.Errorf("tmux %s failed: %s", strings.Join(args, " "), errMsg)
		}
		return "", fmt.Errorf("tmux %s failed: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// AttachCommand builds the human-friendly tmux attach command for a session.
func (c *Client) AttachCommand(session string, readOnly bool) string {
	var parts []string
	parts = append(parts, "tmux")
	if c.Socket != "" {
		parts = append(parts, "-L", c.Socket)
	}
	parts = append(parts, "attach")
	if readOnly {
		parts = append(parts, "-r")
	}
	parts = append(parts, "-t", session)
	return strings.Join(parts, " ")
}

// NormalizeSessionName validates a session name and ensures it has the managed `tu-` prefix.
// Characters like `:` and `.` are rejected to prevent tmux target syntax injection.
func NormalizeSessionName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: session name cannot be empty", ErrInvalidSession)
	}
	if !validSessionCharsRe.MatchString(name) {
		return "", fmt.Errorf("%w: %q must match [a-zA-Z0-9_-]+ (no spaces, ':', or '.')", ErrInvalidSession, name)
	}
	if !strings.HasPrefix(name, SessionPrefix) {
		name = SessionPrefix + name
	}
	if len(name) > 64 {
		return "", fmt.Errorf("%w: %q exceeds 64 characters", ErrInvalidSession, name)
	}
	return name, nil
}

// EnsureManagedSession checks that a session name is strictly prefixed with `tu-` and valid.
func EnsureManagedSession(session string) (string, error) {
	name := strings.TrimSpace(session)
	if !strings.HasPrefix(name, SessionPrefix) {
		return "", fmt.Errorf("%w: session %q must start with %q", ErrInvalidSession, session, SessionPrefix)
	}
	return NormalizeSessionName(name)
}

func isNoSessionErr(stderr string) bool {
	low := strings.ToLower(stderr)
	return strings.Contains(low, "can't find session") ||
		strings.Contains(low, "no server running") ||
		strings.Contains(low, "no sessions") ||
		strings.Contains(low, "error connecting to")
}

func (c *Client) record(session, action string, data any) {
	if c.Recorder != nil {
		c.Recorder.Record(session, action, data)
	}
}
