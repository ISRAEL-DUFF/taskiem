// Package fakemomo is a stand-in for MTN's Mobile Money Open API, built
// from MTN's public developer documentation (docs/integrations/mtnmomo.md)
// for the connector's tests: per-product subscription keys and tokens that
// expire, X-Target-Environment and currency checks, X-Reference-Id as a
// UUID that may be used once (409), request to pay and transfers that stay
// PENDING until the test calls Flush, the sandbox's documented test
// numbers, status reads, balances, account holder checks, and callbacks
// sent once (PUT) to X-Callback-Url, whose host must be the API user's.
package fakemomo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Outcomes of the sandbox's documented test numbers (any other number
// succeeds).
var outcomes = map[string][2]string{
	"46733123450": {"FAILED", "INTERNAL_PROCESSING_ERROR"},
	"46733123451": {"FAILED", "APPROVAL_REJECTED"},
	"46733123452": {"FAILED", "EXPIRED"},
	"46733123453": {"PENDING", ""}, // ongoing: never settles
	"46733123455": {"FAILED", "NOT_ENOUGH_FUNDS"},
}

// Server is a fake MoMo Open API.
type Server struct {
	*httptest.Server

	APIUser, APIKey string
	// Keys are the subscription keys per product.
	Keys         map[string]string
	Target       string // X-Target-Environment
	Currency     string
	CallbackHost string // the API user's providerCallbackHost
	TokenLife    int
	Now          func() time.Time
	Balance      string

	t        testing.TB
	mu       sync.Mutex
	seq      int
	tokens   map[string]tok // value -> product, expiry
	txs      map[string]*tx // product+path+ref
	lose     map[string]bool
	fail     map[string]reply
	Requests []Request
}

type tok struct {
	product string
	until   time.Time
}

type tx struct {
	path, ref, product   string
	amount, currency     string
	externalID, party    string
	partyKey, callback   string
	status, reason, ftid string
	notified             bool
}

type reply struct {
	status int
	body   any
}

// Request is one call the fake received.
type Request struct {
	Method, Path string
	Header       http.Header
	Body         map[string]any
}

// New starts a fake with sandbox-like settings.
func New(t testing.TB) *Server {
	t.Helper()
	//nolint:gosec // test credentials of a fake server
	s := &Server{t: t, APIUser: "00000000-0000-4000-8000-00000000a001", APIKey: "fake-api-key",
		Keys:   map[string]string{"collection": "sub-coll", "disbursement": "sub-disb", "remittance": "sub-remit"},
		Target: "sandbox", Currency: "EUR", CallbackHost: "", TokenLife: 3600, Now: time.Now, Balance: "1000.50",
		tokens: map[string]tok{}, txs: map[string]*tx{}, lose: map[string]bool{}, fail: map[string]reply{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// LoseNextAnswer makes the next POST to path act, then answer 500 as if
// the response were lost.
func (s *Server) LoseNextAnswer(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lose[path] = true
}

// FailNext answers the next call to path with status, code and message,
// without acting.
func (s *Server) FailNext(path string, status int, code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[path] = reply{status, map[string]any{"code": code, "message": message}}
}

// TokensIssued counts tokens issued.
func (s *Server) TokensIssued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for range s.tokens {
		n++
	}
	return n
}

// ExpireTokens revokes every token.
func (s *Server) ExpireTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.tokens {
		v.until = time.Time{}
		s.tokens[k] = v
	}
}

// Flush settles every pending request and sends each callback once, with
// PUT, to its X-Callback-Url.
func (s *Server) Flush(client *http.Client) {
	s.mu.Lock()
	var todo []*tx
	for _, t := range s.txs {
		if t.status == "PENDING" {
			o, ok := outcomes[t.party]
			if !ok {
				o = [2]string{"SUCCESSFUL", ""}
			}
			t.status, t.reason = o[0], o[1]
			if t.status == "SUCCESSFUL" {
				s.seq++
				t.ftid = strconv.Itoa(23503452 + s.seq)
			}
		}
		if t.status != "PENDING" && !t.notified && t.callback != "" {
			t.notified = true
			todo = append(todo, t)
		}
	}
	bodies := make([][]byte, len(todo))
	for i, t := range todo {
		bodies[i], _ = json.Marshal(t.view())
	}
	s.mu.Unlock()
	for i, t := range todo {
		req, _ := http.NewRequest(http.MethodPut, t.callback, bytes.NewReader(bodies[i]))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			s.t.Errorf("fakemomo: callback: %v", err)
			continue
		}
		_ = resp.Body.Close()
	}
}

func (t *tx) view() map[string]any {
	v := map[string]any{"amount": t.amount, "currency": t.currency, "externalId": t.externalID,
		t.partyKey: map[string]any{"partyIdType": "MSISDN", "partyId": t.party}, "payerMessage": "", "payeeNote": "", "status": t.status}
	if t.ftid != "" {
		v["financialTransactionId"] = t.ftid
	}
	if t.reason != "" {
		v["reason"] = map[string]any{"code": t.reason, "message": strings.ToLower(strings.ReplaceAll(t.reason, "_", " "))}
	}
	return v
}

func (s *Server) write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (s *Server) refuse(w http.ResponseWriter, status int, code, message string) {
	s.write(w, status, map[string]any{"code": code, "message": message})
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			s.refuse(w, http.StatusBadRequest, "", "bad request")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})

	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	product, rest := parts[0], ""
	if len(parts) > 1 {
		rest = "/" + parts[1]
	}
	key, ok := s.Keys[product]
	if !ok {
		s.refuse(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found")
		return
	}
	if r.Header.Get("Ocp-Apim-Subscription-Key") != key {
		s.write(w, http.StatusUnauthorized, map[string]any{"statusCode": 401, "message": "Access denied due to invalid subscription key. Make sure to provide a valid key for an active subscription."})
		return
	}
	if rest == "/token/" {
		s.issue(w, r, product)
		return
	}
	t, ok := s.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok || t.product != product || !s.Now().Before(t.until) {
		s.write(w, http.StatusUnauthorized, map[string]any{"error": "invalid_token"})
		return
	}
	if r.Header.Get("X-Target-Environment") != s.Target {
		s.refuse(w, http.StatusInternalServerError, "NOT_ALLOWED_TARGET_ENVIRONMENT", "Access to target environment is forbidden.")
		return
	}
	if f, ok := s.fail[rest]; ok {
		delete(s.fail, rest)
		s.write(w, f.status, f.body)
		return
	}
	switch {
	case r.Method == http.MethodGet && rest == "/v1_0/account/balance":
		s.write(w, http.StatusOK, map[string]any{"availableBalance": s.Balance, "currency": s.Currency})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/v1_0/accountholder/msisdn/"):
		s.accountHolder(w, strings.TrimPrefix(rest, "/v1_0/accountholder/msisdn/"))
	case r.Method == http.MethodPost && (product == "collection" && rest == "/v1_0/requesttopay" || product != "collection" && rest == "/v1_0/transfer"):
		lost := s.lose[rest]
		delete(s.lose, rest)
		rec := httptest.NewRecorder()
		s.create(rec, r, product, rest, body)
		if lost && rec.Code == http.StatusAccepted {
			s.refuse(w, http.StatusInternalServerError, "INTERNAL_PROCESSING_ERROR", "An internal error occurred while processing.")
			return
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	case r.Method == http.MethodGet && (strings.HasPrefix(rest, "/v1_0/requesttopay/") || strings.HasPrefix(rest, "/v1_0/transfer/")):
		i := strings.LastIndex(rest, "/")
		if !uuidV4.MatchString(rest[i+1:]) {
			s.refuse(w, http.StatusBadRequest, "", "incorrectly formatted reference id")
			return
		}
		t, ok := s.txs[product+rest[:i]+"/"+rest[i+1:]]
		if !ok {
			s.refuse(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Requested resource was not found.")
			return
		}
		s.write(w, http.StatusOK, t.view())
	default:
		s.refuse(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Requested resource was not found.")
	}
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request, product string) {
	user, pass, ok := r.BasicAuth()
	if r.Method != http.MethodPost || !ok || user != s.APIUser || pass != s.APIKey {
		s.write(w, http.StatusUnauthorized, map[string]any{"error": "login_failed"})
		return
	}
	s.seq++
	v := fmt.Sprintf("momo-%s-%d", product, s.seq)
	s.tokens[v] = tok{product, s.Now().Add(time.Duration(s.TokenLife) * time.Second)}
	s.write(w, http.StatusOK, map[string]any{"access_token": v, "token_type": "access_token", "expires_in": s.TokenLife})
}

func (s *Server) accountHolder(w http.ResponseWriter, rest string) {
	id, what, _ := strings.Cut(rest, "/")
	switch what {
	case "active":
		s.write(w, http.StatusOK, map[string]any{"result": id != "46733123450" && id != "46733123451"})
	case "basicuserinfo":
		if id == "46733123450" {
			s.refuse(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Requested resource was not found.")
			return
		}
		s.write(w, http.StatusOK, map[string]any{"given_name": "Sand", "family_name": "Box", "birthdate": "1976-08-13", "locale": "sv_SE", "gender": "MALE", "status": "ACTIVE"})
	default:
		s.refuse(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Requested resource was not found.")
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, product, path string, b map[string]any) {
	ref := r.Header.Get("X-Reference-Id")
	if !uuidV4.MatchString(ref) {
		s.refuse(w, http.StatusBadRequest, "", "X-Reference-Id must be a UUID version 4")
		return
	}
	partyKey := "payee"
	if product == "collection" {
		partyKey = "payer"
	}
	p, _ := b[partyKey].(map[string]any)
	amount, _ := b["amount"].(string)
	cur, _ := b["currency"].(string)
	if p == nil || p["partyIdType"] != "MSISDN" || amount == "" {
		s.refuse(w, http.StatusBadRequest, "", "invalid data was sent in the request")
		return
	}
	if _, err := strconv.ParseFloat(amount, 64); err != nil {
		s.refuse(w, http.StatusBadRequest, "", "invalid amount")
		return
	}
	for _, k := range []string{"payerMessage", "payeeNote"} {
		m, _ := b[k].(string)
		if len(m) > 160 || strings.Contains(m, "'") {
			s.refuse(w, http.StatusBadRequest, "", "invalid "+k)
			return
		}
	}
	cb := r.Header.Get("X-Callback-Url")
	if cb != "" {
		u, err := url.Parse(cb)
		if err != nil || u.Hostname() != s.CallbackHost || u.RawQuery != "" {
			s.refuse(w, http.StatusInternalServerError, "INVALID_CALLBACK_URL_HOST", "Callback URL with different host name to configured for API User.")
			return
		}
	}
	if cur != s.Currency {
		s.refuse(w, http.StatusInternalServerError, "INVALID_CURRENCY", "Currency not supported.")
		return
	}
	k := product + path + "/" + ref
	if _, dup := s.txs[k]; dup {
		s.refuse(w, http.StatusConflict, "RESOURCE_ALREADY_EXIST", "Duplicated reference id. Creation of resource failed.")
		return
	}
	party, _ := p["partyId"].(string)
	ext, _ := b["externalId"].(string)
	s.txs[k] = &tx{path: path, ref: ref, product: product, amount: amount, currency: cur, externalID: ext, party: party,
		partyKey: partyKey, callback: cb, status: "PENDING"}
	w.WriteHeader(http.StatusAccepted)
}
