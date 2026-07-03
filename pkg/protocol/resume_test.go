package protocol

import (
	"bytes"
	"testing"
)

func TestResumeRequestEncodeDecode(t *testing.T) {
	original := &ResumeRequestPayload{
		Offset:     209715200, // 200 MB
		ChunkIndex: 200,
	}
	copy(original.SHA256Prefix[:], []byte{0xa3, 0xf2, 0xb8, 0x01, 0x02, 0x03, 0x04, 0x05})
	copy(original.PartialHash[:], bytes.Repeat([]byte{0xab}, 32))

	data := EncodeResumeRequest(original)

	decoded, err := DecodeResumeRequest(data)
	if err != nil {
		t.Fatalf("DecodeResumeRequest: %v", err)
	}

	if original.SHA256Prefix != decoded.SHA256Prefix {
		t.Errorf("SHA256Prefix mismatch: got %x, want %x", decoded.SHA256Prefix, original.SHA256Prefix)
	}
	if original.Offset != decoded.Offset {
		t.Errorf("Offset mismatch: got %d, want %d", decoded.Offset, original.Offset)
	}
	if original.ChunkIndex != decoded.ChunkIndex {
		t.Errorf("ChunkIndex mismatch: got %d, want %d", decoded.ChunkIndex, original.ChunkIndex)
	}
	if original.PartialHash != decoded.PartialHash {
		t.Errorf("PartialHash mismatch: got %x, want %x", decoded.PartialHash, original.PartialHash)
	}
}

func TestResumeRequestDecodeShort(t *testing.T) {
	_, err := DecodeResumeRequest([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for short payload")
	}
}

func TestResumeAcceptEncodeDecode(t *testing.T) {
	tests := []struct {
		name     string
		payload  *ResumeAcceptPayload
	}{
		{
			name: "accepted",
			payload: &ResumeAcceptPayload{
				Offset:     209715200,
				ChunkIndex: 200,
				Accepted:   true,
			},
		},
		{
			name: "rejected",
			payload: &ResumeAcceptPayload{
				Offset:     0,
				ChunkIndex: 0,
				Accepted:   false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := EncodeResumeAccept(tt.payload)

			decoded, err := DecodeResumeAccept(data)
			if err != nil {
				t.Fatalf("DecodeResumeAccept: %v", err)
			}

			if tt.payload.Offset != decoded.Offset {
				t.Errorf("Offset mismatch: got %d, want %d", decoded.Offset, tt.payload.Offset)
			}
			if tt.payload.ChunkIndex != decoded.ChunkIndex {
				t.Errorf("ChunkIndex mismatch: got %d, want %d", decoded.ChunkIndex, tt.payload.ChunkIndex)
			}
			if tt.payload.Accepted != decoded.Accepted {
				t.Errorf("Accepted mismatch: got %v, want %v", decoded.Accepted, tt.payload.Accepted)
			}
		})
	}
}

func TestResumeAcceptDecodeShort(t *testing.T) {
	_, err := DecodeResumeAccept([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for short payload")
	}
}

func TestResumeRequestZeroValues(t *testing.T) {
	// Test with zero offset and chunk index (edge case)
	original := &ResumeRequestPayload{
		Offset:     0,
		ChunkIndex: 0,
	}

	data := EncodeResumeRequest(original)
	decoded, err := DecodeResumeRequest(data)
	if err != nil {
		t.Fatalf("DecodeResumeRequest: %v", err)
	}

	if decoded.Offset != 0 {
		t.Errorf("expected zero offset, got %d", decoded.Offset)
	}
	if decoded.ChunkIndex != 0 {
		t.Errorf("expected zero chunk index, got %d", decoded.ChunkIndex)
	}
}
