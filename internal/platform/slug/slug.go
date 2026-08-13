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
func Slugify(name string) string {
	s := strings.ToLower(name)
	s = nonAlnum.ReplaceAllString(s, "-")
	s = trimHyphen.ReplaceAllString(s, "")
	if s == "" {
		s = "org"
	}
	return s
}
