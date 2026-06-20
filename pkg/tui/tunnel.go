package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TunnelMonitor renders the localhost tunnel monitoring display.
type TunnelMonitor struct {
	mu sync.Mutex

	Port         int
	PublicURL    string
	Tier         ConnectionTier
	Password     bool
	ReplayMax    int

	activePipes    int
	totalRequests  int
	replayCount    int
	requestLog     []RequestLogEntry
	maxLogEntries  int
}

// RequestLogEntry represents a single logged HTTP request.
type RequestLogEntry struct {
	Timestamp  time.Time
	Method     string
	Path       string
	StatusCode int
	StatusText string
	Latency    time.Duration
}

// NewTunnelMonitor creates a new tunnel monitor display.
func NewTunnelMonitor(port int, publicURL string, replayMax int) *TunnelMonitor {
	return &TunnelMonitor{
		Port:          port,
		PublicURL:     publicURL,
		ReplayMax:     replayMax,
		maxLogEntries: 20, // Show last 20 log entries
	}
}

// SetConnected updates the connection status.
func (tm *TunnelMonitor) SetConnected(tier ConnectionTier) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.Tier = tier
}

// LogRequest adds a request to the log.
func (tm *TunnelMonitor) LogRequest(method, path string, statusCode int, statusText string, latency time.Duration) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.totalRequests++
	if tm.replayCount < tm.ReplayMax {
		tm.replayCount++
	}

	entry := RequestLogEntry{
		Timestamp:  time.Now(),
		Method:     method,
		Path:       path,
		StatusCode: statusCode,
		StatusText: statusText,
		Latency:    latency,
	}

	tm.requestLog = append(tm.requestLog, entry)
	if len(tm.requestLog) > tm.maxLogEntries {
		tm.requestLog = tm.requestLog[1:]
	}
}

// SetActivePipes updates the active pipe count.
func (tm *TunnelMonitor) SetActivePipes(count int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.activePipes = count
}

// Render returns the formatted tunnel monitor display lines.
func (tm *TunnelMonitor) Render() []string {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var lines []string
	lines = append(lines, "hop")
	lines = append(lines, FormatSeparator(50))

	// Header info
	lines = append(lines, fmt.Sprintf("tunneling localhost:%d", tm.Port))
	lines = append(lines, fmt.Sprintf("Public URL: %s", tm.PublicURL))

	statusStr := "Connected"
	if tm.Tier == TierNone {
		statusStr = "Connecting..."
	}
	lines = append(lines, fmt.Sprintf("Status: %s (%s)", statusStr, tm.Tier.String()))

	if tm.Password {
		lines = append(lines, "Password: ✓ Protected")
	}

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Active Pipes: %d    |    Total Requests: %d",
		tm.activePipes, tm.totalRequests))
	lines = append(lines, fmt.Sprintf("Replay Buffer: %d/%d requests captured",
		tm.replayCount, tm.ReplayMax))
	lines = append(lines, FormatSeparator(50))

	// Request log
	for _, entry := range tm.requestLog {
		method := padRight(entry.Method, 5)
		path := padRight(entry.Path, 25)
		status := fmt.Sprintf("%d %s", entry.StatusCode, entry.StatusText)
		latency := fmt.Sprintf("(%s)", formatDuration(entry.Latency))

		lines = append(lines, fmt.Sprintf("[Log] %s %s --> %s %s",
			method, path, status, latency))
	}

	return lines
}

// padRight pads a string to the given width.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

// formatDuration formats a duration for display.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
