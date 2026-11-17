package chatlinks

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Telling a person that a business wrote to them in their chat with it
// (docs/spec/taskiem-for-everyone.md §8.3, §9; founder's decision of
// 2026-11-17):
//   - first a push to every live device of the person's own (the app, or
//     a browser allowed to be notified): "{business}: {first words}" (the
//     device's preview setting respected), opening the chat on You;
//   - only when the person has no such device (an iPhone's browser not
//     added to the home screen, say), and only for a reply from a person
//     at the business (never Taskiem's automatic words, such as order
//     updates), an SMS, "{business} answered you on Taskiem: {link}", at
//     most once per conversation per hour. The SMS counts against the
//     business's plan (its SMS allowance), as every message to its
//     customers does (§15).

// AnsweredEvery is how often a conversation may send the SMS.
const AnsweredEvery = time.Hour

// Answer is a business writing to a person in a customer conversation.
type Answer struct {
	// Tenant is the business that wrote.
	Tenant uuid.UUID
	// Conversation is the person's conversation (person_conversations.id,
	// /you/chats/{id}).
	Conversation uuid.UUID
	// Person is the customer's account.
	Person uuid.UUID
	// Words are the message's first words, or what it is ("Sent a photo").
	Words string
	// FromPerson: someone at the business wrote it, not Taskiem's
	// automatic words for it (a system: sender: order updates). Only such
	// a reply may send the SMS.
	FromPerson bool
	// Offer: an offer (pushed, never texted).
	Offer bool
}

// Teller tells a person a business wrote to them. The conversation code
// calls it after a business's message is kept, outside its transaction.
type Teller interface {
	TellAnswered(ctx context.Context, a Answer) error
}

// NoTell tells no one: the default until a server is set up to.
type NoTell struct{}

// TellAnswered does nothing.
func (NoTell) TellAnswered(context.Context, Answer) error { return nil }

// PersonPush is a push to a person's own devices.
type PersonPush struct {
	// Title is the business; Body the first words.
	Title, Body string
	// Link is the chat on You (/you/chats/{id}); Tag the notification it
	// replaces on a device (one per chat).
	Link, Tag string
	// Kind is what it tells, for the person's choice of pushes on You:
	// KindMessages (someone at the business wrote), KindUpdates (its
	// automatic words: an order's or a booking's news) or KindOffers.
	Kind string
}

// The kinds of push a person may turn off on You (§7's notifications).
const (
	KindMessages = "messages"
	KindUpdates  = "updates"
	KindOffers   = "offers"
)

// KindOf is the kind of push an answer is.
func KindOf(a Answer) string {
	switch {
	case a.Offer:
		return KindOffers
	case !a.FromPerson:
		return KindUpdates
	}
	return KindMessages
}

// Pusher queues a push to a person's own devices; sent is false when they
// have none a push can reach now, so the SMS may go instead. A person who
// turned the push's kind off is told nothing, by push or by SMS: the
// Pusher answers sent for them.
type Pusher interface {
	PushPerson(ctx context.Context, person uuid.UUID, p PersonPush) (sent bool, err error)
}

// Tell is the server's Teller.
type Tell struct {
	Pool  *pgxpool.Pool
	Links *Links
	// Messages sends the SMS, from the business's outbox and against its
	// allowance; nil sends none.
	Messages *sender.Queue
	// Push is the person's own devices; nil: none (an answer goes by SMS).
	Push Pusher
}

// pushWords is at most this many letters of a message in a push.
const pushWords = 120

// firstWords is the start of a message for a push: its words up to
// pushWords letters, at a word's edge where there is one.
func firstWords(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= pushWords {
		return s
	}
	r := []rune(s)[:pushWords]
	cut := string(r)
	if i := strings.LastIndex(cut, " "); i > pushWords/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

// ChatLink is the person's conversation on You, as a push opens it.
func ChatLink(conversation uuid.UUID) string { return "/you/chats/" + conversation.String() }

// TellAnswered pushes to the person's own devices; else, for a reply from
// someone at the business, sends the SMS when the conversation has not had
// one in the last hour. A person with no verified phone, a business out of
// SMS, or a number that cannot have one is told nothing more: the message
// is in the chat.
func (t *Tell) TellAnswered(ctx context.Context, a Answer) error {
	if t == nil || t.Pool == nil || a.Tenant == uuid.Nil || a.Person == uuid.Nil {
		return nil
	}
	var name string
	if err := db.InTenantTx(ctx, t.Pool, []uuid.UUID{a.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, a.Tenant).Scan(&name)
	}); err != nil {
		return err
	}
	if t.Push != nil {
		words := firstWords(a.Words)
		if words == "" {
			words = "New message"
		}
		sent, err := t.Push.PushPerson(ctx, a.Person, PersonPush{Title: sender.CleanName(name), Body: words,
			Link: ChatLink(a.Conversation), Tag: "you:" + a.Conversation.String(), Kind: KindOf(a)})
		if err != nil {
			return err
		}
		if sent {
			return nil
		}
	}
	// No device: the SMS, for a person's reply only.
	if !a.FromPerson || a.Offer || t.Messages == nil {
		return nil
	}
	var link string
	if err := db.InTenantTx(ctx, t.Pool, []uuid.UUID{a.Tenant}, func(tx pgx.Tx) error {
		var err error
		link, err = t.Links.LinkFor(ctx, tx, a.Tenant)
		return err
	}); err != nil {
		return err
	}
	if link == "" {
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
