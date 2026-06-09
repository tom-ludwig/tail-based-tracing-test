package middleware

import (
	"net/http"
)

// SecurityHeaders sets security-related HTTP headers
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "middleware.SecurityHeaders")
		// defer span.End()
		// r = r.WithContext(ctx)

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		span.End()

		next.ServeHTTP(w, r)
	})
}
