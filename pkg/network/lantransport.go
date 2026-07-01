package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

// TCPTransport implements transfer.Transport over a direct TCP connection
// established via LAN discovery. It uses the same length-prefixed binary
// framing as the relay WebSocket transport, but over a raw TCP stream.
//
// TCP is used for LAN (instead of the UDP transport used for NAT hole punching)
// because LAN peers can reach each other directly — TCP provides built-in
// reliability, ordering, and flow control with zero custom framing overhead.
type TCPTransport struct {
	mu     sync.Mutex
	conn   net.Conn
	closed bool
}

// NewTCPTransport wraps an established TCP connection into a Transport.
func NewTCPTransport(conn net.Conn) *TCPTransport {
	return &TCPTransport{conn: conn}
}

// Send transmits a protocol message over the TCP connection using
// length-prefixed binary framing.
func (t *TCPTransport) Send(ctx context.Context, msg *protocol.Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("transport is closed")
	}

	data := protocol.Encode(msg)

	// Write the full encoded message (length prefix + type + payload)
	// in a single write to avoid partial frames.
	_, err := t.conn.Write(data)
	if err != nil {
		return fmt.Errorf("writing to TCP: %w", err)
	}

	return nil
}

// Receive reads the next protocol message from the TCP connection.
func (t *TCPTransport) Receive(ctx context.Context) (*protocol.Message, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, fmt.Errorf("transport is closed")
	}
	t.mu.Unlock()

	// Use a goroutine to make the read cancellable via context.
	type result struct {
		msg *protocol.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := protocol.Decode(t.conn)
		ch <- result{msg, err}
	}()

	select {
	case <-ctx.Done():
		// Close the connection to unblock the Decode read
		t.conn.Close()
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			// Check if it's a clean EOF (peer closed connection).
			// protocol.Decode wraps errors, so use errors.Is for unwrapping.
			if errors.Is(res.err, io.EOF) {
				return nil, fmt.Errorf("peer closed connection")
			}
			return nil, fmt.Errorf("reading from TCP: %w", res.err)
		}
		return res.msg, nil
	}
}

// Close shuts down the TCP transport.
func (t *TCPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true
	return t.conn.Close()
}
