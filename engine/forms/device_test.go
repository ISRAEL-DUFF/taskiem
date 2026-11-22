package forms

import (
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestDeviceMeta(t *testing.T) {
	recv := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		sub    Submission
		app    string
		at     string
		online bool
		differ bool
	}{
		{"the form host: a link, the server's time", Submission{Channel: "direct", Device: &Device{App: "app", At: ptr(recv.Add(-time.Hour))}}, "link", "2026-11-22T10:00:00Z", true, false},
		{"the web, by default", Submission{Channel: "app"}, "web", "2026-11-22T10:00:00Z", true, false},
		{"the phone app, a minute's skew", Submission{Channel: "app", Device: &Device{App: "app", Version: "1.4.2", At: ptr(recv.Add(-time.Minute))}}, "app", "2026-11-22T09:59:00Z", true, false},
		{"online, an hour behind", Submission{Channel: "app", Device: &Device{App: "app", At: ptr(recv.Add(-time.Hour))}}, "app", "2026-11-22T09:00:00Z", true, true},
		{"offline, an hour behind: offline explains it", Submission{Channel: "app", Device: &Device{App: "app", Online: ptr(false), At: ptr(recv.Add(-time.Hour))}}, "app", "2026-11-22T09:00:00Z", false, false},
		{"offline, an hour ahead", Submission{Channel: "app", Device: &Device{App: "app", Online: ptr(false), At: ptr(recv.Add(time.Hour))}}, "app", "2026-11-22T11:00:00Z", false, true},
		{"an app the client names is not believed", Submission{Channel: "app", Device: &Device{App: "whatsapp"}}, "web", "2026-11-22T10:00:00Z", true, false},
	} {
		m := deviceMeta(c.sub, recv)
		if m["app"] != c.app || m["at"] != c.at || m["online"] != c.online || (m["clock_differs"] == true) != c.differ || m["received_at"] != "2026-11-22T10:00:00Z" {
			t.Errorf("%s: %v", c.name, m)
		}
	}
	if m := deviceMeta(Submission{Channel: "app", Device: &Device{App: "app", Version: "1.4.2"}}, recv); m["version"] != "1.4.2" {
		t.Errorf("version: %v", m)
	}
}

func TestDeviceAnswers(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"where": map[string]any{"type": "location", "why": "w", "max_accuracy_m": float64(50), "max_age": "5m", "require_source": "device"},
		"code":  map[string]any{"type": "scan", "why": "w", "formats": []any{"qr"}},
		"proof": map[string]any{"type": "photo", "why": "w"},
		"sign":  map[string]any{"type": "signature", "why": "w"},
		"note":  map[string]any{"type": "string"},
	}}
	at := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	app := Submission{Channel: "app", Device: &Device{App: "app"}}

	// A reading from the app: source set by the server whatever the body
	// says, coarse beyond max_accuracy_m; photos and signatures the app
	// cannot send yet are unavailable.
	answers := map[string]any{"note": "ok",
		"where": map[string]any{"lat": 6.452131, "lng": 3.395812, "accuracy_m": float64(80), "at": "2026-11-22T09:58:00Z", "source": "sent", "state": "unverified", "extra": 1},
		"code":  map[string]any{"text": "PKG-1", "format": "qr", "source": "typed"}}
	if why := deviceAnswers(schema, answers, app, at); why != "" {
		t.Fatal(why)
	}
	where := answers["where"].(map[string]any)
	if where["source"] != "device" || where["state"] != "answered" || where["coarse"] != true || where["extra"] != nil || where["at"] != "2026-11-22T09:58:00Z" {
		t.Fatalf("where: %v", where)
	}
	if c := answers["code"].(map[string]any); c["source"] != "camera" || c["state"] != "answered" {
		t.Fatalf("code: %v", c)
	}
	// Photos and signatures the app can send (D1): left out, they are not
	// made up as unavailable.
	for _, f := range []string{"proof", "sign"} {
		if v, ok := answers[f]; ok {
			t.Fatalf("%s: %v", f, v)
		}
	}

	// WhatsApp and Telegram (D1): a pin and a photo sent are `sent`, a
	// signature and a scan unavailable there, in plain words.
	for _, ch := range []string{AppWhatsApp, AppTelegram} {
		chat := Submission{Channel: ch}
		answers = map[string]any{"where": map[string]any{"lat": 6.452131, "lng": 3.395812}, "proof": FileRef{ID: "f2", Name: "photo", Type: "image/jpeg"}}
		if why := deviceAnswers(schema, answers, chat, at); why != "" {
			t.Fatal(why)
		}
		where := answers["where"].(map[string]any)
		if where["source"] != "sent" || where["state"] != "unverified" { // the field requires the device's own reading
			t.Fatalf("%s where: %v", ch, where)
		}
		if p := answers["proof"].(map[string]any); p["source"] != "sent" || p["state"] != "answered" {
			t.Fatalf("%s proof: %v", ch, p)
		}
		if s := answers["sign"].(map[string]any); s["state"] != "unavailable" || !strings.Contains(s["reason"].(string), "cannot be given in") {
			t.Fatalf("%s sign: %v", ch, s)
		}
		if c := answers["code"].(map[string]any); c["state"] != "unavailable" {
			t.Fatalf("%s code: %v", ch, c)
		}
		if d := deviceMeta(chat, at); d["app"] != ch {
			t.Fatalf("%s device: %v", ch, d)
		}
	}

	// An address typed: typed, and so unverified where the device is
	// required; a refusal is an answer.
	answers = map[string]any{"where": map[string]any{"address": "12 Allen Avenue, Ikeja"}, "code": map[string]any{"state": "refused", "reason": "no camera", "text": "x"}}
	if why := deviceAnswers(schema, answers, app, at); why != "" {
		t.Fatal(why)
	}
	if w := answers["where"].(map[string]any); w["source"] != "typed" || w["state"] != "unverified" {
		t.Fatalf("typed: %v", w)
	}
	if c := answers["code"].(map[string]any); c["state"] != "refused" || c["reason"] != "no camera" || c["text"] != nil {
		t.Fatalf("refused: %v", c)
	}

	// Refused in plain words.
	for _, c := range []struct {
		where any
		want  string
	}{
		{map[string]any{"lat": 6.4, "lng": 3.3, "at": "2026-11-22T09:00:00Z"}, "read it again"},
		{map[string]any{"lat": 96.0, "lng": 3.3}, "not a place on Earth"},
		{map[string]any{"lng": 3.3}, "a location is its lat and lng"},
		{"Ikeja", "the answer is not one location field takes"},
	} {
		if why := deviceAnswers(schema, map[string]any{"where": c.where}, app, at); !strings.Contains(why, c.want) {
			t.Errorf("%v: %q, want %q", c.where, why, c.want)
		}
	}
	if why := deviceAnswers(schema, map[string]any{"code": map[string]any{"text": "x", "format": "ean_13"}}, app, at); !strings.Contains(why, "this question does not take ean_13 codes") {
		t.Errorf("format: %q", why)
	}

	// The form host's page asks no device yet: all unavailable, even when
	// something was sent for them.
	answers = map[string]any{}
	if why := deviceAnswers(schema, answers, Submission{Channel: "direct"}, at); why != "" {
		t.Fatal(why)
	}
	if len(answers) != 4 || answers["where"].(map[string]any)["state"] != "unavailable" {
		t.Fatalf("form host: %v", answers)
	}
	// A required location left out on the app is not made up: the
	// schema's required check refuses it.
	answers = map[string]any{}
	_ = deviceAnswers(schema, answers, app, at)
	if _, ok := answers["where"]; ok {
		t.Fatalf("made up: %v", answers)
	}

	// A photo sent from the phone app is the camera's, from the web a
	// file chosen.
	ref := FileRef{ID: "f1", Name: "door.jpg", Type: "image/jpeg", Size: 10, SHA256: "ab"}
	answers = map[string]any{"proof": ref}
	if why := deviceAnswers(schema, answers, app, at); why != "" {
		t.Fatal(why)
	}
	if p := answers["proof"].(map[string]any); p["source"] != "camera" || p["id"] != "f1" {
		t.Fatalf("photo: %v", p)
	}
	answers = map[string]any{"proof": ref}
	_ = deviceAnswers(schema, answers, Submission{Channel: "app"}, at)
	if p := answers["proof"].(map[string]any); p["source"] != "sent" {
		t.Fatalf("photo from the web: %v", p)
	}
}
