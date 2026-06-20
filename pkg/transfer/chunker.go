package transfer

import (
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	// DefaultChunkSize is 1 MB — the optimal chunk size per the spec.
	// A 50 GB file produces ~50,000 chunks at this size, keeping per-chunk
	// overhead low while maintaining a flat memory footprint under 50 MB.
	DefaultChunkSize = 1 << 20 // 1 MB
)

// Chunker reads a file in fixed-size chunks with buffer reuse.
// It maintains a single pre-allocated buffer to keep memory consumption flat
// regardless of file size.
type Chunker struct {
	mu     sync.Mutex
	file   *os.File
	buf    []byte
	size   int64
	offset int64
	index  uint64
}

// NewChunker creates a new Chunker for the given file path.
func NewChunker(path string) (*Chunker, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat file: %w", err)
	}

	return &Chunker{
		file: f,
		buf:  make([]byte, DefaultChunkSize),
		size: info.Size(),
	}, nil
}

// Chunk represents a single file chunk with its data and metadata.
type Chunk struct {
	Data  []byte // The chunk data (slice of the shared buffer — copy if needed)
	Index uint64 // Zero-based chunk index
	Size  int    // Actual bytes in this chunk (may be < DefaultChunkSize for last chunk)
}

// NextChunk reads the next chunk from the file.
// Returns io.EOF when all data has been read.
// IMPORTANT: The returned Chunk.Data is a slice of the internal buffer.
// It will be overwritten on the next call to NextChunk. Callers that need
// to retain the data must copy it before calling NextChunk again.
func (c *Chunker) NextChunk() (*Chunk, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	n, err := c.file.Read(c.buf)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("reading chunk %d: %w", c.index, err)
	}
	if n == 0 {
		return nil, io.EOF
	}

	chunk := &Chunk{
		Data:  c.buf[:n],
		Index: c.index,
		Size:  n,
	}

	c.index++
	c.offset += int64(n)

	return chunk, nil
}

// CopyChunkData returns a copy of the chunk data that is safe to retain
// after subsequent NextChunk calls.
func CopyChunkData(chunk *Chunk) []byte {
	data := make([]byte, chunk.Size)
	copy(data, chunk.Data)
	return data
}

// SeekTo sets the read offset for resumable transfers. The index is
// recalculated based on the offset and chunk size.
func (c *Chunker) SeekTo(offset int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if offset < 0 || offset > c.size {
		return fmt.Errorf("seek offset %d out of range [0, %d]", offset, c.size)
	}

	_, err := c.file.Seek(offset, io.SeekStart)
	if err != nil {
		return fmt.Errorf("seeking to offset %d: %w", offset, err)
	}

	c.offset = offset
	c.index = uint64(offset / DefaultChunkSize)
	return nil
}

// FileSize returns the total file size in bytes.
func (c *Chunker) FileSize() int64 {
	return c.size
}

// Offset returns the current read offset.
func (c *Chunker) Offset() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset
}

// ChunkCount returns the total number of chunks for the file.
func (c *Chunker) ChunkCount() uint64 {
	if c.size == 0 {
		return 0
	}
	return uint64((c.size-1)/DefaultChunkSize) + 1
}

// Close releases the file handle.
func (c *Chunker) Close() error {
	return c.file.Close()
}
