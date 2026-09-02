package ratelimit

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
)

// ClientKey derives the limiter key for a request: the client's IP.
//
// trustProxyHeaders must be false unless the app genuinely sits behind a
// proxy that overwrites X-Forwarded-For. A header a client can set is a
// header a client can vary, so trusting it on a directly-exposed server
// hands every visitor an unlimited supply of distinct keys — which is to
// say, no rate limit at all. That is why this is a deployment setting
// rather than a default.
func ClientKey(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		// The left-most entry is the original client; the proxy appends
		// itself to the right.
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if host := firstForwarded(fwd); host != "" {
				return host
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without a port shouldn't happen from net/http, but a
		// key is still needed and the raw value is better than an empty
		// one shared by every such request.
		return r.RemoteAddr
	}
	return host
}

func firstForwarded(header string) string {
	for i := 0; i < len(header); i++ {
		if header[i] == ',' {
			return trimSpace(header[:i])
		}
	}
	return trimSpace(header)
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// Deny writes the answer to a request that is over the limit. It is given
// the wait in whole seconds, already floored at one, because that is the
// number the response has to state and computing it twice is how the
// header and the body come to disagree.
//
// It exists because "too many requests" has to be said in the language the
// caller is speaking. A form post gets a page; the JSON API has a single
// error envelope every one of its failures takes, and a 429 that arrives
// as plain text is a failure that envelope does not cover — which pushes
// the front end into guessing from a status code what every other error
// tells it outright.
type Deny func(w http.ResponseWriter, r *http.Request, retryAfterSeconds int)

// Middleware rejects requests over the limit with 429 and a Retry-After
// header, and lets everything else through untouched. The refusal is an
// HTML-friendly plain-text message, which is what the public forms want.
func Middleware(l *Limiter, trustProxyHeaders bool, log *slog.Logger) func(http.Handler) http.Handler {
	return MiddlewareWith(l, trustProxyHeaders, log, denyPlain)
}

// MiddlewareWith is Middleware with the refusal written by the caller.
//
// The limiter is passed in rather than created here so that two
// middlewares can share one: the same action reached through a form and
// through the API must draw on the same allowance, or a script that
// alternates between them gets twice the rate and the limit means half
// what it says.
func MiddlewareWith(l *Limiter, trustProxyHeaders bool, log *slog.Logger, deny Deny) func(http.Handler) http.Handler {
	if deny == nil {
		deny = denyPlain
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := ClientKey(r, trustProxyHeaders)
			if l.Allow(key) {
				next.ServeHTTP(w, r)
				return
			}

			retry := l.Retry(key)
			seconds := int(retry.Seconds())
			if seconds < 1 {
				// Never zero: "Retry-After: 0" invites an immediate
				// retry, which is the one thing a client at the limit
				// should not do.
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			log.Warn("rate limit exceeded", "path", r.URL.Path, "retry_after_seconds", seconds)
			deny(w, r, seconds)
		})
	}
}

// denyPlain is the default refusal: a sentence a browser will render.
func denyPlain(w http.ResponseWriter, _ *http.Request, _ int) {
	http.Error(w, "Too many requests — please wait a moment and try again.", http.StatusTooManyRequests)
}
