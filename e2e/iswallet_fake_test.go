package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fakeIswallet behaves as iswallet engineering described it on 5 October
// 2026: a 24-hour replay cache on Idempotency-Key, a ledger that moves once
// per key, wallet transfers final on 200, bank payouts pending on 202, a
// per-transaction limit, and webhooks signed over "<timestamp>.<body>".
type fakeIswallet struct {
	mu         sync.Mutex
	balances   map[string]int64          // wallet -> available kobo
	replay     map[string][]byte         // Idempotency-Key -> original response
	outflows   map[string]map[string]any // outflow id -> record
	moves      map[string]int            // Idempotency-Key -> times money moved
	limit      int64                     // per-transaction limit, kobo
	fail500    map[string]bool           // keys whose first response is lost as a 500 after the money moved
	nextID     int
	requests   int
	failWallet string // the next transfer to this wallet loses its response
}

func newFakeIswallet() *fakeIswallet {
	return &fakeIswallet{balances: map[string]int64{}, replay: map[string][]byte{}, outflows: map[string]map[string]any{},
		moves: map[string]int{}, limit: 5_000_000_00, fail500: map[string]bool{}}
}

func (f *fakeIswallet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer isw_payrolla_key" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"INVALID_API_KEY","message":"bad key","request_id":"req_x"}}`))
		return
	}
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/balance"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/wallets/"), "/balance")
		b := f.balances[id]
		_ = json.NewEncoder(w).Encode(map[string]any{"wallet_id": id, "balances": []any{map[string]any{"currency": "NGN", "total": b, "available": b, "pending": 0, "scale": 2}}})
	case r.Method == http.MethodPost && (r.URL.Path == "/v1/transfers" || r.URL.Path == "/v1/outflows"):
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"MISSING_IDEMPOTENCY_KEY","message":"","request_id":"req_k"}}`))
			return
		}
		if raw, ok := f.replay[key]; ok {
			if f.fail500[key] {
				f.fail500[key] = false // the original answer was lost; now replay it
			}
			w.WriteHeader(int(raw[0]) + 100)
			_, _ = w.Write(raw[1:])
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		amount := int64(in["amount"].(float64))
		if amount > f.limit {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"code":"EXCEEDS_SINGLE_TXN_LIMIT","message":"over the per-transaction limit","request_id":"req_l"}}`))
			return
		}
		f.nextID++
		var status int
		var out map[string]any
		if r.URL.Path == "/v1/transfers" {
			from, to := in["from_wallet_id"].(string), in["to_wallet_id"].(string)
			if to == f.failWallet {
				f.failWallet, f.fail500[key] = "", true
			}
			f.balances[from] -= amount
			f.balances[to] += amount
			status, out = 200, map[string]any{"transfer_id": fmt.Sprintf("tr_%d", f.nextID), "txn_id": fmt.Sprintf("txn_%d", f.nextID),
				"from_wallet_id": from, "to_wallet_id": to, "amount": amount, "currency": "NGN", "status": "completed", "completed_at": time.Now().UTC().Format(time.RFC3339)}
		} else {
			wallet := in["wallet_id"].(string)
			f.balances[wallet] -= amount + 5350
			id := fmt.Sprintf("out_%d", f.nextID)
			out = map[string]any{"outflow_id": id, "operation_id": id, "wallet_id": wallet, "amount": amount, "currency": "NGN", "status": "pending",
				"fee": 5350, "net_amount": amount, "provider_reference": key, "destination": in["destination"]}
			rec := map[string]any{}
			for k, v := range out {
				rec[k] = v
			}
			rec["idempotency_key"] = key
			f.outflows[id] = rec
			status = 202
		}
		f.moves[key]++
		raw, _ := json.Marshal(out)
		f.replay[key] = append([]byte{byte(status - 100)}, raw...)
		if f.fail500[key] {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR","message":"","request_id":"req_500"}}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"","request_id":"req_nf"}}`))
	}
}

// outflowByEmployee finds a payout by the employee in its destination name.
func (f *fakeIswallet) outflowFor(accountName string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.outflows {
		if d, _ := o["destination"].(map[string]any); d != nil && d["account_name"] == accountName {
			return o
		}
	}
	return nil
}

// event builds a signed outflow webhook delivery for o.
func (f *fakeIswallet) event(o map[string]any, eventType, secret string) (body []byte, headers []string) {
	data := map[string]any{}
	for _, k := range []string{"outflow_id", "operation_id", "wallet_id", "amount", "fee", "currency", "idempotency_key", "provider_reference"} {
		data[k] = o[k]
	}
	if eventType == "wallet.outflow.failed" {
		data["failure_reason"] = "beneficiary account closed"
	}
	body, _ = json.Marshal(map[string]any{"id": "evt_" + fmt.Sprint(o["outflow_id"]) + "_" + eventType, "event_type": eventType,
		"schema_version": "v1", "occurred_at": time.Now().UTC().Format(time.RFC3339), "wallet_id": o["wallet_id"], "data": data})
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, ts+"."+string(body))
	return body, []string{"X-iSpend-Signature", "sha256=" + hex.EncodeToString(mac.Sum(nil)), "X-iSpend-Timestamp", ts}
}

// failNextTransferTo makes the next transfer to wallet lose its response as
// a 500 after the money has moved. Call with f.mu held.
func (f *fakeIswallet) failNextTransferTo(wallet string) { f.failWallet = wallet }

// creditEvent builds a signed credit webhook delivery.
func creditEvent(id, eventType, wallet string, data map[string]any, secret string) (body []byte, headers []string) {
	body, _ = json.Marshal(map[string]any{"id": id, "event_type": eventType, "schema_version": "v1",
		"occurred_at": "2026-10-05T09:15:40Z", "wallet_id": wallet, "data": data})
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, ts+"."+string(body))
	return body, []string{"X-iSpend-Signature", "sha256=" + hex.EncodeToString(mac.Sum(nil)), "X-iSpend-Timestamp", ts}
}
