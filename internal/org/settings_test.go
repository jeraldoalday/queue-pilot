package org

import (
	"strings"
	"testing"
	"time"
)

func TestSettingsValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      Settings
		wantErr bool
		reason  string
	}{
		{"a plain name and zone", Settings{Name: "Acme Dental", Timezone: "Europe/London"}, false, ""},
		{"UTC is a zone", Settings{Name: "Acme", Timezone: "UTC"}, false, ""},
		{"surrounding space is trimmed", Settings{Name: "  Acme  ", Timezone: "  Asia/Manila  "}, false, ""},
		{"an empty name", Settings{Name: "", Timezone: "UTC"}, true, "the name appears on the public board"},
		{"a whitespace-only name", Settings{Name: "   ", Timezone: "UTC"}, true, "trimming leaves nothing"},
		{"an overlong name", Settings{Name: strings.Repeat("a", maxNameLen+1), Timezone: "UTC"}, true, "it would break the board layout"},
		{"a name at the limit", Settings{Name: strings.Repeat("a", maxNameLen), Timezone: "UTC"}, false, ""},
		{"an empty zone", Settings{Name: "Acme", Timezone: ""}, true, "every service day derives from it"},
		{"a made-up zone", Settings{Name: "Acme", Timezone: "Mars/Olympus"}, true, "the runtime cannot load it"},
		{"an offset is not a zone name", Settings{Name: "Acme", Timezone: "+05:30"}, true, "an offset has no DST rules"},
		{"a zone abbreviation is not a zone", Settings{Name: "Acme", Timezone: "PST"}, true, "abbreviations are ambiguous"},
		{"Local means the server, not the business", Settings{Name: "Acme", Timezone: "Local"}, true, "it changes if the app is redeployed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.validate()
			if tt.wantErr && err == nil {
				t.Fatalf("validate() accepted %+v, want it rejected: %s", tt.in, tt.reason)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validate() rejected %+v: %v", tt.in, err)
			}
			if err != nil && !IsValidationError(err) {
				t.Errorf("validate() returned %T, want a ValidationError the page can show", err)
			}
		})
	}
}

// A rejected zone must say what a right answer looks like. "Invalid
// timezone" leaves someone guessing between "London", "GMT" and "+00:00",
// all of which are wrong here.
func TestTimezoneErrorNamesAnExample(t *testing.T) {
	err := Settings{Name: "Acme", Timezone: "Nowhere/Nothing"}.validate()
	if err == nil {
		t.Fatal("a made-up zone was accepted")
	}
	if !strings.Contains(err.Error(), "/") {
		t.Errorf("the error %q does not show what a zone name looks like", err)
	}
}

// The suggestions must all be loadable, or the form would offer a zone
// and then reject it on submit.
func TestCommonTimezonesAreAllLoadable(t *testing.T) {
	zones := CommonTimezones()
	if len(zones) < 10 {
		t.Fatalf("only %d suggested zones, want a usable shortlist", len(zones))
	}

	for _, tz := range zones {
		if _, err := time.LoadLocation(tz); err != nil {
			t.Errorf("suggested zone %q does not load: %v", tz, err)
		}
		if err := (Settings{Name: "Acme", Timezone: tz}).validate(); err != nil {
			t.Errorf("suggested zone %q is rejected by validation: %v", tz, err)
		}
	}
}

// Sorted, so the list reads as a list rather than as the order someone
// happened to type it in.
func TestCommonTimezonesAreSortedAndUnique(t *testing.T) {
	zones := CommonTimezones()

	for i := 1; i < len(zones); i++ {
		if zones[i-1] > zones[i] {
			t.Fatalf("zones are not sorted: %q comes before %q", zones[i-1], zones[i])
		}
		if zones[i-1] == zones[i] {
			t.Fatalf("zone %q appears twice", zones[i])
		}
	}
}

func TestCurrentOffset(t *testing.T) {
	// A fixed instant, in January, so the zones below are all on standard
	// time and the expected offsets do not drift with the seasons.
	winter := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		tz   string
		want string
	}{
		{"UTC", "+00:00"},
		{"Asia/Kolkata", "+05:30"},   // a half-hour offset
		{"Asia/Kathmandu", "+05:45"}, // a quarter-hour offset
		{"America/New_York", "-05:00"},
		{"Asia/Tokyo", "+09:00"},
	}

	for _, tt := range tests {
		t.Run(tt.tz, func(t *testing.T) {
			got, ok := CurrentOffset(tt.tz, winter)
			if !ok {
				t.Fatalf("CurrentOffset(%q) reported the zone as unknown", tt.tz)
			}
			if got != tt.want {
				t.Errorf("CurrentOffset(%q) = %q, want %q", tt.tz, got, tt.want)
			}
		})
	}
}

// The offset shown is the one in force now, not the standard one, so a
// business on summer time reads the hour it is actually keeping.
func TestCurrentOffsetFollowsDaylightSaving(t *testing.T) {
	winter := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	summer := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)

	w, _ := CurrentOffset("Europe/London", winter)
	s, _ := CurrentOffset("Europe/London", summer)

	if w != "+00:00" {
		t.Errorf("London in January = %q, want +00:00", w)
	}
	if s != "+01:00" {
		t.Errorf("London in July = %q, want +01:00", s)
	}
}

func TestCurrentOffsetRejectsAnUnknownZone(t *testing.T) {
	if _, ok := CurrentOffset("Mars/Olympus", time.Now()); ok {
		t.Error("CurrentOffset accepted a zone that does not exist")
	}
}
