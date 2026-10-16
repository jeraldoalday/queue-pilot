package emailshape

import "testing"

func TestLooksLikeEmail(t *testing.T) {
	valid := []string{"a@b.co", "someone@example.test", " padded@example.test "}
	for _, s := range valid {
		if !LooksLikeEmail(s) {
			t.Errorf("LooksLikeEmail(%q) = false, want true", s)
		}
	}

	invalid := []string{"", "nobody", "@example.test", "someone@", "two words@example.test", "a@b\nc"}
	for _, s := range invalid {
		if LooksLikeEmail(s) {
			t.Errorf("LooksLikeEmail(%q) = true, want false", s)
		}
	}
}

// A mobile keyboard's auto-space or a pasted address with a trailing
// newline is incidental whitespace, not an invalid address — LooksLikeEmail
// has to look past it the same way every other field here is trimmed
// before being judged blank or too long.
func TestLooksLikeEmailIgnoresSurroundingWhitespace(t *testing.T) {
	if !LooksLikeEmail("  jerry@acme.test\n") {
		t.Error("email with surrounding whitespace was rejected")
	}
	if LooksLikeEmail("not an email") {
		t.Error("string with internal whitespace and no @ was accepted")
	}
	if LooksLikeEmail("  ") {
		t.Error("blank string was accepted")
	}
}

// A bare carriage return in the middle of the address is exactly as
// disqualifying as the newline case above — this is the check that one of
// the two copies this package replaces used to get wrong.
func TestLooksLikeEmailRejectsEmbeddedCarriageReturn(t *testing.T) {
	if LooksLikeEmail("a@b\rc") {
		t.Error("email with an embedded carriage return was accepted")
	}
}
