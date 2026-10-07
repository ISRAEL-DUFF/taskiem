// Package httpsec sets the security headers every Taskiem HTTP listener
// sends: the API and console, and the edge that receives webhooks.
package httpsec

import "net/http"

// CSP is the Content-Security-Policy sent with every response.
const CSP = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// Set writes the security headers to h.
func Set(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", CSP)
}

// Headers is middleware that sets the security headers on every response.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Set(w.Header())
		next.ServeHTTP(w, r)
	})
}
