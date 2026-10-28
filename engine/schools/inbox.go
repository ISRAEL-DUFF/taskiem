package schools

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The family page's inbox (docs/public-host.md): the public role, which
// holds no rail keys and opens no sealed data, leaves what a parent sent
// on the page (taskiem_public_school_family_ask) sealed under the public
// host's inbox key; the api role takes it here within seconds and records
// it under the family's own sealing: a claim, a plan request, or a new
// link sent to the guardians' numbers on file.

// FamilyAsk is what the page's forms send, sealed in the inbox.
type FamilyAsk struct {
	Kind string `json:"kind"` // claim, plan or link
	// A claim.
	StudentID  uuid.UUID `json:"student_id,omitempty"`
	AmountKobo int64     `json:"amount_kobo,omitempty"`
	PaidOn     string    `json:"paid_on,omitempty"`
	How        string    `json:"how,omitempty"`
	Reference  string    `json:"reference,omitempty"`
	Note       string    `json:"note,omitempty"`
	// A plan request.
	Instalments []Instalment `json:"instalments,omitempty"`
	Message     string       `json:"message,omitempty"`
}

// FamilyAskAAD binds a sealed ask to its idempotency key.
func FamilyAskAAD(idem []byte) []byte { return append([]byte("school family ask "), idem...) }

// SealFamilyAsk seals an ask for the inbox (the public role).
func SealFamilyAsk(key, idem []byte, a FamilyAsk) ([]byte, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	g, err := askGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, raw, FamilyAskAAD(idem)), nil
}

// OpenFamilyAsk opens what SealFamilyAsk wrote (the api role).
func OpenFamilyAsk(key, idem, ct []byte) (FamilyAsk, error) {
	var a FamilyAsk
	g, err := askGCM(key)
	if err != nil {
		return a, err
	}
	if len(ct) < g.NonceSize() {
		return a, errors.New("schools: a short inbox payload")
	}
	raw, err := g.Open(nil, ct[:g.NonceSize()], ct[g.NonceSize():], FamilyAskAAD(idem))
	if err != nil {
		return a, fmt.Errorf("schools: an inbox payload that does not open: %w", err)
	}
	return a, json.Unmarshal(raw, &a)
}

func askGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("schools: the inbox key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// DrainFamilyInbox takes what families sent from the inbox and records
// each; one that fails stays and is tried again once its claim lapses. It
// needs InboxKey (the public host's inbox key); without one it does
// nothing. Run it every second or two in the api role.
func (s *Schools) DrainFamilyInbox(ctx context.Context) error {
	if len(s.InboxKey) != 32 {
		return nil
	}
	type row struct {
		id, tenant, family, link uuid.UUID
		kind                     string
		payload                  []byte
		created                  time.Time
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, family_id, link_id, kind, payload, created_at FROM taskiem_school_family_inbox_claim(20)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.id, &x.tenant, &x.family, &x.link, &x.kind, &x.payload, &x.created)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, x := range list {
		if err := s.processAsk(ctx, x.id, x.tenant, x.family, x.kind, x.payload); err != nil {
			errs = append(errs, fmt.Errorf("family inbox %s: %w", x.id, err))
			continue
		}
		if _, err := s.Pool.Exec(ctx, `SELECT taskiem_school_family_inbox_done($1)`, x.id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// processAsk records one ask; an unreadable payload is dropped with a
// log line, since trying again cannot open it.
func (s *Schools) processAsk(ctx context.Context, id, tenant, family uuid.UUID, kind string, payload []byte) error {
	// The payload is the idem (32 bytes) then the sealed ask, so the api
	// role needs nothing the inbox table itself holds.
	if len(payload) < 33 {
		s.log().Error("schools: a family inbox payload too short; dropped", "id", id)
		return nil
	}
	ask, err := OpenFamilyAsk(s.InboxKey, payload[:32], payload[32:])
	if err != nil || ask.Kind != kind {
		s.log().Error("schools: a family inbox payload that does not open; dropped", "id", id, "err", err)
		return nil
	}
	return s.In(ctx, tenant, func(tx pgx.Tx) error {
		switch kind {
		case "claim":
			_, err := s.RecordClaim(ctx, tx, tenant, family, ClaimIn{StudentID: ask.StudentID, AmountKobo: ask.AmountKobo, PaidOn: ask.PaidOn, How: ask.How, Reference: ask.Reference, Note: ask.Note}, "family:"+family.String())
			return dropInvalid(s, id, err)
		case "plan":
			_, err := s.RequestPlan(ctx, tx, tenant, family, PlanIn{StudentID: ask.StudentID, Instalments: ask.Instalments, Message: ask.Message})
			return dropInvalid(s, id, err)
		case "link":
			var business string
			if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
				return err
			}
			days, err := FamilyLinkDays(ctx, tx, tenant)
			if err != nil {
				return err
			}
			_, err = s.SendFamily(ctx, tx, tenant, family, FamilyMessage{Purpose: "link", Key: "link:" + id.String(), Text: func(link string) string {
				return business + ": here is a new link to your family's fees page. " + link
			}, Template: &whatsapp.TplFamilyLink, Vars: func(link string) map[string]string {
				return FamilyLinkVars("Here is a new link to your family's fees page.", days, link)
			}}, "family:"+family.String())
			return dropInvalid(s, id, err)
		}
		return nil
	})
}

// dropInvalid: an ask the record refuses (a student no longer the
// family's, a second plan request, dates out of range) is dropped, not
// retried; anything else is tried again.
func dropInvalid(s *Schools, id uuid.UUID, err error) error {
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrState) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrOff) {
		s.log().Warn("schools: a family's ask was not recorded", "id", id, "why", Plain(err))
		return nil
	}
	return err
}
