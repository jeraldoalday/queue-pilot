package appointment

import (
	"strings"
	"testing"
	"time"
)

func TestBookInputValidate(t *testing.T) {
	// Real UUID shapes: validate() checks the form of these IDs, since a
	// malformed one would otherwise reach db.MustUUID and panic.
	valid := BookInput{
		ServiceID:     "0f7c9b1e-6e1a-4a5b-9d0c-2f3a4b5c6d7e",
		StaffUserID:   "5c1a7f3b-2d4e-4c6a-8b9d-1e2f3a4b5c6d",
		CustomerName:  "Dana Customer",
		CustomerEmail: "dana@example.test",
		StartsAt:      time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC),
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(in BookInput) BookInput
	}{
		{"missing service", func(in BookInput) BookInput { in.ServiceID = ""; return in }},
		{"missing staff", func(in BookInput) BookInput { in.StaffUserID = ""; return in }},
		{"blank customer name", func(in BookInput) BookInput { in.CustomerName = "   "; return in }},
		{"blank customer email", func(in BookInput) BookInput { in.CustomerEmail = ""; return in }},
		{"customer name too long", func(in BookInput) BookInput {
			in.CustomerName = strings.Repeat("a", maxCustomerNameLen+1)
			return in
		}},
		{"customer email too long", func(in BookInput) BookInput {
			in.CustomerEmail = strings.Repeat("a", maxCustomerEmailLen+1) + "@example.test"
			return in
		}},
		{"customer phone too long", func(in BookInput) BookInput {
			in.CustomerPhone = strings.Repeat("5", maxCustomerPhoneLen+1)
			return in
		}},
		{"zero start time", func(in BookInput) BookInput { in.StartsAt = time.Time{}; return in }},
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

// The service and staff IDs arrive as form fields, so a malformed one is
// ordinary bad input. Before this was validated it reached db.MustUUID and
// panicked the request instead of re-rendering the form with an error.
func TestBookInputRejectsMalformedIDs(t *testing.T) {
	valid := "0f7c9b1e-6e1a-4a5b-9d0c-2f3a4b5c6d7e"

	tests := []struct {
		name    string
		in      BookInput
		wantErr string
	}{
		{
			name:    "a malformed service ID",
			in:      BookInput{ServiceID: "notauuid", StaffUserID: valid},
			wantErr: "please choose a service",
		},
		{
			name:    "an empty service ID",
			in:      BookInput{ServiceID: "", StaffUserID: valid},
			wantErr: "please choose a service",
		},
		{
			name:    "a malformed staff ID",
			in:      BookInput{ServiceID: valid, StaffUserID: "notauuid"},
			wantErr: "please choose a staff member",
		},
		{
			name:    "an empty staff ID",
			in:      BookInput{ServiceID: valid, StaffUserID: ""},
			wantErr: "please choose a staff member",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.validate()
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("validate() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
