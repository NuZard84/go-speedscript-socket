package handlers

import (
	"net/http"
	"os"
)

// EnableCORS is a middleware that sets CORS headers. It reads the allowed origin from the
// ALLOWED_ORIGIN environment variable. If not set, it defaults to "https://www.speedscript.dev".
func EnableCORS(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		handler(w, r)
	}
}

// SecurityHeadersMiddleware sets common security headers.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// Adjust Content-Security-Policy as required.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
		next.ServeHTTP(w, r)
	})
}
