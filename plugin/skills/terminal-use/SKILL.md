---
name: terminal-use
description: >-
  Use this skill when driving interactive terminal programs (TUIs like vim, htop, fzf, gdb, less,
  REPLs, SSH sessions, interactive installers/prompts, or long-running dev servers) via tmux so
  the user can monitor live progress with tmux attach.
---

# Terminal Use (`tmux` Interactive Control)

Use `terminal-use` whenever a task requires a **real persistent PTY** or **live human visibility**
into what the agent is running in the terminal.

## Core Workflow

1. **Start a session** with `terminal_start` (or `terminal-use start --name <name>`):
   - Pick a short descriptive name (e.g., `dev`, `debug`, `ssh-box`); it is automatically prefixed
     with `tu-` (`tu-dev`).
   - **Always share the returned watch command** (`tmux attach -r -t tu-<name>` or
     `terminal-use watch tu-<name>`) with the user so they can observe live.
2. **Send input & observe** with `terminal_send_keys`:
   - Put literal characters/commands in `text` (never in `keys`).
   - Put control/navigation strokes in `keys` (e.g., `["Escape"]`, `["C-c"]`, `["Up", "Enter"]`) or
     set `press_enter: true`.
   - Prefer `expect_regex` when waiting for a specific prompt, menu, or completion marker instead of
     blindly guessing timeouts.
   - `terminal_send_keys` automatically captures and returns the updated screen—you usually do not
     need a separate `terminal_capture` call right after it.
3. **Wait for slow output** with `terminal_wait`:
   - If a build/test/download is still streaming when `terminal_send_keys` times out, call
     `terminal_wait` with a larger `timeout_ms` or `expect_regex`.
4. **Interrupt or recover** safely:
   - Use `keys: ["C-c"]` to cancel hung commands, `["C-d"]` to send EOF to REPLs, or `["Escape"]`
     to exit insert mode in `vim`.
   - If a command exits unexpectedly, `remain-on-exit` keeps the final screen visible so you can
     inspect the exit code and traceback with `terminal_capture`.
5. **Clean up** with `terminal_close` once the task is complete (unless the user asked to keep a
   server or session running for inspection).

## Safety & Good Hygiene

- Never type secrets or passwords in plain text unless explicitly instructed; if a prompt requires
  credentials or 2FA, pause and ask the user to attach interactively (`tmux attach -t tu-<name>`)
  and enter them directly.
- Managed sessions are strictly prefixed with `tu-` and cannot touch unrelated personal `tmux`
  sessions.
