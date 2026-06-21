package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// --- Auth Tests ---

func TestAuthenticator_ValidAuth(t *testing.T) {
	auth, err := NewAuthenticator()
	if err != nil {
		t.Fatalf("NewAuthenticator failed: %v", err)
	}

	// Generate an Ed25519 keypair (client-side)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	// Build request
	reqBody := AuthRequest{
		PublicKey:       []byte(pub),
		ProtocolVersion: "HOP/1.0",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.HandleAuth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AuthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.SessionToken == "" {
		t.Fatal("expected non-empty session token")
	}

	if resp.ExpiresAt == 0 {
		t.Fatal("expected non-zero expiry")
	}

	// Verify the token is valid
	session, err := auth.ValidateToken(resp.SessionToken)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	if session.Fingerprint == "" {
		t.Fatal("expected non-empty fingerprint")
	}

	// Verify session count
	if auth.SessionCount() != 1 {
		t.Fatalf("expected 1 session, got %d", auth.SessionCount())
	}
}

func TestAuthenticator_InvalidPublicKey(t *testing.T) {
	auth, _ := NewAuthenticator()

	reqBody := AuthRequest{
		PublicKey:       []byte("too-short"),
		ProtocolVersion: "HOP/1.0",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	auth.HandleAuth(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAuthenticator_WrongMethod(t *testing.T) {
	auth, _ := NewAuthenticator()

	req := httptest.NewRequest(http.MethodGet, "/auth", nil)
	w := httptest.NewRecorder()

	auth.HandleAuth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAuthenticator_InvalidToken(t *testing.T) {
	auth, _ := NewAuthenticator()

	_, err := auth.ValidateToken("invalid.jwt.token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestAuthenticator_CleanupExpired(t *testing.T) {
	auth, _ := NewAuthenticator()

	// Manually insert an expired session
	auth.mu.Lock()
	auth.sessions["expired-fp"] = &Session{
		Fingerprint: "expired-fp",
		CreatedAt:   time.Now().Add(-25 * time.Hour),
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
	}
	auth.mu.Unlock()

	if auth.SessionCount() != 1 {
		t.Fatalf("expected 1 session, got %d", auth.SessionCount())
	}

	removed := auth.CleanupExpired()
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	if auth.SessionCount() != 0 {
		t.Fatalf("expected 0 sessions, got %d", auth.SessionCount())
	}
}

// --- Registry Tests ---

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewRegistry()
	defer reg.Stop()

	sender := &BridgeConn{fingerprint: "sender-fp"}
	entry, err := reg.Register("test-token-01", "sender-fp", sender)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if entry.Token != "test-token-01" {
		t.Fatalf("expected token 'test-token-01', got '%s'", entry.Token)
	}

	// Lookup should find it
	found := reg.Lookup("test-token-01")
	if found == nil {
		t.Fatal("expected to find token")
	}

	// Non-existent token should return nil
	notFound := reg.Lookup("nonexistent-00")
	if notFound != nil {
		t.Fatal("expected nil for nonexistent token")
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	reg := NewRegistry()
	defer reg.Stop()

	sender := &BridgeConn{fingerprint: "sender-fp"}
	_, err := reg.Register("dupe-token-01", "sender-fp", sender)
	if err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	_, err = reg.Register("dupe-token-01", "sender-fp", sender)
	if err == nil {
		t.Fatal("expected error for duplicate token registration")
	}
}

func TestRegistry_JoinAndPairing(t *testing.T) {
	reg := NewRegistry()
	defer reg.Stop()

	sender := &BridgeConn{fingerprint: "sender-fp"}
	entry, _ := reg.Register("join-test-01", "sender-fp", sender)

	// Start a goroutine waiting for pairing
	paired := make(chan bool, 1)
	go func() {
		select {
		case <-entry.Paired:
			paired <- true
		case <-time.After(2 * time.Second):
			paired <- false
		}
	}()

	// Simulate receiver joining
	receiver := &BridgeConn{fingerprint: "receiver-fp"}
	_, err := reg.Join("join-test-01", receiver)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	// Verify pairing signal was received
	if !<-paired {
		t.Fatal("pairing signal not received within timeout")
	}
}

func TestRegistry_JoinNonexistent(t *testing.T) {
	reg := NewRegistry()
	defer reg.Stop()

	receiver := &BridgeConn{fingerprint: "receiver-fp"}
	_, err := reg.Join("nonexistent-00", receiver)
	if err == nil {
		t.Fatal("expected error for joining nonexistent token")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewRegistry()
	defer reg.Stop()

	sender := &BridgeConn{fingerprint: "sender-fp"}
	reg.Register("unreg-test-01", "sender-fp", sender)

	if reg.Count() != 1 {
		t.Fatalf("expected 1 token, got %d", reg.Count())
	}

	reg.Unregister("unreg-test-01")

	if reg.Count() != 0 {
		t.Fatalf("expected 0 tokens, got %d", reg.Count())
	}

	// Lookup should return nil
	if reg.Lookup("unreg-test-01") != nil {
		t.Fatal("expected nil after unregister")
	}
}

// --- Rate Limiter Tests ---

func TestRateLimiter_ConnectionLimit(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	ip := "192.168.1.100"

	// Should allow up to 5 connections
	for i := 0; i < 5; i++ {
		if !rl.AllowConnection(ip) {
			t.Fatalf("connection %d should be allowed", i+1)
		}
	}

	// 6th connection should be rejected
	if rl.AllowConnection(ip) {
		t.Fatal("6th connection should be rejected")
	}

	// Release one connection
	rl.ReleaseConnection(ip)

	// Now another should be allowed
	if !rl.AllowConnection(ip) {
		t.Fatal("connection after release should be allowed")
	}
}

func TestRateLimiter_LookupRateLimit(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	ip := "10.0.0.1"

	// Should allow 10 lookups
	for i := 0; i < 10; i++ {
		if !rl.AllowLookup(ip) {
			t.Fatalf("lookup %d should be allowed", i+1)
		}
	}

	// 11th lookup should be rejected AND trigger a ban
	if rl.AllowLookup(ip) {
		t.Fatal("11th lookup should be rejected")
	}

	// Verify ban is active
	if !rl.IsBanned(ip) {
		t.Fatal("IP should be banned after exceeding lookup rate")
	}

	// Connections should also be blocked during ban
	if rl.AllowConnection(ip) {
		t.Fatal("connections should be blocked during ban")
	}
}

func TestRateLimiter_DifferentIPs(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	// Different IPs should have independent limits
	if !rl.AllowConnection("1.1.1.1") {
		t.Fatal("IP 1 should be allowed")
	}
	if !rl.AllowConnection("2.2.2.2") {
		t.Fatal("IP 2 should be allowed")
	}

	if rl.ActiveConnections("1.1.1.1") != 1 {
		t.Fatal("expected 1 active connection for IP 1")
	}
	if rl.ActiveConnections("2.2.2.2") != 1 {
		t.Fatal("expected 1 active connection for IP 2")
	}
}

// --- Integration Tests (Server + WebSocket) ---

func setupTestServer(t *testing.T) (*RelayServer, *httptest.Server) {
	t.Helper()

	cfg := ServerConfig{Addr: ":0"}
	rs, err := NewRelayServer(cfg)
	if err != nil {
		t.Fatalf("NewRelayServer failed: %v", err)
	}

	// Create a test HTTP server using the relay's handler
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", rs.handleAuth)
	mux.HandleFunc("/ws", rs.handleWS)
	mux.HandleFunc("/health", rs.handleHealth)

	ts := httptest.NewServer(mux)
	return rs, ts
}

func authenticateClient(t *testing.T, serverURL string) string {
	t.Helper()

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	reqBody := AuthRequest{
		PublicKey:       []byte(pub),
		ProtocolVersion: "HOP/1.0",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(serverURL+"/auth", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /auth failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth returned %d", resp.StatusCode)
	}

	var authResp AuthResponse
	json.NewDecoder(resp.Body).Decode(&authResp)
	return authResp.SessionToken
}

func TestIntegration_HealthEndpoint(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var health map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&health)

	if health["status"] != "ok" {
		t.Fatalf("expected status 'ok', got '%v'", health["status"])
	}
}

func TestIntegration_AuthFlow(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	token := authenticateClient(t, ts.URL)
	if token == "" {
		t.Fatal("expected non-empty JWT")
	}
}

func TestIntegration_WebSocketBridge(t *testing.T) {
	rs, ts := setupTestServer(t)
	defer ts.Close()
	defer rs.registry.Stop()
	defer rs.limiter.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Authenticate both sender and receiver
	senderJWT := authenticateClient(t, ts.URL)
	receiverJWT := authenticateClient(t, ts.URL)

	// Connect sender via WebSocket
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws"

	senderConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("sender WS dial failed: %v", err)
	}
	defer senderConn.Close(websocket.StatusNormalClosure, "done")

	// Sender registers token
	senderReq := WSRequest{
		SessionToken: senderJWT,
		Action:       "register",
		Token:        "test-bridge-42",
	}
	senderReqBytes, _ := json.Marshal(senderReq)
	senderConn.Write(ctx, websocket.MessageText, senderReqBytes)

	// Read sender's response
	_, senderRespData, err := senderConn.Read(ctx)
	if err != nil {
		t.Fatalf("sender read response failed: %v", err)
	}
	var senderResp WSResponse
	json.Unmarshal(senderRespData, &senderResp)
	if senderResp.Status != "ok" {
		t.Fatalf("sender registration failed: %s", senderResp.Message)
	}

	// Connect receiver via WebSocket
	receiverConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("receiver WS dial failed: %v", err)
	}
	defer receiverConn.Close(websocket.StatusNormalClosure, "done")

	// Receiver joins token
	receiverReq := WSRequest{
		SessionToken: receiverJWT,
		Action:       "join",
		Token:        "test-bridge-42",
	}
	receiverReqBytes, _ := json.Marshal(receiverReq)
	receiverConn.Write(ctx, websocket.MessageText, receiverReqBytes)

	// Read receiver's response
	_, receiverRespData, err := receiverConn.Read(ctx)
	if err != nil {
		t.Fatalf("receiver read response failed: %v", err)
	}
	var receiverResp WSResponse
	json.Unmarshal(receiverRespData, &receiverResp)
	if receiverResp.Status != "ok" {
		t.Fatalf("receiver join failed: %s", receiverResp.Message)
	}

	// Give the bridge a moment to set up the pipes
	time.Sleep(100 * time.Millisecond)

	// Sender sends data through the bridge
	testPayload := []byte("hello from sender via relay bridge!")
	senderConn.Write(ctx, websocket.MessageBinary, testPayload)

	// Receiver should receive the data
	_, receivedData, err := receiverConn.Read(ctx)
	if err != nil {
		t.Fatalf("receiver read data failed: %v", err)
	}

	if !bytes.Equal(receivedData, testPayload) {
		t.Fatalf("data mismatch:\n  sent:     %q\n  received: %q", testPayload, receivedData)
	}

	// Receiver sends data back through the bridge
	replyPayload := []byte("hello from receiver!")
	receiverConn.Write(ctx, websocket.MessageBinary, replyPayload)

	// Sender should receive the reply
	_, replyData, err := senderConn.Read(ctx)
	if err != nil {
		t.Fatalf("sender read reply failed: %v", err)
	}

	if !bytes.Equal(replyData, replyPayload) {
		t.Fatalf("reply mismatch:\n  sent:     %q\n  received: %q", replyPayload, replyData)
	}
}

func TestIntegration_TokenNotFound(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jwt := authenticateClient(t, ts.URL)
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws"

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	// Try to join a nonexistent token
	req := WSRequest{
		SessionToken: jwt,
		Action:       "join",
		Token:        "nonexistent-99",
	}
	reqBytes, _ := json.Marshal(req)
	conn.Write(ctx, websocket.MessageText, reqBytes)

	// Should get an error response
	_, respData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}

	var resp WSResponse
	json.Unmarshal(respData, &resp)
	if resp.Status != "error" {
		t.Fatalf("expected error status, got: %s", resp.Status)
	}
}

func TestIntegration_InvalidAuth(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws"

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	// Send a request with an invalid JWT
	req := WSRequest{
		SessionToken: "invalid.jwt.token",
		Action:       "register",
		Token:        "test-token-01",
	}
	reqBytes, _ := json.Marshal(req)
	conn.Write(ctx, websocket.MessageText, reqBytes)

	// Should get an error response and the connection should close
	_, respData, err := conn.Read(ctx)
	if err != nil {
		// Connection closed — that's also acceptable
		return
	}

	var resp WSResponse
	json.Unmarshal(respData, &resp)
	if resp.Status != "error" {
		t.Fatalf("expected error status, got: %s", resp.Status)
	}
}

// --- extractIP Tests ---

func TestExtractIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	ip := extractIP(req)
	if ip != "192.168.1.1" {
		t.Fatalf("expected '192.168.1.1', got '%s'", ip)
	}
}

func TestExtractIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")

	ip := extractIP(req)
	if ip != "10.0.0.1" {
		t.Fatalf("expected '10.0.0.1', got '%s'", ip)
	}
}

func TestExtractIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "172.16.0.1")

	ip := extractIP(req)
	if ip != "172.16.0.1" {
		t.Fatalf("expected '172.16.0.1', got '%s'", ip)
	}
}
