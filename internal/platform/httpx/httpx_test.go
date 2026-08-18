package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// serve runs PathUUID against a route with an {id} wildcard, so the test
// exercises the same path-parameter machinery the app's routes use.
func serve(t *testing.T, path string) (string, bool, int) {
	t.Helper()

	var (
		got   string
		valid bool
	)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things/{id}", func(w http.ResponseWriter, r *http.Request) {
		got, valid = PathUUID(w, r, "id")
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return got, valid, rec.Code
}

func TestPathUUIDAcceptsAValidID(t *testing.T) {
	const id = "0f7c9b1e-6e1a-4a5b-9d0c-2f3a4b5c6d7e"

	got, valid, code := serve(t, "/things/"+id)
	if !valid {
		t.Fatal("a well-formed UUID was rejected")
	}
	if got != id {
		t.Fatalf("PathUUID returned %q, want %q", got, id)
	}
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

func TestPathUUIDRejectsMalformedIDsWith404(t *testing.T) {
	// Each of these used to panic the handler on its way into
	// db.MustUUID, closing the connection with no response at all.
	malformed := []string{
		"notauuid",
		"12345678",
		"0f7c9b1e-6e1a-4a5b-9d0c",              // truncated
		"0f7c9b1e6e1a4a5b9d0c2f3a4b5c6d7e0f7c", // too long
		"zzzzzzzz-6e1a-4a5b-9d0c-2f3a4b5c6d7e", // non-hex
		"..%2F..%2Fetc%2Fpasswd",
	}

	for _, id := range malformed {
		t.Run(id, func(t *testing.T) {
			got, valid, code := serve(t, "/things/"+id)
			if valid {
				t.Fatalf("PathUUID accepted %q", id)
			}
			if got != "" {
				t.Fatalf("PathUUID returned %q alongside a rejection", got)
			}
			if code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", code)
			}
		})
	}
}
