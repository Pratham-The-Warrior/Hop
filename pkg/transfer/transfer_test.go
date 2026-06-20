package transfer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChunkerSmallFile(t *testing.T) {
	// Create a temp file with known content
	dir := t.TempDir()
	path := filepath.Join(dir, "test_small.bin")
	data := []byte("hello, hop! this is a small test file.")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	chunker, err := NewChunker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer chunker.Close()

	if chunker.FileSize() != int64(len(data)) {
		t.Fatalf("expected file size %d, got %d", len(data), chunker.FileSize())
	}

	// Should produce exactly 1 chunk
	if chunker.ChunkCount() != 1 {
		t.Fatalf("expected 1 chunk, got %d", chunker.ChunkCount())
	}

	chunk, err := chunker.NextChunk()
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Index != 0 {
		t.Fatalf("expected chunk index 0, got %d", chunk.Index)
	}
	if chunk.Size != len(data) {
		t.Fatalf("expected chunk size %d, got %d", len(data), chunk.Size)
	}
	if string(chunk.Data) != string(data) {
		t.Fatal("chunk data does not match file content")
	}

	// Next call should return EOF
	_, err = chunker.NextChunk()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestChunkerLargerFile(t *testing.T) {
	// Create a file that spans multiple chunks
	dir := t.TempDir()
	path := filepath.Join(dir, "test_multi.bin")

	// 2.5 MB file = 3 chunks (1MB + 1MB + 0.5MB)
	fileSize := int64(DefaultChunkSize*2 + DefaultChunkSize/2)
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	chunker, err := NewChunker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer chunker.Close()

	if chunker.ChunkCount() != 3 {
		t.Fatalf("expected 3 chunks, got %d", chunker.ChunkCount())
	}

	var totalRead int
	var chunkCount int
	for {
		chunk, err := chunker.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		totalRead += chunk.Size
		chunkCount++
	}

	if chunkCount != 3 {
		t.Fatalf("expected 3 chunks, got %d", chunkCount)
	}
	if int64(totalRead) != fileSize {
		t.Fatalf("expected %d total bytes, got %d", fileSize, totalRead)
	}
}

func TestChunkerSeekTo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_seek.bin")

	// 3 MB file
	fileSize := int64(DefaultChunkSize * 3)
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	os.WriteFile(path, data, 0644)

	chunker, err := NewChunker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer chunker.Close()

	// Seek to the second chunk
	err = chunker.SeekTo(int64(DefaultChunkSize))
	if err != nil {
		t.Fatal(err)
	}

	chunk, err := chunker.NextChunk()
	if err != nil {
		t.Fatal(err)
	}

	// Should be chunk index 1 (second chunk)
	if chunk.Index != 1 {
		t.Fatalf("expected chunk index 1 after seek, got %d", chunk.Index)
	}
}

func TestChunkerEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")
	os.WriteFile(path, []byte{}, 0644)

	chunker, err := NewChunker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer chunker.Close()

	if chunker.ChunkCount() != 0 {
		t.Fatalf("expected 0 chunks for empty file, got %d", chunker.ChunkCount())
	}

	_, err = chunker.NextChunk()
	if err != io.EOF {
		t.Fatalf("expected io.EOF for empty file, got %v", err)
	}
}

func TestCopyChunkData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_copy.bin")
	data := []byte("original chunk data")
	os.WriteFile(path, data, 0644)

	chunker, err := NewChunker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer chunker.Close()

	chunk, _ := chunker.NextChunk()
	copied := CopyChunkData(chunk)

	if string(copied) != string(data) {
		t.Fatal("copied data doesn't match")
	}
}

func TestTokenBucketLimiterBasic(t *testing.T) {
	// 1 MB/s rate
	limiter := NewTokenBucketLimiter(1024 * 1024)

	// Should be able to consume immediately (starts with 1 second of tokens)
	start := time.Now()
	limiter.Wait(1024) // 1 KB
	elapsed := time.Since(start)

	if elapsed > 10*time.Millisecond {
		t.Fatalf("initial Wait should be near-instant, took %v", elapsed)
	}
}

func TestTokenBucketLimiterThrottling(t *testing.T) {
	// Very low rate: 100 bytes/s
	limiter := NewTokenBucketLimiter(100)

	// Drain all tokens
	limiter.Wait(100)

	// This should block for approximately 1 second
	start := time.Now()
	limiter.Wait(100)
	elapsed := time.Since(start)

	// Allow some timing slack
	if elapsed < 500*time.Millisecond {
		t.Fatalf("expected throttling delay, only waited %v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("waited too long: %v", elapsed)
	}
}

func TestParseBandwidthLimit(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"5MB/s", 5 * 1024 * 1024},
		{"100KB/s", 100 * 1024},
		{"1GB/s", 1024 * 1024 * 1024},
		{"500B/s", 500},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseBandwidthLimit(tt.input)
			if err != nil {
				t.Fatalf("ParseBandwidthLimit(%q): %v", tt.input, err)
			}
			if result != tt.expected {
				t.Fatalf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func init() {
	// Suppress "imported and not used" for fmt
	_ = fmt.Sprint
}
