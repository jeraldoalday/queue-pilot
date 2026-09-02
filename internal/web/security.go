package web

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy is what the pages are actually built from, stated
// as a policy rather than left implicit.
//
// Each directive earns its place:
//
//	default-src 'self'   nothing loads from anywhere but this origin.
//	script-src 'self'    the only script is the vendored htmx under
//	                     /static, which is why no 'unsafe-inline' or
//	                     'unsafe-eval' is needed — the templates carry no
//	                     inline handlers and htmx is used without its
//	                     scripting features.
//	style-src 'self' 'unsafe-inline'
//	                     the report's histogram sizes each bar with a style
//	                     attribute, because a bar's width is data and a
//	                     Tailwind class computed at runtime is one the CSS
//	                     build never saw. That single attribute is what
//	                     'unsafe-inline' buys here.
//	img-src 'self' data: no remote images; data: keeps a future inline SVG
//	                     or favicon working without another exception.
//	object-src 'none'    there are no plugins to embed, and <object> is a
//	                     way to smuggle one in.
//	base-uri 'self'      an injected <base> would silently repoint every
//	                     relative URL on the page, including the form
//	                     actions that move tickets.
//	form-action 'self'   a form on these pages posts to this app or to
//	                     nothing.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// hstsValue asks for a year of HTTPS-only, including subdomains. It is
// only ever sent over TLS: promising it on a plain-HTTP development server
// would pin localhost to HTTPS in the developer's browser, which is
// tedious to undo and has bitten enough people to be worth avoiding.
const hstsValue = "max-age=31536000; includeSubDomains"

// SecurityHeaders sets the response headers that constrain what a page is
// allowed to do.
//
// The app renders user-supplied text — customer names on a board, an
// organisation name on every public page — and templ escapes all of it.
// These headers are the second line: if an escaping bug ever lets a tag
// through, the policy is what decides whether it can do anything.
//
// secure says whether the app is served over HTTPS, which gates HSTS.
func SecurityHeaders(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff")
			// Referrer-Policy is same-origin rather than the usual
			// strict-origin-when-cross-origin because a ticket URL is a
			// capability: anyone holding it can read that customer's page
			// and cancel their number. Sending even the origin to a third
			// party is more than this app needs to send.
			h.Set("Referrer-Policy", "same-origin")
			// Nothing here uses a camera, a microphone or a location, and
			// a page that never asks should not be able to start.
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
			h.Set("X-Frame-Options", frameOptionsFor(r))
			// A customer's ticket and booking URLs are capabilities:
			// holding one is permission to read that page and cancel from
			// it. A crawler is a machine for copying URLs somewhere else,
			// so every page but the handful a business wants found is
			// marked not to be indexed. See robots.go for the rule.
			if tag := robotsTagFor(r.URL.Path); tag != "" {
				h.Set("X-Robots-Tag", tag)
			}

			if secure {
				h.Set("Strict-Transport-Security", hstsValue)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// frameOptionsFor decides whether a page may be embedded.
//
// Everything is DENY except the waiting-room display. That page is meant
// to be left running on a screen in a room, and signage systems commonly
// compose several sources in frames; refusing would make the app harder to
// use for exactly the purpose the page exists for. It is also the one page
// with nothing to clickjack — it shows numbers, never names, and offers no
// control at all — so the risk the header exists to prevent does not apply
// to it.
func frameOptionsFor(r *http.Request) string {
	if isDisplayPath(r.URL.Path) {
		return "SAMEORIGIN"
	}
	return "DENY"
}

// isDisplayPath reports whether a path is the waiting-room screen or the
// partial it polls. Both live under /q/, which is the unauthenticated
// half of the URL space — no authenticated page is ever embeddable.
func isDisplayPath(path string) bool {
	if !strings.HasPrefix(path, "/q/") {
		return false
	}
	return strings.HasSuffix(path, "/display") || strings.HasSuffix(path, "/display/board")
}
