package network

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

const (
	// LANTimeout is the maximum time to wait for a LAN peer response.
	// Per spec: 500ms.
	LANTimeout = 500 * time.Millisecond

	// LANBroadcastPort is the fixed UDP port used for LAN discovery probes.
	LANBroadcastPort = 41729

	// LANProbeInterval is how often the sender broadcasts a probe.
	LANProbeInterval = 100 * time.Millisecond
)

// LANResult contains the outcome of a successful LAN discovery.
type LANResult struct {
	Conn net.Conn // The established TCP connection to the LAN peer
}

// DiscoverLANPeer attempts to find a peer on the local network sharing
// the same transfer token. It uses UDP broadcast for discovery and
// establishes a TCP connection for the actual data transfer.
//
// The role parameter determines behavior:
//   - "sender": Broadcasts LANProbe packets and waits for a LANResponse.
//     When a response arrives, it dials the responder's TCP port.
//   - "receiver": Listens for LANProbe packets matching its token.
//     When found, sends a LANResponse and accepts a TCP connection.
//
// Returns a LANResult with an established TCP connection on success,
// or an error if no peer is found within LANTimeout.
func DiscoverLANPeer(ctx context.Context, token string, role string) (*LANResult, error) {
	switch role {
	case "sender":
		return discoverAsSender(ctx, token)
	case "receiver":
		return discoverAsReceiver(ctx, token)
	default:
		return nil, fmt.Errorf("invalid LAN discovery role: %q", role)
	}
}

// discoverAsSender broadcasts LAN probes and waits for a response.
func discoverAsSender(ctx context.Context, token string) (*LANResult, error) {
	// Start a TCP listener on an ephemeral port for the peer to connect to
	tcpListener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("starting TCP listener: %w", err)
	}
	defer tcpListener.Close()

	tcpPort := uint32(tcpListener.Addr().(*net.TCPAddr).Port)

	// Open a UDP socket for broadcasting
	broadcastAddr := &net.UDPAddr{
		IP:   net.IPv4bcast, // 255.255.255.255
		Port: LANBroadcastPort,
	}

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("opening UDP socket: %w", err)
	}
	defer udpConn.Close()

	// Build the probe packet
	probe := protocol.EncodeLANProbe(&protocol.LANProbe{
		Token:   token,
		TCPPort: tcpPort,
	})

	// Create a timeout context
	lanCtx, cancel := context.WithTimeout(ctx, LANTimeout)
	defer cancel()

	// Channel to receive the TCP connection from the listener
	type tcpResult struct {
		conn net.Conn
		err  error
	}
	tcpCh := make(chan tcpResult, 1)

	// Accept TCP connections in the background
	go func() {
		tcpListener.(*net.TCPListener).SetDeadline(time.Now().Add(LANTimeout + 100*time.Millisecond))
		conn, err := tcpListener.Accept()
		tcpCh <- tcpResult{conn, err}
	}()

	// Channel to receive a LAN response via UDP
	type udpResult struct {
		resp *protocol.LANResponse
		addr *net.UDPAddr
		err  error
	}
	udpCh := make(chan udpResult, 1)

	// Listen for UDP responses in the background
	go func() {
		buf := make([]byte, 512)
		for {
			udpConn.SetReadDeadline(time.Now().Add(LANTimeout + 100*time.Millisecond))
			n, addr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				udpCh <- udpResult{nil, nil, err}
				return
			}

			resp, err := protocol.DecodeLANResponse(buf[:n])
			if err != nil {
				continue // Not a valid response, keep listening
			}

			if resp.Token == token {
				udpCh <- udpResult{resp, addr, nil}
				return
			}
		}
	}()

	// Broadcast probes periodically
	probeTicker := time.NewTicker(LANProbeInterval)
	defer probeTicker.Stop()

	// Send first probe immediately
	_, _ = udpConn.WriteTo(probe, broadcastAddr)

	for {
		select {
		case <-lanCtx.Done():
			return nil, fmt.Errorf("LAN discovery timed out")

		case <-probeTicker.C:
			_, _ = udpConn.WriteTo(probe, broadcastAddr)

		case res := <-udpCh:
			if res.err != nil {
				continue
			}
			// Got a valid response — the receiver will connect to our TCP port.
			// Wait for the TCP connection.
			select {
			case <-lanCtx.Done():
				return nil, fmt.Errorf("LAN discovery: got response but TCP connect timed out")
			case tcpRes := <-tcpCh:
				if tcpRes.err != nil {
					return nil, fmt.Errorf("LAN TCP accept: %w", tcpRes.err)
				}
				return &LANResult{Conn: tcpRes.conn}, nil
			}

		case tcpRes := <-tcpCh:
			// TCP connection arrived (possibly before we saw the UDP response)
			if tcpRes.err != nil {
				return nil, fmt.Errorf("LAN TCP accept: %w", tcpRes.err)
			}
			return &LANResult{Conn: tcpRes.conn}, nil
		}
	}
}

// discoverAsReceiver listens for LAN probes and responds when it finds
// a matching token.
func discoverAsReceiver(ctx context.Context, token string) (*LANResult, error) {
	// Listen on the well-known broadcast port for probes
	udpAddr := &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: LANBroadcastPort,
	}

	udpConn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("listening on LAN broadcast port %d: %w", LANBroadcastPort, err)
	}
	defer udpConn.Close()

	// Create a timeout context
	lanCtx, cancel := context.WithTimeout(ctx, LANTimeout)
	defer cancel()

	buf := make([]byte, 512)

	for {
		select {
		case <-lanCtx.Done():
			return nil, fmt.Errorf("LAN discovery timed out")
		default:
		}

		// Read with a short deadline so we can check context cancellation
		udpConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, senderAddr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Timeout — check context and try again
			}
			return nil, fmt.Errorf("reading LAN probe: %w", err)
		}

		// Try to decode as a LAN probe
		probe, err := protocol.DecodeLANProbe(buf[:n])
		if err != nil {
			continue // Not a valid probe, keep listening
		}

		// Check if the token matches
		if probe.Token != token {
			continue // Different token, keep listening
		}

		// Token matches! Connect to the sender's TCP port.
		senderTCPAddr := net.JoinHostPort(senderAddr.IP.String(), fmt.Sprintf("%d", probe.TCPPort))

		// Send a LAN response back (unicast) so the sender knows we found them.
		// This is informational — the TCP connect is the authoritative handshake.
		resp := protocol.EncodeLANResponse(&protocol.LANResponse{
			Token:   token,
			TCPPort: 0, // Receiver doesn't need a TCP listener in this flow
		})
		_, _ = udpConn.WriteTo(resp, senderAddr)

		// Establish TCP connection to the sender
		tcpConn, err := net.DialTimeout("tcp", senderTCPAddr, LANTimeout)
		if err != nil {
			return nil, fmt.Errorf("connecting to LAN peer at %s: %w", senderTCPAddr, err)
		}

		return &LANResult{Conn: tcpConn}, nil
	}
}
