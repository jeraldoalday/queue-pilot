package slug

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "Acme Dental", "acme-dental"},
		{"trailing punctuation", "Acme Dental Co.", "acme-dental-co"},
		{"extra whitespace and symbols", "  Joe's Barber Shop!! ", "joe-s-barber-shop"},
		{"already a slug", "acme-clinic", "acme-clinic"},
		{"numbers preserved", "24/7 Diner", "24-7-diner"},
		{"empty falls back", "", "org"},
		{"only symbols falls back", "!!!", "org"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.in); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
