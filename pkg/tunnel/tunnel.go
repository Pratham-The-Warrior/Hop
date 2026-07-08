package tunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prathmeshsarda/hop/pkg/protocol"
	"github.com/prathmeshsarda/hop/pkg/relay"
)

// TunnelCallbacks provides hooks for TUI updates.
type TunnelCallbacks struct {
	// OnConnected is called when the tunnel is established with the relay.
	OnConnected func(publicURL string)
	// OnRequest is called for each proxied HTTP request with response metadata.
	OnRequest func(method, path string, statusCode int, statusText string, latency time.Duration)
	// OnDisconnect is called when the tunnel disconnects.
	OnDisconnect func(reason string)
	// OnPipeChange is called when the active pipe count changes.
	OnPipeChange func(activePipes int)
}

// TunnelClient manages the client-side tunnel connection to the relay and
// proxies incoming HTTP requests to localhost.
type TunnelClient struct {
	port         int
	slug         string
	passwordHash string
	relayURL     string
	client       *relay.Client
	callbacks    TunnelCallbacks
	replay       *ReplayBuffer
	logger       *log.Logger

	activePipes atomic.Int32
	stopOnce    sync.Once
	done        chan struct{}
}

// TunnelConfig holds configuration for starting a tunnel.
type TunnelConfig struct {
	Port           int
	Slug           string
	PasswordHash   string // bcrypt hash, empty if no password
	RelayURL       string
	ReplayBuffer   int    // Number of requests to buffer (default: 50)
	ReplayMaxBody  int64  // Max body size to capture in bytes (default: 1 MB)
	Callbacks      TunnelCallbacks
}

// NewTunnelClient creates a new tunnel client.
func NewTunnelClient(cfg TunnelConfig) *TunnelClient {
	if cfg.ReplayBuffer <= 0 {
		cfg.ReplayBuffer = 50
	}
	if cfg.ReplayMaxBody <= 0 {
		cfg.ReplayMaxBody = 1 * 1024 * 1024 // 1 MB
	}

	return &TunnelClient{
		port:         cfg.Port,
		slug:         cfg.Slug,
		passwordHash: cfg.PasswordHash,
		relayURL:     cfg.RelayURL,
		client:       relay.NewClient(cfg.RelayURL),
		callbacks:    cfg.Callbacks,
		replay:       NewReplayBuffer(cfg.ReplayBuffer, cfg.ReplayMaxBody),
		logger:       log.New(io.Discard, "[tunnel] ", log.LstdFlags),
		done:         make(chan struct{}),
	}
}

// SetLogger sets a logger for the tunnel client.
func (tc *TunnelClient) SetLogger(l *log.Logger) {
	tc.logger = l
}

// ReplayBuf returns the replay buffer for external access (e.g., by replay store).
func (tc *TunnelClient) ReplayBuf() *ReplayBuffer {
	return tc.replay
}

// Start connects to the relay, registers the tunnel, and enters the proxy loop.
// It blocks until the context is cancelled or the tunnel is shut down.
func (tc *TunnelClient) Start(ctx context.Context) error {
	// 1. Authenticate with the relay
	tc.logger.Printf("authenticating with relay at %s", tc.relayURL)
	if err := tc.client.Authenticate(ctx); err != nil {
		return fmt.Errorf("relay authentication failed: %w", err)
	}

	// 2. Register the tunnel via WebSocket
	tc.logger.Printf("registering tunnel slug %q", tc.slug)
	if err := tc.client.RegisterTunnel(ctx, tc.slug, tc.passwordHash); err != nil {
		return fmt.Errorf("tunnel registration failed: %w", err)
	}

	// Build the public URL
	baseURL := tc.relayURL
	baseURL = strings.Replace(baseURL, "ws://", "http://", 1)
	baseURL = strings.Replace(baseURL, "wss://", "https://", 1)
	publicURL := fmt.Sprintf("%s/t/%s", baseURL, tc.slug)

	if tc.callbacks.OnConnected != nil {
		tc.callbacks.OnConnected(publicURL)
	}

	// 3. Enter the proxy loop
	tc.logger.Printf("tunnel active — proxying to localhost:%d", tc.port)
	return tc.proxyLoop(ctx)
}

// proxyLoop reads incoming tunnel requests from the relay and proxies them
// to localhost. Each request is handled in its own goroutine for concurrency.
func (tc *TunnelClient) proxyLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			tc.shutdown()
			return ctx.Err()
		case <-tc.done:
			return nil
		default:
		}

		// Read next message from relay
		msg, err := tc.client.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			tc.logger.Printf("relay connection lost: %v", err)
			if tc.callbacks.OnDisconnect != nil {
				tc.callbacks.OnDisconnect(err.Error())
			}
			return fmt.Errorf("relay disconnected: %w", err)
		}

		switch msg.Type {
		case protocol.MsgTunnelRequest:
			// Decode the HTTP request from the relay
			httpReq, err := protocol.DecodeTunnelHTTPRequest(msg.Payload)
			if err != nil {
				tc.logger.Printf("invalid tunnel request: %v", err)
				continue
			}

			// Handle concurrently
			tc.activePipes.Add(1)
			if tc.callbacks.OnPipeChange != nil {
				tc.callbacks.OnPipeChange(int(tc.activePipes.Load()))
			}

			go tc.handleRequest(ctx, httpReq)

		case protocol.MsgTunnelClose:
			tc.logger.Printf("relay sent tunnel close")
			tc.shutdown()
			return nil

		default:
			tc.logger.Printf("unexpected message type in tunnel: %s", msg.Type)
		}
	}
}

// handleRequest proxies a single HTTP request to localhost and sends the response
// back to the relay.
func (tc *TunnelClient) handleRequest(ctx context.Context, tunnelReq *protocol.TunnelHTTPRequest) {
	defer func() {
		tc.activePipes.Add(-1)
		if tc.callbacks.OnPipeChange != nil {
			tc.callbacks.OnPipeChange(int(tc.activePipes.Load()))
		}
	}()

	start := time.Now()

	// Build the local HTTP request
	targetURL := fmt.Sprintf("http://localhost:%d%s", tc.port, tunnelReq.Path)

	var bodyReader io.Reader
	if len(tunnelReq.Body) > 0 {
		bodyReader = strings.NewReader(string(tunnelReq.Body))
	}

	localReq, err := http.NewRequestWithContext(ctx, tunnelReq.Method, targetURL, bodyReader)
	if err != nil {
		tc.logger.Printf("failed to create local request: %v", err)
		tc.sendErrorResponse(ctx, tunnelReq.RequestID, 502, "Bad Gateway")
		return
	}

	// Copy headers from the tunnel request
	for k, v := range tunnelReq.Headers {
		// Skip hop-by-hop headers
		lower := strings.ToLower(k)
		if lower == "connection" || lower == "upgrade" || lower == "transfer-encoding" {
			continue
		}
		localReq.Header.Set(k, v)
	}

	// Forward to localhost
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		// Don't follow redirects — let them pass through
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	localResp, err := httpClient.Do(localReq)
	if err != nil {
		tc.logger.Printf("localhost request failed: %v", err)
		tc.sendErrorResponse(ctx, tunnelReq.RequestID, 502, "Bad Gateway")

		latency := time.Since(start)
		if tc.callbacks.OnRequest != nil {
			tc.callbacks.OnRequest(tunnelReq.Method, tunnelReq.Path, 502, "Bad Gateway", latency)
		}
		// Capture failed request in replay buffer
		tc.replay.Add(tunnelReq.Method, tunnelReq.Path, tunnelReq.Headers, tunnelReq.Body,
			502, "Bad Gateway", nil, latency)
		return
	}
	defer localResp.Body.Close()

	// Read the response body (cap at 100 MB per spec)
	const maxResponseBody = 100 * 1024 * 1024
	respBody, err := io.ReadAll(io.LimitReader(localResp.Body, maxResponseBody))
	if err != nil {
		tc.logger.Printf("error reading localhost response: %v", err)
		tc.sendErrorResponse(ctx, tunnelReq.RequestID, 502, "Bad Gateway")
		return
	}

	latency := time.Since(start)

	// Collect response headers
	respHeaders := make(map[string]string)
	for k, vals := range localResp.Header {
		respHeaders[k] = strings.Join(vals, ", ")
	}

	// Send response back to relay
	tunnelResp := &protocol.TunnelHTTPResponse{
		RequestID:  tunnelReq.RequestID,
		StatusCode: uint16(localResp.StatusCode),
		Headers:    respHeaders,
		Body:       respBody,
	}

	respMsg := &protocol.Message{
		Type:    protocol.MsgTunnelResponse,
		Payload: protocol.EncodeTunnelHTTPResponse(tunnelResp),
	}

	if err := tc.client.Send(ctx, respMsg); err != nil {
		tc.logger.Printf("failed to send response to relay: %v", err)
	}

	// Notify TUI
	statusText := http.StatusText(localResp.StatusCode)
	if tc.callbacks.OnRequest != nil {
		tc.callbacks.OnRequest(tunnelReq.Method, tunnelReq.Path, localResp.StatusCode, statusText, latency)
	}

	// Capture in replay buffer
	tc.replay.Add(tunnelReq.Method, tunnelReq.Path, tunnelReq.Headers, tunnelReq.Body,
		localResp.StatusCode, statusText, respHeaders, latency)
}

// sendErrorResponse sends an error HTTP response back to the relay.
func (tc *TunnelClient) sendErrorResponse(ctx context.Context, requestID uint32, statusCode int, statusText string) {
	body := []byte(fmt.Sprintf("<html><body><h1>%d %s</h1></body></html>", statusCode, statusText))
	tunnelResp := &protocol.TunnelHTTPResponse{
		RequestID:  requestID,
		StatusCode: uint16(statusCode),
		Headers: map[string]string{
			"Content-Type": "text/html",
		},
		Body: body,
	}

	respMsg := &protocol.Message{
		Type:    protocol.MsgTunnelResponse,
		Payload: protocol.EncodeTunnelHTTPResponse(tunnelResp),
	}

	if err := tc.client.Send(ctx, respMsg); err != nil {
		tc.logger.Printf("failed to send error response to relay: %v", err)
	}
}

// Stop gracefully shuts down the tunnel.
func (tc *TunnelClient) Stop(ctx context.Context) {
	tc.stopOnce.Do(func() {
		tc.logger.Printf("shutting down tunnel")

		// Send close message to relay
		closeMsg := &protocol.Message{
			Type: protocol.MsgTunnelClose,
		}
		if err := tc.client.Send(ctx, closeMsg); err != nil {
			tc.logger.Printf("failed to send tunnel close: %v", err)
		}

		tc.client.Close()
		close(tc.done)
	})
}

// shutdown performs internal cleanup.
func (tc *TunnelClient) shutdown() {
	tc.stopOnce.Do(func() {
		tc.client.Close()
		close(tc.done)
	})
}
