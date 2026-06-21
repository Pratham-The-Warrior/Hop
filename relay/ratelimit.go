package main

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter enforces per-IP rate limits and abuse prevention controls
// per spec Section 7.2.
type RateLimiter struct {
	mu sync.RWMutex

	// Per-IP connection tracking
	connections map[string]*ipState

	// Per-IP token lookup tracking
	lookups map[string]*lookupState

	// Per-IP ban list
	bans map[string]time.Time

	// Limits
	maxConnsPerIP     int           // Max concurrent transfers per IP (default: 5)
	maxLookupsPerMin  int           // Max token lookups per minute per IP (default: 10)
	banDuration       time.Duration // Ban duration on excess lookups (default: 5min)

	stopCh chan struct{}
}

// ipState tracks per-IP connection counts.
type ipState struct {
	active int
}

// lookupState tracks per-IP token lookup rate.
type lookupState struct {
	count    int
	windowStart time.Time
}

// NewRateLimiter creates a new rate limiter with default limits from the spec.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		connections:      make(map[string]*ipState),
		lookups:          make(map[string]*lookupState),
		bans:             make(map[string]time.Time),
		maxConnsPerIP:    5,
		maxLookupsPerMin: 10,
		banDuration:      5 * time.Minute,
		stopCh:           make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// AllowConnection checks if a new connection from the given IP is allowed.
// Returns true if the IP is under the connection limit and not banned.
func (rl *RateLimiter) AllowConnection(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Check ban list
	if banExpiry, banned := rl.bans[ip]; banned {
		if time.Now().Before(banExpiry) {
			return false
		}
		// Ban expired, remove it
		delete(rl.bans, ip)
	}

	state, exists := rl.connections[ip]
	if !exists {
		rl.connections[ip] = &ipState{active: 1}
		return true
	}

	if state.active >= rl.maxConnsPerIP {
		return false
	}

	state.active++
	return true
}

// ReleaseConnection decrements the connection count for an IP.
func (rl *RateLimiter) ReleaseConnection(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	state, exists := rl.connections[ip]
	if !exists {
		return
	}

	state.active--
	if state.active <= 0 {
		delete(rl.connections, ip)
	}
}

// AllowLookup checks if a token lookup from the given IP is allowed.
// Enforces 10 lookups per minute; exceeding triggers a 5-minute ban.
func (rl *RateLimiter) AllowLookup(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Check ban list
	if banExpiry, banned := rl.bans[ip]; banned {
		if time.Now().Before(banExpiry) {
			return false
		}
		delete(rl.bans, ip)
	}

	now := time.Now()
	state, exists := rl.lookups[ip]
	if !exists {
		rl.lookups[ip] = &lookupState{
			count:       1,
			windowStart: now,
		}
		return true
	}

	// Reset window if a minute has passed
	if now.Sub(state.windowStart) > time.Minute {
		state.count = 1
		state.windowStart = now
		return true
	}

	state.count++
	if state.count > rl.maxLookupsPerMin {
		// Ban this IP for 5 minutes
		rl.bans[ip] = now.Add(rl.banDuration)
		return false
	}

	return true
}

// IsBanned checks if an IP is currently banned.
func (rl *RateLimiter) IsBanned(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	banExpiry, banned := rl.bans[ip]
	if !banned {
		return false
	}
	return time.Now().Before(banExpiry)
}

// ActiveConnections returns the number of active connections for an IP.
func (rl *RateLimiter) ActiveConnections(ip string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	state, exists := rl.connections[ip]
	if !exists {
		return 0
	}
	return state.active
}

// Stop shuts down the cleanup goroutine.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.stopCh:
	default:
		close(rl.stopCh)
	}
}

// RateLimitMiddleware wraps an HTTP handler with rate limiting checks.
func (rl *RateLimiter) RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)

		if rl.IsBanned(ip) {
			http.Error(w, "rate limit exceeded — temporarily banned", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// cleanupLoop periodically removes stale entries.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCh:
			return
		}
	}
}

// cleanup removes expired bans and stale lookup windows.
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Clean expired bans
	for ip, expiry := range rl.bans {
		if now.After(expiry) {
			delete(rl.bans, ip)
		}
	}

	// Clean stale lookup windows (older than 2 minutes)
	for ip, state := range rl.lookups {
		if now.Sub(state.windowStart) > 2*time.Minute {
			delete(rl.lookups, ip)
		}
	}
}

// extractIP gets the client IP from a request, checking X-Forwarded-For
// for reverse proxy setups.
func extractIP(r *http.Request) string {
	// Check X-Forwarded-For header (set by reverse proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain (original client)
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr (strip port)
	addr := r.RemoteAddr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
