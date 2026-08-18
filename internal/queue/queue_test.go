package queue

import (
	"strings"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/platform/slug"
)

func TestInputValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      Input
		wantErr string
	}{
		{
			name: "accepts a plain name",
			in:   Input{Name: "Walk-ins"},
		},
		{
			name: "accepts a name at the length limit",
			in:   Input{Name: strings.Repeat("a", maxNameLen)},
		},
		{
			name:    "rejects an empty name",
			in:      Input{Name: ""},
			wantErr: "queue name is required",
		},
		{
			// A name of only spaces is empty as far as a customer reading
			// the board is concerned, so it must fail the same way "" does
			// rather than creating an invisible queue.
			name:    "rejects a whitespace-only name",
			in:      Input{Name: "   \t\n "},
			wantErr: "queue name is required",
		},
		{
			name:    "rejects a name past the length limit",
			in:      Input{Name: strings.Repeat("a", maxNameLen+1)},
			wantErr: "queue name must be 80 characters or fewer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() = nil, want %q", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("validate() = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// A queue's slug ends up in a public URL customers type or scan, so the
// names businesses actually use must produce something usable. This pins
// the shapes that matter rather than re-testing Slugify's internals.
func TestQueueSlugsFromRealisticNames(t *testing.T) {
	tests := []struct{ name, want string }{
		{"Walk-ins", "walk-ins"},
		{"Front Desk", "front-desk"},
		{"Pharmacy — Counter 2", "pharmacy-counter-2"},
		{"  Deli  ", "deli"},
		// A name with nothing sluggable in it falls back to the domain's
		// own word, so the public URL reads /q/acme/queue rather than
		// naming some other kind of thing.
		{"!!!", "queue"},
		{"待ち行列", "queue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slug.SlugifyOr(tt.name, "queue"); got != tt.want {
				t.Fatalf("SlugifyOr(%q, \"queue\") = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
