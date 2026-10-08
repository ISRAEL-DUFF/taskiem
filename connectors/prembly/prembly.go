// Package prembly is the Prembly IdentityPass connector: Nigerian identity
// lookups (BVN, NIN, phone, driver's licence, passport, voter's card, CAC,
// bank account) and face comparison and liveness. Built from Prembly's
// public documentation (docs/integrations/prembly.md).
//
// Prembly answers most lookups with HTTP 200 and a response_code: 00 found,
// 01 no record, 02 try later, 03 wallet empty, 07 blocked (BVN) or
// suspended (NIN). "No record" and "blocked" are results (found: false), not
// failures. Like the Dojah connector, photos and signatures are dropped
// unless the step asks for them.
package prembly

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector. BaseURL replaces api.prembly.com (tests);
// Prembly's sandbox shares the live host and is chosen by the key.
type Options struct {
	BaseURL string
}

// lookup is one person-lookup endpoint: the body fields it sends, from
// which step inputs.
type lookup struct {
	path   string
	fields map[string]string // Prembly field -> input
	ints   map[string]bool   // Prembly fields sent as JSON numbers
}

var lookups = map[string]lookup{
	"lookup_bvn":             {path: "/verification/bvn", fields: map[string]string{"number": "bvn"}},
	"lookup_bvn_basic":       {path: "/verification/bvn_validation", fields: map[string]string{"number": "bvn"}},
	"verify_bvn_with_face":   {path: "/verification/bvn_w_face", fields: map[string]string{"number": "bvn", "image": "image"}},
	"lookup_bvn_by_phone":    {path: "/verification/bvn_with_phone_advance", fields: map[string]string{"phone_number": "phone_number"}},
	"lookup_nin":             {path: "/verification/vnin", fields: map[string]string{"number_nin": "nin"}},
	"lookup_nin_basic":       {path: "/verification/vnin-basic", fields: map[string]string{"number": "nin"}},
	"verify_nin_with_face":   {path: "/verification/nin_w_face", fields: map[string]string{"number_nin": "nin", "image": "image", "date_of_birth": "date_of_birth"}, ints: map[string]bool{"number_nin": true}},
	"lookup_phone":           {path: "/verification/phone_number/advance", fields: map[string]string{"number": "phone_number"}},
	"lookup_phone_basic":     {path: "/verification/phone_number", fields: map[string]string{"number": "phone_number"}},
	"verify_drivers_license": {path: "/verification/drivers_license/advance/v2", fields: map[string]string{"number": "license_number", "first_name": "first_name", "last_name": "last_name"}},
	"verify_passport":        {path: "/verification/national_passport_v2", fields: map[string]string{"number": "passport_number", "dob": "date_of_birth", "nin": "nin"}},
	"verify_voters_card":     {path: "/verification/voters_card", fields: map[string]string{"number": "vin", "last_name": "last_name", "dob": "date_of_birth", "state": "state", "lga": "lga"}},
}

// New returns the Prembly connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/")}
	actions := map[string]connector.Action{
		"lookup_cac":           connector.ActionFunc(c.lookupCAC),
		"resolve_bank_account": connector.ActionFunc(c.resolveBankAccount),
		"compare_faces":        connector.ActionFunc(c.compareFaces),
		"check_liveness":       connector.ActionFunc(c.checkLiveness),
		"get_wallet_balance":   connector.ActionFunc(c.walletBalance),
	}
	for name, l := range lookups {
		actions[name] = connector.ActionFunc(c.person(l))
	}
	return &connector.Connector{Manifest: m, Actions: actions}
}

type client struct{ base string }

// apiError is a refusal Prembly explained.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("prembly %d: %s", e.status, e.message) }

// detail renders Prembly's "detail" or "message", which may be a string or
// a map of field errors.
func detail(m map[string]any) string {
	for _, k := range []string{"detail", "message"} {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			raw, _ := json.Marshal(v)
			return string(raw)
		}
	}
	return ""
}

func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

// do sends one request and returns the decoded body.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body any) (map[string]any, error) {
	key := strings.TrimSpace(req.Credentials["api_key"])
	if key == "" {
		return nil, fmt.Errorf("prembly: the connection has no api_key: %w", effects.ErrFatal)
	}
	var raw json.RawMessage
	err := connector.DoJSON(ctx, req.HTTP, method, c.base+path, map[string]string{"x-api-key": key}, body, &raw)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var m map[string]any
		_ = json.Unmarshal(he.Body, &m)
		ae := &apiError{status: he.Status, message: detail(m)}
		if ae.message == "" {
			ae.message = strings.TrimSpace(string(he.Body))
		}
		// Keep the transport's classification (429 and 503 retryable, other
		// 5xx unknown outcome, 4xx fatal) and add Prembly's explanation.
		return nil, fmt.Errorf("%w: %w", ae, he)
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := decode(raw, &out); err != nil {
			return nil, fmt.Errorf("prembly: unreadable response: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return out, nil
}

// outcome reads Prembly's response_code. It returns found, blocked, or an
// error for codes that are not a result.
func outcome(m map[string]any) (found, blocked bool, err error) {
	code := str(m, "response_code")
	switch code {
	case "00":
		return true, false, nil
	case "01":
		return false, false, nil
	case "07":
		return false, true, nil
	case "02":
		return false, false, fmt.Errorf("prembly: verification can't be completed now (code 02): %s: %w", detail(m), effects.ErrRetryable)
	case "03":
		return false, false, fmt.Errorf("prembly: insufficient wallet balance (code 03); top up the Prembly wallet: %w", effects.ErrFatal)
	case "":
		if flag(m, "status") {
			return true, false, nil
		}
	}
	// Undocumented: retrying a billed lookup blindly could charge again, so
	// a person looks at it.
	return false, false, fmt.Errorf("prembly: undocumented response (code %q): %s: %w", code, detail(m), effects.ErrFatal)
}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}

func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	if o == nil {
		return map[string]any{}
	}
	return o
}

func flag(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

func num(v any) float64 {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// first returns the first non-empty string among keys.
func first(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := strings.TrimSpace(str(m, k)); s != "" {
			return s
		}
	}
	return ""
}

// imageKeys hold photos, signatures and face crops.
var imageKeys = map[string]bool{"base64Image": true, "photo": true, "image": true, "signature": true, "face_image_provided": true,
	"images_from_id": true, "images_from_frontend": true}

// strip removes images from a record, recursively, converting numbers.
func strip(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if imageKeys[k] {
				continue
			}
			out[k] = strip(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = strip(e)
		}
		return out
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	}
	return v
}

// record finds the identity record, which Prembly names differently per
// endpoint (data, nin_data, frsc_data, ...).
func record(m map[string]any) map[string]any {
	for _, k := range []string{"data", "nin_data", "frsc_data", "bvn_data", "vc_data", "passport_data"} {
		if o, ok := m[k].(map[string]any); ok && len(o) > 0 {
			return o
		}
	}
	for k, v := range m {
		if o, ok := v.(map[string]any); ok && strings.HasSuffix(k, "_data") && k != "face_data" && len(o) > 0 {
			return o
		}
	}
	return map[string]any{}
}

func required(in map[string]any, keys ...string) error {
	for _, k := range keys {
		if strings.TrimSpace(str(in, k)) == "" {
			return fmt.Errorf("prembly: %s is required: %w", k, effects.ErrFatal)
		}
	}
	return nil
}

// person runs one identity lookup and normalises the record.
func (c *client) person(l lookup) func(context.Context, connector.Request) (connector.Response, error) {
	return func(ctx context.Context, req connector.Request) (connector.Response, error) {
		body := map[string]any{}
		for field, input := range l.fields {
			v := strings.TrimSpace(str(req.Input, input))
			if v == "" {
				continue
			}
			if l.ints[field] {
				if _, err := strconv.ParseInt(v, 10, 64); err != nil {
					return connector.Response{}, fmt.Errorf("prembly: %s must be digits: %w", input, effects.ErrFatal)
				}
				body[field] = json.Number(v)
				continue
			}
			body[field] = v
		}
		if _, ok := body["number"]; !ok {
			if _, ok := body["number_nin"]; !ok {
				if _, ok := body["phone_number"]; !ok {
					return connector.Response{}, fmt.Errorf("prembly: the identity number is required: %w", effects.ErrFatal)
				}
			}
		}
		m, err := c.do(ctx, req, http.MethodPost, l.path, body)
		if err != nil {
			return connector.Response{}, err
		}
		found, blocked, err := outcome(m)
		if err != nil {
			return connector.Response{}, err
		}
		d := record(m)
		ver := obj(m, "verification")
		face := obj(m, "face_data")
		if len(face) == 0 {
			face = obj(d, "face_data")
		}
		out := map[string]any{
			"found": found, "blocked": blocked, "verification_status": str(ver, "status"), "verified": str(ver, "status") == "VERIFIED",
			"message": detail(m), "reference": str(ver, "reference"),
			"first_name":    first(d, "firstName", "firstname", "first_name"),
			"middle_name":   first(d, "middleName", "middlename", "middle_name", "otherName"),
			"last_name":     first(d, "lastName", "surname", "lastname", "last_name"),
			"date_of_birth": first(d, "dateOfBirth", "birthdate", "birthDate", "dob", "date_of_birth"),
			"gender":        first(d, "gender"),
			"phone_number":  first(d, "phoneNumber1", "phoneNumber", "telephoneno", "phone_number", "mobile"),
			"email":         first(d, "email", "emailId"),
			"address":       first(d, "residentialAddress", "residence_address", "address"),
			"bvn":           first(d, "bvn"), "nin": first(d, "nin"),
			"watchlisted": flag(d, "watchListed"),
			"record":      strip(d),
		}
		if len(face) > 0 {
			out["face_match"] = flag(face, "status") && str(face, "response_code") == "00"
			out["face_confidence"] = num(face["confidence"])
		}
		if inc, _ := req.Input["include_image"].(bool); inc {
			out["image"] = first(d, "base64Image", "photo", "image")
		}
		return connector.Response{Output: out}, nil
	}
}

func (c *client) lookupCAC(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required(in, "rc_number"); err != nil {
		return connector.Response{}, err
	}
	typ := str(in, "company_type")
	if typ == "" {
		typ = "RC"
	}
	body := map[string]any{"rc_number": str(in, "rc_number"), "company_type": typ}
	if n := str(in, "company_name"); n != "" {
		body["company_name"] = n
	}
	path := "/verification/cac/basic"
	if adv, _ := in["advanced"].(bool); adv {
		path = "/verification/cac/advance"
	}
	m, err := c.do(ctx, req, http.MethodPost, path, body)
	if err != nil {
		return connector.Response{}, err
	}
	found, _, err := outcome(m)
	if err != nil {
		return connector.Response{}, err
	}
	d, ver := record(m), obj(m, "verification")
	return connector.Response{Output: map[string]any{"found": found, "verified": str(ver, "status") == "VERIFIED", "verification_status": str(ver, "status"),
		"message": detail(m), "reference": str(ver, "reference"), "company_name": first(d, "company_name", "companyName"),
		"rc_number": first(d, "rc_number", "rcNumber"), "company_type": first(d, "company_type", "companyType"),
		"company_status": first(d, "company_status", "companyStatus"), "registration_date": first(d, "registrationDate", "date_of_registration"),
		"address": first(d, "company_address", "address", "headOfficeAddress"), "email": first(d, "email_address", "email"), "record": strip(d)}}, nil
}

func (c *client) resolveBankAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := required(req.Input, "account_number", "bank_code"); err != nil {
		return connector.Response{}, err
	}
	m, err := c.do(ctx, req, http.MethodPost, "/verification/bank_account/basic",
		map[string]any{"number": str(req.Input, "account_number"), "bank_code": str(req.Input, "bank_code")})
	if err != nil {
		return connector.Response{}, err
	}
	found, _, err := outcome(m)
	if err != nil {
		return connector.Response{}, err
	}
	d := obj(m, "account data") // Prembly's key has a space
	if len(d) == 0 {
		d = record(m)
	}
	name := str(d, "account_name")
	return connector.Response{Output: map[string]any{"found": found && name != "", "account_number": first(d, "account_number"),
		"account_name": name, "message": detail(m)}}, nil
}

func (c *client) compareFaces(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := required(req.Input, "image_one", "image_two"); err != nil {
		return connector.Response{}, err
	}
	m, err := c.do(ctx, req, http.MethodPost, "/verification/biometrics/face/comparison",
		map[string]any{"image_one": str(req.Input, "image_one"), "image_two": str(req.Input, "image_two")})
	if err != nil {
		return connector.Response{}, err
	}
	// Prembly documents only the match response; any other answer that is
	// not a service or wallet error is a non-match.
	found, err := biometric(m)
	if err != nil {
		return connector.Response{}, err
	}
	conf := num(m["confidence"])
	if conf == 0 {
		conf = num(obj(m, "data")["confidence"])
	}
	return connector.Response{Output: map[string]any{"match": found, "confidence": conf, "message": detail(m)}}, nil
}

// biometric reads a face check: codes 02 and 03 are errors, 00 (or none,
// with status true) a positive result, anything else a negative one.
func biometric(m map[string]any) (bool, error) {
	switch code := str(m, "response_code"); code {
	case "02", "03":
		_, _, err := outcome(m)
		return false, err
	case "00", "":
		return flag(m, "status"), nil
	}
	return false, nil
}

func (c *client) checkLiveness(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := required(req.Input, "image"); err != nil {
		return connector.Response{}, err
	}
	m, err := c.do(ctx, req, http.MethodPost, "/verification/biometrics/face/liveliness_check", map[string]any{"image": str(req.Input, "image")})
	if err != nil {
		return connector.Response{}, err
	}
	found, err := biometric(m)
	if err != nil {
		return connector.Response{}, err
	}
	// The confidence is a percentage at the top level in one example and in
	// data in another.
	conf := num(m["confidence_in_percentage"])
	if conf == 0 {
		conf = num(obj(m, "data")["confidence_in_percentage"])
	}
	return connector.Response{Output: map[string]any{"live": found, "confidence": conf, "message": detail(m),
		"reference": str(obj(m, "verification"), "reference")}}, nil
}

func (c *client) walletBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	m, err := c.do(ctx, req, http.MethodGet, "/api/v1/wallet", nil)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"wallet": strip(m)}}, nil
}
