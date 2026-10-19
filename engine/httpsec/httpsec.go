// Package httpsec sets the security headers every Taskiem HTTP listener
// sends: the API and console, and the edge that receives webhooks.
package httpsec

import (
	"net/http"
	"strings"
)

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

// FrameCSP is the Content-Security-Policy of the embedded builder's frame
// page (docs/embedding.md#iframe-mode): the one page that may be framed,
// and only by ancestors, an embed app's allowed origins. Scripts and API
// calls stay on the page's own origin; images, styles and fonts may come
// from the https URLs of the app's branding tokens. With no ancestors no
// one may frame it.
func FrameCSP(ancestors []string) string {
	fa := "'none'"
	if len(ancestors) > 0 {
		fa = strings.Join(ancestors, " ")
	}
	return "default-src 'none'; script-src 'self'; connect-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline' https:; " +
		"font-src 'self' data: https:; base-uri 'none'; form-action 'none'; frame-ancestors " + fa
}

// SetFrame writes the frame page's headers over Set's: frame-ancestors
// names the allowed origins, and X-Frame-Options, which cannot name them,
// is left out (browsers that know frame-ancestors ignore it anyway).
func SetFrame(h http.Header, ancestors []string) {
	h.Del("X-Frame-Options")
	h.Set("Content-Security-Policy", FrameCSP(ancestors))
}

// PublicCSP is the Content-Security-Policy of public pages: storefronts
// and hosted forms (docs/spec/storefronts.md 8.3). No inline script or
// style, scripts and styles only from the page's own origin, images also
// from https (photos in object storage or a CDN), and nothing may frame it.
const PublicCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; font-src 'self'; connect-src 'self'; " +
	"form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// SetPublic writes a public page's headers: Set's, with the public policy,
// a browsing context of its own, and no access to the camera, microphone,
// location or payment APIs.
func SetPublic(h http.Header) {
	Set(h)
	h.Set("Content-Security-Policy", PublicCSP)
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
}
