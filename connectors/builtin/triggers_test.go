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
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
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
		{"telegram@1", "update", `{"update_id":10000,"message":{"message_id":1365,"chat":{"id":1111111,"type":"private"},"date":1791320000,"text":"/start"}}`,
			"message", "10000", "1111111"},
		{"telegram@1", "update", `{"update_id":10001,"callback_query":{"id":"4382bf","from":{"id":1111111,"is_bot":false,"first_name":"Ada"},"message":{"message_id":4242,"chat":{"id":-1001234567890,"type":"supergroup"},"date":1791320000},"chat_instance":"-1","data":"approve:run-17"}}`,
			"callback_query", "10001", "-1001234567890"},
		{"whatsapp@1", "messages", `{"object":"whatsapp_business_account","entry":[{"id":"102290129340398","changes":[{"value":{"messaging_product":"whatsapp","metadata":{"display_phone_number":"15550783881","phone_number_id":"106540352242922"},"contacts":[{"profile":{"name":"Sheena Nelson"},"wa_id":"16505551234"}],"messages":[{"from":"16505551234","id":"wamid.IN1","timestamp":"1749416383","type":"text","text":{"body":"Hi"}}]},"field":"messages"}]}]}`,
			"message", "wamid.IN1", "16505551234"},
		{"whatsapp@1", "messages", `{"object":"whatsapp_business_account","entry":[{"id":"102290129340398","changes":[{"value":{"messaging_product":"whatsapp","metadata":{"display_phone_number":"15550783881","phone_number_id":"106540352242922"},"statuses":[{"id":"wamid.OUT1","status":"delivered","timestamp":"1750263773","recipient_id":"16505551234"}]},"field":"messages"}]}]}`,
			"status.delivered", "wamid.OUT1:delivered", "wamid.OUT1"},
		{"africastalking@1", "delivery_report", `{"id":"ATXid_a1","status":"Success","phoneNumber":"+254711000001","networkCode":"63902"}`,
			"Success", "ATXid_a1:Success", "ATXid_a1"},
		{"africastalking@1", "incoming_sms", `{"date":"2026-10-06 11:20:46","from":"+254711000001","id":"15071","linkId":"L1","text":"STOP","to":"28901","networkCode":"63902"}`,
			"incoming_sms", "15071", "+254711000001"},
		{"slack@1", "events", `{"type":"event_callback","event":{"type":"message","channel":"C123ABC456","user":"U1","text":"ok","ts":"1503435999.000300","thread_ts":"1503435956.000247"},"event_id":"Ev123ABC456"}`,
			"message", "Ev123ABC456", "C123ABC456:1503435956.000247"},
		{"slack@1", "events", `{"type":"event_callback","event":{"type":"reaction_added","reaction":"white_check_mark","item":{"type":"message","channel":"C123ABC456","ts":"1503435956.000247"}},"event_id":"Ev2"}`,
			"reaction_added", "Ev2", "C123ABC456:1503435956.000247"},
		{"opay@1", "payment", `{"payload":{"amount":"49160","currency":"NGN","reference":"10023","refunded":false,"status":"SUCCESS","timestamp":"2022-05-07T06:20:46Z","token":"220507145660712931829","transactionId":"220507145660712931829"},"sha512":"9f605d69f04e94172875dc156537071cead060bbcaeaca94a7b8805af9f89611e2fdf6836713c9c90b028ca7e4470b1356e996975f2abc862315aaa9b7f2ae2d","type":"transaction-status"}`,
			"transaction-status", "9f605d69f04e94172875dc156537071cead060bbcaeaca94a7b8805af9f89611e2fdf6836713c9c90b028ca7e4470b1356e996975f2abc862315aaa9b7f2ae2d", "10023"},
		{"remita@1", "invoice_payment", `[{"rrr":"110002071256","channel":"CARDPAYMENT","amount":650000.00,"orderId":"6954148807","type":"PY"}]`,
			"payment_notification", "110002071256", "6954148807"},
		{"mono@1", "event", `{"event":"mono.events.account_connected","event_id":"jU4i","data":{"id":"6979","customer":"6961"}}`, "mono.events.account_connected", "jU4i", "6979"},
		{"mono@1", "event", `{"event":"direct_debit.payment_successful","event_id":"Psm1","data":{"object":{"id":"txd_1","reference":"ref123"}}}`, "direct_debit.payment_successful", "Psm1", "ref123"},
		{"mono@1", "event", `{"event":"events.mandates.debit.successful","event_id":"d1","data":{"mandate":"mmc_1","reference_number":"dref"}}`, "events.mandates.debit.successful", "d1", "dref"},
		{"prembly@1", "verification", `{"status":true,"response_code":"00","data":{"firstName":"A"},"verification":{"status":"NOT-VERIFIED","reference":"9a7c"}}`, "NOT-VERIFIED", "9a7c", "9a7c"},
		{"youverify@1", "event", `{"event":"identity.verification.completed","apiVersion":"v2","data":{"id":"646b","status":"found","createdAt":"2024-03-27T08:30:03.367Z"}}`, "identity.verification.completed", "identity.verification.completed:646b:found::2024-03-27T08:30:03.367Z", "646b"},
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
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}, "item": nil}
		if spec.Split != "" { // batched deliveries: check the first event
			v, err := e.Eval(spec.Split, act)
			list, _ := v.([]any)
			if err != nil || len(list) == 0 {
				t.Fatalf("%s %s split: %v %v", c.ref, c.trigger, v, err)
			}
			act["item"] = list[0]
		}
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
