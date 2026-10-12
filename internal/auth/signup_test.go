package auth

import (
	"strings"
	"testing"
)

func TestSignupInputValidate(t *testing.T) {
	valid := SignupInput{
		OrgName:    "Acme Dental",
		AdminName:  "Jerry Alday",
		AdminEmail: "jerry@acme.test",
		Password:   "at-least-8-chars",
	}

	if err := valid.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(in SignupInput) SignupInput
		wantErr bool
	}{
		{"missing org name", func(in SignupInput) SignupInput { in.OrgName = "  "; return in }, true},
		{"org name too long", func(in SignupInput) SignupInput {
			in.OrgName = strings.Repeat("a", maxOrgNameLen+1)
			return in
		}, true},
		{"missing admin name", func(in SignupInput) SignupInput { in.AdminName = ""; return in }, true},
		{"admin name too long", func(in SignupInput) SignupInput {
			in.AdminName = strings.Repeat("a", maxPersonNameLen+1)
			return in
		}, true},
		{"invalid email", func(in SignupInput) SignupInput { in.AdminEmail = "not-an-email"; return in }, true},
		{"email too long", func(in SignupInput) SignupInput {
			in.AdminEmail = strings.Repeat("a", maxEmailLen+1) + "@acme.test"
			return in
		}, true},
		{"short password", func(in SignupInput) SignupInput { in.Password = "short"; return in }, true},
		// bcrypt's GenerateFromPassword rejects anything past 72 bytes with
		// ErrPasswordTooLong; this has to be caught here so it comes back
		// as a ValidationError instead of reaching HashPassword and
		// surfacing as an unexplained internal error.
		{"password too long for bcrypt", func(in SignupInput) SignupInput {
			in.Password = strings.Repeat("a", maxPasswordLen+1)
			return in
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.mutate(valid)
			err := in.validate()
			if tt.wantErr && err == nil {
				t.Error("expected validation error, got nil")
			}
			if tt.wantErr {
				if _, ok := err.(ValidationError); !ok {
					t.Errorf("expected ValidationError, got %T: %v", err, err)
				}
			}
		})
	}
}

// A mobile keyboard's auto-space or a pasted address with a trailing
// newline is incidental whitespace, not an invalid address — looksLikeEmail
// has to look past it the same way every other field here is trimmed
// before being judged blank or too long.
func TestLooksLikeEmailIgnoresSurroundingWhitespace(t *testing.T) {
	if !looksLikeEmail("  jerry@acme.test\n") {
		t.Error("email with surrounding whitespace was rejected")
	}
	if looksLikeEmail("not an email") {
		t.Error("string with internal whitespace and no @ was accepted")
	}
	if looksLikeEmail("  ") {
		t.Error("blank string was accepted")
	}
}

func TestSignupInputValidateAllowsAPasswordAtBcryptsLimit(t *testing.T) {
	in := SignupInput{
		OrgName:    "Acme Dental",
		AdminName:  "Jerry Alday",
		AdminEmail: "jerry@acme.test",
		Password:   strings.Repeat("a", maxPasswordLen),
	}
	if err := in.validate(); err != nil {
		t.Fatalf("password at bcrypt's length limit was rejected: %v", err)
	}
}
