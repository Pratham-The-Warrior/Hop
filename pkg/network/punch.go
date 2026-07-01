package network

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

const (
	// PunchAttempts is the number of UDP hole punch probe attempts.
	PunchAttempts = 3

	// PunchInterval is the delay between punch attempts.
	PunchInterval = 1500 * time.Millisecond

	// PunchTimeout is the total timeout for the hole punch process.
	PunchTimeout = 5 * time.Second

	// ProbeSize is the fixed size of a UDP punch probe packet.
	ProbeSize = 13 // 4 magic + 1 type + 8 nonce

	// ProbeMagic is the magic prefix identifying a hop punch probe.
	ProbeMagic = "HOP!"
)

// ProbeType identifies the kind of UDP punch probe.
type ProbeType byte

const (
	ProbeTypeProbe ProbeType = 0x01 // Initial probe
	ProbeTypeAck   ProbeType = 0x02 // Acknowledgment of received probe
)

// PunchResult contains the outcome of a successful hole punch.
type PunchResult struct {
	Conn     *net.UDPConn // The UDP connection to the peer
	PeerAddr *net.UDPAddr // The peer's address we're communicating with
}

// signalRequest matches the relay's signaling WebSocket handshake.
type signalRequest struct {
	SessionToken string `json:"session_token"`
	Token        string `json:"token"`
}

// signalResponse matches the relay's signaling response.
type signalResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// encodeProbe builds a UDP punch probe packet.
func encodeProbe(probeType ProbeType) ([]byte, error) {
	buf := make([]byte, ProbeSize)
	copy(buf[0:4], ProbeMagic)
	buf[4] = byte(probeType)

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating probe nonce: %w", err)
	}
	copy(buf[5:], nonce)

	return buf, nil
}

// decodeProbe parses a UDP punch probe packet.
// Returns the probe type, or an error if the packet is malformed.
func decodeProbe(data []byte) (ProbeType, error) {
	if len(data) < ProbeSize {
		return 0, fmt.Errorf("probe too short: %d bytes", len(data))
	}
	if string(data[0:4]) != ProbeMagic {
		return 0, fmt.Errorf("invalid probe magic")
	}
	return ProbeType(data[4]), nil
}

// getLocalIP returns the preferred outbound local IP address.
func getLocalIP() (string, error) {
	// Dial a public address to determine the local IP used for outbound traffic.
	// No actual connection is established (UDP is connectionless).
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("determining local IP: %w", err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}

// AttemptHolePunch coordinates a UDP NAT hole punch through the relay's
// signaling server. Both peers must call this concurrently for the punch
// to succeed.
//
// The flow:
//  1. Open a local UDP socket on a random port
//  2. Connect to the relay's /signal endpoint via WebSocket
//  3. Send our PeerInfo (local IP + UDP port)
//  4. Wait for PunchSignal (peer's public + local addresses)
//  5. Fire 3 UDP probes at the peer's public address, ~1.5s apart
//  6. Simultaneously listen for incoming probes
//  7. If a probe arrives, respond with an ACK; if ACK received, return success
//  8. Total timeout: 5 seconds
func AttemptHolePunch(ctx context.Context, relayURL, transferToken, sessionToken string) (*PunchResult, error) {
	// Create a context with the punch timeout
	punchCtx, cancel := context.WithTimeout(ctx, PunchTimeout)
	defer cancel()

	// Step 1: Open a local UDP socket
	udpConn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("opening UDP socket: %w", err)
	}
	localUDPAddr := udpConn.LocalAddr().(*net.UDPAddr)

	// Step 2: Get our local IP
	localIP, err := getLocalIP()
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("getting local IP: %w", err)
	}

	// Step 3: Connect to the signaling server
	peerSignal, err := exchangeSignaling(punchCtx, relayURL, transferToken, sessionToken,
		uint32(localUDPAddr.Port), localIP)
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("signaling exchange: %w", err)
	}

	// Step 4: Resolve the peer's public address for punching
	peerPublicAddr, err := net.ResolveUDPAddr("udp4",
		fmt.Sprintf("%s:%d", peerSignal.PublicIP, peerSignal.PublicPort))
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("resolving peer public address: %w", err)
	}

	// Also resolve the peer's local address (useful if on the same LAN)
	peerLocalAddr, err := net.ResolveUDPAddr("udp4",
		fmt.Sprintf("%s:%d", peerSignal.LocalIP, peerSignal.LocalPort))
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("resolving peer local address: %w", err)
	}

	// Step 5: Run the punch loop
	result, err := runPunchLoop(punchCtx, udpConn, peerPublicAddr, peerLocalAddr)
	if err != nil {
		udpConn.Close()
		return nil, err
	}

	return result, nil
}

// exchangeSignaling connects to the relay's /signal WebSocket endpoint,
// sends our PeerInfo, and waits for the PunchSignal response.
func exchangeSignaling(ctx context.Context, relayURL, transferToken, sessionToken string,
	udpPort uint32, localIP string) (*protocol.PunchSignal, error) {

	// Build WebSocket URL
	wsURL := strings.Replace(relayURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/signal"

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{})
	if err != nil {
		return nil, fmt.Errorf("connecting to signaling server: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "signaling complete")

	conn.SetReadLimit(1024 * 1024) // 1 MB — signaling messages are tiny

	// Send the signaling handshake (JSON)
	req := signalRequest{
		SessionToken: sessionToken,
		Token:        transferToken,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling signal request: %w", err)
	}

	writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer writeCancel()
	if err := conn.Write(writeCtx, websocket.MessageText, reqBytes); err != nil {
		return nil, fmt.Errorf("sending signal handshake: %w", err)
	}

	// Read the handshake response
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	_, respData, err := conn.Read(readCtx)
	if err != nil {
		return nil, fmt.Errorf("reading signal response: %w", err)
	}

	var resp signalResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("decoding signal response: %w", err)
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("signaling rejected: %s", resp.Message)
	}

	// Send our PeerInfo as a binary protocol message
	peerInfo := &protocol.PeerInfo{
		UDPPort: udpPort,
		LocalIP: localIP,
	}
	peerInfoMsg := &protocol.Message{
		Type:    protocol.MsgPeerInfo,
		Payload: protocol.EncodePeerInfo(peerInfo),
	}
	peerInfoBytes := protocol.Encode(peerInfoMsg)

	writeCtx2, writeCancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer writeCancel2()
	if err := conn.Write(writeCtx2, websocket.MessageBinary, peerInfoBytes); err != nil {
		return nil, fmt.Errorf("sending PEER_INFO: %w", err)
	}

	// Wait for PunchSignal from the signaling server
	_, signalData, err := conn.Read(ctx) // Uses the punch timeout context
	if err != nil {
		return nil, fmt.Errorf("waiting for PUNCH_SIGNAL: %w", err)
	}

	// Parse the protocol message
	if len(signalData) < protocol.HeaderSize {
		return nil, fmt.Errorf("PUNCH_SIGNAL message too short: %d bytes", len(signalData))
	}
	msgType := protocol.MessageType(signalData[4])
	if msgType != protocol.MsgPunchSignal {
		return nil, fmt.Errorf("expected PUNCH_SIGNAL, got %s", msgType)
	}
	payload := signalData[protocol.HeaderSize:]

	punchSignal, err := protocol.DecodePunchSignal(payload)
	if err != nil {
		return nil, fmt.Errorf("decoding PUNCH_SIGNAL: %w", err)
	}

	return punchSignal, nil
}

// runPunchLoop executes the actual UDP hole punch attempts.
// It sends probes to both the peer's public and local addresses concurrently,
// while listening for incoming probes.
func runPunchLoop(ctx context.Context, conn net.PacketConn,
	peerPublic, peerLocal *net.UDPAddr) (*PunchResult, error) {

	type probeResult struct {
		addr *net.UDPAddr
		err  error
	}
	resultCh := make(chan probeResult, 1)

	// Goroutine: Listen for incoming probes
	go func() {
		buf := make([]byte, 256)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Set a short read deadline so we can check context cancellation
			conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue // Timeout, retry
				}
				return // Real error
			}

			probeType, err := decodeProbe(buf[:n])
			if err != nil {
				continue // Not a valid probe, ignore
			}

			peerUDPAddr := addr.(*net.UDPAddr)

			switch probeType {
			case ProbeTypeProbe:
				// Received a probe from the peer — send ACK back
				ackData, err := encodeProbe(ProbeTypeAck)
				if err != nil {
					continue
				}
				conn.WriteTo(ackData, addr)

				// Also report success — if we received their probe and sent ACK,
				// the hole is punched
				select {
				case resultCh <- probeResult{addr: peerUDPAddr}:
				default:
				}

			case ProbeTypeAck:
				// Received ACK — hole punch confirmed!
				select {
				case resultCh <- probeResult{addr: peerUDPAddr}:
				default:
				}
			}
		}
	}()

	// Goroutine: Send probes
	go func() {
		for attempt := 0; attempt < PunchAttempts; attempt++ {
			select {
			case <-ctx.Done():
				return
			default:
			}

			probeData, err := encodeProbe(ProbeTypeProbe)
			if err != nil {
				continue
			}

			// Send to the peer's public address (the one seen by the relay)
			conn.WriteTo(probeData, peerPublic)

			// Also send to the peer's local address (in case they're on the same LAN)
			if peerLocal != nil && !peerPublic.IP.Equal(peerLocal.IP) {
				conn.WriteTo(probeData, peerLocal)
			}

			if attempt < PunchAttempts-1 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(PunchInterval):
				}
			}
		}
	}()

	// Wait for a result or timeout
	select {
	case result := <-resultCh:
		if result.err != nil {
			return nil, result.err
		}
		// Convert net.PacketConn to *net.UDPConn
		udpConn, ok := conn.(*net.UDPConn)
		if !ok {
			// Wrap it — this shouldn't happen since we created it with ListenPacket
			return nil, fmt.Errorf("unexpected connection type")
		}
		return &PunchResult{
			Conn:     udpConn,
			PeerAddr: result.addr,
		}, nil

	case <-ctx.Done():
		return nil, fmt.Errorf("hole punch timed out after %s (%d attempts)", PunchTimeout, PunchAttempts)
	}
}

// UDPProbePort returns the port portion of a UDP address.
func UDPProbePort(addr *net.UDPAddr) uint32 {
	return uint32(addr.Port)
}

// ExtractPort extracts the port from a "host:port" string.
func ExtractPort(addr string) (uint32, error) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	var port uint32
	_, err = fmt.Sscanf(portStr, "%d", &port)
	return port, err
}

// ExtractIP extracts the IP from a "host:port" string.
func ExtractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// nonceFromProbe extracts the 8-byte nonce from a probe packet.
func nonceFromProbe(data []byte) uint64 {
	if len(data) < ProbeSize {
		return 0
	}
	return binary.BigEndian.Uint64(data[5:])
}
