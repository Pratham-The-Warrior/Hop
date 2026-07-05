package transfer

import (
	"context"
	"fmt"
	"io"
	"os"

	hopcrypto "github.com/prathmeshsarda/hop/pkg/crypto"
	"github.com/prathmeshsarda/hop/pkg/protocol"
	"github.com/prathmeshsarda/hop/pkg/tui"
)

// EngineCallbacks provides hooks for the transfer engine to report progress
// back to the calling command. All methods are optional (nil-safe).
type EngineCallbacks struct {
	// OnHandshakeComplete is called after the key exchange succeeds.
	OnHandshakeComplete func(tier tui.ConnectionTier)

	// OnOfferReceived is called on the receiver side when a transfer offer arrives.
	// Return true to accept, false to reject.
	OnOfferReceived func(offer *protocol.TransferOffer) bool

	// OnProgress is called after each chunk is sent/received.
	OnProgress func(bytesSoFar int64, totalBytes int64)

	// OnComplete is called when the transfer finishes successfully.
	OnComplete func(hashHex string)

	// OnError is called when the transfer fails.
	OnError func(err error)

	// OnResumeDetected is called when a resumable partial transfer is found.
	// offset is the byte position to resume from, total is the full file size.
	OnResumeDetected func(offset int64, total int64)

	// OnBrowserDownload is called when a browser download begins.
	OnBrowserDownload func()
}

const (
	// markerUpdateInterval controls how often the resume marker is updated
	// during a transfer (in chunks). Lower values increase crash-recovery
	// granularity but add more disk I/O.
	markerUpdateInterval = 50
)

// SendFile orchestrates the full sender-side transfer:
//
//  1. Pre-compute file SHA-256
//  2. Perform HOP_HELLO key exchange → derive shared encryption key
//  3. Send TRANSFER_OFFER and wait for ACCEPT/REJECT
//  4. Stream encrypted chunks with CRC-32 integrity
//  5. Send TRANSFER_COMPLETE with final hash
func SendFile(ctx context.Context, transport Transport, filePath string, compress bool, limiter *TokenBucketLimiter, callbacks *EngineCallbacks) error {
	if callbacks == nil {
		callbacks = &EngineCallbacks{}
	}

	// --- Step 1: Pre-compute SHA-256 ---
	fileHash, fileInfo, err := computeFileHash(filePath)
	if err != nil {
		return fmt.Errorf("computing file hash: %w", err)
	}

	// --- Step 2: Key exchange (HOP_HELLO) ---
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
	if err := transport.Send(ctx, helloMsg); err != nil {
		return fmt.Errorf("sending HOP_HELLO: %w", err)
	}

	// Wait for HOP_HELLO_ACK from receiver
	ackMsg, err := transport.Receive(ctx)
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

	// Version compatibility check
	if !protocol.CurrentVersion.Compatible(peerHello.Version) {
		return fmt.Errorf("incompatible protocol version: peer is %s, we are %s",
			peerHello.Version.String(), protocol.CurrentVersion.String())
	}

	// Derive shared encryption key
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

	// --- Step 3: Send TRANSFER_OFFER ---
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
	if err := transport.Send(ctx, offerMsg); err != nil {
		return fmt.Errorf("sending TRANSFER_OFFER: %w", err)
	}

	// Wait for TRANSFER_ACCEPT or TRANSFER_REJECT
	respMsg, err := transport.Receive(ctx)
	if err != nil {
		return fmt.Errorf("waiting for transfer response: %w", err)
	}

	switch respMsg.Type {
	case protocol.MsgTransferReject:
		return fmt.Errorf("transfer rejected by receiver")
	case protocol.MsgTransferAccept:
		// Continue with transfer
	default:
		return fmt.Errorf("unexpected message type: %s (expected ACCEPT or REJECT)", respMsg.Type)
	}

	// Parse resume offset from TRANSFER_ACCEPT
	var resumeOffset int64
	if len(respMsg.Payload) >= 8 {
		acceptPayload, err := protocol.DecodeTransferAccept(respMsg.Payload)
		if err == nil && acceptPayload.ResumeOffset > 0 {
			resumeOffset = acceptPayload.ResumeOffset
		}
	}

	// --- Step 4: Stream encrypted chunks ---
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
			return fmt.Errorf("seeking to resume offset %d: %w", resumeOffset, err)
		}
		// Skip encryptor nonces for chunks already sent
		noncesToSkip := uint64(resumeOffset / DefaultChunkSize)
		encryptor.SkipNonces(noncesToSkip)
		totalBytesSent = resumeOffset
		totalChunks = noncesToSkip

		if callbacks.OnResumeDetected != nil {
			callbacks.OnResumeDetected(resumeOffset, fileInfo.Size())
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Send cancellation
			cancelMsg := &protocol.Message{Type: protocol.MsgTransferCancel}
			_ = transport.Send(context.Background(), cancelMsg)
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

		// Copy chunk data since the buffer is reused
		chunkData := CopyChunkData(chunk)

		// Apply rate limiting if configured
		if limiter != nil {
			limiter.Wait(chunk.Size)
		}

		// Compute CRC-32 of the plaintext chunk
		crc := hopcrypto.ChunkCRC32(chunkData)

		// Encrypt the chunk data
		encrypted, err := encryptor.Encrypt(chunkData)
		if err != nil {
			return fmt.Errorf("encrypting chunk %d: %w", chunk.Index, err)
		}

		// Build chunk message: [ChunkHeader][encrypted data]
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
		if err := transport.Send(ctx, chunkMsg); err != nil {
			return fmt.Errorf("sending chunk %d: %w", chunk.Index, err)
		}

		// Wait for CHUNK_ACK (stop-and-wait flow control)
		ackResp, err := transport.Receive(ctx)
		if err != nil {
			return fmt.Errorf("waiting for chunk %d ACK: %w", chunk.Index, err)
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

	// --- Step 5: Send TRANSFER_COMPLETE ---
	complete := &protocol.TransferCompletePayload{
		SHA256:      fileHash,
		TotalChunks: totalChunks,
		TotalBytes:  uint64(totalBytesSent),
	}
	completeMsg := &protocol.Message{
		Type:    protocol.MsgTransferComplete,
		Payload: protocol.EncodeTransferComplete(complete),
	}
	if err := transport.Send(ctx, completeMsg); err != nil {
		return fmt.Errorf("sending TRANSFER_COMPLETE: %w", err)
	}

	if callbacks.OnComplete != nil {
		callbacks.OnComplete(fmt.Sprintf("%x", fileHash))
	}

	return nil
}

// ReceiveFile orchestrates the full receiver-side transfer:
//
//  1. Perform HOP_HELLO_ACK key exchange → derive shared decryption key
//  2. Receive TRANSFER_OFFER and present to user for acceptance
//  3. Receive encrypted chunks, decrypt, verify CRC-32, write to disk
//  4. Verify final SHA-256 hash
func ReceiveFile(ctx context.Context, transport Transport, outputDir string, enableResume bool, callbacks *EngineCallbacks) error {
	if callbacks == nil {
		callbacks = &EngineCallbacks{}
	}

	// --- Step 1: Key exchange ---
	keyPair, err := hopcrypto.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generating key pair: %w", err)
	}

	// Wait for sender's HOP_HELLO
	helloMsg, err := transport.Receive(ctx)
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

	// Version compatibility check
	if !protocol.CurrentVersion.Compatible(peerHello.Version) {
		return fmt.Errorf("incompatible protocol version: peer is %s, we are %s; please upgrade with 'hop update'",
			peerHello.Version.String(), protocol.CurrentVersion.String())
	}

	// Send our HOP_HELLO_ACK
	ack := &protocol.HopHello{
		Version:  protocol.CurrentVersion,
		Features: protocol.AllFeatures(),
	}
	copy(ack.PublicKey[:], keyPair.PublicKeyBytes())

	ackMsg := &protocol.Message{
		Type:    protocol.MsgHopHelloAck,
		Payload: protocol.EncodeHopHello(ack),
	}
	if err := transport.Send(ctx, ackMsg); err != nil {
		return fmt.Errorf("sending HOP_HELLO_ACK: %w", err)
	}

	// Derive shared decryption key
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

	// --- Step 2: Receive TRANSFER_OFFER ---
	offerMsg, err := transport.Receive(ctx)
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

	// Present offer to user for acceptance
	accepted := true
	if callbacks.OnOfferReceived != nil {
		accepted = callbacks.OnOfferReceived(offer)
	}

	if !accepted {
		rejectMsg := &protocol.Message{Type: protocol.MsgTransferReject}
		_ = transport.Send(ctx, rejectMsg)
		return fmt.Errorf("transfer rejected by user")
	}

	// --- Resume detection ---
	var resumeState *ResumeState
	if enableResume {
		state, err := DetectResumable(outputDir, offer)
		if err != nil {
			// Non-fatal: log and start fresh
			resumeState = nil
		} else {
			resumeState = state
		}
	}

	var resumeOffset int64
	if resumeState != nil {
		resumeOffset = resumeState.Offset
		if callbacks.OnResumeDetected != nil {
			callbacks.OnResumeDetected(resumeOffset, offer.FileSize)
		}
	}

	// Send TRANSFER_ACCEPT with resume offset
	acceptPayload := &protocol.TransferAcceptPayload{ResumeOffset: resumeOffset}
	acceptMsg := &protocol.Message{
		Type:    protocol.MsgTransferAccept,
		Payload: protocol.EncodeTransferAccept(acceptPayload),
	}
	if err := transport.Send(ctx, acceptMsg); err != nil {
		return fmt.Errorf("sending TRANSFER_ACCEPT: %w", err)
	}

	// --- Step 3: Receive chunks ---
	outPath := outputDir + string(os.PathSeparator) + offer.FileName
	var outFile *os.File
	fileHasher := hopcrypto.NewFileHasher()
	var totalBytesReceived int64

	if resumeState != nil {
		// Append mode: open existing partial file
		outFile, err = os.OpenFile(outPath, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("opening partial file for resume '%s': %w", outPath, err)
		}

		// Re-feed existing data into the streaming SHA-256 hasher so the
		// final full-file hash is computed correctly. The partial hash was
		// already verified inside DetectResumable, so we don't need to
		// check it again here.
		if rehashErr := feedHasherFromFile(fileHasher, outPath, resumeState.Offset); rehashErr != nil {
			outFile.Close()
			return fmt.Errorf("feeding hasher from partial file: %w", rehashErr)
		}

		totalBytesReceived = resumeState.Offset
	} else {
		// Fresh transfer: create new file
		outFile, err = os.Create(outPath)
		if err != nil {
			return fmt.Errorf("creating output file '%s': %w", outPath, err)
		}
	}
	defer outFile.Close()

	for {
		select {
		case <-ctx.Done():
			rejectMsg := &protocol.Message{Type: protocol.MsgTransferCancel}
			_ = transport.Send(context.Background(), rejectMsg)
			// Write marker so we can resume later
			if enableResume {
				writeResumeMarkerSafe(outputDir, offer, totalBytesReceived, fileHasher)
			}
			return ctx.Err()
		default:
		}

		msg, err := transport.Receive(ctx)
		if err != nil {
			// Write marker so we can resume after connection error
			if enableResume {
				writeResumeMarkerSafe(outputDir, offer, totalBytesReceived, fileHasher)
			}
			return fmt.Errorf("receiving data: %w", err)
		}

		// Handle TRANSFER_COMPLETE
		if msg.Type == protocol.MsgTransferComplete {
			completePayload, err := protocol.DecodeTransferComplete(msg.Payload)
			if err != nil {
				return fmt.Errorf("decoding TRANSFER_COMPLETE: %w", err)
			}

			// --- Step 4: Verify final SHA-256 ---
			if !fileHasher.Verify(completePayload.SHA256) {
				return fmt.Errorf("SHA-256 verification failed: file may be corrupted")
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

		// Handle TRANSFER_CANCEL
		if msg.Type == protocol.MsgTransferCancel {
			if enableResume {
				writeResumeMarkerSafe(outputDir, offer, totalBytesReceived, fileHasher)
			}
			return fmt.Errorf("transfer cancelled by sender")
		}

		// Expect CHUNK_DATA
		if msg.Type != protocol.MsgChunkData {
			return fmt.Errorf("expected CHUNK_DATA or TRANSFER_COMPLETE, got %s", msg.Type)
		}

		// Parse chunk header (16 bytes) + encrypted payload
		if len(msg.Payload) < 16 {
			return fmt.Errorf("chunk message too short: %d bytes", len(msg.Payload))
		}

		chunkHdr, err := protocol.DecodeChunkHeader(msg.Payload[:16])
		if err != nil {
			return fmt.Errorf("decoding chunk header: %w", err)
		}

		encryptedData := msg.Payload[16:]

		// Decrypt
		plaintext, err := decryptor.Decrypt(encryptedData)
		if err != nil {
			return fmt.Errorf("decrypting chunk %d: %w", chunkHdr.Index, err)
		}

		// Verify CRC-32
		if !hopcrypto.VerifyChunkCRC32(plaintext, chunkHdr.CRC32) {
			return fmt.Errorf("CRC-32 mismatch on chunk %d: data corrupted in transit", chunkHdr.Index)
		}

		// Write to disk
		n, err := outFile.Write(plaintext)
		if err != nil {
			// Write marker before returning error so we can resume
			if enableResume {
				writeResumeMarkerSafe(outputDir, offer, totalBytesReceived, fileHasher)
			}
			return fmt.Errorf("writing chunk %d to disk: %w", chunkHdr.Index, err)
		}

		// Feed into file hasher for final verification
		fileHasher.Write(plaintext)

		totalBytesReceived += int64(n)

		// Update resume marker periodically
		if enableResume && chunkHdr.Index > 0 && chunkHdr.Index%markerUpdateInterval == 0 {
			writeResumeMarkerSafe(outputDir, offer, totalBytesReceived, fileHasher)
		}

		// Send CHUNK_ACK
		chunkAck := &protocol.Message{
			Type:    protocol.MsgChunkAck,
			Payload: protocol.EncodeChunkHeader(chunkHdr),
		}
		if err := transport.Send(ctx, chunkAck); err != nil {
			// Write marker before returning
			if enableResume {
				writeResumeMarkerSafe(outputDir, offer, totalBytesReceived, fileHasher)
			}
			return fmt.Errorf("sending CHUNK_ACK for chunk %d: %w", chunkHdr.Index, err)
		}

		if callbacks.OnProgress != nil {
			callbacks.OnProgress(totalBytesReceived, offer.FileSize)
		}
	}
}

// computeFileHash streams the entire file through SHA-256 and returns the hash
// along with the file info. Uses a 1 MB buffer to keep memory flat.
func computeFileHash(filePath string) ([32]byte, os.FileInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return [32]byte{}, nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return [32]byte{}, nil, err
	}

	hasher := hopcrypto.NewFileHasher()
	buf := make([]byte, DefaultChunkSize) // 1 MB buffer
	for {
		n, err := f.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return [32]byte{}, nil, fmt.Errorf("reading file for hash: %w", err)
		}
	}

	return hasher.Sum(), info, nil
}

// feedHasherFromFile reads a file up to `length` bytes and feeds the data into
// the given FileHasher. This is used during resume to rebuild the streaming
// SHA-256 state from the existing partial file.
func feedHasherFromFile(hasher *hopcrypto.FileHasher, path string, length int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, DefaultChunkSize)
	var fed int64

	for fed < length {
		toRead := int64(len(buf))
		if toRead > length-fed {
			toRead = length - fed
		}

		n, err := f.Read(buf[:toRead])
		if err != nil && err != io.EOF {
			return fmt.Errorf("reading file at offset %d: %w", fed, err)
		}
		if n == 0 {
			break
		}

		hasher.Write(buf[:n])
		fed += int64(n)
	}

	return nil
}

// writeResumeMarkerSafe writes a resume marker, ignoring errors.
// Used in error paths where we want best-effort persistence.
func writeResumeMarkerSafe(dir string, offer *protocol.TransferOffer, bytesReceived int64, hasher *hopcrypto.FileHasher) {
	partialHash := hasher.Sum()
	chunkIndex := uint64(bytesReceived / DefaultChunkSize)
	_ = WriteMarker(dir, offer.SHA256, bytesReceived, chunkIndex, partialHash, offer.ChunkSize, offer.FileName)
}

// SendFileBrowserMode handles the sender side of a browser bridge download.
// Unlike SendFile, this does NOT use E2E encryption — the relay handles TLS
// to the browser. Chunks are sent in plaintext with CRC-32 integrity.
//
// The flow is:
//  1. Wait for BROWSER_INFO_REQ → respond with file metadata
//  2. Wait for BROWSER_DOWNLOAD_START → stream plaintext chunks
//  3. Send TRANSFER_COMPLETE with SHA-256 hash
//  4. Loop: wait for more browser requests until cancelled
//
// This function blocks until the context is cancelled (sender Ctrl+C).
func SendFileBrowserMode(ctx context.Context, transport Transport, filePath string, limiter *TokenBucketLimiter, callbacks *EngineCallbacks) error {
	if callbacks == nil {
		callbacks = &EngineCallbacks{}
	}

	// Pre-compute file SHA-256
	fileHash, fileInfo, err := computeFileHash(filePath)
	if err != nil {
		return fmt.Errorf("computing file hash: %w", err)
	}

	// Main loop: serve browser requests until sender disconnects
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Wait for a message from the relay
		msg, err := transport.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("receiving browser bridge message: %w", err)
		}

		switch msg.Type {
		case protocol.MsgBrowserInfoReq:
			// Respond with file metadata
			info := &protocol.BrowserInfoResponse{
				FileName:  fileInfo.Name(),
				FileSize:  fileInfo.Size(),
				SHA256:    fileHash,
				ChunkSize: DefaultChunkSize,
			}
			resp := &protocol.Message{
				Type:    protocol.MsgBrowserInfoResp,
				Payload: protocol.EncodeBrowserInfoResponse(info),
			}
			if err := transport.Send(ctx, resp); err != nil {
				return fmt.Errorf("sending browser info response: %w", err)
			}

		case protocol.MsgBrowserDownloadStart:
			// A browser download has begun — stream the file
			if callbacks.OnBrowserDownload != nil {
				callbacks.OnBrowserDownload()
			}

			err := sendFileToBrowser(ctx, transport, filePath, fileHash, fileInfo, limiter, callbacks)
			if err != nil {
				if callbacks.OnError != nil {
					callbacks.OnError(err)
				}
				// Don't return — allow more downloads. Only fatal errors
				// (like ctx cancellation) should exit the loop.
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}

		case protocol.MsgHopHello:
			// This is a CLI-to-CLI transfer — return a sentinel error
			// so the caller can switch to the regular SendFile path.
			return ErrCLIReceiverDetected{Msg: msg}

		default:
			// Unexpected message type — log and continue
			continue
		}
	}
}

// ErrCLIReceiverDetected is returned by SendFileBrowserMode when a CLI receiver
// connects instead of a browser. The caller should switch to the regular
// SendFile path using the contained HOP_HELLO message.
type ErrCLIReceiverDetected struct {
	Msg *protocol.Message // The HOP_HELLO message from the CLI receiver
}

func (e ErrCLIReceiverDetected) Error() string {
	return "CLI receiver detected — switch to SendFile"
}

// sendFileToBrowser streams a single file to a browser via the relay.
// Sends plaintext chunks with CRC-32 integrity (no encryption).
func sendFileToBrowser(ctx context.Context, transport Transport, filePath string, fileHash [32]byte, fileInfo os.FileInfo, limiter *TokenBucketLimiter, callbacks *EngineCallbacks) error {
	chunker, err := NewChunker(filePath)
	if err != nil {
		return fmt.Errorf("creating chunker: %w", err)
	}
	defer chunker.Close()

	var totalBytesSent int64

	for {
		select {
		case <-ctx.Done():
			cancelMsg := &protocol.Message{Type: protocol.MsgTransferCancel}
			_ = transport.Send(context.Background(), cancelMsg)
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

		// Apply rate limiting
		if limiter != nil {
			limiter.Wait(chunk.Size)
		}

		// Compute CRC-32 (integrity check)
		crc := hopcrypto.ChunkCRC32(chunkData)

		// Build chunk message: [ChunkHeader][plaintext data]
		// No encryption for browser mode — TLS handles security.
		hdr := &protocol.ChunkHeader{
			Index: chunk.Index,
			Size:  uint32(chunk.Size),
			CRC32: crc,
		}
		hdrBytes := protocol.EncodeChunkHeader(hdr)
		payload := make([]byte, len(hdrBytes)+len(chunkData))
		copy(payload, hdrBytes)
		copy(payload[len(hdrBytes):], chunkData)

		chunkMsg := &protocol.Message{
			Type:    protocol.MsgChunkData,
			Payload: payload,
		}
		if err := transport.Send(ctx, chunkMsg); err != nil {
			return fmt.Errorf("sending chunk %d: %w", chunk.Index, err)
		}

		// Wait for ACK (stop-and-wait)
		ackMsg, err := transport.Receive(ctx)
		if err != nil {
			return fmt.Errorf("waiting for chunk %d ACK: %w", chunk.Index, err)
		}

		if ackMsg.Type == protocol.MsgBrowserDownloadCancel {
			return fmt.Errorf("browser download cancelled")
		}
		if ackMsg.Type == protocol.MsgTransferCancel {
			return fmt.Errorf("transfer cancelled by relay")
		}
		if ackMsg.Type != protocol.MsgChunkAck {
			return fmt.Errorf("expected CHUNK_ACK, got %s", ackMsg.Type)
		}

		totalBytesSent += int64(chunk.Size)

		if callbacks.OnProgress != nil {
			callbacks.OnProgress(totalBytesSent, fileInfo.Size())
		}
	}

	// Send TRANSFER_COMPLETE
	complete := &protocol.TransferCompletePayload{
		SHA256:     fileHash,
		TotalBytes: uint64(totalBytesSent),
	}
	completeMsg := &protocol.Message{
		Type:    protocol.MsgTransferComplete,
		Payload: protocol.EncodeTransferComplete(complete),
	}
	if err := transport.Send(ctx, completeMsg); err != nil {
		return fmt.Errorf("sending TRANSFER_COMPLETE: %w", err)
	}

	if callbacks.OnComplete != nil {
		callbacks.OnComplete(fmt.Sprintf("%x", fileHash))
	}

	return nil
}
