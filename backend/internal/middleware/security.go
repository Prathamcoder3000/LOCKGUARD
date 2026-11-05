package middleware

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// SecurityHeaders applies production HTTP security headers to all responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent MIME-sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking / frame embedding
		w.Header().Set("X-Frame-Options", "DENY")

		// Cross-Site Scripting protection
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// HTTP Strict Transport Security (HSTS)
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Content Security Policy
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")

		// Referrer policy
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		next.ServeHTTP(w, r)
	})
}

// MaxBodySize restricts the maximum bytes allowed in a request body to prevent unbounded memory allocation DOS.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // Default 1 MB
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error": map[string]interface{}{
						"code":    "ERR_PAYLOAD_TOO_LARGE",
						"message": "request body exceeds maximum allowed size (1MB)",
					},
				})
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ValidateContentType verifies that mutating HTTP requests (POST, PUT, PATCH) provide application/json if Content-Type is specified.
func ValidateContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			ct := r.Header.Get("Content-Type")
			// If Content-Type is explicitly provided and does not contain application/json, reject with 415
			if ct != "" && !strings.Contains(strings.ToLower(ct), "application/json") {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnsupportedMediaType)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error": map[string]interface{}{
						"code":    "ERR_UNSUPPORTED_MEDIA_TYPE",
						"message": "Content-Type must be application/json",
					},
					"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
