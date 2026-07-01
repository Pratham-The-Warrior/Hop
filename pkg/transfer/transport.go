package transfer

import (
	"context"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

// Transport abstracts the communication channel for the transfer engine.
// Both relay.Client (WebSocket via relay) and network.UDPTransport (direct
// P2P over UDP) implement this interface, allowing the transfer engine to
// operate identically regardless of the underlying connectivity tier.
type Transport interface {
	// Send transmits a protocol message to the peer.
	Send(ctx context.Context, msg *protocol.Message) error

	// Receive reads the next protocol message from the peer.
	Receive(ctx context.Context) (*protocol.Message, error)

	// Close cleanly shuts down the transport connection.
	Close() error
}
