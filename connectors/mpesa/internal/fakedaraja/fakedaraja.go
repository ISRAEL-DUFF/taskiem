// Package fakedaraja is a stand-in for Safaricom's Daraja APIs, built from
// Safaricom's public documentation (docs/integrations/mpesa.md) for the
// connector's tests: tokens that expire and replace one another, STK push
// with the password check and a customer who answers later, STK query,
// C2B URL registration, B2C with OriginatorConversationID deduplication,
// transaction status, account balance and reversals, each result posted to
// the callback URLs in the request when the test calls Flush.
package fakedaraja

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Server is a fake Daraja.
type Server struct {
	*httptest.Server

	ConsumerKey, ConsumerSecret string
	Shortcode, Passkey          string
	// InitiatorPassword is what security credentials must decrypt to with
	// Key; Credential, when set, is also accepted as is.
	InitiatorPassword string
	Key               *rsa.PrivateKey
	Credential        string
	// Production refuses a second C2B URL registration, as live Daraja does.
	Production bool
	// TokenLife is the expires_in of issued tokens (default 3599).
	TokenLife int
	// Now is the fake's clock (token expiry, STK timestamps).
	Now func() time.Time
	// Customer decides how a phone answers an STK prompt: a ResultCode
	// (0 paid, 1032 cancelled, 1 insufficient funds). Default 0.
	Customer map[string]int

	t  testing.TB
	mu sync.Mutex
	// tokens issued, newest last; only the newest is valid (Daraja: each
	// request invalidates the previous token).
	tokens   []issued
	stk      map[string]*stk
	seen     map[string]bool // OriginatorConversationIDs
	urls     bool
	pending  []callback
	lose     map[string]bool // paths whose next answer is lost after acting
	fail     map[string]reply
	Requests []Request
	seq      int
}

type issued struct {
	value string
	until time.Time
}

type stk struct {
	phone, amount, code, checkout, merchant, callback string
	answered                                          bool
	result                                            int
}

type callback struct {
	url  string
	body any
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

// New starts a fake Daraja with a fresh RSA key for security credentials.
func New(t testing.TB) *Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{t: t, ConsumerKey: "ck_test", ConsumerSecret: "cs_test", Shortcode: "174379", Passkey: "pk_test",
		InitiatorPassword: "Safaricom999!*!", Key: key, TokenLife: 3599, Now: time.Now, Customer: map[string]int{},
		stk: map[string]*stk{}, seen: map[string]bool{}, lose: map[string]bool{}, fail: map[string]reply{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// LoseNextAnswer makes the next call to path act, then answer 500 as if
// the response were lost on the way.
func (s *Server) LoseNextAnswer(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lose[path] = true
}

// FailNext answers the next call to path with status and body, without
// acting.
func (s *Server) FailNext(path string, status int, code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[path] = reply{status, map[string]any{"requestId": "fake-req", "errorCode": code, "errorMessage": message}}
}

// RevokeTokens forgets every issued token (another process asked for one).
func (s *Server) RevokeTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = nil
}

// TokensIssued counts token requests answered.
func (s *Server) TokensIssued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// Pending is the number of callbacks waiting for Flush.
func (s *Server) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Flush answers every open STK prompt and posts every pending callback
// with client, in order (TimeoutNext turns one into a QueueTimeOutURL
// notification first).
func (s *Server) Flush(client *http.Client) {
	s.mu.Lock()
	for _, p := range s.stk {
		if !p.answered {
			p.answered, p.result = true, s.Customer[p.phone]
			s.pending = append(s.pending, callback{p.callback, p.callbackBody()})
		}
	}
	todo := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, cb := range todo {
		raw, _ := json.Marshal(cb.body)
		resp, err := client.Post(cb.url, "application/json", bytes.NewReader(raw))
		if err != nil {
			s.t.Errorf("fakedaraja: callback to %s: %v", cb.url, err)
			continue
		}
		_ = resp.Body.Close()
	}
}

func (p *stk) callbackBody() map[string]any {
	cb := map[string]any{"MerchantRequestID": p.merchant, "CheckoutRequestID": p.checkout, "ResultCode": p.result}
	switch p.result {
	case 0:
		amt, _ := strconv.Atoi(p.amount)
		cb["ResultDesc"] = "The service request is processed successfully."
		cb["CallbackMetadata"] = map[string]any{"Item": []any{
			map[string]any{"Name": "Amount", "Value": float64(amt)},
			map[string]any{"Name": "MpesaReceiptNumber", "Value": "TJK" + p.checkout[len(p.checkout)-7:]},
			map[string]any{"Name": "TransactionDate", "Value": 20261007101520},
			map[string]any{"Name": "PhoneNumber", "Value": json.Number(p.phone)},
		}}
	case 1032:
		cb["ResultDesc"] = "Request cancelled by user"
	default:
		cb["ResultDesc"] = "The balance is insufficient for the transaction."
	}
	return map[string]any{"Body": map[string]any{"stkCallback": cb}}
}

func (s *Server) write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) refuse(w http.ResponseWriter, status int, code, message string) {
	s.write(w, status, map[string]any{"requestId": "fake-req", "errorCode": code, "errorMessage": message})
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			s.refuse(w, http.StatusBadRequest, "400.002.05", "Invalid Request Payload")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})

	if r.URL.Path == "/oauth/v1/generate" {
		s.issue(w, r)
		return
	}
	if r.Method != http.MethodPost {
		s.refuse(w, http.StatusMethodNotAllowed, "405.001", r.Method+" Method Not Allowed")
		return
	}
	if !s.authorized(r) {
		s.refuse(w, http.StatusNotFound, "404.001.03", "Invalid Access Token")
		return
	}
	if f, ok := s.fail[r.URL.Path]; ok {
		delete(s.fail, r.URL.Path)
		s.write(w, f.status, f.body)
		return
	}
	lost := s.lose[r.URL.Path]
	delete(s.lose, r.URL.Path)
	rec := httptest.NewRecorder()
	switch r.URL.Path {
	case "/mpesa/stkpush/v1/processrequest":
		s.stkPush(rec, body)
	case "/mpesa/stkpushquery/v1/query":
		s.stkQuery(rec, body)
	case "/mpesa/c2b/v2/registerurl":
		s.register(rec, body)
	case "/mpesa/b2c/v3/paymentrequest":
		s.b2c(rec, body)
	case "/mpesa/transactionstatus/v1/query":
		s.async(rec, body, "TransactionStatusQuery", s.statusResult)
	case "/mpesa/accountbalance/v1/query":
		s.async(rec, body, "AccountBalance", s.balanceResult)
	case "/mpesa/reversal/v1/request":
		s.async(rec, body, "TransactionReversal", s.reversalResult)
	default:
		s.refuse(rec, http.StatusNotFound, "404.001.01", "Resource not found")
	}
	if lost && rec.Code < 300 {
		s.refuse(w, http.StatusInternalServerError, "500.003.1001", "Internal Server Error")
		return
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.refuse(w, http.StatusMethodNotAllowed, "405.001", "Method Not Allowed")
		return
	}
	if r.URL.Query().Get("grant_type") != "client_credentials" {
		s.refuse(w, http.StatusBadRequest, "400.008.02", "Invalid grant type passed")
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		s.refuse(w, http.StatusBadRequest, "400.008.01", "Invalid Authentication passed")
		return
	}
	if user != s.ConsumerKey || pass != s.ConsumerSecret {
		s.refuse(w, http.StatusBadRequest, "400.008.01", "Invalid Authentication passed")
		return
	}
	s.seq++
	tok := fmt.Sprintf("tok-%d", s.seq)
	// A new token invalidates the previous one.
	s.tokens = []issued{{tok, s.Now().Add(time.Duration(s.TokenLife) * time.Second)}}
	s.write(w, http.StatusOK, map[string]any{"access_token": tok, "expires_in": strconv.Itoa(s.TokenLife)})
}

func (s *Server) authorized(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	for _, t := range s.tokens {
		if t.value == got && s.Now().Before(t.until) {
			return true
		}
	}
	return false
}

func field(b map[string]any, k string) string {
	switch v := b[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func wholeAmount(b map[string]any) (string, bool) {
	a := field(b, "Amount")
	n, err := strconv.Atoi(a)
	return a, err == nil && n >= 1
}

func (s *Server) next(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s%07d", prefix, s.seq)
}

func (s *Server) stkPush(w http.ResponseWriter, b map[string]any) {
	for _, k := range []string{"BusinessShortCode", "Password", "Timestamp", "TransactionType", "Amount", "PartyA", "PartyB", "PhoneNumber", "CallBackURL", "AccountReference"} {
		if field(b, k) == "" {
			s.refuse(w, http.StatusBadRequest, "400.002.02", "Bad Request - Invalid "+k)
			return
		}
	}
	code, ts := field(b, "BusinessShortCode"), field(b, "Timestamp")
	if code != s.Shortcode {
		s.refuse(w, http.StatusInternalServerError, "500.001.1001", "Merchant does not exist")
		return
	}
	if field(b, "Password") != base64.StdEncoding.EncodeToString([]byte(code+s.Passkey+ts)) {
		s.refuse(w, http.StatusInternalServerError, "500.001.1001", "Wrong credentials")
		return
	}
	amount, ok := wholeAmount(b)
	if !ok {
		s.refuse(w, http.StatusBadRequest, "400.002.02", "Bad Request - Invalid Amount")
		return
	}
	if len(field(b, "AccountReference")) > 12 {
		s.refuse(w, http.StatusBadRequest, "400.002.02", "Bad Request - Invalid AccountReference")
		return
	}
	phone := field(b, "PhoneNumber")
	for _, p := range s.stk {
		if p.phone == phone && !p.answered {
			s.refuse(w, http.StatusInternalServerError, "500.001.1001", "Unable to lock subscriber, a transaction is already in process for the current subscriber")
			return
		}
	}
	p := &stk{phone: phone, amount: amount, code: code, checkout: s.next("ws_CO_0710202610152"), merchant: s.next("29115-3462-"), callback: field(b, "CallBackURL")}
	s.stk[p.checkout] = p
	s.write(w, http.StatusOK, map[string]any{"MerchantRequestID": p.merchant, "CheckoutRequestID": p.checkout, "ResponseCode": "0",
		"ResponseDescription": "Success. Request accepted for processing", "CustomerMessage": "Success. Request accepted for processing"})
}

func (s *Server) stkQuery(w http.ResponseWriter, b map[string]any) {
	code, ts := field(b, "BusinessShortCode"), field(b, "Timestamp")
	if field(b, "Password") != base64.StdEncoding.EncodeToString([]byte(code+s.Passkey+ts)) {
		s.refuse(w, http.StatusInternalServerError, "500.001.1001", "Wrong credentials")
		return
	}
	p, ok := s.stk[field(b, "CheckoutRequestID")]
	if !ok {
		s.refuse(w, http.StatusBadRequest, "400.002.02", "Bad Request - Invalid CheckoutRequestID")
		return
	}
	if !p.answered {
		s.refuse(w, http.StatusInternalServerError, "500.001.1001", "The transaction is being processed")
		return
	}
	desc := p.callbackBody()["Body"].(map[string]any)["stkCallback"].(map[string]any)["ResultDesc"]
	s.write(w, http.StatusOK, map[string]any{"ResponseCode": "0", "ResponseDescription": "The service request has been accepted successsfully",
		"MerchantRequestID": p.merchant, "CheckoutRequestID": p.checkout, "ResultCode": strconv.Itoa(p.result), "ResultDesc": desc})
}

func (s *Server) register(w http.ResponseWriter, b map[string]any) {
	rt := field(b, "ResponseType")
	if rt != "Completed" && rt != "Cancelled" {
		s.refuse(w, http.StatusBadRequest, "400.003.02", "Bad Request")
		return
	}
	for _, k := range []string{"ConfirmationURL", "ValidationURL"} {
		u := strings.ToLower(field(b, k))
		if u == "" || strings.Contains(u, "m-pesa") || strings.Contains(u, "safaricom") {
			s.refuse(w, http.StatusBadRequest, "400.003.02", "Bad Request")
			return
		}
	}
	if s.urls && s.Production {
		s.refuse(w, http.StatusInternalServerError, "500.003.1001", "Urls are already registered")
		return
	}
	s.urls = true
	s.write(w, http.StatusOK, map[string]any{"OriginatorCoversationID": s.next("6e86-45dd-91ac-"), "ResponseCode": "0", "ResponseDescription": "Success"})
}

// credentialOK checks a SecurityCredential the way M-Pesa does: it must
// decrypt to the initiator's password.
func (s *Server) credentialOK(b map[string]any) bool {
	cred := field(b, "SecurityCredential")
	if s.Credential != "" && cred == s.Credential {
		return true
	}
	raw, err := base64.StdEncoding.DecodeString(cred)
	if err != nil {
		return false
	}
	//nolint:staticcheck // the fake decrypts what Daraja requires to be PKCS #1 v1.5.
	pw, err := rsa.DecryptPKCS1v15(nil, s.Key, raw)
	return err == nil && string(pw) == s.InitiatorPassword
}

func (s *Server) ack(w http.ResponseWriter, originator, conversation string) {
	s.write(w, http.StatusOK, map[string]any{"OriginatorConversationID": originator, "ConversationID": conversation,
		"ResponseCode": "0", "ResponseDescription": "Accept the service request successfully."})
}

func result(code any, desc, originator, conversation, tx string, params []any, refItem string) map[string]any {
	r := map[string]any{"ResultType": 0, "ResultCode": code, "ResultDesc": desc, "OriginatorConversationID": originator,
		"ConversationID": conversation, "TransactionID": tx,
		"ReferenceData": map[string]any{"ReferenceItem": map[string]any{"Key": "QueueTimeoutURL", "Value": refItem}}}
	if params != nil {
		r["ResultParameters"] = map[string]any{"ResultParameter": params}
	}
	return map[string]any{"Result": r}
}

func (s *Server) b2c(w http.ResponseWriter, b map[string]any) {
	id := field(b, "OriginatorConversationID")
	if id == "" || field(b, "ResultURL") == "" || field(b, "QueueTimeOutURL") == "" || field(b, "InitiatorName") == "" {
		s.refuse(w, http.StatusBadRequest, "400.002.02", "Bad Request - Invalid OriginatorConversationID")
		return
	}
	amount, ok := wholeAmount(b)
	if !ok {
		s.refuse(w, http.StatusBadRequest, "400.002.02", "Bad Request - Invalid amount")
		return
	}
	if s.seen[id] {
		s.refuse(w, http.StatusInternalServerError, "500.002.1001", "Duplicate OriginatorConversationID.")
		return
	}
	s.seen[id] = true
	conv := s.next("AG_20261007_2010")
	tx := s.next("TJ7")
	var cb map[string]any
	if !s.credentialOK(b) {
		cb = result(2001, "The initiator information is invalid.", id, conv, tx, nil, "https://internalsandbox.safaricom.co.ke/mpesa/b2cresults/v1/submit")
	} else {
		n, _ := strconv.Atoi(amount)
		cb = result(0, "The service request is processed successfully.", id, conv, tx, []any{
			map[string]any{"Key": "TransactionAmount", "Value": n},
			map[string]any{"Key": "TransactionReceipt", "Value": tx},
			map[string]any{"Key": "ReceiverPartyPublicName", "Value": field(b, "PartyB") + " - JANE WANJIKU"},
			map[string]any{"Key": "TransactionCompletedDateTime", "Value": "07.10.2026 10:15:20"},
			map[string]any{"Key": "B2CUtilityAccountAvailableFunds", "Value": 8959269.6},
			map[string]any{"Key": "B2CRecipientIsRegisteredCustomer", "Value": "Y"},
		}, "https://internalsandbox.safaricom.co.ke/mpesa/b2cresults/v1/submit")
	}
	s.pending = append(s.pending, callback{field(b, "ResultURL"), cb})
	s.ack(w, id, conv)
}

func (s *Server) async(w http.ResponseWriter, b map[string]any, command string, res func(b map[string]any, originator, conv string) map[string]any) {
	if field(b, "CommandID") != command || field(b, "ResultURL") == "" || field(b, "QueueTimeOutURL") == "" {
		s.refuse(w, http.StatusBadRequest, "400.002.02", "Bad Request - Invalid CommandID")
		return
	}
	originator, conv := s.next("16917-2257-"), s.next("AG_20261007_0000")
	var cb map[string]any
	if !s.credentialOK(b) {
		cb = result(2001, "The initiator information is invalid.", originator, conv, "TJ70000000", nil, "")
	} else {
		cb = res(b, originator, conv)
	}
	s.pending = append(s.pending, callback{field(b, "ResultURL"), cb})
	s.ack(w, originator, conv)
}

// TimeoutNext turns the next pending result into a QueueTimeOutURL
// notification (M-Pesa gave up waiting); Safaricom does not document its
// body, so the fake posts the Result envelope with code 1037.
func (s *Server) TimeoutNext(timeoutURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		s.t.Fatal("fakedaraja: nothing pending to time out")
	}
	p := s.pending[0]
	r := p.body.(map[string]any)["Result"].(map[string]any)
	s.pending[0] = callback{timeoutURL, result(1037, "DS timeout user cannot be reached", r["OriginatorConversationID"].(string), r["ConversationID"].(string), "", nil, "")}
}

func (s *Server) statusResult(b map[string]any, originator, conv string) map[string]any {
	return result(0, "The service request is processed successfully.", originator, conv, "TJ70000000", []any{
		map[string]any{"Key": "DebitPartyName", "Value": "600310 - Safaricom333"},
		map[string]any{"Key": "ReceiptNo", "Value": field(b, "TransactionID")},
		map[string]any{"Key": "TransactionStatus", "Value": "Completed"},
		map[string]any{"Key": "Amount", "Value": "300"},
	}, "")
}

func (s *Server) balanceResult(_ map[string]any, originator, conv string) map[string]any {
	return result("0", "The service request is processed successfully", originator, conv, "TJ70000000", []any{
		map[string]any{"Key": "AccountBalance", "Value": "Working Account|KES|700000.00|700000.00|0.00|0.00&Utility Account|KES|228037.00|228037.00|0.00|0.00"},
		map[string]any{"Key": "BOCompletedTime", "Value": "20261007101520"},
	}, "")
}

func (s *Server) reversalResult(b map[string]any, originator, conv string) map[string]any {
	tx := field(b, "TransactionID")
	if !strings.HasPrefix(tx, "TJ") {
		return result("R000002", "The OriginalTransactionID is invalid.", originator, conv, "TJ70000000", nil, "")
	}
	return result(0, "The service request is processed successfully.", originator, conv, s.next("TJR"), []any{
		map[string]any{"Key": "OriginalTransactionID", "Value": tx},
		map[string]any{"Key": "Amount", "Value": 1.0},
	}, "")
}
