package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The second review of stage 5a (people-chatting, "Second review fixes
// (server)").

// typingTo counts the typing marks written into person's copies.
func (w *peopleWorld) typingTo(t *testing.T, person uuid.UUID) int {
	t.Helper()
	return w.adminCount(t, `SELECT count(*) FROM people_typing WHERE person_id = $1`, person)
}

// C1: with the other person deleted or suspended, what the person does
// on their own copy still works (the reach answers instead of aborting
// the transaction): typing (204, nothing written), reading, reacting to
// a message, marking a share paid, opening the chat, accepting a waiting
// request, delete for me and delete for everyone. Delete for everyone
// still removes the message, and its search tokens, from a suspended
// person's copy (a suspension stops messaging, not forgetting); a deleted
// account has no copy left.
func TestPeoplePeerAway(t *testing.T) {
	for _, how := range []string{"deleted", "suspended"} {
		t.Run(how, func(t *testing.T) {
			ctx := context.Background()
			w := newJoinedWorld(t)
			ada, adaID := w.person(t, "+2348031132101", "Ada Obi")
			bola, bolaID := w.person(t, "+2348031132102", "Bola Ade")
			cee, ceeID := w.person(t, "+2348031132103", "Cee Eze")
			w.older(t, adaID)
			w.older(t, bolaID)
			w.older(t, ceeID)
			adaChat, bolaChat := friends(t, ada, bola, bolaID)
			m := send(t, ada, adaChat, "secret wordzz here")
			mine := send(t, ada, adaChat, "only mine to forget")
			send(t, bola, bolaChat, "unread one")
			bola.must(200, "PUT", "/v1/me/payout-account", map[string]any{"bank": "GTBank", "account_number": "0123456789", "account_name": "Bola Ade"})
			split := bola.must(201, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"card": map[string]any{"type": "split", "amount_kobo": 300000,
				"what": "The cake"}})
			// Cee writes to Ada: a request waiting in her Requests.
			send(t, cee, chatWith(t, cee, adaID), "Hi Ada")
			req := requestOf(t, ada)
			if n := w.adminCount(t, `SELECT count(*) FROM people_search WHERE person_id = $1 AND message_id = $2`, bolaID, m["id"]); n == 0 {
				t.Fatal("Bola's copy is not indexed")
			}

			switch how {
			case "deleted":
				w.deleteAccount(t, bola, "+2348031132102")
				w.deleteAccount(t, cee, "+2348031132103")
			case "suspended":
				if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = ANY ($1)`, []uuid.UUID{bolaID, ceeID}); err != nil {
					t.Fatal(err)
				}
			}

			ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil)
			if n := w.typingTo(t, bolaID); n != 0 {
				t.Fatalf("typing reached a %s account: %d", how, n)
			}
			ada.must(204, "POST", "/v1/me/people/"+adaChat+"/read", nil)
			if c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil); c["unread"] != float64(0) {
				t.Fatalf("read with Bola %s: %v", how, c)
			}
			if r := ada.must(200, "PUT", "/v1/me/people/"+adaChat+"/messages/"+m["id"].(string), map[string]any{"reaction": "👍"}); r["my_reaction"] != "👍" {
				t.Fatalf("reacted with Bola %s: %v", how, r)
			}
			ada.must(200, "POST", "/v1/me/people/"+adaChat+"/messages/"+split["id"].(string)+"/paid", map[string]any{"paid": true})
			ada.must(200, "POST", "/v1/me/people/"+req+"/accept", nil)
			ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+mine["id"].(string)+"?for=me", nil)
			ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+m["id"].(string)+"?for=everyone", nil)
			for _, x := range messagesOf(t, ada, adaChat) {
				if x["id"] == m["id"] && x["deleted"] != true {
					t.Fatalf("not deleted on Ada's side: %v", x)
				}
				if x["id"] == mine["id"] {
					t.Fatal("deleted for me, still there")
				}
			}
			if how == "suspended" {
				// The suspended account's copy forgot it too.
				if n := w.adminCount(t, `SELECT count(*) FROM people_messages WHERE person_id = $1 AND id = $2 AND deleted_at IS NOT NULL AND sealed IS NULL`,
					bolaID, m["id"]); n != 1 {
					t.Fatal("deleted for everyone, kept on the suspended account's copy")
				}
				if n := w.adminCount(t, `SELECT count(*) FROM people_search WHERE person_id = $1 AND message_id = $2`, bolaID, m["id"]); n != 0 {
					t.Fatalf("its search tokens stayed: %d", n)
				}
				// Nothing else reached it: no reaction, no read mark, no paid.
				if n := w.adminCount(t, `SELECT count(*) FROM people_messages WHERE person_id = $1 AND (their_reaction IS NOT NULL OR (mine AND read_at IS NOT NULL AND id <> $2)
					OR paid_at IS NOT NULL)`, bolaID, m["id"]); n != 0 {
					t.Fatalf("a change reached the suspended account's copy: %d", n)
				}
				// Accepting a suspended stranger's request: their copy shows it
				// accepted; they still write to no one.
				if n := w.adminCount(t, `SELECT count(*) FROM people_chats WHERE person_id = $1 AND peer_id = $2 AND waiting_at IS NULL AND accepted_at IS NOT NULL`,
					ceeID, adaID); n != 1 {
					t.Fatal("the suspended sender's copy is still waiting")
				}
			}
		})
	}
}

// S3: a suspended person still reads and keeps their own copy, but
// nothing of theirs reaches anyone: a reaction, marking a share paid and
// accepting a request are refused with the words that send them to Your
// standing; typing answers 204 and writes nothing; reading marks only
// their own copy. Deleting their own message for everyone still works.
func TestPeopleSuspendedReachesNoOne(t *testing.T) {
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031132201", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031132202", "Bola Ade")
	cee, ceeID := w.person(t, "+2348031132203", "Cee Eze")
	w.older(t, adaID)
	w.older(t, bolaID)
	w.older(t, ceeID)
	mod := newModerator(t, w, "mod@taskiem.test")
	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	m := send(t, ada, adaChat, "Are you there?")
	ada.must(200, "PUT", "/v1/me/payout-account", map[string]any{"bank": "GTBank", "account_number": "0123456789", "account_name": "Ada Obi"})
	split := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "split", "amount_kobo": 300000, "what": "The cake"}})
	hers := messageOf(t, bola, bolaChat, "Hello you")
	send(t, cee, chatWith(t, cee, bolaID), "Hi Bola")
	req := requestOf(t, bola)

	target := bolaID.String()
	mod.write(201, "ops.people.suspend", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "suspend", "reason": "Abuse", "days": 0})

	standing := func(e map[string]any) {
		t.Helper()
		if !strings.Contains(e["error"].(string), "Your standing") {
			t.Fatalf("not the standing words: %v", e)
		}
	}
	standing(bola.must(403, "PUT", "/v1/me/people/"+bolaChat+"/messages/"+m["id"].(string), map[string]any{"reaction": "PAY_ME_NOW"}))
	standing(bola.must(403, "POST", "/v1/me/people/"+bolaChat+"/messages/"+split["id"].(string)+"/paid", map[string]any{"paid": true}))
	standing(bola.must(403, "POST", "/v1/me/people/"+req+"/accept", nil))
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/typing", nil)
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/read", nil)
	// A star is their own.
	bola.must(200, "PUT", "/v1/me/people/"+bolaChat+"/messages/"+m["id"].(string), map[string]any{"starred": true})

	if n := w.typingTo(t, adaID); n != 0 {
		t.Fatalf("a suspended person's typing reached Ada: %d", n)
	}
	for _, x := range messagesOf(t, ada, adaChat) {
		if x["their_reaction"] != nil || (x["id"] == split["id"] && x["card"].(map[string]any)["paid"] == true) {
			t.Fatalf("a suspended person's mark reached Ada: %v", x)
		}
		if x["id"] == m["id"] && x["read_at"] != nil {
			t.Fatalf("a suspended person's read mark reached Ada: %v", x)
		}
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_chats WHERE person_id = $1 AND waiting_at IS NOT NULL`, ceeID); n != 1 {
		t.Fatal("Cee's request was accepted by a suspended person")
	}
	// Forgetting their own message still reaches Ada's copy.
	bola.must(204, "DELETE", "/v1/me/people/"+bolaChat+"/messages/"+hers["id"].(string)+"?for=everyone", nil)
	for _, x := range messagesOf(t, ada, adaChat) {
		if x["id"] == hers["id"] && x["deleted"] != true {
			t.Fatalf("deleted for everyone by a suspended person, kept on Ada's side: %v", x)
		}
	}
}

// failDeletion makes deleting an account fail (a transient failure, stood
// in by a trigger) until the returned func is called.
func failDeletion(t *testing.T, w *peopleWorld) func() {
	t.Helper()
	ctx := context.Background()
	if _, err := w.env.DB.Admin.Exec(ctx, `CREATE FUNCTION rev2_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'transient'; END $$;
		CREATE TRIGGER rev2_fail BEFORE UPDATE OF deleted_at ON users FOR EACH ROW WHEN (NEW.deleted_at IS NOT NULL) EXECUTE FUNCTION rev2_fail()`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := w.env.DB.Admin.Exec(ctx, `DROP TRIGGER rev2_fail ON users; DROP FUNCTION rev2_fail()`); err != nil {
			t.Fatal(err)
		}
	}
}

// S1: a closure whose deletion fails is not marked done: the account
// stays closing (suspended, writing to no one, shown suspended), the
// failure is recorded, and the next tick closes it.
func TestPeopleClosureRetried(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031132301", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031132302", "Bola Ade")
	w.older(t, adaID)
	w.older(t, bolaID)
	mod := newModerator(t, w, "mod@taskiem.test")
	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	target := bolaID.String()
	cl := mod.write(201, "ops.people.close", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "close", "reason": "Scams"})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_moderation SET until = now() - interval '1 minute' WHERE id = $1`, cl["id"]); err != nil {
		t.Fatal(err)
	}
	restore := failDeletion(t, w)
	for range 2 {
		if err := w.srv.SafetyTick(ctx); err == nil {
			t.Fatal("a failed closure is not said")
		}
		if n := w.adminCount(t, `SELECT count(*) FROM people_moderation WHERE id = $1 AND closed_at IS NULL`, cl["id"]); n != 1 {
			t.Fatal("a closure that failed is marked done")
		}
		if n := w.adminCount(t, `SELECT count(*) FROM person_profiles WHERE person_id = $1 AND suspended AND NOT active`, bolaID); n != 1 {
			t.Fatal("a closure that failed restored the account")
		}
		bola.must(403, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"text": "I'm back"})
		ada.must(409, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Bola?"})
		if s := bola.must(200, "GET", "/v1/me/standing", nil); s["state"] != "closing" {
			t.Fatalf("standing after a failed closure: %v", s)
		}
	}
	if a := platformAudits(t, w, "people.account.close.failed"); len(a) != 2 || !strings.Contains(a[0].Detail["error"].(string), "transient") {
		t.Fatalf("the failures' records: %v", a)
	}
	if a := platformAudits(t, w, "people.account.close"); len(a) != 0 {
		t.Fatalf("recorded closed: %v", a)
	}
	restore()
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL`, bolaID); n != 1 {
		t.Fatal("the closure was not carried out on the next tick")
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_moderation WHERE id = $1 AND closed_at IS NOT NULL AND close_note = 'closed'`, cl["id"]); n != 1 {
		t.Fatal("the closure is not marked done")
	}
	if a := platformAudits(t, w, "people.account.close"); len(a) != 1 || a[0].Detail["outcome"] != "closed" {
		t.Fatalf("the closure's record: %v", a)
	}
}

// S2: an appeal made while the closure is being carried out holds it
// (the deletion locks the closure and looks again for an open appeal); an
// appeal once the closing day has passed is refused in plain words; a
// carried-out closure can no longer be lifted.
func TestPeopleClosureRaces(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	bola, bolaID := w.person(t, "+2348031132402", "Bola Ade")
	w.older(t, bolaID)
	mod := newModerator(t, w, "mod@taskiem.test")
	target := bolaID.String()
	cl := mod.write(201, "ops.people.close", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "close", "reason": "Scams"})
	clID := cl["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_moderation SET until = now() - interval '1 minute' WHERE id = $1`, clID); err != nil {
		t.Fatal(err)
	}
	// Too late to appeal.
	if e := bola.must(409, "POST", "/v1/me/standing/appeals", map[string]any{"action_id": clID, "text": "Please"}); !strings.Contains(e["error"].(string), "time to appeal") {
		t.Fatalf("a late appeal: %v", e)
	}
	for _, a := range bola.must(200, "GET", "/v1/me/standing", nil)["actions"].([]any) {
		if a.(map[string]any)["can_appeal"] != false {
			t.Fatalf("a closure past its day can be appealed: %v", a)
		}
	}

	// An appeal written just as the closure is carried out: it holds the
	// closure's lock (as appealing does) while the safety job waits for it.
	tx, err := w.env.DB.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM people_moderation WHERE id = $1 FOR UPDATE`, clID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var tickErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		tickErr = w.srv.SafetyTick(ctx)
	}()
	// The job is waiting for the lock.
	deadline := time.Now().Add(10 * time.Second)
	for w.adminCount(t, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%taskiem_people_closure_due%'`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the safety job did not wait for the closure's lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO people_appeals (id, action_id, person_id, text) VALUES ($1, $2, $3, 'Please, it was not me')`, uuid.New(), clID, bolaID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if tickErr != nil {
		t.Fatal(tickErr)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NULL`, bolaID); n != 1 {
		t.Fatal("closed while an appeal was being made")
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_moderation WHERE id = $1 AND closed_at IS NULL`, clID); n != 1 {
		t.Fatal("the held closure is marked done")
	}

	// Upheld, it is carried out; then nothing lifts it.
	second := newModerator(t, w, "second@taskiem.test")
	appeals := second.must(200, "GET", "/v1/ops/people/appeals", nil)["appeals"].([]any)
	ap := appeals[0].(map[string]any)["id"].(string)
	second.write(200, "ops.people.appeal.uphold", ap, "/v1/ops/people/appeals/"+ap+"/uphold", map[string]any{"note": "Clear."})
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL`, bolaID); n != 1 {
		t.Fatal("the upheld closure was not carried out")
	}
	mod.write(409, "ops.people.lift", clID, "/v1/ops/people/actions/"+clID+"/lift", map[string]any{"reason": "Too late"})
}

// S4: reports count each reporter once (even after they delete their
// account, by the server's keyed mark), and only reports of a person who
// wrote to the reporter weigh on that person's allowance: one account
// reporting someone who never wrote to it, many times, then deleting
// itself, does not slow an honest account.
func TestPeopleReportsCountOnce(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	var others []uuid.UUID
	for i := range 5 {
		_, id := w.person(t, "+23480311325"+string(rune('1'+i))+"0", "Person "+string(rune('A'+i)))
		w.older(t, id)
		others = append(others, id)
	}
	// The chats are opened first: what is counted is the requests the
	// sender may have waiting, not the new chats an hour.
	open := func(c *client) []string {
		var chats []string
		for _, o := range others {
			chats = append(chats, chatWith(t, c, o))
		}
		return chats
	}
	requests := func(c *client, chats []string) int {
		n := 0
		for _, chat := range chats {
			code, _ := c.do("POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": "Hello there"})
			if code != 201 {
				return n
			}
			n++
		}
		return n
	}
	hundredDays := func(id uuid.UUID) {
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET created_at = now() - interval '100 days' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}

	// The attack: an account the victim never wrote to reports them five
	// times, then deletes itself.
	victim, victimID := w.person(t, "+2348031132601", "Victim")
	hundredDays(victimID)
	att, attID := w.person(t, "+2348031132602", "Attacker")
	w.older(t, attID)
	chat := chatWith(t, att, victimID)
	send(t, att, chat, "hello")
	for range 5 {
		att.must(201, "POST", "/v1/me/people/"+chat+"/report", map[string]any{"reason": "spam", "consent": true})
	}
	w.deleteAccount(t, att, "+2348031132602")
	if n := w.adminCount(t, `SELECT count(DISTINCT reporter_mark) FROM people_reports WHERE reported_id = $1 AND person_id IS NULL AND reporter_mark IS NOT NULL`, victimID); n != 1 {
		t.Fatalf("the detached reports keep one reporter's mark: %d", n)
	}
	if n := requests(victim, open(victim)); n != 5 {
		t.Fatalf("an honest 100-day account reported by one deleted account that it never wrote to sent %d", n)
	}

	// Two people the sender wrote to report them (one twice), and one of
	// them deletes their account: still two reporters, a third of the
	// allowance.
	rep, repID := w.person(t, "+2348031132603", "Reported")
	hundredDays(repID)
	repChats := open(rep)
	x, xID := w.person(t, "+2348031132604", "Xavier")
	y, yID := w.person(t, "+2348031132605", "Yemi")
	w.older(t, xID)
	w.older(t, yID)
	for _, p := range []struct {
		c  *client
		id uuid.UUID
	}{{x, xID}, {y, yID}} {
		send(t, rep, chatWith(t, rep, p.id), "Buy my coins")
		req := requestOf(t, p.c)
		p.c.must(200, "POST", "/v1/me/people/"+req+"/accept", nil)
		p.c.must(201, "POST", "/v1/me/people/"+req+"/report", map[string]any{"reason": "scam", "consent": true})
		if p.id == xID {
			p.c.must(201, "POST", "/v1/me/people/"+req+"/report", map[string]any{"reason": "scam", "consent": true})
		}
	}
	w.deleteAccount(t, x, "+2348031132604")
	if n := requests(rep, repChats); n != 3 {
		t.Fatalf("an account reported by two people it wrote to sent %d", n)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	if p := mod.must(200, "GET", "/v1/ops/people/persons/"+repID.String(), nil)["person"].(map[string]any); p["reports_against"] != float64(2) {
		t.Fatalf("moderators see the reporters counted once: %v", p)
	}
}

// S5: a person's report of a business keeps its messages sealed under the
// report's own key; moderators read them opened; one kept plain before is
// sealed by the safety job.
func TestCustomerReportsSealed(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	mod := newModerator(t, w, "mod@taskiem.test")
	person, _ := w.person(t, "+2348031132701", "Tolu")
	chat := person.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	person.must(201, "POST", "/v1/me/chats/"+chat+"/messages", map[string]any{"text": "Send me your PIN, says the shop"})
	rep := person.must(201, "POST", "/v1/me/chats/"+chat+"/report", map[string]any{"reason": "scam", "consent": true})
	id := rep["id"].(string)
	if n := w.adminCount(t, `SELECT count(*) FROM customer_reports WHERE id = $1 AND messages = '[]' AND sealed IS NOT NULL AND message_count >= 1
		AND position(convert_to('PIN', 'UTF8') in sealed) = 0`, id); n != 1 {
		t.Fatal("a report of a business is kept plain")
	}
	if l := mod.must(200, "GET", "/v1/ops/customer-reports", nil)["reports"].([]any); len(l) != 1 || l[0].(map[string]any)["messages"].(float64) < 1 {
		t.Fatalf("listed: %v", l)
	}
	got := mod.must(200, "GET", "/v1/ops/customer-reports/"+id+"?why=checking+the+scam", nil)
	if !strings.Contains(string(asJSON(t, got)), "Send me your PIN") {
		t.Fatalf("opened: %v", got)
	}
	// One kept plain (before 00481) is sealed by the safety job.
	old := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO customer_reports (id, person_id, tenant_id, reason, note, consent, messages)
		SELECT $1, person_id, tenant_id, 'spam', '', true, '[{"body": "old plain words", "kind": "text"}]' FROM customer_reports WHERE id = $2`, old, id); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM customer_reports WHERE id = $1 AND messages = '[]' AND sealed IS NOT NULL AND message_count = 1`, old); n != 1 {
		t.Fatal("an old plain report was not sealed")
	}
	if got := mod.must(200, "GET", "/v1/ops/customer-reports/"+old.String()+"?why=checking+the+old+one", nil); !strings.Contains(string(asJSON(t, got)), "old plain words") {
		t.Fatalf("the old report opened: %v", got)
	}
}

// C4: the safety job prunes gone records older than an hour, typing marks
// past their time and live connections not beaten for a while, for
// everyone.
func TestPeopleSweep(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031132801", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031132802", "Bola Ade")
	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	for _, q := range []string{
		`INSERT INTO people_gone (person_id, chat_id, at) VALUES ($1, $2::uuid, now() - interval '2 hours'), ($1, $2::uuid, now())`,
		`INSERT INTO people_typing (person_id, chat_id, conversation_id, until) SELECT $1, id, conversation_id, now() - interval '1 minute' FROM people_chats WHERE id = $2::uuid`,
		`INSERT INTO person_live (person_id, id, seen_at) SELECT $1::uuid, gen_random_uuid(), now() - interval '1 hour' WHERE $2::uuid IS NOT NULL
		 UNION ALL SELECT $1::uuid, gen_random_uuid(), now()`,
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, adaID, adaChat); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_typing (person_id, chat_id, conversation_id, until) SELECT $1, id, conversation_id, now() + interval '1 minute'
		FROM people_chats WHERE id = $2::uuid`, bolaID, bolaChat); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_gone WHERE person_id = $1`, adaID); n != 1 {
		t.Fatalf("gone records left: %d", n)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_typing`); n != 1 {
		t.Fatalf("typing marks left: %d", n)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM person_live WHERE person_id = $1`, adaID); n != 1 {
		t.Fatalf("live connections left: %d", n)
	}
}

// C5: a person suspended while connected is away when their connection
// closes; typing keeps each person's own limit under memory pressure
// (never one bucket shared by everyone).
func TestPeopleLiveAndTypingLimits(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031132901", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031132902", "Bola Ade")
	cee, ceeID := w.person(t, "+2348031132903", "Cee Eze")
	dayo, dayoID := w.person(t, "+2348031132904", "Dayo")
	adaChat, _ := friends(t, ada, bola, bolaID)
	ceeChat, _ := friends(t, cee, dayo, dayoID)

	conn := uuid.New()
	if err := w.srv.LiveConnect(ctx, adaID, conn, true); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM person_presence WHERE person_id = $1 AND online_until > now()`, adaID); n != 1 {
		t.Fatal("not online on connecting")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	w.srv.LiveClose(adaID, conn)
	if n := w.adminCount(t, `SELECT count(*) FROM person_presence WHERE person_id = $1 AND online_until IS NULL`, adaID); n != 1 {
		t.Fatal("a person suspended while connected stays online after closing")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}

	// Under memory pressure, one person's typing does not stop another's,
	// and each is still passed on at most every 3 seconds.
	w.srv.FillLimiters()
	defer w.srv.ResetLimiters()
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil)
	cee.must(204, "POST", "/v1/me/people/"+ceeChat+"/typing", nil)
	if w.typingTo(t, bolaID) != 1 || w.typingTo(t, ceeID) != 0 || w.typingTo(t, dayoID) != 1 {
		t.Fatal("under memory pressure, one person's typing stopped another's")
	}
	var until1, until2 time.Time
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT until FROM people_typing WHERE person_id = $1`, bolaID).Scan(&until1)
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil)
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT until FROM people_typing WHERE person_id = $1`, bolaID).Scan(&until2)
	if until1.IsZero() || !until1.Equal(until2) {
		t.Fatalf("typing passed on twice at once under memory pressure: %v %v", until1, until2)
	}
}
