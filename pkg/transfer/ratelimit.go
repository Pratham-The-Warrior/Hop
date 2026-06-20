package transfer

import (
	"fmt"
	"sync"
	"time"
)

// TokenBucketLimiter implements a token bucket rate limiter for bandwidth throttling.
// It allows bursts up to the bucket capacity while maintaining an average rate.
type TokenBucketLimiter struct {
	mu         sync.Mutex
	rate       float64   // tokens per second (bytes per second)
	capacity   float64   // max burst size in tokens (bytes)
	tokens     float64   // current available tokens
	lastRefill time.Time // last time tokens were added
}

// NewTokenBucketLimiter creates a rate limiter with the given bytes-per-second limit.
// The burst capacity is set to 2x the rate to allow small bursts while maintaining
// the average throughput.
func NewTokenBucketLimiter(bytesPerSecond int64) *TokenBucketLimiter {
	rate := float64(bytesPerSecond)
	return &TokenBucketLimiter{
		rate:       rate,
		capacity:   rate * 2, // Allow 2-second burst
		tokens:     rate,     // Start with 1 second of tokens
		lastRefill: time.Now(),
	}
}

// Wait blocks until n bytes are allowed to be sent/received.
// It consumes n tokens from the bucket, sleeping if necessary
// until enough tokens accumulate.
func (l *TokenBucketLimiter) Wait(n int) {
	for {
		l.mu.Lock()
		l.refill()

		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return
		}

		// Calculate how long we need to wait for enough tokens
		deficit := float64(n) - l.tokens
		waitTime := time.Duration(deficit / l.rate * float64(time.Second))
		l.mu.Unlock()

		// Sleep, then try again
		time.Sleep(waitTime)
	}
}

// TryConsume attempts to consume n tokens without blocking.
// Returns true if the tokens were available and consumed, false otherwise.
func (l *TokenBucketLimiter) TryConsume(n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()
	if l.tokens >= float64(n) {
		l.tokens -= float64(n)
		return true
	}
	return false
}

// refill adds tokens based on elapsed time since last refill. Must be called with mu held.
func (l *TokenBucketLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	l.tokens += elapsed * l.rate
	if l.tokens > l.capacity {
		l.tokens = l.capacity
	}
	l.lastRefill = now
}

// ParseBandwidthLimit parses a human-readable bandwidth limit string
// (e.g., "5MB/s", "100KB/s", "1GB/s") into bytes per second.
func ParseBandwidthLimit(limit string) (int64, error) {
	// Handle common formats
	var value float64
	var unit string

	n, _ := fmt.Sscanf(limit, "%f%s", &value, &unit)
	if n < 1 {
		return 0, fmt.Errorf("invalid bandwidth limit: %s", limit)
	}

	switch unit {
	case "B/s", "b/s":
		return int64(value), nil
	case "KB/s", "kb/s", "K/s", "k/s":
		return int64(value * 1024), nil
	case "MB/s", "mb/s", "M/s", "m/s":
		return int64(value * 1024 * 1024), nil
	case "GB/s", "gb/s", "G/s", "g/s":
		return int64(value * 1024 * 1024 * 1024), nil
	default:
		// If no unit, assume bytes/s
		return int64(value), nil
	}
}
