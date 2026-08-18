package queue

import (
	"strings"
	"testing"
)

func TestTicketInputValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      TicketInput
		wantErr string
	}{
		{
			name: "accepts a name alone",
			in:   TicketInput{CustomerName: "Ada"},
		},
		{
			name: "accepts a name and phone",
			in:   TicketInput{CustomerName: "Ada", CustomerPhone: "+63 917 000 0000"},
		},
		{
			name:    "rejects a missing name",
			in:      TicketInput{},
			wantErr: "please enter a name",
		},
		{
			// The kiosk is unauthenticated, so the name field is the one
			// place a passer-by can put arbitrary text on a shared screen.
			name:    "rejects a whitespace-only name",
			in:      TicketInput{CustomerName: "  "},
			wantErr: "please enter a name",
		},
		{
			name:    "rejects an over-long name",
			in:      TicketInput{CustomerName: strings.Repeat("a", maxCustomerNameLen+1)},
			wantErr: "name must be 60 characters or fewer",
		},
		{
			name:    "rejects an over-long phone number",
			in:      TicketInput{CustomerName: "Ada", CustomerPhone: strings.Repeat("9", maxPhoneLen+1)},
			wantErr: "phone number must be 32 characters or fewer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("validate() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
