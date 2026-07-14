package compress

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestCompressDecompressRoundTrip(t *testing.T) {
	comp, err := NewCompressor()
	if err != nil {
		t.Fatalf("NewCompressor: %v", err)
	}
	defer comp.Close()

	decomp, err := NewDecompressor()
	if err != nil {
		t.Fatalf("NewDecompressor: %v", err)
	}
	defer decomp.Close()

	// Test with compressible data (repeated patterns)
	original := bytes.Repeat([]byte("hello hop compression test! "), 10000)

	compressed := comp.CompressChunk(original)

	// Compressed should be smaller for compressible data
	if len(compressed) >= len(original) {
		t.Errorf("compressed size (%d) should be smaller than original (%d) for compressible data",
			len(compressed), len(original))
	}

	decompressed, err := decomp.DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(decompressed), len(original))
	}
}

func TestCompressIncompressibleData(t *testing.T) {
	comp, err := NewCompressor()
	if err != nil {
		t.Fatalf("NewCompressor: %v", err)
	}
	defer comp.Close()

	decomp, err := NewDecompressor()
	if err != nil {
		t.Fatalf("NewDecompressor: %v", err)
	}
	defer decomp.Close()

	// Random data is incompressible
	original := make([]byte, 1<<20) // 1 MB
	if _, err := rand.Read(original); err != nil {
		t.Fatalf("generating random data: %v", err)
	}

	compressed := comp.CompressChunk(original)

	// For random data, compressed may be slightly larger — that's fine
	t.Logf("incompressible: %d → %d bytes (%.1f%%)",
		len(original), len(compressed), float64(len(compressed))/float64(len(original))*100)

	decompressed, err := decomp.DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Fatal("round-trip mismatch for incompressible data")
	}
}

func TestCompressEmptyData(t *testing.T) {
	comp, err := NewCompressor()
	if err != nil {
		t.Fatalf("NewCompressor: %v", err)
	}
	defer comp.Close()

	decomp, err := NewDecompressor()
	if err != nil {
		t.Fatalf("NewDecompressor: %v", err)
	}
	defer decomp.Close()

	compressed := comp.CompressChunk([]byte{})

	decompressed, err := decomp.DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk empty: %v", err)
	}

	if len(decompressed) != 0 {
		t.Errorf("expected empty decompressed data, got %d bytes", len(decompressed))
	}
}

func TestMultipleSequentialOperations(t *testing.T) {
	comp, err := NewCompressor()
	if err != nil {
		t.Fatalf("NewCompressor: %v", err)
	}
	defer comp.Close()

	decomp, err := NewDecompressor()
	if err != nil {
		t.Fatalf("NewDecompressor: %v", err)
	}
	defer decomp.Close()

	// Compress and decompress multiple different chunks in sequence
	// to verify encoder/decoder reuse works correctly
	for i := 0; i < 10; i++ {
		original := make([]byte, 4096+i*100)
		for j := range original {
			original[j] = byte(i*17 + j%256)
		}

		compressed := comp.CompressChunk(original)
		decompressed, err := decomp.DecompressChunk(compressed)
		if err != nil {
			t.Fatalf("iteration %d: DecompressChunk: %v", i, err)
		}

		if !bytes.Equal(original, decompressed) {
			t.Fatalf("iteration %d: round-trip mismatch", i)
		}
	}
}

func TestDecompressInvalidData(t *testing.T) {
	decomp, err := NewDecompressor()
	if err != nil {
		t.Fatalf("NewDecompressor: %v", err)
	}
	defer decomp.Close()

	_, err = decomp.DecompressChunk([]byte("this is not zstd data"))
	if err == nil {
		t.Fatal("expected error for invalid zstd data")
	}
}

func BenchmarkCompressChunk(b *testing.B) {
	comp, err := NewCompressor()
	if err != nil {
		b.Fatalf("NewCompressor: %v", err)
	}
	defer comp.Close()

	// 1 MB compressible chunk (typical source code)
	data := bytes.Repeat([]byte("func main() { fmt.Println(\"hello\") }\n"), 1<<20/38)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		comp.CompressChunk(data)
	}
}

func BenchmarkDecompressChunk(b *testing.B) {
	comp, err := NewCompressor()
	if err != nil {
		b.Fatalf("NewCompressor: %v", err)
	}
	defer comp.Close()

	decomp, err := NewDecompressor()
	if err != nil {
		b.Fatalf("NewDecompressor: %v", err)
	}
	defer decomp.Close()

	data := bytes.Repeat([]byte("func main() { fmt.Println(\"hello\") }\n"), 1<<20/38)
	compressed := comp.CompressChunk(data)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		decomp.DecompressChunk(compressed)
	}
}
