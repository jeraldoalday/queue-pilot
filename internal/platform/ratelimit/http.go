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

// Middleware rejects requests over the limit with 429 and a Retry-After
// header, and lets everything else through untouched.
func Middleware(l *Limiter, trustProxyHeaders bool, log *slog.Logger) func(http.Handler) http.Handler {
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
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			log.Warn("rate limit exceeded", "path", r.URL.Path, "retry_after_seconds", seconds)
			http.Error(w, "Too many requests — please wait a moment and try again.", http.StatusTooManyRequests)
		})
	}
}
