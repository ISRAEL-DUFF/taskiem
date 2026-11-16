package chatlinks

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Telling a customer in the browser chat that the business answered
// (docs/spec/taskiem-for-everyone.md §9): the browser's push where the
// person allowed it, else an SMS, "{business} answered you on Taskiem:
// {link}", at most once per conversation per hour. The SMS counts against
// the business's plan (its SMS allowance), as every message to its
// customers does (§15).

// AnsweredEvery is how often a conversation may send the SMS.
const AnsweredEvery = time.Hour

// Answer is a business answering a person in a customer conversation.
type Answer struct {
	// Tenant is the business that answered.
	Tenant uuid.UUID
	// Conversation is the customer conversation (its id on either side,
	// the same each time).
	Conversation uuid.UUID
	// Person is the customer's account.
	Person uuid.UUID
}

// Teller tells a person a business answered them. The conversation code
// (stream A) calls it after a business's reply is kept, outside the
// reply's transaction.
type Teller interface {
	TellAnswered(ctx context.Context, a Answer) error
}

// NoTell tells no one: the default until a server is set up to.
type NoTell struct{}

// TellAnswered does nothing.
func (NoTell) TellAnswered(context.Context, Answer) error { return nil }

// Pusher sends a person's browsers a push; sent is false when they have
// none that may be pushed to (no subscription, or the browser cannot be
// notified), so the SMS goes instead.
type Pusher interface {
	PushPerson(ctx context.Context, person uuid.UUID, title, body, link string) (sent bool, err error)
}

// Tell is the server's Teller.
type Tell struct {
	Pool  *pgxpool.Pool
	Links *Links
	// Messages sends the SMS, from the business's outbox and against its
	// allowance; nil sends none.
	Messages *sender.Queue
	// Push is the person's browsers; nil: none (every answer goes by SMS).
	Push Pusher
}

// TellAnswered pushes to the person's browser, else sends the SMS when
// the conversation has not had one in the last hour. A person with no
// verified phone, a business out of SMS, or a number that cannot have one
// is told nothing more: the answer is in the chat.
func (t *Tell) TellAnswered(ctx context.Context, a Answer) error {
	if t == nil || t.Pool == nil || a.Tenant == uuid.Nil || a.Person == uuid.Nil {
		return nil
	}
	var name string
	var link string
	if err := db.InTenantTx(ctx, t.Pool, []uuid.UUID{a.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, a.Tenant).Scan(&name); err != nil {
			return err
		}
		var err error
		link, err = t.Links.LinkFor(ctx, tx, a.Tenant)
		return err
	}); err != nil {
		return err
	}
	if t.Push != nil {
		sent, err := t.Push.PushPerson(ctx, a.Person, name, name+" answered you", link)
		if err != nil {
			return err
		}
		if sent {
			return nil
		}
	}
	if t.Messages == nil || link == "" {
		return nil
	}
	var number *string
	if err := t.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_of($1)`, a.Person).Scan(&number); err != nil {
		return err
	}
	if number == nil {
		return nil
	}
	return db.InTenantTx(ctx, t.Pool, []uuid.UUID{a.Tenant}, func(tx pgx.Tx) error {
		took, err := TakeNoticeTx(ctx, tx, a.Tenant, NoticeAnsweredSMS, a.Conversation[:], AnsweredEvery)
		if err != nil || !took {
			return err
		}
		hour := time.Now().UTC().Format("2006010215")
		_, err = t.Messages.Enqueue(ctx, tx, sender.Message{Tenant: a.Tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: *number,
			SenderName: name, Text: AnsweredText(name, link), Key: "answered:" + a.Conversation.String() + ":" + hour})
		if errors.Is(err, sender.ErrAllowance) || errors.Is(err, sender.ErrRecipientLimit) || errors.Is(err, sender.ErrInvalid) {
			return nil // told nothing more; the hour is still taken
		}
		return err
	})
}

// AnsweredText is the SMS: "{business} answered you on Taskiem: {link}".
func AnsweredText(business, link string) string {
	return sender.CleanName(business) + " answered you on Taskiem: " + link
}
