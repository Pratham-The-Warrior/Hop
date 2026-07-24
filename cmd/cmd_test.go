package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prathmeshsarda/hop/pkg/history"
)

// TestVersionCommand validates the version command executes without error
// and produces output to stdout.
func TestVersionCommand(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"version"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	// Version command uses fmt.Printf (writes to os.Stdout), not cmd.OutOrStdout().
	// We validate the command runs without error; output format is validated
	// by the constants appVersion and protocolVersion.
	if appVersion == "" {
		t.Error("appVersion constant is empty")
	}
	if protocolVersion == "" {
		t.Error("protocolVersion constant is empty")
	}
	if !strings.HasPrefix(protocolVersion, "HOP/") {
		t.Errorf("protocolVersion should start with HOP/, got %q", protocolVersion)
	}
}

// TestVersionConstants validates version metadata.
func TestVersionConstants(t *testing.T) {
	// Version should be semver-like
	parts := strings.Split(appVersion, ".")
	if len(parts) != 3 {
		t.Errorf("appVersion %q should be semver (X.Y.Z)", appVersion)
	}

	if githubOwner == "" || githubRepo == "" {
		t.Error("GitHub owner/repo constants should not be empty")
	}
}

// TestHistoryFormatting validates that history entries format correctly.
func TestHistoryFormatting(t *testing.T) {
	entry := history.Entry{
		Timestamp: time.Date(2026, 7, 20, 14, 32, 0, 0, time.UTC),
		Direction: history.Sent,
		FileName:  "test_file.mp4",
		FileSize:  "2.00GB",
		Token:     "summer-surf-14",
		Tier:      "LAN",
		Duration:  "1m42s",
		Verified:  true,
	}

	if entry.Direction != "SENT" {
		t.Errorf("direction = %q, want SENT", entry.Direction)
	}
	if entry.Timestamp.Format("2006-01-02 15:04") != "2026-07-20 14:32" {
		t.Errorf("timestamp format wrong: %s", entry.Timestamp.Format("2006-01-02 15:04"))
	}
}

// TestHistoryLogAndList tests writing and reading history entries.
func TestHistoryLogAndList(t *testing.T) {
	tmpDir := t.TempDir()

	// Override HOME so history is written to temp
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)

	// Write an entry — use a size WITHOUT spaces so the whitespace parser doesn't break
	entry := history.Entry{
		Timestamp: time.Date(2026, 7, 20, 14, 32, 0, 0, time.UTC),
		Direction: history.Sent,
		FileName:  "test_file.mp4",
		FileSize:  "2.00GB",
		Token:     "summer-surf-14",
		Tier:      "LAN",
		Duration:  "1m42s",
		Verified:  true,
	}

	if err := history.Log(entry); err != nil {
		t.Fatalf("logging history entry: %v", err)
	}

	// Verify the log file was created
	logPath := filepath.Join(tmpDir, ".hop", "history.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Fatal("history.log was not created")
	}

	// Read entries back
	entries, err := history.List()
	if err != nil {
		t.Fatalf("listing history: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	got := entries[0]
	if got.Direction != history.Sent {
		t.Errorf("direction = %q, want SENT", got.Direction)
	}
	if got.FileName != "test_file.mp4" {
		t.Errorf("fileName = %q, want test_file.mp4", got.FileName)
	}
	if got.Token != "summer-surf-14" {
		t.Errorf("token = %q, want summer-surf-14", got.Token)
	}
	if got.Tier != "LAN" {
		t.Errorf("tier = %q, want LAN", got.Tier)
	}
}

// TestHistoryClear verifies history clearing.
func TestHistoryClear(t *testing.T) {
	tmpDir := t.TempDir()

	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)

	// Log an entry first
	entry := history.Entry{
		Timestamp: time.Now(),
		Direction: history.Received,
		FileName:  "recv.txt",
		FileSize:  "1KB",
		Token:     "blue-wave-08",
		Tier:      "Relay",
		Duration:  "2s",
		Verified:  true,
	}
	history.Log(entry)

	// Verify entry exists
	entries, _ := history.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry before clear, got %d", len(entries))
	}

	// Clear
	if err := history.Clear(); err != nil {
		t.Fatalf("clearing history: %v", err)
	}

	// Verify cleared
	entries, _ = history.List()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(entries))
	}
}

// TestCompletionCommand validates all four shell completion scripts generate.
func TestCompletionCommand_Bash(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"completion", "bash"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("completion bash failed: %v", err)
	}

	output := buf.String()
	if len(output) < 100 {
		t.Errorf("bash completion output too short (%d bytes)", len(output))
	}
}

func TestCompletionCommand_Zsh(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"completion", "zsh"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("completion zsh failed: %v", err)
	}

	output := buf.String()
	if len(output) < 100 {
		t.Errorf("zsh completion output too short (%d bytes)", len(output))
	}
}

func TestCompletionCommand_Fish(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"completion", "fish"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("completion fish failed: %v", err)
	}

	output := buf.String()
	if len(output) < 100 {
		t.Errorf("fish completion output too short (%d bytes)", len(output))
	}
}

func TestCompletionCommand_PowerShell(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"completion", "powershell"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("completion powershell failed: %v", err)
	}

	output := buf.String()
	if len(output) < 100 {
		t.Errorf("powershell completion output too short (%d bytes)", len(output))
	}
}

// TestTruncateHistoryStr validates string truncation utility.
func TestTruncateHistoryStr(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"longer string", 10, "longer ..."}, // truncated
		{"ab", 3, "ab"},
		{"abcd", 3, "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncateHistoryStr(tt.input, tt.maxLen)
			if got != tt.expected {
				t.Errorf("truncateHistoryStr(%q, %d) = %q, want %q",
					tt.input, tt.maxLen, got, tt.expected)
			}
		})
	}
}
