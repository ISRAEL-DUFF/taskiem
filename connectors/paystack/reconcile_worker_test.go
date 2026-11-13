package paystack_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/connectors/paystack"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// fakePaystack answers the refund endpoints for one payment, PAY-1. The
// first POST /refund is taken (when takes is set) and then answered with a
// 502, as a gateway does when the answer is lost: an unknown outcome.
type fakePaystack struct {
	mu       sync.Mutex
	takes    bool // the lost refund really was made
	refunds  int  // POST /refund calls
	refunded bool
	verified []string // references asked of /transaction/verify
	srv      *httptest.Server
}

func newFakePaystack(t *testing.T, takes bool) *fakePaystack {
	f := &fakePaystack{takes: takes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/refund":
			f.refunds++
			if f.refunds == 1 {
				f.refunded = f.takes
				rw.WriteHeader(http.StatusBadGateway)
				_, _ = rw.Write([]byte(`{"status":false,"message":"Bad gateway"}`))
				return
			}
			_, _ = rw.Write([]byte(`{"status":true,"data":{"id":2,"amount":300000,"status":"pending"}}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/transaction/verify/"):
			ref := strings.TrimPrefix(r.URL.Path, "/transaction/verify/")
			f.verified = append(f.verified, ref)
			if ref != "PAY-1" {
				rw.WriteHeader(http.StatusBadRequest)
				_, _ = rw.Write([]byte(`{"status":false,"message":"Transaction reference not found"}`))
				return
			}
			status := "success"
			if f.refunded {
				status = "reversed"
			}
			_ = json.NewEncoder(rw).Encode(map[string]any{"status": true, "data": map[string]any{"reference": ref, "status": status, "amount": 300000}})
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// A refund whose answer was lost is reconciled by the worker itself, with
// the real refund_status action: it must be asked about the payment the
// refund was for. The worker used to give it only the engine's derived
// key, which names no Paystack transaction, so every such refund parked
// with "reference not found".
func TestRefundReconcilesThroughTheWorker(t *testing.T) {
	e := rt.New(t)
	f := newFakePaystack(t, true)
	got := e.RunConnectorStep(t, paystack.New(paystack.Options{BaseURL: f.srv.URL}), map[string]string{"secret_key": "sk_test_x"},
		"refund_transaction", map[string]any{"reference": "PAY-1", "merchant_note": "wrong order"})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.verified) == 0 {
		t.Fatalf("reconcile never asked Paystack: %+v", got)
	}
	for _, ref := range f.verified {
		if ref != "PAY-1" {
			t.Errorf("reconcile asked about %q, not the payment the refund was for", ref)
		}
	}
	out, _ := got.Output.(map[string]any)
	if got.Status != "completed" || !got.Reconciled || out["status"] != "reversed" || out["reference"] != "PAY-1" {
		t.Errorf("the refund Paystack made must be found: status %s, reconciled %v, output %v, failures %+v", got.Status, got.Reconciled, got.Output, got.Failures)
	}
	if f.refunds != 1 || len(got.Keys) != 1 {
		t.Errorf("refund sent %d times under %d keys; once", f.refunds, len(got.Keys))
	}
}

// The payment is not (yet) reversed: the refund may be pending, which the
// payment does not show. Not "not found", so it is not sent again: the
// step parks for a person.
func TestRefundNotYetReversedParksRatherThanResends(t *testing.T) {
	e := rt.New(t)
	f := newFakePaystack(t, false)
	got := e.RunConnectorStep(t, paystack.New(paystack.Options{BaseURL: f.srv.URL}), map[string]string{"secret_key": "sk_test_x"},
		"refund_transaction", map[string]any{"reference": "PAY-1"})
	f.mu.Lock()
	defer f.mu.Unlock()
	if got.Status != "needs_reconciliation" || f.refunds != 1 || len(got.Keys) != 1 {
		t.Fatalf("status %s, refund sent %d times under %d keys: %+v", got.Status, f.refunds, len(got.Keys), got.Failures)
	}
	last := got.Failures[len(got.Failures)-1]
	if last.Error.Next != "park" || !strings.Contains(last.Error.Message, "may still be pending") {
		t.Errorf("parked with %+v", last.Error)
	}
	if len(f.verified) == 0 || f.verified[0] != "PAY-1" {
		t.Errorf("asked about %v", f.verified)
	}
}
