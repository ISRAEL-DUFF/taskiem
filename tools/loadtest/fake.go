package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// fakeProvider stands in for Paystack (balance, an idempotent transfer
// keyed by reference, verify) and Termii (an SMS send with no idempotency
// key: an unsafe write), on loopback. It records every effect so a test
// can prove none was lost or repeated. The workflows put the run id in the
// transfer's reason and in the SMS text.
type fakeProvider struct {
	latency       time.Duration // added to every request
	transferDelay atomic.Int64  // extra delay after a transfer is recorded (ns): lets a test kill a worker mid-step
	smsDelay      atomic.Int64

	inflight atomic.Int64

	mu        sync.Mutex
	transfers map[string]string   // reference -> run id (the money moved once per reference)
	refsByRun map[string][]string // run id -> distinct references it moved money under
	posts     map[string]int      // reference -> POST /transfer requests
	sms       map[string]int      // run id -> SMS sent
	requests  int
}

func newFakeProvider(latency time.Duration) *fakeProvider {
	return &fakeProvider{latency: latency, transfers: map[string]string{}, refsByRun: map[string][]string{}, posts: map[string]int{}, sms: map[string]int{}}
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.inflight.Add(1)
	defer f.inflight.Add(-1)
	if f.latency > 0 {
		time.Sleep(f.latency)
	}
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/balance":
		reply(200, map[string]any{"status": true, "data": []any{map[string]any{"currency": "NGN", "balance": 1_000_000_000}}})
	case r.Method == http.MethodPost && r.URL.Path == "/transfer":
		var in struct {
			Reference string `json:"reference"`
			Reason    string `json:"reason"`
			Amount    any    `json:"amount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.posts[in.Reference]++
		_, dup := f.transfers[in.Reference]
		if !dup {
			f.transfers[in.Reference] = in.Reason
			f.refsByRun[in.Reason] = append(f.refsByRun[in.Reason], in.Reference)
		}
		f.mu.Unlock()
		if dup {
			reply(400, map[string]any{"status": false, "message": "Duplicate Transfer Reference"})
			return
		}
		time.Sleep(time.Duration(f.transferDelay.Load()))
		reply(200, map[string]any{"status": true, "data": map[string]any{"reference": in.Reference, "transfer_code": "TRF_" + in.Reference, "status": "success", "amount": in.Amount, "currency": "NGN"}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/transfer/verify/"):
		ref := strings.TrimPrefix(r.URL.Path, "/transfer/verify/")
		f.mu.Lock()
		_, ok := f.transfers[ref]
		f.mu.Unlock()
		if !ok {
			reply(404, map[string]any{"status": false, "message": "Transfer not found"})
			return
		}
		reply(200, map[string]any{"status": true, "data": map[string]any{"reference": ref, "transfer_code": "TRF_" + ref, "status": "success", "currency": "NGN"}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/sms/send":
		var in struct {
			SMS string `json:"sms"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.sms[in.SMS]++
		f.mu.Unlock()
		time.Sleep(time.Duration(f.smsDelay.Load()))
		reply(200, map[string]any{"message_id": "msg_" + in.SMS, "message": "Successfully Sent", "balance": 100})
	case r.Method == http.MethodGet && r.URL.Path == "/api/get-balance":
		reply(200, map[string]any{"balance": 100, "currency": "NGN"})
	default:
		reply(404, map[string]any{"status": false, "message": "no route"})
	}
}

// effects summarises what the provider saw: runs that moved money under
// more than one reference (duplicates), references posted more than once
// (retries the provider deduplicated), and SMS sent more than once per run.
type effects struct {
	Transfers, DuplicateRuns, RetriedRefs, SMS, DuplicateSMS int
	transferRuns, smsRuns                                    map[string]int
}

func (f *fakeProvider) effects() effects {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := effects{transferRuns: map[string]int{}, smsRuns: map[string]int{}}
	e.Transfers = len(f.transfers)
	for run, refs := range f.refsByRun {
		e.transferRuns[run] = len(refs)
		if len(refs) > 1 {
			e.DuplicateRuns++
		}
	}
	for _, n := range f.posts {
		if n > 1 {
			e.RetriedRefs++
		}
	}
	for run, n := range f.sms {
		e.SMS += n
		e.smsRuns[run] = n
		if n > 1 {
			e.DuplicateSMS++
		}
	}
	return e
}
