package ingest_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/telegram"
	"github.com/israel-duff/taskiem/connectors/whatsapp"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// What a customer writes to a business's WhatsApp number or Telegram bot,
// and who they are, is sealed in the run's history (decision 0062, P1),
// while the run's steps still read it.
func TestCustomerMessagesAreSealed(t *testing.T) {
	w := newWorld(t)
	if err := w.Registry.Register(whatsapp.New(whatsapp.Options{})); err != nil {
		t.Fatal(err)
	}
	if err := w.Registry.Register(telegram.New(telegram.Options{})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "whatsapp", "main", "api_key",
		map[string]string{"access_token": "EAA1", "phone_number_id": "106540352242922", "app_secret": "wa-secret"}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "telegram", "bot", "api_key",
		map[string]string{"bot_token": "1:x", "webhook_secret": "tg-secret"}, "test"); err != nil {
		t.Fatal(err)
	}
	wa := w.publish(t, `{"schema":"wd/v1","id":"wf_wa","version":1,"name":"wa","trigger":{"type":"connector_event","config":{"connector":"whatsapp@1","trigger":"messages","events":["message"]}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=size(trigger.item.message.text.body)"}}]}`)
	tg := w.publish(t, `{"schema":"wd/v1","id":"wf_tg","version":1,"name":"tg","trigger":{"type":"connector_event","config":{"connector":"telegram@1","trigger":"update","events":["message"]}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=size(trigger.body.message.text)"}}]}`)

	body := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{"messaging_product":"whatsapp",
	  "metadata":{"display_phone_number":"15550783881","phone_number_id":"106540352242922"},
	  "contacts":[{"profile":{"name":"Adaeze Okonkwo"},"wa_id":"2348031234567"}],
	  "messages":[{"from":"2348031234567","id":"wamid.IN1","timestamp":"1762600000","type":"text","text":{"body":"My address is 12 Admiralty Way"}}]}}]}]}`)
	if st, out := w.post(t, "/connectors/whatsapp@1/messages?env=prod", body, "X-Hub-Signature-256", "sha256="+sign(sha256.New, "wa-secret", body)); st != 202 {
		t.Fatalf("whatsapp delivery: %d %v", st, out)
	}
	tgBody := []byte(`{"update_id":901,"message":{"message_id":5,"date":1762600000,"chat":{"id":555000111,"type":"private","first_name":"Chidinma","username":"chidi_n"},
	  "from":{"id":555000111,"is_bot":false,"first_name":"Chidinma","username":"chidi_n"},"text":"I live at 12 Admiralty Way"}}`)
	if st, out := w.post(t, "/connectors/telegram@1/update?env=prod", tgBody, "X-Telegram-Bot-Api-Secret-Token", "tg-secret"); st != 202 {
		t.Fatalf("telegram delivery: %d %v", st, out)
	}
	for name, wf := range map[string]uuid.UUID{"whatsapp": wa, "telegram": tg} {
		var run uuid.UUID
		if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT id FROM runs WHERE workflow_id = $1`, wf).Scan(&run)
		}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		w.Drain(t)
		ref := runtime.RunRef{TenantID: w.Tenant, ID: run}
		if st := w.Status(t, ref); st != "completed" {
			t.Fatalf("%s: the run could not read the message: %s", name, st)
		}
		evs, err := w.Store.RunHistory(ctx, ref)
		if err != nil || len(evs) == 0 {
			t.Fatal(err)
		}
		raw := string(evs[0].Payload)
		for _, clear := range []string{"Admiralty", "2348031234567", "Adaeze", "Chidinma", "chidi_n", "555000111"} {
			if strings.Contains(raw, clear) {
				t.Errorf("%s: %q is in history in the clear", name, clear)
			}
		}
	}
}
