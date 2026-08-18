// Package httpx holds small HTTP helpers shared by the handler packages.
package httpx

import (
	"net/http"

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
