package api_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/chatlinks"
)

// tells records who was told a business answered (chatlinks.Teller).
type tells struct {
	mu  sync.Mutex
	got []chatlinks.Answer
}

func (t *tells) TellAnswered(_ context.Context, a chatlinks.Answer) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.got = append(t.got, a)
	return nil
}

func (t *tells) n() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.got)
}

// Streams A and B together (taskiem-for-everyone §8.2, §8.4, §9): a chat
// starts from a business's live chat link, checked by the door itself; a
// person cannot claim a link to open a business with no shop; an old
// link says so; a business taking no chats takes no new ones; the person
// is told when the business answers, not for an offer or a muted chat.
func TestCustomerDoorWithChatLinks(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	tell := &tells{}
	w.srv.Answered = tell

	// A business with no published shop and no orders: only its link opens it.
	bola := w.tenant(t, "Bola Bakes", "bola@bola.test")
	bolaID := tenantUUID(t, bola)
	tolu, _ := w.personIn(t, "+2348031110021")
	tolu.must(400, "POST", "/v1/me/chats", map[string]any{"business_id": bolaID, "from": "code"})
	tolu.must(404, "POST", "/v1/me/chats", map[string]any{"business_id": bolaID})
	// The door itself refuses a claimed link: straight to the database, as
	// the API once did, with "via: code" and no live code.
	toluID := personID(t, tolu)
	if err := doorStartWith(ctx, w, toluID, bolaID, map[string]any{"via": "code"}); err == nil {
		t.Fatal("the door opened a business for a claimed link")
	}
	if err := doorStartWith(ctx, w, toluID, bolaID, map[string]any{"via": "code", "code_hash": "00"}); err == nil {
		t.Fatal("the door opened a business for a wrong code")
	}

	code := bola.must(200, "GET", "/v1/customers/chat-link", nil)["code"].(string)
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"code": code})
	if chat["name"] != "Bola Bakes" {
		t.Fatalf("from the link: %v", chat)
	}
	var hashes int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'customer.start' AND detail::text LIKE '%code_hash%'`, bolaID).Scan(&hashes); err != nil || hashes != 0 {
		t.Fatalf("the code's hash in the audit: %d %v", hashes, err)
	}

	// A new link: the old one says so, the new one works.
	fresh := bola.must(200, "POST", "/v1/customers/chat-link", nil)["code"].(string)
	ade, _ := w.personIn(t, "+2348031110022")
	if got := ade.must(410, "POST", "/v1/me/chats", map[string]any{"code": code}); got["link_replaced"] != true {
		t.Fatalf("old link: %v", got)
	}
	ade.must(201, "POST", "/v1/me/chats", map[string]any{"code": fresh})

	// Chats turned off: no new ones, even from the shop.
	w.owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"takes_chats": false})
	tolu.must(409, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})
	w.owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"takes_chats": true})
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Hello", "client_id": "h1"})

	// The business answers: the person told once; muted, not told.
	var thread string
	for _, c := range w.owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any) {
		if c.(map[string]any)["name"] == "Tolu Ade" {
			thread = c.(map[string]any)["id"].(string)
		}
	}
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Hi Tolu", "client_id": "a1"})
	if tell.n() != 1 || tell.got[0].Tenant != w.biz || tell.got[0].Person != toluID {
		t.Fatalf("told: %+v", tell.got)
	}
	tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"muted": true})
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Still there?", "client_id": "a2"})
	if tell.n() != 1 {
		t.Fatalf("a muted chat was told: %d", tell.n())
	}
}

// doorStartWith calls the door as the person, in a transaction of its own.
func doorStartWith(ctx context.Context, w *doorShopWorld, person, tenant uuid.UUID, detail map[string]any) error {
	tx, err := w.env.DB.App.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.person_scope', $1, true)`, person.String()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.start', $3)`, person, tenant, detail)
	return err
}
