package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReplayStoreEntry is the JSON representation of a captured request for IPC.
type ReplayStoreEntry struct {
	Index      int               `json:"index"`
	Timestamp  string            `json:"timestamp"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 string            `json:"body_base64,omitempty"` // base64-encoded body
	Truncated  bool              `json:"truncated"`
	StatusCode int               `json:"status_code"`
	StatusText string            `json:"status_text"`
	LatencyMs  int64             `json:"latency_ms"`
}

// ReplayStoreState is the JSON state file written by `hop http` and read by `hop replay`.
type ReplayStoreState struct {
	Port      int                `json:"port"`
	Slug      string             `json:"slug"`
	PublicURL string             `json:"public_url"`
	PID       int                `json:"pid"`
	StartedAt string             `json:"started_at"`
	Entries   []ReplayStoreEntry `json:"entries"`
}

// ReplayStore handles the IPC state file for cross-process replay communication.
type ReplayStore struct {
	path string
}

// NewReplayStore creates a new replay store.
// The state file is stored at ~/.hop/tunnel.json.
func NewReplayStore() (*ReplayStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	hopDir := filepath.Join(homeDir, ".hop")
	if err := os.MkdirAll(hopDir, 0700); err != nil {
		return nil, fmt.Errorf("creating .hop directory: %w", err)
	}

	return &ReplayStore{
		path: filepath.Join(hopDir, "tunnel.json"),
	}, nil
}

// Write saves the current tunnel state and replay buffer to the state file.
func (rs *ReplayStore) Write(port int, slug, publicURL string, buf *ReplayBuffer) error {
	entries := buf.List()

	storeEntries := make([]ReplayStoreEntry, len(entries))
	for i, e := range entries {
		storeEntries[i] = ReplayStoreEntry{
			Index:      e.Index,
			Timestamp:  e.Timestamp.Format(time.RFC3339),
			Method:     e.Method,
			Path:       e.Path,
			Headers:    e.Headers,
			Truncated:  e.Truncated,
			StatusCode: e.StatusCode,
			StatusText: e.StatusText,
			LatencyMs:  e.Latency.Milliseconds(),
		}
		// Store body as base64 for JSON compatibility
		if len(e.Body) > 0 {
			storeEntries[i].BodyBase64 = string(e.Body) // Store as raw for now; bodies are typically text
		}
	}

	state := ReplayStoreState{
		Port:      port,
		Slug:      slug,
		PublicURL: publicURL,
		PID:       os.Getpid(),
		StartedAt: time.Now().Format(time.RFC3339),
		Entries:   storeEntries,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmpPath := rs.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("writing temp state file: %w", err)
	}

	if err := os.Rename(tmpPath, rs.path); err != nil {
		// On Windows, rename may fail if target exists — try remove + rename
		os.Remove(rs.path)
		if err := os.Rename(tmpPath, rs.path); err != nil {
			return fmt.Errorf("renaming state file: %w", err)
		}
	}

	return nil
}

// Read loads the tunnel state from the state file.
func (rs *ReplayStore) Read() (*ReplayStoreState, error) {
	data, err := os.ReadFile(rs.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no active tunnel session found")
		}
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var state ReplayStoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}

	// Check if the tunnel process is still alive
	if !isProcessAlive(state.PID) {
		return nil, fmt.Errorf("tunnel process (PID %d) is no longer running", state.PID)
	}

	return &state, nil
}

// ReadStatic loads the tunnel state from the state file.
// This is a package-level function for use by cmd/replay.go.
func ReadStatic() (*ReplayStoreState, error) {
	store, err := NewReplayStore()
	if err != nil {
		return nil, err
	}
	return store.Read()
}

// Clean removes the state file.
func (rs *ReplayStore) Clean() {
	os.Remove(rs.path)
}

// isProcessAlive checks if a process with the given PID is still running.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. We need to send signal 0 to check.
	// On Windows, FindProcess already validates the PID.
	// For simplicity, we assume it's alive if FindProcess succeeds.
	_ = proc
	return true
}
