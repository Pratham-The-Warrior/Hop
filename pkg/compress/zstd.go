// Package compress provides zstd streaming compression and decompression
// for hop's chunk-level transfer pipeline. When the --compress flag is used,
// each 1 MB chunk is compressed before encryption and decompressed after
// decryption on the receiver side.
//
// Uses the klauspost/compress pure-Go zstd implementation (no CGO required).
// Compression level is set to SpeedDefault (level 3) for a good balance
// between compression ratio and speed on real-time streaming workloads.
package compress

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Compressor wraps a zstd encoder for chunk-level compression.
// It is safe for sequential use (not concurrent — use one per goroutine
// or protect with external synchronization).
type Compressor struct {
	mu      sync.Mutex
	encoder *zstd.Encoder
}

// NewCompressor creates a new zstd compressor with speed-optimized settings.
// The encoder is reused across calls to avoid repeated allocations.
func NewCompressor() (*Compressor, error) {
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1), // Single-threaded per chunk
	)
	if err != nil {
		return nil, fmt.Errorf("creating zstd encoder: %w", err)
	}
	return &Compressor{encoder: enc}, nil
}

// CompressChunk compresses a single data chunk using zstd.
// Returns the compressed data. For already-compressed or incompressible data,
// the output may be slightly larger than the input.
func (c *Compressor) CompressChunk(data []byte) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoder.EncodeAll(data, make([]byte, 0, len(data)))
}

// Close releases the compressor's resources.
func (c *Compressor) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.encoder.Close()
}

// Decompressor wraps a zstd decoder for chunk-level decompression.
type Decompressor struct {
	mu      sync.Mutex
	decoder *zstd.Decoder
}

// NewDecompressor creates a new zstd decompressor.
// The decoder is reused across calls.
func NewDecompressor() (*Decompressor, error) {
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(16<<20), // 16 MB max decompressed size per chunk
	)
	if err != nil {
		return nil, fmt.Errorf("creating zstd decoder: %w", err)
	}
	return &Decompressor{decoder: dec}, nil
}

// DecompressChunk decompresses a single zstd-compressed chunk.
// Returns the decompressed data.
func (d *Decompressor) DecompressChunk(data []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.decoder.DecodeAll(data, make([]byte, 0, len(data)*2))
	if err != nil {
		return nil, fmt.Errorf("zstd decompress: %w", err)
	}
	return result, nil
}

// Close releases the decompressor's resources.
func (d *Decompressor) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.decoder.Close()
}
