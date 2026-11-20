package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The person's account on You ("As built: stage 3, Orders and account
// review fixes" in docs/spec/taskiem-for-everyone.md): deleting it, their
// photo, which pushes they get, the privacy page.

// putFile puts a file to path as a multipart form.
func (c *client) putFile(t *testing.T, path, name string, data []byte) (int, map[string]any) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPut, c.base+path, &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// Deleting an account (§14, decision B): refused for a business's owner;
// a fresh proof needed; then the person's side goes at once, every
// session ends, each business they chatted with is told only "This
// customer left Taskiem" and keeps its records, and the number's next
// holder finds none of the account's orders, by any way.
func TestDeleteAccount(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031150101"

	// An owner cannot delete their account while they own the business.
	if e := w.owner.must(409, "POST", "/v1/me/delete/start", nil); e["owns_business"] != true || len(e["businesses"].([]any)) != 1 {
		t.Fatalf("an owner: %v", e)
	}
	w.owner.must(409, "POST", "/v1/me/delete", map[string]any{"confirm": true, "password": "correct horse battery"})

	// Before Tolu's account: an order made with the number (waiting).
	early := w.placeByPhone(t, w.biz, number, storefront.ChatPayLater, nil)
	time.Sleep(10 * time.Millisecond)
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+chat+"/messages", map[string]any{"text": "Hello, is the jollof ready?"})
	app := w.placeByPhone(t, w.biz, number, storefront.ChatPayLater, &toluID)
	byPhone := w.placeByPhone(t, w.biz, number, storefront.ChatPayLater, nil)
	tolu.must(201, "POST", "/v1/me/addresses", map[string]any{"label": "Home", "line": "12 Allen Avenue"})
	tolu.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mum", "bank_name": "Zenith Bank", "account_number": "2222222222"})
	tolu.must(201, "POST", "/v1/me/saved-kinds", map[string]any{"name": "Car", "fields": []map[string]any{{"label": "Plate number", "type": "text"}}})
	tolu.must(200, "PUT", "/v1/me/notifications", map[string]any{"offers": false})
	thread := w.threadOf(t, toluID)

	// A fresh proof is needed, and confirm.
	tolu.must(400, "POST", "/v1/me/delete", map[string]any{"code": "123456"})
	if e := tolu.must(403, "POST", "/v1/me/delete", map[string]any{"confirm": true}); e["reauth"] == nil {
		t.Fatalf("no proof: %v", e)
	}
	if e := tolu.must(401, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": "000000"}); e["code_wrong"] != true {
		t.Fatalf("no code asked for: %v", e)
	}
	tolu.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	code := w.sms.code(t, number)
	// A wrong code is refused (and counted, as every code is).
	if e := tolu.must(401, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": "111111"}); e["code_wrong"] != true {
		t.Fatalf("a wrong code: %v", e)
	}
	out := tolu.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": code})
	if out["deleted"] != true || out["businesses_told"] != float64(1) {
		t.Fatalf("deleted: %v", out)
	}

	// Every session ended.
	if st, _ := tolu.do("GET", "/v1/me", nil); st != http.StatusUnauthorized {
		t.Fatalf("the session after deleting: %d", st)
	}
	// The account row emptied and kept; the person's side gone.
	var name, email, status string
	var deleted *time.Time
	var phones, addresses, chats, messages, settings, sessions int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT name, email, status, deleted_at,
		(SELECT count(*) FROM user_phones WHERE user_id = $1), (SELECT count(*) FROM saved_objects WHERE person_id = $1 AND kind = 'place'),
		(SELECT count(*) FROM person_conversations WHERE person_id = $1), (SELECT count(*) FROM person_messages WHERE person_id = $1),
		(SELECT count(*) FROM person_notify WHERE person_id = $1), (SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL)
		FROM users WHERE id = $1`, toluID).Scan(&name, &email, &status, &deleted, &phones, &addresses, &chats, &messages, &settings, &sessions); err != nil {
		t.Fatal(err)
	}
	var savedLeft int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM saved_objects WHERE person_id = $1) + (SELECT count(*) FROM saved_object_events WHERE person_id = $1) + (SELECT count(*) FROM saved_kinds WHERE person_id = $1)`,
		toluID).Scan(&savedLeft); err != nil || savedLeft != 0 {
		t.Fatalf("saved things left: %d %v", savedLeft, err)
	}
	if name != "" || !strings.HasSuffix(email, "@deleted.taskiem.invalid") || status != "disabled" || deleted == nil ||
		phones+addresses+chats+messages+settings+sessions != 0 {
		t.Fatalf("after deleting: %q %q %s %v, rows %d %d %d %d %d %d", name, email, status, deleted, phones, addresses, chats, messages, settings, sessions)
	}

	// The business keeps its thread and its orders, told only that the
	// customer left; it can write no more.
	th := w.owner.must(200, "GET", "/v1/customer-chats/"+thread, nil)
	if th["notice"] != "This customer left Taskiem" || th["window_open"] != false {
		t.Fatalf("the business's thread: %v", th)
	}
	msgs := w.owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if len(msgs) != 2 || last["body"] != "This customer left Taskiem." || last["sender_name"] != "Taskiem" ||
		msgs[0].(map[string]any)["body"] != "Hello, is the jollof ready?" {
		t.Fatalf("the business's messages: %v", msgs)
	}
	w.owner.must(409, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Are you there?"})
	if so := w.owner.must(200, "GET", "/v1/storefront/orders/"+app.Reference, nil)["order"].(map[string]any); so["reference"] != app.Reference {
		t.Fatalf("the business's order: %v", so)
	}
	if w.audits(t, "customer.left", toluID) != 1 {
		t.Fatal("the business's chain has no customer.left")
	}
	var platform int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'account.delete' AND actor_id = $2 AND detail ->> 'how' = 'phone_code'`,
		ops.PlatformChain, toluID.String()).Scan(&platform); err != nil || platform != 1 {
		t.Fatalf("the platform's chain: %d %v", platform, err)
	}

	// The phone index: nothing under the number, nothing of the account.
	var indexed, unmarked int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM customer_phone_index WHERE order_id = ANY ($1)),
		(SELECT count(*) FROM storefront_orders WHERE id = $2 AND (person_id IS NOT NULL OR NOT person_left))`,
		[]uuid.UUID{early.ID, app.ID, byPhone.ID}, app.ID).Scan(&indexed, &unmarked); err != nil || indexed != 0 || unmarked != 0 {
		t.Fatalf("the index after deleting: %d rows, %d unmarked (%v)", indexed, unmarked, err)
	}
	// The number's next holder finds none of it, even after "These are mine".
	next, _ := w.personIn(t, number)
	if got := next.must(200, "GET", "/v1/me/orders", nil); len(got["orders"].([]any)) != 0 || got["waiting_count"] != float64(0) {
		t.Fatalf("the next holder's orders: %v", got)
	}
	next.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	if got := next.must(200, "GET", "/v1/me/orders", nil); len(got["orders"].([]any)) != 0 {
		t.Fatalf("the next holder's orders after These are mine: %v", got)
	}
	if got := next.must(200, "GET", "/v1/me/storefront-orders", nil); len(got["orders"].([]any)) != 0 {
		t.Fatalf("the next holder's phone orders: %v", got)
	}
}

// A users row deleted by hand (not through the route): the app orders it
// made are taken out of the index and marked, so the number's next holder
// never gets them, address and all, through "These are mine" (decision
// B's bug: person_id was set null and the order looked like anyone's).
func TestDeletedUsersRowReleasesItsOrders(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031150201"
	ade, _ := w.personIn(t, number)
	adeID := personID(t, ade)
	app := w.placeByPhone(t, w.biz, number, storefront.ChatPayLater, &adeID)
	for _, q := range []string{`DELETE FROM sessions WHERE user_id = $1`, `DELETE FROM user_phones WHERE user_id = $1`, `DELETE FROM users WHERE id = $1`} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, adeID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var left bool
	var person *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT person_left, person_id FROM storefront_orders WHERE id = $1`, app.ID).Scan(&left, &person); err != nil || !left || person != nil {
		t.Fatalf("the order: left %v person %v (%v)", left, person, err)
	}
	// Even put back in the index (a fill), it is no one's by phone.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO customer_phone_index (order_id, tenant_id, phone_hash) VALUES ($1, $2, $3)`,
		app.ID, w.biz, w.srv.PhoneIndex.Hash(number)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	next, _ := w.personIn(t, number)
	if got := next.must(200, "GET", "/v1/me/orders", nil); len(got["orders"].([]any)) != 0 || got["waiting_count"] != float64(0) {
		t.Fatalf("the next holder: %v", got)
	}
	next.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	if got := next.must(200, "GET", "/v1/me/orders", nil); len(got["orders"].([]any)) != 0 {
		t.Fatalf("the next holder after These are mine: %v", got)
	}
	next.must(404, "GET", "/v1/me/orders/"+w.biz.String()+"/"+app.Reference, nil)
}

// You's settings: the person's photo (theirs only), which pushes they
// get, and the privacy page.
func TestYouSettings(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031150301"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)

	// The photo.
	tolu.must(404, "GET", "/v1/me/photo", nil)
	if me := tolu.must(200, "GET", "/v1/me", nil)["user"].(map[string]any); me["photo"] != nil {
		t.Fatalf("no photo yet: %v", me)
	}
	if st, e := tolu.putFile(t, "/v1/me/photo", "notes.txt", []byte("not a photo at all")); st != 400 || !strings.Contains(e["error"].(string), "cannot be used") {
		t.Fatalf("a text file: %d %v", st, e)
	}
	st, got := tolu.putFile(t, "/v1/me/photo", "me.png", pngPhoto(t, 90))
	if st != 200 || got["photo"] == nil {
		t.Fatalf("a photo: %d %v", st, got)
	}
	code, hdr, body := tolu.getRaw(t, "/v1/me/photo")
	if code != 200 || hdr.Get("Content-Type") != "image/jpeg" || len(body) < 100 {
		t.Fatalf("the photo back: %d %s %d", code, hdr.Get("Content-Type"), len(body))
	}
	if me := tolu.must(200, "GET", "/v1/me", nil)["user"].(map[string]any); me["photo"] == nil {
		t.Fatalf("/v1/me without the photo: %v", me)
	}
	ade, _ := w.personIn(t, "+2348031150302")
	ade.must(404, "GET", "/v1/me/photo", nil)
	tolu.must(204, "DELETE", "/v1/me/photo", nil)
	tolu.must(404, "GET", "/v1/me/photo", nil)

	// Pushes: all on, then offers off; an offer is then told no one (the
	// pusher answers it handled, so no SMS goes either).
	if n := tolu.must(200, "GET", "/v1/me/notifications", nil); n["messages"] != true || n["updates"] != true || n["offers"] != true {
		t.Fatalf("the default: %v", n)
	}
	if n := tolu.must(200, "PUT", "/v1/me/notifications", map[string]any{"offers": false}); n["offers"] != false || n["messages"] != true {
		t.Fatalf("offers off: %v", n)
	}
	if n := tolu.must(200, "PUT", "/v1/me/notifications", map[string]any{"updates": false}); n["offers"] != false || n["updates"] != false || n["messages"] != true {
		t.Fatalf("updates off too: %v", n)
	}
	pp := w.srv.PersonPusher()
	if sent, err := pp.PushPerson(ctx, toluID, chatlinks.PersonPush{Title: "Mama's Kitchen", Body: "20% off", Kind: chatlinks.KindOffers}); err != nil || !sent {
		t.Fatalf("an offer turned off: %v %v", sent, err)
	}
	if sent, err := pp.PushPerson(ctx, toluID, chatlinks.PersonPush{Title: "Mama's Kitchen", Body: "Hello", Kind: chatlinks.KindMessages}); err != nil || sent {
		t.Fatalf("a message (no device here): %v %v", sent, err)
	}
	if k := chatlinks.KindOf(chatlinks.Answer{FromPerson: false}); k != chatlinks.KindUpdates {
		t.Fatalf("an automatic word's kind: %s", k)
	}

	// The privacy page: what businesses see, who has the number.
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	ada := w.tenant(t, "Ada Stores", "ada@ada.test")
	adaID := tenantUUID(t, ada)
	w.placeByPhone(t, adaID, number, storefront.ChatPayLater, nil)
	w.placeByPhone(t, adaID, number, storefront.ChatPayLater, nil)
	p := tolu.must(200, "GET", "/v1/me/privacy", nil)
	shown := p["shown_to_businesses"].(map[string]any)
	if shown["name"] != "Tolu Ade" || shown["phone"] != number || shown["photo"] != false {
		t.Fatalf("what businesses see: %v", shown)
	}
	got2 := map[string]map[string]any{}
	for _, b := range p["businesses"].([]any) {
		m := b.(map[string]any)
		got2[m["business"].(string)] = m
	}
	if a := got2["Ada Stores"]; a == nil || a["orders"] != float64(2) || a["chat_id"] != nil {
		t.Fatalf("Ada Stores: %v", p["businesses"])
	}
	if m := got2["Mama's Kitchen"]; m == nil || m["chat_id"] != chat || m["orders"] != float64(0) {
		t.Fatalf("Mama's Kitchen: %v", p["businesses"])
	}
	if e := p["erase"].(map[string]any); !strings.Contains(e["words"].(string), "ask it in your chat") {
		t.Fatalf("how to erase: %v", e)
	}
	if got := ade.must(200, "GET", "/v1/me/privacy", nil); len(got["businesses"].([]any)) != 0 {
		t.Fatalf("another person's page: %v", got)
	}
}
