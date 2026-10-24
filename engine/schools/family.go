package schools

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The family page's links (docs/spec/schools.md 10, B6;
// docs/schools.md#the-family-page). A family reaches its page from a link
// in any message the school sends: /s/<token> on the form host. Each
// message mints a new token (24 random bytes); the database keeps its
// hash and when it expires (the school's family_link_days, 3 by default,
// 1 to 30). The page is served by the public role, which can open none of
// the record's sealed data, so the link row also carries a view, sealed
// under a key derived from the token itself: the children's names, the
// account names, and the links of their invoice pages and receipts. The
// database alone shows nothing of it; whoever holds the link reads it,
// which is whoever the page is for.

// FamilyLinkDays is the school's link life in days.
func FamilyLinkDays(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT family_link_days FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultFamilyLinkDays, nil
	}
	return n, err
}

// DefaultFamilyLinkDays is how long a family page link works unless the
// school sets otherwise.
const DefaultFamilyLinkDays = 3

// SetFamilyLinkDays sets it (1 to 30); it reports the setting before.
func SetFamilyLinkDays(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, days int, by string) (int, error) {
	if days < 1 || days > 30 {
		return 0, invalid("a family page link works for 1 to 30 days")
	}
	if err := need(ctx, tx, tenant); err != nil {
		return 0, err
	}
	was, err := FamilyLinkDays(ctx, tx, tenant)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE school_settings SET family_link_days = $2, updated_by = $3, updated_at = now() WHERE tenant_id = $1`, tenant, days, by)
	return was, err
}

// FamilyView is what a link's sealed view carries for the page: what the
// public role may not open from the record.
type FamilyView struct {
	// Names are the students' names by id.
	Names map[string]string `json:"names"`
	// AccountNames are the names of the students' own accounts by student
	// id: what a parent should expect to see when they transfer.
	AccountNames map[string]string `json:"account_names,omitempty"`
	// InvoiceLinks are the invoice pages (/i/<token>) by invoice id, where
	// a child's open invoice can be paid into a one-time account.
	InvoiceLinks map[string]string `json:"invoice_links,omitempty"`
	// ReceiptLinks are the receipts' verification pages (/r/<token>) by
	// receipt id.
	ReceiptLinks map[string]string `json:"receipt_links,omitempty"`
}

// FamilyLinkMinted is a link just minted.
type FamilyLinkMinted struct {
	ID        uuid.UUID
	URL       string
	Token     string
	ExpiresAt time.Time
}

// FamilyTokenHash is what finds a link's row from its token.
func FamilyTokenHash(token string) []byte {
	h := sha256.Sum256([]byte("taskiem family page " + token))
	return h[:]
}

// familyViewKey is the key a link's view is sealed under: derived from the
// token, never stored.
func familyViewKey(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	return hkdf.Key(sha256.New, raw, nil, "taskiem family page view", 32)
}

// SealFamilyView seals a view under the token's key, bound to the link.
func SealFamilyView(token string, link uuid.UUID, v FamilyView) ([]byte, error) {
	key, err := familyViewKey(token)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, raw, []byte(link.String())), nil
}

// OpenFamilyView opens what SealFamilyView wrote, with the token.
func OpenFamilyView(token string, link uuid.UUID, ct []byte) (FamilyView, error) {
	var v FamilyView
	key, err := familyViewKey(token)
	if err != nil {
		return v, err
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return v, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return v, err
	}
	if len(ct) < g.NonceSize() {
		return v, errors.New("schools: a short family view")
	}
	raw, err := g.Open(nil, ct[:g.NonceSize()], ct[g.NonceSize():], []byte(link.String()))
	if err != nil {
		return v, fmt.Errorf("schools: a family view that does not open: %w", err)
	}
	return v, json.Unmarshal(raw, &v)
}

// FamilyPageBase is where family pages are served: the host that serves
// invoice pages ("" when none).
func (s *Schools) FamilyPageBase() string {
	if s.Invoices == nil {
		return ""
	}
	return strings.TrimRight(s.Invoices.PageBase, "/")
}

// MintFamilyLink mints a new link to a family's page, valid for the
// school's family_link_days, with its sealed view. purpose says what the
// link went out with (message, invoice, receipt, link, decision).
func (s *Schools) MintFamilyLink(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID, purpose, by string) (FamilyLinkMinted, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return FamilyLinkMinted{}, err
	}
	base := s.FamilyPageBase()
	if base == "" {
		return FamilyLinkMinted{}, refused("family pages are not served on this server (no public form host)")
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_families WHERE tenant_id = $1 AND id = $2)`, tenant, family).Scan(&exists); err != nil {
		return FamilyLinkMinted{}, err
	}
	if !exists {
		return FamilyLinkMinted{}, ErrNotFound
	}
	days, err := FamilyLinkDays(ctx, tx, tenant)
	if err != nil {
		return FamilyLinkMinted{}, err
	}
	view, err := s.familyView(ctx, tx, tenant, family)
	if err != nil {
		return FamilyLinkMinted{}, err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return FamilyLinkMinted{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	id := newID()
	sealed, err := SealFamilyView(token, id, view)
	if err != nil {
		return FamilyLinkMinted{}, err
	}
	if purpose == "" {
		purpose = "message"
	}
	expires := s.now().Add(time.Duration(days) * 24 * time.Hour)
	if _, err := tx.Exec(ctx, `INSERT INTO school_family_links (id, tenant_id, family_id, token_hash, view, purpose, expires_at, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenant, family, FamilyTokenHash(token), sealed, purpose, expires, by); err != nil {
		return FamilyLinkMinted{}, err
	}
	return FamilyLinkMinted{ID: id, URL: base + "/s/" + token, Token: token, ExpiresAt: expires}, nil
}

// FamilyLink mints a link for a message or a template: the page's URL.
func (s *Schools) FamilyLink(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID) (string, error) {
	l, err := s.MintFamilyLink(ctx, tx, tenant, family, "message", "system:family_link")
	return l.URL, err
}

// FamilyLinkForStudent mints a family page link for a student's family;
// "" when the student has no family or pages are not served.
func (s *Schools) FamilyLinkForStudent(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID, purpose string) (string, error) {
	var family *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT family_id FROM school_students WHERE tenant_id = $1 AND id = $2`, tenant, student).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && family == nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if s.FamilyPageBase() == "" {
		return "", nil
	}
	l, err := s.MintFamilyLink(ctx, tx, tenant, *family, purpose, "system:family_link")
	if errors.Is(err, ErrOff) {
		return "", nil
	}
	return l.URL, err
}

// FamilyLinkForInvoice mints a family page link for the family of the
// student a term invoice is for; "" for an invoice that is not a
// school's, so invoices of other businesses carry their own link.
func (s *Schools) FamilyLinkForInvoice(ctx context.Context, tx pgx.Tx, tenant, invoice uuid.UUID, purpose string) (string, error) {
	var student uuid.UUID
	err := tx.QueryRow(ctx, `SELECT student_id FROM school_term_invoices WHERE tenant_id = $1 AND invoice_id = $2`, tenant, invoice).Scan(&student)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s.FamilyLinkForStudent(ctx, tx, tenant, student, purpose)
}

// familyView gathers what the public role cannot read: names, account
// names, invoice page links and receipt links of the family's students.
func (s *Schools) familyView(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID) (FamilyView, error) {
	v := FamilyView{Names: map[string]string{}, AccountNames: map[string]string{}, InvoiceLinks: map[string]string{}, ReceiptLinks: map[string]string{}}
	students, err := s.listStudents(ctx, tx, tenant, `s.family_id = $2`, &family)
	if err != nil {
		return v, err
	}
	for _, st := range students {
		v.Names[st.ID.String()] = st.Name
		accounts, err := s.StudentAccounts(ctx, tx, tenant, st.ID)
		if err != nil {
			return v, err
		}
		for _, a := range accounts {
			if a.Status == "issued" && a.AccountName != "" && a.AccountName != Erased {
				v.AccountNames[st.ID.String()] = a.AccountName
				break
			}
		}
		if s.Invoices == nil {
			continue
		}
		// Open invoices' pages, where a one-time account can be asked for.
		rows, err := tx.Query(ctx, `SELECT i.id FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
			WHERE ti.tenant_id = $1 AND ti.student_id = $2 AND i.status IN ('sent', 'part_paid', 'overdue')`, tenant, st.ID)
		if err != nil {
			return v, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return v, err
		}
		for _, id := range ids {
			x, err := s.Invoices.Get(ctx, tx, tenant, id.String())
			if err != nil {
				return v, err
			}
			link, err := s.Invoices.PageLink(ctx, tx, tenant, x)
			if err != nil {
				return v, err
			}
			if link != "" {
				v.InvoiceLinks[id.String()] = link
			}
		}
		receipts, err := s.Receipts(ctx, tx, tenant, ReceiptFilter{Student: &st.ID, Limit: 200})
		if err != nil {
			return v, err
		}
		for _, r := range receipts {
			if r.Link != "" {
				v.ReceiptLinks[r.ID.String()] = r.Link
			}
		}
	}
	return v, nil
}

// --- messages to a family ---

// OutboxFamily is the chat outbox kind of a message to a family's
// guardian on WhatsApp or Telegram (api).
const OutboxFamily = "school_family"

// FamilyMessage is a message to send a family: Text is given the fresh
// link and returns the message; Purpose names why (invoice, receipt,
// reminder, decision, link); Key makes a retry send nothing twice.
type FamilyMessage struct {
	Purpose string
	Key     string
	Text    func(link string) string
	// Short, when set, is the text an SMS gets (at most three parts, and
	// ₦ makes every character count double); Text otherwise.
	Short func(link string) string
}

// FamilySent is what SendFamily did.
type FamilySent struct {
	Link FamilyLinkMinted `json:"-"`
	// Channels counts the guardians reached by channel (whatsapp,
	// telegram, sms); Nobody is true when no guardian could be reached.
	Channels  map[string]int `json:"channels"`
	ExpiresAt time.Time      `json:"expires_at"`
	Nobody    bool           `json:"nobody,omitempty"`
}

// SendFamily sends a family a message with a fresh link, in tx: on
// WhatsApp or Telegram to each guardian who tapped in for the business's
// updates (decision 0039), otherwise by SMS to their phone. The chat
// message is queued for the api role's outbox with the text sealed under
// the guardian's subject; a guardian whose chat message cannot go gets it
// by SMS there instead.
func (s *Schools) SendFamily(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID, m FamilyMessage, by string) (FamilySent, error) {
	out := FamilySent{Channels: map[string]int{}}
	if m.Text == nil || m.Key == "" {
		return out, errors.New("schools: a family message needs text and a key")
	}
	link, err := s.MintFamilyLink(ctx, tx, tenant, family, m.Purpose, by)
	if err != nil {
		return out, err
	}
	out.Link, out.ExpiresAt = link, link.ExpiresAt
	text, short := m.Text(link.URL), ""
	if m.Short != nil {
		short = m.Short(link.URL)
	}
	type guardian struct {
		id    uuid.UUID
		env   map[string]any
		phone string
	}
	rows, err := tx.Query(ctx, `SELECT id, details FROM school_guardians WHERE tenant_id = $1 AND family_id = $2 ORDER BY created_at, id`, tenant, family)
	if err != nil {
		return out, err
	}
	gs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (guardian, error) {
		var g guardian
		return g, r.Scan(&g.id, &g.env)
	})
	if err != nil {
		return out, err
	}
	var business string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
		return out, err
	}
	for _, g := range gs {
		var d guardianSealed
		erased, err := s.open(ctx, tx, tenant, g.env, &d)
		if err != nil {
			return out, err
		}
		if erased || d.Phone == "" {
			continue
		}
		subject, _ := g.env["subject"].(string)
		channel, err := s.sendGuardian(ctx, tx, tenant, family, link.ID, g.id, subject, d.Phone, business, text, short, m, by)
		if err != nil {
			return out, err
		}
		if channel != "" {
			out.Channels[channel]++
		}
	}
	out.Nobody = len(out.Channels) == 0
	return out, nil
}

// sendGuardian sends one guardian the message: the channel it went on.
func (s *Schools) sendGuardian(ctx context.Context, tx pgx.Tx, tenant, family, link, guardian uuid.UUID, subject, phone, business, text, short string, m FamilyMessage, by string) (string, error) {
	key := "family:" + m.Key + ":" + guardian.String()
	if short == "" {
		short = text
	}
	record := func(channel string) error {
		sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "other", map[string]string{"text": text, "short": short})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO school_family_messages (id, tenant_id, family_id, link_id, guardian_id, channel, purpose, text, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenant.String()+":"+key)), tenant, family, link, guardian, channel, m.Purpose, sealed, by)
		if code, _ := pgCode(err); code == "23505" {
			return errAlreadySent
		}
		return err
	}
	if s.Invoices != nil && s.Invoices.Reach.Ready() && len(s.Invoices.Chat) > 0 {
		t, ok, err := s.Invoices.Reach.FindTx(ctx, tx, tenant, uuid.Nil, phone, s.Invoices.Chat)
		if err != nil {
			return "", err
		}
		if ok && t.Contact != uuid.Nil {
			if s.Invoices.Messages != nil {
				if ok, err := s.Invoices.Messages.TakeRecipient(ctx, tx, "chat", phone); err != nil {
					return "", err
				} else if !ok {
					return "", nil
				}
			}
			switch err := record(t.Channel); {
			case errors.Is(err, errAlreadySent):
				return t.Channel, nil
			case err != nil:
				return "", err
			}
			id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenant.String()+":"+key))
			contact, step := t.Contact, OutboxFamily
			if _, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: t.Channel, Kind: OutboxFamily, Contact: &contact, Run: &id, Step: &step}, key); err != nil {
				return "", err
			}
			return t.Channel, nil
		}
	}
	if s.Invoices == nil || s.Invoices.Messages == nil {
		return "", nil
	}
	switch err := record("sms"); {
	case errors.Is(err, errAlreadySent):
		return "sms", nil
	case err != nil:
		return "", err
	}
	_, err := s.Invoices.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: phone, SenderName: business, Text: short, Key: key})
	switch {
	case errors.Is(err, sender.ErrAllowance), errors.Is(err, sender.ErrRecipientLimit), errors.Is(err, sender.ErrInvalid):
		// Over the allowance or the person's limit: the link is minted all
		// the same; the school sees nobody was reached.
		s.log().Warn("schools: a family's SMS was not queued", "family", family, "purpose", m.Purpose, "why", err)
		return "", nil
	case err != nil:
		return "", err
	}
	return "sms", nil
}

var errAlreadySent = errors.New("schools: already sent")

// FamilyMessageText is a queued chat message's text (and its short form
// for an SMS), opened for the outbox, with the guardian's phone for the
// SMS fallback; ok is false when the guardian is gone or erased.
func (s *Schools) FamilyMessageText(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (text, short, phone string, ok bool, err error) {
	var env, details map[string]any
	err = tx.QueryRow(ctx, `SELECT m.text, g.details FROM school_family_messages m LEFT JOIN school_guardians g ON g.id = m.guardian_id WHERE m.tenant_id = $1 AND m.id = $2`, tenant, id).Scan(&env, &details)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	v, err := s.Vault.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	if m, ok := v.(map[string]any); ok {
		text, _ = m["text"].(string)
		short, _ = m["short"].(string)
	}
	if short == "" {
		short = text
	}
	if details != nil {
		var d guardianSealed
		if _, err := s.open(ctx, tx, tenant, details, &d); err != nil {
			return "", "", "", false, err
		}
		phone = d.Phone
	}
	return text, short, phone, text != "", nil
}
