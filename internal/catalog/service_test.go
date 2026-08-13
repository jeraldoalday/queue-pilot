package catalog

import "testing"

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
