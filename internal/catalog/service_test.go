package catalog

import (
	"math"
	"testing"
)

func TestServiceInputValidate(t *testing.T) {
	valid := ServiceInput{Name: "Haircut", Description: "A basic cut", DurationMinutes: 30}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(in ServiceInput) ServiceInput
	}{
		{"missing name", func(in ServiceInput) ServiceInput { in.Name = "  "; return in }},
		{"zero duration", func(in ServiceInput) ServiceInput { in.DurationMinutes = 0; return in }},
		{"negative duration", func(in ServiceInput) ServiceInput { in.DurationMinutes = -5; return in }},
		// Too large to fit the int32 column CreateService/UpdateService
		// narrow it into; letting this through would wrap on the cast
		// instead of failing cleanly here.
		{"duration too large for the column", func(in ServiceInput) ServiceInput {
			in.DurationMinutes = math.MaxInt32 + 1
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

func TestServiceInputValidateAllowsTheLargestDurationTheColumnHolds(t *testing.T) {
	in := ServiceInput{Name: "Retreat", DurationMinutes: math.MaxInt32}
	if err := in.validate(); err != nil {
		t.Fatalf("duration at the int32 boundary was rejected: %v", err)
	}
}
