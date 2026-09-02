package web

import (
	"net/http"
	"strings"
)

// Search engines and QueuePilot's URL space.
//
// Two kinds of page live under the public prefixes, and they want
// opposite things. A business's booking page and its queue board are
// meant to be found — a shop that puts "book with us" on its website
// wants that link to rank. A customer's own ticket or booking is the
// opposite: the URL *is* the credential, so anything that copies it
// somewhere else is a leak, and a crawler is a machine for copying URLs
// somewhere else.
//
// The rule is therefore closed by default. A path is indexable only if it
// is one of the few that a business would want found; everything else is
// marked noindex, including anything added later that nobody thought
// about. Getting that default the other way round means the mistake is
// silent and public.

// robotsTag is what a private page sends. nofollow as well as noindex,
// because the confirmation page links to its own cancel action and the
// board links to tickets — following those is how a crawler ends up
// requesting URLs that do something.
const robotsTag = "noindex, nofollow"

// robotsTagFor reports the X-Robots-Tag for a path, or "" when the page is
// one that should be indexed.
func robotsTagFor(path string) string {
	if isIndexablePath(path) {
		return ""
	}
	return robotsTag
}

// isIndexablePath reports whether a path is a page a business would want
// found by search.
//
// The shapes are spelled out rather than pattern-matched loosely, because
// the failure mode of a slightly-too-generous rule here is a customer's
// booking in somebody's search results.
func isIndexablePath(path string) bool {
	// The marketing front page.
	if path == "/" {
		return true
	}

	segments := splitPath(path)
	switch {
	// /book/{org} and /book/{org}/{service}: what a business links to.
	// /book/{org}/a/{id} is a customer's own booking and is three
	// segments too, so the "a" is checked rather than the length alone.
	case len(segments) >= 2 && segments[0] == "book":
		return len(segments) == 2 || (len(segments) == 3 && segments[2] != "a")

	// /q/{org}/{queue} and its waiting-room display. /q/{org}/{queue}/t/…
	// is a customer's own ticket, and the board partial is a fragment
	// with no page around it, so neither is offered.
	case len(segments) >= 3 && segments[0] == "q":
		return len(segments) == 3 || (len(segments) == 4 && segments[3] == "display")
	}

	return false
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// robotsTxt is the crawler-facing half of the same policy.
//
// The header above is what actually enforces it — a crawler that ignores
// this file still sees the header on the response, and a page already in
// an index is dropped by the header rather than by a rule that only says
// "do not fetch". This file is the polite version: it stops the request
// being made at all, which is the difference between a crawler learning a
// ticket URL exists and a crawler merely being told not to keep it.
//
// The sitemap line is deliberately absent. Which booking pages exist is a
// list of who uses the product, and publishing it at a well-known address
// hands that list to anyone who asks.
const robotsTxt = `User-agent: *
# A customer's own ticket, booking and confirmation. The URL is the
# credential — anyone holding it can read the page and cancel from it.
Disallow: /q/*/t/
Disallow: /book/*/a/
# The staff application. Nothing here is reachable without a session, and
# a crawler asking for it only generates 302s to the sign-in page.
Disallow: /app
Disallow: /login
Disallow: /signup
# The JSON API answers the same things the pages do, in a form no reader
# wants in a search result.
Disallow: /api/
`

// handleRobots serves robots.txt.
//
// It is a handler rather than a file under /static so that the policy
// lives next to the header that enforces it: two copies of this rule in
// two places is how a new private path ends up covered by one and not the
// other.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// A day: long enough that crawlers are not re-fetching it constantly,
	// short enough that a change to the policy takes effect the same week.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := w.Write([]byte(robotsTxt)); err != nil {
		s.log.Warn("handleRobots: write", "error", err)
	}
}
