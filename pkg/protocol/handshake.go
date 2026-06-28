package protocol

import (
	"encoding/binary"
	"fmt"
)

// HopHello contains the data exchanged in the initial protocol handshake.
// Both HOP_HELLO and HOP_HELLO_ACK use the same payload structure since
// both sides need to exchange identical information (public key, version,
// and feature flags).
//
// Wire format:
//
//	[32 bytes: X25519 public key]
//	[2 bytes:  protocol major version (big-endian uint16)]
//	[2 bytes:  protocol minor version (big-endian uint16)]
//	[4 bytes:  feature flags (big-endian uint32)]
//
// Total: 40 bytes fixed size.
type HopHello struct {
	PublicKey    [32]byte       // X25519 public key for ECDH key exchange
	Version     ProtocolVersion
	Features    FeatureFlags
}

const hopHelloSize = 32 + 2 + 2 + 4 // 40 bytes

// EncodeHopHello serializes a HopHello into a message payload.
func EncodeHopHello(h *HopHello) []byte {
	buf := make([]byte, hopHelloSize)
	offset := 0

	copy(buf[offset:], h.PublicKey[:])
	offset += 32

	binary.BigEndian.PutUint16(buf[offset:], uint16(h.Version.Major))
	offset += 2

	binary.BigEndian.PutUint16(buf[offset:], uint16(h.Version.Minor))
	offset += 2

	binary.BigEndian.PutUint32(buf[offset:], uint32(h.Features))

	return buf
}

// DecodeHopHello deserializes a HopHello from a message payload.
func DecodeHopHello(data []byte) (*HopHello, error) {
	if len(data) < hopHelloSize {
		return nil, fmt.Errorf("HopHello payload too short: got %d bytes, need %d", len(data), hopHelloSize)
	}

	h := &HopHello{}
	offset := 0

	copy(h.PublicKey[:], data[offset:offset+32])
	offset += 32

	h.Version.Major = int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	h.Version.Minor = int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	h.Features = FeatureFlags(binary.BigEndian.Uint32(data[offset:]))

	return h, nil
}

// TransferAcceptPayload contains data sent with TRANSFER_ACCEPT.
// Currently empty but structured for future extensibility (e.g., resume offset).
type TransferAcceptPayload struct {
	ResumeOffset int64 // 0 means start from beginning
}

const transferAcceptSize = 8

// EncodeTransferAccept serializes a TransferAcceptPayload.
func EncodeTransferAccept(p *TransferAcceptPayload) []byte {
	buf := make([]byte, transferAcceptSize)
	binary.BigEndian.PutUint64(buf[0:], uint64(p.ResumeOffset))
	return buf
}

// DecodeTransferAccept deserializes a TransferAcceptPayload.
func DecodeTransferAccept(data []byte) (*TransferAcceptPayload, error) {
	if len(data) < transferAcceptSize {
		return nil, fmt.Errorf("TransferAccept payload too short: got %d bytes, need %d", len(data), transferAcceptSize)
	}
	return &TransferAcceptPayload{
		ResumeOffset: int64(binary.BigEndian.Uint64(data[0:])),
	}, nil
}

// TransferCompletePayload contains the final SHA-256 hash for verification.
type TransferCompletePayload struct {
	SHA256       [32]byte
	TotalChunks  uint64
	TotalBytes   uint64
}

const transferCompleteSize = 32 + 8 + 8

// EncodeTransferComplete serializes a TransferCompletePayload.
func EncodeTransferComplete(p *TransferCompletePayload) []byte {
	buf := make([]byte, transferCompleteSize)
	offset := 0

	copy(buf[offset:], p.SHA256[:])
	offset += 32

	binary.BigEndian.PutUint64(buf[offset:], p.TotalChunks)
	offset += 8

	binary.BigEndian.PutUint64(buf[offset:], p.TotalBytes)

	return buf
}

// DecodeTransferComplete deserializes a TransferCompletePayload.
func DecodeTransferComplete(data []byte) (*TransferCompletePayload, error) {
	if len(data) < transferCompleteSize {
		return nil, fmt.Errorf("TransferComplete payload too short: got %d bytes, need %d", len(data), transferCompleteSize)
	}

	p := &TransferCompletePayload{}
	offset := 0

	copy(p.SHA256[:], data[offset:offset+32])
	offset += 32

	p.TotalChunks = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	p.TotalBytes = binary.BigEndian.Uint64(data[offset:])

	return p, nil
}
