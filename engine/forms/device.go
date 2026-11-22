package forms

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/wd"
)

// Device fields and the device an answer came from
// (docs/spec/device-fields.md §3, §4): every answer carries `device`, the
// app that answered, its clock and the server's; a device field's answer
// carries its `state` and a `source` the server sets from the channel,
// never from the answer's own body.

// Device is what a client says of the device that answered. The api role
// takes it from the app and the web; the form host sets none.
type Device struct {
	// App is app (the phone app) or web; others are set by the server.
	App     string `json:"app,omitempty"`
	Version string `json:"version,omitempty"`
	// Online is false for an answer made offline and sent later.
	Online *bool `json:"online,omitempty"`
	// At is when the answer was made, by the device's clock.
	At *time.Time `json:"at,omitempty"`
}

// Device apps an answer can come from (§3).
const (
	AppPhone    = "app"
	AppWeb      = "web"
	AppLink     = "link"
	AppWhatsApp = "whatsapp"
	AppTelegram = "telegram"
)

// ClockSlack is how far a device's clock may be from the server's before
// an answer is marked clock_differs. An answer made offline may have been
// made any time before it was received, so only a clock ahead of the
// server's beyond this counts then.
const ClockSlack = 5 * time.Minute

// deviceMeta is an answer's `device`: received is the server's time.
func deviceMeta(sub Submission, received time.Time) map[string]any {
	app := AppLink
	var d Device
	if sub.Device != nil {
		d = *sub.Device
	}
	online := true
	switch sub.Channel {
	case "app":
		// The member API, from the phone app or the web.
		app = AppWeb
		if d.App == AppPhone {
			app = AppPhone
		}
		if d.Online != nil {
			online = *d.Online
		}
	case AppWhatsApp, AppTelegram:
		app = sub.Channel
	}
	received = received.UTC().Truncate(time.Second)
	at := received
	if d.At != nil && !d.At.IsZero() && sub.Channel == "app" {
		at = d.At.UTC().Truncate(time.Second)
	}
	out := map[string]any{"app": app, "online": online, "at": at.Format(time.RFC3339), "received_at": received.Format(time.RFC3339)}
	if v := strings.TrimSpace(d.Version); v != "" && sub.Channel == "app" {
		out["version"] = clip(v, 40)
	}
	ahead, behind := at.Sub(received), received.Sub(at)
	if ahead > ClockSlack || (online && behind > ClockSlack) {
		out["clock_differs"] = true
	}
	return out
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// answerable reports whether a channel can answer a kind of device field
// today. The member API (the app and the web) takes a location and a scan
// as values; photos and signatures need an upload it does not take yet,
// and the form host's page does not ask a device before D1.
func answerable(kind, channel string) bool {
	return channel == "app" && (kind == wd.DeviceLocation || kind == wd.DeviceScan)
}

// deviceSource is how a channel got a device answer: never what the
// answer's body says.
func deviceSource(kind string, sub Submission, typed bool) string {
	chat := sub.Channel == AppWhatsApp || sub.Channel == AppTelegram
	switch kind {
	case wd.DeviceLocation:
		switch {
		case typed:
			return "typed"
		case chat:
			return "sent"
		}
		return "device"
	case wd.DevicePhoto:
		if sub.Channel == "app" && sub.Device != nil && sub.Device.App == AppPhone {
			return "camera"
		}
		return "sent"
	case wd.DeviceScan:
		if typed || chat {
			return "typed"
		}
		return "camera"
	}
	return "drawn"
}

// deviceAnswers reads each device field's answer into what the run keeps:
// its state and source, a location's coarse mark, refusals in plain words.
// A field the channel cannot answer and that has no answer is unavailable.
func deviceAnswers(schema map[string]any, answers map[string]any, sub Submission, deviceAt time.Time) string {
	props, _ := schema["properties"].(map[string]any)
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		kind := wd.DeviceKind(p)
		if kind == "" {
			continue
		}
		v, present := answers[name]
		if !present || v == nil {
			delete(answers, name)
			if !answerable(kind, sub.Channel) {
				answers[name] = map[string]any{"state": "unavailable", "reason": "this question cannot be answered here yet"}
			}
			continue
		}
		var m map[string]any
		switch t := v.(type) {
		case map[string]any:
			m = t
		case FileRef:
			m = map[string]any{"id": t.ID, "name": t.Name, "type": t.Type, "size": t.Size, "sha256": t.SHA256}
		default:
			return fmt.Sprintf("%q: the answer is not one %s field takes", name, kind)
		}
		out, why := deviceAnswer(name, kind, p, m, sub, deviceAt)
		if why != "" {
			return why
		}
		answers[name] = out
	}
	return ""
}

func deviceAnswer(name, kind string, p, m map[string]any, sub Submission, deviceAt time.Time) (map[string]any, string) {
	if st, _ := m["state"].(string); st == "refused" || st == "unavailable" {
		// A refusal is an answer (§4).
		out := map[string]any{"state": st}
		if r, ok := m["reason"].(string); ok && strings.TrimSpace(r) != "" {
			out["reason"] = clip(strings.TrimSpace(r), 500)
		}
		return out, ""
	}
	allowTyped := p["allow_typed"] != false
	out := map[string]any{}
	typed := false
	switch kind {
	case wd.DeviceLocation:
		lat, okLat := number(m["lat"])
		lng, okLng := number(m["lng"])
		addr, _ := m["address"].(string)
		switch {
		case okLat && okLng:
			if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
				return nil, fmt.Sprintf("%q: that is not a place on Earth", name)
			}
			out["lat"], out["lng"] = lat, lng
			acc, okAcc := number(m["accuracy_m"])
			if okAcc && acc >= 0 {
				out["accuracy_m"] = acc
			}
			if fix, _ := m["fix"].(string); fix == "gps" || fix == "network" {
				out["fix"] = fix
			}
			at := deviceAt
			if s, _ := m["at"].(string); s != "" {
				t, err := time.Parse(time.RFC3339, s)
				if err != nil {
					return nil, fmt.Sprintf("%q: when the location was read is a time, like 2026-11-22T10:00:00Z", name)
				}
				at = t
			}
			out["at"] = at.UTC().Truncate(time.Second).Format(time.RFC3339)
			if s, _ := p["max_age"].(string); s != "" {
				if max, err := wd.ParseDuration(s); err == nil && deviceAt.Sub(at) > max {
					return nil, fmt.Sprintf("%q: the location was read %s before the answer was made, longer ago than this question takes; read it again", name, deviceAt.Sub(at).Round(time.Second))
				}
			}
			if max, ok := number(p["max_accuracy_m"]); ok && (!okAcc || acc > max) {
				out["coarse"] = true
			}
		case strings.TrimSpace(addr) != "":
			if !allowTyped {
				return nil, fmt.Sprintf("%q needs the device's location: an address typed is not taken", name)
			}
			typed = true
			out["address"] = clip(strings.TrimSpace(addr), 500)
		default:
			return nil, fmt.Sprintf("%q: a location is its lat and lng (with accuracy_m and at), or an address typed", name)
		}
	case wd.DeviceScan:
		text, _ := m["text"].(string)
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Sprintf("%q: a scan is the code's text", name)
		}
		out["text"] = clip(text, 2000)
		format, _ := m["format"].(string)
		typed = format == ""
		if typed && !allowTyped {
			return nil, fmt.Sprintf("%q needs the code scanned: a code typed is not taken", name)
		}
		if format != "" {
			if fs, ok := p["formats"].([]any); ok && !slices.Contains(fs, any(format)) {
				return nil, fmt.Sprintf("%q: this question does not take %s codes", name, format)
			}
			out["format"] = format
		}
	case wd.DevicePhoto, wd.DeviceSignature:
		for _, k := range []string{"id", "name", "type", "sha256", "size"} {
			if v, ok := m[k]; ok {
				out[k] = v
			}
		}
		if _, ok := out["id"].(string); !ok {
			return nil, fmt.Sprintf("%q: %s is sent as a file", name, kind)
		}
	}
	source := deviceSource(kind, sub, typed)
	out["source"] = source
	out["state"] = "answered"
	if want, _ := p["require_source"].(string); want != "" && source != want {
		// A weaker source than the field requires: held as unverified,
		// which the workflow handles as it handles refused (§3).
		out["state"] = "unverified"
	}
	return out, ""
}

func number(v any) (float64, bool) {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case int64:
		f = float64(t)
	case int:
		f = float64(t)
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}
