package wd_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

// handover is the delivery fixture of docs/spec/device-fields.md §6, asked
// of a member; to: {link}, the fixture's own (D1), is checked below.
const handover = `{"schema":"wd/v1","id":"wf_deliver","version":1,"name":"Deliver",
 "trigger":{"type":"manual"},
 "steps":[{"id":"handover","type":"form","config":{"title":"Delivered",
  "to":{"member":"=trigger.body.rider"},
  "schema":{"type":"object","required":["where","proof"],"properties":{
   "where":{"type":"location","why":"so the delivery is recorded where it was handed over","max_accuracy_m":100,"max_age":"5m","require_source":"device"},
   "proof":{"type":"photo","why":"a photo of the parcel at the door, for the shop's records","require_source":"camera"},
   "code":{"type":"scan","why":"the parcel's label","formats":["qr","code_128"],"allow_typed":false},
   "signed":{"type":"signature","why":"if the customer will sign for it"},
   "note":{"type":"string"}}},
  "timeout":"2h","on_timeout":"continue"}}]}`

func TestDeviceFieldsValidate(t *testing.T) {
	if p := wd.Validate([]byte(handover)); len(p) != 0 {
		t.Fatalf("the delivery: %v", p)
	}
	d, err := wd.Load([]byte(handover))
	if err != nil {
		t.Fatal(err)
	}
	f := d.Steps[0].Form
	if f.To.Kind() != "member" || len(wd.DeviceFields(f.Schema)) != 4 || !wd.HasFileFields(f.Schema) {
		t.Fatalf("parsed: %+v", f.To)
	}
	for _, c := range []struct{ from, to, want string }{
		// why is required on every device field.
		{`"why":"so the delivery is recorded where it was handed over",`, ``, `"where" asks for a location: say why`},
		{`"why":"if the customer will sign for it"`, `"why":"  "`, `"signed" asks for a signature: say why`},
		// The options, each on its own kinds.
		{`"max_accuracy_m":100`, `"max_accuracy_m":0`, "max_accuracy_m is a number of metres"},
		{`"max_age":"5m"`, `"max_age":"soon"`, "max_age is a duration"},
		{`"require_source":"device"`, `"require_source":"camera"`, `require_source on a location field is "device"`},
		{`"require_source":"camera"`, `"require_source":"device"`, `require_source on a photo field is "camera"`},
		{`"formats":["qr","code_128"]`, `"formats":["qr","pdf417"]`, "pdf417 is not a code format"},
		{`"allow_typed":false`, `"allow_typed":"no"`, "allow_typed is true or false"},
		{`"why":"if the customer will sign for it"`, `"why":"if they sign","max_age":"5m"`, `a signature field has no option "max_age"`},
		{`"formats":["qr","code_128"],`, `"formats":["qr"],"require_source":"camera",`, `a scan field has no option "require_source"`},
		{`"max_accuracy_m":100,`, `"max_accuracy_m":100,"x-pii":"other",`, "a location is always sealed as a location"},
		// At most one of each kind.
		{`"note":{"type":"string"}`, `"note":{"type":"location","why":"again"}`, `"where" and "note" both ask for a location`},
		// A required signature of someone asked by phone or email.
		{`"to":{"member":"=trigger.body.rider"}`, `"to":{"phone":"=trigger.body.phone"}`, ""},
		{`"required":["where","proof"]`, `"required":["where","proof","signed"]`, ""},
		// to as a person.
		{`"to":{"member":"=trigger.body.rider"}`, `"to":{"member":"rider"}`, "member is a member's id"},
		{`"to":{"member":"=trigger.body.rider"}`, `"to":{"link":"rider"}`, "link is a business link's id"},
		{`"to":{"member":"=trigger.body.rider"}`, `"to":{"link":"=trigger.body.rider"},"verify":true`, "verify is for a phone or email"},
		{`"to":{"member":"=trigger.body.rider"}`, `"to":{"member":"=trigger.body.rider","role":"riders"}`, "/to"},
		{`"to":{"member":"=trigger.body.rider"}`, `"to":{"member":"=trigger.body.rider"},"verify":true`, "verify is for a phone or email"},
	} {
		if c.want == "" {
			continue
		}
		doc := strings.Replace(handover, c.from, c.to, 1)
		if doc == handover {
			t.Fatalf("%q not in the fixture", c.from)
		}
		p := wd.Validate([]byte(doc))
		if !strings.Contains(fmtProblems(p), c.want) {
			t.Errorf("%s → %s: %v, want %q", c.from, c.to, p, c.want)
		}
	}

	// A signature required of a phone or an email is refused; optional,
	// it is not; of a member, it is not.
	required := strings.Replace(handover, `"required":["where","proof"]`, `"required":["where","proof","signed"]`, 1)
	if p := wd.Validate([]byte(required)); len(p) != 0 {
		t.Fatalf("a signature required of a member: %v", p)
	}
	for _, to := range []string{`{"phone":"=trigger.body.phone"}`, `{"email":"=trigger.body.email"}`} {
		doc := strings.Replace(required, `{"member":"=trigger.body.rider"}`, to, 1)
		if p := fmtProblems(wd.Validate([]byte(doc))); !strings.Contains(p, `"signed": a signature cannot be required of someone asked by phone or email`) {
			t.Errorf("%s: %s", to, p)
		}
		if p := wd.Validate([]byte(strings.Replace(handover, `{"member":"=trigger.body.rider"}`, to, 1))); len(p) != 0 {
			t.Errorf("an optional signature of %s: %v", to, p)
		}
	}
	// require_source "device" asked by phone: the link opens in a
	// browser, which reads a location, so it can be met.
	if p := wd.Validate([]byte(strings.Replace(handover, `{"member":"=trigger.body.rider"}`, `{"phone":"=trigger.body.phone"}`, 1))); len(p) != 0 {
		t.Errorf("require_source by phone: %v", p)
	}
	// The person working a business link (D1): the fixture as §6 has it.
	if p := wd.Validate([]byte(strings.Replace(handover, `{"member":"=trigger.body.rider"}`, `{"link":"=trigger.body.rider"}`, 1))); len(p) != 0 {
		t.Errorf("to: {link}: %v", p)
	}
	// Five device fields.
	five := strings.Replace(handover, `"note":{"type":"string"}`, `"note":{"type":"photo","why":"another"}`, 1)
	if p := fmtProblems(wd.Validate([]byte(five))); !strings.Contains(p, "at most 4 device fields") || !strings.Contains(p, `both ask for a photo`) {
		t.Errorf("five device fields: %s", p)
	}
}

// siteVisit is a members' tool whose inputs ask the device (build-plan-gaps
// H3): where the visit was, a photo and the site manager's signature.
const siteVisit = `{"schema":"wd/v1","id":"wf_visit","version":1,"name":"Add a site visit",
 "trigger":{"type":"manual"},"tool":{"command":"visit","name":"Add a site visit","about":"Record a visit to a site"},
 "inputs":{"schema":{"type":"object","required":["where"],"properties":{
  "where":{"type":"location","why":"so the visit is recorded where it was","max_accuracy_m":200},
  "proof":{"type":"photo","why":"a photo of the work"},
  "signed":{"type":"signature","why":"the site manager signs it off","ask_name":true},
  "note":{"type":"string"}}}},
 "steps":[{"id":"n","type":"transform","config":{"output":{}}}]}`

// A location, a photo and a signature are a members' tool's top-level
// inputs (build-plan-gaps H3); anywhere else they are a form step's.
func TestDeviceFieldsAsToolInputs(t *testing.T) {
	if p := wd.Validate([]byte(siteVisit)); len(p) != 0 {
		t.Fatalf("a site visit: %v", p)
	}
	d, err := wd.Load([]byte(siteVisit))
	if err != nil {
		t.Fatal(err)
	}
	if got := wd.DeviceInputs(d); len(got) != 3 || got["where"] != "location" || got["proof"] != "photo" || got["signed"] != "signature" {
		t.Fatalf("DeviceInputs: %v", got)
	}
	// What a start may send for each.
	for _, c := range []struct {
		body string
		ok   bool
	}{
		{`{"where":{"lat":6.45,"lng":3.39,"accuracy_m":12,"at":"2026-11-24T10:00:00Z","fix":"gps"},"proof":{"file":"0193"},"signed":{"file":"0194","signed_by":"Ada"}}`, true},
		{`{"where":{"address":"12 Allen Avenue"}}`, true},
		{`{"where":{"state":"refused","reason":"no"},"proof":{"state":"unavailable"}}`, true},
		{`{"where":{"lat":200,"lng":3}}`, false},
		{`{"where":{"lat":6.45,"lng":3.39},"proof":{"id":"0193","state":"answered","source":"camera"}}`, false},
		{`{}`, false},
	} {
		if got := d.ValidateInputs([]byte(c.body)); (len(got) == 0) != c.ok {
			t.Errorf("%s: %v, want ok %v", c.body, got, c.ok)
		}
	}

	for _, c := range []struct{ from, to, want string }{
		{`"tool":{"command":"visit","name":"Add a site visit","about":"Record a visit to a site"}`, `"tool":{"command":"visit","name":"Add a site visit","about":"Record a visit to a site","for":"customers"}`, "a tool for customers cannot ask for a location"},
		{`"note":{"type":"string"}`, `"note":{"type":"location","why":"again"}`, "both ask for a location"},
		{`"note":{"type":"string"}`, `"note":{"type":"object","properties":{"inner":{"type":"photo","why":"x"}}}`, "A photo in a workflow's inputs is one of its top-level inputs"},
		{`"why":"so the visit is recorded where it was",`, ``, `"where" asks for a location: say why`},
		{`"trigger":{"type":"manual"},"tool":{"command":"visit","name":"Add a site visit","about":"Record a visit to a site"},`, `"trigger":{"type":"form","config":{"title":"Visit"}},`, "A location in a workflow's inputs is one of its top-level inputs"},
	} {
		doc := strings.Replace(siteVisit, c.from, c.to, 1)
		if doc == siteVisit {
			t.Fatalf("%q not in the fixture", c.from)
		}
		if p := fmtProblems(wd.Validate([]byte(doc))); !strings.Contains(p, c.want) {
			t.Errorf("%s → %s: %s, want %q", c.from, c.to, p, c.want)
		}
	}
	// A customers' tool keeps refusing each of them.
	for _, kind := range []string{"photo", "signature"} {
		doc := strings.Replace(siteVisit, `"about":"Record a visit to a site"}`, `"about":"Record a visit to a site","for":"customers"}`, 1)
		doc = strings.Replace(doc, `"where":{"type":"location","why":"so the visit is recorded where it was","max_accuracy_m":200},`, ``, 1)
		doc = strings.Replace(doc, `"required":["where"],`, ``, 1)
		if p := fmtProblems(wd.Validate([]byte(doc))); !strings.Contains(p, "a tool for customers cannot ask for a "+kind) {
			t.Errorf("customers' %s: %s", kind, p)
		}
	}
}

func TestDeviceAnswerSchemas(t *testing.T) {
	d, err := wd.Load([]byte(handover))
	if err != nil {
		t.Fatal(err)
	}
	schema := d.Steps[0].Form.Schema
	ans := wd.AnswerSchema(schema)
	props := ans["properties"].(map[string]any)
	where := props["where"].(map[string]any)
	if where["type"] != "object" || props["note"].(map[string]any)["type"] != "string" {
		t.Fatalf("answer schema: %v", props)
	}
	// The schema written is untouched.
	if schema["properties"].(map[string]any)["where"].(map[string]any)["type"] != "location" {
		t.Fatal("the step's schema changed")
	}
	plain := wd.WithoutDeviceFields(schema)
	raw, _ := json.Marshal(plain)
	if strings.Contains(string(raw), "location") || strings.Contains(string(raw), `"where"`) || !strings.Contains(string(raw), `"note"`) {
		t.Fatalf("without device fields: %s", raw)
	}
	if _, ok := plain["required"]; ok {
		t.Fatalf("required kept device fields: %v", plain["required"])
	}
}

func TestFormToStarterRoundTrips(t *testing.T) {
	var to wd.FormTo
	if err := json.Unmarshal([]byte(`"starter"`), &to); err != nil || to.Kind() != "starter" {
		t.Fatalf("%+v %v", to, err)
	}
	raw, _ := json.Marshal(to)
	if string(raw) != `"starter"` {
		t.Fatalf("%s", raw)
	}
	to = wd.FormTo{Member: "=trigger.body.rider"}
	raw, _ = json.Marshal(to)
	if string(raw) != `{"member":"=trigger.body.rider"}` {
		t.Fatalf("%s", raw)
	}
}
