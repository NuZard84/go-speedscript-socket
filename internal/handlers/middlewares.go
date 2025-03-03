package handlers

import (
	"net/http"
	"os"
	"strings"
)

// EnableCORS is a middleware that sets CORS headers. It reads the allowed origin from the
// ALLOWED_ORIGIN environment variable. If not set, it defaults to "https://www.speedscript.dev".
func EnableCORS(next http.HandlerFunc) http.HandlerFunc {
	// Read comma-separated origins from ALLOWED_ORIGINS
	allowedOrigins := strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",")
	// Trim spaces just in case
	for i, origin := range allowedOrigins {
		allowedOrigins[i] = strings.TrimSpace(origin)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		requestOrigin := r.Header.Get("Origin")

		var isAllowed bool
		for _, allowed := range allowedOrigins {
			if allowed == requestOrigin {
				isAllowed = true
				break
			}
		}

		// If the request Origin is in the allowed list, set CORS headers for that Origin
		if isAllowed {
			w.Header().Set("Access-Control-Allow-Origin", requestOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		} else {
			// Optionally, return a 403 if you want to block explicitly:
			// http.Error(w, "Forbidden", http.StatusForbidden)
			// return
			//
			// Or do nothing and let it fail in the browser due to missing CORS headers.
		}

		// If it's an OPTIONS request, just return 200 OK
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Otherwise, continue to the actual handler
		next(w, r)
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
