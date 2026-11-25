package forms

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/sender"
)

// WhatsApp follow-ups (docs/spec/forms-that-sell.md §6.4, L4). A typed
// phone is unproven (0039 point 3): it is never matched to a linked chat,
// and WhatsApp reaches it only by the approved "finish your order"
// template from the business's own number, never by a free-form message,
// whatever its window. FollowupChat is nil until the deployment names
// that template, and then a phone is reached as in L3: a text. The caps,
// the hold and STOP are the record's, whatever the channel: they are
// taken before the channel is chosen (followUpTx).
//
// A template is sent at most once (the review of L4): the record is
// claimed as sending and committed, with the caps, before WhatsApp is
// asked; the answer is written after. One WhatsApp clearly refused goes
// by text, once; one whose outcome is unclear (a timeout once Meta may
// have taken it, a crash) stays sent.

// chatReady says whether a record's follow-up may go on WhatsApp: the
// form the buyer saw named WhatsApp (Choice.WhatsAppNamed: the public
// host says which line it drew), the buyer asked for it where the
// template is a marketing one, and the business's account holds the
// template ready. A chat that cannot tell is a no: the text goes.
func (s *Service) chatReady(ctx context.Context, x dueRow) bool {
	if s.FollowupChat == nil || !x.choice.WhatsAppNamed || s.FollowupChat.OptIn() && !x.choice.WhatsApp {
		return false
	}
	ok, err := s.FollowupChat.Ready(ctx, x.tenant)
	if err != nil {
		s.log().Warn("forms: whether a follow-up may go on WhatsApp", "tenant", x.tenant, "err", err)
		return false
	}
	return ok
}

// claimChat marks a record followed up on WhatsApp, sending, with its
// restore link; m gets the link's token. False when it was followed up
// meanwhile.
func (s *Service) claimChat(ctx context.Context, tx pgx.Tx, x dueRow, m *FollowupMessage) (bool, error) {
	token := NewLinkToken()
	ok, err := s.followedWith(ctx, tx, x.tenant, x.id, "whatsapp", token)
	if err != nil || !ok {
		return ok, err
	}
	if _, err := tx.Exec(ctx, `SELECT taskiem_form_left_wa_sending($1, $2)`, x.tenant, x.id); err != nil {
		return false, err
	}
	m.Token, m.Link = token, s.backLink(ctx, tx, x.tenant, token)
	m.Key = "followup:" + x.id.String()
	return true, nil
}

// sendChat sends a claimed follow-up on WhatsApp, outside any
// transaction, and writes what became of it: sent (or unclear, taken as
// sent), or clearly not sent and texted instead, once.
func (s *Service) sendChat(ctx context.Context, x dueRow, m FollowupMessage) error {
	ref, err := s.FollowupChat.Send(ctx, m)
	// What follows is written whatever becomes of the caller's context:
	// a record left sending is never sent again either way.
	bg := context.WithoutCancel(ctx)
	if errors.Is(err, ErrChatNotSent) {
		return s.chatUnsent(bg, x, m)
	}
	if err != nil {
		s.log().Warn("forms: a WhatsApp follow-up's outcome is unclear; taken as sent, never sent again", "tenant", x.tenant, "err", err)
		ref = ""
	}
	var code *int32
	if err := db.InTenantTx(bg, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(bg, `SELECT taskiem_form_left_wa_sent($1, $2, $3)`, x.tenant, x.id, ref).Scan(&code)
	}); err != nil {
		return err
	}
	if code != nil {
		// WhatsApp reported it failed before its id was written.
		return s.FollowupFailed(bg, x.tenant, ref, int(*code))
	}
	return nil
}

// chatUnsent texts a follow-up WhatsApp clearly did not take, with the
// link already made, once; one the outbox refuses is not followed up
// after all.
func (s *Service) chatUnsent(ctx context.Context, x dueRow, m FollowupMessage) error {
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_form_left_wa_unsent($1, $2)`, x.tenant, x.id).Scan(&ok); err != nil || !ok {
			return err
		}
		return s.sendFollowup(ctx, tx, sender.SMS, m)
	})
	if outcome, ok := notSent(err); ok {
		return db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_form_left_unfollow($1, $2, $3)`, x.tenant, x.id, outcome)
			return err
		})
	}
	return err
}

// followedWith is FollowedUp with the restore link's token given: the
// one the message carries already.
func (s *Service) followedWith(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, channel, token string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT taskiem_form_left_followed($1, $2, $3, $4, $5)`, tenant, id, TokenHash(token), s.now(), channel).Scan(&ok)
	return ok, err
}

// errNothingToText rolls back a failed WhatsApp follow-up whose text
// cannot be written (its form or product gone from the page).
var errNothingToText = errors.New("forms: nothing to text")

// WhatsApp's error codes for a failed message that are no reason to text
// (§6.4, the review of L4).
const (
	// WAStoppedMarketing: the person stopped marketing messages from the
	// business on WhatsApp; recorded as a stop of its follow-ups.
	WAStoppedMarketing = 131050
	// WAMarketingLimit: Meta's limit of marketing messages a person gets;
	// left followed up, never tried again.
	WAMarketingLimit = 131049
)

// FollowupFailed is WhatsApp's word that a follow-up it took was not
// delivered, with its error code; ref is the message's id. A number not
// on WhatsApp goes by text instead, once, with a link of its own, unless
// the contact stopped the business's follow-ups, the form no longer
// follows up, the buyer came back or the page's order was recorded since.
// A person who stopped the business's marketing on WhatsApp is recorded
// as a stop, and Meta's marketing limit is left as followed up: neither
// is texted. Nothing for a message that was no follow-up; one whose id is
// not written yet is remembered for when it is (sendChat).
func (s *Service) FollowupFailed(ctx context.Context, tenant uuid.UUID, ref string, code int) error {
	if s.Messages == nil || ref == "" {
		return nil
	}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		token := NewLinkToken()
		x := dueRow{tenant: tenant, on: true}
		err := tx.QueryRow(ctx, `SELECT id, action_id, front_id, site, contact, contact_kind, contact_hash, choice, last_keep_at FROM taskiem_form_left_wa_failed($1, $2, $3, $4)`,
			tenant, ref, TokenHash(token), code).Scan(&x.id, &x.action, &x.front, &x.site, &x.contact, &x.kind, &x.hash, &x.choice, &x.lastKeep)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		m, ok, err := s.followupMessage(ctx, tx, x)
		if err != nil {
			return err
		}
		if !ok {
			return errNothingToText
		}
		m.Link, m.Key = s.backLink(ctx, tx, tenant, token), "followup:"+x.id.String()+":text"
		return s.sendFollowup(ctx, tx, sender.SMS, m)
	})
	if errors.Is(err, errNothingToText) {
		return nil
	}
	if _, ok := notSent(err); ok {
		// The outbox refused the text (the person's limits, the plan's
		// texts): the WhatsApp follow-up stands as it was.
		s.log().Warn("forms: the text after a failed WhatsApp follow-up was refused", "tenant", tenant, "err", err)
		return nil
	}
	return err
}

// StopFollowups stops every follow-up to a contact from a business (a
// STOP on WhatsApp, or the template's Stop), as StopContact does, and
// says whether the business followed that contact up in the last 7 days,
// so the chat can answer the STOP.
func (s *Service) StopFollowups(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, contact string) (bool, error) {
	if s.Messages == nil {
		return false, nil
	}
	kind, value, err := Keep{Contact: contact}.ContactOf()
	if err != nil {
		return false, nil //nolint:nilerr // a sender that is no phone has no follow-ups
	}
	var had bool
	err = tx.QueryRow(ctx, `SELECT taskiem_form_followup_stop($1, $2)`, tenant, s.ContactHash(kind, value)).Scan(&had)
	return had, err
}
