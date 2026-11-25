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

// followUpChat tries WhatsApp for a record whose caps were taken: sent
// true once the template went, with the record marked followed up on
// WhatsApp and its restore link written; false when WhatsApp cannot
// carry it (the buyer did not ask for WhatsApp where the template is a
// marketing one, no own number, the template not approved there) and the
// text goes instead.
func (s *Service) followUpChat(ctx context.Context, tx pgx.Tx, x dueRow, m FollowupMessage) (bool, error) {
	if s.FollowupChat.OptIn() && !x.choice.WhatsApp {
		return false, nil
	}
	token := NewLinkToken()
	m.Token, m.Link = token, s.backLink(ctx, tx, x.tenant, token)
	ref, sent, err := s.FollowupChat.FollowUp(ctx, tx, m)
	if err != nil || !sent {
		return false, err
	}
	ok, err := s.followedWith(ctx, tx, x.tenant, x.id, "whatsapp", token)
	if err != nil || !ok {
		return ok, err
	}
	if ref != "" {
		if _, err := tx.Exec(ctx, `SELECT taskiem_form_left_wa_sent($1, $2, $3)`, x.tenant, x.id, ref); err != nil {
			return false, err
		}
	}
	return true, nil
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

// FollowupFailed is WhatsApp's word that a follow-up it took was not
// delivered (a number not on WhatsApp): the record goes by text instead,
// once, with a link of its own; ref is the message's id. Nothing for a
// message that was no follow-up, or one whose buyer came back.
func (s *Service) FollowupFailed(ctx context.Context, tenant uuid.UUID, ref string) error {
	if s.Messages == nil || ref == "" {
		return nil
	}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		token := NewLinkToken()
		x := dueRow{tenant: tenant, on: true}
		err := tx.QueryRow(ctx, `SELECT id, action_id, front_id, site, contact, contact_kind, contact_hash, choice, last_keep_at FROM taskiem_form_left_wa_failed($1, $2, $3)`,
			tenant, ref, TokenHash(token)).Scan(&x.id, &x.action, &x.front, &x.site, &x.contact, &x.kind, &x.hash, &x.choice, &x.lastKeep)
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
