package appointment

import (
	"testing"
	"time"
)

func TestBookInputValidate(t *testing.T) {
	valid := BookInput{
		ServiceID:     "svc-1",
		StaffUserID:   "staff-1",
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
