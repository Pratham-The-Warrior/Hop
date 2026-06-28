package config

import "os"

const (
	// DefaultRelayURL is used when no HOP_RELAY env var is set.
	// Set to localhost for development since no production relay is deployed yet.
	DefaultRelayURL = "http://localhost:9999"
)

// RelayURL returns the relay server URL from the HOP_RELAY environment
// variable, falling back to DefaultRelayURL if unset.
func RelayURL() string {
	if url := os.Getenv("HOP_RELAY"); url != "" {
		return url
	}
	return DefaultRelayURL
}
