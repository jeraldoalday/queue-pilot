package auth

import (
	"strings"
	"testing"
)

func TestCreateStaffInputValidate(t *testing.T) {
	valid := CreateStaffInput{
		Name:     "Jerry Alday",
		Email:    "jerry@acme.test",
		Password: "at-least-8-chars",
	}

	if err := valid.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(in CreateStaffInput) CreateStaffInput
	}{
		{"missing name", func(in CreateStaffInput) CreateStaffInput { in.Name = "  "; return in }},
		{"invalid email", func(in CreateStaffInput) CreateStaffInput { in.Email = "not-an-email"; return in }},
		{"short password", func(in CreateStaffInput) CreateStaffInput { in.Password = "short"; return in }},
		// Same bcrypt limit SignupInput enforces: see signup_test.go.
		{"password too long for bcrypt", func(in CreateStaffInput) CreateStaffInput {
			in.Password = strings.Repeat("a", maxPasswordLen+1)
			return in
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mutate(valid).validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if _, ok := err.(ValidationError); !ok {
				t.Errorf("expected ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestCreateStaffInputValidateAllowsAPasswordAtBcryptsLimit(t *testing.T) {
	in := CreateStaffInput{
		Name:     "Jerry Alday",
		Email:    "jerry@acme.test",
		Password: strings.Repeat("a", maxPasswordLen),
	}
	if err := in.validate(); err != nil {
		t.Fatalf("password at bcrypt's length limit was rejected: %v", err)
	}
}
