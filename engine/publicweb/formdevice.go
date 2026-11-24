package publicweb

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/wd"
)

// Device fields on the one-time page (docs/spec/device-fields.md §3, §17,
// build-plan-gaps H3 g-dev-link): a form step sent by text or email asks
// the browser, at the person's tap and after the question's why, as the
// app and the web do. A location is the browser's own prompt (a refusal
// sent refused, a failure unavailable; an address typed where the field
// allows); a photo the camera where the phone offers it, else a file; a
// signature a pad, its drawing sent as a PNG file; a code the camera
// where the browser has a barcode reader (BarcodeDetector), else typed.
// Every one has "I can't answer this", a refusal being an answer (§4).
// The page works without its script for what needs none: an address or
// a code typed, a photo chosen, I can't. The server sets each answer's
// source from the channel (engine/forms), never from what the page says.

// deviceField makes a device property's widget.
func deviceField(f *field, kind string, p map[string]any) {
	f.Device, f.Why = kind, str(p["why"])
	f.AllowTyped = p["allow_typed"] != false
	switch kind {
	case wd.DeviceLocation:
		f.Widget = "location"
	case wd.DeviceScan:
		f.Widget = "scan"
		var fs []string
		if list, ok := p["formats"].([]any); ok {
			for _, x := range list {
				if s := str(x); s != "" {
					fs = append(fs, s)
				}
			}
		}
		if len(fs) == 0 {
			fs = wd.ScanFormats
		}
		f.Formats = strings.Join(fs, " ")
	case wd.DevicePhoto:
		f.Widget, f.Accept, f.maxMB = "file", "image/jpeg,image/png", maxFileMB
	case wd.DeviceSignature:
		f.Widget, f.Accept, f.maxMB = "file", "image/png", 1
		f.AskName = p["ask_name"] == true
	}
}

// cantReason is a device field's refusal on the page.
const cantReason = "I could not answer this on the page"

// readDevice reads a device field from what the browser sent into the
// state's answers; the message says what is missing or wrong.
func (hf *hform) readDevice(st *hstate, g *field, v url.Values, r *http.Request, carried *int) string {
	name := g.Name
	defer func() { g.deviceShow(st.Answers[name]) }()
	if v.Get(g.Input()+".cant") != "" {
		st.Answers[name] = map[string]any{"state": "refused", "reason": cantReason}
		delete(st.Files, name)
		return ""
	}
	switch g.Device {
	case wd.DevicePhoto, wd.DeviceSignature:
		msg := hf.readFile(st, g, r, carried)
		h, held := st.Files[name]
		if held {
			g.Held = h.Name
		}
		switch {
		case !held:
			delete(st.Answers, name)
			if msg != "" && g.Device == wd.DeviceSignature {
				msg = "Please sign for " + g.Label + ", or tick I can't answer this."
			} else if msg != "" && strings.HasPrefix(msg, "Please attach") {
				msg = "Please take or choose a photo for " + g.Label + ", or tick I can't answer this."
			}
			return msg
		case g.Device == wd.DeviceSignature && g.AskName:
			who, bad := text(v.Get(g.Input()+".signed_by"), 120, false)
			g.SignedBy = who
			if bad || who == "" {
				delete(st.Answers, name)
				return "Please write the name of who signed " + g.Label + "."
			}
			// Beside the drawing: engine/forms keeps it as the signer.
			st.Answers[name] = map[string]any{"signed_by": who}
		default:
			delete(st.Answers, name) // the file is the answer
		}
		return msg
	case wd.DeviceLocation:
		lat, errLat := strconv.ParseFloat(strings.TrimSpace(v.Get(g.Input()+".lat")), 64)
		lng, errLng := strconv.ParseFloat(strings.TrimSpace(v.Get(g.Input()+".lng")), 64)
		state := v.Get(g.Input() + ".state")
		addr, bad := text(v.Get(g.Input()+".address"), 500, false)
		switch {
		case errLat == nil && errLng == nil:
			if math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
				delete(st.Answers, name)
				return "The location read for " + g.Label + " is not a place on Earth. Please read it again."
			}
			m := map[string]any{"lat": lat, "lng": lng}
			if acc, err := strconv.ParseFloat(strings.TrimSpace(v.Get(g.Input()+".acc")), 64); err == nil && acc >= 0 && !math.IsInf(acc, 0) {
				m["accuracy_m"] = math.Round(acc)
			}
			if at := strings.TrimSpace(v.Get(g.Input() + ".at")); at != "" {
				if t, err := time.Parse(time.RFC3339, at); err == nil {
					m["at"] = t.UTC().Format(time.RFC3339)
				}
			}
			if fix := v.Get(g.Input() + ".fix"); fix == "gps" || fix == "network" {
				m["fix"] = fix
			}
			st.Answers[name] = m
		case state == "refused":
			st.Answers[name] = map[string]any{"state": "refused", "reason": "the browser was not allowed to read where I am"}
		case state == "unavailable":
			st.Answers[name] = map[string]any{"state": "unavailable", "reason": "the browser could not read where I am"}
		case !bad && addr != "" && g.AllowTyped:
			st.Answers[name] = map[string]any{"address": addr}
		default:
			delete(st.Answers, name)
			if g.Required {
				if g.AllowTyped {
					return "Please tap Use my location for " + g.Label + ", type the address, or tick I can't answer this."
				}
				return "Please tap Use my location for " + g.Label + ", or tick I can't answer this."
			}
		}
		return ""
	case wd.DeviceScan:
		code := strings.TrimSpace(v.Get(g.Input()))
		format := v.Get(g.Input() + ".format")
		g.Value = code
		if code == "" {
			delete(st.Answers, name)
			if g.Required {
				return "Please scan or type the code for " + g.Label + ", or tick I can't answer this."
			}
			return ""
		}
		if why := wd.ScanTextProblem(code); why != "" {
			delete(st.Answers, name)
			return "Please check the code for " + g.Label + ": " + why + "."
		}
		if !slices.Contains(wd.ScanFormats, format) {
			format = ""
		}
		if !wd.ScanTakes(g.schema, format) {
			delete(st.Answers, name)
			if format == "" {
				return "Please scan the code for " + g.Label + " with the camera: a code typed is not taken."
			}
			return fmt.Sprintf("This question does not take %s codes. Please scan the code it asks for.", formatWords(format))
		}
		m := map[string]any{"text": code}
		if format != "" {
			m["format"] = format
		}
		st.Answers[name] = m
	}
	return ""
}

// deviceShow sets what a device field shows from its answer so far.
func (f *field) deviceShow(v any) {
	m, _ := v.(map[string]any)
	f.Cant, f.Got, f.Lat, f.Lng, f.Acc, f.At, f.Fix, f.State, f.Format = false, "", "", "", "", "", "", "", ""
	if m == nil {
		return
	}
	switch st, _ := m["state"].(string); {
	case st == "refused" && m["reason"] == cantReason:
		f.Cant, f.Got = true, "You said you can't answer this."
		return
	case st == "refused":
		f.State, f.Got = st, "Your browser was not allowed to read your location."
		return
	case st == "unavailable":
		f.State, f.Got = st, "Your browser could not read your location."
		return
	}
	switch f.Device {
	case wd.DeviceLocation:
		if lat, ok := m["lat"].(float64); ok {
			lng, _ := m["lng"].(float64)
			f.Lat, f.Lng = strconv.FormatFloat(lat, 'f', -1, 64), strconv.FormatFloat(lng, 'f', -1, 64)
			f.Got = "Your location was read."
			if acc, ok := m["accuracy_m"].(float64); ok {
				f.Acc = fmtNum(acc)
				f.Got = "Your location was read, to within " + fmtNum(acc) + " m."
			}
			f.At, _ = m["at"].(string)
			f.Fix, _ = m["fix"].(string)
		} else if a, ok := m["address"].(string); ok {
			f.Address, f.Got = a, ""
		}
	case wd.DeviceScan:
		f.Value, _ = m["text"].(string)
		f.Format, _ = m["format"].(string)
		if f.Format != "" {
			f.Got = "Scanned: " + formatWords(f.Format) + "."
		}
	case wd.DeviceSignature:
		f.SignedBy, _ = m["signed_by"].(string)
	}
}

// formatWords names a code format as people read it.
func formatWords(f string) string {
	switch f {
	case "qr":
		return "QR"
	case "ean_13":
		return "EAN-13"
	case "ean_8":
		return "EAN-8"
	case "upc_a":
		return "UPC-A"
	case "upc_e":
		return "UPC-E"
	case "code_128":
		return "Code 128"
	}
	return f
}

// hasDevice reports whether a form asks a device, and hasScan whether it
// asks a code (the page may then use the camera to scan it).
func (hf *hform) hasDevice() bool {
	for _, f := range hf.fields {
		if f.Device != "" {
			return true
		}
	}
	return false
}

func (hf *hform) hasScan() bool {
	for _, f := range hf.fields {
		if f.Device == wd.DeviceScan {
			return true
		}
	}
	return false
}
