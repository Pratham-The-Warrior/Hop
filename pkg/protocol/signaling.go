package protocol

import (
	"encoding/binary"
	"fmt"
)

// PeerInfo is sent by each client to the signaling server during NAT hole
// punch coordination. It contains the client's UDP listen port and local
// (private) IP address so the server can relay this information to the peer.
//
// Wire format:
//
//	[4 bytes: UDP listen port (big-endian uint32)]
//	[4 bytes: local IP length (big-endian uint32)]
//	[N bytes: local IP string (UTF-8)]
type PeerInfo struct {
	UDPPort uint32 // The local UDP port this peer is listening on
	LocalIP string // The peer's local/private IP address
}

// EncodePeerInfo serializes a PeerInfo into a message payload.
func EncodePeerInfo(p *PeerInfo) []byte {
	ipBytes := []byte(p.LocalIP)
	buf := make([]byte, 4+4+len(ipBytes))
	offset := 0

	binary.BigEndian.PutUint32(buf[offset:], p.UDPPort)
	offset += 4

	binary.BigEndian.PutUint32(buf[offset:], uint32(len(ipBytes)))
	offset += 4

	copy(buf[offset:], ipBytes)

	return buf
}

// DecodePeerInfo deserializes a PeerInfo from a message payload.
func DecodePeerInfo(data []byte) (*PeerInfo, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("PeerInfo payload too short: got %d bytes, need at least 8", len(data))
	}

	offset := 0
	p := &PeerInfo{}

	p.UDPPort = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	ipLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	if offset+ipLen > len(data) {
		return nil, fmt.Errorf("PeerInfo payload truncated: need %d more bytes for IP", ipLen-(len(data)-offset))
	}

	p.LocalIP = string(data[offset : offset+ipLen])

	return p, nil
}

// PunchSignal is sent by the signaling server to each client when both peers
// have registered. It contains the peer's public (observed) and private
// (self-reported) network addresses for hole punch attempts.
//
// Wire format:
//
//	[4 bytes: peer public port (big-endian uint32)]
//	[4 bytes: peer public IP length (big-endian uint32)]
//	[N bytes: peer public IP string (UTF-8)]
//	[4 bytes: peer local/private port (big-endian uint32)]
//	[4 bytes: peer local IP length (big-endian uint32)]
//	[M bytes: peer local IP string (UTF-8)]
type PunchSignal struct {
	PublicIP   string // The peer's public (NAT-observed) IP address
	PublicPort uint32 // The peer's public (NAT-observed) UDP port
	LocalIP    string // The peer's self-reported local/private IP
	LocalPort  uint32 // The peer's self-reported local UDP port
}

// EncodePunchSignal serializes a PunchSignal into a message payload.
func EncodePunchSignal(s *PunchSignal) []byte {
	pubIPBytes := []byte(s.PublicIP)
	localIPBytes := []byte(s.LocalIP)
	buf := make([]byte, 4+4+len(pubIPBytes)+4+4+len(localIPBytes))
	offset := 0

	binary.BigEndian.PutUint32(buf[offset:], s.PublicPort)
	offset += 4

	binary.BigEndian.PutUint32(buf[offset:], uint32(len(pubIPBytes)))
	offset += 4

	copy(buf[offset:], pubIPBytes)
	offset += len(pubIPBytes)

	binary.BigEndian.PutUint32(buf[offset:], s.LocalPort)
	offset += 4

	binary.BigEndian.PutUint32(buf[offset:], uint32(len(localIPBytes)))
	offset += 4

	copy(buf[offset:], localIPBytes)

	return buf
}

// DecodePunchSignal deserializes a PunchSignal from a message payload.
func DecodePunchSignal(data []byte) (*PunchSignal, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("PunchSignal payload too short: got %d bytes, need at least 16", len(data))
	}

	offset := 0
	s := &PunchSignal{}

	s.PublicPort = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	pubIPLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	if offset+pubIPLen > len(data) {
		return nil, fmt.Errorf("PunchSignal payload truncated at public IP")
	}
	s.PublicIP = string(data[offset : offset+pubIPLen])
	offset += pubIPLen

	if offset+8 > len(data) {
		return nil, fmt.Errorf("PunchSignal payload truncated at local port")
	}

	s.LocalPort = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	localIPLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	if offset+localIPLen > len(data) {
		return nil, fmt.Errorf("PunchSignal payload truncated at local IP")
	}
	s.LocalIP = string(data[offset : offset+localIPLen])

	return s, nil
}
