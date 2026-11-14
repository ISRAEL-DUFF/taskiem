package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

const testPrint = "14:6D:E9:83:C5:73:06:50:D8:EE:B9:95:2F:34:FC:64:16:A0:83:42:E6:1D:BE:A8:8A:04:96:B2:3F:CF:44:E5"

func TestParseAppLinks(t *testing.T) {
	a, err := api.ParseAppLinks(`{"package":"app.taskiem.chat","sha256":["`+strings.ToLower(testPrint)+`"]}`, "ABCDE12345.app.taskiem.chat")
	if err != nil {
		t.Fatal(err)
	}
	if a.AndroidPackage != "app.taskiem.chat" || a.AndroidSHA256[0] != testPrint || a.IOSAppID != "ABCDE12345.app.taskiem.chat" {
		t.Errorf("parsed: %+v", a)
	}
	// The origin Android gives a passkey made in the app: the certificate's hash, base64url.
	if o := a.PasskeyOrigins(); len(o) != 1 || o[0] != "android:apk-key-hash:FG3pg8VzBlDY7rmVLzT8ZBagg0LmHb6oigSWsj_PROU" {
		t.Errorf("origins: %v", o)
	}
	if none, err := api.ParseAppLinks("", ""); err != nil || none.AndroidPackage != "" || none.PasskeyOrigins() != nil {
		t.Errorf("none: %+v %v", none, err)
	}
	for _, bad := range [][2]string{
		{`{"package":"app.taskiem.chat","sha256":["AB:CD"]}`, ""},
		{`{"package":"not a package","sha256":["` + testPrint + `"]}`, ""},
		{`{"package":"app.taskiem.chat"}`, ""},
		{"app.taskiem.chat", ""},
		{"", "app.taskiem.chat"},
	} {
		if _, err := api.ParseAppLinks(bad[0], bad[1]); err == nil {
			t.Errorf("accepted %q %q", bad[0], bad[1])
		}
	}
}

func TestWellKnownAppFiles(t *testing.T) {
	w := newWorld(t)
	anon := &client{t: t, base: w.base}
	for _, p := range []string{"/.well-known/assetlinks.json", "/.well-known/apple-app-site-association"} {
		if s, _ := anon.do("GET", p, nil); s != http.StatusNotFound {
			t.Errorf("%s without configuration: %d", p, s)
		}
	}
	w.srv.AppLinks, _ = api.ParseAppLinks(`{"package":"app.taskiem.chat","sha256":["`+testPrint+`"]}`, "ABCDE12345.app.taskiem.chat")
	h := w.srv.Handler()
	get := func(p string) (int, any) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		var out any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if ct := rec.Header().Get("Content-Type"); rec.Code == 200 && !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: %s", p, ct)
		}
		return rec.Code, out
	}
	code, links := get("/.well-known/assetlinks.json")
	body, _ := json.Marshal(links)
	if code != 200 || !strings.Contains(string(body), `"package_name":"app.taskiem.chat"`) || !strings.Contains(string(body), testPrint) || !strings.Contains(string(body), "get_login_creds") {
		t.Errorf("assetlinks: %d %s", code, body)
	}
	code, apple := get("/.well-known/apple-app-site-association")
	body, _ = json.Marshal(apple)
	if code != 200 || !strings.Contains(string(body), `"appIDs":["ABCDE12345.app.taskiem.chat"]`) || !strings.Contains(string(body), `"webcredentials":{"apps":["ABCDE12345.app.taskiem.chat"]}`) || !strings.Contains(string(body), `{"/":"/chat"}`) {
		t.Errorf("apple: %d %s", code, body)
	}
}
