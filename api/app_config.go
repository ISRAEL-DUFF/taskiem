package api

import "net/http"

// The phone app's config (docs/spec/mobile-app.md §5.1, S1): what the app
// is told about this server before anyone signs in. An app older than the
// lowest version served says so and asks to be updated, rather than failing
// in ways nobody can see.

// appMinVersionDefault is the lowest app version served when the deployment
// names none: the first.
const appMinVersionDefault = "0.1.0"

// appConfig is GET /v1/app/config (no session).
func (s *Server) appConfig(w http.ResponseWriter, _ *http.Request) {
	min := s.AppMinVersion
	if min == "" {
		min = appMinVersionDefault
	}
	out := map[string]any{
		"min_version": min,
		"passkeys":    s.WebAuthn.RPID != "" && len(s.WebAuthn.Origins) > 0,
		"signup":      s.AllowSignup,
		// Signing up in the app as on WhatsApp (app_signup.go).
		"chat_signup": s.appSignupOn(),
	}
	// The app starts Firebase with these (public client values, as in any
	// app's google-services.json), so notifications turn on with no new build.
	if s.Push.FCMOn() {
		out["push"] = map[string]any{"fcm": s.Push.FCM.Client}
	}
	writeJSON(w, http.StatusOK, out)
}
