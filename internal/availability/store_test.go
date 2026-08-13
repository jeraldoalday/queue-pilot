package availability

import "testing"

func TestHoursInputValidate(t *testing.T) {
	valid := HoursInput{Weekday: 1, StartMin: 9 * 60, EndMin: 17 * 60}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(in HoursInput) HoursInput
	}{
		{"weekday too low", func(in HoursInput) HoursInput { in.Weekday = -1; return in }},
		{"weekday too high", func(in HoursInput) HoursInput { in.Weekday = 7; return in }},
		{"end before start", func(in HoursInput) HoursInput { in.EndMin = in.StartMin - 1; return in }},
		{"end equals start", func(in HoursInput) HoursInput { in.EndMin = in.StartMin; return in }},
		{"negative start", func(in HoursInput) HoursInput { in.StartMin = -1; return in }},
		{"end past midnight", func(in HoursInput) HoursInput { in.EndMin = 24*60 + 1; return in }},
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
