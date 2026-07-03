package transfer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	hopcrypto "github.com/prathmeshsarda/hop/pkg/crypto"
	"github.com/prathmeshsarda/hop/pkg/protocol"
)

// ResumeMarker stores the state of a partially received file for resume.
// It is written as JSON to `.hop-resume-<sha256-prefix>` in the output directory.
type ResumeMarker struct {
	SHA256      string `json:"sha256"`       // Full hex SHA-256 of the complete file
	Offset      int64  `json:"offset"`       // Byte offset of the next byte to receive
	ChunkIndex  uint64 `json:"chunk_index"`  // Next chunk index to receive
	PartialHash string `json:"partial_hash"` // Hex SHA-256 of bytes received so far
	ChunkSize   uint32 `json:"chunk_size"`   // Chunk size used for the transfer
	FileName    string `json:"file_name"`    // Name of the file being transferred
	Timestamp   string `json:"timestamp"`    // ISO-8601 timestamp of last update
}

// MarkerFileName returns the marker file name for a given file SHA-256 hash.
// Uses the first 12 hex characters (6 bytes) of the hash as a prefix.
func MarkerFileName(sha256 [32]byte) string {
	return fmt.Sprintf(".hop-resume-%x", sha256[:6])
}

// markerPath returns the full path to the marker file in the given directory.
func markerPath(dir string, sha256 [32]byte) string {
	return filepath.Join(dir, MarkerFileName(sha256))
}

// WriteMarker atomically writes a resume marker to disk.
// It writes to a temporary file first and renames for crash safety.
func WriteMarker(dir string, sha256 [32]byte, offset int64, chunkIndex uint64, partialHash [32]byte, chunkSize uint32, fileName string) error {
	marker := &ResumeMarker{
		SHA256:      fmt.Sprintf("%x", sha256),
		Offset:      offset,
		ChunkIndex:  chunkIndex,
		PartialHash: fmt.Sprintf("%x", partialHash),
		ChunkSize:   chunkSize,
		FileName:    fileName,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling resume marker: %w", err)
	}

	targetPath := markerPath(dir, sha256)
	tmpPath := targetPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing resume marker temp file: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		// Fallback: try direct write if rename fails (some filesystems)
		os.Remove(tmpPath)
		if err := os.WriteFile(targetPath, data, 0644); err != nil {
			return fmt.Errorf("writing resume marker: %w", err)
		}
	}

	return nil
}

// ReadMarker reads and parses a resume marker from disk.
// Returns nil if the marker does not exist.
func ReadMarker(dir string, sha256 [32]byte) (*ResumeMarker, error) {
	data, err := os.ReadFile(markerPath(dir, sha256))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading resume marker: %w", err)
	}

	marker := &ResumeMarker{}
	if err := json.Unmarshal(data, marker); err != nil {
		return nil, fmt.Errorf("parsing resume marker: %w", err)
	}

	return marker, nil
}

// DeleteMarker removes the resume marker for a completed transfer.
func DeleteMarker(dir string, sha256 [32]byte) error {
	path := markerPath(dir, sha256)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing resume marker: %w", err)
	}
	return nil
}

// ResumeState contains the validated information needed to resume a transfer.
type ResumeState struct {
	Offset      int64    // Byte offset to resume from
	ChunkIndex  uint64   // Chunk index to resume from
	PartialHash [32]byte // SHA-256 of bytes received so far
}

// DetectResumable checks if a partial file and valid marker exist for a given
// transfer offer. Returns a ResumeState if the transfer can be resumed, or
// nil if the transfer should start fresh.
//
// Validation checks:
//  1. Marker file exists for this SHA-256
//  2. Partial output file exists
//  3. Partial file size matches the marker offset
//  4. Chunk size matches the offer's chunk size
//  5. File name matches the offer
//  6. SHA-256 of the partial file matches the marker's partial hash
func DetectResumable(dir string, offer *protocol.TransferOffer) (*ResumeState, error) {
	// Read the marker
	marker, err := ReadMarker(dir, offer.SHA256)
	if err != nil {
		return nil, fmt.Errorf("reading marker: %w", err)
	}
	if marker == nil {
		return nil, nil // No marker → no resume
	}

	// Check file name matches
	if marker.FileName != offer.FileName {
		return nil, nil // Different file → no resume
	}

	// Check chunk size matches
	if marker.ChunkSize != offer.ChunkSize {
		return nil, nil // Different chunk size → no resume
	}

	// Check the partial file exists and size matches
	outPath := filepath.Join(dir, offer.FileName)
	info, err := os.Stat(outPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Partial file is gone — clean up the orphaned marker
			_ = DeleteMarker(dir, offer.SHA256)
			return nil, nil
		}
		return nil, fmt.Errorf("stat partial file: %w", err)
	}

	if info.Size() != marker.Offset {
		// File size doesn't match marker — can't safely resume
		// Clean up and start fresh
		_ = DeleteMarker(dir, offer.SHA256)
		return nil, nil
	}

	// Verify the partial hash by re-hashing the existing file
	partialHash, err := hashPartialFile(outPath, marker.Offset)
	if err != nil {
		return nil, fmt.Errorf("hashing partial file: %w", err)
	}

	expectedHash := fmt.Sprintf("%x", partialHash)
	if expectedHash != marker.PartialHash {
		// Hash mismatch — file was modified or corrupted
		_ = DeleteMarker(dir, offer.SHA256)
		return nil, nil
	}

	return &ResumeState{
		Offset:      marker.Offset,
		ChunkIndex:  marker.ChunkIndex,
		PartialHash: partialHash,
	}, nil
}

// hashPartialFile computes the SHA-256 of the first `length` bytes of a file.
func hashPartialFile(path string, length int64) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	hasher := hopcrypto.NewFileHasher()
	buf := make([]byte, DefaultChunkSize)
	var read int64

	for read < length {
		toRead := int64(len(buf))
		if toRead > length-read {
			toRead = length - read
		}

		n, err := f.Read(buf[:toRead])
		if err != nil && err != io.EOF {
			return [32]byte{}, fmt.Errorf("reading file: %w", err)
		}
		if n == 0 {
			break
		}

		hasher.Write(buf[:n])
		read += int64(n)
	}

	return hasher.Sum(), nil
}
