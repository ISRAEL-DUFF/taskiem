package api

// Links that open the phone app (docs/spec/mobile-app-m4.md N5) and the
// app's passkeys (N6): the two files a phone reads from this server to trust
// the app with its links and its sign-ins, from configuration. Without it
// both answer 404, and links open the web as they always have.

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// AppLinks is which apps this server's links and passkeys belong to.
type AppLinks struct {
	// AndroidPackage and AndroidSHA256 (the signing certificates'
	// fingerprints, AA:BB:...) are the Android app's.
	AndroidPackage string
	AndroidSHA256  []string
	// IOSAppID is the iPhone app's, its team and bundle id (TEAM.bundle).
	IOSAppID string
}

var (
	androidPackage = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)+$`)
	certPrint      = regexp.MustCompile(`^([0-9A-F]{2}:){31}[0-9A-F]{2}$`)
	iosAppID       = regexp.MustCompile(`^[A-Z0-9]{10}\.[a-zA-Z0-9\-.]+$`)
)

// ParseAppLinks reads TASKIEM_ANDROID_APP ({"package": ..., "sha256":
// ["AA:BB:..."]}) and TASKIEM_IOS_APP (TEAMID.bundle.id); either may be empty.
func ParseAppLinks(android, ios string) (AppLinks, error) {
	var out AppLinks
	if android = strings.TrimSpace(android); android != "" {
		var a struct {
			Package string   `json:"package"`
			SHA256  []string `json:"sha256"`
		}
		if err := json.Unmarshal([]byte(android), &a); err != nil || !androidPackage.MatchString(a.Package) || len(a.SHA256) == 0 || len(a.SHA256) > 5 {
			return AppLinks{}, errors.New(`TASKIEM_ANDROID_APP is {"package": "app.taskiem.chat", "sha256": ["AA:BB:..."]}: the package name and its signing certificates' SHA-256`)
		}
		for _, p := range a.SHA256 {
			p = strings.ToUpper(strings.TrimSpace(p))
			if !certPrint.MatchString(p) {
				return AppLinks{}, errors.New("TASKIEM_ANDROID_APP: a certificate's SHA-256 is 32 bytes as AA:BB:... (keytool -list -v, or Play Console, App signing)")
			}
			out.AndroidSHA256 = append(out.AndroidSHA256, p)
		}
		out.AndroidPackage = a.Package
	}
	if ios = strings.TrimSpace(ios); ios != "" {
		if !iosAppID.MatchString(ios) {
			return AppLinks{}, errors.New("TASKIEM_IOS_APP is the iPhone app's team and bundle id, as TEAMID.app.taskiem.chat")
		}
		out.IOSAppID = ios
	}
	return out, nil
}

// PasskeyOrigins are the origins a passkey made in the Android app comes
// from (android:apk-key-hash:<the certificate's SHA-256, base64url>); an
// iPhone app's passkeys come from the web's own origin.
func (a AppLinks) PasskeyOrigins() []string {
	var out []string
	for _, p := range a.AndroidSHA256 {
		raw, err := hex.DecodeString(strings.ReplaceAll(p, ":", ""))
		if err != nil {
			continue
		}
		out = append(out, "android:apk-key-hash:"+base64.RawURLEncoding.EncodeToString(raw))
	}
	return out
}

// appPaths are the pages the app opens; everything else stays on the web.
var appPaths = []string{"/chat", "/chat/*", "/invoices/*", "/trade-orders/*", "/storefront/orders/*", "/bills/*", "/purchases/*", "/approvals", "/approvals/*"}

// assetLinks is Android's Digital Asset Links: the app handles these links
// and shares these sign-ins (passkeys).
func (s *Server) assetLinks(w http.ResponseWriter, _ *http.Request) {
	a := s.AppLinks
	if a.AndroidPackage == "" {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, []any{map[string]any{
		"relation": []string{"delegate_permission/common.handle_all_urls", "delegate_permission/common.get_login_creds"},
		"target": map[string]any{
			"namespace":                "android_app",
			"package_name":             a.AndroidPackage,
			"sha256_cert_fingerprints": a.AndroidSHA256,
		},
	}})
}

// appleSiteAssociation is the iPhone's: the links the app opens, and its
// passkeys (webcredentials).
func (s *Server) appleSiteAssociation(w http.ResponseWriter, _ *http.Request) {
	id := s.AppLinks.IOSAppID
	if id == "" {
		http.NotFound(w, nil)
		return
	}
	components := make([]map[string]string, 0, len(appPaths))
	for _, p := range appPaths {
		components = append(components, map[string]string{"/": p})
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"applinks":       map[string]any{"details": []any{map[string]any{"appIDs": []string{id}, "components": components}}},
		"webcredentials": map[string]any{"apps": []string{id}},
	})
}
