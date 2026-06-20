package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MessageType identifies the kind of protocol message on the wire.
type MessageType uint8

const (
	// Handshake messages
	MsgHopHello       MessageType = 0x01
	MsgHopHelloAck    MessageType = 0x02

	// Transfer lifecycle
	MsgTransferOffer    MessageType = 0x10
	MsgTransferAccept   MessageType = 0x11
	MsgTransferReject   MessageType = 0x12
	MsgTransferCancel   MessageType = 0x13
	MsgTransferComplete MessageType = 0x14

	// Data flow
	MsgChunkData MessageType = 0x20
	MsgChunkAck  MessageType = 0x21

	// Resume
	MsgResumeRequest MessageType = 0x30
	MsgResumeAccept  MessageType = 0x31

	// Tunnel
	MsgTunnelRequest  MessageType = 0x40
	MsgTunnelResponse MessageType = 0x41
	MsgTunnelData     MessageType = 0x42

	// Session
	MsgSessionAuth    MessageType = 0x50
	MsgSessionToken   MessageType = 0x51

	// Signaling (for NAT hole punching)
	MsgPeerInfo       MessageType = 0x60
	MsgPunchSignal    MessageType = 0x61
)

// String returns a human-readable name for the message type.
func (mt MessageType) String() string {
	switch mt {
	case MsgHopHello:
		return "HOP_HELLO"
	case MsgHopHelloAck:
		return "HOP_HELLO_ACK"
	case MsgTransferOffer:
		return "TRANSFER_OFFER"
	case MsgTransferAccept:
		return "TRANSFER_ACCEPT"
	case MsgTransferReject:
		return "TRANSFER_REJECT"
	case MsgTransferCancel:
		return "TRANSFER_CANCELLED"
	case MsgTransferComplete:
		return "TRANSFER_COMPLETE"
	case MsgChunkData:
		return "CHUNK_DATA"
	case MsgChunkAck:
		return "CHUNK_ACK"
	case MsgResumeRequest:
		return "RESUME_REQUEST"
	case MsgResumeAccept:
		return "RESUME_ACCEPT"
	case MsgTunnelRequest:
		return "TUNNEL_REQUEST"
	case MsgTunnelResponse:
		return "TUNNEL_RESPONSE"
	case MsgTunnelData:
		return "TUNNEL_DATA"
	case MsgSessionAuth:
		return "SESSION_AUTH"
	case MsgSessionToken:
		return "SESSION_TOKEN"
	case MsgPeerInfo:
		return "PEER_INFO"
	case MsgPunchSignal:
		return "PUNCH_SIGNAL"
	default:
		return fmt.Sprintf("UNKNOWN(0x%02x)", uint8(mt))
	}
}

// Message represents a single protocol message with a type and payload.
//
// Wire format (length-prefixed binary framing):
//   [4 bytes: payload length (big-endian uint32)]
//   [1 byte: message type]
//   [N bytes: payload]
//
// Maximum payload size is 16 MB to prevent memory exhaustion.
type Message struct {
	Type    MessageType
	Payload []byte
}

const (
	// HeaderSize is the fixed header: 4 bytes length + 1 byte type
	HeaderSize = 5

	// MaxPayloadSize prevents memory exhaustion from malicious/corrupt frames
	MaxPayloadSize = 16 * 1024 * 1024 // 16 MB
)

// Encode serializes a Message into its wire format.
func Encode(msg *Message) []byte {
	payloadLen := len(msg.Payload)
	buf := make([]byte, HeaderSize+payloadLen)

	// Length prefix (does NOT include the length field itself)
	binary.BigEndian.PutUint32(buf[0:4], uint32(1+payloadLen)) // type + payload
	buf[4] = byte(msg.Type)

	if payloadLen > 0 {
		copy(buf[5:], msg.Payload)
	}

	return buf
}

// Decode reads a single Message from the given reader.
func Decode(r io.Reader) (*Message, error) {
	// Read the 4-byte length prefix
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("reading message length: %w", err)
	}

	frameLen := binary.BigEndian.Uint32(lenBuf[:])
	if frameLen < 1 {
		return nil, fmt.Errorf("invalid frame length: %d", frameLen)
	}
	if frameLen > MaxPayloadSize+1 {
		return nil, fmt.Errorf("frame too large: %d bytes (max %d)", frameLen, MaxPayloadSize+1)
	}

	// Read the frame (type + payload)
	frame := make([]byte, frameLen)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, fmt.Errorf("reading message frame: %w", err)
	}

	msg := &Message{
		Type: MessageType(frame[0]),
	}
	if len(frame) > 1 {
		msg.Payload = frame[1:]
	}

	return msg, nil
}

// --- Structured Payload Helpers ---

// TransferOffer contains the metadata for a file transfer offer.
type TransferOffer struct {
	FileName   string
	FileSize   int64
	SHA256     [32]byte
	IsDir      bool
	ChunkSize  uint32
	Compressed bool
}

// EncodeTransferOffer serializes a TransferOffer into a message payload.
func EncodeTransferOffer(offer *TransferOffer) []byte {
	nameBytes := []byte(offer.FileName)
	// Layout: [2 name_len][name][8 file_size][32 sha256][1 is_dir][4 chunk_size][1 compressed]
	buf := make([]byte, 2+len(nameBytes)+8+32+1+4+1)
	offset := 0

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(nameBytes)))
	offset += 2
	copy(buf[offset:], nameBytes)
	offset += len(nameBytes)

	binary.BigEndian.PutUint64(buf[offset:], uint64(offer.FileSize))
	offset += 8

	copy(buf[offset:], offer.SHA256[:])
	offset += 32

	if offer.IsDir {
		buf[offset] = 1
	}
	offset++

	binary.BigEndian.PutUint32(buf[offset:], offer.ChunkSize)
	offset += 4

	if offer.Compressed {
		buf[offset] = 1
	}

	return buf
}

// DecodeTransferOffer deserializes a TransferOffer from a message payload.
func DecodeTransferOffer(data []byte) (*TransferOffer, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("transfer offer too short")
	}

	offset := 0
	nameLen := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	if offset+nameLen+8+32+1+4+1 > len(data) {
		return nil, fmt.Errorf("transfer offer truncated")
	}

	offer := &TransferOffer{}
	offer.FileName = string(data[offset : offset+nameLen])
	offset += nameLen

	offer.FileSize = int64(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	copy(offer.SHA256[:], data[offset:offset+32])
	offset += 32

	offer.IsDir = data[offset] == 1
	offset++

	offer.ChunkSize = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	offer.Compressed = data[offset] == 1

	return offer, nil
}

// ChunkHeader is prepended to each CHUNK_DATA message payload.
type ChunkHeader struct {
	Index    uint64
	Size     uint32
	CRC32    uint32
}

// EncodeChunkHeader serializes a ChunkHeader.
func EncodeChunkHeader(hdr *ChunkHeader) []byte {
	buf := make([]byte, 16) // 8 + 4 + 4
	binary.BigEndian.PutUint64(buf[0:], hdr.Index)
	binary.BigEndian.PutUint32(buf[8:], hdr.Size)
	binary.BigEndian.PutUint32(buf[12:], hdr.CRC32)
	return buf
}

// DecodeChunkHeader deserializes a ChunkHeader.
func DecodeChunkHeader(data []byte) (*ChunkHeader, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("chunk header too short: %d bytes", len(data))
	}
	return &ChunkHeader{
		Index: binary.BigEndian.Uint64(data[0:]),
		Size:  binary.BigEndian.Uint32(data[8:]),
		CRC32: binary.BigEndian.Uint32(data[12:]),
	}, nil
}
