package protocol

import (
	"encoding/binary"
	"fmt"
)

// ResumeRequestPayload is sent by the receiver to request resuming a transfer.
// It contains enough information for the sender to verify the resume point
// and skip ahead to the correct offset.
//
// Wire format:
//
//	[8 bytes:  SHA-256 prefix (first 8 bytes of full hash)]
//	[8 bytes:  byte offset to resume from (big-endian uint64)]
//	[8 bytes:  chunk index to resume from (big-endian uint64)]
//	[32 bytes: SHA-256 of the partial file received so far]
//
// Total: 56 bytes fixed size.
type ResumeRequestPayload struct {
	SHA256Prefix [8]byte  // First 8 bytes of the file's SHA-256 for identification
	Offset       int64    // Byte offset to resume from
	ChunkIndex   uint64   // Chunk index to resume from
	PartialHash  [32]byte // SHA-256 of bytes received so far (for verification)
}

const resumeRequestSize = 8 + 8 + 8 + 32 // 56 bytes

// EncodeResumeRequest serializes a ResumeRequestPayload.
func EncodeResumeRequest(p *ResumeRequestPayload) []byte {
	buf := make([]byte, resumeRequestSize)
	offset := 0

	copy(buf[offset:], p.SHA256Prefix[:])
	offset += 8

	binary.BigEndian.PutUint64(buf[offset:], uint64(p.Offset))
	offset += 8

	binary.BigEndian.PutUint64(buf[offset:], p.ChunkIndex)
	offset += 8

	copy(buf[offset:], p.PartialHash[:])

	return buf
}

// DecodeResumeRequest deserializes a ResumeRequestPayload.
func DecodeResumeRequest(data []byte) (*ResumeRequestPayload, error) {
	if len(data) < resumeRequestSize {
		return nil, fmt.Errorf("ResumeRequest payload too short: got %d bytes, need %d", len(data), resumeRequestSize)
	}

	p := &ResumeRequestPayload{}
	offset := 0

	copy(p.SHA256Prefix[:], data[offset:offset+8])
	offset += 8

	p.Offset = int64(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	p.ChunkIndex = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	copy(p.PartialHash[:], data[offset:offset+32])

	return p, nil
}

// ResumeAcceptPayload is sent by the sender to confirm a resume request.
// If Accepted is true, the sender will skip ahead to the specified offset.
//
// Wire format:
//
//	[8 bytes: confirmed byte offset (big-endian uint64)]
//	[8 bytes: confirmed chunk index (big-endian uint64)]
//	[1 byte:  accepted (1 = yes, 0 = no — restart from beginning)]
//
// Total: 17 bytes fixed size.
type ResumeAcceptPayload struct {
	Offset     int64  // Confirmed byte offset
	ChunkIndex uint64 // Confirmed chunk index
	Accepted   bool   // Whether resume was accepted
}

const resumeAcceptSize = 8 + 8 + 1 // 17 bytes

// EncodeResumeAccept serializes a ResumeAcceptPayload.
func EncodeResumeAccept(p *ResumeAcceptPayload) []byte {
	buf := make([]byte, resumeAcceptSize)
	offset := 0

	binary.BigEndian.PutUint64(buf[offset:], uint64(p.Offset))
	offset += 8

	binary.BigEndian.PutUint64(buf[offset:], p.ChunkIndex)
	offset += 8

	if p.Accepted {
		buf[offset] = 1
	}

	return buf
}

// DecodeResumeAccept deserializes a ResumeAcceptPayload.
func DecodeResumeAccept(data []byte) (*ResumeAcceptPayload, error) {
	if len(data) < resumeAcceptSize {
		return nil, fmt.Errorf("ResumeAccept payload too short: got %d bytes, need %d", len(data), resumeAcceptSize)
	}

	p := &ResumeAcceptPayload{}
	offset := 0

	p.Offset = int64(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	p.ChunkIndex = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	p.Accepted = data[offset] == 1

	return p, nil
}
