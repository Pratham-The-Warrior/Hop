package transfer

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	hopcrypto "github.com/prathmeshsarda/hop/pkg/crypto"
	"github.com/prathmeshsarda/hop/pkg/protocol"
	"github.com/prathmeshsarda/hop/pkg/tui"
)

// channelRelay comment — the tests use channelClient (defined below) to bridge
// sender and receiver through Go channels, avoiding real WebSocket connections.

// createTestFile creates a temporary file with random content of the given size.
func createTestFile(t *testing.T, dir string, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("generating random data: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
	return path
}

// TestEngineEndToEnd tests a full sender → receiver transfer using in-memory channels.
// Since engine.go uses *relay.Client (concrete type), we test the flow by running
// sender and receiver logic against each other through a real relay server.
// For a pure unit test, we test the individual steps.
func TestComputeFileHash(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file with known content
	content := []byte("hello, hop! this is a test file for hashing.")
	path := filepath.Join(tmpDir, "testfile.txt")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	hash, info, err := computeFileHash(path)
	if err != nil {
		t.Fatalf("computeFileHash: %v", err)
	}

	if info.Size() != int64(len(content)) {
		t.Errorf("file size = %d, want %d", info.Size(), len(content))
	}

	// Hash should be non-zero
	zeroHash := [32]byte{}
	if hash == zeroHash {
		t.Error("hash is all zeros")
	}

	// Hashing the same file again should produce the same result
	hash2, _, err := computeFileHash(path)
	if err != nil {
		t.Fatalf("computeFileHash (second): %v", err)
	}
	if hash != hash2 {
		t.Error("hashing same file twice produced different results")
	}
}

func TestComputeFileHashLargeFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file larger than the 1 MB chunk buffer to test multi-read hashing
	size := 2*1024*1024 + 42 // 2 MB + 42 bytes (not chunk-aligned)
	path := createTestFile(t, tmpDir, "large.bin", size)

	hash, info, err := computeFileHash(path)
	if err != nil {
		t.Fatalf("computeFileHash: %v", err)
	}

	if info.Size() != int64(size) {
		t.Errorf("file size = %d, want %d", info.Size(), size)
	}

	zeroHash := [32]byte{}
	if hash == zeroHash {
		t.Error("hash is all zeros for large file")
	}
}

func TestComputeFileHashNonexistent(t *testing.T) {
	_, _, err := computeFileHash("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// TestEngineIntegration runs a full sender↔receiver flow using goroutines
// and an in-memory message bridge to simulate the relay.
func TestEngineIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	testData := make([]byte, 3*DefaultChunkSize+500) // 3.something chunks
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("generating test data: %v", err)
	}
	srcPath := filepath.Join(tmpDir, "source.bin")
	if err := os.WriteFile(srcPath, testData, 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	outDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("creating output dir: %v", err)
	}

	// Create channel-based message bridge
	// sender sends on sToR, receiver reads from sToR
	// receiver sends on rToS, sender reads from rToS
	sToR := make(chan *protocol.Message, 100)
	rToS := make(chan *protocol.Message, 100)

	senderClient := &channelClient{sendCh: sToR, recvCh: rToS}
	receiverClient := &channelClient{sendCh: rToS, recvCh: sToR}

	ctx := context.Background()
	var wg sync.WaitGroup
	var sendErr, recvErr error

	var senderHandshakeDone, receiverHandshakeDone bool
	var senderHashHex, receiverHashHex string

	// Run sender
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendErr = sendFileViaChannels(ctx, senderClient, srcPath, false, nil, &EngineCallbacks{
			OnHandshakeComplete: func(tier tui.ConnectionTier) {
				senderHandshakeDone = true
			},
			OnComplete: func(hashHex string) {
				senderHashHex = hashHex
			},
		})
	}()

	// Run receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		recvErr = receiveFileViaChannels(ctx, receiverClient, outDir, false, &EngineCallbacks{
			OnHandshakeComplete: func(tier tui.ConnectionTier) {
				receiverHandshakeDone = true
			},
			OnOfferReceived: func(offer *protocol.TransferOffer) bool {
				return true // auto-accept
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

	if !senderHandshakeDone {
		t.Error("sender handshake callback not called")
	}
	if !receiverHandshakeDone {
		t.Error("receiver handshake callback not called")
	}

	if senderHashHex == "" {
		t.Error("sender hash is empty")
	}
	if receiverHashHex == "" {
		t.Error("receiver hash is empty")
	}
	if senderHashHex != receiverHashHex {
		t.Errorf("hash mismatch: sender=%s receiver=%s", senderHashHex, receiverHashHex)
	}

	// Verify the output file matches the source
	outPath := filepath.Join(outDir, "source.bin")
	outData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	if len(outData) != len(testData) {
		t.Fatalf("output file size = %d, want %d", len(outData), len(testData))
	}
	for i := range testData {
		if testData[i] != outData[i] {
			t.Fatalf("byte mismatch at offset %d: got 0x%02x, want 0x%02x", i, outData[i], testData[i])
		}
	}
}

// TestEngineReject tests that the receiver can reject a transfer offer.
func TestEngineReject(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "reject_test.bin")
	if err := os.WriteFile(srcPath, []byte("test data"), 0644); err != nil {
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
				return false // reject
			},
		})
	}()

	wg.Wait()

	if sendErr == nil {
		t.Fatal("expected sender to get rejection error")
	}
	if recvErr == nil {
		t.Fatal("expected receiver to get rejection error")
	}
}

// channelClient is a test adapter that sends/receives protocol messages over Go channels.
type channelClient struct {
	sendCh chan *protocol.Message
	recvCh chan *protocol.Message
}

func (c *channelClient) Send(ctx context.Context, msg *protocol.Message) error {
	select {
	case c.sendCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *channelClient) Receive(ctx context.Context) (*protocol.Message, error) {
	select {
	case msg := <-c.recvCh:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *channelClient) Close() error {
	return nil
}

// sendFileViaChannels is a copy of SendFile that uses channelClient instead of *relay.Client.
// This avoids needing a real WebSocket connection for tests.
func sendFileViaChannels(ctx context.Context, client *channelClient, filePath string, compress bool, limiter *TokenBucketLimiter, callbacks *EngineCallbacks) error {
	if callbacks == nil {
		callbacks = &EngineCallbacks{}
	}




	// We inline the flow to avoid importing relay.Client
	fileHash, fileInfo, err := computeFileHash(filePath)
	if err != nil {
		return fmt.Errorf("computing file hash: %w", err)
	}

	keyPair, err := hopcrypto.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generating key pair: %w", err)
	}

	hello := &protocol.HopHello{
		Version:  protocol.CurrentVersion,
		Features: protocol.AllFeatures(),
	}
	copy(hello.PublicKey[:], keyPair.PublicKeyBytes())

	helloMsg := &protocol.Message{
		Type:    protocol.MsgHopHello,
		Payload: protocol.EncodeHopHello(hello),
	}
	if err := client.Send(ctx, helloMsg); err != nil {
		return fmt.Errorf("sending HOP_HELLO: %w", err)
	}

	ackMsg, err := client.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receiving HOP_HELLO_ACK: %w", err)
	}
	if ackMsg.Type != protocol.MsgHopHelloAck {
		return fmt.Errorf("expected HOP_HELLO_ACK, got %s", ackMsg.Type)
	}

	peerHello, err := protocol.DecodeHopHello(ackMsg.Payload)
	if err != nil {
		return fmt.Errorf("decoding peer HOP_HELLO_ACK: %w", err)
	}

	if !protocol.CurrentVersion.Compatible(peerHello.Version) {
		return fmt.Errorf("incompatible protocol version")
	}

	sharedSecret, err := hopcrypto.DeriveSharedSecret(keyPair.Private, peerHello.PublicKey[:])
	if err != nil {
		return fmt.Errorf("deriving shared secret: %w", err)
	}
	encKey, err := hopcrypto.DeriveEncryptionKey(sharedSecret, "hop-transfer-key")
	if err != nil {
		return fmt.Errorf("deriving encryption key: %w", err)
	}

	encryptor, err := hopcrypto.NewEncryptor(encKey)
	if err != nil {
		return fmt.Errorf("creating encryptor: %w", err)
	}

	if callbacks.OnHandshakeComplete != nil {
		callbacks.OnHandshakeComplete(tui.TierRelayed)
	}

	offer := &protocol.TransferOffer{
		FileName:   fileInfo.Name(),
		FileSize:   fileInfo.Size(),
		SHA256:     fileHash,
		ChunkSize:  DefaultChunkSize,
		Compressed: compress,
	}

	offerMsg := &protocol.Message{
		Type:    protocol.MsgTransferOffer,
		Payload: protocol.EncodeTransferOffer(offer),
	}
	if err := client.Send(ctx, offerMsg); err != nil {
		return fmt.Errorf("sending TRANSFER_OFFER: %w", err)
	}

	respMsg, err := client.Receive(ctx)
	if err != nil {
		return fmt.Errorf("waiting for transfer response: %w", err)
	}

	switch respMsg.Type {
	case protocol.MsgTransferReject:
		return fmt.Errorf("transfer rejected by receiver")
	case protocol.MsgTransferAccept:
		// continue
	default:
		return fmt.Errorf("unexpected message: %s", respMsg.Type)
	}

	// Parse resume offset from TRANSFER_ACCEPT
	var resumeOffset int64
	if len(respMsg.Payload) >= 8 {
		acceptPayload, err := protocol.DecodeTransferAccept(respMsg.Payload)
		if err == nil && acceptPayload.ResumeOffset > 0 {
			resumeOffset = acceptPayload.ResumeOffset
		}
	}

	chunker, err := NewChunker(filePath)
	if err != nil {
		return fmt.Errorf("creating chunker: %w", err)
	}
	defer chunker.Close()

	var totalBytesSent int64
	var totalChunks uint64

	// Handle resume: seek past already-sent data
	if resumeOffset > 0 {
		if err := chunker.SeekTo(resumeOffset); err != nil {
			return fmt.Errorf("seeking to resume offset: %w", err)
		}
		noncesToSkip := uint64(resumeOffset / DefaultChunkSize)
		encryptor.SkipNonces(noncesToSkip)
		totalBytesSent = resumeOffset
		totalChunks = noncesToSkip
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		chunk, err := chunker.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading chunk: %w", err)
		}

		chunkData := CopyChunkData(chunk)

		if limiter != nil {
			limiter.Wait(chunk.Size)
		}

		crc := hopcrypto.ChunkCRC32(chunkData)

		encrypted, err := encryptor.Encrypt(chunkData)
		if err != nil {
			return fmt.Errorf("encrypting chunk %d: %w", chunk.Index, err)
		}

		hdr := &protocol.ChunkHeader{
			Index: chunk.Index,
			Size:  uint32(chunk.Size),
			CRC32: crc,
		}
		hdrBytes := protocol.EncodeChunkHeader(hdr)
		payload := make([]byte, len(hdrBytes)+len(encrypted))
		copy(payload, hdrBytes)
		copy(payload[len(hdrBytes):], encrypted)

		chunkMsg := &protocol.Message{
			Type:    protocol.MsgChunkData,
			Payload: payload,
		}
		if err := client.Send(ctx, chunkMsg); err != nil {
			return fmt.Errorf("sending chunk %d: %w", chunk.Index, err)
		}

		ackResp, err := client.Receive(ctx)
		if err != nil {
			return fmt.Errorf("waiting for chunk ACK: %w", err)
		}
		if ackResp.Type == protocol.MsgTransferCancel {
			return fmt.Errorf("transfer cancelled by receiver")
		}
		if ackResp.Type != protocol.MsgChunkAck {
			return fmt.Errorf("expected CHUNK_ACK, got %s", ackResp.Type)
		}

		totalBytesSent += int64(chunk.Size)
		totalChunks++

		if callbacks.OnProgress != nil {
			callbacks.OnProgress(totalBytesSent, fileInfo.Size())
		}
	}

	complete := &protocol.TransferCompletePayload{
		SHA256:      fileHash,
		TotalChunks: totalChunks,
		TotalBytes:  uint64(totalBytesSent),
	}
	completeMsg := &protocol.Message{
		Type:    protocol.MsgTransferComplete,
		Payload: protocol.EncodeTransferComplete(complete),
	}
	if err := client.Send(ctx, completeMsg); err != nil {
		return fmt.Errorf("sending TRANSFER_COMPLETE: %w", err)
	}

	if callbacks.OnComplete != nil {
		callbacks.OnComplete(fmt.Sprintf("%x", fileHash))
	}

	return nil
}

// receiveFileViaChannels is a copy of ReceiveFile that uses channelClient.
func receiveFileViaChannels(ctx context.Context, client *channelClient, outputDir string, enableResume bool, callbacks *EngineCallbacks) error {
	if callbacks == nil {
		callbacks = &EngineCallbacks{}
	}

	keyPair, err := hopcrypto.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generating key pair: %w", err)
	}

	helloMsg, err := client.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receiving HOP_HELLO: %w", err)
	}
	if helloMsg.Type != protocol.MsgHopHello {
		return fmt.Errorf("expected HOP_HELLO, got %s", helloMsg.Type)
	}

	peerHello, err := protocol.DecodeHopHello(helloMsg.Payload)
	if err != nil {
		return fmt.Errorf("decoding sender HOP_HELLO: %w", err)
	}

	if !protocol.CurrentVersion.Compatible(peerHello.Version) {
		return fmt.Errorf("incompatible protocol version")
	}

	ack := &protocol.HopHello{
		Version:  protocol.CurrentVersion,
		Features: protocol.AllFeatures(),
	}
	copy(ack.PublicKey[:], keyPair.PublicKeyBytes())

	ackMsg := &protocol.Message{
		Type:    protocol.MsgHopHelloAck,
		Payload: protocol.EncodeHopHello(ack),
	}
	if err := client.Send(ctx, ackMsg); err != nil {
		return fmt.Errorf("sending HOP_HELLO_ACK: %w", err)
	}

	sharedSecret, err := hopcrypto.DeriveSharedSecret(keyPair.Private, peerHello.PublicKey[:])
	if err != nil {
		return fmt.Errorf("deriving shared secret: %w", err)
	}
	decKey, err := hopcrypto.DeriveEncryptionKey(sharedSecret, "hop-transfer-key")
	if err != nil {
		return fmt.Errorf("deriving decryption key: %w", err)
	}

	decryptor, err := hopcrypto.NewDecryptor(decKey)
	if err != nil {
		return fmt.Errorf("creating decryptor: %w", err)
	}

	if callbacks.OnHandshakeComplete != nil {
		callbacks.OnHandshakeComplete(tui.TierRelayed)
	}

	offerMsg, err := client.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receiving TRANSFER_OFFER: %w", err)
	}
	if offerMsg.Type != protocol.MsgTransferOffer {
		return fmt.Errorf("expected TRANSFER_OFFER, got %s", offerMsg.Type)
	}

	offer, err := protocol.DecodeTransferOffer(offerMsg.Payload)
	if err != nil {
		return fmt.Errorf("decoding TRANSFER_OFFER: %w", err)
	}

	accepted := true
	if callbacks.OnOfferReceived != nil {
		accepted = callbacks.OnOfferReceived(offer)
	}

	if !accepted {
		rejectMsg := &protocol.Message{Type: protocol.MsgTransferReject}
		_ = client.Send(ctx, rejectMsg)
		return fmt.Errorf("transfer rejected by user")
	}

	acceptPayload := &protocol.TransferAcceptPayload{ResumeOffset: 0}

	// Check for resume if enabled
	var resumeState *ResumeState
	if enableResume {
		state, err := DetectResumable(outputDir, offer)
		if err == nil {
			resumeState = state
		}
	}

	var resumeOffset int64
	if resumeState != nil {
		resumeOffset = resumeState.Offset
		acceptPayload.ResumeOffset = resumeOffset
		if callbacks.OnResumeDetected != nil {
			callbacks.OnResumeDetected(resumeOffset, offer.FileSize)
		}
	}

	acceptMsg := &protocol.Message{
		Type:    protocol.MsgTransferAccept,
		Payload: protocol.EncodeTransferAccept(acceptPayload),
	}
	if err := client.Send(ctx, acceptMsg); err != nil {
		return fmt.Errorf("sending TRANSFER_ACCEPT: %w", err)
	}

	outPath := outputDir + string(os.PathSeparator) + offer.FileName
	var outFile *os.File
	fileHasher := hopcrypto.NewFileHasher()
	var totalBytesReceived int64

	if resumeState != nil {
		outFile, err = os.OpenFile(outPath, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("opening partial file for resume: %w", err)
		}
		rehashErr := feedHasherFromFile(fileHasher, outPath, resumeState.Offset)
		if rehashErr != nil {
			outFile.Close()
			return fmt.Errorf("feeding hasher: %w", rehashErr)
		}
		totalBytesReceived = resumeState.Offset
	} else {
		outFile, err = os.Create(outPath)
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}
	}
	defer outFile.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := client.Receive(ctx)
		if err != nil {
			return fmt.Errorf("receiving data: %w", err)
		}

		if msg.Type == protocol.MsgTransferComplete {
			completePayload, err := protocol.DecodeTransferComplete(msg.Payload)
			if err != nil {
				return fmt.Errorf("decoding TRANSFER_COMPLETE: %w", err)
			}

			if !fileHasher.Verify(completePayload.SHA256) {
				return fmt.Errorf("SHA-256 verification failed")
			}

			// Clean up resume marker on successful completion
			if enableResume {
				_ = DeleteMarker(outputDir, offer.SHA256)
			}

			if callbacks.OnComplete != nil {
				callbacks.OnComplete(fileHasher.SumHex())
			}
			return nil
		}

		if msg.Type == protocol.MsgTransferCancel {
			return fmt.Errorf("transfer cancelled by sender")
		}

		if msg.Type != protocol.MsgChunkData {
			return fmt.Errorf("expected CHUNK_DATA, got %s", msg.Type)
		}

		if len(msg.Payload) < 16 {
			return fmt.Errorf("chunk message too short")
		}

		chunkHdr, err := protocol.DecodeChunkHeader(msg.Payload[:16])
		if err != nil {
			return fmt.Errorf("decoding chunk header: %w", err)
		}

		encryptedData := msg.Payload[16:]

		plaintext, err := decryptor.Decrypt(encryptedData)
		if err != nil {
			return fmt.Errorf("decrypting chunk %d: %w", chunkHdr.Index, err)
		}

		if !hopcrypto.VerifyChunkCRC32(plaintext, chunkHdr.CRC32) {
			return fmt.Errorf("CRC-32 mismatch on chunk %d", chunkHdr.Index)
		}

		n, err := outFile.Write(plaintext)
		if err != nil {
			return fmt.Errorf("writing chunk %d: %w", chunkHdr.Index, err)
		}

		fileHasher.Write(plaintext)
		totalBytesReceived += int64(n)

		chunkAck := &protocol.Message{
			Type:    protocol.MsgChunkAck,
			Payload: protocol.EncodeChunkHeader(chunkHdr),
		}
		if err := client.Send(ctx, chunkAck); err != nil {
			return fmt.Errorf("sending CHUNK_ACK: %w", err)
		}

		if callbacks.OnProgress != nil {
			callbacks.OnProgress(totalBytesReceived, offer.FileSize)
		}
	}
}

// TestEngineResumeIntegration simulates an interrupted transfer followed by a
// resumed one, verifying the final file is correct.
func TestEngineResumeIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file — 5 full chunks
	testData := make([]byte, 5*DefaultChunkSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("generating test data: %v", err)
	}
	srcPath := filepath.Join(tmpDir, "source.bin")
	if err := os.WriteFile(srcPath, testData, 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	outDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("creating output dir: %v", err)
	}

	// --- Phase 1: Partial transfer (sender sends 2 of 5 chunks, then stops) ---
	sToR := make(chan *protocol.Message, 100)
	rToS := make(chan *protocol.Message, 100)

	senderClient := &channelClient{sendCh: sToR, recvCh: rToS}
	receiverClient := &channelClient{sendCh: rToS, recvCh: sToR}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var sendErr error
	chunksToSendBeforeCancel := 2
	chunksSent := 0

	// Run sender — cancel after 2 chunks
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendErr = sendFileViaChannels(ctx, senderClient, srcPath, false, nil, &EngineCallbacks{
			OnProgress: func(bytesSoFar, totalBytes int64) {
				chunksSent++
				if chunksSent >= chunksToSendBeforeCancel {
					cancel() // Interrupt the transfer
				}
			},
		})
	}()

	// Run receiver — enable resume so it writes a marker on cancel
	var recvErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		recvErr = receiveFileViaChannels(ctx, receiverClient, outDir, true, &EngineCallbacks{
			OnOfferReceived: func(offer *protocol.TransferOffer) bool {
				return true
			},
		})
	}()

	wg.Wait()

	// Both should have errors (context cancelled)
	if sendErr == nil {
		t.Log("Note: sender may not have errored if cancel raced")
	}
	// Receiver should have errored due to cancellation or connection loss
	if recvErr == nil {
		t.Log("Note: receiver completed unexpectedly (possible race)")
	}

	// Verify partial file exists
	outPath := filepath.Join(outDir, "source.bin")
	partialInfo, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("partial file should exist: %v", err)
	}
	if partialInfo.Size() == 0 {
		t.Fatal("partial file should have some data")
	}
	if partialInfo.Size() >= int64(len(testData)) {
		t.Skip("full file was transferred in phase 1 — cancel arrived too late")
	}

	t.Logf("Phase 1: partial file is %d bytes (%.0f%% of %d)",
		partialInfo.Size(), float64(partialInfo.Size())/float64(len(testData))*100, len(testData))

	// Manually write a resume marker for the partial data (since the test
	// receiver is a copy, we ensure the marker is correct)
	fileHash, _, err := computeFileHash(srcPath)
	if err != nil {
		t.Fatalf("computing file hash: %v", err)
	}

	partialHash, err := hashPartialFile(outPath, partialInfo.Size())
	if err != nil {
		t.Fatalf("hashing partial file: %v", err)
	}

	chunkIndex := uint64(partialInfo.Size() / DefaultChunkSize)
	err = WriteMarker(outDir, fileHash, partialInfo.Size(), chunkIndex, partialHash, DefaultChunkSize, "source.bin")
	if err != nil {
		t.Fatalf("writing resume marker: %v", err)
	}

	// Verify marker exists
	marker, err := ReadMarker(outDir, fileHash)
	if err != nil {
		t.Fatalf("reading marker: %v", err)
	}
	if marker == nil {
		t.Fatal("marker should exist")
	}
	t.Logf("Phase 1: marker written — offset=%d chunkIndex=%d", marker.Offset, marker.ChunkIndex)

	// --- Phase 2: Resumed transfer ---
	sToR2 := make(chan *protocol.Message, 100)
	rToS2 := make(chan *protocol.Message, 100)

	senderClient2 := &channelClient{sendCh: sToR2, recvCh: rToS2}
	receiverClient2 := &channelClient{sendCh: rToS2, recvCh: sToR2}

	ctx2 := context.Background()
	var wg2 sync.WaitGroup
	var sendErr2, recvErr2 error
	var senderHashHex2, receiverHashHex2 string
	var resumeDetected bool

	// Run sender
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		sendErr2 = sendFileViaChannels(ctx2, senderClient2, srcPath, false, nil, &EngineCallbacks{
			OnComplete: func(hashHex string) {
				senderHashHex2 = hashHex
			},
		})
	}()

	// Run receiver with resume enabled
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		recvErr2 = receiveFileViaChannels(ctx2, receiverClient2, outDir, true, &EngineCallbacks{
			OnOfferReceived: func(offer *protocol.TransferOffer) bool {
				return true
			},
			OnComplete: func(hashHex string) {
				receiverHashHex2 = hashHex
			},
			OnResumeDetected: func(offset int64, total int64) {
				resumeDetected = true
				t.Logf("Phase 2: resume detected at offset %d / %d", offset, total)
			},
		})
	}()

	wg2.Wait()

	if sendErr2 != nil {
		t.Fatalf("sender error in phase 2: %v", sendErr2)
	}
	if recvErr2 != nil {
		t.Fatalf("receiver error in phase 2: %v", recvErr2)
	}

	if !resumeDetected {
		t.Error("resume should have been detected in phase 2")
	}

	if senderHashHex2 == "" || receiverHashHex2 == "" {
		t.Fatal("hashes should not be empty")
	}
	if senderHashHex2 != receiverHashHex2 {
		t.Errorf("hash mismatch: sender=%s receiver=%s", senderHashHex2, receiverHashHex2)
	}

	// Verify the final file matches the original
	finalData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading final file: %v", err)
	}

	if len(finalData) != len(testData) {
		t.Fatalf("final file size = %d, want %d", len(finalData), len(testData))
	}
	for i := range testData {
		if testData[i] != finalData[i] {
			t.Fatalf("byte mismatch at offset %d: got 0x%02x, want 0x%02x", i, finalData[i], testData[i])
		}
	}

	// Verify marker was cleaned up
	markerAfter, _ := ReadMarker(outDir, fileHash)
	if markerAfter != nil {
		t.Error("resume marker should have been deleted after successful transfer")
	}

	t.Logf("Phase 2: resume transfer complete — file verified (%d bytes)", len(finalData))
}
