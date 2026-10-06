package builtin

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
)

// TestTriggerExpressions evaluates every new connector's trigger
// expressions against payloads shaped as each provider documents them, with
// the engine ingest uses.
func TestTriggerExpressions(t *testing.T) {
	reg := connector.NewRegistry()
	if err := Register(reg, Options{}); err != nil {
		t.Fatal(err)
	}
	e := expr.MustNewWithRoots("body", "headers", "query")
	for _, c := range []struct {
		ref, trigger, body        string
		event, dedup, correlation string
	}{
		{"lenco@1", "event", `{"event":"transaction.successful","data":{"id":"46ce","clientReference":"tsk_1","status":"successful"}}`,
			"transaction.successful", "transaction.successful:46ce", "tsk_1"},
		{"lenco@1", "event", `{"event":"transaction.failed","data":{"id":"46cf","clientReference":null}}`,
			"transaction.failed", "transaction.failed:46cf", ""},
		{"lenco@1", "event", `{"event":"virtual-account.transaction","data":{"id":"v1","accountReference":"f0f1"}}`,
			"virtual-account.transaction", "virtual-account.transaction:v1", "f0f1"},
		{"anchor@1", "transfer_event", `{"data":{"id":"e1-anc_et","type":"nip.transfer.successful","relationships":{"transfer":{"data":{"id":"t1-anc_trsf"}}}}}`,
			"nip.transfer.successful", "e1-anc_et", "t1-anc_trsf"},
		{"anchor@1", "transfer_event", `{"id":"e2-anc_et","type":"nip.transfer.reversed","relationships":{"transfer":{"data":{"id":"t1-anc_trsf"}}}}`,
			"nip.transfer.reversed", "e2-anc_et", "t1-anc_trsf"},
		{"anchor@1", "inflow_event", `{"data":{"id":"e3","type":"nip.inbound.completed","relationships":{"account":{"data":{"id":"a1-anc_acc"}}}}}`,
			"nip.inbound.completed", "e3", "a1-anc_acc"},
		{"flutterwave@1", "event", `{"event":"transfer.completed","event.type":"Transfer","data":{"id":33286,"status":"SUCCESSFUL","reference":"tsk_1"}}`,
			"transfer.completed", "transfer.completed:33286:SUCCESSFUL", "tsk_1"},
		{"flutterwave@1", "event", `{"event":"charge.completed","data":{"id":285959875,"tx_ref":"order-17","status":"successful"}}`,
			"charge.completed", "charge.completed:285959875:successful", "order-17"},
		{"breet@1", "event", `{"event":"trade.completed","eventId":"e3f2","id":"692f","destinationAddress":"6DKY"}`,
			"trade.completed", "e3f2", "6DKY"},
		{"breet@1", "event", `{"event":"withdrawal.completed","eventId":"2d7f","id":"6968"}`,
			"withdrawal.completed", "2d7f", "6968"},
		{"breet@1", "event", `{"event":"trade.address.created","eventId":"8b4c","address":"TB3y"}`,
			"trade.address.created", "8b4c", "TB3y"},
		{"moniepoint@1", "event", `{"eventType":"SUCCESSFUL_DISBURSEMENT","eventData":{"amount":10,"transactionReference":"MFDS|20210317032332|002431","fee":8,"reference":"tsk_1","status":"SUCCESS"}}`,
			"SUCCESSFUL_DISBURSEMENT", "SUCCESSFUL_DISBURSEMENT:MFDS|20210317032332|002431", "tsk_1"},
		{"moniepoint@1", "event", `{"eventType":"SUCCESSFUL_TRANSACTION","eventData":{"product":{"reference":"acct-ref-1","type":"RESERVED_ACCOUNT"},"transactionReference":"MNFY|04|1","paymentReference":"MNFY|04|1","amountPaid":3000}}`,
			"SUCCESSFUL_TRANSACTION", "SUCCESSFUL_TRANSACTION:MNFY|04|1", "acct-ref-1"},
		{"moniepoint@1", "event", `{"eventType":"SUCCESSFUL_REFUND","eventData":{"transactionReference":"MNFY|9","refundReference":"ref001","refundStatus":"COMPLETED"}}`,
			"SUCCESSFUL_REFUND", "SUCCESSFUL_REFUND:ref001", "ref001"},
		{"interswitch@1", "event", `{"event":"TRANSACTION.COMPLETED","uuid":"2Xdf35faAyX2Sk5Dalu405rUD","timestamp":1594646111460,"data":{"amount":12000,"responseCode":"00","merchantReference":"order-17"}}`,
			"TRANSACTION.COMPLETED", "TRANSACTION.COMPLETED:2Xdf35faAyX2Sk5Dalu405rUD:1594646111460", "order-17"},
		{"interswitch@1", "event", `{"event":"INVOICE.TRANSACTION_SUCCESSFUL","uuid":"inv-9","timestamp":1594646112000,"data":{"amount":5000}}`,
			"INVOICE.TRANSACTION_SUCCESSFUL", "INVOICE.TRANSACTION_SUCCESSFUL:inv-9:1594646112000", "inv-9"},
	} {
		conn, ok := reg.Get(c.ref)
		if !ok {
			t.Fatalf("%s not registered", c.ref)
		}
		spec := conn.Manifest.Triggers[c.trigger]
		body, err := expr.DecodeJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
		for _, f := range []struct{ name, src, want string }{{"event_type", spec.EventType, c.event}, {"dedup", spec.Dedup, c.dedup}, {"correlation", spec.Correlation, c.correlation}} {
			v, err := e.Eval(f.src, act)
			if err != nil {
				t.Errorf("%s %s %s: %v", c.ref, c.trigger, f.name, err)
				continue
			}
			got, _ := v.(string)
			if v != nil && got == "" {
				t.Errorf("%s %s %s: %v (%T) is not a string", c.ref, c.trigger, f.name, v, v)
			}
			if got != f.want {
				t.Errorf("%s %s %s = %q, want %q", c.ref, c.trigger, f.name, got, f.want)
			}
		}
	}
}
