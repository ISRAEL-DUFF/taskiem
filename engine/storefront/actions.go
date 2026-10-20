package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Kinds are the forms a storefront can offer (storefronts 6.3).
var Kinds = []string{"order", "enquiry", "callback", "booking"}

// ErrNoWorkflow refuses enabling a form no workflow is deployed for.
var ErrNoWorkflow = errors.New("storefront: no workflow is deployed for this form")

// Action is one form's settings.
type Action struct {
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
	// Connection is the Paystack connection payments use (default main).
	Connection string `json:"connection,omitempty"`
	// Pickup and Delivery are what an order may choose; neither means
	// neither is asked.
	Pickup   bool `json:"pickup,omitempty"`
	Delivery bool `json:"delivery,omitempty"`
	// DeliveryNote is shown with delivery ("Yaba and Surulere only").
	DeliveryNote string `json:"delivery_note,omitempty"`
	// DepositPercent is how much of a service's price a booking pays
	// online (1 to 100; unset is the full price).
	DepositPercent int `json:"deposit_percent,omitempty"`
	// CancelRefund is what a customer cancelling a paid booking gets back:
	// full (the default) or keep_deposit.
	CancelRefund string `json:"cancel_refund,omitempty"`
}

// takesPayment reports whether a form's kind may ask for payment.
func takesPayment(kind string) bool { return kind == "order" || kind == "booking" }

// Deposit is what a booking pays online of price.
func (o ActionOptions) Deposit(price int64) int64 {
	if o.DepositPercent <= 0 || o.DepositPercent >= 100 {
		return price
	}
	return (price*int64(o.DepositPercent) + 99) / 100
}

// PublicAction is what a published page shows of a form.
type PublicAction struct {
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	Payment      string `json:"payment"`
	Pickup       bool   `json:"pickup,omitempty"`
	Delivery     bool   `json:"delivery,omitempty"`
	DeliveryNote string `json:"delivery_note,omitempty"`
	// DepositPercent is a booking's online part of the price (100: all).
	DepositPercent int `json:"deposit_percent,omitempty"`
}

var defaultLabels = map[string]string{"order": "Order now", "enquiry": "Ask a question", "callback": "Request a call back", "booking": "Book"}

// Actions reads the storefront's forms, every kind present (unset ones
// disabled with their default label), with the workflow deployed for each.
func Actions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Action, error) {
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	have := map[string]Action{}
	rows, err := tx.Query(ctx, `SELECT kind, enabled, label, payment, options FROM storefront_actions WHERE storefront_id = $1`, sf.ID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Action, error) {
		var a Action
		var opts []byte
		err := r.Scan(&a.Kind, &a.Enabled, &a.Label, &a.Payment, &opts)
		_ = json.Unmarshal(opts, &a.Options)
		return a, err
	})
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		have[a.Kind] = a
	}
	out := make([]Action, 0, len(Kinds))
	for _, k := range Kinds {
		a, ok := have[k]
		if !ok {
			a = Action{Kind: k, Label: defaultLabels[k], Payment: "after_accept"}
			if !takesPayment(k) {
				a.Payment = "none"
			}
		}
		var wf uuid.UUID
		err := tx.QueryRow(ctx, `SELECT workflow_id FROM triggers WHERE type = 'storefront' AND storefront_action = $1 AND environment = 'prod'`, k).Scan(&wf)
		if err == nil {
			a.Workflow = &wf
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// SaveAction sets a form's settings. A form can be enabled only once a
// workflow is deployed in prod for it.
func SaveAction(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, a Action) (Action, error) {
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
	case "booking":
		if a.Options.DepositPercent < 0 || a.Options.DepositPercent > 100 {
			return Action{}, fmt.Errorf("%w: a deposit is 1 to 100 per cent of the price", ErrInvalid)
		}
		if c := a.Options.CancelRefund; c != "" && c != "full" && c != "keep_deposit" {
			return Action{}, fmt.Errorf("%w: cancel_refund is full or keep_deposit", ErrInvalid)
		}
		a.Options = ActionOptions{Connection: a.Options.Connection, DepositPercent: a.Options.DepositPercent, CancelRefund: a.Options.CancelRefund}
	default:
		a.Options = ActionOptions{}
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
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM triggers WHERE type = 'storefront' AND storefront_action = $1 AND environment = 'prod'`, a.Kind).Scan(&n); err != nil {
			return Action{}, err
		}
		if n == 0 {
			return Action{}, fmt.Errorf("%w: deploy a workflow with a storefront %s trigger to prod first", ErrNoWorkflow, a.Kind)
		}
	}
	opts, _ := json.Marshal(a.Options)
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment, options) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (storefront_id, kind) DO UPDATE SET enabled = EXCLUDED.enabled, label = EXCLUDED.label, payment = EXCLUDED.payment,
		  options = EXCLUDED.options, updated_at = now()`, sf.ID, a.Kind, tenant, a.Enabled, a.Label, a.Payment, opts); err != nil {
		return Action{}, err
	}
	list, err := Actions(ctx, tx, tenant)
	if err != nil {
		return Action{}, err
	}
	for _, x := range list {
		if x.Kind == a.Kind {
			return x, nil
		}
	}
	return a, nil
}

// publicActions are the enabled forms, for a snapshot.
func publicActions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]PublicAction, error) {
	list, err := Actions(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	var out []PublicAction
	for _, a := range list {
		if a.Enabled {
			out = append(out, PublicAction{Kind: a.Kind, Label: a.Label, Payment: a.Payment, Pickup: a.Options.Pickup, Delivery: a.Options.Delivery, DeliveryNote: a.Options.DeliveryNote,
				DepositPercent: a.Options.DepositPercent})
		}
	}
	return out, nil
}
