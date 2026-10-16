// Package emailshape holds the one deliberately loose email-shape check
// shared by every form that collects a customer, admin, or staff email —
// enough to catch a typo'd or empty field, not an attempt to validate
// deliverability, which only sending mail can do.
//
// auth and appointment each carried their own copy of this before, and the
// copies had quietly drifted apart: auth's version let a carriage return
// through as long as it wasn't a space, tab, or newline, while appointment's
// rejected it. One address with an embedded CR would therefore pass signup
// but fail booking — the exact kind of inconsistency a single shared
// implementation exists to rule out.
package emailshape

import "strings"

// LooksLikeEmail reports whether s has the rough shape of an email address:
// an '@' with at least one character on each side, no leading/trailing
// space (ignoring surrounding whitespace, which is trimmed first), and no
// whitespace in the middle of what's left.
func LooksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t\r\n")
}
