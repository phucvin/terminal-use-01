# terminal-use-01

**Computer Use, for the Terminal.**

`terminal-use-01` is a self-contained Go binary (zero external Go dependencies) that lets Antigravity CLI (`agy`) or any coding agent drive real interactive PTY terminals via `tmux send-keys` and `tmux capture-pane`, while letting you watch—or take over—the live session at any time using `tmux attach`.

## Features

- **Single static binary**: Runs as both an **MCP stdio server** (`terminal-use mcp`) and a **CLI** (`terminal-use start|send|capture|wait|resize|list|close|watch|logs`).
- **Default `tmux` server with `tu-*` isolation**: Managed sessions live on your default `tmux` server with a strict `tu-` prefix, so plain `tmux attach -r -t tu-<name>` works out of the box while unrelated personal `tmux` sessions are never listed or killed. (Set `TERMINAL_USE_SOCKET=<name>` if you ever want a separate socket.)
- **Computer-Use-style action loop**:
  - `terminal_start` — Spawn a persistent `120x40` PTY session with `remain-on-exit`, `window-size manual` (so attaching from a smaller terminal won't reflow TUI layouts), and a helpful status bar (`Detach: Ctrl-b d`).
  - `terminal_send_keys` — Cleanly separates literal `text` (`send-keys -l` or `load-buffer`/`paste-buffer` for multiline text) from validated tmux special `keys` (`Enter`, `C-c`, `Escape`, `Tab`, arrows, `F1`-`F12`), waits for quiescence or `expect_regex`, and returns the updated screen automatically.
  - `terminal_capture` — Capture visible viewport or scrollback (`-S -N`), cursor coordinates `(x, y)`, active command, alive/exit code, and attach commands.
  - `terminal_wait` — Wait for screen quiescence or a Go regex match.
  - `terminal_resize`, `terminal_list`, `terminal_close` — Full lifecycle control.
- **Live human monitoring & takeover**:
  - Read-only watch: `./bin/terminal-use watch [session]` (or `tmux attach -r -t tu-<name>`)
  - Interactive takeover: `./bin/terminal-use watch [session] --interactive` (or `tmux attach -t tu-<name>`)
  - Detach anytime with `Ctrl-b d`.
- **Automatic session logs**: Raw pane output (`<session>.log` via `tmux pipe-pane`) and structured action traces (`<session>.jsonl`) stored under `~/.local/state/terminal-use/logs` (inspect via `terminal-use logs <session> [--events]`; disable with `TERMINAL_USE_NO_LOG=1`).

---

## Build & Test

Requires Go 1.24+ and `tmux`.

```bash
make build
make test
```

The binary is built at `./bin/terminal-use`.

---

## Quick Demo (CLI)

```bash
# 1. Start a managed session
./bin/terminal-use start --name demo

# 2. In another terminal, monitor live (read-only):
./bin/terminal-use watch demo
# (or directly: tmux attach -r -t tu-demo)

# 3. Drive the terminal from an agent or shell
./bin/terminal-use send demo --text 'echo "hello from terminal-use"' --enter
./bin/terminal-use send demo --text 'node -e "console.log(6 * 7)"' --enter --expect '42'

# 4. List & clean up
./bin/terminal-use list
./bin/terminal-use close demo
```

---

## Connecting to Coding Agents

### Antigravity CLI (`agy`)

**Option A — Plugin (recommended, includes MCP server + `terminal-use` skill):**
```bash
agy plugin validate /home/user/terminal-use-01/plugin --strict
agy plugin install /home/user/terminal-use-01/plugin
```

**Option B — MCP server only (`~/.gemini/config/mcp_config.json` or `agy mcp add`):**
```json
{
  "mcpServers": {
    "terminal-use": {
      "command": "/home/user/terminal-use-01/bin/terminal-use",
      "args": ["mcp"]
    }
  }
}
```

### Other MCP Agents (Claude Code, Cursor, Gemini CLI, etc.)

Point your agent's MCP configuration at the same stdio command:
- **Command**: `/home/user/terminal-use-01/bin/terminal-use`
- **Args**: `["mcp"]`

### Shell-Only Agents (No MCP)

Any agent that can run shell commands can drive `./bin/terminal-use` directly with `--json` output and follow [plugin/skills/terminal-use/SKILL.md](./plugin/skills/terminal-use/SKILL.md).
