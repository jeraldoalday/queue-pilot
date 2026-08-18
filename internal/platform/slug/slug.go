// Package slug turns free-text names into URL-safe slugs, shared by every
// domain that needs one (organizations, services, queues).
package slug

import (
	"regexp"
	"strings"
)

var (
	nonAlnum   = regexp.MustCompile(`[^a-z0-9]+`)
	trimHyphen = regexp.MustCompile(`^-+|-+$`)
)

// Slugify turns a name into a URL-safe slug, e.g. "Acme Dental Co." ->
// "acme-dental-co". It does not guarantee uniqueness — callers append a
// disambiguating suffix on collision (see auth.uniqueOrgSlug).
//
// A name with nothing sluggable in it — "!!!", or a name written entirely
// in a script this ASCII-only transform drops — slugifies to the empty
// string. That is reported honestly rather than papered over, because
// what the right substitute is depends on what is being named: see
// SlugifyOr.
func Slugify(name string) string {
	s := strings.ToLower(name)
	s = nonAlnum.ReplaceAllString(s, "-")
	return trimHyphen.ReplaceAllString(s, "")
}

// SlugifyOr is Slugify with a caller-supplied substitute for names that
// slugify to nothing.
//
// The fallback belongs to the caller because the slug is user-visible:
// every domain used to share one hard-coded "org", so a queue named in
// Japanese was published at /q/acme/org and a service at
// /app/services/org — a URL that names the wrong kind of thing and, worse,
// collides with the next unsluggable name in the same org, pushing it to
// "org-2". Passing "queue" or "service" keeps the disambiguation series
// readable and scoped to what it actually names.
func SlugifyOr(name, fallback string) string {
	if s := Slugify(name); s != "" {
		return s
	}
	return fallback
}
