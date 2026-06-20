package crypto

import (
	"encoding/binary"
	"fmt"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
)

// Encryptor provides ChaCha20-Poly1305 AEAD encryption with automatic
// sequential nonce management. Each instance is bound to a single session key.
type Encryptor struct {
	mu       sync.Mutex
	aead     interface{ Seal(dst, nonce, plaintext, additionalData []byte) []byte }
	counter  uint64
}

// Decryptor provides ChaCha20-Poly1305 AEAD decryption.
type Decryptor struct {
	mu   sync.Mutex
	aead interface {
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
		NonceSize() int
		Overhead() int
	}
}

// NewEncryptor creates a new ChaCha20-Poly1305 encryptor with the given 32-byte key.
func NewEncryptor(key []byte) (*Encryptor, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("creating ChaCha20-Poly1305 cipher: %w", err)
	}
	return &Encryptor{
		aead:    aead,
		counter: 0,
	}, nil
}

// NewDecryptor creates a new ChaCha20-Poly1305 decryptor with the given 32-byte key.
func NewDecryptor(key []byte) (*Decryptor, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("creating ChaCha20-Poly1305 cipher: %w", err)
	}
	return &Decryptor{aead: aead}, nil
}

// Encrypt encrypts plaintext using ChaCha20-Poly1305 with an auto-incrementing
// nonce. Returns the ciphertext (including Poly1305 authentication tag) prepended
// with the 12-byte nonce.
func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	e.mu.Lock()
	nonce := makeNonce(e.counter)
	e.counter++
	e.mu.Unlock()

	// Seal appends the ciphertext + auth tag to dst
	ciphertext := e.aead.Seal(nil, nonce, plaintext, nil)

	// Prepend nonce to ciphertext for self-describing packets
	result := make([]byte, len(nonce)+len(ciphertext))
	copy(result[:len(nonce)], nonce)
	copy(result[len(nonce):], ciphertext)

	return result, nil
}

// EncryptWithNonce encrypts plaintext with a specific nonce (for cases where
// nonce is managed externally, such as chunk index-based nonces).
func (e *Encryptor) EncryptWithNonce(plaintext, nonce []byte) ([]byte, error) {
	ciphertext := e.aead.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext that was produced by Encrypt (nonce-prepended format).
// Returns the original plaintext or an error if authentication fails.
func (d *Decryptor) Decrypt(data []byte) ([]byte, error) {
	nonceSize := d.aead.NonceSize()
	if len(data) < nonceSize+d.aead.Overhead() {
		return nil, fmt.Errorf("ciphertext too short: need at least %d bytes, got %d",
			nonceSize+d.aead.Overhead(), len(data))
	}

	nonce := data[:nonceSize]
	ciphertext := data[nonceSize:]

	plaintext, err := d.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (authentication error): %w", err)
	}

	return plaintext, nil
}

// DecryptWithNonce decrypts ciphertext using a specific nonce.
func (d *Decryptor) DecryptWithNonce(ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := d.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}
	return plaintext, nil
}

// makeNonce creates a 12-byte nonce from a 64-bit counter.
// The counter is placed in the last 8 bytes (little-endian), with the
// first 4 bytes zeroed. This supports 2^64 chunks before nonce reuse.
func makeNonce(counter uint64) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSize) // 12 bytes
	binary.LittleEndian.PutUint64(nonce[4:], counter)
	return nonce
}

// MakeChunkNonce creates a nonce from a chunk index. Useful when the
// sender and receiver need to independently derive the same nonce for
// a given chunk without exchanging it.
func MakeChunkNonce(chunkIndex uint64) []byte {
	return makeNonce(chunkIndex)
}
