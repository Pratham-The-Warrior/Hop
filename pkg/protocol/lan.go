package protocol

import (
	"encoding/binary"
	"fmt"
)

// LAN discovery constants.
const (
	// LANMagic is the 4-byte magic prefix for LAN discovery packets.
	LANMagic = "HOP!"

	// LANMagicSize is the byte length of the magic prefix.
	LANMagicSize = 4
)

// LANPacketType identifies the kind of LAN discovery packet.
type LANPacketType byte

const (
	LANPacketProbe    LANPacketType = 0x01 // Broadcast probe: "is anyone sharing this token?"
	LANPacketResponse LANPacketType = 0x02 // Unicast response: "yes, connect to me here"
)

// LANProbe is broadcast on the local network to discover a peer sharing
// the same transfer token.
//
// Wire format:
//
//	[4 bytes: magic "HOP!"]
//	[1 byte:  packet type (0x01)]
//	[2 bytes: token length (big-endian uint16)]
//	[N bytes: token string (UTF-8)]
//	[4 bytes: TCP listen port (big-endian uint32)]
type LANProbe struct {
	Token   string // Transfer token (word-word-NN)
	TCPPort uint32 // Port the sender's TCP listener is bound to
}

// EncodeLANProbe serializes a LANProbe into a UDP packet payload.
func EncodeLANProbe(p *LANProbe) []byte {
	tokenBytes := []byte(p.Token)
	buf := make([]byte, LANMagicSize+1+2+len(tokenBytes)+4)
	offset := 0

	copy(buf[offset:], LANMagic)
	offset += LANMagicSize

	buf[offset] = byte(LANPacketProbe)
	offset++

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(tokenBytes)))
	offset += 2

	copy(buf[offset:], tokenBytes)
	offset += len(tokenBytes)

	binary.BigEndian.PutUint32(buf[offset:], p.TCPPort)

	return buf
}

// DecodeLANProbe deserializes a LANProbe from a UDP packet payload.
// Returns an error if the packet is malformed or has the wrong type.
func DecodeLANProbe(data []byte) (*LANProbe, error) {
	if len(data) < LANMagicSize+1+2+4 {
		return nil, fmt.Errorf("LAN probe too short: %d bytes", len(data))
	}

	if string(data[:LANMagicSize]) != LANMagic {
		return nil, fmt.Errorf("LAN probe: invalid magic")
	}

	if LANPacketType(data[LANMagicSize]) != LANPacketProbe {
		return nil, fmt.Errorf("LAN probe: wrong packet type 0x%02x", data[LANMagicSize])
	}

	offset := LANMagicSize + 1
	tokenLen := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	if offset+tokenLen+4 > len(data) {
		return nil, fmt.Errorf("LAN probe truncated")
	}

	p := &LANProbe{}
	p.Token = string(data[offset : offset+tokenLen])
	offset += tokenLen

	p.TCPPort = binary.BigEndian.Uint32(data[offset:])

	return p, nil
}

// LANResponse is sent as a unicast reply to a LANProbe when the receiver
// recognizes the transfer token.
//
// Wire format: identical structure to LANProbe but with type 0x02.
type LANResponse struct {
	Token   string // Transfer token (echoed back for verification)
	TCPPort uint32 // Port the responder's TCP listener is bound to
}

// EncodeLANResponse serializes a LANResponse into a UDP packet payload.
func EncodeLANResponse(r *LANResponse) []byte {
	tokenBytes := []byte(r.Token)
	buf := make([]byte, LANMagicSize+1+2+len(tokenBytes)+4)
	offset := 0

	copy(buf[offset:], LANMagic)
	offset += LANMagicSize

	buf[offset] = byte(LANPacketResponse)
	offset++

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(tokenBytes)))
	offset += 2

	copy(buf[offset:], tokenBytes)
	offset += len(tokenBytes)

	binary.BigEndian.PutUint32(buf[offset:], r.TCPPort)

	return buf
}

// DecodeLANResponse deserializes a LANResponse from a UDP packet payload.
func DecodeLANResponse(data []byte) (*LANResponse, error) {
	if len(data) < LANMagicSize+1+2+4 {
		return nil, fmt.Errorf("LAN response too short: %d bytes", len(data))
	}

	if string(data[:LANMagicSize]) != LANMagic {
		return nil, fmt.Errorf("LAN response: invalid magic")
	}

	if LANPacketType(data[LANMagicSize]) != LANPacketResponse {
		return nil, fmt.Errorf("LAN response: wrong packet type 0x%02x", data[LANMagicSize])
	}

	offset := LANMagicSize + 1
	tokenLen := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	if offset+tokenLen+4 > len(data) {
		return nil, fmt.Errorf("LAN response truncated")
	}

	r := &LANResponse{}
	r.Token = string(data[offset : offset+tokenLen])
	offset += tokenLen

	r.TCPPort = binary.BigEndian.Uint32(data[offset:])

	return r, nil
}
