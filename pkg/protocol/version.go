package protocol

import "fmt"

// ProtocolVersion represents a semantic version for the hop wire protocol.
// Minor version differences are backward-compatible (new optional features).
// Major version changes indicate breaking protocol changes.
type ProtocolVersion struct {
	Major int
	Minor int
}

// CurrentVersion is the protocol version for this build.
var CurrentVersion = ProtocolVersion{Major: 1, Minor: 0}

// String returns the protocol version string (e.g., "HOP/1.0").
func (v ProtocolVersion) String() string {
	return fmt.Sprintf("HOP/%d.%d", v.Major, v.Minor)
}

// Compatible checks if another protocol version is compatible with this one.
// Compatibility requires the same major version.
func (v ProtocolVersion) Compatible(other ProtocolVersion) bool {
	return v.Major == other.Major
}

// FeatureFlags represents optional features supported by a peer.
type FeatureFlags uint32

const (
	FeatureCompression FeatureFlags = 1 << iota // zstd compression support
	FeatureResume                                // Chunk-level resume support
	FeatureBrowserBridge                         // Browser Bridge support
	FeatureTunneling                             // HTTP tunnel support
)

// Has checks if a specific feature flag is set.
func (f FeatureFlags) Has(flag FeatureFlags) bool {
	return f&flag != 0
}

// AllFeatures returns all features supported by this build.
func AllFeatures() FeatureFlags {
	return FeatureCompression | FeatureResume | FeatureBrowserBridge | FeatureTunneling
}

// Negotiate returns the intersection of two feature flag sets.
func Negotiate(local, remote FeatureFlags) FeatureFlags {
	return local & remote
}
