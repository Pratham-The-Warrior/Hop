package crypto

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"hash/crc32"
)

// ChunkCRC32 computes a CRC-32 checksum for a data chunk.
// Used for fast per-chunk corruption detection during transit.
func ChunkCRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// VerifyChunkCRC32 checks if a data chunk matches its expected CRC-32.
func VerifyChunkCRC32(data []byte, expected uint32) bool {
	return ChunkCRC32(data) == expected
}

// FileHasher provides streaming SHA-256 hash computation for full-file
// integrity verification. Chunks can be fed incrementally as they arrive,
// and the final hash is compared after all chunks are received.
type FileHasher struct {
	hasher hash.Hash
	total  int64
}

// NewFileHasher creates a new streaming SHA-256 hasher.
func NewFileHasher() *FileHasher {
	return &FileHasher{
		hasher: sha256.New(),
	}
}

// Write feeds data into the hash computation. Can be called multiple times
// with successive chunks. This implements io.Writer.
func (fh *FileHasher) Write(data []byte) (int, error) {
	n, err := fh.hasher.Write(data)
	fh.total += int64(n)
	return n, err
}

// Sum returns the final SHA-256 digest as a 32-byte array.
// Can only be called once after all data has been written.
func (fh *FileHasher) Sum() [32]byte {
	var digest [32]byte
	copy(digest[:], fh.hasher.Sum(nil))
	return digest
}

// SumHex returns the SHA-256 digest as a lowercase hex string.
func (fh *FileHasher) SumHex() string {
	return fmt.Sprintf("%x", fh.hasher.Sum(nil))
}

// SumPrefix returns a truncated hash prefix for display (e.g., "a3f2b8...c91d").
func (fh *FileHasher) SumPrefix() string {
	hex := fh.SumHex()
	if len(hex) < 12 {
		return hex
	}
	return hex[:6] + "..." + hex[len(hex)-4:]
}

// Verify compares the computed hash against an expected 32-byte digest.
func (fh *FileHasher) Verify(expected [32]byte) bool {
	computed := fh.Sum()
	return computed == expected
}

// BytesHashed returns the total number of bytes fed into the hasher.
func (fh *FileHasher) BytesHashed() int64 {
	return fh.total
}

// Reset clears the hasher state for reuse.
func (fh *FileHasher) Reset() {
	fh.hasher.Reset()
	fh.total = 0
}

// QuickSHA256 computes the SHA-256 hash of a byte slice in one shot.
// Useful for small payloads; for large files, use FileHasher instead.
func QuickSHA256(data []byte) [32]byte {
	return sha256.Sum256(data)
}
