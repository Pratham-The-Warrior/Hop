package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SessionClaims are the JWT claims bound to a client session.
type SessionClaims struct {
	jwt.RegisteredClaims
	PublicKeyFingerprint string `json:"pkfp"`
	ProtocolVersion      string `json:"proto"`
}

// AuthRequest is the JSON body sent by a client to authenticate.
type AuthRequest struct {
	PublicKey       []byte `json:"public_key"`        // Ed25519 public key (32 bytes)
	ProtocolVersion string `json:"protocol_version"`  // e.g. "HOP/1.0"
}

// AuthResponse is the JSON body returned after successful authentication.
type AuthResponse struct {
	SessionToken string `json:"session_token"` // JWT
	ExpiresAt    int64  `json:"expires_at"`    // Unix timestamp
}

// Authenticator manages session authentication using Ed25519 + JWT.
type Authenticator struct {
	mu        sync.RWMutex
	jwtSecret []byte // HMAC-SHA256 signing key
	sessions  map[string]*Session // fingerprint → session
}

// Session represents an authenticated client session.
type Session struct {
	PublicKey   ed25519.PublicKey
	Fingerprint string
	Token       string // JWT token
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// NewAuthenticator creates a new Authenticator with a random JWT signing secret.
func NewAuthenticator() (*Authenticator, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generating JWT secret: %w", err)
	}

	return &Authenticator{
		jwtSecret: secret,
		sessions:  make(map[string]*Session),
	}, nil
}

// HandleAuth processes authentication requests.
// Client sends Ed25519 public key + protocol version, receives a JWT session token.
func (a *Authenticator) HandleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate Ed25519 public key (must be exactly 32 bytes)
	if len(req.PublicKey) != ed25519.PublicKeySize {
		http.Error(w, fmt.Sprintf("invalid public key size: expected %d bytes, got %d",
			ed25519.PublicKeySize, len(req.PublicKey)), http.StatusBadRequest)
		return
	}

	// Validate protocol version
	if req.ProtocolVersion == "" {
		http.Error(w, "protocol version required", http.StatusBadRequest)
		return
	}

	// Compute public key fingerprint (SHA-256 of the public key, hex-encoded)
	fingerprint := publicKeyFingerprint(req.PublicKey)

	// Generate JWT with 24-hour expiry
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	claims := SessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fingerprint,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			Issuer:    "hop-relay",
		},
		PublicKeyFingerprint: fingerprint,
		ProtocolVersion:      req.ProtocolVersion,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(a.jwtSecret)
	if err != nil {
		http.Error(w, "internal error generating session token", http.StatusInternalServerError)
		return
	}

	// Store the session
	session := &Session{
		PublicKey:   ed25519.PublicKey(req.PublicKey),
		Fingerprint: fingerprint,
		Token:       tokenString,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
	}

	a.mu.Lock()
	a.sessions[fingerprint] = session
	a.mu.Unlock()

	// Return the session token
	resp := AuthResponse{
		SessionToken: tokenString,
		ExpiresAt:    expiresAt.Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ValidateToken verifies a JWT session token and returns the associated session.
func (a *Authenticator) ValidateToken(tokenString string) (*Session, error) {
	claims := &SessionClaims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid session token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("session token is not valid")
	}

	// Look up the stored session
	a.mu.RLock()
	session, exists := a.sessions[claims.PublicKeyFingerprint]
	a.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("session not found for fingerprint %s", claims.PublicKeyFingerprint)
	}

	return session, nil
}

// RemoveSession removes a session by fingerprint.
func (a *Authenticator) RemoveSession(fingerprint string) {
	a.mu.Lock()
	delete(a.sessions, fingerprint)
	a.mu.Unlock()
}

// CleanupExpired removes all expired sessions. Called periodically.
func (a *Authenticator) CleanupExpired() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	removed := 0
	for fp, session := range a.sessions {
		if now.After(session.ExpiresAt) {
			delete(a.sessions, fp)
			removed++
		}
	}
	return removed
}

// SessionCount returns the number of active sessions.
func (a *Authenticator) SessionCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.sessions)
}

// publicKeyFingerprint computes a hex-encoded SHA-256 fingerprint of a public key.
func publicKeyFingerprint(pubKey []byte) string {
	hash := sha256.Sum256(pubKey)
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes (128 bits) for brevity
}
