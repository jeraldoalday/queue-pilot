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
		{"uppercase folded", "ACME", "acme"},
		{"internal runs collapse", "Acme   ---   Dental", "acme-dental"},
		{"empty yields empty", "", ""},
		{"only symbols yields empty", "!!!", ""},
		{"non-latin script yields empty", "待ち行列", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.in); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSlugifyOr(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		fallback string
		want     string
	}{
		{"sluggable name ignores fallback", "Walk-in Line", "queue", "walk-in-line"},
		{"unsluggable name takes fallback", "待ち行列", "queue", "queue"},
		{"empty name takes fallback", "", "service", "service"},
		{"symbols take fallback", "***", "org", "org"},
		{"fallback is per-domain", "!!!", "service", "service"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SlugifyOr(tt.in, tt.fallback); got != tt.want {
				t.Errorf("SlugifyOr(%q, %q) = %q, want %q", tt.in, tt.fallback, got, tt.want)
			}
		})
	}
}

// A slug is only useful if it survives a round trip through a URL path
// segment unchanged, so nothing Slugify emits may need escaping.
func TestSlugifyOutputIsURLSafe(t *testing.T) {
	names := []string{
		"Acme Dental Co.", "  Joe's Barber Shop!! ", "24/7 Diner",
		"Ärzte & Söhne", "front—desk", "a/b?c#d",
	}

	for _, name := range names {
		got := SlugifyOr(name, "org")
		for _, r := range got {
			isLower := r >= 'a' && r <= 'z'
			isDigit := r >= '0' && r <= '9'
			if !isLower && !isDigit && r != '-' {
				t.Errorf("SlugifyOr(%q) = %q, contains disallowed rune %q", name, got, r)
			}
		}
	}
}
