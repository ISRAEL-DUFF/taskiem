package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
)

// On Slack (H7, g-pay-fwd): a message shared into the DM with account
// details is read as a forward, and a reply of "pay" in a message's
// thread reads the message it is under: each answered with the bank's
// name for the account and "how much, and for what?".
func TestSlackPayForward(t *testing.T) {
	w := newSlackWorld(t)
	owner := w.tenant(t, "Acme", "owner@slackpay.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	w.fake.users["owner@slackpay.test"] = "U0OWNER"
	w.link(t, owner, "U0OWNER")
	rails := &standInRails{balance: 10_000_000, quotes: true, names: map[string]string{}}
	w.srv.AccountRails = rails
	for _, c := range []*connector.Connector{payconn.New(w.srv.PaymentsOps()), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	banks := []map[string]any{{"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	chidi := fitting(t, "058", "012345678")
	rails.names[chidi] = "CHIDI OKEKE"

	last := func(before int) string {
		t.Helper()
		got := w.fake.to("U0OWNER")[before:]
		if len(got) == 0 {
			t.Fatal("no answer")
		}
		return got[len(got)-1]["text"].(string)
	}
	// Shared into the DM.
	share := dm("EvS1", "U0OWNER", "")
	share["event"].(map[string]any)["ts"] = "1700000001.000100"
	share["event"].(map[string]any)["attachments"] = []any{map[string]any{"is_share": true, "text": "Chidi Okeke " + chidi + " GTBank"}}
	before := len(w.fake.to("U0OWNER"))
	w.event(t, share, slackSecret)
	if got := last(before); !strings.Contains(got, "Pay CHIDI OKEKE (") || !strings.Contains(got, "how much, and for what?") {
		t.Fatalf("a shared message: %q", got)
	}
	w.event(t, dm("EvS2", "U0OWNER", "cancel"), slackSecret)
	// A message of its own, then "pay" in its thread.
	acct := dm("EvS3", "U0OWNER", "acct "+chidi+" GTBank Chidi")
	acct["event"].(map[string]any)["ts"] = "1700000002.000200"
	w.event(t, acct, slackSecret)
	w.event(t, dm("EvS4", "U0OWNER", "cancel"), slackSecret)
	reply := dm("EvS5", "U0OWNER", "pay")
	reply["event"].(map[string]any)["ts"] = "1700000003.000300"
	reply["event"].(map[string]any)["thread_ts"] = "1700000002.000200"
	before = len(w.fake.to("U0OWNER"))
	w.event(t, reply, slackSecret)
	if got := last(before); !strings.Contains(got, "Pay CHIDI OKEKE (") {
		t.Fatalf("pay in a thread: %q", got)
	}
}
