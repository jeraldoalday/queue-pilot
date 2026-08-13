package session

import (
	"crypto/rand"
	"encoding/base64"
)

// newToken returns a 256-bit random value, base64url-encoded — used for
// both session tokens and CSRF tokens. It carries no information about the
// user or org, so leaking one token type doesn't help forge the other.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
