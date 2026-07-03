package transfer

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	hopcrypto "github.com/prathmeshsarda/hop/pkg/crypto"
	"github.com/prathmeshsarda/hop/pkg/protocol"
)

func TestMarkerWriteRead(t *testing.T) {
	dir := t.TempDir()

	var sha256 [32]byte
	rand.Read(sha256[:])

	var partialHash [32]byte
	rand.Read(partialHash[:])

	err := WriteMarker(dir, sha256, 209715200, 200, partialHash, DefaultChunkSize, "big.zip")
	if err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	marker, err := ReadMarker(dir, sha256)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if marker == nil {
		t.Fatal("expected marker, got nil")
	}

	if marker.Offset != 209715200 {
		t.Errorf("Offset = %d, want 209715200", marker.Offset)
	}
	if marker.ChunkIndex != 200 {
		t.Errorf("ChunkIndex = %d, want 200", marker.ChunkIndex)
	}
	if marker.ChunkSize != DefaultChunkSize {
		t.Errorf("ChunkSize = %d, want %d", marker.ChunkSize, DefaultChunkSize)
	}
	if marker.FileName != "big.zip" {
		t.Errorf("FileName = %q, want %q", marker.FileName, "big.zip")
	}
	if marker.Timestamp == "" {
		t.Error("Timestamp is empty")
	}
}

func TestMarkerReadNotExist(t *testing.T) {
	dir := t.TempDir()

	var sha256 [32]byte
	marker, err := ReadMarker(dir, sha256)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if marker != nil {
		t.Fatal("expected nil for nonexistent marker")
	}
}

func TestMarkerDelete(t *testing.T) {
	dir := t.TempDir()

	var sha256 [32]byte
	rand.Read(sha256[:])
	var partialHash [32]byte

	err := WriteMarker(dir, sha256, 100, 1, partialHash, DefaultChunkSize, "test.bin")
	if err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	// Verify it exists
	marker, _ := ReadMarker(dir, sha256)
	if marker == nil {
		t.Fatal("marker should exist before delete")
	}

	err = DeleteMarker(dir, sha256)
	if err != nil {
		t.Fatalf("DeleteMarker: %v", err)
	}

	// Verify it's gone
	marker, _ = ReadMarker(dir, sha256)
	if marker != nil {
		t.Fatal("marker should be nil after delete")
	}
}

func TestMarkerDeleteNonexistent(t *testing.T) {
	dir := t.TempDir()
	var sha256 [32]byte

	// Deleting a non-existent marker should not error
	err := DeleteMarker(dir, sha256)
	if err != nil {
		t.Fatalf("DeleteMarker of nonexistent file should not error: %v", err)
	}
}

func TestDetectResumable(t *testing.T) {
	dir := t.TempDir()

	// Create a "partial" file of 2 chunks
	partialData := make([]byte, 2*DefaultChunkSize)
	rand.Read(partialData)

	fileName := "testfile.bin"
	outPath := filepath.Join(dir, fileName)
	if err := os.WriteFile(outPath, partialData, 0644); err != nil {
		t.Fatalf("writing partial file: %v", err)
	}

	// Compute the partial hash
	hasher := hopcrypto.NewFileHasher()
	hasher.Write(partialData)
	partialHash := hasher.Sum()

	// The full file SHA-256 (just needs to be consistent)
	var fullSHA256 [32]byte
	rand.Read(fullSHA256[:])

	// Write a valid marker
	offset := int64(len(partialData))
	chunkIndex := uint64(2)
	err := WriteMarker(dir, fullSHA256, offset, chunkIndex, partialHash, DefaultChunkSize, fileName)
	if err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	// Offer that matches
	offer := &protocol.TransferOffer{
		FileName:  fileName,
		FileSize:  int64(5 * DefaultChunkSize), // total file is 5 chunks
		SHA256:    fullSHA256,
		ChunkSize: DefaultChunkSize,
	}

	state, err := DetectResumable(dir, offer)
	if err != nil {
		t.Fatalf("DetectResumable: %v", err)
	}
	if state == nil {
		t.Fatal("expected resume state, got nil")
	}

	if state.Offset != offset {
		t.Errorf("Offset = %d, want %d", state.Offset, offset)
	}
	if state.ChunkIndex != chunkIndex {
		t.Errorf("ChunkIndex = %d, want %d", state.ChunkIndex, chunkIndex)
	}
	if state.PartialHash != partialHash {
		t.Error("PartialHash mismatch")
	}
}

func TestDetectResumableNoMarker(t *testing.T) {
	dir := t.TempDir()

	// Create a partial file but NO marker
	outPath := filepath.Join(dir, "test.bin")
	os.WriteFile(outPath, make([]byte, DefaultChunkSize), 0644)

	offer := &protocol.TransferOffer{
		FileName:  "test.bin",
		FileSize:  int64(5 * DefaultChunkSize),
		ChunkSize: DefaultChunkSize,
	}

	state, err := DetectResumable(dir, offer)
	if err != nil {
		t.Fatalf("DetectResumable: %v", err)
	}
	if state != nil {
		t.Fatal("expected nil state when no marker exists")
	}
}

func TestDetectResumableMismatchFileName(t *testing.T) {
	dir := t.TempDir()

	var sha256 [32]byte
	rand.Read(sha256[:])
	var partialHash [32]byte

	// Marker for a different file name
	WriteMarker(dir, sha256, DefaultChunkSize, 1, partialHash, DefaultChunkSize, "other.bin")

	offer := &protocol.TransferOffer{
		FileName:  "test.bin",
		SHA256:    sha256,
		ChunkSize: DefaultChunkSize,
	}

	state, err := DetectResumable(dir, offer)
	if err != nil {
		t.Fatalf("DetectResumable: %v", err)
	}
	if state != nil {
		t.Fatal("expected nil state for mismatched file name")
	}
}

func TestDetectResumableMismatchChunkSize(t *testing.T) {
	dir := t.TempDir()

	var sha256 [32]byte
	rand.Read(sha256[:])
	var partialHash [32]byte

	// Marker with different chunk size
	WriteMarker(dir, sha256, DefaultChunkSize, 1, partialHash, 512*1024, "test.bin")

	offer := &protocol.TransferOffer{
		FileName:  "test.bin",
		SHA256:    sha256,
		ChunkSize: DefaultChunkSize,
	}

	state, err := DetectResumable(dir, offer)
	if err != nil {
		t.Fatalf("DetectResumable: %v", err)
	}
	if state != nil {
		t.Fatal("expected nil state for mismatched chunk size")
	}
}

func TestDetectResumableSizeMismatch(t *testing.T) {
	dir := t.TempDir()

	var sha256 [32]byte
	rand.Read(sha256[:])
	var partialHash [32]byte

	// Create a partial file
	outPath := filepath.Join(dir, "test.bin")
	os.WriteFile(outPath, make([]byte, DefaultChunkSize), 0644)

	// But marker says offset is 2 * DefaultChunkSize
	WriteMarker(dir, sha256, 2*int64(DefaultChunkSize), 2, partialHash, DefaultChunkSize, "test.bin")

	offer := &protocol.TransferOffer{
		FileName:  "test.bin",
		SHA256:    sha256,
		ChunkSize: DefaultChunkSize,
	}

	state, err := DetectResumable(dir, offer)
	if err != nil {
		t.Fatalf("DetectResumable: %v", err)
	}
	if state != nil {
		t.Fatal("expected nil state for size mismatch between file and marker")
	}
}

func TestDetectResumablePartialFileGone(t *testing.T) {
	dir := t.TempDir()

	var sha256 [32]byte
	rand.Read(sha256[:])
	var partialHash [32]byte

	// Write marker but no partial file
	WriteMarker(dir, sha256, DefaultChunkSize, 1, partialHash, DefaultChunkSize, "test.bin")

	offer := &protocol.TransferOffer{
		FileName:  "test.bin",
		SHA256:    sha256,
		ChunkSize: DefaultChunkSize,
	}

	state, err := DetectResumable(dir, offer)
	if err != nil {
		t.Fatalf("DetectResumable: %v", err)
	}
	if state != nil {
		t.Fatal("expected nil state when partial file is gone")
	}

	// Verify marker was cleaned up
	marker, _ := ReadMarker(dir, sha256)
	if marker != nil {
		t.Fatal("orphaned marker should have been cleaned up")
	}
}

func TestMarkerFileName(t *testing.T) {
	var sha256 [32]byte
	sha256[0] = 0xa3
	sha256[1] = 0xf2
	sha256[2] = 0xb8
	sha256[3] = 0x01
	sha256[4] = 0x02
	sha256[5] = 0x03

	name := MarkerFileName(sha256)
	expected := ".hop-resume-a3f2b8010203"
	if name != expected {
		t.Errorf("MarkerFileName = %q, want %q", name, expected)
	}
}
