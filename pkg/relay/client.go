package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

// Client manages the connection from a hop CLI instance to the relay server.
// It handles authentication, token registration/joining, and message streaming.
type Client struct {
	mu        sync.Mutex
	relayURL  string             // Base URL of the relay (e.g., "http://localhost:9999")
	conn      *websocket.Conn    // Active WebSocket connection
	token     string             // JWT session token
	pubKey    ed25519.PublicKey   // Our ephemeral Ed25519 public key
	privKey   ed25519.PrivateKey  // Our ephemeral Ed25519 private key
	connected bool
}

// authRequest matches the relay's AuthRequest JSON structure.
type authRequest struct {
	PublicKey       []byte `json:"public_key"`
	ProtocolVersion string `json:"protocol_version"`
}

// authResponse matches the relay's AuthResponse JSON structure.
type authResponse struct {
	SessionToken string `json:"session_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

// wsRequest matches the relay's WSRequest JSON structure.
type wsRequest struct {
	SessionToken string `json:"session_token"`
	Action       string `json:"action"`
	Token        string `json:"token"`
}

// wsResponse matches the relay's WSResponse JSON structure.
type wsResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// NewClient creates a new relay client for the given relay URL.
func NewClient(relayURL string) *Client {
	// Normalize URL — strip trailing slash
	relayURL = strings.TrimRight(relayURL, "/")
	return &Client{
		relayURL: relayURL,
	}
}

// Authenticate generates an ephemeral Ed25519 keypair and authenticates with
// the relay server. Returns the JWT session token.
func (c *Client) Authenticate(ctx context.Context) error {
	// Generate ephemeral Ed25519 keypair
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generating Ed25519 keypair: %w", err)
	}
	c.pubKey = pub
	c.privKey = priv

	// Send auth request
	reqBody := authRequest{
		PublicKey:       []byte(pub),
		ProtocolVersion: protocol.CurrentVersion.String(),
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling auth request: %w", err)
	}

	authURL := c.relayURL + "/auth"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return fmt.Errorf("creating auth request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sending auth request to %s: %w", authURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var authResp authResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return fmt.Errorf("decoding auth response: %w", err)
	}

	c.token = authResp.SessionToken
	return nil
}

// RegisterToken connects to the relay via WebSocket and registers a transfer token
// as the sender. Blocks until a receiver joins or the context is cancelled.
func (c *Client) RegisterToken(ctx context.Context, transferToken string) error {
	return c.connectWS(ctx, "register", transferToken)
}

// JoinToken connects to the relay via WebSocket and joins an existing transfer
// token as the receiver.
func (c *Client) JoinToken(ctx context.Context, transferToken string) error {
	return c.connectWS(ctx, "join", transferToken)
}

// RegisterTunnel connects to the relay via WebSocket and registers a tunnel slug.
// The tunnel uses the /tunnel WebSocket endpoint instead of /ws.
func (c *Client) RegisterTunnel(ctx context.Context, slug, passwordHash string) error {
	if c.token == "" {
		return fmt.Errorf("not authenticated — call Authenticate() first")
	}

	// Build WebSocket URL for the tunnel endpoint
	wsURL := strings.Replace(c.relayURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/tunnel"

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{})
	if err != nil {
		return fmt.Errorf("connecting to relay tunnel endpoint: %w", err)
	}

	conn.SetReadLimit(17 * 1024 * 1024)

	// Send the initial handshake (same as bridge: session_token + action + token)
	req := wsRequest{
		SessionToken: c.token,
		Action:       "register_tunnel",
		Token:        slug,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "marshal error")
		return fmt.Errorf("marshaling tunnel request: %w", err)
	}

	writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer writeCancel()
	if err := conn.Write(writeCtx, websocket.MessageText, reqBytes); err != nil {
		conn.Close(websocket.StatusInternalError, "write error")
		return fmt.Errorf("sending tunnel handshake: %w", err)
	}

	// Read the handshake response
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readCancel()
	_, respData, err := conn.Read(readCtx)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "read error")
		return fmt.Errorf("reading tunnel response: %w", err)
	}

	var resp wsResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		conn.Close(websocket.StatusProtocolError, "invalid response")
		return fmt.Errorf("decoding tunnel response: %w", err)
	}

	if resp.Status != "ok" {
		conn.Close(websocket.StatusPolicyViolation, "rejected")
		return fmt.Errorf("tunnel registration rejected: %s", resp.Message)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	return nil
}

// connectWS establishes the WebSocket connection and sends the initial handshake.
func (c *Client) connectWS(ctx context.Context, action, transferToken string) error {
	if c.token == "" {
		return fmt.Errorf("not authenticated — call Authenticate() first")
	}

	// Build WebSocket URL
	wsURL := strings.Replace(c.relayURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/ws"

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		// Subprotocols can be added later for version negotiation
	})
	if err != nil {
		return fmt.Errorf("connecting to relay WebSocket: %w", err)
	}

	conn.SetReadLimit(17 * 1024 * 1024) // 17 MB

	// Send the handshake message
	req := wsRequest{
		SessionToken: c.token,
		Action:       action,
		Token:        transferToken,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "marshal error")
		return fmt.Errorf("marshaling WS request: %w", err)
	}

	writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer writeCancel()
	if err := conn.Write(writeCtx, websocket.MessageText, reqBytes); err != nil {
		conn.Close(websocket.StatusInternalError, "write error")
		return fmt.Errorf("sending WS handshake: %w", err)
	}

	// Read the response
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readCancel()
	_, respData, err := conn.Read(readCtx)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "read error")
		return fmt.Errorf("reading WS response: %w", err)
	}

	var resp wsResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		conn.Close(websocket.StatusProtocolError, "invalid response")
		return fmt.Errorf("decoding WS response: %w", err)
	}

	if resp.Status != "ok" {
		conn.Close(websocket.StatusPolicyViolation, "rejected")
		return fmt.Errorf("relay rejected request: %s", resp.Message)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	return nil
}

// Send transmits a protocol message through the relay.
func (c *Client) Send(ctx context.Context, msg *protocol.Message) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("not connected to relay")
	}

	// Encode the protocol message into wire format
	data := protocol.Encode(msg)

	return conn.Write(ctx, websocket.MessageBinary, data)
}

// Receive reads a protocol message from the relay.
func (c *Client) Receive(ctx context.Context) (*protocol.Message, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("not connected to relay")
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading from relay: %w", err)
	}

	// Decode the wire-format message
	// The data is a complete protocol.Encode() output: [4 len][1 type][payload]
	if len(data) < protocol.HeaderSize {
		return nil, fmt.Errorf("message too short: %d bytes", len(data))
	}

	// Parse the message type and payload from the wire format
	// Skip the 4-byte length prefix since WebSocket framing handles that
	msgType := protocol.MessageType(data[4])
	var payload []byte
	if len(data) > protocol.HeaderSize {
		payload = data[protocol.HeaderSize:]
	}

	return &protocol.Message{
		Type:    msgType,
		Payload: payload,
	}, nil
}

// SendRaw sends raw bytes through the relay WebSocket.
func (c *Client) SendRaw(ctx context.Context, data []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("not connected to relay")
	}

	return conn.Write(ctx, websocket.MessageBinary, data)
}

// ReceiveRaw reads raw bytes from the relay WebSocket.
func (c *Client) ReceiveRaw(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("not connected to relay")
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading from relay: %w", err)
	}

	return data, nil
}

// Close cleanly disconnects from the relay.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}

	c.connected = false
	err := c.conn.Close(websocket.StatusNormalClosure, "client disconnect")
	c.conn = nil
	return err
}

// IsConnected returns whether the client has an active relay connection.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// RelayURL returns the relay server URL.
func (c *Client) RelayURL() string {
	return c.relayURL
}

// SessionToken returns the JWT session token obtained during authentication.
// Returns an empty string if not yet authenticated.
func (c *Client) SessionToken() string {
	return c.token
}
