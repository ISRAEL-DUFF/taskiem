package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
)

// A trigger whose address carries the secret is shown with a placeholder,
// never the value; a remotely registered one is left out.
func TestMoneyWebhooksPlaceholders(t *testing.T) {
	m := &connector.Manifest{ID: "momo", Version: "2.1.0", Name: "MoMo",
		Auth: connector.Auth{Fields: []connector.AuthField{{Key: "callback_token", Label: "Callback token", Secret: true}}},
		Triggers: map[string]connector.TriggerSpec{
			"cb":     {Type: "webhook", Verify: &connector.VerifySpec{Scheme: "path_secret", SecretField: "callback_token"}},
			"q":      {Type: "webhook", Verify: &connector.VerifySpec{Scheme: "query_secret", Query: "token", SecretField: "callback_token"}},
			"remote": {Type: "webhook", Registration: "remote", Verify: &connector.VerifySpec{Scheme: "hmac_sha256", SecretField: "callback_token"}},
		},
		Events: map[string]connector.EventMap{
			"payment.received": {Trigger: "cb"},
			"payout.settled":   {Trigger: "q"},
			"payout.failed":    {Trigger: "remote"},
		},
	}
	tenant := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := moneyWebhooks(&connector.Connector{Manifest: m}, "https://h.test/hooks", tenant, "main")
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].URL != "https://h.test/hooks/"+tenant.String()+"/connectors/momo@2/cb/prod/main/{callback_token}" || got[0].Placeholder != "{callback_token}" {
		t.Fatalf("path_secret: %+v", got[0])
	}
	if got[1].URL != "https://h.test/hooks/"+tenant.String()+"/connectors/momo@2/q?connection=main&env=prod&token={callback_token}" {
		t.Fatalf("query_secret: %+v", got[1])
	}
	if !strings.Contains(got[0].Verification, "Callback token") || got[0].Title != "Cb" || !strings.Contains(got[0].Where, "MoMo's dashboard") {
		t.Fatalf("words: %+v", got[0])
	}
	if moneyWebhooks(&connector.Connector{Manifest: &connector.Manifest{ID: "x", Version: "1.0.0"}}, "/hooks", tenant, "main") != nil {
		t.Fatal("a connector without events has addresses")
	}
}
