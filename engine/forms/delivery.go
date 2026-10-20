package forms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sender"
)

// AlertFormRequest is the alert members of a role get for a form step
// addressed to them: its link opens in the app.
const AlertFormRequest = "form.request"

// Tick sends what is due: form steps' links and reminders, verification
// codes, and expires requests past their time. The api role runs it.
func (s *Service) Tick(ctx context.Context) error {
	errs := []error{s.SendCodes(ctx)}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_form_request_tenants()`)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, t := range tenants {
		if err := s.sendRequests(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

type pending struct {
	id        uuid.UUID
	title     string
	recipient map[string]any
	prefill   map[string]any
	via       []string
	verify    bool
	remind    []time.Duration
	sent      *time.Time
	reminders int
	expires   time.Time
	business  string
}

// RequestLink is a form step's link on the form host.
func (s *Service) RequestLink(id uuid.UUID) string {
	return "https://" + s.FormHost + "/q/" + s.Keys.RequestToken(id.String())
}

func (s *Service) sendRequests(ctx context.Context, tenant uuid.UUID) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE form_requests SET status = 'expired' WHERE status = 'pending' AND expires_at <= now()`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT r.id, r.title, r.recipient, r.prefill, r.via, r.verify,
			ARRAY(SELECT extract(epoch FROM x)::bigint FROM unnest(r.remind) x), r.sent_at, r.reminders_sent, r.expires_at, t.name
			FROM form_requests r JOIN tenants t ON t.id = r.tenant_id
			WHERE r.status = 'pending' AND (r.sent_at IS NULL OR r.reminders_sent < cardinality(r.remind)) ORDER BY r.created_at LIMIT 50 FOR UPDATE OF r SKIP LOCKED`)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
			var p pending
			var secs []int64
			err := r.Scan(&p.id, &p.title, &p.recipient, &p.prefill, &p.via, &p.verify, &secs, &p.sent, &p.reminders, &p.expires, &p.business)
			for _, x := range secs {
				p.remind = append(p.remind, time.Duration(x)*time.Second)
			}
			return p, err
		})
		if err != nil {
			return err
		}
		for _, p := range list {
			if err := s.sendRequest(ctx, tx, tenant, p); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) sendRequest(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p pending) error {
	now := s.now()
	first := p.sent == nil
	if !first && (p.reminders >= len(p.remind) || now.Before(p.sent.Add(p.remind[p.reminders]))) {
		return nil
	}
	to, err := s.open(ctx, tx, tenant, p.recipient)
	if err != nil {
		return err
	}
	link := s.RequestLink(p.id)
	if role, _ := to["role"].(string); role != "" {
		// Members by role: their link opens in the app.
		if !first {
			_, err = tx.Exec(ctx, `UPDATE form_requests SET reminders_sent = reminders_sent + 1 WHERE id = $1`, p.id)
			return err
		}
		app := strings.TrimRight(s.AppURL, "/") + "/forms/requests/" + p.id.String()
		if _, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertFormRequest, Dedup: p.id.String(), Title: "To fill in: " + p.title,
			Body: "For members with the role " + role + ".", Link: app, Detail: map[string]any{"role": role, "request": p.id.String()}}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE form_requests SET sent_at = now() WHERE id = $1`, p.id)
		return err
	}
	phone, _ := to["phone"].(string)
	email, _ := to["email"].(string)
	text := p.title + ". Please fill it in here: " + link
	key := "form:" + p.id.String() + ":link"
	if !first {
		text = "Reminder: " + p.title + ". Please fill it in here: " + link
		key = fmt.Sprintf("form:%s:remind:%d", p.id, p.reminders+1)
	}
	sent := false
	var lastErr error
	for _, ch := range p.via {
		addr := phone
		channel := sender.SMS
		if ch == "email" {
			addr, channel = email, sender.Email
		}
		if addr == "" || s.Messages == nil {
			continue
		}
		m := sender.Message{Tenant: tenant, Channel: channel, Purpose: sender.PurposeLink, To: addr, SenderName: p.business,
			Text: text, Key: key + ":" + ch, ExpiresAt: p.expires}
		if channel == sender.Email {
			m.Subject = p.title
		}
		_, err := s.Messages.Enqueue(ctx, tx, m)
		if errors.Is(err, sender.ErrInvalid) || errors.Is(err, sender.ErrRecipientLimit) {
			lastErr = err
			continue
		}
		if err != nil {
			return err
		}
		sent = true
		break // the first channel that takes it
	}
	if !first {
		_, err = tx.Exec(ctx, `UPDATE form_requests SET reminders_sent = reminders_sent + 1 WHERE id = $1`, p.id)
		return err
	}
	if !sent {
		note := "No way to send the link: the step has no phone or email it can reach"
		if s.Messages == nil {
			note = "No way to send the link: messages are not configured on this deployment"
		} else if lastErr != nil {
			note = "The link could not be sent: " + lastErr.Error()
		}
		s.log().Warn("forms: a form step's link was not sent", "request", p.id, "why", note)
		_, err = tx.Exec(ctx, `UPDATE form_requests SET sent_at = now(), note = $2 WHERE id = $1`, p.id, note)
		return err
	}
	prefill, err := s.openAny(ctx, tx, tenant, p.prefill)
	if err != nil {
		return err
	}
	view := RequestView{}
	if m, ok := prefill.(map[string]any); ok {
		view.Prefill = m
	}
	if p.verify {
		if phone != "" {
			view.Channel, view.Hint = "sms", MaskPhone(phone)
		} else {
			view.Channel, view.Hint = "email", MaskEmail(email)
		}
	}
	sealed, err := s.Keys.Seal(RequestViewAAD(p.id.String()), view)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE form_requests SET sent_at = now(), token_hash = $2, public_view = $3 WHERE id = $1`,
		p.id, TokenHash(s.Keys.RequestToken(p.id.String())), sealed)
	return err
}

// open opens a sealed map's values (a recipient).
func (s *Service) open(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, m map[string]any) (map[string]any, error) {
	v, err := s.openAny(ctx, tx, tenant, m)
	if err != nil {
		return nil, err
	}
	out, _ := v.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func (s *Service) openAny(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s.Vault == nil {
		return v, nil
	}
	return pii.Open(ctx, s.Vault, tx, tenant, v, pii.Taint{})
}

// SendCodes sends the verification codes the form host asked for.
func (s *Service) SendCodes(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, scope, payload FROM taskiem_form_codes_claim(20)`)
	if err != nil {
		return err
	}
	type code struct {
		id, tenant, scope uuid.UUID
		payload           []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (code, error) {
		var c code
		return c, r.Scan(&c.id, &c.tenant, &c.scope, &c.payload)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range list {
		var req CodeRequest
		if err := s.Keys.Open(CodeAAD(c.scope.String()), c.payload, &req); err != nil {
			continue
		}
		err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{c.tenant}, func(tx pgx.Tx) error {
			var business string
			if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, c.tenant).Scan(&business); err != nil {
				return err
			}
			addr, channel := req.Address, req.Channel
			if addr == "" {
				// A form step's request: the code goes to its recipient.
				var recipient map[string]any
				if err := tx.QueryRow(ctx, `SELECT recipient FROM form_requests WHERE id = $1`, c.scope).Scan(&recipient); err != nil {
					return err
				}
				to, err := s.open(ctx, tx, c.tenant, recipient)
				if err != nil {
					return err
				}
				if p, _ := to["phone"].(string); p != "" {
					addr, channel = p, "sms"
				} else {
					addr, _ = to["email"].(string)
					channel = "email"
				}
			}
			if addr == "" || s.Messages == nil {
				return nil
			}
			ch := sender.SMS
			if channel == "email" {
				ch = sender.Email
			}
			subject := ""
			if ch == sender.Email {
				subject = "Your code"
			}
			_, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: c.tenant, Channel: ch, Purpose: sender.PurposeCode, To: addr, SenderName: business,
				Subject: subject, Text: "Your code is " + req.Code + ". It expires in 10 minutes. Never share it with anyone.", Key: "form-code:" + c.id.String()})
			if errors.Is(err, sender.ErrInvalid) || errors.Is(err, sender.ErrRecipientLimit) {
				s.log().Warn("forms: a code was not sent", "scope", c.scope, "err", err)
				return nil
			}
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Respond sets the message a run's respondent sees on the result page and
// the status link (form@1 respond).
func (s *Service) Respond(ctx context.Context, tenant, run uuid.UUID, message string) (map[string]any, error) {
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > 1000 {
		return nil, fmt.Errorf("%w: a message is 1 to 1000 characters", ErrRefused)
	}
	var ref string
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE form_submissions SET message = $2, updated_at = now()
			WHERE id = (SELECT id FROM form_submissions WHERE run_id = $1 AND endpoint_id IS NOT NULL ORDER BY created_at DESC LIMIT 1) RETURNING reference`, run, message).Scan(&ref)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: this run was not started by a form", ErrNoRequest)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"reference": ref, "message": message}, nil
}

// FileURL is a short-lived download link for one of the organisation's
// form files (form@1 file_url): the content never goes into the run.
func (s *Service) FileURL(ctx context.Context, tenant uuid.UUID, file string, ttl time.Duration) (map[string]any, error) {
	id, err := uuid.Parse(file)
	if err != nil {
		return nil, fmt.Errorf("%w: no such file", ErrNoRequest)
	}
	var path, name, ctype string
	var size int64
	if err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT storage_key, name, type, size FROM form_files WHERE id = $1`, id).Scan(&path, &name, &ctype, &size)
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: no such file", ErrNoRequest)
		}
		return nil, err
	}
	if s.Files == nil {
		return nil, errors.New("forms: the organisation's storage is not set up")
	}
	if ttl <= 0 || ttl > time.Hour {
		ttl = 15 * time.Minute
	}
	url, exp, err := s.Files.SignURL(ctx, tenant, path, ttl)
	if err != nil {
		return nil, err
	}
	return map[string]any{"url": url, "expires_at": exp.UTC().Format(time.RFC3339), "name": name, "type": ctype, "size": size}, nil
}

// MaskPhone leaves a phone's last four digits.
func MaskPhone(p string) string {
	if len(p) <= 4 {
		return "•••"
	}
	return "•••" + p[len(p)-4:]
}

// MaskEmail leaves an email's first letter and domain.
func MaskEmail(e string) string {
	at := strings.IndexByte(e, '@')
	if at <= 0 {
		return "•••"
	}
	return e[:1] + "•••" + e[at:]
}
