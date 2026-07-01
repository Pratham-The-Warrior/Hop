package token

import (
	"testing"
)

func TestGenerate(t *testing.T) {
	tok := Generate()
	if !Validate(tok) {
		t.Fatalf("generated token '%s' does not pass validation", tok)
	}
}

func TestGenerateUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	count := 1000

	for i := 0; i < count; i++ {
		tok := Generate()
		if seen[tok] {
			t.Fatalf("duplicate token generated after %d attempts: %s", i, tok)
		}
		seen[tok] = true
	}
}

func TestGenerateFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		tok := Generate()
		w1, w2, num, ok := ParseToken(tok)
		if !ok {
			t.Fatalf("failed to parse token: %s", tok)
		}
		if w1 == "" || w2 == "" {
			t.Fatalf("empty word in token: %s", tok)
		}
		if w1 == w2 {
			t.Fatalf("both words are the same in token: %s", tok)
		}
		if num < 0 || num > 99 {
			t.Fatalf("number out of range in token: %s (num=%d)", tok, num)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"summer-surf-14", true},
		{"bright-moon-07", true},
		{"green-river-22", true},
		{"hello-world-00", true},
		{"hello-world-99", true},
		{"", false},
		{"hello", false},
		{"hello-world", false},
		{"hello-world-1", false},     // Only 1 digit
		{"hello-world-100", false},   // 3 digits
		{"Hello-World-14", false},    // Uppercase
		{"hello-world-ab", false},    // Non-numeric suffix
		{"hello_world_14", false},    // Underscores
		{"a-b-c-14", false},          // Extra hyphen
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := Validate(tt.input)
			if result != tt.valid {
				t.Errorf("Validate(%q) = %v, want %v", tt.input, result, tt.valid)
			}
		})
	}
}

func TestParseToken(t *testing.T) {
	w1, w2, num, ok := ParseToken("summer-surf-14")
	if !ok {
		t.Fatal("expected successful parse")
	}
	if w1 != "summer" || w2 != "surf" || num != 14 {
		t.Fatalf("unexpected parse result: w1=%s w2=%s num=%d", w1, w2, num)
	}

	_, _, _, ok = ParseToken("invalid")
	if ok {
		t.Fatal("expected parse to fail for invalid input")
	}
}
