package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/prathmeshsarda/hop/pkg/protocol"
)

const (
	// UDPMaxDatagram is the maximum safe UDP payload size.
	// Standard MTU (1500) - IP header (20) - UDP header (8) = 1472.
	// We use a slightly smaller value for safety with various network paths.
	UDPMaxDatagram = 1400

	// UDPReadTimeout is the default read timeout per datagram.
	UDPReadTimeout = 30 * time.Second

	// UDPWriteTimeout is the default write timeout per datagram.
	UDPWriteTimeout = 10 * time.Second

	// UDPRetries is the number of retransmission attempts for unacknowledged frames.
	UDPRetries = 5

	// UDPRetryInterval is the base interval between retransmission attempts.
	UDPRetryInterval = 500 * time.Millisecond

	// frameHeaderSize is the overhead per UDP frame:
	// [4 bytes: total message length (uint32)]
	// [4 bytes: sequence number (uint32)]
	// [2 bytes: fragment index (uint16)]
	// [2 bytes: total fragments (uint16)]
	// = 12 bytes
	frameHeaderSize = 12

	// frameMaxPayload is the maximum payload per UDP frame.
	frameMaxPayload = UDPMaxDatagram - frameHeaderSize

	// frameMagicAck is the sequence number used for ACK frames.
	frameMagicAck = 0xFFFFFFFF

	// ackFrameSize is the size of an ACK frame: header only, payload = acked seq number.
	ackFrameSize = frameHeaderSize + 4
)

// UDPTransport implements transfer.Transport over a direct UDP connection
// established by NAT hole punching. It provides:
//   - Message fragmentation and reassembly for messages larger than MTU
//   - Sequence numbers for ordering
//   - Stop-and-wait reliability (send frame, wait for ACK, retransmit if needed)
type UDPTransport struct {
	mu       sync.Mutex
	conn     *net.UDPConn
	peerAddr *net.UDPAddr
	seqSend  uint32 // Next sequence number for sending
	seqRecv  uint32 // Expected sequence number for receiving
	closed   bool
}

// NewUDPTransport wraps an established UDP connection into a Transport.
func NewUDPTransport(conn *net.UDPConn, peerAddr *net.UDPAddr) *UDPTransport {
	return &UDPTransport{
		conn:     conn,
		peerAddr: peerAddr,
		seqSend:  1,
		seqRecv:  1,
	}
}

// Send transmits a protocol message over UDP with fragmentation and reliability.
func (t *UDPTransport) Send(ctx context.Context, msg *protocol.Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("transport is closed")
	}

	// Encode the full protocol message
	data := protocol.Encode(msg)
	totalLen := uint32(len(data))

	// Fragment the message
	fragments := t.fragment(data, totalLen, t.seqSend)

	// Send each fragment with stop-and-wait reliability
	for _, frag := range fragments {
		if err := t.sendWithRetry(ctx, frag, t.seqSend); err != nil {
			return fmt.Errorf("sending fragment: %w", err)
		}
	}

	t.seqSend++
	return nil
}

// Receive reads the next protocol message from UDP, reassembling fragments.
func (t *UDPTransport) Receive(ctx context.Context) (*protocol.Message, error) {
	if t.closed {
		return nil, fmt.Errorf("transport is closed")
	}

	// Collect fragments until we have a complete message
	var fragments [][]byte
	var totalFragments uint16
	var totalMsgLen uint32

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Read a UDP datagram
		buf := make([]byte, UDPMaxDatagram+100) // Extra space for safety
		t.conn.SetReadDeadline(time.Now().Add(UDPReadTimeout))
		n, addr, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Check context before retrying
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			return nil, fmt.Errorf("reading UDP: %w", err)
		}

		// Verify it's from our peer
		if !addr.IP.Equal(t.peerAddr.IP) || addr.Port != t.peerAddr.Port {
			continue // Ignore packets from unknown sources
		}

		if n < frameHeaderSize {
			continue // Too short, skip
		}

		frame := buf[:n]

		// Parse frame header
		msgLen := binary.BigEndian.Uint32(frame[0:4])
		seqNum := binary.BigEndian.Uint32(frame[4:8])
		fragIdx := binary.BigEndian.Uint16(frame[8:10])
		fragTotal := binary.BigEndian.Uint16(frame[10:12])
		payload := frame[frameHeaderSize:]

		// Check if this is an ACK frame (used by Send, not Receive)
		if seqNum == frameMagicAck {
			// ACK frame received while we're trying to receive — ignore
			continue
		}

		// Send ACK for this frame
		t.sendACK(seqNum, addr)

		// Check if this is a retransmit of an old message
		if seqNum < t.seqRecv {
			continue // Already processed, ignore
		}

		// First fragment initializes our expectations
		if len(fragments) == 0 {
			totalFragments = fragTotal
			totalMsgLen = msgLen
			fragments = make([][]byte, totalFragments)
		}

		if fragIdx < totalFragments {
			fragments[fragIdx] = make([]byte, len(payload))
			copy(fragments[fragIdx], payload)
		}

		// Check if we have all fragments
		complete := true
		for _, f := range fragments {
			if f == nil {
				complete = false
				break
			}
		}

		if complete {
			// Reassemble the message
			reassembled := make([]byte, 0, totalMsgLen)
			for _, f := range fragments {
				reassembled = append(reassembled, f...)
			}

			// Trim to actual message length
			if uint32(len(reassembled)) > totalMsgLen {
				reassembled = reassembled[:totalMsgLen]
			}

			t.seqRecv = seqNum + 1

			// Parse the protocol message from the reassembled data
			if len(reassembled) < protocol.HeaderSize {
				return nil, fmt.Errorf("reassembled message too short: %d bytes", len(reassembled))
			}

			msgType := protocol.MessageType(reassembled[4])
			var msgPayload []byte
			if len(reassembled) > protocol.HeaderSize {
				msgPayload = reassembled[protocol.HeaderSize:]
			}

			return &protocol.Message{
				Type:    msgType,
				Payload: msgPayload,
			}, nil
		}
	}
}

// Close shuts down the UDP transport.
func (t *UDPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true
	return t.conn.Close()
}

// fragment splits a message into UDP-sized frames.
func (t *UDPTransport) fragment(data []byte, totalLen uint32, seqNum uint32) [][]byte {
	if len(data) <= frameMaxPayload {
		// Single frame, no fragmentation needed
		frame := make([]byte, frameHeaderSize+len(data))
		binary.BigEndian.PutUint32(frame[0:4], totalLen)
		binary.BigEndian.PutUint32(frame[4:8], seqNum)
		binary.BigEndian.PutUint16(frame[8:10], 0)  // fragment index
		binary.BigEndian.PutUint16(frame[10:12], 1)  // total fragments
		copy(frame[frameHeaderSize:], data)
		return [][]byte{frame}
	}

	// Calculate number of fragments
	numFrags := (len(data) + frameMaxPayload - 1) / frameMaxPayload
	frames := make([][]byte, numFrags)

	for i := 0; i < numFrags; i++ {
		start := i * frameMaxPayload
		end := start + frameMaxPayload
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]

		frame := make([]byte, frameHeaderSize+len(chunk))
		binary.BigEndian.PutUint32(frame[0:4], totalLen)
		binary.BigEndian.PutUint32(frame[4:8], seqNum)
		binary.BigEndian.PutUint16(frame[8:10], uint16(i))
		binary.BigEndian.PutUint16(frame[10:12], uint16(numFrags))
		copy(frame[frameHeaderSize:], chunk)

		frames[i] = frame
	}

	return frames
}

// sendWithRetry sends all fragments for a sequence and waits for an ACK.
// Retransmits up to UDPRetries times with exponential backoff.
func (t *UDPTransport) sendWithRetry(ctx context.Context, frame []byte, seqNum uint32) error {
	for attempt := 0; attempt <= UDPRetries; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Send the frame
		t.conn.SetWriteDeadline(time.Now().Add(UDPWriteTimeout))
		_, err := t.conn.WriteToUDP(frame, t.peerAddr)
		if err != nil {
			return fmt.Errorf("writing UDP frame: %w", err)
		}

		// Wait for ACK with timeout
		ackTimeout := UDPRetryInterval * time.Duration(1<<uint(attempt)) // Exponential backoff
		if ackTimeout > 5*time.Second {
			ackTimeout = 5 * time.Second
		}

		acked, err := t.waitForACK(ctx, seqNum, ackTimeout)
		if err != nil {
			return err
		}
		if acked {
			return nil
		}

		// No ACK received, retransmit
	}

	return fmt.Errorf("no ACK received after %d retransmission attempts", UDPRetries)
}

// waitForACK waits for an ACK frame matching the given sequence number.
func (t *UDPTransport) waitForACK(ctx context.Context, seqNum uint32, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, UDPMaxDatagram)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		t.conn.SetReadDeadline(time.Now().Add(remaining))

		n, addr, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return false, nil // Timeout — no ACK
			}
			return false, fmt.Errorf("reading ACK: %w", err)
		}

		// Verify it's from our peer
		if !addr.IP.Equal(t.peerAddr.IP) || addr.Port != t.peerAddr.Port {
			continue
		}

		if n < frameHeaderSize+4 {
			continue
		}

		// Check if it's an ACK frame
		ackSeq := binary.BigEndian.Uint32(buf[4:8])
		if ackSeq == frameMagicAck {
			// Extract the acked sequence number from the payload
			ackedSeq := binary.BigEndian.Uint32(buf[frameHeaderSize : frameHeaderSize+4])
			if ackedSeq == seqNum {
				return true, nil
			}
		}
	}

	return false, nil
}

// sendACK sends an ACK frame for the given sequence number.
func (t *UDPTransport) sendACK(seqNum uint32, addr *net.UDPAddr) {
	frame := make([]byte, ackFrameSize)
	binary.BigEndian.PutUint32(frame[0:4], 4)             // Payload length
	binary.BigEndian.PutUint32(frame[4:8], frameMagicAck) // ACK magic
	binary.BigEndian.PutUint16(frame[8:10], 0)            // Fragment 0
	binary.BigEndian.PutUint16(frame[10:12], 1)           // 1 fragment
	binary.BigEndian.PutUint32(frame[frameHeaderSize:], seqNum) // Acked seq

	t.conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	t.conn.WriteTo(frame, addr)
}
