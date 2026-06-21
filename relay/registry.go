package main

import (
	"fmt"
	"sync"
	"time"
)

// Registry manages the mapping of transfer tokens to active relay sessions.
// It is the central lookup table that connects senders to receivers.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]*RegistryEntry
	stopCh   chan struct{}
}

// RegistryEntry represents a registered token with its associated session info.
type RegistryEntry struct {
	Token       string
	Sender      *BridgeConn       // The sender's bridge connection (set when sender connects)
	Receiver    *BridgeConn       // The receiver's bridge connection (set when receiver joins)
	Fingerprint string            // Sender's session fingerprint
	CreatedAt   time.Time
	ExpiresAt   time.Time         // 24h automatic expiry
	Paired      chan struct{}      // Closed when receiver joins (signals sender)
	Done        chan struct{}      // Closed when transfer completes or is cancelled
}

// NewRegistry creates a new token registry with background expiry cleanup.
func NewRegistry() *Registry {
	r := &Registry{
		entries: make(map[string]*RegistryEntry),
		stopCh:  make(chan struct{}),
	}
	go r.cleanupLoop()
	return r
}

// Register adds a token to the registry, associating it with the sender's session.
// Returns an error if the token is already registered.
func (r *Registry) Register(token, fingerprint string, sender *BridgeConn) (*RegistryEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[token]; exists {
		return nil, fmt.Errorf("token %q already registered", token)
	}

	entry := &RegistryEntry{
		Token:       token,
		Sender:      sender,
		Fingerprint: fingerprint,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		Paired:      make(chan struct{}),
		Done:        make(chan struct{}),
	}

	r.entries[token] = entry
	return entry, nil
}

// Lookup finds a registered token entry. Returns nil if not found or expired.
func (r *Registry) Lookup(token string) *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.entries[token]
	if !exists {
		return nil
	}

	// Check if expired
	if time.Now().After(entry.ExpiresAt) {
		return nil
	}

	return entry
}

// Join associates a receiver with an existing token entry.
// Returns an error if the token doesn't exist or already has a receiver.
func (r *Registry) Join(token string, receiver *BridgeConn) (*RegistryEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.entries[token]
	if !exists {
		return nil, fmt.Errorf("token %q not found", token)
	}

	if time.Now().After(entry.ExpiresAt) {
		delete(r.entries, token)
		return nil, fmt.Errorf("token %q has expired", token)
	}

	if entry.Receiver != nil {
		return nil, fmt.Errorf("token %q already has a receiver", token)
	}

	entry.Receiver = receiver

	// Signal the sender that a receiver has joined
	select {
	case <-entry.Paired:
		// Already closed (shouldn't happen, but be safe)
	default:
		close(entry.Paired)
	}

	return entry, nil
}

// Unregister removes a token from the registry and signals completion.
func (r *Registry) Unregister(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.entries[token]
	if exists {
		// Signal any waiting goroutines that this entry is done
		select {
		case <-entry.Done:
		default:
			close(entry.Done)
		}
		delete(r.entries, token)
	}
}

// Count returns the number of active tokens in the registry.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Stop shuts down the background cleanup goroutine.
func (r *Registry) Stop() {
	select {
	case <-r.stopCh:
	default:
		close(r.stopCh)
	}
}

// cleanupLoop periodically removes expired entries from the registry.
func (r *Registry) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.cleanupExpired()
		case <-r.stopCh:
			return
		}
	}
}

// cleanupExpired removes all expired entries.
func (r *Registry) cleanupExpired() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	removed := 0
	for token, entry := range r.entries {
		if now.After(entry.ExpiresAt) {
			select {
			case <-entry.Done:
			default:
				close(entry.Done)
			}
			delete(r.entries, token)
			removed++
		}
	}
	return removed
}
