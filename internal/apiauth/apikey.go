package apiauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// APIKeyFromRequest reads a key from X-API-Key or Authorization: Bearer <token>.
func APIKeyFromRequest(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-API-Key")); v != "" {
		return v
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return ""
}

// ValidAPIKey reports whether the request carries the expected key (constant-time when lengths match).
func ValidAPIKey(r *http.Request, expected string) bool {
	if expected == "" {
		return false
	}
	got := APIKeyFromRequest(r)
	if len(got) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}
