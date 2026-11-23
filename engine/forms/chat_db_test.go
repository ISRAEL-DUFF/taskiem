package forms_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/forms"
)

// Device fields D1, on review (docs/spec/device-fields.md §15): chat
// answers taken one at a time under the request's lock, each person's
// draft their own, the owner's photo not a cashier's answer, a form step
// answered once, and drafts gone with the answer.

// cashierFlow asks the cashiers for where and a photo, both required.
const cashierFlow = `{"schema":"wd/v1","id":"wf_count","version":1,"name":"Count","trigger":{"type":"manual"},
 "steps":[{"id":"shelf","type":"form","config":{"title":"Shelf check","to":{"role":"cashier"},
   "schema":{"type":"object","required":["where","proof"],"properties":{
    "where":{"type":"location","why":"so the check is recorded where it was made"},
    "proof":{"type":"photo","why":"a photo of the shelf"}}}}}]}`

// cashierAsked starts the flow and marks its request asked, as Tick does.
func (w *world) cashierAsked(t *testing.T) uuid.UUID {
	t.Helper()
	wf := w.env.Publish(t, cashierFlow)
	run := w.env.Start(t, wf, map[string]any{"body": map[string]any{}})
	w.env.Drain(t)
	req := uuid.MustParse(decide.FormRequestID(run.ID.String(), "shelf"))
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE form_requests SET sent_at = now() WHERE id = $1`, req); err != nil {
		t.Fatal(err)
	}
	return req
}

func (w *world) question(t *testing.T, who forms.ChatAsker, req uuid.UUID) *forms.ChatQuestion {
	t.Helper()
	qs, err := w.svc.ChatQuestions(context.Background(), who)
	if err != nil {
		t.Fatal(err)
	}
	for i := range qs {
		if qs[i].Request == req {
			return &qs[i]
		}
	}
	return nil
}

func photoHeld(id string) map[string]any {
	return map[string]any{"media": map[string]any{"channel": "whatsapp", "id": id, "mime": "image/jpeg"}}
}

func TestChatAnswersTakenOnceAndKeptApart(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	req := w.cashierAsked(t)
	ada := forms.ChatAsker{Tenant: w.env.Tenant, User: uuid.New(), Roles: []string{"cashier"}}
	bayo := forms.ChatAsker{Tenant: w.env.Tenant, User: uuid.New(), Roles: []string{"cashier"}}
	owner := forms.ChatAsker{Tenant: w.env.Tenant, User: uuid.New(), Roles: []string{"owner"}}

	// The owner sees every role's request under To fill in, but a photo
	// they send in a chat is not taken for the cashiers' question.
	if q := w.question(t, owner, req); q != nil {
		t.Fatalf("the owner's chat matched the cashiers' question: %+v", q)
	}
	q := w.question(t, ada, req)
	if q == nil {
		t.Fatal("not asked of a cashier")
	}

	// A location and a photo arriving together: decided one after the
	// other, so the question is whole once, with both.
	var wg sync.WaitGroup
	results := make([]forms.ChatAdded, 2)
	errs := make([]error, 2)
	for i, f := range []struct {
		field string
		value map[string]any
	}{{"where", map[string]any{"lat": lat, "lng": lng}}, {"proof", photoHeld("m-1")}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = w.svc.ChatAdd(ctx, *q, ada.User, f.field, f.value)
		}()
	}
	wg.Wait()
	whole := 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if r.Whole {
			whole++
			if r.Question.Held["where"] == nil || r.Question.Held["proof"] == nil {
				t.Fatalf("whole without both: %v", r.Question.Held)
			}
		}
	}
	if whole != 1 {
		t.Fatalf("whole %d times", whole)
	}
	held := w.question(t, ada, req).Held
	if held["where"].(map[string]any)["lat"] != lat || held["proof"] == nil {
		t.Fatalf("Ada's draft: %v", held)
	}
	// The pin is sealed where it is kept.
	var raw string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT answers::text FROM form_chat_answers WHERE request_id = $1 AND user_id = $2`, req, ada.User).Scan(&raw)
	if strings.Contains(raw, "6.452131") || raw == "" {
		t.Fatalf("stored: %s", raw)
	}

	// Another cashier's draft is their own: they see nothing of Ada's, and
	// what they send does not join it.
	qb := w.question(t, bayo, req)
	if qb == nil || len(qb.Held) != 0 {
		t.Fatalf("Bayo sees: %+v", qb)
	}
	got, err := w.svc.ChatAdd(ctx, *qb, bayo.User, "proof", photoHeld("m-2"))
	if err != nil || got.Whole || len(got.Question.Held) != 1 {
		t.Fatalf("Bayo's photo: %+v %v", got, err)
	}
	if held := w.question(t, ada, req).Held; held["proof"].(map[string]any)["media"].(map[string]any)["id"] != "m-1" {
		t.Fatalf("Ada's draft changed: %v", held)
	}

	// Answered in the app meanwhile: every draft goes with the answer, and
	// a chat message for it is told it is no longer waiting.
	w.answer(req, forms.Submission{Channel: "app", Submitted: time.Now(), Respondent: forms.Respondent{Kind: "member", MemberID: bayo.User.String()},
		Answers: map[string]any{"where": map[string]any{"address": "Shelf 4, Allen Avenue"}, "proof": map[string]any{"state": "unavailable"}}})
	var n int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_chat_answers WHERE request_id = $1`, req).Scan(&n)
	if n != 0 {
		t.Fatalf("%d drafts left after the answer", n)
	}
	if _, err := w.svc.ChatAdd(ctx, *q, ada.User, "where", map[string]any{"lat": lat, "lng": lng}); !errors.Is(err, forms.ErrNotWaiting) {
		t.Fatalf("after the answer: %v", err)
	}

	// Withdrawn with its run: the draft holding a sealed pin goes too.
	wf := w.env.Publish(t, cashierFlow)
	run := w.env.Start(t, wf, map[string]any{"body": map[string]any{}})
	w.env.Drain(t)
	req2 := uuid.MustParse(decide.FormRequestID(run.ID.String(), "shelf"))
	_, _ = w.env.DB.Admin.Exec(ctx, `UPDATE form_requests SET sent_at = now() WHERE id = $1`, req2)
	if _, err := w.svc.ChatAdd(ctx, *w.question(t, ada, req2), ada.User, "where", map[string]any{"lat": lat, "lng": lng}); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Store.CancelRun(ctx, run, "test", false); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_chat_answers WHERE request_id = $1`, req2).Scan(&n)
	if n != 0 {
		t.Fatalf("%d drafts left after the run was cancelled", n)
	}
}

// Two answers to one form step arriving together: one is taken, the other
// refused in plain words and its stored file discarded.
func TestFormStepAnsweredOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	req := w.cashierAsked(t)
	files := w.svc.Files.(*fakeFiles)
	before := files.count()
	refs := []string{forms.NewReference(), forms.NewReference()}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, ref := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := sha256.Sum256([]byte(ref))
			meta := forms.FileMeta{Field: "proof", Name: "shelf.jpg", Type: "image/jpeg", Size: 5}
			errs[i] = w.svc.Process(ctx, forms.Incoming{Tenant: w.env.Tenant, Request: &req, Reference: ref, StatusHash: h[:],
				Uploads: []forms.Upload{{Meta: meta, Data: []byte("shelf")}},
				Submission: forms.Submission{Channel: "app", Submitted: time.Now(), Respondent: forms.Respondent{Kind: "member", MemberID: uuid.NewString()},
					Files: []forms.FileMeta{meta}, Answers: map[string]any{"where": map[string]any{"lat": lat, "lng": lng}}}})
		}()
	}
	wg.Wait()
	statuses := map[string]int{}
	for i, ref := range refs {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		var status string
		var note *string
		_ = w.env.DB.Admin.QueryRow(ctx, `SELECT status, note FROM form_submissions WHERE reference = $1`, ref).Scan(&status, &note)
		statuses[status]++
		if status == "refused" && (note == nil || *note != forms.NoLongerTaking) {
			t.Fatalf("refused with %v", note)
		}
	}
	if statuses["refused"] != 1 || statuses["received"]+statuses["answered"] != 1 {
		t.Fatalf("statuses: %v", statuses)
	}
	if err := w.svc.DrainDeletions(ctx); err != nil {
		t.Fatal(err)
	}
	var kept int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_files f JOIN form_submissions s ON s.id = f.submission_id WHERE s.request_id = $1`, req).Scan(&kept)
	if kept != 1 || files.count()-before != 1 {
		t.Fatalf("files: %d recorded, %d stored", kept, files.count()-before)
	}
}
