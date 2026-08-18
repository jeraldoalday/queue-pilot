// Package config loads QueuePilot's runtime configuration from environment
// variables, following 12-factor conventions.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	Env         string // "development" | "production"
	Port        string
	DatabaseURL string
	// SessionCookieSecure controls the Secure flag on session cookies.
	// It is derived from Env, not set independently, so it can't be
	// misconfigured to false in production.
	SessionCookieSecure bool
	// TrustProxyHeaders says whether X-Forwarded-For may be believed when
	// identifying a client for rate limiting. It must only be true when
	// the app genuinely sits behind a proxy that overwrites that header:
	// on a directly-exposed server, believing it lets any client mint
	// unlimited identities and defeat the limit entirely. It defaults to
	// false so the unsafe setting is always a deliberate one.
	TrustProxyHeaders bool
}

func Load() (Config, error) {
	env := getenv("ENV", "development")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}

	return Config{
		Env:                 env,
		Port:                getenv("PORT", "8080"),
		DatabaseURL:         dbURL,
		SessionCookieSecure: env == "production",
		TrustProxyHeaders:   getenv("TRUST_PROXY_HEADERS", "") == "true",
	}, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
