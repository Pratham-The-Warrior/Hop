package transfer

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prathmeshsarda/hop/pkg/protocol"
	"github.com/prathmeshsarda/hop/pkg/tui"
)

// TestEngineWithSenderRateLimiter verifies that sender-side rate limiting
// slows down the transfer without corrupting data.
func TestEngineWithSenderRateLimiter(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file — 3 chunks worth of data
	testData := make([]byte, 3*DefaultChunkSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("generating test data: %v", err)
	}
	srcPath := filepath.Join(tmpDir, "limited.bin")
	if err := os.WriteFile(srcPath, testData, 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	outDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outDir, 0755)

	sToR := make(chan *protocol.Message, 100)
	rToS := make(chan *protocol.Message, 100)

	senderClient := &channelClient{sendCh: sToR, recvCh: rToS}
	receiverClient := &channelClient{sendCh: rToS, recvCh: sToR}

	ctx := context.Background()
	var wg sync.WaitGroup
	var sendErr, recvErr error

	// Use a generous rate limit so the test completes quickly but still
	// exercises the limiter's Wait() path.
	limiter := NewTokenBucketLimiter(50 * 1024 * 1024) // 50 MB/s

	var senderHashHex, receiverHashHex string

	wg.Add(1)
	go func() {
		defer wg.Done()
		sendErr = sendFileViaChannels(ctx, senderClient, srcPath, false, limiter, &EngineCallbacks{
			OnComplete: func(hashHex string) {
				senderHashHex = hashHex
			},
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		recvErr = receiveFileViaChannels(ctx, receiverClient, outDir, false, &EngineCallbacks{
			OnOfferReceived: func(offer *protocol.TransferOffer) bool {
				return true
			},
			OnComplete: func(hashHex string) {
				receiverHashHex = hashHex
			},
		})
	}()

	wg.Wait()

	if sendErr != nil {
		t.Fatalf("sender error: %v", sendErr)
	}
	if recvErr != nil {
		t.Fatalf("receiver error: %v", recvErr)
	}

	if senderHashHex == "" || receiverHashHex == "" {
		t.Fatal("hashes should not be empty")
	}
	if senderHashHex != receiverHashHex {
		t.Errorf("hash mismatch: sender=%s receiver=%s", senderHashHex, receiverHashHex)
	}

	// Verify the output file is byte-identical
	outData, err := os.ReadFile(filepath.Join(outDir, "limited.bin"))
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if len(outData) != len(testData) {
		t.Fatalf("output file size = %d, want %d", len(outData), len(testData))
	}
	for i := range testData {
		if testData[i] != outData[i] {
			t.Fatalf("byte mismatch at offset %d", i)
		}
	}
}

// TestEngineProgressCallbacks validates that progress callbacks fire with
// monotonically increasing byte counts and correct totals.
func TestEngineProgressCallbacks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file spanning exactly 2 chunks + remainder
	fileSize := 2*DefaultChunkSize + 42
	testData := make([]byte, fileSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("generating test data: %v", err)
	}
	srcPath := filepath.Join(tmpDir, "progress.bin")
	if err := os.WriteFile(srcPath, testData, 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	outDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outDir, 0755)

	sToR := make(chan *protocol.Message, 100)
	rToS := make(chan *protocol.Message, 100)

	senderClient := &channelClient{sendCh: sToR, recvCh: rToS}
	receiverClient := &channelClient{sendCh: rToS, recvCh: sToR}

	ctx := context.Background()
	var wg sync.WaitGroup
	var sendErr, recvErr error

	// Track sender progress
	var senderProgressCount int64
	var senderLastBytes int64

	// Track receiver progress
	var recvProgressCount int64
	var recvLastBytes int64
	var recvTotalBytes int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		sendErr = sendFileViaChannels(ctx, senderClient, srcPath, false, nil, &EngineCallbacks{
			OnProgress: func(bytesSoFar, totalBytes int64) {
				atomic.AddInt64(&senderProgressCount, 1)
				// Verify monotonically increasing
				prev := atomic.SwapInt64(&senderLastBytes, bytesSoFar)
				if bytesSoFar < prev {
					t.Errorf("sender progress went backwards: %d → %d", prev, bytesSoFar)
				}
			},
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		recvErr = receiveFileViaChannels(ctx, receiverClient, outDir, false, &EngineCallbacks{
			OnOfferReceived: func(offer *protocol.TransferOffer) bool {
				return true
			},
			OnProgress: func(bytesSoFar, totalBytes int64) {
				atomic.AddInt64(&recvProgressCount, 1)
				prev := atomic.SwapInt64(&recvLastBytes, bytesSoFar)
				if bytesSoFar < prev {
					t.Errorf("receiver progress went backwards: %d → %d", prev, bytesSoFar)
				}
				atomic.StoreInt64(&recvTotalBytes, totalBytes)
			},
		})
	}()

	wg.Wait()

	if sendErr != nil {
		t.Fatalf("sender error: %v", sendErr)
	}
	if recvErr != nil {
		t.Fatalf("receiver error: %v", recvErr)
	}

	// We should have received exactly 3 progress callbacks (3 chunks)
	sPC := atomic.LoadInt64(&senderProgressCount)
	if sPC != 3 {
		t.Errorf("sender progress callback count = %d, want 3", sPC)
	}

	rPC := atomic.LoadInt64(&recvProgressCount)
	if rPC != 3 {
		t.Errorf("receiver progress callback count = %d, want 3", rPC)
	}

	// Final bytes should equal total file size
	finalSender := atomic.LoadInt64(&senderLastBytes)
	if finalSender != int64(fileSize) {
		t.Errorf("sender final bytes = %d, want %d", finalSender, fileSize)
	}

	finalRecv := atomic.LoadInt64(&recvLastBytes)
	if finalRecv != int64(fileSize) {
		t.Errorf("receiver final bytes = %d, want %d", finalRecv, fileSize)
	}

	// Total bytes reported should match file size
	total := atomic.LoadInt64(&recvTotalBytes)
	if total != int64(fileSize) {
		t.Errorf("receiver total bytes = %d, want %d", total, fileSize)
	}
}

// TestEngineSmallFile validates a file smaller than one chunk.
func TestEngineSmallFile(t *testing.T) {
	tmpDir := t.TempDir()

	testData := []byte("hello from hop! this is a small test file.")
	srcPath := filepath.Join(tmpDir, "small.txt")
	if err := os.WriteFile(srcPath, testData, 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	outDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outDir, 0755)

	sToR := make(chan *protocol.Message, 100)
	rToS := make(chan *protocol.Message, 100)

	senderClient := &channelClient{sendCh: sToR, recvCh: rToS}
	receiverClient := &channelClient{sendCh: rToS, recvCh: sToR}

	ctx := context.Background()
	var wg sync.WaitGroup
	var sendErr, recvErr error

	var handshakeTier tui.ConnectionTier
	var senderHash, receiverHash string

	wg.Add(1)
	go func() {
		defer wg.Done()
		sendErr = sendFileViaChannels(ctx, senderClient, srcPath, false, nil, &EngineCallbacks{
			OnHandshakeComplete: func(tier tui.ConnectionTier) {
				handshakeTier = tier
			},
			OnComplete: func(hashHex string) {
				senderHash = hashHex
			},
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		recvErr = receiveFileViaChannels(ctx, receiverClient, outDir, false, &EngineCallbacks{
			OnOfferReceived: func(offer *protocol.TransferOffer) bool {
				if offer.FileName != "small.txt" {
					t.Errorf("offer filename = %q, want small.txt", offer.FileName)
				}
				if offer.FileSize != int64(len(testData)) {
					t.Errorf("offer size = %d, want %d", offer.FileSize, len(testData))
				}
				return true
			},
			OnComplete: func(hashHex string) {
				receiverHash = hashHex
			},
		})
	}()

	wg.Wait()

	if sendErr != nil {
		t.Fatalf("sender error: %v", sendErr)
	}
	if recvErr != nil {
		t.Fatalf("receiver error: %v", recvErr)
	}

	if handshakeTier != tui.TierRelayed {
		t.Errorf("handshake tier = %v, want TierRelayed", handshakeTier)
	}

	if senderHash != receiverHash {
		t.Errorf("hash mismatch: sender=%s receiver=%s", senderHash, receiverHash)
	}

	// Verify output
	outData, err := os.ReadFile(filepath.Join(outDir, "small.txt"))
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(outData) != string(testData) {
		t.Errorf("output mismatch: got %q, want %q", string(outData), string(testData))
	}
}

// TestEngineCancellation verifies that context cancellation during transfer
// produces clean error handling without deadlocks.
func TestEngineCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a large file to ensure the transfer takes time
	testData := make([]byte, 5*DefaultChunkSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("generating test data: %v", err)
	}
	srcPath := filepath.Join(tmpDir, "cancel.bin")
	if err := os.WriteFile(srcPath, testData, 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	outDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outDir, 0755)

	sToR := make(chan *protocol.Message, 100)
	rToS := make(chan *protocol.Message, 100)

	senderClient := &channelClient{sendCh: sToR, recvCh: rToS}
	receiverClient := &channelClient{sendCh: rToS, recvCh: sToR}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var sendErr, recvErr error

	chunkCount := int64(0)

	wg.Add(1)
	go func() {
		defer wg.Done()
		sendErr = sendFileViaChannels(ctx, senderClient, srcPath, false, nil, &EngineCallbacks{})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		recvErr = receiveFileViaChannels(ctx, receiverClient, outDir, false, &EngineCallbacks{
			OnOfferReceived: func(offer *protocol.TransferOffer) bool {
				return true
			},
			OnProgress: func(bytesSoFar, totalBytes int64) {
				count := atomic.AddInt64(&chunkCount, 1)
				if count == 2 {
					// Cancel after 2 chunks
					cancel()
				}
			},
		})
	}()

	wg.Wait()

	// At least one side should have seen an error
	if sendErr == nil && recvErr == nil {
		t.Error("expected at least one side to report an error after cancellation")
	}

	// Test should complete without deadlock (timeout would catch this)
	t.Logf("cancellation test completed: sendErr=%v recvErr=%v chunks=%d",
		sendErr, recvErr, atomic.LoadInt64(&chunkCount))
}

// TestTokenBucketLimiter_Wait validates that the rate limiter actually delays.
func TestTokenBucketLimiter_Wait(t *testing.T) {
	// 1 KB/s rate limiter
	limiter := NewTokenBucketLimiter(1024)

	// First call should succeed immediately (burst capacity available)
	start := time.Now()
	limiter.Wait(1024)
	firstDuration := time.Since(start)

	// Should be near-instant (under 100ms)
	if firstDuration > 100*time.Millisecond {
		t.Errorf("first Wait took too long: %v", firstDuration)
	}

	// Second call should be delayed — we consumed the burst, need to wait
	start = time.Now()
	limiter.Wait(512)
	secondDuration := time.Since(start)

	// Should take roughly 0.5s (512 bytes at 1024 bytes/s)
	if secondDuration < 200*time.Millisecond {
		t.Errorf("second Wait was too fast: %v (expected ~500ms)", secondDuration)
	}
	if secondDuration > 2*time.Second {
		t.Errorf("second Wait took too long: %v", secondDuration)
	}
}

// TestTokenBucketLimiter_TryConsume validates non-blocking consumption.
func TestTokenBucketLimiter_TryConsume(t *testing.T) {
	limiter := NewTokenBucketLimiter(1024)

	// Should succeed immediately
	if !limiter.TryConsume(512) {
		t.Error("TryConsume(512) should succeed with fresh limiter")
	}

	// Consume remaining tokens
	if !limiter.TryConsume(512) {
		t.Error("TryConsume(512) should succeed — still within burst")
	}

	// Now bucket should be near-empty — a large request should fail
	if limiter.TryConsume(2048) {
		t.Error("TryConsume(2048) should fail — bucket depleted")
	}
}
