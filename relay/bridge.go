package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

const (
	// MaxBandwidthPerSession is the maximum bytes a single session can relay (10 GB).
	MaxBandwidthPerSession int64 = 10 * 1024 * 1024 * 1024

	// IdleTimeout is how long a session can go without data before being torn down.
	IdleTimeout = 5 * time.Minute

	// ReconnectWindow is how long the sender waits for a receiver to reconnect.
	ReconnectWindow = 60 * time.Second

	// MaxMessageSize is the max WebSocket message size (16 MB + framing overhead).
	MaxMessageSize = 17 * 1024 * 1024
)

// BridgeConn represents one side of a relay connection (sender or receiver).
type BridgeConn struct {
	conn        *websocket.Conn
	fingerprint string
	ip          string
	mu          sync.Mutex
}

// Send writes a binary message to the WebSocket connection.
func (bc *BridgeConn) Send(ctx context.Context, data []byte) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.conn.Write(ctx, websocket.MessageBinary, data)
}

// Receive reads a binary message from the WebSocket connection.
func (bc *BridgeConn) Receive(ctx context.Context) ([]byte, error) {
	_, data, err := bc.conn.Read(ctx)
	return data, err
}

// Close closes the WebSocket connection with a normal closure status.
func (bc *BridgeConn) Close() {
	bc.conn.Close(websocket.StatusNormalClosure, "session ended")
}

// Bridge manages the WebSocket upgrade and bidirectional data streaming
// between senders and receivers.
type Bridge struct {
	auth     *Authenticator
	registry *Registry
	limiter  *RateLimiter
	logger   *log.Logger
}

// NewBridge creates a new Bridge with the given dependencies.
func NewBridge(auth *Authenticator, registry *Registry, limiter *RateLimiter, logger *log.Logger) *Bridge {
	return &Bridge{
		auth:     auth,
		registry: registry,
		limiter:  limiter,
		logger:   logger,
	}
}

// WSRequest is the initial JSON message sent by a client after WebSocket upgrade.
type WSRequest struct {
	SessionToken string `json:"session_token"` // JWT from /auth
	Action       string `json:"action"`        // "register" or "join"
	Token        string `json:"token"`         // Transfer token (word-word-NN)
}

// WSResponse is sent back to the client after processing the initial request.
type WSResponse struct {
	Status  string `json:"status"`           // "ok" or "error"
	Message string `json:"message,omitempty"` // Human-readable status
}

// HandleWebSocket handles the WebSocket upgrade and initial handshake.
func (b *Bridge) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	ip := extractIP(r)

	// Check rate limit for connections
	if !b.limiter.AllowConnection(ip) {
		http.Error(w, "too many connections from your IP", http.StatusTooManyRequests)
		return
	}

	// Accept WebSocket upgrade
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow all origins for now; tighten in production
		InsecureSkipVerify: true,
	})
	if err != nil {
		b.limiter.ReleaseConnection(ip)
		b.logger.Printf("[bridge] WebSocket upgrade failed from %s: %v", ip, err)
		return
	}

	conn.SetReadLimit(MaxMessageSize)

	// Read the initial handshake message (WSRequest)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		b.limiter.ReleaseConnection(ip)
		conn.Close(websocket.StatusProtocolError, "failed to read handshake")
		b.logger.Printf("[bridge] handshake read failed from %s: %v", ip, err)
		return
	}

	var req WSRequest
	if err := json.Unmarshal(data, &req); err != nil {
		b.limiter.ReleaseConnection(ip)
		conn.Close(websocket.StatusProtocolError, "invalid handshake JSON")
		b.logger.Printf("[bridge] invalid handshake from %s: %v", ip, err)
		return
	}

	// Validate the session token
	session, err := b.auth.ValidateToken(req.SessionToken)
	if err != nil {
		b.limiter.ReleaseConnection(ip)
		sendWSResponse(conn, "error", fmt.Sprintf("authentication failed: %v", err))
		conn.Close(websocket.StatusPolicyViolation, "authentication failed")
		b.logger.Printf("[bridge] auth failed from %s: %v", ip, err)
		return
	}

	bc := &BridgeConn{
		conn:        conn,
		fingerprint: session.Fingerprint,
		ip:          ip,
	}

	// Route based on action
	switch req.Action {
	case "register":
		b.handleRegister(r.Context(), bc, req.Token)
	case "join":
		b.handleJoin(r.Context(), bc, req.Token)
	default:
		sendWSResponse(conn, "error", fmt.Sprintf("unknown action: %s", req.Action))
		conn.Close(websocket.StatusProtocolError, "unknown action")
		b.limiter.ReleaseConnection(ip)
	}
}

// handleRegister processes a sender registering a transfer token.
func (b *Bridge) handleRegister(ctx context.Context, sender *BridgeConn, token string) {
	defer b.limiter.ReleaseConnection(sender.ip)

	// Register the token
	entry, err := b.registry.Register(token, sender.fingerprint, sender)
	if err != nil {
		sendWSResponse(sender.conn, "error", err.Error())
		sender.Close()
		b.logger.Printf("[bridge] register failed for token %s: %v", token, err)
		return
	}

	// Enable browser mode — all tokens support browser downloads via hop.to links.
	// This allows the BrowserBridge to communicate with the sender via their WebSocket.
	b.registry.SetBrowserMode(token)

	b.logger.Printf("[bridge] token %s registered by %s (browser mode enabled)", token, sender.ip)
	sendWSResponse(sender.conn, "ok", "token registered — waiting for receiver")

	// Wait for a receiver to join, or for timeout/cancellation
	select {
	case <-entry.Paired:
		b.logger.Printf("[bridge] token %s paired — starting bridge", token)
		// Receiver has joined, start the bidirectional bridge
		b.streamData(ctx, entry)

	case <-entry.Done:
		b.logger.Printf("[bridge] token %s cancelled before pairing", token)
		sender.Close()
		return

	case <-ctx.Done():
		b.logger.Printf("[bridge] token %s — sender context cancelled", token)
		b.registry.Unregister(token)
		sender.Close()
		return
	}
}

// handleJoin processes a receiver joining an existing transfer token.
func (b *Bridge) handleJoin(ctx context.Context, receiver *BridgeConn, token string) {
	defer b.limiter.ReleaseConnection(receiver.ip)

	// Check lookup rate limit
	if !b.limiter.AllowLookup(receiver.ip) {
		sendWSResponse(receiver.conn, "error", "rate limit exceeded — too many lookup attempts")
		receiver.Close()
		return
	}

	// Join the token
	entry, err := b.registry.Join(token, receiver)
	if err != nil {
		sendWSResponse(receiver.conn, "error", err.Error())
		receiver.Close()
		b.logger.Printf("[bridge] join failed for token %s from %s: %v", token, receiver.ip, err)
		return
	}

	b.logger.Printf("[bridge] receiver %s joined token %s", receiver.ip, token)
	sendWSResponse(receiver.conn, "ok", "joined transfer — bridge active")

	// The sender's goroutine handles the actual streaming via streamData.
	// The receiver just waits for the transfer to complete.
	select {
	case <-entry.Done:
		return
	case <-ctx.Done():
		return
	}
}

// streamData creates the bidirectional pipe between sender and receiver.
// It runs two goroutines: sender→receiver and receiver→sender.
func (b *Bridge) streamData(parentCtx context.Context, entry *RegistryEntry) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	defer b.registry.Unregister(entry.Token)
	defer entry.Sender.Close()
	defer entry.Receiver.Close()

	var totalBytes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)

	// Sender → Receiver pipe
	go func() {
		defer wg.Done()
		b.pipe(ctx, cancel, entry.Sender, entry.Receiver, &totalBytes, "sender→receiver", entry.Token)
	}()

	// Receiver → Sender pipe
	go func() {
		defer wg.Done()
		b.pipe(ctx, cancel, entry.Receiver, entry.Sender, &totalBytes, "receiver→sender", entry.Token)
	}()

	wg.Wait()
	b.logger.Printf("[bridge] token %s — bridge closed (total relayed: %d bytes)", entry.Token, totalBytes.Load())
}

// pipe reads from src and writes to dst, enforcing bandwidth caps and idle timeouts.
func (b *Bridge) pipe(ctx context.Context, cancel context.CancelFunc, src, dst *BridgeConn, totalBytes *atomic.Int64, direction, token string) {
	defer cancel()

	for {
		// Set idle timeout per read
		readCtx, readCancel := context.WithTimeout(ctx, IdleTimeout)
		data, err := src.Receive(readCtx)
		readCancel()

		if err != nil {
			// Check if it's a normal closure or context cancellation
			if ctx.Err() != nil {
				return
			}
			b.logger.Printf("[bridge] %s %s — read error: %v", token, direction, err)
			return
		}

		// Enforce bandwidth cap
		newTotal := totalBytes.Add(int64(len(data)))
		if newTotal > MaxBandwidthPerSession {
			b.logger.Printf("[bridge] %s — bandwidth cap exceeded (%d bytes)", token, newTotal)
			sendWSResponse(src.conn, "error", "bandwidth cap exceeded (10 GB limit)")
			return
		}

		// Forward to destination
		writeCtx, writeCancel := context.WithTimeout(ctx, 30*time.Second)
		err = dst.Send(writeCtx, data)
		writeCancel()

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.logger.Printf("[bridge] %s %s — write error: %v", token, direction, err)
			return
		}
	}
}

// sendWSResponse sends a JSON response over the WebSocket connection.
func sendWSResponse(conn *websocket.Conn, status, message string) {
	resp := WSResponse{
		Status:  status,
		Message: message,
	}
	data, _ := json.Marshal(resp)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn.Write(ctx, websocket.MessageText, data)
}
