package network

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/prathmeshsarda/hop/pkg/transfer"
	"github.com/prathmeshsarda/hop/pkg/tui"
)

// ConnectResult holds the outcome of tier negotiation — a ready-to-use
// transport and the tier that was selected.
type ConnectResult struct {
	Transport transfer.Transport
	Tier      tui.ConnectionTier
}

// ConnectConfig holds configuration for the tier negotiation process.
type ConnectConfig struct {
	RelayURL     string // Base URL of the relay server
	Token        string // Transfer token (word-word-NN)
	SessionToken string // JWT session token from relay auth
	EnableP2P    bool   // Whether to attempt NAT hole punching (Tier 2)
	EnableLAN    bool   // Whether to attempt LAN discovery (Tier 1)
	Role         string // "sender" or "receiver" — needed for LAN discovery coordination
}

// logger is a shared logger for connection events.
var logger = log.New(os.Stderr, "", 0)

// Connect attempts tiers in order: Tier 1 (LAN) → Tier 2 (P2P) → Tier 3 (Relay).
//
// The relay client must already be authenticated and connected (RegisterToken
// or JoinToken must have succeeded) before calling Connect. If a faster tier
// succeeds, the relay client can be closed by the caller.
//
// The relayTransport parameter is the already-connected relay.Client that
// satisfies transfer.Transport. If a direct tier succeeds, it is used instead.
func Connect(ctx context.Context, cfg ConnectConfig, relayTransport transfer.Transport) (*ConnectResult, error) {
	// Tier 1: LAN Fast-Path (if enabled)
	if cfg.EnableLAN {
		result, err := attemptTier1(ctx, cfg)
		if err == nil && result != nil {
			logger.Printf("  ✓ LAN peer found — using direct LAN connection\n")
			return result, nil
		}
		if err != nil {
			logger.Printf("  ✗ LAN discovery failed: %v — trying P2P\n", err)
		}
	}

	// Tier 2: NAT Hole Punching (if enabled)
	if cfg.EnableP2P {
		result, err := attemptTier2(ctx, cfg)
		if err == nil && result != nil {
			logger.Printf("  ✓ NAT hole punch succeeded — using direct P2P\n")
			return result, nil
		}
		if err != nil {
			logger.Printf("  ✗ NAT hole punch failed: %v — falling back to relay\n", err)
		}
	}

	// Tier 3: Relay Fallback (always available)
	return &ConnectResult{
		Transport: relayTransport,
		Tier:      tui.TierRelayed,
	}, nil
}

// attemptTier1 tries to discover a peer on the same LAN via UDP broadcast.
func attemptTier1(ctx context.Context, cfg ConnectConfig) (*ConnectResult, error) {
	logger.Printf("  → Scanning local network...")

	result, err := DiscoverLANPeer(ctx, cfg.Token, cfg.Role)
	if err != nil {
		return nil, fmt.Errorf("LAN discovery: %w", err)
	}

	// Wrap the TCP connection in a TCPTransport
	tcpTransport := NewTCPTransport(result.Conn)

	return &ConnectResult{
		Transport: tcpTransport,
		Tier:      tui.TierLAN,
	}, nil
}

// attemptTier2 tries to establish a direct P2P connection via NAT hole punching.
func attemptTier2(ctx context.Context, cfg ConnectConfig) (*ConnectResult, error) {
	// Create a timeout context for the entire Tier 2 attempt
	tier2Ctx, cancel := context.WithTimeout(ctx, PunchTimeout+2*time.Second) // Extra time for signaling
	defer cancel()

	logger.Printf("  → Attempting NAT hole punch...")

	result, err := AttemptHolePunch(tier2Ctx, cfg.RelayURL, cfg.Token, cfg.SessionToken)
	if err != nil {
		return nil, fmt.Errorf("hole punch: %w", err)
	}

	// Wrap the UDP connection in a UDPTransport
	udpTransport := NewUDPTransport(result.Conn, result.PeerAddr)

	return &ConnectResult{
		Transport: udpTransport,
		Tier:      tui.TierP2P,
	}, nil
}
