package config

import "testing"

// setEnv clears every variable Load reads, then applies overrides, so each
// test starts from "nothing set" rather than whatever a previous test (or
// the process's own environment) happened to leave behind.
func setEnv(t *testing.T, overrides map[string]string) {
	for _, key := range []string{"ENV", "PORT", "DATABASE_URL", "TRUST_PROXY_HEADERS", "METRICS_TOKEN"} {
		t.Setenv(key, "")
	}
	for key, value := range overrides {
		t.Setenv(key, value)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	setEnv(t, nil)

	_, err := Load()

	if err == nil {
		t.Fatal("Load() with no DATABASE_URL set: want an error, got nil")
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/queuepilot"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want no error", err)
	}

	if cfg.Env != "development" {
		t.Errorf("Env = %q, want %q", cfg.Env, "development")
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
	if cfg.SessionCookieSecure {
		t.Error("SessionCookieSecure = true in development, want false")
	}
	if cfg.TrustProxyHeaders {
		t.Error("TrustProxyHeaders = true by default, want false")
	}
	if cfg.MetricsToken != "" {
		t.Errorf("MetricsToken = %q, want empty", cfg.MetricsToken)
	}
}

func TestLoadCarriesDatabaseURLAndPortThrough(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://localhost/queuepilot",
		"PORT":         "9090",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want no error", err)
	}

	if cfg.DatabaseURL != "postgres://localhost/queuepilot" {
		t.Errorf("DatabaseURL = %q, want the configured value", cfg.DatabaseURL)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9090")
	}
}

// SessionCookieSecure is derived from Env rather than its own variable, on
// purpose: a deployment cannot leave production cookies non-Secure by
// forgetting to set an unrelated flag.
func TestSessionCookieSecureFollowsEnv(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"development", false},
		{"production", true},
		{"staging", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			setEnv(t, map[string]string{
				"DATABASE_URL": "postgres://localhost/queuepilot",
				"ENV":          tt.env,
			})

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() = %v, want no error", err)
			}
			if cfg.SessionCookieSecure != tt.want {
				t.Errorf("SessionCookieSecure for ENV=%q = %v, want %v", tt.env, cfg.SessionCookieSecure, tt.want)
			}
		})
	}
}

// TrustProxyHeaders is a deliberate opt-in: anything other than the exact
// string "true" must leave it off, since believing a spoofable header by
// accident defeats the rate limiter it gates.
func TestTrustProxyHeadersRequiresExactTrue(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"", false},
		{"1", false},
		{"yes", false},
		{"True", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			setEnv(t, map[string]string{
				"DATABASE_URL":        "postgres://localhost/queuepilot",
				"TRUST_PROXY_HEADERS": tt.value,
			})

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() = %v, want no error", err)
			}
			if cfg.TrustProxyHeaders != tt.want {
				t.Errorf("TrustProxyHeaders for TRUST_PROXY_HEADERS=%q = %v, want %v", tt.value, cfg.TrustProxyHeaders, tt.want)
			}
		})
	}
}

func TestMetricsTokenPassesThrough(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":  "postgres://localhost/queuepilot",
		"METRICS_TOKEN": "secret-token",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want no error", err)
	}
	if cfg.MetricsToken != "secret-token" {
		t.Errorf("MetricsToken = %q, want %q", cfg.MetricsToken, "secret-token")
	}
}
