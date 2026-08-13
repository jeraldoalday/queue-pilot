// Package org handles organization (tenant) creation and lookup.
package org

import (
	"regexp"
	"strings"
)

var (
	nonAlnum   = regexp.MustCompile(`[^a-z0-9]+`)
	trimHyphen = regexp.MustCompile(`^-+|-+$`)
)

// Slugify turns an org name into a URL-safe slug, e.g. "Acme Dental Co."
// -> "acme-dental-co". It does not guarantee uniqueness — callers append a
// disambiguating suffix on collision (see auth.Signup).
func Slugify(name string) string {
	s := strings.ToLower(name)
	s = nonAlnum.ReplaceAllString(s, "-")
	s = trimHyphen.ReplaceAllString(s, "")
	if s == "" {
		s = "org"
	}
	return s
}
