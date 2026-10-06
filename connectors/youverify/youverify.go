// Package youverify is the Youverify connector (API v2): Nigerian identity
// checks, face comparison, business checks, physical address verification
// and AML screening. Built from Youverify's public documentation
// (docs/integrations/youverify.md).
//
// Identity checks answer HTTP 200 with data.status found, not_found or
// pending; not_found is a result (found: false), and pending means the
// source was down and the result will arrive as a webhook. Like the Dojah
// connector, photos and signatures are dropped unless asked for.
package youverify

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// SandboxURL is used for connections whose environment is "sandbox".
const SandboxURL = "https://api.sandbox.youverify.co"

// Options configure the connector; BaseURL overrides both environments
// (tests).
type Options struct {
	BaseURL string
}

// New returns the Youverify connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"verify_bvn":                   connector.ActionFunc(c.verifyBVN),
		"verify_nin":                   connector.ActionFunc(c.verifyNIN),
		"verify_drivers_license":       connector.ActionFunc(c.verifyLicence),
		"verify_phone":                 connector.ActionFunc(c.phone("/v2/api/identity/ng/phone")),
		"search_phone":                 connector.ActionFunc(c.phone("/v2/api/identity/ng/nin-phone")),
		"resolve_bank_account":         connector.ActionFunc(c.resolveBankAccount),
		"compare_faces":                connector.ActionFunc(c.compareFaces),
		"verify_business":              connector.ActionFunc(c.verifyBusiness),
		"search_businesses":            connector.ActionFunc(c.searchBusinesses),
		"get_business_details":         connector.ActionFunc(c.businessDetails),
		"screen_aml":                   connector.ActionFunc(c.screenAML),
		"get_aml_check":                connector.ActionFunc(c.getAML),
		"create_address_candidate":     connector.ActionFunc(c.createCandidate),
		"request_address_verification": connector.ActionFunc(c.requestAddress),
		"get_address_verification":     connector.ActionFunc(c.getAddress),
	}}
}

type client struct{ live, sandbox string }

func (c *client) base(req connector.Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox") {
		return c.sandbox
	}
	return c.live
}

// apiError is a refusal Youverify explained.
type apiError struct {
	status  int
	name    string
	message string
}

func (e *apiError) Error() string {
	if e.name != "" {
		return fmt.Sprintf("youverify %d (%s): %s", e.status, e.name, e.message)
	}
	return fmt.Sprintf("youverify %d: %s", e.status, e.message)
}

func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

// do sends one request and returns the envelope's data.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body any) (any, error) {
	key := strings.TrimSpace(req.Credentials["secret_key"])
	if key == "" {
		return nil, fmt.Errorf("youverify: the connection has no secret_key: %w", effects.ErrFatal)
	}
	var raw json.RawMessage
	err := connector.DoJSON(ctx, req.HTTP, method, c.base(req)+path, map[string]string{"token": key}, body, &raw)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var e struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(he.Body, &e)
		ae := &apiError{status: he.Status, name: e.Name, message: e.Message}
		if ae.message == "" {
			ae.message = strings.TrimSpace(string(he.Body))
		}
		// Keep the transport's classification (429 and 503 retryable, other
		// 5xx unknown outcome, 4xx fatal: 402 is an empty wallet) and add
		// Youverify's explanation.
		return nil, fmt.Errorf("%w: %w", ae, he)
	}
	if err != nil {
		return nil, err
	}
	var env struct {
		Success *bool `json:"success"`
		Message string
		Name    string
		Data    any `json:"data"`
	}
	if err := decode(raw, &env); err != nil {
		return nil, fmt.Errorf("youverify: unreadable response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if env.Success != nil && !*env.Success {
		// A 2xx that says it failed: undocumented. For a write the outcome
		// is unknown.
		return nil, fmt.Errorf("%w: %w", &apiError{status: http.StatusOK, name: env.Name, message: env.Message}, effects.ErrUnknownOutcome)
	}
	return plain(env.Data), nil
}

// notFound turns a 404 on a lookup by id into a clear fatal error.
func notFound(err error, what string) error {
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusNotFound {
		return fmt.Errorf("youverify: no such %s (%s): %w", what, ae.message, effects.ErrFatal)
	}
	return err
}

// plain converts json.Number to int64 or float64.
func plain(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = plain(e)
		}
	case []any:
		for i, e := range x {
			x[i] = plain(e)
		}
	}
	return v
}

func asObj(v any) map[string]any {
	o, _ := v.(map[string]any)
	if o == nil {
		return map[string]any{}
	}
	return o
}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
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

func number(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func fatalf(format string, args ...any) error {
	return fmt.Errorf("youverify: "+format+": %w", append(args, effects.ErrFatal)...)
}

func need(in map[string]any, keys ...string) error {
	for _, k := range keys {
		if strings.TrimSpace(str(in, k)) == "" {
			return fatalf("%s is required", k)
		}
	}
	return nil
}

// consent: Youverify requires the subject's consent on every check; the
// step must assert it rather than the connector assuming it.
func consent(in map[string]any) error {
	if c, _ := in["subject_consent"].(bool); !c {
		return fatalf("subject_consent must be true: Youverify checks need the subject's consent")
	}
	return nil
}

var imageKeys = map[string]bool{"image": true, "signature": true, "photo": true, "images": true}

// strip removes images from a record.
func strip(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if !imageKeys[k] {
				out[k] = strip(e)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = strip(e)
		}
		return out
	}
	return v
}

// identity sends one identity check and normalises the record.
func (c *client) identity(ctx context.Context, req connector.Request, path string, body map[string]any) (connector.Response, error) {
	body["isSubjectConsent"] = true
	v, err := c.do(ctx, req, http.MethodPost, path, body)
	if err != nil {
		return connector.Response{}, err
	}
	d := asObj(v)
	status := str(d, "status")
	switch status {
	case "found", "not_found", "pending":
	case "failed":
		return connector.Response{}, fmt.Errorf("youverify: the check could not be completed (%s): %w", str(d, "reason"), effects.ErrRetryable)
	default:
		return connector.Response{}, fmt.Errorf("youverify: undocumented check status %q: %w", status, effects.ErrFatal)
	}
	addr := asObj(d["address"])
	address := str(addr, "addressLine")
	if address == "" {
		address = str(d, "address")
	}
	out := map[string]any{"found": status == "found", "status": status, "id": str(d, "id"), "reason": str(d, "reason"),
		"first_name": str(d, "firstName"), "middle_name": str(d, "middleName"), "last_name": str(d, "lastName"),
		"date_of_birth": str(d, "dateOfBirth"), "gender": str(d, "gender"), "mobile": str(d, "mobile"), "email": str(d, "email"),
		"address": address, "all_validation_passed": flag(d, "allValidationPassed"), "data_validation": flag(d, "dataValidation"),
		"selfie_validation": flag(d, "selfieValidation"), "record": strip(d)}
	if inc, _ := req.Input["include_image"].(bool); inc {
		out["image"] = str(d, "image")
	}
	return connector.Response{Output: out}, nil
}

// validations builds Youverify's data-validation object from the step.
func validations(in map[string]any) map[string]any {
	data := map[string]any{}
	for k, f := range map[string]string{"firstName": "first_name", "lastName": "last_name", "dateOfBirth": "date_of_birth"} {
		if v := str(in, f); v != "" {
			data[k] = v
		}
	}
	return data
}

func (c *client) verifyBVN(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := consent(in); err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "bvn"); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"id": str(in, "bvn")}
	if p, _ := in["premium"].(bool); p {
		body["premiumBVN"] = true
	}
	// The BVN reference names this object "validation" (the NIN one
	// "validations"); sent as documented.
	if d := validations(in); len(d) > 0 {
		body["validation"] = map[string]any{"data": d}
	}
	return c.identity(ctx, req, "/v2/api/identity/ng/bvn", body)
}

func (c *client) verifyNIN(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := consent(in); err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "nin"); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"id": str(in, "nin")}
	if p, _ := in["premium"].(bool); p {
		body["premiumNIN"] = true
	}
	v := map[string]any{}
	if d := validations(in); len(d) > 0 {
		v["data"] = d
	}
	if s := str(in, "selfie_image"); s != "" {
		v["selfie"] = map[string]any{"image": s}
	}
	if len(v) > 0 {
		body["validations"] = v
	}
	return c.identity(ctx, req, "/v2/api/identity/ng/nin", body)
}

func (c *client) verifyLicence(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := consent(req.Input); err != nil {
		return connector.Response{}, err
	}
	if err := need(req.Input, "license_number"); err != nil {
		return connector.Response{}, err
	}
	return c.identity(ctx, req, "/v2/api/identity/ng/drivers-license", map[string]any{"id": str(req.Input, "license_number")})
}

func (c *client) phone(path string) func(context.Context, connector.Request) (connector.Response, error) {
	return func(ctx context.Context, req connector.Request) (connector.Response, error) {
		if err := consent(req.Input); err != nil {
			return connector.Response{}, err
		}
		if err := need(req.Input, "phone"); err != nil {
			return connector.Response{}, err
		}
		return c.identity(ctx, req, path, map[string]any{"mobile": str(req.Input, "phone")})
	}
}

func (c *client) resolveBankAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := consent(in); err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "account_number", "bank_code"); err != nil {
		return connector.Response{}, err
	}
	v, err := c.do(ctx, req, http.MethodPost, "/v2/api/identity/ng/bank-account-number/resolve",
		map[string]any{"accountNumber": str(in, "account_number"), "bankCode": str(in, "bank_code"), "isSubjectConsent": true})
	if err != nil {
		return connector.Response{}, err
	}
	d := asObj(v)
	b := asObj(d["bankDetails"])
	return connector.Response{Output: map[string]any{"found": str(d, "status") == "found", "id": str(d, "id"), "account_name": str(b, "accountName"),
		"account_number": str(b, "accountNumber"), "bank_name": str(b, "bankName")}}, nil
}

func (c *client) compareFaces(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := consent(in); err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "image1", "image2"); err != nil {
		return connector.Response{}, err
	}
	v, err := c.do(ctx, req, http.MethodPost, "/v2/api/identity/compare-image",
		map[string]any{"image1": str(in, "image1"), "image2": str(in, "image2"), "isSubjectConsent": true})
	if err != nil {
		return connector.Response{}, err
	}
	d := asObj(v)
	cmp := asObj(d["imageComparison"])
	return connector.Response{Output: map[string]any{"id": str(d, "id"), "match": flag(cmp, "match"), "confidence": number(cmp["confidenceLevel"]),
		"threshold": number(cmp["threshold"]), "reason": str(d, "reason")}}, nil
}

func business(d map[string]any) map[string]any {
	return map[string]any{"found": str(d, "status") == "found", "status": str(d, "status"), "id": str(d, "id"), "name": str(d, "name"),
		"registration_number": str(d, "registrationNumber"), "company_status": str(d, "companyStatus"), "type_of_entity": str(d, "typeOfEntity"),
		"registration_date": str(d, "registrationDate"), "nature_of_business": str(d, "natureOfBusiness"), "record": strip(d)}
}

func (c *client) verifyBusiness(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := consent(req.Input); err != nil {
		return connector.Response{}, err
	}
	if err := need(req.Input, "registration_number"); err != nil {
		return connector.Response{}, err
	}
	v, err := c.do(ctx, req, http.MethodPost, "/v2/api/verifications/ng/company/basic",
		map[string]any{"registrationNumber": str(req.Input, "registration_number"), "isConsent": true})
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: business(asObj(v))}, nil
}

func (c *client) searchBusinesses(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := need(in, "query"); err != nil {
		return connector.Response{}, err
	}
	q := url.Values{"query": {str(in, "query")}}
	if cc := str(in, "country_code"); cc != "" {
		q.Set("countryCode", cc)
	}
	if l, ok := in["limit"]; ok && l != nil {
		n := number(plain(l))
		if n < 1 || n > 100 || n != float64(int64(n)) {
			return connector.Response{}, fatalf("limit must be 1 to 100")
		}
		q.Set("limit", strconv.FormatInt(int64(n), 10))
	}
	v, err := c.do(ctx, req, http.MethodGet, "/v2/api/verifications/global/search-companies?"+q.Encode(), nil)
	if err != nil {
		return connector.Response{}, err
	}
	list, _ := v.([]any)
	out := []any{}
	for _, e := range list {
		b := asObj(e)
		out = append(out, map[string]any{"name": str(b, "name"), "company_number": str(b, "companyNumber"),
			"country_code": str(b, "countryCode"), "country_name": str(b, "countryName")})
	}
	return connector.Response{Output: map[string]any{"businesses": out}}, nil
}

func (c *client) businessDetails(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := need(req.Input, "verification_id"); err != nil {
		return connector.Response{}, err
	}
	v, err := c.do(ctx, req, http.MethodGet, "/v2/api/verifications/ng/company-details/"+url.PathEscape(str(req.Input, "verification_id")), nil)
	if err != nil {
		return connector.Response{}, notFound(err, "business verification")
	}
	return connector.Response{Output: business(asObj(v))}, nil
}

func aml(d map[string]any) map[string]any {
	return map[string]any{"id": str(d, "id"), "status": str(d, "status"), "total_hits": int64(number(d["totalEntity"])),
		"category_count": asObj(d["categoryCount"]), "record": strip(d)}
}

func (c *client) screenAML(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := consent(in); err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "query"); err != nil {
		return connector.Response{}, err
	}
	typ := str(in, "type")
	if typ == "" {
		typ = "individual"
	}
	body := map[string]any{"query": str(in, "query"), "type": typ, "isSubjectConsent": true}
	if s, ok := in["strict"].(bool); ok && s {
		body["strictMode"] = "true"
	}
	adv := map[string]any{}
	for k, f := range map[string]string{"firstName": "first_name", "lastName": "last_name", "country": "country", "gender": "gender"} {
		if v := str(in, f); v != "" {
			adv[k] = v
		}
	}
	if len(adv) > 0 {
		body["advancedSearch"] = adv
	}
	v, err := c.do(ctx, req, http.MethodPost, "/v2/api/verifications/advanced/name/aml-checks", body)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: aml(asObj(v))}, nil
}

func (c *client) getAML(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := need(req.Input, "verification_id"); err != nil {
		return connector.Response{}, err
	}
	v, err := c.do(ctx, req, http.MethodGet, "/v2/api/verifications/aml-checks/"+url.PathEscape(str(req.Input, "verification_id")), nil)
	if err != nil {
		return connector.Response{}, notFound(err, "AML screening")
	}
	return connector.Response{Output: aml(asObj(v))}, nil
}

func (c *client) createCandidate(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := need(in, "first_name", "last_name", "mobile", "image"); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{}
	for k, f := range map[string]string{"firstName": "first_name", "middleName": "middle_name", "lastName": "last_name", "mobile": "mobile",
		"dateOfBirth": "date_of_birth", "email": "email", "image": "image"} {
		if v := str(in, f); v != "" {
			body[k] = v
		}
	}
	v, err := c.do(ctx, req, http.MethodPost, "/v2/api/addresses/candidates", body)
	if err != nil {
		return connector.Response{}, err
	}
	id := str(asObj(v), "id")
	if id == "" {
		return connector.Response{}, fmt.Errorf("youverify: candidate created without an id: %w", effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"candidate_id": id}}, nil
}

func addressOutput(d map[string]any) map[string]any {
	ref := str(d, "referenceId")
	if ref == "" {
		ref = str(d, "verificationId")
	}
	return map[string]any{"id": str(d, "id"), "reference_id": ref, "status": str(d, "status"), "task_status": str(d, "taskStatus"),
		"flagged": flag(d, "isFlagged"), "download_url": str(d, "downloadUrl"), "completed_at": str(d, "completedAt")}
}

func (c *client) requestAddress(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := consent(in); err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "candidate_id", "building_number", "street", "landmark", "city", "state"); err != nil {
		return connector.Response{}, err
	}
	addr := map[string]any{}
	for k, f := range map[string]string{"flatNumber": "flat_number", "buildingName": "building_name", "buildingNumber": "building_number",
		"street": "street", "subStreet": "sub_street", "landmark": "landmark", "city": "city", "lga": "lga", "state": "state"} {
		if v := str(in, f); v != "" {
			addr[k] = v
		}
	}
	body := map[string]any{"candidateId": str(in, "candidate_id"), "subjectConsent": true, "address": addr}
	if d := str(in, "description"); d != "" {
		body["description"] = d
	}
	if m, ok := in["metadata"].(map[string]any); ok {
		body["metadata"] = m
	}
	v, err := c.do(ctx, req, http.MethodPost, "/v2/api/addresses/individual/request", body)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: addressOutput(asObj(v))}, nil
}

func (c *client) getAddress(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := need(req.Input, "id"); err != nil {
		return connector.Response{}, err
	}
	v, err := c.do(ctx, req, http.MethodGet, "/v2/api/addresses/"+url.PathEscape(str(req.Input, "id")), nil)
	if err != nil {
		return connector.Response{}, notFound(err, "address verification")
	}
	return connector.Response{Output: addressOutput(asObj(v))}, nil
}
