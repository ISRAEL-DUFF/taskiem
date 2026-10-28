// Package riders is a business's own riders (docs/spec/dispatch.md D1,
// decision 0053): the people who deliver its orders, outside the business
// as its customers are (decision 0039). A rider has a name, a phone, the
// delivery areas they cover and a fee per delivery; they are reached on
// the chat app they tapped in on (their contact in the reach book, the
// same phone hash) and by SMS until then. A rider is retired rather than
// deleted, so their deliveries keep them.
//
// A rider several businesses added is a row in each, under each one's own
// security. What the platform knows of them as a person is their presence
// (free, busy or off), from their own words to the platform number: kept
// by the hash of their phone and answered to each business only as a
// state, never naming another business (taskiem_rider_presence).
package riders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

var (
	// ErrInvalid refuses what cannot be recorded, in plain words.
	ErrInvalid = errors.New("riders: invalid")
	// ErrNoRider: no such rider in the business.
	ErrNoRider = errors.New("riders: no such rider")
	// ErrExists: the phone is already one of the business's riders.
	ErrExists = errors.New("riders: already a rider")
	// ErrTooSoon: the tap-in link was sent a moment ago.
	ErrTooSoon = errors.New("riders: the link was sent a moment ago")
)

// InviteEvery is how often the tap-in link may be sent to a rider again.
const InviteEvery = 10 * time.Minute

// MaxName is the most a rider's name may be.
const MaxName = 60

// Messenger queues an SMS (sender.Queue).
type Messenger interface {
	Enqueue(ctx context.Context, tx pgx.Tx, m sender.Message) (bool, error)
}

// Riders is the businesses' riders.
type Riders struct {
	Pool  *pgxpool.Pool
	Vault reach.Cipher
	// Book keys the phone hashes (decision 0039's) and tells which riders
	// tapped in on a chat app.
	Book *reach.Book
	// Messages sends the tap-in link by SMS; nil sends none.
	Messages Messenger
	// PageBase is the form host's address ("https://forms.example"), where
	// a business's tap-in page is; "" sends no link.
	PageBase string
	Now      func() time.Time
}

func (r *Riders) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Ready reports whether riders can be kept here.
func (r *Riders) Ready() bool { return r != nil && r.Pool != nil && r.Vault != nil && r.Book.Ready() }

// Rider is a rider as their business sees them.
type Rider struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Phone   string    `json:"phone"`
	Zones   []string  `json:"zones"`
	FeeKobo int64     `json:"fee_kobo"`
	// Active riders are offered deliveries; one not active is kept but
	// passed over (on leave).
	Active bool `json:"active"`
	// Reached is where the business reaches them: the chat app they
	// tapped in on (whatsapp or telegram), or sms until they tap in.
	Reached  string   `json:"reached"`
	Presence Presence `json:"presence"`
	// NotAnswering: three offers unanswered today, passed over until they
	// tap anything (this business only).
	NotAnswering bool `json:"not_answering,omitempty"`
	// Bank is the account their payouts go to, as it may be shown ("Wema
	// Bank, ending 4567, MUSA BELLO"); "" until set.
	Bank string `json:"bank,omitempty"`
	// InvitedAt is when the tap-in link was last sent.
	InvitedAt *time.Time `json:"invited_at"`
	CreatedAt time.Time  `json:"created_at"`
	RetiredAt *time.Time `json:"retired_at"`
	phoneHash []byte
}

// Presence is what a business may know of a rider as a person: free,
// busy (until a time) or off, from their own words to the platform number
// whichever business they ride for.
type Presence struct {
	State string     `json:"state"` // free, busy or off
	Until *time.Time `json:"until,omitempty"`
	Since *time.Time `json:"since,omitempty"`
	// Open is how many deliveries they hold now, for any business: busy
	// while they hold one (dispatch D2).
	Open int `json:"open,omitempty"`
}

// Line is the presence in a few words: "free", "busy till 15:00", "off".
func (p Presence) Line() string {
	switch p.State {
	case "busy":
		if p.Until != nil {
			return "busy till " + Clock(*p.Until)
		}
		if p.Open == 1 {
			return "busy, 1 delivery"
		}
		if p.Open > 1 {
			return fmt.Sprintf("busy, %d deliveries", p.Open)
		}
		return "busy"
	case "off":
		return "off"
	}
	return "free"
}

// Clock is a time as riders and owners read it: 15:04, Lagos time.
func Clock(t time.Time) string { return t.In(lagos).Format("15:04") }

var lagos = func() *time.Location {
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return l
}()

// In is a rider to add or change. A nil field is left as it is.
type In struct {
	Name    *string   `json:"name"`
	Phone   *string   `json:"phone"`
	Zones   *[]string `json:"zones"`
	FeeKobo *int64    `json:"fee_kobo"`
	Active  *bool     `json:"active"`
}

const riderCols = `r.id, r.phone_hash, r.phone, r.name, r.zones, r.fee_kobo, r.active, r.invited_at, r.created_at, r.retired_at,
	r.not_answering_since IS NOT NULL, COALESCE(r.bank_shown, ''),
	COALESCE((SELECT ch.channel FROM reach_contacts c JOIN reach_channels ch ON ch.contact_id = c.id
	  WHERE c.tenant_id = r.tenant_id AND c.phone_hash = r.phone_hash AND ch.stopped_at IS NULL
	  ORDER BY ch.linked_at DESC LIMIT 1), 'sms')`

// scan reads riders and opens their names and phones; an erased rider
// reads as erased.
func (r *Riders) scan(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]Rider, error) {
	type row struct {
		x           Rider
		phone, name []byte
	}
	list, err := pgx.CollectRows(rows, func(c pgx.CollectableRow) (row, error) {
		var v row
		err := c.Scan(&v.x.ID, &v.x.phoneHash, &v.phone, &v.name, &v.x.Zones, &v.x.FeeKobo, &v.x.Active, &v.x.InvitedAt, &v.x.CreatedAt,
			&v.x.RetiredAt, &v.x.NotAnswering, &v.x.Bank, &v.x.Reached)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Rider, 0, len(list))
	hashes := make([][]byte, 0, len(list))
	for _, v := range list {
		x := v.x
		x.Phone, x.Name = pii.Erased, pii.Erased
		for _, f := range []struct {
			raw []byte
			to  *string
		}{{v.phone, &x.Phone}, {v.name, &x.Name}} {
			var env map[string]any
			if err := json.Unmarshal(f.raw, &env); err != nil {
				return nil, err
			}
			got, err := r.Vault.OpenTx(ctx, tx, tenant, env)
			if errors.Is(err, pii.ErrErased) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if s, ok := got.(string); ok {
				*f.to = s
			}
		}
		if x.Zones == nil {
			x.Zones = []string{}
		}
		x.Presence = Presence{State: "free"}
		out = append(out, x)
		hashes = append(hashes, x.phoneHash)
	}
	if len(hashes) == 0 {
		return out, nil
	}
	// What they said, for the riders of the business in scope only.
	prows, err := tx.Query(ctx, `SELECT phone_hash, said, until, said_at, open, open_since FROM taskiem_rider_presence($1)`, hashes)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	said := map[string]Presence{}
	for prows.Next() {
		var h []byte
		var state *string
		var p Presence
		var at, since *time.Time
		if err := prows.Scan(&h, &state, &p.Until, &at, &p.Open, &since); err != nil {
			return nil, err
		}
		switch {
		case state != nil && *state == "off":
			p.State, p.Since = "off", at
		case p.Open > 0:
			// Holding a delivery is busy, whatever they said.
			p.State, p.Since, p.Until = "busy", since, nil
		case state != nil:
			p.State, p.Since = *state, at
		default:
			p.State = "free"
		}
		said[string(h)] = p
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if p, ok := said[string(out[i].phoneHash)]; ok {
			out[i].Presence = p
		}
	}
	return out, nil
}

// Filter narrows a list: retired riders instead of current ones, those
// covering a delivery area, active ones only.
type Filter struct {
	Retired    bool
	Zone       string
	ActiveOnly bool
}

// ListTx lists the business's riders in tx, by name.
func (r *Riders) ListTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f Filter) ([]Rider, error) {
	q := `SELECT ` + riderCols + ` FROM riders r WHERE r.tenant_id = $1 AND (r.retired_at IS NOT NULL) = $2`
	args := []any{tenant, f.Retired}
	if f.ActiveOnly {
		q += ` AND r.active`
	}
	if f.Zone != "" {
		q += ` AND EXISTS (SELECT 1 FROM unnest(r.zones) z WHERE lower(z) = lower($3))`
		args = append(args, f.Zone)
	}
	rows, err := tx.Query(ctx, q+` ORDER BY r.created_at LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	list, err := r.scan(ctx, tx, tenant, rows)
	if err != nil {
		return nil, err
	}
	sortByName(list)
	return list, nil
}

func sortByName(list []Rider) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && strings.ToLower(list[j].Name) < strings.ToLower(list[j-1].Name); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// GetTx reads one rider.
func (r *Riders) GetTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Rider, error) {
	rows, err := tx.Query(ctx, `SELECT `+riderCols+` FROM riders r WHERE r.tenant_id = $1 AND r.id = $2`, tenant, id)
	if err != nil {
		return Rider{}, err
	}
	list, err := r.scan(ctx, tx, tenant, rows)
	if err != nil {
		return Rider{}, err
	}
	if len(list) == 0 {
		return Rider{}, ErrNoRider
	}
	return list[0], nil
}

// cleanName is a rider's name, tidied; "" when it cannot be one.
func cleanName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || len([]rune(s)) > MaxName || strings.ContainsAny(s, "<>{}") {
		return ""
	}
	return s
}

// CleanZones checks a rider's delivery areas against the business's:
// with areas set on the order form, each must be one of them (spelt as
// there, whatever the case); without, they are short labels.
func CleanZones(in []string, known []storefront.Zone) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, z := range in {
		z = strings.Join(strings.Fields(z), " ")
		if z == "" {
			continue
		}
		if len(known) > 0 {
			found := ""
			for _, k := range known {
				if strings.EqualFold(k.Name, z) {
					found = k.Name
				}
			}
			if found == "" {
				return nil, fmt.Errorf("%w: %s is not one of your delivery areas (%s)", ErrInvalid, z, zoneNames(known))
			}
			z = found
		} else if len([]rune(z)) > storefront.MaxZoneName || strings.ContainsAny(z, ",<>{}") {
			return nil, fmt.Errorf("%w: an area is up to %d characters", ErrInvalid, storefront.MaxZoneName)
		}
		if !seen[strings.ToLower(z)] {
			seen[strings.ToLower(z)] = true
			out = append(out, z)
		}
	}
	if len(out) > storefront.MaxZones {
		return nil, fmt.Errorf("%w: up to %d areas", ErrInvalid, storefront.MaxZones)
	}
	return out, nil
}

func zoneNames(zs []storefront.Zone) string {
	names := make([]string, len(zs))
	for i, z := range zs {
		names[i] = z.Name
	}
	return strings.Join(names, ", ")
}

// SplitZones reads the areas typed after a rider's phone in chat ("Lekki
// Phase 1, Yaba" or "Lekki Ajah"): parts between commas; without commas,
// the business's areas found in the words, longest first, and any other
// word on its own.
func SplitZones(text string, known []storefront.Zone) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if strings.Contains(text, ",") {
		var out []string
		for _, p := range strings.Split(text, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	words := strings.Fields(text)
	var out []string
	for i := 0; i < len(words); {
		best := 0
		for _, k := range known {
			kw := strings.Fields(k.Name)
			if len(kw) > best && i+len(kw) <= len(words) && strings.EqualFold(strings.Join(words[i:i+len(kw)], " "), k.Name) {
				best = len(kw)
			}
		}
		if best == 0 {
			best = 1
		}
		out = append(out, strings.Join(words[i:i+best], " "))
		i += best
	}
	return out
}

// AddTx adds a rider by name and phone, with their areas and fee, and
// sends them the tap-in link. A retired rider added again comes back with
// what is given now; one already riding answers ErrExists with the rider.
func (r *Riders) AddTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in In, actor string) (Rider, error) {
	if in.Name == nil || in.Phone == nil {
		return Rider{}, fmt.Errorf("%w: a rider needs a name and a phone number", ErrInvalid)
	}
	name := cleanName(*in.Name)
	if name == "" {
		return Rider{}, fmt.Errorf("%w: a name is 1 to %d characters", ErrInvalid, MaxName)
	}
	phone, err := r.Book.Phone(*in.Phone)
	if err != nil {
		return Rider{}, fmt.Errorf("%w: %s is not a mobile number Taskiem can reach (like 0803 123 4567)", ErrInvalid, strings.TrimSpace(*in.Phone))
	}
	known, err := storefront.Zones(ctx, tx, tenant)
	if err != nil {
		return Rider{}, err
	}
	zones := []string{}
	if in.Zones != nil {
		if zones, err = CleanZones(*in.Zones, known); err != nil {
			return Rider{}, err
		}
	}
	var fee int64
	if in.FeeKobo != nil {
		fee = *in.FeeKobo
	}
	if fee < 0 || fee > storefront.MaxZoneFeeKobo {
		return Rider{}, fmt.Errorf("%w: a fee per delivery is from ₦0 to ₦100,000", ErrInvalid)
	}
	hash := r.Book.PhoneHash(phone)
	var id uuid.UUID
	var retired *time.Time
	err = tx.QueryRow(ctx, `SELECT id, retired_at FROM riders WHERE tenant_id = $1 AND phone_hash = $2 FOR UPDATE`, tenant, hash).Scan(&id, &retired)
	switch {
	case err == nil && retired == nil:
		x, err := r.GetTx(ctx, tx, tenant, id)
		if err != nil {
			return Rider{}, err
		}
		return x, fmt.Errorf("%w: %s (%s) already rides for you", ErrExists, x.Name, x.Phone)
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return Rider{}, err
	}
	envPhone, err := r.Vault.SealTx(ctx, tx, tenant, "phone", phone)
	if err != nil {
		return Rider{}, err
	}
	subject, _ := envPhone["subject"].(string)
	envName, err := r.Vault.SealUnderTx(ctx, tx, tenant, subject, "name", name)
	if err != nil {
		return Rider{}, err
	}
	action := "rider.added"
	if retired != nil {
		action = "rider.back"
		if _, err := tx.Exec(ctx, `UPDATE riders SET phone = $3, name = $4, zones = $5, fee_kobo = $6, active = true, retired_at = NULL, updated_at = now()
			WHERE tenant_id = $1 AND id = $2`, tenant, id, envPhone, envName, zones, fee); err != nil {
			return Rider{}, err
		}
	} else {
		id = uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO riders (id, tenant_id, phone_hash, phone, name, zones, fee_kobo, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, tenant, hash, envPhone, envName, zones, fee, actor); err != nil {
			return Rider{}, err
		}
	}
	if err := audit(ctx, tx, tenant, action, id, map[string]any{"by": actor, "zones": zones, "fee_kobo": fee}); err != nil {
		return Rider{}, err
	}
	x, err := r.GetTx(ctx, tx, tenant, id)
	if err != nil {
		return Rider{}, err
	}
	if x.Reached == "sms" {
		if _, err := r.inviteTx(ctx, tx, tenant, x); err != nil {
			return Rider{}, err
		}
		return r.GetTx(ctx, tx, tenant, id)
	}
	return x, nil
}

// UpdateTx changes a current rider's name, areas, fee or whether they are
// active. The phone is who the rider is: a new number is a new rider.
func (r *Riders) UpdateTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, in In, actor string) (Rider, error) {
	x, err := r.GetTx(ctx, tx, tenant, id)
	if err != nil {
		return Rider{}, err
	}
	if x.RetiredAt != nil {
		return Rider{}, fmt.Errorf("%w: %s is retired; add them again to bring them back", ErrInvalid, x.Name)
	}
	if in.Phone != nil {
		if p, err := r.Book.Phone(*in.Phone); err != nil || p != x.Phone {
			return Rider{}, fmt.Errorf("%w: a rider's phone number cannot change; retire them and add the new number", ErrInvalid)
		}
	}
	changed := map[string]any{"by": actor}
	if in.Name != nil {
		name := cleanName(*in.Name)
		if name == "" {
			return Rider{}, fmt.Errorf("%w: a name is 1 to %d characters", ErrInvalid, MaxName)
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT phone FROM riders WHERE id = $1`, id).Scan(&raw); err != nil {
			return Rider{}, err
		}
		var env map[string]any
		_ = json.Unmarshal(raw, &env)
		subject, _ := env["subject"].(string)
		sealed, err := r.Vault.SealUnderTx(ctx, tx, tenant, subject, "name", name)
		if err != nil {
			return Rider{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE riders SET name = $2, updated_at = now() WHERE id = $1`, id, sealed); err != nil {
			return Rider{}, err
		}
		changed["name"] = true
	}
	if in.Zones != nil {
		known, err := storefront.Zones(ctx, tx, tenant)
		if err != nil {
			return Rider{}, err
		}
		zones, err := CleanZones(*in.Zones, known)
		if err != nil {
			return Rider{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE riders SET zones = $2, updated_at = now() WHERE id = $1`, id, zones); err != nil {
			return Rider{}, err
		}
		changed["zones"] = zones
	}
	if in.FeeKobo != nil {
		if *in.FeeKobo < 0 || *in.FeeKobo > storefront.MaxZoneFeeKobo {
			return Rider{}, fmt.Errorf("%w: a fee per delivery is from ₦0 to ₦100,000", ErrInvalid)
		}
		if _, err := tx.Exec(ctx, `UPDATE riders SET fee_kobo = $2, updated_at = now() WHERE id = $1`, id, *in.FeeKobo); err != nil {
			return Rider{}, err
		}
		changed["fee_kobo"] = *in.FeeKobo
	}
	if in.Active != nil {
		if _, err := tx.Exec(ctx, `UPDATE riders SET active = $2, updated_at = now() WHERE id = $1`, id, *in.Active); err != nil {
			return Rider{}, err
		}
		changed["active"] = *in.Active
	}
	if err := audit(ctx, tx, tenant, "rider.changed", id, changed); err != nil {
		return Rider{}, err
	}
	return r.GetTx(ctx, tx, tenant, id)
}

// RetireTx retires a rider: no more offers, kept with their deliveries.
// A rider already retired is answered as they are.
func (r *Riders) RetireTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (Rider, error) {
	tag, err := tx.Exec(ctx, `UPDATE riders SET retired_at = now(), active = false, updated_at = now() WHERE tenant_id = $1 AND id = $2 AND retired_at IS NULL`, tenant, id)
	if err != nil {
		return Rider{}, err
	}
	if tag.RowsAffected() == 1 {
		if err := audit(ctx, tx, tenant, "rider.retired", id, map[string]any{"by": actor}); err != nil {
			return Rider{}, err
		}
	}
	return r.GetTx(ctx, tx, tenant, id)
}

// InviteTx sends a current rider the tap-in link again (at most once in
// InviteEvery).
func (r *Riders) InviteTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (Rider, error) {
	x, err := r.GetTx(ctx, tx, tenant, id)
	if err != nil {
		return Rider{}, err
	}
	if x.RetiredAt != nil {
		return Rider{}, fmt.Errorf("%w: %s is retired", ErrInvalid, x.Name)
	}
	if x.InvitedAt != nil && r.now().Sub(*x.InvitedAt) < InviteEvery {
		return x, fmt.Errorf("%w: wait a few minutes before sending it again", ErrTooSoon)
	}
	if _, err := r.inviteTx(ctx, tx, tenant, x); err != nil {
		return Rider{}, err
	}
	if err := audit(ctx, tx, tenant, "rider.invited", id, map[string]any{"by": actor}); err != nil {
		return Rider{}, err
	}
	return r.GetTx(ctx, tx, tenant, id)
}

// TapIn is the business's tap-in page: where its riders (and anyone it
// reaches) open WhatsApp or Telegram with one tap. "" when there is none.
func (r *Riders) TapIn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	if r.PageBase == "" {
		return "", nil
	}
	id, err := reach.PageTx(ctx, tx, tenant, false)
	if err != nil {
		return "", err
	}
	return r.PageBase + "/u/" + id, nil
}

// InviteText is the SMS a rider gets with the tap-in link.
func InviteText(business, link string) string {
	return "You were added as a rider for " + business + ". Tap to get their deliveries on WhatsApp: " + link
}

// inviteTx queues the tap-in link by SMS; false when it cannot be sent
// here (no messages, no form host, the SMS allowance used up).
func (r *Riders) inviteTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Rider) (bool, error) {
	if r.Messages == nil || x.Phone == pii.Erased {
		return false, nil
	}
	link, err := r.TapIn(ctx, tx, tenant)
	if err != nil || link == "" {
		return false, err
	}
	var business string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
		return false, err
	}
	now := r.now()
	_, err = r.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: x.Phone,
		SenderName: business, Text: InviteText(business, link), Key: fmt.Sprintf("rider-invite:%s:%d", x.ID, now.Unix()/int64(InviteEvery/time.Second))})
	if errors.Is(err, sender.ErrAllowance) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE riders SET invited_at = $2 WHERE id = $1`, x.ID, now)
	return err == nil, err
}

// --- presence ---

// Said is a rider's word to the platform number.
type Said struct {
	State string    // on, off or busy
	Until time.Time // busy's end
}

// Say records a rider's word for every business they ride for: the rider
// whose phone sent it (WhatsApp proves it; phone "" on Telegram, where
// the chat address's contact says who). n is how many businesses they
// ride for; 0 when they ride for none (nothing recorded).
func (r *Riders) Say(ctx context.Context, phone, channel, addr string, s Said) (int, error) {
	var phoneHash []byte
	if phone != "" {
		phoneHash = r.Book.PhoneHash(phone)
	}
	var until *time.Time
	if s.State == "busy" {
		until = &s.Until
	}
	var n int
	err := r.Pool.QueryRow(ctx, `SELECT taskiem_rider_say($1, $2, $3, $4, $5)`, phoneHash, channel, r.Book.AddressHash(channel, addr), s.State, until).Scan(&n)
	return n, err
}

// IsRiderTx reports whether a proven phone is a current rider of the
// business (a contact who just tapped in), in tx.
func (r *Riders) IsRiderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, phone string) (bool, error) {
	p, err := r.Book.Phone(phone)
	if err != nil {
		return false, nil //nolint:nilerr // not a phone: not a rider
	}
	var ok bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM riders WHERE tenant_id = $1 AND phone_hash = $2 AND retired_at IS NULL)`, tenant, r.Book.PhoneHash(p)).Scan(&ok)
	return ok, err
}

// ParseWord reads a rider's word: on, free, off, busy, or busy till a
// time ("busy till 3pm", "busy until 15:30"), the next such time in Lagos.
// Plain busy is busy for the rest of the day. ok is false for anything
// else.
func ParseWord(text string, now time.Time) (Said, bool) {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(text), "/ .!"))
	t = strings.Join(strings.Fields(t), " ")
	switch t {
	case "on", "free", "available":
		return Said{State: "on"}, true
	case "off":
		return Said{State: "off"}, true
	case "busy":
		l := now.In(lagos)
		return Said{State: "busy", Until: time.Date(l.Year(), l.Month(), l.Day(), 23, 59, 0, 0, lagos)}, true
	}
	for _, p := range []string{"busy till ", "busy until ", "busy to "} {
		if rest, ok := strings.CutPrefix(t, p); ok {
			at, ok := clock(rest, now)
			if !ok {
				return Said{}, false
			}
			return Said{State: "busy", Until: at}, true
		}
	}
	return Said{}, false
}

// clock reads "3pm", "3:30pm", "15:30" or "15" as the next such time
// after now, in Lagos.
func clock(s string, now time.Time) (time.Time, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	pm, am := strings.HasSuffix(s, "pm"), strings.HasSuffix(s, "am")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "pm"), "am")
	var h, m int
	hm := strings.SplitN(strings.ReplaceAll(s, ".", ":"), ":", 2)
	if _, err := fmt.Sscanf(hm[0], "%d", &h); err != nil || len(hm[0]) > 2 {
		return time.Time{}, false
	}
	if len(hm) == 2 {
		if _, err := fmt.Sscanf(hm[1], "%d", &m); err != nil || len(hm[1]) != 2 || m > 59 {
			return time.Time{}, false
		}
	}
	switch {
	case (pm || am) && (h < 1 || h > 12):
		return time.Time{}, false
	case pm && h < 12:
		h += 12
	case am && h == 12:
		h = 0
	case h > 23:
		return time.Time{}, false
	}
	l := now.In(lagos)
	at := time.Date(l.Year(), l.Month(), l.Day(), h, m, 0, 0, lagos)
	if !at.After(now) {
		at = at.Add(24 * time.Hour)
	}
	return at, true
}

// --- for workflows (riders@1) and chat ---

// List lists the business's current riders.
func (r *Riders) List(ctx context.Context, tenant uuid.UUID, f Filter) ([]Rider, error) {
	var out []Rider
	err := db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = r.ListTx(ctx, tx, tenant, f)
		return err
	})
	return out, err
}

// Get reads one rider.
func (r *Riders) Get(ctx context.Context, tenant, id uuid.UUID) (Rider, error) {
	var out Rider
	err := db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = r.GetTx(ctx, tx, tenant, id)
		return err
	})
	return out, err
}

// Add adds a rider (AddTx).
func (r *Riders) Add(ctx context.Context, tenant uuid.UUID, in In, actor string) (Rider, error) {
	var out Rider
	err := db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = r.AddTx(ctx, tx, tenant, in, actor)
		return err
	})
	return out, err
}

// Retire retires a rider (RetireTx).
func (r *Riders) Retire(ctx context.Context, tenant, id uuid.UUID, actor string) (Rider, error) {
	var out Rider
	err := db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = r.RetireTx(ctx, tx, tenant, id, actor)
		return err
	})
	return out, err
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action string, id uuid.UUID, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'riders', $2, $3, $4)`, tenant, action, id.String(), raw)
	return err
}
