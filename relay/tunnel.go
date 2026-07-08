package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

const (
	// TunnelSlugCooldown is how long a slug is reserved after the tunnel disconnects.
	TunnelSlugCooldown = 30 * time.Second

	// TunnelRequestTimeout is the max time to wait for a tunnel client to respond.
	TunnelRequestTimeout = 30 * time.Second

	// TunnelMaxRequestBody is the max body size for incoming public HTTP requests (100 MB).
	TunnelMaxRequestBody int64 = 100 * 1024 * 1024
)

// tunnelSession represents an active tunnel connection from a CLI client.
type tunnelSession struct {
	mu           sync.Mutex
	slug         string
	conn         *websocket.Conn
	passwordHash string // bcrypt hash (empty if no password)
	localPort    uint16
	ip           string
	createdAt    time.Time
	activePipes  atomic.Int32
	nextReqID    atomic.Uint32
	pendingReqs  sync.Map // requestID → chan *protocol.TunnelHTTPResponse
}

// TunnelServer manages tunnel registrations and proxies public HTTP traffic
// to connected CLI clients.
type TunnelServer struct {
	mu       sync.RWMutex
	tunnels  map[string]*tunnelSession // slug → session
	cooldown map[string]time.Time      // slug → when it becomes available
	auth     *Authenticator
	limiter  *RateLimiter
	logger   *log.Logger
}

// NewTunnelServer creates a new tunnel server.
func NewTunnelServer(auth *Authenticator, limiter *RateLimiter, logger *log.Logger) *TunnelServer {
	ts := &TunnelServer{
		tunnels:  make(map[string]*tunnelSession),
		cooldown: make(map[string]time.Time),
		auth:     auth,
		limiter:  limiter,
		logger:   logger,
	}
	go ts.cleanupLoop()
	return ts
}

// HandleTunnel handles the WebSocket connection from a tunnel CLI client.
// This is the endpoint at /tunnel.
func (ts *TunnelServer) HandleTunnel(w http.ResponseWriter, r *http.Request) {
	ip := extractIP(r)

	if !ts.limiter.AllowConnection(ip) {
		http.Error(w, "too many connections from your IP", http.StatusTooManyRequests)
		return
	}

	// WebSocket upgrade
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		ts.limiter.ReleaseConnection(ip)
		ts.logger.Printf("[tunnel] WebSocket upgrade failed from %s: %v", ip, err)
		return
	}
	conn.SetReadLimit(MaxMessageSize)

	// Read the initial handshake (JSON: session_token + action + token)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		ts.limiter.ReleaseConnection(ip)
		conn.Close(websocket.StatusProtocolError, "failed to read handshake")
		return
	}

	var req WSRequest
	if err := json.Unmarshal(data, &req); err != nil {
		ts.limiter.ReleaseConnection(ip)
		conn.Close(websocket.StatusProtocolError, "invalid handshake")
		return
	}

	// Validate session token
	_, err = ts.auth.ValidateToken(req.SessionToken)
	if err != nil {
		ts.limiter.ReleaseConnection(ip)
		sendWSResponse(conn, "error", fmt.Sprintf("authentication failed: %v", err))
		conn.Close(websocket.StatusPolicyViolation, "auth failed")
		return
	}

	if req.Action != "register_tunnel" {
		ts.limiter.ReleaseConnection(ip)
		sendWSResponse(conn, "error", "expected action: register_tunnel")
		conn.Close(websocket.StatusProtocolError, "wrong action")
		return
	}

	sendWSResponse(conn, "ok", "tunnel handshake accepted")

	// Read the tunnel registration message
	ts.handleRegistration(r.Context(), conn, ip)
}

// handleRegistration processes the tunnel registration after the WebSocket handshake.
func (ts *TunnelServer) handleRegistration(ctx context.Context, conn *websocket.Conn, ip string) {
	defer ts.limiter.ReleaseConnection(ip)

	// Read the TUNNEL_REGISTER protocol message
	_, data, err := conn.Read(ctx)
	if err != nil {
		ts.logger.Printf("[tunnel] failed to read registration from %s: %v", ip, err)
		conn.Close(websocket.StatusProtocolError, "registration read error")
		return
	}

	// Parse the wire-format message
	if len(data) < protocol.HeaderSize {
		conn.Close(websocket.StatusProtocolError, "message too short")
		return
	}
	msgType := protocol.MessageType(data[4])
	if msgType != protocol.MsgTunnelRegister {
		ts.logger.Printf("[tunnel] expected TUNNEL_REGISTER, got %s", msgType)
		conn.Close(websocket.StatusProtocolError, "expected TUNNEL_REGISTER")
		return
	}

	payload := data[protocol.HeaderSize:]
	reg, err := protocol.DecodeTunnelRegister(payload)
	if err != nil {
		ts.logger.Printf("[tunnel] invalid tunnel register payload: %v", err)
		conn.Close(websocket.StatusProtocolError, "invalid registration")
		return
	}

	// Check if slug is available
	ts.mu.Lock()
	if _, exists := ts.tunnels[reg.Slug]; exists {
		ts.mu.Unlock()
		ts.logger.Printf("[tunnel] slug %q already in use", reg.Slug)
		sendWSResponse(conn, "error", "slug already in use")
		conn.Close(websocket.StatusPolicyViolation, "slug taken")
		return
	}

	// Check cooldown
	if cooldownExpiry, inCooldown := ts.cooldown[reg.Slug]; inCooldown {
		if time.Now().Before(cooldownExpiry) {
			ts.mu.Unlock()
			sendWSResponse(conn, "error", "slug is in cooldown, try again shortly")
			conn.Close(websocket.StatusPolicyViolation, "slug cooldown")
			return
		}
		delete(ts.cooldown, reg.Slug)
	}

	session := &tunnelSession{
		slug:         reg.Slug,
		conn:         conn,
		passwordHash: reg.PasswordHash,
		localPort:    reg.LocalPort,
		ip:           ip,
		createdAt:    time.Now(),
	}
	ts.tunnels[reg.Slug] = session
	ts.mu.Unlock()

	ts.logger.Printf("[tunnel] registered slug %q from %s (port %d, password: %v)",
		reg.Slug, ip, reg.LocalPort, reg.PasswordHash != "")

	// Send confirmation with public URL
	publicURL := fmt.Sprintf("https://hop.to/t/%s", reg.Slug)
	registered := &protocol.TunnelRegistered{
		PublicURL: publicURL,
	}
	respMsg := &protocol.Message{
		Type:    protocol.MsgTunnelResponse,
		Payload: protocol.EncodeTunnelRegistered(registered),
	}
	respData := protocol.Encode(respMsg)
	writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
	conn.Write(writeCtx, websocket.MessageBinary, respData)
	writeCancel()

	// Enter the tunnel event loop — read responses from the client
	ts.tunnelLoop(ctx, session)

	// Cleanup on disconnect
	ts.mu.Lock()
	delete(ts.tunnels, reg.Slug)
	ts.cooldown[reg.Slug] = time.Now().Add(TunnelSlugCooldown)
	ts.mu.Unlock()

	ts.logger.Printf("[tunnel] slug %q disconnected (30s cooldown)", reg.Slug)
}

// tunnelLoop reads responses from the tunnel client and routes them to the
// appropriate pending request channels.
func (ts *TunnelServer) tunnelLoop(ctx context.Context, session *tunnelSession) {
	for {
		_, data, err := session.conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			ts.logger.Printf("[tunnel] %s — read error: %v", session.slug, err)
			return
		}

		if len(data) < protocol.HeaderSize {
			continue
		}

		msgType := protocol.MessageType(data[4])

		switch msgType {
		case protocol.MsgTunnelResponse:
			payload := data[protocol.HeaderSize:]
			resp, err := protocol.DecodeTunnelHTTPResponse(payload)
			if err != nil {
				ts.logger.Printf("[tunnel] %s — invalid response: %v", session.slug, err)
				continue
			}

			// Route the response to the pending request
			if ch, ok := session.pendingReqs.LoadAndDelete(resp.RequestID); ok {
				ch.(chan *protocol.TunnelHTTPResponse) <- resp
			}

		case protocol.MsgTunnelClose:
			ts.logger.Printf("[tunnel] %s — client sent TUNNEL_CLOSE", session.slug)
			return

		default:
			ts.logger.Printf("[tunnel] %s — unexpected message: %s", session.slug, msgType)
		}
	}
}

// HandlePublicRequest proxies an incoming public HTTP request to the tunnel client.
func (ts *TunnelServer) HandlePublicRequest(w http.ResponseWriter, r *http.Request, slug, subpath string) {
	ts.mu.RLock()
	session, exists := ts.tunnels[slug]
	ts.mu.RUnlock()

	if !exists {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}

	// Check password protection
	if session.passwordHash != "" {
		if !ts.checkPassword(w, r, session) {
			return
		}
	}

	// Read the incoming request body (capped at 100 MB)
	body, err := io.ReadAll(io.LimitReader(r.Body, TunnelMaxRequestBody))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	// Flatten request headers
	headers := make(map[string]string)
	for k, vals := range r.Header {
		headers[k] = strings.Join(vals, ", ")
	}
	// Add the original host
	headers["X-Forwarded-Host"] = r.Host
	headers["X-Forwarded-Proto"] = "https"

	// Build the path
	path := "/" + subpath
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}

	// Assign a request ID
	reqID := session.nextReqID.Add(1)

	tunnelReq := &protocol.TunnelHTTPRequest{
		RequestID: reqID,
		Method:    r.Method,
		Path:      path,
		Headers:   headers,
		Body:      body,
	}

	// Create a channel to receive the response
	respCh := make(chan *protocol.TunnelHTTPResponse, 1)
	session.pendingReqs.Store(reqID, respCh)
	defer session.pendingReqs.Delete(reqID)

	session.activePipes.Add(1)
	defer session.activePipes.Add(-1)

	// Send the request to the tunnel client
	reqMsg := &protocol.Message{
		Type:    protocol.MsgTunnelRequest,
		Payload: protocol.EncodeTunnelHTTPRequest(tunnelReq),
	}
	reqData := protocol.Encode(reqMsg)

	session.mu.Lock()
	writeCtx, writeCancel := context.WithTimeout(r.Context(), 5*time.Second)
	err = session.conn.Write(writeCtx, websocket.MessageBinary, reqData)
	writeCancel()
	session.mu.Unlock()

	if err != nil {
		ts.logger.Printf("[tunnel] %s — failed to forward request: %v", slug, err)
		http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		return
	}

	// Wait for the response from the tunnel client
	select {
	case resp := <-respCh:
		// Write response headers
		for k, v := range resp.Headers {
			// Skip hop-by-hop headers
			lower := strings.ToLower(k)
			if lower == "transfer-encoding" || lower == "connection" {
				continue
			}
			w.Header().Set(k, v)
		}
		w.WriteHeader(int(resp.StatusCode))
		w.Write(resp.Body)

	case <-time.After(TunnelRequestTimeout):
		ts.logger.Printf("[tunnel] %s — request %d timed out", slug, reqID)
		http.Error(w, "504 Gateway Timeout", http.StatusGatewayTimeout)

	case <-r.Context().Done():
		// Client disconnected
		return
	}
}

// checkPassword validates the password for a password-protected tunnel.
// Returns true if the password is correct or has been submitted.
func (ts *TunnelServer) checkPassword(w http.ResponseWriter, r *http.Request, session *tunnelSession) bool {
	// Check for password in cookie
	cookie, err := r.Cookie("hop_tunnel_auth")
	if err == nil && cookie.Value == session.passwordHash {
		return true
	}

	// Check for password in POST form
	if r.Method == http.MethodPost && r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		r.ParseForm()
		password := r.FormValue("password")
		if password != "" {
			// Verify against bcrypt hash
			// We compare the raw password hash here — the client sends bcrypt hash,
			// so the visitor needs to enter the original password which we then hash and compare.
			// For simplicity, we use a plaintext comparison with the stored hash as a session cookie.
			// In production, this would use bcrypt.CompareHashAndPassword.
			if verifyTunnelPassword(password, session.passwordHash) {
				// Set auth cookie so they don't have to re-enter
				http.SetCookie(w, &http.Cookie{
					Name:     "hop_tunnel_auth",
					Value:    session.passwordHash,
					Path:     "/t/" + session.slug,
					HttpOnly: true,
					Secure:   true,
					MaxAge:   86400, // 24 hours
				})
				// Redirect to GET to avoid form resubmission
				http.Redirect(w, r, r.URL.String(), http.StatusSeeOther)
				return false // Will be handled on the redirect
			}
		}
	}

	// Serve the password prompt page
	ts.servePasswordPage(w, session.slug, r.URL.Path)
	return false
}

// verifyTunnelPassword verifies a plaintext password against a bcrypt hash.
func verifyTunnelPassword(password, hash string) bool {
	// Import bcrypt at the call site to match the CLI's hashing
	// For now, we use golang.org/x/crypto/bcrypt
	// This is imported via the existing dependency
	err := bcryptCompare(hash, password)
	return err == nil
}

// servePasswordPage serves a styled HTML password prompt.
func (ts *TunnelServer) servePasswordPage(w http.ResponseWriter, slug, path string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>hop — Password Required</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #0a0a0a;
            color: #e0e0e0;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
        }
        .card {
            background: #1a1a1a;
            border: 1px solid #333;
            border-radius: 12px;
            padding: 40px;
            max-width: 400px;
            width: 90%;
            text-align: center;
        }
        .logo { font-size: 2em; font-weight: 700; color: #7c3aed; margin-bottom: 8px; }
        .subtitle { color: #888; font-size: 0.9em; margin-bottom: 24px; }
        .lock-icon { font-size: 3em; margin-bottom: 16px; }
        h2 { font-size: 1.2em; margin-bottom: 8px; color: #fff; }
        p { color: #999; font-size: 0.85em; margin-bottom: 24px; }
        form { display: flex; flex-direction: column; gap: 12px; }
        input[type="password"] {
            padding: 12px 16px;
            border-radius: 8px;
            border: 1px solid #333;
            background: #111;
            color: #fff;
            font-size: 1em;
            outline: none;
            transition: border-color 0.2s;
        }
        input[type="password"]:focus { border-color: #7c3aed; }
        button {
            padding: 12px;
            border-radius: 8px;
            border: none;
            background: #7c3aed;
            color: #fff;
            font-size: 1em;
            font-weight: 600;
            cursor: pointer;
            transition: background 0.2s;
        }
        button:hover { background: #6d28d9; }
        .slug { color: #7c3aed; font-family: monospace; }
    </style>
</head>
<body>
    <div class="card">
        <div class="logo">hop</div>
        <div class="subtitle">Secure Localhost Tunnel</div>
        <div class="lock-icon">🔒</div>
        <h2>Password Required</h2>
        <p>This tunnel (<span class="slug">` + slug + `</span>) is password-protected.</p>
        <form method="POST" action="` + path + `">
            <input type="password" name="password" placeholder="Enter password" autofocus required>
            <button type="submit">Access Tunnel</button>
        </form>
    </div>
</body>
</html>`
	fmt.Fprint(w, html)
}

// ActiveTunnelCount returns the number of active tunnels.
func (ts *TunnelServer) ActiveTunnelCount() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.tunnels)
}

// cleanupLoop periodically removes expired cooldowns.
func (ts *TunnelServer) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		ts.mu.Lock()
		now := time.Now()
		for slug, expiry := range ts.cooldown {
			if now.After(expiry) {
				delete(ts.cooldown, slug)
			}
		}
		ts.mu.Unlock()
	}
}
