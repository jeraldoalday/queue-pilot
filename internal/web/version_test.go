package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The endpoint must answer with something well-formed even under `go
// test`, which builds outside a repository and so has no VCS stamps at
// all. A blank field there reads as a broken endpoint; "unknown" reads as
// the fact it is.
func TestBuildInfoIsAlwaysPopulated(t *testing.T) {
	b := Build()

	if b.Go == "" {
		t.Error("Go version is empty")
	}
	for name, value := range map[string]string{
		"Version":  b.Version,
		"Revision": b.Revision,
		"Time":     b.Time,
	} {
		if value == "" {
			t.Errorf("%s is empty, want a value or \"unknown\"", name)
		}
	}
}

// Cached because it cannot change while the process runs.
func TestBuildIsStable(t *testing.T) {
	if Build() != Build() {
		t.Error("Build() returned different values on consecutive calls")
	}
}

func TestBuildInfoShort(t *testing.T) {
	tests := []struct {
		name string
		in   BuildInfo
		want string
	}{
		{
			"a full hash is abbreviated",
			BuildInfo{Revision: "0123456789abcdef0123456789abcdef01234567", Go: "go1.26"},
			"0123456789ab (go1.26)",
		},
		{
			"a short revision is left alone",
			BuildInfo{Revision: "unknown", Go: "go1.26"},
			"unknown (go1.26)",
		},
		{
			// A binary built from a dirty tree corresponds to no commit,
			// and saying so is the difference between a revision that
			// identifies the code and one that merely resembles it.
			"a dirty tree is called out",
			BuildInfo{Revision: "0123456789abcdef", Modified: true, Go: "go1.26"},
			"0123456789ab-dirty (go1.26)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Short(); got != tt.want {
				t.Errorf("Short() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionEndpointServesJSON(t *testing.T) {
	s := &Server{log: discardSlog()}

	rec := httptest.NewRecorder()
	s.handleVersion(rec, httptest.NewRequest(http.MethodGet, "/version", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	// Asked precisely to find out whether a deploy landed, so a cached
	// answer would defeat the point.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	var got BuildInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the response is not valid JSON: %v", err)
	}
	if got.Go == "" || got.Revision == "" {
		t.Errorf("the response is missing fields: %+v", got)
	}
}

// Nothing beyond identity belongs in the response: it is unauthenticated,
// so a host name, an environment or a database URL leaking in later would
// be published to anyone who asks.
func TestVersionEndpointExposesOnlyBuildIdentity(t *testing.T) {
	s := &Server{log: discardSlog()}

	rec := httptest.NewRecorder()
	s.handleVersion(rec, httptest.NewRequest(http.MethodGet, "/version", nil))

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("the response is not valid JSON: %v", err)
	}

	allowed := map[string]bool{"version": true, "revision": true, "time": true, "modified": true, "go": true}
	for key := range raw {
		if !allowed[key] {
			t.Errorf("the version response carries an unexpected field %q", key)
		}
	}
}

// discardSlog is a logger for handler tests that have nothing to assert
// about logging.
func discardSlog() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}
