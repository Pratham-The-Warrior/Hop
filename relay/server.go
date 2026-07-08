package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// RelayServer is the central coordinator for the hop relay service.
type RelayServer struct {
	addr        string
	auth        *Authenticator
	registry    *Registry
	limiter     *RateLimiter
	bridge      *Bridge
	browser     *BrowserBridge
	signal      *SignalServer
	tunnel      *TunnelServer
	logger      *log.Logger
	server      *http.Server
}

// ServerConfig holds configuration for the relay server.
type ServerConfig struct {
	Addr     string // Listen address (e.g., ":9999")
	TLS      bool   // Enable TLS
	CertFile string // TLS certificate file
	KeyFile  string // TLS key file
}

// NewRelayServer creates and initializes a new relay server.
func NewRelayServer(cfg ServerConfig) (*RelayServer, error) {
	logger := log.New(os.Stdout, "[relay] ", log.LstdFlags|log.Lmsgprefix)

	auth, err := NewAuthenticator()
	if err != nil {
		return nil, fmt.Errorf("initializing authenticator: %w", err)
	}

	registry := NewRegistry()
	limiter := NewRateLimiter()
	bridge := NewBridge(auth, registry, limiter, logger)
	browser := NewBrowserBridge(registry, limiter, logger)
	signal := NewSignalServer(auth, registry, limiter, logger)
	tunnelSrv := NewTunnelServer(auth, limiter, logger)

	rs := &RelayServer{
		addr:     cfg.Addr,
		auth:     auth,
		registry: registry,
		limiter:  limiter,
		bridge:   bridge,
		browser:  browser,
		signal:   signal,
		tunnel:   tunnelSrv,
		logger:   logger,
	}

	// Build the HTTP mux for known API routes
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", rs.handleAuth)
	mux.HandleFunc("/ws", rs.handleWS)
	mux.HandleFunc("/signal", rs.handleSignal)
	mux.HandleFunc("/tunnel", rs.handleTunnel)
	mux.HandleFunc("/health", rs.handleHealth)

	// Wrap with TokenRouter to handle browser bridge requests for token paths
	tokenRouter := NewTokenRouter(mux, browser, tunnelSrv)

	// Wrap with middleware
	handler := rs.loggingMiddleware(rs.recoveryMiddleware(limiter.RateLimitMiddleware(tokenRouter)))

	rs.server = &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 10 * time.Minute, // Increased for browser file downloads
		IdleTimeout:  120 * time.Second,
	}

	return rs, nil
}

// Start begins listening for connections.
func (rs *RelayServer) Start() error {
	rs.logger.Printf("starting relay server on %s", rs.addr)
	rs.logger.Printf("  max connections per IP: 5")
	rs.logger.Printf("  max lookups per minute: 10")
	rs.logger.Printf("  bandwidth cap per session: 10 GB")
	rs.logger.Printf("  idle timeout: 5 minutes")
	rs.logger.Printf("  session expiry: 24 hours")
	rs.logger.Printf("  browser bridge: enabled")
	rs.logger.Printf("  tunnel server: enabled")

	return rs.server.ListenAndServe()
}

// StartTLS begins listening with TLS.
func (rs *RelayServer) StartTLS(certFile, keyFile string) error {
	rs.logger.Printf("starting relay server on %s (TLS)", rs.addr)
	return rs.server.ListenAndServeTLS(certFile, keyFile)
}

// Shutdown gracefully stops the server.
func (rs *RelayServer) Shutdown(ctx context.Context) error {
	rs.logger.Printf("shutting down relay server...")
	rs.registry.Stop()
	rs.limiter.Stop()
	return rs.server.Shutdown(ctx)
}

// --- Route Handlers ---

func (rs *RelayServer) handleAuth(w http.ResponseWriter, r *http.Request) {
	rs.auth.HandleAuth(w, r)
}

func (rs *RelayServer) handleWS(w http.ResponseWriter, r *http.Request) {
	rs.bridge.HandleWebSocket(w, r)
}

func (rs *RelayServer) handleSignal(w http.ResponseWriter, r *http.Request) {
	rs.signal.HandleSignal(w, r)
}

func (rs *RelayServer) handleTunnel(w http.ResponseWriter, r *http.Request) {
	rs.tunnel.HandleTunnel(w, r)
}

func (rs *RelayServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","sessions":%d,"tokens":%d,"tunnels":%d}`,
		rs.auth.SessionCount(), rs.registry.Count(), rs.tunnel.ActiveTunnelCount())
}

// --- Middleware ---

// loggingMiddleware logs each incoming HTTP request.
func (rs *RelayServer) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		rs.logger.Printf("%s %s %s %d %s",
			extractIP(r), r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

// recoveryMiddleware catches panics and returns 500.
func (rs *RelayServer) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				rs.logger.Printf("PANIC: %v", rec)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter wraps ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
