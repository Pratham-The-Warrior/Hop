package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ConnectionTier represents the active connection type.
type ConnectionTier int

const (
	TierNone    ConnectionTier = iota
	TierLAN                    // Tier 1: Direct LAN
	TierP2P                    // Tier 2: NAT hole-punched P2P
	TierRelayed                // Tier 3: Cloud relay
)

// String returns the display string for a connection tier.
func (t ConnectionTier) String() string {
	switch t {
	case TierLAN:
		return "⚡ Direct (LAN)"
	case TierP2P:
		return "🔗 Direct (P2P)"
	case TierRelayed:
		return "☁️  Relayed"
	default:
		return "⏳ Connecting..."
	}
}

// ProgressBar tracks and renders file transfer progress.
type ProgressBar struct {
	mu sync.Mutex

	FileName  string
	FileSize  int64
	Token     string
	Link      string
	Tier      ConnectionTier
	Compress  bool
	Limit     string
	IsDir     bool
	Direction string // "sharing" or "receiving"

	transferred int64
	startTime   time.Time
	speedWindow []speedSample
	completed   bool
	verified    bool
	hashPrefix  string
}

type speedSample struct {
	time  time.Time
	bytes int64
}

// NewProgressBar creates a new progress bar for a transfer.
func NewProgressBar(fileName string, fileSize int64, token, link string) *ProgressBar {
	return &ProgressBar{
		FileName:  fileName,
		FileSize:  fileSize,
		Token:     token,
		Link:      link,
		Direction: "sharing",
		startTime: time.Now(),
	}
}

// Update records bytes transferred.
func (p *ProgressBar) Update(bytesTransferred int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transferred = bytesTransferred
	p.speedWindow = append(p.speedWindow, speedSample{
		time:  time.Now(),
		bytes: bytesTransferred,
	})
	// Keep only last 3 seconds of samples
	cutoff := time.Now().Add(-3 * time.Second)
	for len(p.speedWindow) > 1 && p.speedWindow[0].time.Before(cutoff) {
		p.speedWindow = p.speedWindow[1:]
	}
}

// Complete marks the transfer as successfully completed.
func (p *ProgressBar) Complete(hashPrefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transferred = p.FileSize
	p.completed = true
	p.verified = true
	p.hashPrefix = hashPrefix
}

// Render returns the formatted progress display lines.
func (p *ProgressBar) Render() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var lines []string
	lines = append(lines, "hop")
	lines = append(lines, FormatSeparator(50))

	// File info
	if p.IsDir {
		lines = append(lines, fmt.Sprintf("%s '%s/' (tar.gz)", p.Direction, p.FileName))
	} else {
		lines = append(lines, fmt.Sprintf("%s '%s' (%s)", p.Direction, p.FileName, formatBytes(p.FileSize)))
	}

	// Token and link
	lines = append(lines, fmt.Sprintf("Token: %s", p.Token))
	if p.Link != "" {
		lines = append(lines, fmt.Sprintf("Link:  %s", p.Link))
	}
	lines = append(lines, fmt.Sprintf("Connection: %s", p.Tier.String()))
	lines = append(lines, "")

	// Progress bar
	percentage := float64(0)
	if p.FileSize > 0 {
		percentage = float64(p.transferred) / float64(p.FileSize) * 100
	}

	barWidth := 36
	filled := int(percentage / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}

	var bar string
	if p.completed {
		bar = fmt.Sprintf("[%s] 100%%", strings.Repeat("=", barWidth))
	} else {
		bar = fmt.Sprintf("[%s>%s] %d%%",
			strings.Repeat("=", filled),
			strings.Repeat("-", barWidth-filled),
			int(percentage),
		)
	}
	lines = append(lines, bar)

	// Speed and progress
	speed := p.calculateSpeed()
	speedStr := formatBytesPerSecond(speed)
	progressStr := fmt.Sprintf("%s / %s", formatBytes(p.transferred), formatBytes(p.FileSize))

	if p.completed && p.verified {
		lines = append(lines, fmt.Sprintf("Speed: %s  |  %s", speedStr, progressStr))
		lines = append(lines, fmt.Sprintf("✓ Transfer complete. SHA-256 verified: %s", p.hashPrefix))
	} else {
		etaStr := p.calculateETA(speed)
		lines = append(lines, fmt.Sprintf("Speed: %s  |  Progress: %s", speedStr, progressStr))
		lines = append(lines, fmt.Sprintf("Time: %s remaining", etaStr))
	}

	lines = append(lines, FormatSeparator(50))
	return lines
}

// calculateSpeed computes transfer speed from the sliding window.
func (p *ProgressBar) calculateSpeed() float64 {
	if len(p.speedWindow) < 2 {
		return 0
	}
	first := p.speedWindow[0]
	last := p.speedWindow[len(p.speedWindow)-1]
	duration := last.time.Sub(first.time).Seconds()
	if duration <= 0 {
		return 0
	}
	return float64(last.bytes-first.bytes) / duration
}

// calculateETA estimates remaining time.
func (p *ProgressBar) calculateETA(speed float64) string {
	if speed <= 0 {
		return "calculating..."
	}
	remaining := float64(p.FileSize - p.transferred)
	seconds := remaining / speed

	if seconds < 60 {
		return fmt.Sprintf("%ds", int(seconds))
	} else if seconds < 3600 {
		return fmt.Sprintf("%dm%ds", int(seconds)/60, int(seconds)%60)
	}
	return fmt.Sprintf("%dh%dm", int(seconds)/3600, (int(seconds)%3600)/60)
}

// formatBytes converts bytes to human-readable string.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// formatBytesPerSecond converts speed to human-readable string.
func formatBytesPerSecond(bps float64) string {
	const (
		KB = 1024.0
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bps >= GB:
		return fmt.Sprintf("%.1f GB/s", bps/GB)
	case bps >= MB:
		return fmt.Sprintf("%.1f MB/s", bps/MB)
	case bps >= KB:
		return fmt.Sprintf("%.1f KB/s", bps/KB)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}
