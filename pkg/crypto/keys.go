package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// KeyPair holds an X25519 public/private key pair for Diffie-Hellman exchange.
type KeyPair struct {
	Private *ecdh.PrivateKey
	Public  *ecdh.PublicKey
}

// GenerateKeyPair generates a new X25519 key pair for ECDH key exchange.
func GenerateKeyPair() (*KeyPair, error) {
	curve := ecdh.X25519()
	private, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating X25519 key pair: %w", err)
	}

	return &KeyPair{
		Private: private,
		Public:  private.PublicKey(),
	}, nil
}

// PublicKeyBytes returns the raw public key bytes for transmission.
func (kp *KeyPair) PublicKeyBytes() []byte {
	return kp.Public.Bytes()
}

// DeriveSharedSecret performs the X25519 ECDH computation to derive a shared
// secret from our private key and the peer's public key.
func DeriveSharedSecret(privateKey *ecdh.PrivateKey, peerPublicKeyBytes []byte) ([]byte, error) {
	curve := ecdh.X25519()
	peerPublic, err := curve.NewPublicKey(peerPublicKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing peer public key: %w", err)
	}

	shared, err := privateKey.ECDH(peerPublic)
	if err != nil {
		return nil, fmt.Errorf("computing shared secret: %w", err)
	}

	return shared, nil
}

// DeriveEncryptionKey uses HKDF-SHA256 to derive a 32-byte ChaCha20 key
// from the raw ECDH shared secret. This adds proper key separation and
// domain separation via the info parameter.
func DeriveEncryptionKey(sharedSecret []byte, info string) ([]byte, error) {
	if info == "" {
		info = "hop-chacha20-key"
	}

	hkdfReader := hkdf.New(sha256.New, sharedSecret, nil, []byte(info))
	key := make([]byte, 32) // ChaCha20 requires a 256-bit key

	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("deriving encryption key: %w", err)
	}

	return key, nil
}
