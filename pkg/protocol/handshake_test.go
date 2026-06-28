package protocol

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestHopHelloRoundtrip(t *testing.T) {
	original := &HopHello{
		Version:  ProtocolVersion{Major: 1, Minor: 0},
		Features: FeatureCompression | FeatureResume,
	}
	// Fill public key with random data
	if _, err := rand.Read(original.PublicKey[:]); err != nil {
		t.Fatalf("generating random key: %v", err)
	}

	encoded := EncodeHopHello(original)
	if len(encoded) != hopHelloSize {
		t.Fatalf("encoded size = %d, want %d", len(encoded), hopHelloSize)
	}

	decoded, err := DecodeHopHello(encoded)
	if err != nil {
		t.Fatalf("DecodeHopHello: %v", err)
	}

	if decoded.PublicKey != original.PublicKey {
		t.Errorf("PublicKey mismatch")
	}
	if decoded.Version != original.Version {
		t.Errorf("Version = %v, want %v", decoded.Version, original.Version)
	}
	if decoded.Features != original.Features {
		t.Errorf("Features = %v, want %v", decoded.Features, original.Features)
	}
}

func TestHopHelloDecodeShort(t *testing.T) {
	_, err := DecodeHopHello(make([]byte, 10))
	if err == nil {
		t.Fatal("expected error for short payload")
	}
}

func TestHopHelloAllFeatures(t *testing.T) {
	original := &HopHello{
		Version:  ProtocolVersion{Major: 2, Minor: 3},
		Features: AllFeatures(),
	}
	if _, err := rand.Read(original.PublicKey[:]); err != nil {
		t.Fatalf("generating random key: %v", err)
	}

	decoded, err := DecodeHopHello(EncodeHopHello(original))
	if err != nil {
		t.Fatalf("DecodeHopHello: %v", err)
	}

	if !decoded.Features.Has(FeatureCompression) {
		t.Error("missing FeatureCompression")
	}
	if !decoded.Features.Has(FeatureResume) {
		t.Error("missing FeatureResume")
	}
	if !decoded.Features.Has(FeatureBrowserBridge) {
		t.Error("missing FeatureBrowserBridge")
	}
	if !decoded.Features.Has(FeatureTunneling) {
		t.Error("missing FeatureTunneling")
	}
}

func TestTransferAcceptRoundtrip(t *testing.T) {
	original := &TransferAcceptPayload{
		ResumeOffset: 1048576, // 1 MB
	}

	encoded := EncodeTransferAccept(original)
	decoded, err := DecodeTransferAccept(encoded)
	if err != nil {
		t.Fatalf("DecodeTransferAccept: %v", err)
	}

	if decoded.ResumeOffset != original.ResumeOffset {
		t.Errorf("ResumeOffset = %d, want %d", decoded.ResumeOffset, original.ResumeOffset)
	}
}

func TestTransferAcceptZeroOffset(t *testing.T) {
	original := &TransferAcceptPayload{ResumeOffset: 0}

	decoded, err := DecodeTransferAccept(EncodeTransferAccept(original))
	if err != nil {
		t.Fatalf("DecodeTransferAccept: %v", err)
	}

	if decoded.ResumeOffset != 0 {
		t.Errorf("ResumeOffset = %d, want 0", decoded.ResumeOffset)
	}
}

func TestTransferCompleteRoundtrip(t *testing.T) {
	original := &TransferCompletePayload{
		TotalChunks: 1024,
		TotalBytes:  1073741824, // 1 GB
	}
	if _, err := rand.Read(original.SHA256[:]); err != nil {
		t.Fatalf("generating random hash: %v", err)
	}

	encoded := EncodeTransferComplete(original)
	decoded, err := DecodeTransferComplete(encoded)
	if err != nil {
		t.Fatalf("DecodeTransferComplete: %v", err)
	}

	if !bytes.Equal(decoded.SHA256[:], original.SHA256[:]) {
		t.Error("SHA256 mismatch")
	}
	if decoded.TotalChunks != original.TotalChunks {
		t.Errorf("TotalChunks = %d, want %d", decoded.TotalChunks, original.TotalChunks)
	}
	if decoded.TotalBytes != original.TotalBytes {
		t.Errorf("TotalBytes = %d, want %d", decoded.TotalBytes, original.TotalBytes)
	}
}

func TestTransferCompleteDecodeShort(t *testing.T) {
	_, err := DecodeTransferComplete(make([]byte, 5))
	if err == nil {
		t.Fatal("expected error for short payload")
	}
}
