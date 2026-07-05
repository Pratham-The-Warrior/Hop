package protocol

import (
	"encoding/binary"
	"fmt"
)

// BrowserInfoResponse contains file metadata sent from the sender to the relay
// in response to a MsgBrowserInfoReq. The relay uses this to render the
// browser download page.
//
// Wire format:
//
//	[2 bytes:  file name length (big-endian uint16)]
//	[N bytes:  file name (UTF-8)]
//	[8 bytes:  file size (big-endian int64)]
//	[32 bytes: SHA-256 hash]
//	[1 byte:   compressed flag (0 or 1)]
//	[4 bytes:  chunk size (big-endian uint32)]
type BrowserInfoResponse struct {
	FileName   string
	FileSize   int64
	SHA256     [32]byte
	Compressed bool
	ChunkSize  uint32
}

// EncodeBrowserInfoResponse serializes a BrowserInfoResponse into a message payload.
func EncodeBrowserInfoResponse(r *BrowserInfoResponse) []byte {
	nameBytes := []byte(r.FileName)
	// Layout: [2 name_len][name][8 file_size][32 sha256][1 compressed][4 chunk_size]
	buf := make([]byte, 2+len(nameBytes)+8+32+1+4)
	offset := 0

	binary.BigEndian.PutUint16(buf[offset:], uint16(len(nameBytes)))
	offset += 2
	copy(buf[offset:], nameBytes)
	offset += len(nameBytes)

	binary.BigEndian.PutUint64(buf[offset:], uint64(r.FileSize))
	offset += 8

	copy(buf[offset:], r.SHA256[:])
	offset += 32

	if r.Compressed {
		buf[offset] = 1
	}
	offset++

	binary.BigEndian.PutUint32(buf[offset:], r.ChunkSize)

	return buf
}

// DecodeBrowserInfoResponse deserializes a BrowserInfoResponse from a message payload.
func DecodeBrowserInfoResponse(data []byte) (*BrowserInfoResponse, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("BrowserInfoResponse too short")
	}

	offset := 0
	nameLen := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	if offset+nameLen+8+32+1+4 > len(data) {
		return nil, fmt.Errorf("BrowserInfoResponse truncated")
	}

	r := &BrowserInfoResponse{}
	r.FileName = string(data[offset : offset+nameLen])
	offset += nameLen

	r.FileSize = int64(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	copy(r.SHA256[:], data[offset:offset+32])
	offset += 32

	r.Compressed = data[offset] == 1
	offset++

	r.ChunkSize = binary.BigEndian.Uint32(data[offset:])

	return r, nil
}

// BrowserDownloadStart signals that a browser download is beginning.
// Sent from the relay to the sender. Contains no additional data beyond
// the message type — the sender already knows its file metadata.
//
// Wire format: empty payload (0 bytes).

// EncodeBrowserDownloadStart returns an empty payload for the download start signal.
func EncodeBrowserDownloadStart() []byte {
	return nil
}

// BrowserDownloadCancel signals that a browser client disconnected mid-download.
// Sent from the relay to the sender so it can stop streaming chunks.
//
// Wire format: empty payload (0 bytes).

// EncodeBrowserDownloadCancel returns an empty payload for the cancel signal.
func EncodeBrowserDownloadCancel() []byte {
	return nil
}
