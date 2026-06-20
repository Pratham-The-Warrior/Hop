package history

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Direction indicates whether a transfer was sent or received.
type Direction string

const (
	Sent     Direction = "SENT"
	Received Direction = "RECV"
)

// Entry represents a single line in the transfer history log.
type Entry struct {
	Timestamp time.Time
	Direction Direction
	FileName  string
	FileSize  string
	Token     string
	Tier      string // "LAN", "P2P", or "Relay"
	Duration  string
	Verified  bool
}

// historyDir returns the path to the hop config directory.
func historyDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".hop")
}

// historyPath returns the path to the history log file.
func historyPath() string {
	return filepath.Join(historyDir(), "history.log")
}

// Log appends a transfer entry to the history log.
func Log(entry Entry) error {
	dir := historyDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating history directory: %w", err)
	}

	f, err := os.OpenFile(historyPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening history file: %w", err)
	}
	defer f.Close()

	verified := "✓"
	if !entry.Verified {
		verified = "✗"
	}

	line := fmt.Sprintf("%-19s  %-4s  %-30s  %-10s  %-20s  %-6s  %-8s  %s\n",
		entry.Timestamp.Format("2006-01-02 15:04"),
		entry.Direction,
		truncate(entry.FileName, 30),
		entry.FileSize,
		entry.Token,
		entry.Tier,
		entry.Duration,
		verified,
	)

	_, err = f.WriteString(line)
	return err
}

// List reads and returns all entries from the history log.
func List() ([]Entry, error) {
	f, err := os.Open(historyPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No history yet
		}
		return nil, fmt.Errorf("opening history file: %w", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Parse the fixed-width line
		entry, err := parseLine(line)
		if err != nil {
			continue // Skip malformed lines
		}
		entries = append(entries, entry)
	}

	return entries, scanner.Err()
}

// Clear removes the history log file.
func Clear() error {
	return os.Remove(historyPath())
}

// parseLine attempts to parse a history log line back into an Entry.
func parseLine(line string) (Entry, error) {
	// Minimum expected fields from the fixed-width format
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return Entry{}, fmt.Errorf("too few fields: %d", len(fields))
	}

	// Parse timestamp (first two fields: date + time)
	ts, err := time.Parse("2006-01-02 15:04", fields[0]+" "+fields[1])
	if err != nil {
		return Entry{}, fmt.Errorf("parsing timestamp: %w", err)
	}

	return Entry{
		Timestamp: ts,
		Direction: Direction(fields[2]),
		FileName:  fields[3],
		FileSize:  fields[4],
		Token:     fields[5],
		Tier:      fields[6],
		Duration:  fields[7],
		Verified:  len(fields) > 8 && fields[8] == "✓",
	}, nil
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
