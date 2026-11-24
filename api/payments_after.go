package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// After a payment (docs/spec/chat-pay-people.md, "After" and Settled 4
// and 6; build-plan-gaps H7):
//
//   - A diary line in the chat it was paid from, "Paid Chidi Okeke ₦45,000
//     (delivery) · GTBank ••6789", naming the payment, so it shows as the
//     payment stands: received by the bank, or failed with Try again.
//   - The fee as its own money-log line, bank charges, once the provider
//     reports it.
//   - The person paid told in their chat once the provider confirms (a
//     rider on their chat app, a staff member in their Taskiem chat and
//     chat app), and again if it fails or comes back: the amount, what
//     for, the date and the last four digits of their own account only;
//     never who approved, the payer's balance or the fee. Not for wages,
//     an owner's own account or refunds to customers.

// outboxPayee is the outbox kind of a message to the person paid.
const outboxPayee = "payee_paid"

// payeeReachable says whether the person a payment goes to can be told in
// a chat of theirs: a rider or a staff member, for anything but wages or
// a refund, never an owner's own account (Settled 6).
func payeeReachable(x payment) bool {
	if x.Personal || x.Purpose == "wages" || x.Purpose == "refund" {
		return false
	}
	return x.RiderID != nil || x.StaffID != nil
}

// diaryWhere checks where a payment's diary line goes: "taskiem" (the
// person's own Taskiem conversation, the default for a person), or one of
// the business's active links (their side of it); nil for none (prepared
// by no person).
func (s *Server) diaryWhere(ctx context.Context, tenant, person uuid.UUID, where string) (*string, error) {
	where = strings.TrimSpace(where)
	switch {
	case person == uuid.Nil:
		// A workflow's, or an API key's: no chat of a person's.
		return nil, nil
	case where == "" || where == "taskiem":
		// A person's payment from Tools has its line in their Taskiem chat.
		t := "taskiem"
		return &t, nil
	}
	link, err := uuid.Parse(where)
	if err != nil {
		return nil, refusePay(http.StatusBadRequest, "where", "Where is your Taskiem chat or a business's.")
	}
	ok := false
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		l, err := links.GetTx(ctx, tx, link)
		if errors.Is(err, links.ErrNotFound) {
			return nil
		}
		ok = err == nil && l.State == links.Active
		return err
	}); err != nil {
		return nil, err
	}
	if !ok {
		return nil, refusePay(http.StatusBadRequest, "where", "That chat is not one of yours.")
	}
	w := link.String()
	return &w, nil
}

// purposeShort is what a payment was for, in a word or two.
func purposeShort(x payment) string {
	if x.Purpose == "other" && x.PurposeNote != "" {
		return x.PurposeNote
	}
	return map[string]string{"bill": "a bill", "wages": "wages", "delivery": "delivery", "refund": "refund", "stock": "stock", "other": "other"}[x.Purpose]
}

// payDiaryText is a payment's diary line.
func payDiaryText(x payment) string {
	return fmt.Sprintf("Paid %s %s (%s) · %s %s", payeeWords(x), invoicing.Naira(x.AmountKobo), purposeShort(x), x.BankName, x.Account)
}

// batchDiaryText is a batch's one diary line: "Paid 12 people ₦386,000
// (riders, week 45)".
func batchDiaryText(b batchView) string {
	who := fmt.Sprintf("%d people", b.Count)
	if b.Count == 1 {
		who = "1 person"
	}
	t := fmt.Sprintf("Paid %s %s", who, invoicing.Naira(b.TotalKobo))
	if strings.TrimSpace(b.Title) != "" {
		t += " (" + strings.TrimSpace(b.Title) + ")"
	}
	return t
}

// payDiaryTx writes a diary line for a payment (or a batch) where it was
// paid from: the person's own Taskiem conversation, or their side of a
// linked business's conversation.
func (s *Server) payDiaryTx(ctx context.Context, tx pgx.Tx, p *Principal, where *string, text string, payment, batch *uuid.UUID) error {
	if where == nil || p.UserID == uuid.Nil {
		return nil
	}
	if *where == "taskiem" {
		env, err := s.sealMemberText(ctx, tx, p.TenantID, p.UserID, text)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO chat_history (id, tenant_id, user_id, channel, inbound, kind, body, buttons, expires_at, payment_id, batch_id)
			VALUES ($1, $2, $3, 'web', false, 'diary', $4, '{}', now() + $5::interval, $6, $7)`,
			uuid.Must(uuid.NewV7()), p.TenantID, p.UserID, env, s.chatHistoryTTL().String(), payment, batch)
		return err
	}
	link, err := uuid.Parse(*where)
	if err != nil {
		return nil //nolint:nilerr // checked when it was prepared
	}
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&name); err != nil {
		return err
	}
	_, err = links.PaymentDiaryTx(ctx, tx, p.TenantID, link, text, links.Sender{ID: p.Actor(), FirstName: firstWord(name)}, payment, batch)
	return err
}

// paymentSent writes the diary line of a payment just confirmed (once:
// a payment confirmed again to give the payer's OK has its line).
func (s *Server) paymentSent(ctx context.Context, p *Principal, x payment) {
	if x.diaryWhere == nil {
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var had bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chat_history WHERE payment_id = $1) OR EXISTS (SELECT 1 FROM link_messages WHERE payment_id = $1)`,
			x.ID).Scan(&had); err != nil || had {
			return err
		}
		return s.payDiaryTx(ctx, tx, p, x.diaryWhere, payDiaryText(x), &x.ID, nil)
	}); err != nil {
		s.Logger.Warn("payments: the diary line", "payment", x.ID, "err", err)
	}
}

// afterPaymentTx, in the transaction that recorded payment id's end, writes
// its fee as its own money-log line (bank charges) and tells the person
// paid in their chat (once it is paid).
func (s *Server) afterPaymentTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	x, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1`, id))
	if err != nil {
		return err
	}
	if x.Status != "paid" {
		return nil
	}
	if x.FeeKobo != nil && *x.FeeKobo > 0 && x.payoutRef != nil && *x.payoutRef != "" {
		e := moneylog.FeeEntry(x.provider, *x.payoutRef, *x.FeeKobo, "Fee: "+payDiaryText(x))
		if _, _, err := s.moneyLog().RecordTx(ctx, tx, tenant, e); err != nil && !errors.Is(err, moneylog.ErrInvalid) {
			return err
		}
	}
	return s.tellPayeeTx(ctx, tx, tenant, x, "paid")
}

// tellPayeeTx queues the message to the person paid, for how a payment
// stands now (paid, failed or reversed), once each; a payment never told
// it was paid is not told it failed.
func (s *Server) tellPayeeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x payment, now string) error {
	if !x.TellPayee || !payeeReachable(x) {
		return nil
	}
	told := deref(x.payeeTold)
	if now == "paid" && told != "" || now != "paid" && told != "paid" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE payments SET payee_told = $2 WHERE id = $1`, x.ID, now); err != nil {
		return err
	}
	if x.StaffID != nil {
		// Their own Taskiem conversation: a card that shows the payment as
		// it stands (paid, or came back), kept as their messages are.
		if now == "paid" {
			var business string
			if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
				return err
			}
			env, err := s.sealMemberText(ctx, tx, tenant, *x.StaffID, payeeWordsFor(lang.EN, business, x))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO chat_history (id, tenant_id, user_id, channel, inbound, kind, body, buttons, expires_at, payment_id)
				SELECT $1, $2, $3, 'web', false, 'paid_you', $4, '{}', now() + $5::interval, $6
				WHERE EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $2 AND user_id = $3)`,
				uuid.Must(uuid.NewV7()), tenant, *x.StaffID, env, s.chatHistoryTTL().String(), x.ID); err != nil {
				return err
			}
		}
		ch, err := firstChatTx(ctx, tx, *x.StaffID)
		if err != nil || ch == "" {
			return err
		}
		_, err = whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: ch, Kind: outboxPayee, User: *x.StaffID, Payment: &x.ID},
			"payee:"+x.ID.String()+":"+now)
		return err
	}
	// A rider: on the chat app they tapped in on, found when it is sent.
	_, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Kind: outboxPayee, Payment: &x.ID}, "payee:"+x.ID.String()+":"+now)
	return err
}

// payeeWordsFor is the message to the person paid, by how the payment
// stands: the amount, what for, the date and their account's last four
// digits; never who approved, the balance or the fee.
func payeeWordsFor(l lang.Tag, business string, x payment) string {
	at := x.CreatedAt
	if x.FinishedAt != nil {
		at = *x.FinishedAt
	}
	day := at.In(lagosTZ).Format("2 Jan 2006")
	last4 := strings.TrimPrefix(x.Account, "••")
	what := paymentPurposeWords(x.Purpose)
	if x.Purpose == "other" && x.PurposeNote != "" {
		what = x.PurposeNote
	}
	amount := invoicing.Naira(x.AmountKobo)
	switch x.Status {
	case "failed", "reversed":
		return tr(l, "wa.pay.payee.back", "business", business, "amount", amount, "date", day)
	}
	return tr(l, "wa.pay.payee.paid", "business", business, "amount", amount, "for", what, "date", day, "last4", last4)
}

// sendPayeeItem sends the message to the person paid: a staff member on
// their chat app, a rider on the one they tapped in on.
func (s *Server) sendPayeeItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Payment == nil {
		return fmt.Errorf("%w: no payment", whatsapp.ErrNothingToSend)
	}
	var x payment
	var business, phone string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		var err error
		if x, err = scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1`, *it.Payment)); err != nil {
			return err
		}
		if x.RiderID != nil && it.User == uuid.Nil && s.Riders.Ready() {
			rd, err := s.Riders.GetTx(ctx, tx, it.Tenant, *x.RiderID)
			if err != nil {
				return err
			}
			phone = rd.Phone
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the payment is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	if !payeeReachable(x) || !x.TellPayee {
		return fmt.Errorf("%w: not told", whatsapp.ErrNothingToSend)
	}
	var ch chatChannel
	var addr string
	switch {
	case it.User != uuid.Nil:
		for _, c := range append([]string{it.Channel}, s.chatChannelsOn()...) {
			if ch, addr, err = s.chatAddrOf(ctx, c, it.Tenant, it.User); !errors.Is(err, whatsapp.ErrNothingToSend) {
				break
			}
		}
		if err != nil {
			return err
		}
	case phone != "" && s.Dispatch.Ready() && s.Reach.Ready():
		var t reach.Target
		var ok bool
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
			var err error
			if t, ok, err = s.Riders.Book.FindTx(ctx, tx, it.Tenant, uuid.Nil, phone, s.Dispatch.Chat); err != nil || !ok || t.Contact == uuid.Nil {
				ok = false
				return err
			}
			addr, err = s.Reach.AddressTx(ctx, tx, it.Tenant, reach.Target{Channel: t.Channel, Contact: t.Contact})
			return err
		}); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: the rider is on no chat app", whatsapp.ErrNothingToSend)
		}
		if ch, err = s.chatChannelFor(ctx, t.Channel, it.Tenant); err != nil {
			return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
		}
	default:
		return fmt.Errorf("%w: nobody to tell", whatsapp.ErrNothingToSend)
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	text := payeeWordsFor(l, business, x)
	tpl := whatsapp.TplUpdate
	m := whatsapp.Message{Tenant: it.Tenant, Text: ch.Prefix(business) + text, Template: &tpl,
		Vars: map[string]string{"tenant": business, "about": "payment", "message": oneLine(text)}}
	if _, err := ch.Send(ctx, addr, m); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}
