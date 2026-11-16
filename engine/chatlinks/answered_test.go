package chatlinks_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

type fakePush struct {
	sent  bool
	calls int
}

func (f *fakePush) PushPerson(context.Context, uuid.UUID, string, string, string) (bool, error) {
	f.calls++
	return f.sent, nil
}

// The browser chat's SMS (docs/spec/taskiem-for-everyone.md §9): a push
// where the person's browser takes one, else "{business} answered you on
// Taskiem: {link}" by SMS, at most once per conversation per hour, from the
// business's own outbox (its plan's SMS).
func TestTellAnswered(t *testing.T) {
	env := rt.New(t)
	env.NoReplay = true
	ctx := context.Background()
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET name = 'Mama''s Kitchen' WHERE id = $1`, env.Tenant); err != nil {
		t.Fatal(err)
	}
	person := uuid.Must(uuid.NewV7())
	if _, err := env.DB.Admin.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'Bola')`, person, person.String()+"@phone.taskiem.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Admin.Exec(ctx, `INSERT INTO user_phones (user_id, number) VALUES ($1, '+2348031234567')`, person); err != nil {
		t.Fatal(err)
	}
	links := &chatlinks.Links{Pool: env.DB.App, Key: bytes.Repeat([]byte{7}, 32), Base: "https://taskiem.test"}
	push := &fakePush{}
	tell := &chatlinks.Tell{Pool: env.DB.App, Links: links, Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Push: push}
	conv, other := uuid.New(), uuid.New()
	sms := func() int {
		t.Helper()
		var n int
		if err := env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND channel = 'sms' AND purpose = 'notice'`, env.Tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	answer := func(c uuid.UUID) {
		t.Helper()
		if err := tell.TellAnswered(ctx, chatlinks.Answer{Tenant: env.Tenant, Conversation: c, Person: person}); err != nil {
			t.Fatal(err)
		}
	}
	answer(conv)
	if got := sms(); got != 1 {
		t.Fatalf("first answer: %d SMS", got)
	}
	answer(conv)
	answer(conv)
	if got := sms(); got != 1 || push.calls != 3 {
		t.Fatalf("within the hour: %d SMS, %d pushes tried", got, push.calls)
	}
	// Another conversation has its own hour.
	answer(other)
	if got := sms(); got != 2 {
		t.Fatalf("another conversation: %d", got)
	}
	// An hour later, one more (the outbox's key is the hour's: moved too).
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE customer_chat_notices SET last_at = last_at - interval '61 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE platform_messages SET dedup_key = dedup_key || ':before'`); err != nil {
		t.Fatal(err)
	}
	answer(conv)
	if got := sms(); got != 3 {
		t.Fatalf("an hour later: %d", got)
	}
	var link chatlinks.Link
	in(t, env.DB, env.Tenant, func(tx pgx.Tx) error {
		var err error
		link, err = links.CurrentTx(ctx, tx, env.Tenant, "test")
		return err
	})
	if got, want := chatlinks.AnsweredText("Mama's Kitchen", link.URL), "Mama's Kitchen answered you on Taskiem: https://taskiem.test/m/"+link.Code; got != want {
		t.Fatalf("words: %q", got)
	}

	// A browser that takes the push: no SMS.
	push.sent = true
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE customer_chat_notices SET last_at = last_at - interval '61 minutes'`); err != nil {
		t.Fatal(err)
	}
	answer(conv)
	if got := sms(); got != 3 {
		t.Fatalf("pushed, and texted: %d", got)
	}
	// A person with no phone: nothing, and no error.
	push.sent = false
	if err := tell.TellAnswered(ctx, chatlinks.Answer{Tenant: env.Tenant, Conversation: uuid.New(), Person: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	if got := sms(); got != 3 {
		t.Fatalf("no phone, texted: %d", got)
	}
	// NoTell tells no one.
	if err := (chatlinks.NoTell{}).TellAnswered(ctx, chatlinks.Answer{}); err != nil {
		t.Fatal(err)
	}
}
