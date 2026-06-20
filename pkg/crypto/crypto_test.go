package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// --- Key Exchange Tests ---

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}
	if kp.Private == nil {
		t.Fatal("private key is nil")
	}
	if kp.Public == nil {
		t.Fatal("public key is nil")
	}
	if len(kp.PublicKeyBytes()) != 32 {
		t.Fatalf("expected 32-byte public key, got %d bytes", len(kp.PublicKeyBytes()))
	}
}

func TestSharedSecretAgreement(t *testing.T) {
	// Two parties generate their own key pairs
	alice, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Alice key gen: %v", err)
	}
	bob, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Bob key gen: %v", err)
	}

	// Both derive the shared secret using the other's public key
	aliceSecret, err := DeriveSharedSecret(alice.Private, bob.PublicKeyBytes())
	if err != nil {
		t.Fatalf("Alice DeriveSharedSecret: %v", err)
	}
	bobSecret, err := DeriveSharedSecret(bob.Private, alice.PublicKeyBytes())
	if err != nil {
		t.Fatalf("Bob DeriveSharedSecret: %v", err)
	}

	// Both must derive the same shared secret
	if !bytes.Equal(aliceSecret, bobSecret) {
		t.Fatal("shared secrets do not match between Alice and Bob")
	}
}

func TestDeriveEncryptionKey(t *testing.T) {
	// Generate a fake shared secret
	sharedSecret := make([]byte, 32)
	rand.Read(sharedSecret)

	key, err := DeriveEncryptionKey(sharedSecret, "hop-chacha20-key")
	if err != nil {
		t.Fatalf("DeriveEncryptionKey: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d bytes", len(key))
	}

	// Same input must produce the same output (deterministic)
	key2, err := DeriveEncryptionKey(sharedSecret, "hop-chacha20-key")
	if err != nil {
		t.Fatalf("DeriveEncryptionKey (2nd call): %v", err)
	}
	if !bytes.Equal(key, key2) {
		t.Fatal("HKDF is not deterministic")
	}

	// Different info strings must produce different keys
	key3, err := DeriveEncryptionKey(sharedSecret, "hop-different-key")
	if err != nil {
		t.Fatalf("DeriveEncryptionKey (different info): %v", err)
	}
	if bytes.Equal(key, key3) {
		t.Fatal("different info strings should produce different keys")
	}
}

// --- Encryption/Decryption Tests ---

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	dec, err := NewDecryptor(key)
	if err != nil {
		t.Fatalf("NewDecryptor: %v", err)
	}

	testCases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"single byte", []byte{0x42}},
		{"small text", []byte("hello, hop!")},
		{"1KB", make([]byte, 1024)},
		{"1MB", make([]byte, 1024*1024)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Fill with random data for non-empty cases
			if len(tc.data) > 0 {
				rand.Read(tc.data)
			}

			ciphertext, err := enc.Encrypt(tc.data)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}

			// Ciphertext must be larger than plaintext (nonce + tag overhead)
			if len(ciphertext) <= len(tc.data) {
				t.Fatal("ciphertext should be larger than plaintext")
			}

			plaintext, err := dec.Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}

			if !bytes.Equal(plaintext, tc.data) {
				t.Fatal("decrypted plaintext does not match original")
			}
		})
	}
}

func TestTamperDetection(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	dec, err := NewDecryptor(key)
	if err != nil {
		t.Fatalf("NewDecryptor: %v", err)
	}

	plaintext := []byte("sensitive data that must not be tampered with")
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Flip a bit in the ciphertext (after the nonce)
	if len(ciphertext) > 13 {
		ciphertext[13] ^= 0x01
	}

	_, err = dec.Decrypt(ciphertext)
	if err == nil {
		t.Fatal("expected decryption to fail on tampered ciphertext, but it succeeded")
	}
}

func TestWrongKeyDecryption(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	enc, _ := NewEncryptor(key1)
	dec, _ := NewDecryptor(key2) // Wrong key!

	ciphertext, _ := enc.Encrypt([]byte("secret message"))
	_, err := dec.Decrypt(ciphertext)
	if err == nil {
		t.Fatal("decryption with wrong key should fail")
	}
}

// --- Integrity Tests ---

func TestCRC32Consistency(t *testing.T) {
	data := []byte("test data for CRC-32 consistency check")

	crc1 := ChunkCRC32(data)
	crc2 := ChunkCRC32(data)

	if crc1 != crc2 {
		t.Fatalf("CRC-32 not consistent: %d vs %d", crc1, crc2)
	}

	if !VerifyChunkCRC32(data, crc1) {
		t.Fatal("VerifyChunkCRC32 failed for correct checksum")
	}

	// Modified data should produce different CRC
	modified := make([]byte, len(data))
	copy(modified, data)
	modified[0] ^= 0x01

	if VerifyChunkCRC32(modified, crc1) {
		t.Fatal("VerifyChunkCRC32 should fail for modified data")
	}
}

func TestSHA256StreamingHasher(t *testing.T) {
	// Known test vector: SHA-256 of empty string
	hasher := NewFileHasher()
	emptyHash := hasher.Sum()
	expectedEmpty := QuickSHA256([]byte{})
	if emptyHash != expectedEmpty {
		t.Fatal("empty hash mismatch")
	}

	// Test streaming: hash "hello world" in two chunks
	hasher.Reset()
	hasher.Write([]byte("hello "))
	hasher.Write([]byte("world"))

	fullHash := QuickSHA256([]byte("hello world"))
	if !hasher.Verify(fullHash) {
		t.Fatal("streaming hash does not match single-shot hash")
	}

	if hasher.BytesHashed() != 11 {
		t.Fatalf("expected 11 bytes hashed, got %d", hasher.BytesHashed())
	}
}

func TestSHA256Prefix(t *testing.T) {
	hasher := NewFileHasher()
	hasher.Write([]byte("test"))
	prefix := hasher.SumPrefix()

	// Should be in format "xxxxxx...xxxx"
	if len(prefix) != 13 { // 6 + 3 + 4
		t.Fatalf("unexpected prefix length: %d (got '%s')", len(prefix), prefix)
	}
}

// --- Full Key Exchange + Encryption Integration Test ---

func TestFullKeyExchangeAndEncryption(t *testing.T) {
	// Simulate a complete Alice <-> Bob key exchange + encrypted transfer
	alice, _ := GenerateKeyPair()
	bob, _ := GenerateKeyPair()

	// Exchange public keys and derive shared secrets
	aliceSecret, _ := DeriveSharedSecret(alice.Private, bob.PublicKeyBytes())
	bobSecret, _ := DeriveSharedSecret(bob.Private, alice.PublicKeyBytes())

	// Derive encryption keys
	aliceKey, _ := DeriveEncryptionKey(aliceSecret, "hop-chacha20-key")
	bobKey, _ := DeriveEncryptionKey(bobSecret, "hop-chacha20-key")

	if !bytes.Equal(aliceKey, bobKey) {
		t.Fatal("derived encryption keys don't match")
	}

	// Alice encrypts, Bob decrypts
	aliceEnc, _ := NewEncryptor(aliceKey)
	bobDec, _ := NewDecryptor(bobKey)

	message := []byte("This is a secret file chunk transferred via hop")
	ciphertext, err := aliceEnc.Encrypt(message)
	if err != nil {
		t.Fatalf("Alice encrypt: %v", err)
	}

	plaintext, err := bobDec.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Bob decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, message) {
		t.Fatal("Bob received different message than Alice sent")
	}
}
