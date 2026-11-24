package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The review of stage 2's conversations ("As built: stage 2, review fixes
// (conversations)" in docs/spec/taskiem-for-everyone.md).

// threadOf is the business's one thread with a person in the app.
func (w *doorShopWorld) threadOf(t *testing.T, person uuid.UUID) string {
	t.Helper()
	var id uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM customer_conversations WHERE tenant_id = $1 AND person_id = $2 AND line = 'app'`,
		w.biz, person).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// The way back is the business's alone. A person's transaction that went
// through the door has the business in scope, and once reached its
// customer's copy opened the users row too: now reach refuses a person's
// transaction (for another person, or for themself), and a business that
// reached a person sees that person's copy with it, never their users row.
func TestCustomerDoorReachChainRefused(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	tolu, _ := w.personIn(t, "+2348031120001")
	ada, _ := w.personIn(t, "+2348031120002")
	toluID, adaID := personID(t, tolu), personID(t, ada)
	tc := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+tc+"/messages", map[string]any{"text": "Tolu's words"})
	ac := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	ada.must(201, "POST", "/v1/me/chats/"+ac+"/messages", map[string]any{"text": "Ada's words"})
	app := w.env.DB.App

	// Ada, through the door into Mama's Kitchen, reaching Tolu: refused.
	err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.chat', '{}')`, adaID, w.biz); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, 'customer.read', '{}')`, w.biz, toluID)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "business's own transaction") {
		t.Fatalf("a person reaching another through the door: %v", err)
	}
	// Nor herself (the chain is refused whoever it names), nor with no door.
	for _, enter := range []bool{true, false} {
		err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
			if enter {
				if _, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.chat', '{}')`, adaID, w.biz); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, 'customer.message', '{}')`, w.biz, adaID)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "business's own transaction") {
			t.Fatalf("a person reaching (door %v): %v", enter, err)
		}
	}
	// Through the door, typing is the person's own side only, and never
	// read of another person.
	err = db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.chat', '{}')`, adaID, w.biz); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_customer_typing($1, $2, 'business')`, w.biz, toluID)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "own side") {
		t.Fatalf("a person typing as the business: %v", err)
	}
	tolu.must(204, "POST", "/v1/me/chats/"+tc+"/typing", nil)
	if err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.chat', '{}')`, adaID, w.biz); err != nil {
			return err
		}
		var typing bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_customer_typing_of($1, $2, 'person')`, w.biz, toluID).Scan(&typing); err != nil || typing {
			return fmt.Errorf("Ada read Tolu typing: %v %w", typing, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The business reaching Tolu: his copy with it, and no users row.
	if err := db.InTenantTx(ctx, app, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, 'customer.read', '{}')`, w.biz, toluID); err != nil {
			return err
		}
		var users, mine, others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, toluID).Scan(&users); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE person_id = $1), count(*) FILTER (WHERE person_id <> $1) FROM person_messages`, toluID).Scan(&mine, &others); err != nil {
			return err
		}
		if users != 0 || mine != 1 || others != 0 {
			return fmt.Errorf("reached: users row %d, Tolu's messages %d, others' %d", users, mine, others)
		}
		// Reaching Ada next: hers only, Tolu's no more.
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, 'customer.read', '{}')`, w.biz, adaID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE person_id = $1), count(*) FILTER (WHERE person_id <> $1) FROM person_messages`, adaID).Scan(&mine, &others); err != nil {
			return err
		}
		if mine != 1 || others != 0 {
			return fmt.Errorf("reached Ada: hers %d, others' %d", mine, others)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A phone that changes hands: the new holder of the number gets a thread
// of their own; the old holder's history never becomes theirs, and the
// old holder goes on writing in their own.
func TestCustomerDoorThreadsNeverRepointed(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	const number = "+2348031120011"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	tc := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+tc+"/messages", map[string]any{"text": "Tolu's private order"})
	toluThread := w.threadOf(t, toluID)

	// Tolu moves to a new number; the old one is given to Ada.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE user_phones SET number = '+2348031120012' WHERE user_id = $1`, toluID); err != nil {
		t.Fatal(err)
	}
	ada, _ := w.personIn(t, number)
	adaID := personID(t, ada)
	if adaID == toluID {
		t.Fatal("the number signed in to Tolu's account")
	}
	ac := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	if msgs := ada.must(200, "GET", "/v1/me/chats/"+ac+"/messages", nil)["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("Ada sees: %v", msgs)
	}
	ada.must(201, "POST", "/v1/me/chats/"+ac+"/messages", map[string]any{"text": "Ada here"})
	adaThread := w.threadOf(t, adaID)
	if adaThread == toluThread {
		t.Fatal("Ada was given Tolu's thread")
	}
	if w.threadOf(t, toluID) != toluThread {
		t.Fatal("Tolu's thread moved")
	}
	// Each thread its own history, on the business's side.
	for thread, want := range map[string]string{toluThread: "Tolu's private order", adaThread: "Ada here"} {
		bm := w.owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
		if len(bm) != 1 || bm[0].(map[string]any)["body"] != want {
			t.Fatalf("thread %s: %v", thread, bm)
		}
	}
	// Tolu goes on, and the business answers each in their own.
	tolu.must(201, "POST", "/v1/me/chats/"+tc+"/messages", map[string]any{"text": "Still me"})
	w.owner.must(201, "POST", "/v1/customer-chats/"+toluThread+"/messages", map[string]any{"text": "Yes Tolu"})
	if got := ada.must(200, "GET", "/v1/me/chats/"+ac+"/messages", nil)["messages"].([]any); len(got) != 1 {
		t.Fatalf("Ada got Tolu's answer: %v", got)
	}
	if got := tolu.must(200, "GET", "/v1/me/chats/"+tc+"/messages", nil)["messages"].([]any); len(got) != 3 {
		t.Fatalf("Tolu's copy: %v", got)
	}
}

// Waiting (§8.6): from the first message no one answered, through three
// messages; not ended by Taskiem's closed reply or an offer; ended by an
// answer. Counted in open hours of the published page: Needs you after
// 30 open minutes, the owner told once after 2, in words with no name;
// not while the customer has blocked the business.
func TestCustomerDoorWaitingSince(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	owner := w.owner
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	tolu, _ := w.personIn(t, "+2348031120021")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	waiting := func() *time.Time {
		t.Helper()
		var at *time.Time
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT waiting_since FROM customer_conversations WHERE tenant_id = $1 AND person_id = $2`, w.biz, toluID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if waiting() != nil {
		t.Fatal("waiting before writing")
	}
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "One"})
	first := waiting()
	for _, x := range []string{"Two", "Three"} {
		time.Sleep(5 * time.Millisecond)
		tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": x})
	}
	if again := waiting(); first == nil || again == nil || !again.Equal(*first) {
		t.Fatalf("waiting since: %v, then %v", first, again)
	}
	thread := w.threadOf(t, toluID)
	// An offer is not an answer.
	tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"offers": true})
	owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "10% off", "offer": true})
	if waiting() == nil {
		t.Fatal("an offer ended the wait")
	}
	// The answer is.
	owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Hello Tolu"})
	if waiting() != nil {
		t.Fatal("still waiting after the answer")
	}
	// Closed now (the published page): the closed reply is not an answer.
	w.hours(t, false)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Are you open?"})
	if waiting() == nil {
		t.Fatal("the closed reply ended the wait")
	}
	// Waiting 40 minutes by the clock, all of them closed: not on Needs you,
	// and the owner not told even after 3 hours.
	needsYou := func() bool {
		t.Helper()
		for _, a := range owner.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
			if a := a.(map[string]any); a["kind"] == "customer_waiting" && a["id"] == thread {
				return true
			}
		}
		return false
	}
	setSince := func(ago string) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET waiting_since = now() - $2::interval, wait_check_at = NULL WHERE id = $1`, thread, ago); err != nil {
			t.Fatal(err)
		}
	}
	setSince("40 minutes")
	if needsYou() {
		t.Fatal("on Needs you after 40 closed minutes")
	}
	setSince("3 hours")
	if err := w.srv.CustomerChatsTick(ctx); err != nil {
		t.Fatal(err)
	}
	told := func() int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE tenant_id = $1 AND group_key = $2`, w.biz, "customer_waiting:"+thread).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if told() != 0 {
		t.Fatal("the owner told of a wait in closed hours")
	}
	var later *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT wait_check_at FROM customer_conversations WHERE id = $1`, thread).Scan(&later); err != nil || later == nil || !later.After(time.Now()) {
		t.Fatalf("not left until it may be due: %v %v", later, err)
	}
	// Open all day: Needs you, and the owner told once, with no name.
	w.hours(t, true)
	setSince("40 minutes")
	if !needsYou() {
		t.Fatal("not on Needs you after 40 open minutes")
	}
	setSince("3 hours")
	for range 2 {
		if err := w.srv.CustomerChatsTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if told() != 1 {
		t.Fatalf("told %d times", told())
	}
	var title string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT title FROM notifications WHERE tenant_id = $1 AND group_key = $2`, w.biz, "customer_waiting:"+thread).Scan(&title); err != nil ||
		title != "A customer has waited 2 hours for an answer" {
		t.Fatalf("the owner's notice: %q %v", title, err)
	}
	// The customer blocked the business: not waiting.
	tolu.must(200, "POST", "/v1/me/chats/"+id+"/block", nil)
	setSince("40 minutes")
	if needsYou() {
		t.Fatal("a customer who blocked the business is waiting")
	}
}

// openBetween counts only the open minutes of the hours.
func TestOpenBetween(t *testing.T) {
	var h storefront.Hours
	for d := range 7 {
		h.Days[d] = []storefront.Span{{Open: "09:00", Close: "17:00"}}
	}
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 11, day, hh, mm, 0, 0, storefront.Lagos) }
	for _, c := range []struct {
		from, to time.Time
		want     time.Duration
	}{
		{at(16, 8, 0), at(16, 10, 0), time.Hour},
		{at(16, 16, 0), at(17, 10, 0), 2 * time.Hour},
		{at(16, 18, 0), at(16, 23, 0), 0},
		{at(16, 9, 30), at(16, 9, 45), 15 * time.Minute},
	} {
		if got := api.OpenBetween(h, true, c.from, c.to); got != c.want {
			t.Errorf("%v to %v: %v, want %v", c.from, c.to, got, c.want)
		}
	}
	// Overlapping and past midnight: counted once.
	var late storefront.Hours
	for d := range 7 {
		late.Days[d] = []storefront.Span{{Open: "20:00", Close: "02:00"}, {Open: "00:00", Close: "01:00"}}
	}
	if got := api.OpenBetween(late, true, at(16, 19, 0), at(17, 3, 0)); got != 6*time.Hour {
		t.Errorf("past midnight: %v", got)
	}
	if got := api.OpenBetween(storefront.Hours{}, false, at(16, 1, 0), at(16, 2, 0)); got != time.Hour {
		t.Errorf("no hours: %v", got)
	}
}

// The owners' sweep takes the oldest waits first and leaves a closed
// business's until it may be due, so 600 customers waiting at a closed
// business never crowd out one waiting at an open one.
func TestCustomerChatsSweepNotStarved(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	tolu, _ := w.personIn(t, "+2348031120031")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Hello"})
	thread := w.threadOf(t, toluID)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET waiting_since = now() - interval '3 hours' WHERE id = $1`, thread); err != nil {
		t.Fatal(err)
	}
	closed := w.tenant(t, "Night Shop", "owner@night.test")
	verifyPhone(t, w.world, closed, "phone", "business")
	closed.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "night-shop", "draft": map[string]any{"name": "Night Shop", "hours": hoursOpen(false)}})
	closed.must(200, "POST", "/v1/storefront/publish", nil)
	night := tenantIDNamed(t, w.world, "Night Shop")
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO customer_conversations (id, tenant_id, phone_number_id, subject_id, who, line, last_inbound_at, waiting_since)
		SELECT gen_random_uuid(), $1, 'app', 'subject-' || g, '{}', 'app', now() - interval '5 hours', now() - interval '5 hours' - g * interval '1 second'
		  FROM generate_series(1, 600) g`, night); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := w.srv.CustomerChatsTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var told int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE tenant_id = $1 AND group_key = $2`, w.biz, "customer_waiting:"+thread).Scan(&told); err != nil || told != 1 {
		t.Fatalf("the open business's owner told %d times: %v", told, err)
	}
	var due int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM taskiem_customer_waiting('2 hours') WHERE tenant_id = $1`, night).Scan(&due); err != nil || due != 0 {
		t.Fatalf("the closed business's still due: %d %v", due, err)
	}
}

// Offers sent at once are counted one after the other: two a week, never
// more, however many go together.
func TestCustomerDoorConcurrentOffers(t *testing.T) {
	w := newDoorShopWorld(t)
	tolu, _ := w.personIn(t, "+2348031120041")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"offers": true})
	thread := w.threadOf(t, personID(t, tolu))
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = w.owner.do("POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": fmt.Sprintf("Offer %d", i), "offer": true})
		}()
	}
	wg.Wait()
	sent := 0
	for _, c := range codes {
		switch c {
		case 201:
			sent++
		case 429:
		default:
			t.Fatalf("codes: %v", codes)
		}
	}
	if sent != 2 {
		t.Fatalf("%d offers sent at once: %v", sent, codes)
	}
}

// After the business erases an app customer: both sides are told plainly
// (409, not a failure), and the business's copies of their files are
// deleted from its storage.
func TestCustomerDoorErasedCustomer(t *testing.T) {
	w := newDoorShopWorld(t)
	ps := newProjectStorage(t)
	w.srv.Media = ps.service()
	w.srv.Media.Scanner = chatScanner{}
	tolu, _ := w.personIn(t, "+2348031120051")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	if code, out := tolu.postFile(t, "/v1/me/chats/"+id+"/files", "my receipt.png", pngPhoto(t, 40), nil); code != 201 {
		t.Fatalf("a photo: %d %v", code, out)
	}
	thread := w.threadOf(t, personID(t, tolu))
	// The file's name is sealed under the customer, and read back.
	var clear int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM customer_messages WHERE tenant_id = $1 AND attachment::text LIKE '%receipt%'`, w.biz).Scan(&clear); err != nil || clear != 0 {
		t.Fatalf("the file's name in the clear: %d %v", clear, err)
	}
	bm := w.owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
	if a := bm[0].(map[string]any)["attachment"].(map[string]any); a["name"] != "my receipt.png" {
		t.Fatalf("the business's copy: %v", a)
	}
	files := func() int {
		// Only the chat's file is in this storage.
		ps.mu.Lock()
		defer ps.mu.Unlock()
		return len(ps.files)
	}
	if files() == 0 {
		t.Fatal("the business kept no copy")
	}
	secret := enrol(t, w.owner)
	w.owner.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "phone", "value": "+2348031120051", "totp": codeAfter(secret, 1)})
	if n := files(); n != 0 {
		t.Fatalf("%d files left in the business's storage", n)
	}
	if e := w.owner.must(409, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Hello?"}); !strings.Contains(e["error"].(string), "erased") {
		t.Fatalf("writing to an erased customer: %v", e)
	}
	if e := tolu.must(409, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Hello?"}); !strings.Contains(e["error"].(string), "erased") {
		t.Fatalf("writing after the erasure: %v", e)
	}
	// The person's own copy is theirs still.
	if got := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any); len(got) != 1 {
		t.Fatalf("the person's copy: %v", got)
	}
}

// A closed reply that fails keeps the customer's message: it is in a
// savepoint, and logged.
func TestCustomerDoorClosedReplyFailureKeepsMessage(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.hours(t, false)
	w.srv.SetClosedReplyFault(func() error { return fmt.Errorf("the closed reply broke") })
	tolu, _ := w.personIn(t, "+2348031120061")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Kept"})
	thread := w.threadOf(t, personID(t, tolu))
	bm := w.owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
	if len(bm) != 1 || bm[0].(map[string]any)["body"] != "Kept" {
		t.Fatalf("the business's copy: %v", bm)
	}
	var claimed *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT closed_reply_at FROM customer_conversations WHERE id = $1`, thread).Scan(&claimed); err != nil || claimed != nil {
		t.Fatalf("the failed reply's claim kept: %v %v", claimed, err)
	}
	// Once it works again, the next message gets it.
	w.srv.SetClosedReplyFault(nil)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Again"})
	if got := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any); len(got) != 3 {
		t.Fatalf("the person's copy: %v", got)
	}
}

// Typing is refused while either side blocked the other; a business's
// block leaves the person's mute and archive theirs, refusing only the
// answer to offers; the offers question, shown and passed over, is not
// asked again; the person's account gone, the business keeps its record
// of their answer.
func TestCustomerDoorBlocksTypingAndOffersAsked(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	tolu, _ := w.personIn(t, "+2348031120071")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	thread := w.threadOf(t, toluID)

	if got := tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"offers_asked": true}); got["ask_offers"] != false || got["offers"] != "unasked" {
		t.Fatalf("passed over: %v", got)
	}
	if got := tolu.must(200, "GET", "/v1/me/chats/"+id, nil); got["ask_offers"] != false {
		t.Fatalf("asked again: %v", got)
	}

	w.owner.must(204, "POST", "/v1/customer-chats/"+thread+"/block", nil)
	if e := tolu.must(403, "POST", "/v1/me/chats/"+id+"/typing", nil); e["blocked"] != true {
		t.Fatalf("typing while blocked: %v", e)
	}
	if got := tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"muted": true, "archived": true, "offers": true}); got["muted"] != true || got["archived"] != true ||
		got["offers"] != "unasked" || got["offers_refused"] != true {
		t.Fatalf("mute and archive while blocked: %v", got)
	}
	tolu.must(403, "PUT", "/v1/me/chats/"+id, map[string]any{"offers": true})
	w.owner.must(204, "DELETE", "/v1/customer-chats/"+thread+"/block", nil)

	tolu.must(200, "POST", "/v1/me/chats/"+id+"/block", nil)
	w.owner.must(409, "POST", "/v1/customer-chats/"+thread+"/typing", nil)
	if got := w.owner.must(200, "GET", "/v1/customer-chats/"+thread, nil); got["typing"] != false {
		t.Fatalf("typing seen while blocked: %v", got)
	}
	tolu.must(200, "DELETE", "/v1/me/chats/"+id+"/block", nil)

	tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"offers": true})
	// The account deleted (its sign-ins first: there is no deleting path
	// yet, §14).
	for _, q := range []string{`DELETE FROM sessions WHERE user_id = $1`, `DELETE FROM user_phones WHERE user_id = $1`} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, toluID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM users WHERE id = $1`, toluID); err != nil {
		t.Skipf("an account cannot be deleted here: %v", err)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_offer_consents WHERE tenant_id = $1 AND person_id IS NULL AND answer`, w.biz).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the business's record of the answer: %d %v", n, err)
	}
}

// The conversation's header: the business's photo (when it has one) and
// what it has proved; a business message asking for a transfer to an
// account is marked.
func TestCustomerDoorHeaderAndTransferWarning(t *testing.T) {
	w := newDoorShopWorld(t)
	tolu, _ := w.personIn(t, "+2348031120081")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	got := tolu.must(200, "GET", "/v1/me/chats/"+id, nil)
	v, _ := got["verified"].(map[string]any)
	if v == nil || v["phone"] != true || v["payments"] != false || v["months"] != float64(0) {
		t.Fatalf("verified: %v", got)
	}
	thread := w.threadOf(t, personID(t, tolu))
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Pay into 0123456789 GTBank, Ada Obi"})
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Call me on 08031234567"})
	msgs := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if msgs[0].(map[string]any)["warn_transfer"] != true || msgs[1].(map[string]any)["warn_transfer"] != nil {
		t.Fatalf("warnings: %v", msgs)
	}
}

func TestWarnTransfer(t *testing.T) {
	for text, want := range map[string]bool{
		"Pay into 0123456789 GTBank":                        true,
		"Send it to my opay 012 345 6789":                   true,
		"Account number: 0123-456-789, Access Bank":         true,
		"Acct 0123456789 zenith":                            true,
		"Call me on 08031234567":                            false,
		"Your order 0123456789 is ready":                    false,
		"Transfer to 01234567890 first bank":                false, // eleven digits: a phone
		"Bank: GTBank":                                      false,
		"Pay with the link: https://pay.example/0123456789": false,
	} {
		if got := api.WarnTransfer(text); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}

// An order confirmed from a draft in an app thread is the person's: linked
// to them, listed on their side, and its news told in their conversation,
// found by the person (never by the phone's subject).
func TestCustomerDoorDraftInAppThread(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	tolu, _ := w.personIn(t, "+2348031120091")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "One chapman please, I'll pick it up"})
	var item string
	for _, x := range tolu.must(200, "GET", "/v1/me/chats/"+id+"/shop", nil)["items"].([]any) {
		if x.(map[string]any)["name"] == "Chapman" {
			item = x.(map[string]any)["id"].(string)
		}
	}
	thread := w.threadOf(t, toluID)
	d := w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/orders", nil)
	d = w.owner.must(200, "PUT", "/v1/order-drafts/"+d["id"].(string), map[string]any{"version": d["version"], "op": "add", "item_id": item, "quantity": 1})
	d = w.owner.must(200, "PUT", "/v1/order-drafts/"+d["id"].(string), map[string]any{"version": d["version"], "op": "details", "fulfilment": "pickup", "pay": "later"})
	out := w.owner.must(200, "POST", "/v1/order-drafts/"+d["id"].(string)+"/confirm", map[string]any{"version": d["version"]})
	ref := out["order"].(map[string]any)["reference"].(string)
	var person *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT person_id FROM storefront_orders WHERE reference = $1`, ref).Scan(&person); err != nil || person == nil || *person != toluID {
		t.Fatalf("the order's person: %v %v", person, err)
	}
	if mine := tolu.must(200, "GET", "/v1/me/chats/"+id+"/orders", nil)["orders"].([]any); len(mine) != 1 || mine[0].(map[string]any)["reference"] != ref {
		t.Fatalf("your orders: %v", mine)
	}
	told, err := w.srv.Orders.NotifyCustomer(ctx, w.biz, ref, "Your chapman is ready", "", "ready:"+ref)
	if err != nil {
		t.Fatal(err)
	}
	if q := told["queued"].([]string); len(q) != 1 || q[0] != "app" {
		t.Fatalf("told by: %v", told)
	}
	pm := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if last := pm[len(pm)-1].(map[string]any); last["body"] != "Your chapman is ready" {
		t.Fatalf("told in the chat: %v", last)
	}
}

// A delivery card: the rider's progress on the customer's order, once a
// rider holds it; the person's orders follow it as it is.
func TestCustomerDoorDeliveryCard(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	tolu, _ := w.personIn(t, "+2348031120111")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	var item string
	for _, x := range tolu.must(200, "GET", "/v1/me/chats/"+id+"/shop", nil)["items"].([]any) {
		item = x.(map[string]any)["id"].(string)
	}
	o := tolu.must(201, "POST", "/v1/me/chats/"+id+"/orders", map[string]any{"lines": []any{map[string]any{"item_id": item, "quantity": 1}},
		"fulfilment": "delivery", "zone": "Yaba", "address": "12 Herbert Macaulay Way"}, "Idempotency-Key", "d1")["order"].(map[string]any)
	ref := o["reference"].(string)
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	thread := w.threadOf(t, toluID)
	w.owner.must(409, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"card": map[string]any{"type": "delivery", "reference": ref}})
	rider := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO riders (id, tenant_id, phone_hash, phone, name, created_by) VALUES ($1, $2, $3, '{}', '{}', 'test')`,
		rider, w.biz, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO deliveries (id, tenant_id, order_id, rider_id, status, offered_by, expires_at, accepted_at, picked_up_at)
		SELECT gen_random_uuid(), $1, o.id, $2, 'picked_up', 'test', now() + interval '1 hour', now() - interval '10 minutes', now() FROM storefront_orders o WHERE o.reference = $3`,
		w.biz, rider, ref); err != nil {
		t.Fatal(err)
	}
	c := w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"card": map[string]any{"type": "delivery", "reference": ref}})["card"].(map[string]any)
	if c["type"] != "delivery" || c["status"] != "picked_up" || c["delivery"].(map[string]any)["line"] != "On its way to you" {
		t.Fatalf("the delivery card: %v", c)
	}
	mine := tolu.must(200, "GET", "/v1/me/chats/"+id+"/orders", nil)["orders"].([]any)
	if d, _ := mine[0].(map[string]any)["delivery"].(map[string]any); d == nil || d["status"] != "picked_up" {
		t.Fatalf("your orders: %v", mine)
	}
}

// Stock counts an item once across the lines it is on.
func TestCustomerDoorStockAcrossLines(t *testing.T) {
	w := newDoorShopWorld(t)
	owner := w.owner
	withStock(w.world)
	var jollof string
	for _, it := range owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)["items"].([]any) {
		if it := it.(map[string]any); it["name"] == "Jollof rice" {
			jollof = it["id"].(string)
		}
	}
	// Counted by a stock item, at 3.
	owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": jollof, "reason": "count", "count": 3}, "Idempotency-Key", "st1")
	owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	tolu, _ := w.personIn(t, "+2348031120101")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	two := map[string]any{"item_id": jollof, "quantity": 2}
	order := map[string]any{"lines": []any{two, two}, "fulfilment": "pickup"}
	if e := tolu.must(422, "POST", "/v1/me/chats/"+id+"/orders", order, "Idempotency-Key", "s1"); !strings.Contains(e["error"].(string), "Only 3") {
		t.Fatalf("four of three: %v", e)
	}
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/orders", map[string]any{"lines": []any{two, map[string]any{"item_id": jollof, "quantity": 1}}, "fulfilment": "pickup"},
		"Idempotency-Key", "s2")
}
