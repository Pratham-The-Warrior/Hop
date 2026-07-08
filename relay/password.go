package main

import (
	"golang.org/x/crypto/bcrypt"
)

// bcryptCompare compares a plaintext password with a bcrypt hash.
// Returns nil if they match.
func bcryptCompare(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
