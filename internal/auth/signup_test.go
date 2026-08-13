package auth

import "testing"

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
		{"missing admin name", func(in SignupInput) SignupInput { in.AdminName = ""; return in }, true},
		{"invalid email", func(in SignupInput) SignupInput { in.AdminEmail = "not-an-email"; return in }, true},
		{"short password", func(in SignupInput) SignupInput { in.Password = "short"; return in }, true},
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
