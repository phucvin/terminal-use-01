package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/phucvin/terminal-use-01/internal/mcp"
	"github.com/phucvin/terminal-use-01/internal/recorder"
	"github.com/phucvin/terminal-use-01/internal/tmux"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// Run dispatches CLI arguments and returns an exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := tmux.NewClient()
	rec := recorder.NewFileRecorder("")
	if os.Getenv("TERMINAL_USE_NO_LOG") != "1" {
		client.Recorder = rec
	}

	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "terminal-use %s\n", mcp.ServerVersion)
		return 0
	case "mcp":
		srv := mcp.NewServer()
		mcp.RegisterTerminalTools(srv, client)
		if err := srv.ServeStdio(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(stderr, "mcp server error: %v\n", err)
			return 1
		}
		return 0
	case "start":
		return cmdStart(ctx, client, rest, stdout, stderr)
	case "send":
		return cmdSend(ctx, client, rest, stdout, stderr)
	case "capture":
		return cmdCapture(ctx, client, rest, stdout, stderr)
	case "wait":
		return cmdWait(ctx, client, rest, stdout, stderr)
	case "resize":
		return cmdResize(ctx, client, rest, stdout, stderr)
	case "list", "ls":
		return cmdList(ctx, client, rest, stdout, stderr)
	case "close", "kill", "stop":
		return cmdClose(ctx, client, rest, stdout, stderr)
	case "watch", "attach":
		return cmdWatch(ctx, client, rest, stdout, stderr)
	case "logs":
		return cmdLogs(rec, rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", sub)
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `terminal-use - Computer-use-style tmux controller for coding agents & human monitoring

Usage:
  terminal-use <command> [flags]

Commands:
  mcp                                   Run the MCP server over stdio (for agy, Claude Code, etc.)
  start   [--name N] [--cwd D] [--cmd C] [--cols 120] [--rows 40] [--env K=V] [--json]
                                        Create a managed tmux session (tu-*)
  send    <session> [--text T] [--key K]... [--enter] [--expect RE] [--wait-ms N] [--json]
                                        Send literal text and/or tmux keys and capture the screen
  capture <session> [--scrollback N] [--ansi] [--json]
                                        Capture current pane screen and metadata
  wait    <session> [--expect RE] [--settle-ms 350] [--timeout-ms 5000] [--json]
                                        Wait for screen quiescence or regex match
  resize  <session> --cols N --rows N [--json]
                                        Resize a managed session window
  list    [--json]                      List active managed (tu-*) sessions
  close   <session> [--json]            Terminate a managed session
  watch   [session] [--interactive]     Attach to a live session (read-only by default; Ctrl-b d to detach)
  logs    <session> [--events] [--tail N]
                                        View raw session log or JSONL action trace
  version                               Print version

Environment:
  TERMINAL_USE_SOCKET   Optional tmux socket name (-L). Defaults to the default tmux server.
  TERMINAL_USE_LOG_DIR  Override log directory (default: ~/.local/state/terminal-use/logs).
  TERMINAL_USE_NO_LOG   Set to 1 to disable recording session logs.
`)
}

// reorderFlags moves flag tokens ahead of positional arguments so users and agents can write
// either `terminal-use send tu-demo --text hi --enter` or `terminal-use send --text hi tu-demo`.
func reorderFlags(args []string, boolFlags map[string]bool) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") || boolFlags[name] {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, a)
		}
	}
	return append(flags, pos...)
}

func cmdStart(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		opts    tmux.StartOptions
		envVars stringSliceFlag
		asJSON  bool
	)
	fs.StringVar(&opts.Name, "name", "", "Session name (prefixed with tu-)")
	fs.StringVar(&opts.Cwd, "cwd", "", "Working directory")
	fs.StringVar(&opts.Command, "cmd", "", "Initial command to execute")
	fs.IntVar(&opts.Cols, "cols", tmux.DefaultCols, "Pane columns")
	fs.IntVar(&opts.Rows, "rows", tmux.DefaultRows, "Pane rows")
	fs.IntVar(&opts.SettleMs, "settle-ms", 250, "Initial settle duration (ms)")
	fs.IntVar(&opts.TimeoutMs, "timeout-ms", 2000, "Initial wait timeout (ms)")
	fs.Var(&envVars, "env", "Environment variable KEY=VALUE (repeatable)")
	fs.BoolVar(&asJSON, "json", false, "Output JSON")

	reordered := reorderFlags(args, map[string]bool{"json": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if opts.Name == "" && fs.NArg() > 0 {
		opts.Name = fs.Arg(0)
	}
	if len(envVars) > 0 {
		opts.Env = make(map[string]string, len(envVars))
		for _, kv := range envVars {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				fmt.Fprintf(stderr, "invalid --env %q: expected KEY=VALUE\n", kv)
				return 2
			}
			opts.Env[k] = v
		}
	}

	cap, err := client.Start(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return writeCapture(stdout, cap, asJSON)
}

func cmdSend(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		opts   tmux.SendKeysOptions
		keys   stringSliceFlag
		asJSON bool
	)
	fs.StringVar(&opts.Session, "session", "", "Managed session name")
	fs.StringVar(&opts.Text, "text", "", "Literal text to type")
	fs.Var(&keys, "key", "Tmux key name to send (repeatable, e.g. --key Escape --key Enter)")
	fs.BoolVar(&opts.PressEnter, "enter", false, "Press Enter after sending text/keys")
	fs.IntVar(&opts.WaitMs, "wait-ms", 0, "Fixed wait before capture (ms)")
	fs.IntVar(&opts.SettleMs, "settle-ms", tmux.DefaultSettleMs, "Quiescence settle time (ms)")
	fs.IntVar(&opts.TimeoutMs, "timeout-ms", tmux.DefaultTimeoutMs, "Wait timeout (ms)")
	fs.StringVar(&opts.ExpectRegex, "expect", "", "Regex pattern to wait for")
	fs.IntVar(&opts.Scrollback, "scrollback", 0, "Extra scrollback lines to capture")
	fs.BoolVar(&opts.IncludeANSI, "ansi", false, "Preserve ANSI escape codes")
	fs.BoolVar(&asJSON, "json", false, "Output JSON")

	reordered := reorderFlags(args, map[string]bool{"enter": true, "ansi": true, "json": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if opts.Session == "" && fs.NArg() > 0 {
		opts.Session = fs.Arg(0)
	}
	if opts.Session == "" {
		fmt.Fprintln(stderr, "error: session name is required")
		return 2
	}
	opts.Keys = keys

	cap, err := client.SendKeys(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		if cap != nil && !asJSON {
			fmt.Fprintln(stdout, cap.FormatForLLM())
		}
		return 1
	}
	return writeCapture(stdout, cap, asJSON)
}

func cmdCapture(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		opts   tmux.CaptureOptions
		asJSON bool
	)
	fs.StringVar(&opts.Session, "session", "", "Managed session name")
	fs.IntVar(&opts.Scrollback, "scrollback", 0, "Extra scrollback lines above viewport")
	fs.BoolVar(&opts.IncludeANSI, "ansi", false, "Preserve ANSI escape codes")
	fs.BoolVar(&asJSON, "json", false, "Output JSON")

	reordered := reorderFlags(args, map[string]bool{"ansi": true, "json": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if opts.Session == "" && fs.NArg() > 0 {
		opts.Session = fs.Arg(0)
	}
	if opts.Session == "" {
		fmt.Fprintln(stderr, "error: session name is required")
		return 2
	}

	cap, err := client.Capture(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return writeCapture(stdout, cap, asJSON)
}

func cmdWait(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		opts   tmux.WaitOptions
		asJSON bool
	)
	fs.StringVar(&opts.Session, "session", "", "Managed session name")
	fs.StringVar(&opts.ExpectRegex, "expect", "", "Regex pattern to wait for")
	fs.IntVar(&opts.SettleMs, "settle-ms", tmux.DefaultSettleMs, "Quiescence settle time (ms)")
	fs.IntVar(&opts.TimeoutMs, "timeout-ms", tmux.DefaultTimeoutMs, "Wait timeout (ms)")
	fs.IntVar(&opts.Scrollback, "scrollback", 0, "Extra scrollback lines")
	fs.BoolVar(&opts.IncludeANSI, "ansi", false, "Preserve ANSI escape codes")
	fs.BoolVar(&asJSON, "json", false, "Output JSON")

	reordered := reorderFlags(args, map[string]bool{"ansi": true, "json": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if opts.Session == "" && fs.NArg() > 0 {
		opts.Session = fs.Arg(0)
	}
	if opts.Session == "" {
		fmt.Fprintln(stderr, "error: session name is required")
		return 2
	}

	cap, err := client.Wait(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return writeCapture(stdout, cap, asJSON)
}

func cmdResize(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("resize", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		session string
		cols    int
		rows    int
		asJSON  bool
	)
	fs.StringVar(&session, "session", "", "Managed session name")
	fs.IntVar(&cols, "cols", 0, "New width in columns")
	fs.IntVar(&rows, "rows", 0, "New height in rows")
	fs.BoolVar(&asJSON, "json", false, "Output JSON")

	reordered := reorderFlags(args, map[string]bool{"json": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if session == "" && fs.NArg() > 0 {
		session = fs.Arg(0)
	}
	if session == "" || cols <= 0 || rows <= 0 {
		fmt.Fprintln(stderr, "error: session, --cols, and --rows are required")
		return 2
	}

	cap, err := client.Resize(ctx, session, cols, rows)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return writeCapture(stdout, cap, asJSON)
}

func cmdList(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	sessions, err := client.List(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(sessions)
		return 0
	}

	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "No active terminal-use (tu-*) sessions.")
		return 0
	}
	for _, s := range sessions {
		status := "alive"
		if !s.Alive {
			if s.ExitCode != nil {
				status = fmt.Sprintf("exited(%d)", *s.ExitCode)
			} else {
				status = "exited"
			}
		}
		fmt.Fprintf(stdout, "%-24s %-10s %dx%-4d cmd=%-12s attached=%d  (%s)\n",
			s.Session, status, s.Cols, s.Rows, s.CurrentCommand, s.AttachedClients, s.AttachCommand)
	}
	return 0
}

func cmdClose(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		session string
		asJSON  bool
	)
	fs.StringVar(&session, "session", "", "Managed session name")
	fs.BoolVar(&asJSON, "json", false, "Output JSON")

	reordered := reorderFlags(args, map[string]bool{"json": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if session == "" && fs.NArg() > 0 {
		session = fs.Arg(0)
	}
	if session == "" {
		fmt.Fprintln(stderr, "error: session name is required")
		return 2
	}

	if err := client.Close(ctx, session); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	norm, _ := tmux.NormalizeSessionName(session)
	if asJSON {
		fmt.Fprintf(stdout, "{\"closed\":%q}\n", norm)
	} else {
		fmt.Fprintf(stdout, "Closed session %s.\n", norm)
	}
	return 0
}

func cmdWatch(ctx context.Context, client *tmux.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		session     string
		interactive bool
	)
	fs.StringVar(&session, "session", "", "Managed session name")
	fs.BoolVar(&interactive, "interactive", false, "Attach interactively (allows typing/intervening; default is read-only)")
	fs.BoolVar(&interactive, "i", false, "Shorthand for --interactive")

	reordered := reorderFlags(args, map[string]bool{"interactive": true, "i": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if session == "" && fs.NArg() > 0 {
		session = fs.Arg(0)
	}

	// Auto-select if there is exactly one active managed session.
	if session == "" {
		sessions, err := client.List(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if len(sessions) == 0 {
			fmt.Fprintln(stderr, "No active terminal-use (tu-*) sessions to watch.")
			return 1
		}
		if len(sessions) > 1 {
			fmt.Fprintln(stderr, "Multiple active sessions found; specify one:")
			for _, s := range sessions {
				fmt.Fprintf(stderr, "  terminal-use watch %s   (cmd=%s)\n", s.Session, s.CurrentCommand)
			}
			return 1
		}
		session = sessions[0].Session
	}

	norm, err := tmux.NormalizeSessionName(session)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	tmuxArgs := append(client.BaseArgs(), "attach-session")
	if !interactive {
		tmuxArgs = append(tmuxArgs, "-r")
	}
	tmuxArgs = append(tmuxArgs, "-t", "="+norm)

	mode := "read-only"
	if interactive {
		mode = "interactive"
	}
	fmt.Fprintf(stderr, "Attaching to %s (%s). Press Ctrl-b d to detach.\n", norm, mode)

	cmd := exec.CommandContext(ctx, client.Binary, tmuxArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "attach failed: %v\n", err)
		return 1
	}
	return 0
}

func cmdLogs(rec *recorder.FileRecorder, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		session string
		events  bool
		tailN   int
	)
	fs.StringVar(&session, "session", "", "Managed session name")
	fs.BoolVar(&events, "events", false, "Show JSONL action trace instead of raw pane output")
	fs.IntVar(&tailN, "tail", 0, "Show only the last N lines (0 = all)")

	reordered := reorderFlags(args, map[string]bool{"events": true})
	if err := fs.Parse(reordered); err != nil {
		return 2
	}
	if session == "" && fs.NArg() > 0 {
		session = fs.Arg(0)
	}
	if session == "" {
		fmt.Fprintln(stderr, "error: session name is required")
		return 2
	}
	norm, err := tmux.NormalizeSessionName(session)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	var path string
	if events {
		path = rec.EventLogPath(norm)
	} else {
		path, _ = rec.RawLogPath(norm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "error reading log %s: %v\n", path, err)
		return 1
	}

	text := string(data)
	if tailN > 0 {
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) > tailN {
			lines = lines[len(lines)-tailN:]
		}
		text = strings.Join(lines, "\n") + "\n"
	}
	_, _ = io.WriteString(stdout, text)
	return 0
}

func writeCapture(w io.Writer, cap *tmux.PaneCapture, asJSON bool) int {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(cap)
		return 0
	}
	fmt.Fprintln(w, cap.FormatForLLM())
	return 0
}
