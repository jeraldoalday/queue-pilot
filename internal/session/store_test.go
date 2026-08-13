package session

import (
	"testing"
	"time"
)

func TestExpired(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		now       time.Time
		expiresAt time.Time
		want      bool
	}{
		{"well before expiry", base, base.Add(time.Hour), false},
		{"exact expiry instant", base, base, false},
		{"one second past expiry", base.Add(time.Second), base, true},
		{"well past expiry", base.Add(24 * time.Hour), base, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expired(tt.now, tt.expiresAt); got != tt.want {
				t.Errorf("expired(%v, %v) = %v, want %v", tt.now, tt.expiresAt, got, tt.want)
			}
		})
	}
}

func TestNewTokenIsUniqueAndURLSafe(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		tok, err := newToken()
		if err != nil {
			t.Fatalf("newToken: %v", err)
		}
		if len(tok) == 0 {
			t.Fatal("newToken returned empty string")
		}
		if seen[tok] {
			t.Fatalf("newToken produced a duplicate: %s", tok)
		}
		seen[tok] = true
		for _, r := range tok {
			if r == '+' || r == '/' || r == '=' {
				t.Fatalf("newToken produced non-URL-safe char %q in %s", r, tok)
			}
		}
	}
}
