package recorder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event represents a single recorded terminal-use action.
type Event struct {
	Timestamp string `json:"ts"`
	Session   string `json:"session"`
	Action    string `json:"action"`
	Data      any    `json:"data,omitempty"`
}

// FileRecorder writes JSONL action traces and provides raw pipe-pane log paths per session.
type FileRecorder struct {
	Dir string
	mu  sync.Mutex
}

// DefaultDir returns ~/.local/state/terminal-use/logs (or TERMINAL_USE_LOG_DIR if set).
func DefaultDir() string {
	if env := os.Getenv("TERMINAL_USE_LOG_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "terminal-use-logs")
	}
	return filepath.Join(home, ".local", "state", "terminal-use", "logs")
}

// NewFileRecorder creates a FileRecorder backed by dir (creating dir on demand).
func NewFileRecorder(dir string) *FileRecorder {
	if dir == "" {
		dir = DefaultDir()
	}
	return &FileRecorder{Dir: dir}
}

// Record appends an action event to <Dir>/<session>.jsonl.
func (r *FileRecorder) Record(session, action string, data any) {
	if r == nil || r.Dir == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return
	}
	ev := Event{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Session:   session,
		Action:    action,
		Data:      data,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	path := filepath.Join(r.Dir, session+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// RawLogPath returns the file path used by `tmux pipe-pane` to store raw session output.
func (r *FileRecorder) RawLogPath(session string) (string, bool) {
	if r == nil || r.Dir == "" {
		return "", false
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return "", false
	}
	return filepath.Join(r.Dir, session+".log"), true
}

// EventLogPath returns the path to the JSONL event trace file for a session.
func (r *FileRecorder) EventLogPath(session string) string {
	return filepath.Join(r.Dir, session+".jsonl")
}
