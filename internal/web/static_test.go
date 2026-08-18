package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The templates reference these by URL. A missing one is invisible in
// development — the file is on disk either way — and only shows up in a
// deployment as an unstyled page, so it is pinned here instead.
func TestEmbeddedAssetsIncludeWhatTemplatesReference(t *testing.T) {
	want := []string{
		"css/app.css",
		"vendor/htmx.min.js",
	}

	got, err := StaticAssets()
	if err != nil {
		t.Fatalf("StaticAssets: %v", err)
	}

	have := make(map[string]bool, len(got))
	for _, p := range got {
		have[p] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("asset %q is not embedded in the binary (embedded: %v)", w, got)
		}
	}
}

// The point of embedding is that the assets do not come from the working
// directory. Serving them through the handler and getting bytes back is
// the only way to show that, since the test binary runs in the package
// directory where the old http.Dir path would not have resolved either.
func TestStaticHandlerServesEmbeddedAssets(t *testing.T) {
	h := staticHandler()

	for _, path := range []string{"/static/css/app.css", "/static/vendor/htmx.min.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s served an empty body", path)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != staticMaxAge {
			t.Errorf("GET %s Cache-Control = %q, want %q", path, cc, staticMaxAge)
		}
	}
}

// A miss must not be cached: an hour of "this file does not exist" would
// outlive the deploy that adds the file.
func TestStaticHandlerDoesNotCacheMisses(t *testing.T) {
	rec := httptest.NewRecorder()
	staticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/nope.css", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset = %d, want 404", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("Cache-Control on a 404 = %q, want it unset", cc)
	}
}

// http.FileServerFS lists directory contents by default. The static tree
// is not sensitive, but publishing an index of it is an invitation to
// probe, and nothing in the app links to one.
func TestStaticHandlerDoesNotServeDirectoryListings(t *testing.T) {
	rec := httptest.NewRecorder()
	staticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/", nil))

	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "app.css") {
		t.Error("GET /static/css/ served a directory listing")
	}
}
