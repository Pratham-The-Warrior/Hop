package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

// SignalServer handles the signaling WebSocket endpoint for NAT hole punch
// coordination. When both peers connect with the same transfer token, the
// server exchanges their public (observed) and private (self-reported)
// addresses, then sends a synchronized PunchSignal to both.
type SignalServer struct {
	auth     *Authenticator
	registry *Registry
	limiter  *RateLimiter
	logger   *log.Logger

	mu    sync.Mutex
	peers map[string]*signalPeer // token → first peer waiting for match
}

// signalPeer represents one side of a signaling session.
type signalPeer struct {
	conn       *websocket.Conn
	publicIP   string
	publicPort uint32          // Observed from the WebSocket connection
	localIP    string          // Self-reported via PeerInfo
	localPort  uint32          // Self-reported via PeerInfo
	ready      chan struct{}   // Closed when PeerInfo is received
	matched    chan struct{}   // Closed when the second peer arrives (for first peer to unblock)
	peerInfo   *protocol.PeerInfo
}

// NewSignalServer creates a new signaling server.
func NewSignalServer(auth *Authenticator, registry *Registry, limiter *RateLimiter, logger *log.Logger) *SignalServer {
	return &SignalServer{
		auth:     auth,
		registry: registry,
		limiter:  limiter,
		logger:   logger,
		peers:    make(map[string]*signalPeer),
	}
}

// signalHandshake is the initial JSON message from the client.
type signalHandshake struct {
	SessionToken string `json:"session_token"`
	Token        string `json:"token"`
}

// HandleSignal processes WebSocket connections for NAT hole punch signaling.
func (s *SignalServer) HandleSignal(w http.ResponseWriter, r *http.Request) {
	ip := extractIP(r)

	// Rate limit
	if !s.limiter.AllowConnection(ip) {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	defer s.limiter.ReleaseConnection(ip)

	// Accept WebSocket
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger.Printf("[signal] WebSocket upgrade failed from %s: %v", ip, err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "signaling complete")

	conn.SetReadLimit(1024 * 1024) // 1 MB

	// Read the handshake (JSON)
	handshakeCtx, handshakeCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer handshakeCancel()

	_, data, err := conn.Read(handshakeCtx)
	if err != nil {
		s.logger.Printf("[signal] handshake read failed from %s: %v", ip, err)
		return
	}

	var hs signalHandshake
	if err := json.Unmarshal(data, &hs); err != nil {
		sendSignalResponse(conn, "error", "invalid handshake JSON")
		s.logger.Printf("[signal] invalid handshake from %s: %v", ip, err)
		return
	}

	// Validate session token
	_, err = s.auth.ValidateToken(hs.SessionToken)
	if err != nil {
		sendSignalResponse(conn, "error", fmt.Sprintf("authentication failed: %v", err))
		s.logger.Printf("[signal] auth failed from %s: %v", ip, err)
		return
	}

	// Check that the token exists in the registry
	entry := s.registry.Lookup(hs.Token)
	if entry == nil {
		sendSignalResponse(conn, "error", "token not found")
		s.logger.Printf("[signal] token %s not found (from %s)", hs.Token, ip)
		return
	}

	// Determine the public IP and port from the WebSocket connection
	publicIP, publicPort := extractIPPort(r)

	sendSignalResponse(conn, "ok", "signaling — waiting for peer info")
	s.logger.Printf("[signal] peer registered for token %s from %s:%d", hs.Token, publicIP, publicPort)

	// Read the PeerInfo message (binary protocol message)
	peerInfoCtx, peerInfoCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer peerInfoCancel()

	_, peerInfoData, err := conn.Read(peerInfoCtx)
	if err != nil {
		s.logger.Printf("[signal] PeerInfo read failed from %s: %v", ip, err)
		return
	}

	// Parse the protocol message
	if len(peerInfoData) < protocol.HeaderSize {
		s.logger.Printf("[signal] PeerInfo message too short from %s", ip)
		return
	}
	msgType := protocol.MessageType(peerInfoData[4])
	if msgType != protocol.MsgPeerInfo {
		s.logger.Printf("[signal] expected PEER_INFO, got %s from %s", msgType, ip)
		return
	}

	peerInfo, err := protocol.DecodePeerInfo(peerInfoData[protocol.HeaderSize:])
	if err != nil {
		s.logger.Printf("[signal] failed to decode PeerInfo from %s: %v", ip, err)
		return
	}

	thisPeer := &signalPeer{
		conn:       conn,
		publicIP:   publicIP,
		publicPort: publicPort,
		localIP:    peerInfo.LocalIP,
		localPort:  peerInfo.UDPPort,
		ready:      make(chan struct{}),
		matched:    make(chan struct{}),
		peerInfo:   peerInfo,
	}
	close(thisPeer.ready)

	// Try to match with an existing peer for this token
	s.mu.Lock()
	existingPeer, exists := s.peers[hs.Token]
	if !exists {
		// We're the first peer — register and wait
		s.peers[hs.Token] = thisPeer
		s.mu.Unlock()

		s.logger.Printf("[signal] token %s — first peer registered, waiting for second", hs.Token)

		// Wait for the second peer to show up (with timeout)
		waitCtx, waitCancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer waitCancel()

		select {
		case <-thisPeer.matched:
			// Second peer arrived and will handle the exchange — just wait
			// for the PunchSignal to be sent (the second peer handles it)
			s.logger.Printf("[signal] token %s — first peer was matched, signal sent", hs.Token)
			// Give the client time to read the signal before we close
			time.Sleep(500 * time.Millisecond)
			return
		case <-waitCtx.Done():
			// Cleanup
			s.mu.Lock()
			delete(s.peers, hs.Token)
			s.mu.Unlock()
			s.logger.Printf("[signal] token %s — timed out waiting for second peer", hs.Token)
			return
		}
	}

	// We're the second peer — match found!
	delete(s.peers, hs.Token)
	s.mu.Unlock()

	s.logger.Printf("[signal] token %s — both peers connected, exchanging addresses", hs.Token)

	// Wait for existing peer to be ready (PeerInfo received)
	select {
	case <-existingPeer.ready:
	case <-time.After(5 * time.Second):
		s.logger.Printf("[signal] token %s — existing peer not ready in time", hs.Token)
		return
	}

	// Send PunchSignal to both peers
	// Peer A gets Peer B's addresses and vice versa
	signalToExisting := &protocol.PunchSignal{
		PublicIP:   thisPeer.publicIP,
		PublicPort: thisPeer.publicPort,
		LocalIP:    thisPeer.localIP,
		LocalPort:  thisPeer.localPort,
	}

	signalToThis := &protocol.PunchSignal{
		PublicIP:   existingPeer.publicIP,
		PublicPort: existingPeer.publicPort,
		LocalIP:    existingPeer.localIP,
		LocalPort:  existingPeer.localPort,
	}

	// Send to existing peer
	sendPunchSignal(existingPeer.conn, signalToExisting)
	s.logger.Printf("[signal] token %s — sent PunchSignal to first peer", hs.Token)

	// Unblock the first peer's wait goroutine
	close(existingPeer.matched)
	// Send to this peer
	sendPunchSignal(thisPeer.conn, signalToThis)
	s.logger.Printf("[signal] token %s — sent PunchSignal to second peer", hs.Token)

	// Give clients a moment to read the signal before closing
	time.Sleep(500 * time.Millisecond)
}

// sendPunchSignal sends a PunchSignal protocol message over a WebSocket.
func sendPunchSignal(conn *websocket.Conn, signal *protocol.PunchSignal) {
	msg := &protocol.Message{
		Type:    protocol.MsgPunchSignal,
		Payload: protocol.EncodePunchSignal(signal),
	}
	data := protocol.Encode(msg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn.Write(ctx, websocket.MessageBinary, data)
}

// sendSignalResponse sends a JSON status response over the WebSocket.
func sendSignalResponse(conn *websocket.Conn, status, message string) {
	resp := struct {
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}{
		Status:  status,
		Message: message,
	}
	data, _ := json.Marshal(resp)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn.Write(ctx, websocket.MessageText, data)
}

// extractIPPort extracts the IP and port from an HTTP request's remote address.
func extractIPPort(r *http.Request) (string, uint32) {
	addr := r.RemoteAddr

	// Handle X-Forwarded-For if behind a proxy
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		addr = forwarded
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}

	var port uint32
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
