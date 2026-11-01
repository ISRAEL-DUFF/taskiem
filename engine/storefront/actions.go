package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Kinds are the forms a storefront can offer (storefronts 6.3), and
// BuiltinKinds the four every storefront has on its first front; a hire
// (fronts F5) is only ever an action of a front of its kind.
var (
	Kinds        = []string{"order", "enquiry", "callback", "booking", "hire", "space", "tickets", "class"}
	BuiltinKinds = []string{"order", "enquiry", "callback", "booking"}
)

// ErrNoWorkflow refuses enabling a form no workflow is deployed for.
var ErrNoWorkflow = errors.New("storefront: no workflow is deployed for this form")

// Action is one form's settings.
type Action struct {
	// ID and Slug name the action (fronts F1); Builtin is one of the four
	// found by kind, the others copies made for a workflow (F2).
	ID      *uuid.UUID `json:"id,omitempty"`
	Slug    string     `json:"slug,omitempty"`
	Builtin bool       `json:"builtin"`
	// FrontID is the front the action is on (fronts F3).
	FrontID *uuid.UUID    `json:"front_id,omitempty"`
	Kind    string        `json:"kind"`
	Enabled bool          `json:"enabled"`
	Label   string        `json:"label"`
	Payment string        `json:"payment"` // after_accept, at_order or none
	Options ActionOptions `json:"options"`
	// Workflow is the workflow deployed in prod for this form, if any.
	Workflow *uuid.UUID `json:"workflow_id"`
}

// ActionOptions are a form's choices.
type ActionOptions struct {
	// Connection is the connection payments use: a Paystack connection's
	// name (default main), which takes card payments and transfers, or
	// another rail's as connector/name ("iswallet/taskiem"), which takes
	// bank transfers only (N3).
	Connection string `json:"connection,omitempty"`
	// Pickup and Delivery are what an order may choose; neither means
	// neither is asked.
	Pickup   bool `json:"pickup,omitempty"`
	Delivery bool `json:"delivery,omitempty"`
	// DeliveryNote is shown with delivery ("Yaba and Surulere only").
	DeliveryNote string `json:"delivery_note,omitempty"`
	// Zones are the areas an order is delivered to, each with its fee
	// (dispatch D1): the form asks for one with delivery and adds its fee
	// to the total, and riders cover them. None: the address is enough.
	Zones []Zone `json:"zones,omitempty"`
	// PayOnDelivery lets a customer choosing delivery pay at the door, by
	// a one-time account number when the rider arrives or in cash
	// (dispatch D4). Off by default.
	PayOnDelivery bool `json:"pay_on_delivery,omitempty"`
	// DepositPercent is how much of a service's price a booking pays
	// online (1 to 100; unset is the full price).
	DepositPercent int `json:"deposit_percent,omitempty"`
	// CancelRefund is what a customer cancelling a paid booking gets back:
	// full (the default) or keep_deposit.
	CancelRefund string `json:"cancel_refund,omitempty"`
	// LateFeeKobo is what a hire adds for each day it comes back late
	// (fronts F5); 0 for none.
	LateFeeKobo int64 `json:"late_fee_kobo,omitempty"`
	// MaxPerOrder is how many tickets or places one order may take
	// (fronts F6); 0 is 10.
	MaxPerOrder int `json:"max_per_order,omitempty"`
	// Instalments is how many parts a class's fee may be paid in (1 to 4;
	// 0 or 1 is in full), InstalmentDays the days between them (default
	// 30), and WaitingList whether a full class takes names (fronts F6).
	Instalments    int  `json:"instalments,omitempty"`
	InstalmentDays int  `json:"instalment_days,omitempty"`
	WaitingList    bool `json:"waiting_list,omitempty"`
}

// Zone is an area a business delivers to and what delivery there costs.
type Zone struct {
	Name    string `json:"name"`
	FeeKobo int64  `json:"fee_kobo"`
}

// Limits on delivery areas: how many, a name's length, and the most a
// delivery may cost (₦100,000).
const (
	MaxZones       = 30
	MaxZoneName    = 40
	MaxZoneFeeKobo = 10_000_000
)

// CleanZones checks a form's delivery areas: named, each once (whatever
// the case), with a fee from nothing to ₦100,000.
func CleanZones(in []Zone) ([]Zone, error) {
	if len(in) > MaxZones {
		return nil, fmt.Errorf("%w: up to %d delivery areas", ErrInvalid, MaxZones)
	}
	out := make([]Zone, 0, len(in))
	seen := map[string]bool{}
	for _, z := range in {
		z.Name = strings.Join(strings.Fields(z.Name), " ")
		if z.Name == "" || len([]rune(z.Name)) > MaxZoneName || hasLink(z.Name) || strings.ContainsAny(z.Name, ",") {
			return nil, fmt.Errorf("%w: an area's name is 1 to %d characters, with no commas or links", ErrInvalid, MaxZoneName)
		}
		if seen[strings.ToLower(z.Name)] {
			return nil, fmt.Errorf("%w: %s is listed twice", ErrInvalid, z.Name)
		}
		seen[strings.ToLower(z.Name)] = true
		if z.FeeKobo < 0 || z.FeeKobo > MaxZoneFeeKobo {
			return nil, fmt.Errorf("%w: a delivery fee is from ₦0 to ₦100,000", ErrInvalid)
		}
		out = append(out, z)
	}
	return out, nil
}

// Zones are the order form's delivery areas, in its order.
func Zones(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Zone, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT options->'zones' FROM storefront_actions WHERE tenant_id = $1 AND kind = 'order' AND builtin`, tenant).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || raw == nil {
		return []Zone{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Zone
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Zone{}
	}
	return out, nil
}

// formConnection is a form's payments connection: name or connector/name.
var formConnection = regexp.MustCompile(`^(?:[a-z][a-z0-9_-]{0,63}/)?[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

// takesPayment reports whether a form's kind may ask for payment.
func takesPayment(kind string) bool {
	return kind == "order" || kind == "booking" || kind == "hire" || kind == "space" || kind == "tickets" || kind == "class"
}

// Deposit is what a booking pays online of price.
func (o ActionOptions) Deposit(price int64) int64 {
	if o.DepositPercent <= 0 || o.DepositPercent >= 100 {
		return price
	}
	return (price*int64(o.DepositPercent) + 99) / 100
}

// PublicAction is what a published page shows of a form.
type PublicAction struct {
	// ID and Slug name the action (fronts F1); a snapshot's top-level
	// actions, kept for a public host on the old binary, have neither.
	ID   *uuid.UUID `json:"id,omitempty"`
	Slug string     `json:"slug,omitempty"`
	// Form is the action's form: its own, or its kind's (fronts §5.3, F1);
	// absent from the top-level actions.
	Form         json.RawMessage `json:"form,omitempty"`
	Kind         string          `json:"kind"`
	Label        string          `json:"label"`
	Payment      string          `json:"payment"`
	Pickup       bool            `json:"pickup,omitempty"`
	Delivery     bool            `json:"delivery,omitempty"`
	DeliveryNote string          `json:"delivery_note,omitempty"`
	// Zones are the delivery areas the customer chooses from, with fees.
	Zones []Zone `json:"zones,omitempty"`
	// PayOnDelivery offers paying at the door with delivery.
	PayOnDelivery bool `json:"pay_on_delivery,omitempty"`
	// DepositPercent is a booking's online part of the price (100: all).
	DepositPercent int `json:"deposit_percent,omitempty"`
	// A ticket or class form's (fronts F6): how many one order may take,
	// how many parts a fee may be paid in, and whether a full class takes
	// names for a waiting list.
	MaxPerOrder int  `json:"max_per_order,omitempty"`
	Instalments int  `json:"instalments,omitempty"`
	WaitingList bool `json:"waiting_list,omitempty"`
}

var defaultLabels = map[string]string{"order": "Order now", "enquiry": "Ask a question", "callback": "Request a call back", "booking": "Book", "hire": "Hire now",
	"space": "Book the space", "tickets": "Buy tickets", "class": "Enrol"}

// builtinPosition is a built-in action's place on the first front.
func builtinPosition(kind string) int {
	for i, k := range []string{"order", "booking", "enquiry", "callback"} {
		if k == kind {
			return i + 1
		}
	}
	return 0
}

// BuiltinTriggerSQL is the SQL condition on a triggers row t for a
// storefront trigger on the business's built-in action of a kind (the kind
// a parameter or a literal), read in its tenant's transaction: by the
// action's id, or by the kind for a trigger deployed before the storefront
// had that action (linked when the action is made, 00280).
func BuiltinTriggerSQL(t, kind string) string {
	return `(` + t + `.storefront_action_id = (SELECT b.id FROM storefront_actions b WHERE b.kind = ` + kind + ` AND b.builtin)
		OR (` + t + `.storefront_action_id IS NULL AND ` + t + `.storefront_action = ` + kind + `))`
}

// Actions reads the storefront's forms, every kind present (unset ones
// disabled with their default label), with the workflow deployed for each.
func Actions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Action, error) {
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	have := map[string]Action{}
	rows, err := tx.Query(ctx, `SELECT a.id, a.slug, a.builtin, a.front_id, a.kind, a.enabled, a.label, a.payment, a.options FROM storefront_actions a
		LEFT JOIN storefront_fronts f ON f.id = a.front_id WHERE a.storefront_id = $1
		ORDER BY NOT a.builtin, f.slug <> '', f.position, a.position, a.slug`, sf.ID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Action, error) {
		var a Action
		var id uuid.UUID
		var opts []byte
		err := r.Scan(&id, &a.Slug, &a.Builtin, &a.FrontID, &a.Kind, &a.Enabled, &a.Label, &a.Payment, &opts)
		a.ID = &id
		_ = json.Unmarshal(opts, &a.Options)
		return a, err
	})
	if err != nil {
		return nil, err
	}
	var others []Action
	for _, a := range list {
		if a.Builtin {
			have[a.Kind] = a
		} else {
			others = append(others, a)
		}
	}
	out := make([]Action, 0, len(BuiltinKinds))
	for _, k := range BuiltinKinds {
		a, ok := have[k]
		if !ok {
			a = Action{Kind: k, Slug: k, Builtin: true, Label: defaultLabels[k], Payment: "after_accept"}
			if !takesPayment(k) {
				a.Payment = "none"
			}
		}
		var wf uuid.UUID
		err := tx.QueryRow(ctx, `SELECT workflow_id FROM triggers t WHERE type = 'storefront' AND `+BuiltinTriggerSQL("t", "$1")+` AND environment = 'prod'`, k).Scan(&wf)
		if err == nil {
			a.Workflow = &wf
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out = append(out, a)
	}
	// The other actions, each with the workflow deployed in prod for it.
	for _, a := range others {
		var wf uuid.UUID
		err := tx.QueryRow(ctx, `SELECT workflow_id FROM triggers WHERE type = 'storefront' AND storefront_action_id = $1 AND environment = 'prod'`, *a.ID).Scan(&wf)
		if err == nil {
			a.Workflow = &wf
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// SaveAction sets a form's settings: a built-in form's by its kind, any
// other action's by its id (its kind its own). A form can be enabled only
// once a workflow is deployed in prod for it.
func SaveAction(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, a Action) (Action, error) {
	if a.ID != nil {
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM storefront_actions WHERE id = $1 AND NOT builtin`, *a.ID).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return Action{}, fmt.Errorf("%w: no such action", ErrInvalid)
		}
		if err != nil {
			return Action{}, err
		}
		a.Kind = kind
	}
	if !contains(Kinds, a.Kind) {
		return Action{}, fmt.Errorf("%w: no form %q", ErrInvalid, a.Kind)
	}
	a.Label = strings.TrimSpace(a.Label)
	if a.Label == "" {
		a.Label = defaultLabels[a.Kind]
	}
	if len([]rune(a.Label)) > 40 || hasLink(a.Label) {
		return Action{}, fmt.Errorf("%w: a label is up to 40 characters, with no links", ErrInvalid)
	}
	if !takesPayment(a.Kind) {
		a.Payment = "none"
	}
	switch a.Payment {
	case "after_accept", "at_order", "none":
	case "":
		a.Payment = "after_accept"
	default:
		return Action{}, fmt.Errorf("%w: payment is after_accept, at_order or none", ErrInvalid)
	}
	if a.Kind == "order" && !a.Options.Pickup && !a.Options.Delivery {
		a.Options.Pickup = true
	}
	switch a.Kind {
	case "order":
		a.Options.DepositPercent = 0
		zones, err := CleanZones(a.Options.Zones)
		if err != nil {
			return Action{}, err
		}
		a.Options.Zones = zones
		if !a.Options.Delivery || a.Payment == "none" {
			a.Options.PayOnDelivery = false
		}
	case "booking":
		if a.Options.DepositPercent < 0 || a.Options.DepositPercent > 100 {
			return Action{}, fmt.Errorf("%w: a deposit is 1 to 100 per cent of the price", ErrInvalid)
		}
		if c := a.Options.CancelRefund; c != "" && c != "full" && c != "keep_deposit" {
			return Action{}, fmt.Errorf("%w: cancel_refund is full or keep_deposit", ErrInvalid)
		}
		a.Options = ActionOptions{Connection: a.Options.Connection, DepositPercent: a.Options.DepositPercent, CancelRefund: a.Options.CancelRefund}
	case "hire":
		if a.Options.DepositPercent < 0 || a.Options.DepositPercent > 100 {
			return Action{}, fmt.Errorf("%w: a deposit is 1 to 100 per cent of the price", ErrInvalid)
		}
		if a.Options.LateFeeKobo < 0 {
			return Action{}, fmt.Errorf("%w: a late fee is 0 or more", ErrInvalid)
		}
		if !a.Options.Pickup && !a.Options.Delivery {
			a.Options.Pickup = true
		}
		// A hire takes its deposit once accepted, and the rest later.
		a.Payment = "after_accept"
		a.Options = ActionOptions{Connection: a.Options.Connection, DepositPercent: a.Options.DepositPercent, LateFeeKobo: a.Options.LateFeeKobo,
			Pickup: a.Options.Pickup, Delivery: a.Options.Delivery, DeliveryNote: a.Options.DeliveryNote}
	case "space":
		if a.Options.DepositPercent < 0 || a.Options.DepositPercent > 100 {
			return Action{}, fmt.Errorf("%w: a deposit is 1 to 100 per cent of the price", ErrInvalid)
		}
		// A space takes its deposit once accepted, and the rest later.
		a.Payment = "after_accept"
		a.Options = ActionOptions{Connection: a.Options.Connection, DepositPercent: a.Options.DepositPercent}
	case "tickets", "class":
		if a.Options.MaxPerOrder == 0 {
			a.Options.MaxPerOrder = 10
		}
		if a.Options.MaxPerOrder < 1 || a.Options.MaxPerOrder > 100 {
			return Action{}, fmt.Errorf("%w: tickets or places per order are 1 to 100", ErrInvalid)
		}
		opts := ActionOptions{Connection: a.Options.Connection, MaxPerOrder: a.Options.MaxPerOrder}
		if a.Kind == "class" {
			if a.Options.Instalments < 0 || a.Options.Instalments > MaxInstalments {
				return Action{}, fmt.Errorf("%w: a fee is paid in 1 to %d instalments", ErrInvalid, MaxInstalments)
			}
			if a.Options.InstalmentDays < 0 || a.Options.InstalmentDays > 120 {
				return Action{}, fmt.Errorf("%w: instalments are 1 to 120 days apart", ErrInvalid)
			}
			opts.Instalments, opts.InstalmentDays, opts.WaitingList = a.Options.Instalments, a.Options.InstalmentDays, a.Options.WaitingList
			if opts.Instalments > 1 && opts.InstalmentDays == 0 {
				opts.InstalmentDays = 30
			}
		}
		// Tickets and places are paid for when they are taken; a class's
		// first instalment then, and the rest later.
		a.Payment = "at_order"
		a.Options = opts
	default:
		a.Options = ActionOptions{}
	}
	if c := a.Options.Connection; c != "" && !formConnection.MatchString(c) {
		return Action{}, fmt.Errorf("%w: the payments connection is a name, or connector/name", ErrInvalid)
	}
	if len([]rune(a.Options.DeliveryNote)) > 120 || hasLink(a.Options.DeliveryNote) {
		return Action{}, fmt.Errorf("%w: a delivery note is up to 120 characters, with no links", ErrInvalid)
	}
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return Action{}, err
	}
	if a.Enabled && a.Kind == "booking" && sf.ServicesTable == nil {
		return Action{}, fmt.Errorf("%w: choose the services table before taking bookings", ErrInvalid)
	}
	if a.Enabled {
		var n int
		var err error
		if a.ID != nil {
			err = tx.QueryRow(ctx, `SELECT count(*) FROM triggers WHERE type = 'storefront' AND storefront_action_id = $1 AND environment = 'prod'`, *a.ID).Scan(&n)
		} else {
			err = tx.QueryRow(ctx, `SELECT count(*) FROM triggers t WHERE type = 'storefront' AND `+BuiltinTriggerSQL("t", "$1")+` AND environment = 'prod'`, a.Kind).Scan(&n)
		}
		if err != nil {
			return Action{}, err
		}
		if n == 0 {
			return Action{}, fmt.Errorf("%w: deploy a workflow with a storefront %s trigger to prod first", ErrNoWorkflow, a.Kind)
		}
	}
	opts, _ := json.Marshal(a.Options)
	if a.ID != nil {
		if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET enabled = $2, label = $3, payment = $4, options = $5, updated_at = now() WHERE id = $1`,
			*a.ID, a.Enabled, a.Label, a.Payment, opts); err != nil {
			return Action{}, err
		}
		list, err := Actions(ctx, tx, tenant)
		if err != nil {
			return Action{}, err
		}
		for _, x := range list {
			if x.ID != nil && *x.ID == *a.ID {
				return x, nil
			}
		}
		return a, nil
	}
	// A built-in action lives on the first front, its slug its kind
	// (fronts §5.3).
	front, err := ensureFirstFront(ctx, tx, sf)
	if err != nil {
		return Action{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment, options, id, front_id, slug, position, builtin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $2, $10, true)
		ON CONFLICT (storefront_id, kind) WHERE builtin DO UPDATE SET enabled = EXCLUDED.enabled, label = EXCLUDED.label, payment = EXCLUDED.payment,
		  options = EXCLUDED.options, updated_at = now()`, sf.ID, a.Kind, tenant, a.Enabled, a.Label, a.Payment, opts, uuid.Must(uuid.NewV7()), front,
		builtinPosition(a.Kind)); err != nil {
		return Action{}, err
	}
	list, err := Actions(ctx, tx, tenant)
	if err != nil {
		return Action{}, err
	}
	for _, x := range list {
		if x.Builtin && x.Kind == a.Kind {
			return x, nil
		}
	}
	return a, nil
}

// publicAction is an action as a snapshot holds it.
func publicAction(a Action) PublicAction {
	return PublicAction{Kind: a.Kind, Label: a.Label, Payment: a.Payment, Pickup: a.Options.Pickup, Delivery: a.Options.Delivery, DeliveryNote: a.Options.DeliveryNote,
		Zones: a.Options.Zones, PayOnDelivery: a.Options.PayOnDelivery, DepositPercent: a.Options.DepositPercent,
		MaxPerOrder: a.Options.MaxPerOrder, Instalments: a.Options.Instalments, WaitingList: a.Options.WaitingList}
}

// Ref is how a page addresses the action, /a/<ref>: its slug, or for one
// published before fronts its kind.
func (a PublicAction) Ref() string {
	if a.Slug != "" {
		return a.Slug
	}
	return a.Kind
}

// CopiedAction is a new action copied from another for a workflow.
type CopiedAction struct {
	ID    uuid.UUID `json:"id"`
	Slug  string    `json:"slug"`
	Kind  string    `json:"kind"`
	Label string    `json:"label"`
}

// CopyAction makes a new action from one the business has (fronts F2,
// Make a new action for this workflow): on the same front, of the same
// kind, with its label, payment, options and form, off, its slug the
// kind's next free one (order-2, order-3, …). A booking is not copied:
// the storefront keeps one calendar until fronts (F3).
func CopyAction(ctx context.Context, tx pgx.Tx, from uuid.UUID) (CopiedAction, error) {
	var c CopiedAction
	var sf, front, tenant uuid.UUID
	var payment string
	var opts, form []byte
	err := tx.QueryRow(ctx, `SELECT storefront_id, front_id, tenant_id, kind, label, payment, options, form FROM storefront_actions WHERE id = $1`, from).
		Scan(&sf, &front, &tenant, &c.Kind, &c.Label, &payment, &opts, &form)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fmt.Errorf("%w: no such action", ErrInvalid)
	}
	if err != nil {
		return c, err
	}
	if !CanCopy(c.Kind) {
		return c, fmt.Errorf("%w: the booking form is not copied: a storefront takes bookings on one calendar for now", ErrInvalid)
	}
	var taken []string
	rows, err := tx.Query(ctx, `SELECT slug FROM storefront_actions WHERE front_id = $1`, front)
	if err != nil {
		return c, err
	}
	if taken, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return c, err
	}
	for n := 2; ; n++ {
		if s := fmt.Sprintf("%s-%d", c.Kind, n); !contains(taken, s) {
			c.Slug = s
			break
		}
	}
	c.ID = uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_actions (id, storefront_id, front_id, tenant_id, kind, slug, builtin, enabled, label, payment, options, form, position)
		SELECT $1, $2, $3, $4, $5, $6, false, false, $7, $8, $9, $10, COALESCE(max(position), 0) + 1 FROM storefront_actions WHERE front_id = $3`,
		c.ID, sf, front, tenant, c.Kind, c.Slug, c.Label, payment, opts, form); err != nil {
		return c, err
	}
	return c, nil
}

// CanCopy reports whether an action of a kind can be copied (F2).
func CanCopy(kind string) bool { return kind != "booking" }

// publicActions are the enabled forms, for a snapshot.
func publicActions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]PublicAction, error) {
	list, err := Actions(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	var out []PublicAction
	for _, a := range list {
		if a.Enabled && a.Builtin {
			out = append(out, publicAction(a))
		}
	}
	return out, nil
}
