// Package httpx holds small HTTP helpers shared by the handler packages.
package httpx

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// PathUUID reads a path parameter that must be a UUID, writing a 404 and
// reporting false when it isn't.
//
// Every domain lookup in QueuePilot converts an ID with db.MustUUID, which
// panics on a malformed value — correct for IDs the app generates itself,
// and wrong for anything out of a URL. Without this check a request to
// /app/services/notauuid panicked the handler. Validating at the boundary
// keeps db.MustUUID's panic meaning what it should: a programmer error,
// never a visitor typing in the address bar.
//
// A malformed ID answers 404 rather than 400 because it identifies no
// resource, and because the app already answers "an ID that isn't yours"
// with 404 — matching them means a probe cannot tell a malformed ID from
// another tenant's real one.
func PathUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	value := r.PathValue(name)
	if _, err := uuid.Parse(value); err != nil {
		http.NotFound(w, r)
		return "", false
	}
	return value, true
}

// AbsoluteURL turns an app-relative path into a full URL for the request
// that asked for it.
//
// Nearly every link QueuePilot writes is relative, which is correct: the
// app does not know its own public address, and hardcoding one is what
// breaks the day a business puts it on their own domain. A handful of
// things leave the browser, though — a calendar file read by a phone, a
// link inside a downloaded document — and a relative path in one of those
// resolves to nothing at all.
//
// The scheme is taken from the connection, or from X-Forwarded-Proto when
// a proxy terminated TLS. The host is the one the client asked for, which
// is a header the client controls; that is acceptable here precisely
// because the result is only ever handed back to the same client, in a
// file they requested. A spoofed Host poisons nobody's link but their own.
func AbsoluteURL(r *http.Request, path string) string {
	host := r.Host
	if host == "" {
		// Nothing sensible to build from. A relative path is still a
		// usable link inside a browser, which is better than a URL with
		// an empty authority that is a usable link nowhere.
		return path
	}
	if path != "" && path[0] != '/' {
		path = "/" + path
	}
	return scheme(r) + "://" + host + path
}

// scheme reports whether this request arrived over http or https, looking
// through a terminating proxy when there is one.
func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	// A proxy appends to the right, so the left-most entry is the scheme
	// the client actually used.
	proto := r.Header.Get("X-Forwarded-Proto")
	if comma := strings.Index(proto, ","); comma >= 0 {
		proto = proto[:comma]
	}
	switch strings.ToLower(strings.TrimSpace(proto)) {
	case "https":
		return "https"
	case "http":
		return "http"
	default:
		return "http"
	}
}
