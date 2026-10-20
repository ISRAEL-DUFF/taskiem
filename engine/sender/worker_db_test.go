package sender

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

type fakeSMS struct {
	name string
	mu   sync.Mutex
	sent []string // to|text
	err  error
}

func (f *fakeSMS) Name() string { return f.name }

func (f *fakeSMS) Send(_ context.Context, to, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.sent = append(f.sent, to+"|"+text)
	return fmt.Sprintf("%s-%d", f.name, len(f.sent)), nil
}

type fakeMailer struct {
	msgs [][]byte
	err  error
}

func (f *fakeMailer) Send(_ context.Context, _ string, _ []string, msg []byte) error {
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, msg)
	return nil
}

type env struct {
	d     *dbtest.DB
	q     *Queue
	w     *Worker
	a, b  *fakeSMS
	mail  *fakeMailer
	t1    uuid.UUID
	vault *secrets.Vault
}

func setup(t *testing.T) *env {
	d := dbtest.New(t)
	kms, err := secrets.NewLocalKMS(map[string][]byte{"root": bytes.Repeat([]byte{5}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	v := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "root"}
	e := &env{d: d, vault: v, a: &fakeSMS{name: "termii"}, b: &fakeSMS{name: "africastalking"}, mail: &fakeMailer{}}
	e.q = &Queue{Vault: v, HashKey: bytes.Repeat([]byte{9}, 32)}
	e.w = &Worker{Pool: d.App, Vault: v, SMS: []SMSProvider{e.a, e.b}, Rate: 1000, Mailer: e.mail, From: "no-reply@taskiem.test"}
	e.t1 = d.SeedTenant(t, nil).ID
	return e
}

func (e *env) enqueue(t *testing.T, tenant uuid.UUID, m Message) (bool, error) {
	t.Helper()
	m.Tenant = tenant
	var queued bool
	err := db.InTenantTx(context.Background(), e.d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		queued, err = e.q.Enqueue(context.Background(), tx, m)
		return err
	})
	return queued, err
}

func sms(key, to string) Message {
	return Message{Channel: SMS, Purpose: PurposeConfirmation, To: to, SenderName: "Ada's Kitchen", Text: "Order AK-1 is confirmed.", Key: key}
}

type row struct {
	status, provider, lastErr string
	attempts, segments        int
	sealed                    bool
	due                       time.Time
}

func (e *env) row(t *testing.T, tenant uuid.UUID, key string) row {
	t.Helper()
	var r row
	var p, le *string
	var seg *int
	if err := e.d.Admin.QueryRow(context.Background(), `SELECT status, provider, last_error, attempts, segments, recipient IS NOT NULL AND body IS NOT NULL, next_attempt_at
		FROM platform_messages WHERE tenant_id = $1 AND dedup_key = $2`, tenant, key).Scan(&r.status, &p, &le, &r.attempts, &seg, &r.sealed, &r.due); err != nil {
		t.Fatal(err)
	}
	if p != nil {
		r.provider = *p
	}
	if le != nil {
		r.lastErr = *le
	}
	if seg != nil {
		r.segments = *seg
	}
	return r
}

func (e *env) tick(t *testing.T) {
	t.Helper()
	if err := e.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e *env) makeDue(t *testing.T) {
	t.Helper()
	if _, err := e.d.Admin.Exec(context.Background(), `UPDATE platform_messages SET next_attempt_at = now() WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
}

func TestSendOnceAndWipe(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if ok, err := e.enqueue(t, e.t1, sms("order:1", "0803 123 4567")); err != nil || !ok {
		t.Fatalf("enqueue: %v %v", ok, err)
	}
	if ok, err := e.enqueue(t, e.t1, sms("order:1", "08031234567")); err != nil || ok {
		t.Fatalf("the same key again must not queue: %v %v", ok, err)
	}
	// Sealed at rest: neither the number nor the text is in the row.
	var raw string
	if err := e.d.Admin.QueryRow(ctx, `SELECT row_to_json(m)::text FROM platform_messages m WHERE dedup_key = 'order:1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "8031234567") || strings.Contains(raw, "AK-1") {
		t.Fatalf("stored in the clear: %s", raw)
	}
	e.tick(t)
	e.tick(t)
	if len(e.a.sent) != 1 || e.a.sent[0] != "+2348031234567|Ada's Kitchen: Order AK-1 is confirmed." || len(e.b.sent) != 0 {
		t.Fatalf("sent %v %v", e.a.sent, e.b.sent)
	}
	r := e.row(t, e.t1, "order:1")
	if r.status != "sent" || r.provider != "termii" || r.segments != 1 || r.sealed || r.attempts != 1 {
		t.Fatalf("%+v", r)
	}
	if segs, mails, err := Usage(ctx, e.d.App, e.t1, time.Now().Add(-time.Hour)); err != nil || segs != 1 || mails != 0 {
		t.Fatalf("usage %d %d %v", segs, mails, err)
	}
}

func TestFallbackAndRefusals(t *testing.T) {
	e := setup(t)
	e.a.err = fmt.Errorf("termii: %w: balance", ErrProvider)
	if _, err := e.enqueue(t, e.t1, sms("fallback", "08031234567")); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	if r := e.row(t, e.t1, "fallback"); r.status != "sent" || r.provider != "africastalking" || len(e.b.sent) != 1 {
		t.Fatalf("fallback: %+v", r)
	}

	// A bad number is dead at once, without trying the next provider.
	e.a.err = fmt.Errorf("termii: %w: invalid number", ErrRecipient)
	if _, err := e.enqueue(t, e.t1, sms("bad", "08031234568")); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	if r := e.row(t, e.t1, "bad"); r.status != "dead" || r.sealed || len(e.b.sent) != 1 {
		t.Fatalf("recipient refused: %+v", r)
	}

	// An answer that may mean it went is never retried.
	e.a.err = errors.New("read: connection reset")
	if _, err := e.enqueue(t, e.t1, sms("maybe", "08031234569")); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	e.makeDue(t)
	e.tick(t)
	if r := e.row(t, e.t1, "maybe"); r.status != "unknown" || r.attempts != 1 || len(e.b.sent) != 1 {
		t.Fatalf("ambiguous: %+v", r)
	}
}

func TestBackoffUntilDead(t *testing.T) {
	e := setup(t)
	e.a.err = fmt.Errorf("%w: down", ErrNotSent)
	e.b.err = fmt.Errorf("%w: down", ErrNotSent)
	if _, err := e.enqueue(t, e.t1, sms("retry", "08031234567")); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	r := e.row(t, e.t1, "retry")
	if r.status != "pending" || r.attempts != 1 || time.Until(r.due) < 20*time.Second || r.lastErr == "" || !r.sealed {
		t.Fatalf("first failure: %+v", r)
	}
	e.tick(t) // not due yet
	if r := e.row(t, e.t1, "retry"); r.attempts != 1 {
		t.Fatalf("retried before its time: %+v", r)
	}
	for range MaxAttempts - 1 {
		e.makeDue(t)
		e.tick(t)
	}
	if r := e.row(t, e.t1, "retry"); r.status != "dead" || r.attempts != MaxAttempts || r.sealed {
		t.Fatalf("after %d attempts: %+v", MaxAttempts, r)
	}
}

func TestDropped(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := sms("code", "08031234567")
	m.Purpose, m.Text = PurposeCode, "Your code is 123456"
	if _, err := e.enqueue(t, e.t1, m); err != nil {
		t.Fatal(err)
	}
	e.w.now = func() time.Time { return time.Now().Add(CodeTTL + time.Minute) }
	e.tick(t)
	if r := e.row(t, e.t1, "code"); r.status != "dropped" || len(e.a.sent) != 0 {
		t.Fatalf("a late code: %+v", r)
	}
	e.w.now = nil

	// Erasing the recipient erases what was queued for them.
	if _, err := e.enqueue(t, e.t1, sms("erase", "08039999999")); err != nil {
		t.Fatal(err)
	}
	var subject string
	if err := e.d.Admin.QueryRow(ctx, `SELECT subject_id FROM platform_messages WHERE dedup_key = 'erase'`).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if err := e.vault.Erase(ctx, e.t1, subject, "test"); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	if r := e.row(t, e.t1, "erase"); r.status != "dropped" || len(e.a.sent) != 0 {
		t.Fatalf("erased: %+v", r)
	}
}

func TestRecipientLimitAcrossTenants(t *testing.T) {
	e := setup(t)
	e.q.PerHour = 2
	t2 := e.d.SeedTenant(t, nil).ID
	if _, err := e.enqueue(t, e.t1, sms("a", "08031234567")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.enqueue(t, t2, sms("b", "+2348031234567")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.enqueue(t, t2, sms("c", "2348031234567")); !errors.Is(err, ErrRecipientLimit) {
		t.Fatalf("third message in the hour, from another tenant: %v", err)
	}
	// Another number, and the same number by email, are counted apart.
	if _, err := e.enqueue(t, t2, sms("d", "08031234568")); err != nil {
		t.Fatal(err)
	}
	// A refused message holds no allowance: its transaction rolled back.
	var n int
	if err := e.d.Admin.QueryRow(context.Background(), `SELECT count(*) FROM platform_messages`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("messages %d %v", n, err)
	}
	// Tenants see only their own messages.
	err := db.InTenantTx(context.Background(), e.d.App, []uuid.UUID{t2}, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM platform_messages`).Scan(&n)
	})
	if err != nil || n != 2 {
		t.Fatalf("tenant view %d %v", n, err)
	}
}

func TestEmail(t *testing.T) {
	e := setup(t)
	m := Message{Channel: Email, Purpose: PurposeConfirmation, To: "Customer@Example.com", SenderName: "Ada's Kitchen", ReplyTo: "owner@ada.ng",
		Subject: "Your order AK-1", Text: "Thank you.\nWe'll call you.", Key: "mail"}
	if _, err := e.enqueue(t, e.t1, m); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	if len(e.mail.msgs) != 1 {
		t.Fatalf("mails %d", len(e.mail.msgs))
	}
	raw := string(e.mail.msgs[0])
	if !strings.Contains(raw, `From: "Ada's Kitchen via Taskiem" <no-reply@taskiem.test>`) || !strings.Contains(raw, "Reply-To: owner@ada.ng") ||
		!strings.Contains(raw, "To: customer@example.com") || !strings.Contains(raw, "We'll call you.") {
		t.Fatalf("%s", raw)
	}
	if r := e.row(t, e.t1, "mail"); r.status != "sent" || r.provider != "smtp" || r.sealed {
		t.Fatalf("%+v", r)
	}
	// A permanent refusal from the mail server is dead.
	e.mail.err = &textproto.Error{Code: 550, Msg: "no such user"}
	m.Key, m.To = "mail2", "nobody@example.com"
	if _, err := e.enqueue(t, e.t1, m); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	if r := e.row(t, e.t1, "mail2"); r.status != "dead" {
		t.Fatalf("%+v", r)
	}
	if _, mails, err := Usage(context.Background(), e.d.App, e.t1, time.Now().Add(-time.Hour)); err != nil || mails != 1 {
		t.Fatalf("usage %d %v", mails, err)
	}
}

func TestSMSAllowance(t *testing.T) {
	e := setup(t)
	e.q.Allowance = func(context.Context, uuid.UUID) (int64, error) { return 2, nil }
	for i, to := range []string{"08031234561", "08031234562"} {
		if ok, err := e.enqueue(t, e.t1, sms(fmt.Sprint("a", i), to)); err != nil || !ok {
			t.Fatalf("within the allowance: %v %v", ok, err)
		}
	}
	// The month's allowance is used: notices are refused, codes still go,
	// and email is not counted.
	if _, err := e.enqueue(t, e.t1, sms("a3", "08031234563")); !errors.Is(err, ErrAllowance) {
		t.Fatalf("beyond the allowance: %v", err)
	}
	code := sms("code", "08031234563")
	code.Purpose, code.Text = PurposeCode, "Your code is 123456."
	if ok, err := e.enqueue(t, e.t1, code); err != nil || !ok {
		t.Fatalf("a code: %v %v", ok, err)
	}
	mail := Message{Channel: Email, Purpose: PurposeConfirmation, To: "ada@example.com", SenderName: "Ada's Kitchen", Subject: "Order", Text: "Confirmed.", Key: "mail"}
	if ok, err := e.enqueue(t, e.t1, mail); err != nil || !ok {
		t.Fatalf("an email: %v %v", ok, err)
	}
	// No limit (0) takes everything.
	e.q.Allowance = func(context.Context, uuid.UUID) (int64, error) { return 0, nil }
	if ok, err := e.enqueue(t, e.t1, sms("a4", "08031234564")); err != nil || !ok {
		t.Fatalf("no limit: %v %v", ok, err)
	}
}
