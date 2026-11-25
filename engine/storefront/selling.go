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

// Forms that sell (docs/spec/forms-that-sell.md §5): an order form that
// sells one product in packages, or a basket of some of a front's items.
// It is an action like any order form, so it is priced, stocked, ordered
// and run as one; the owner makes one from Fronts (Add an order form) or
// from a workflow's hosted form (Sell something with this form).

// SellSpec is what an order form sells: one item in packages (Item and
// Packages, with Choose for sizes and colours), or a basket of the rows
// Items (all of the list when none).
type SellSpec struct {
	Item     string    `json:"item,omitempty"`
	Packages []Package `json:"packages,omitempty"`
	Choose   string    `json:"choose,omitempty"`
	Items    []string  `json:"items,omitempty"`
}

// Form is the spec's order form over list, with the questions of extra.
func (s SellSpec) Form(list string, extra map[string]json.RawMessage) ([]byte, error) {
	if len(s.Packages) > 0 || s.Item != "" {
		if s.Item == "" {
			return nil, fmt.Errorf("%w: choose the product the packages are of", ErrInvalid)
		}
		return PackagesForm(list, s.Item, s.Packages, s.Choose, extra)
	}
	return BasketForm(list, s.Items, extra)
}

// FrontItems are the items a front's order form sells from, and how its
// form names their list: the home page's catalogue, or the front's own
// first list.
func FrontItems(ctx context.Context, tx pgx.Tx, tenant, front uuid.UUID) ([]Item, string, error) {
	var slug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM storefront_fronts WHERE id = $1 AND tenant_id = $2`, front, tenant).Scan(&slug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	if slug == "" {
		items, err := Catalogue(ctx, tx, tenant)
		return items, "catalogue", err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT config FROM storefront_blocks WHERE front_id = $1 AND kind = 'list' ORDER BY position LIMIT 1`, front).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", fmt.Errorf("%w: this front has no list of items to sell from", ErrInvalid)
	}
	if err != nil {
		return nil, "", err
	}
	var lc ListConfig
	if json.Unmarshal(raw, &lc) != nil || lc.TableName == "" {
		return nil, "", fmt.Errorf("%w: this front has no list of items to sell from", ErrInvalid)
	}
	items, err := ListItems(ctx, tx, tenant, lc.TableName)
	return items, lc.TableName, err
}

// CheckSells refuses an order form whose packages are of an item that is
// not on its list, or not sold at a price, or whose basket names an item
// the list does not have.
func CheckSells(items []Item, form []byte) error {
	has := func(id string) (Item, bool) {
		for _, it := range items {
			if it.ID == id {
				return it, true
			}
		}
		return Item{}, false
	}
	if p, ok := FormPackages(form); ok {
		it, found := has(p.Item)
		if !found {
			return fmt.Errorf("%w: the product the packages are of is not on this front's list", ErrInvalid)
		}
		if it.PriceKobo == nil {
			return fmt.Errorf("%w: %s has no price on your list: give it one first", ErrInvalid, it.Name)
		}
	}
	for _, id := range BasketItems(form) {
		if _, found := has(id); !found {
			return fmt.Errorf("%w: an item of the basket is not on this front's list", ErrInvalid)
		}
	}
	return nil
}

// SellWarnings are what an order form's packages say against the list's
// prices: a package dearer per item than the item's own price (a
// warning, not a refusal: a bundle may carry a gift).
func SellWarnings(items []Item, form []byte) []string {
	p, ok := FormPackages(form)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range items {
		if it.ID != p.Item || it.PriceKobo == nil {
			continue
		}
		for _, x := range p.Packages {
			if w := PackageWarning(it.Name, x, *it.PriceKobo); w != "" {
				out = append(out, w)
			}
		}
	}
	return out
}

// NewOrderForm is an order form to add to a front.
type NewOrderForm struct {
	Label  string
	Sells  SellSpec
	Notice string
	// Extra are the form's own questions besides what it sells, and
	// Required those of them that must be answered.
	Extra    map[string]json.RawMessage
	Required []string
}

// withRequired is form with its required questions.
func withRequired(form []byte, required []string) ([]byte, error) {
	if len(required) == 0 {
		return form, nil
	}
	var m map[string]any
	if err := json.Unmarshal(form, &m); err != nil {
		return nil, err
	}
	m["required"] = required
	out, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return out, CheckForm(out)
}

// AddOrderAction adds an order form that sells to a front, off: its
// payment, delivery areas, pay at the door and payments connection as the
// home page's order form has them (the storefront's own), its address from
// its label. It answers the action and the publish warnings of its
// packages.
func AddOrderAction(ctx context.Context, tx pgx.Tx, tenant, front uuid.UUID, in NewOrderForm) (Action, []string, error) {
	var sf uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT storefront_id FROM storefront_fronts WHERE id = $1 AND tenant_id = $2`, front, tenant).Scan(&sf); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Action{}, nil, ErrNotFound
		}
		return Action{}, nil, err
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = defaultLabels["order"]
	}
	if len([]rune(label)) > 40 || hasLink(label) {
		return Action{}, nil, fmt.Errorf("%w: a form's label is up to 40 characters, with no links", ErrInvalid)
	}
	if err := CheckNotice(in.Notice); err != nil {
		return Action{}, nil, err
	}
	items, list, err := FrontItems(ctx, tx, tenant, front)
	if err != nil {
		return Action{}, nil, err
	}
	form, err := in.Sells.Form(list, in.Extra)
	if err == nil {
		form, err = withRequired(form, in.Required)
	}
	if err != nil {
		return Action{}, nil, err
	}
	if len(form) > 64<<10 {
		return Action{}, nil, fmt.Errorf("%w: a form is up to 64 KB", ErrInvalid)
	}
	if err := CheckSells(items, form); err != nil {
		return Action{}, nil, err
	}
	payment := "after_accept"
	opts := ActionOptions{Pickup: true}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT payment, options FROM storefront_actions WHERE storefront_id = $1 AND kind = 'order' AND builtin`, sf).Scan(&payment, &raw)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return Action{}, nil, err
	default:
		var o ActionOptions
		_ = json.Unmarshal(raw, &o)
		opts = ActionOptions{Connection: o.Connection, Pickup: o.Pickup, Delivery: o.Delivery, DeliveryNote: o.DeliveryNote, Zones: o.Zones,
			PayOnDelivery: o.PayOnDelivery, DepositPercent: o.DepositPercent}
	}
	if !opts.Pickup && !opts.Delivery {
		opts.Pickup = true
	}
	opts.Notice = in.Notice
	optsRaw, _ := json.Marshal(opts)
	var taken []string
	rows, err := tx.Query(ctx, `SELECT slug FROM storefront_actions WHERE front_id = $1`, front)
	if err != nil {
		return Action{}, nil, err
	}
	if taken, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return Action{}, nil, err
	}
	base := FrontSlug(label)
	if base == "" || !validActionSlug(base) {
		base = "order"
	}
	slug := base
	for n := 2; contains(taken, slug) || contains(BuiltinKinds, slug); n++ {
		slug = fmt.Sprintf("%s-%d", strings.TrimRight(trimRunes(base, 36), "-"), n)
	}
	id := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_actions (id, storefront_id, front_id, tenant_id, kind, slug, builtin, enabled, label, payment, options, form, position)
		SELECT $1, $2, $3, $4, 'order', $5, false, false, $6, $7, $8, $9, COALESCE(max(position), 0) + 1 FROM storefront_actions WHERE front_id = $3`,
		id, sf, front, tenant, slug, label, payment, optsRaw, form); err != nil {
		return Action{}, nil, err
	}
	return Action{ID: &id, Slug: slug, FrontID: &front, Kind: "order", Label: label, Payment: payment, Options: opts}, SellWarnings(items, form), nil
}

func trimRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// validActionSlug is an address /a/ takes: lower-case letters, digits and
// hyphens, 1 to 40, not starting or ending with a hyphen.
func validActionSlug(s string) bool {
	if s == "" || len(s) > 40 || strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// SetSells changes what an order form sells (its packages, or its
// basket's items) and its notice, keeping its own questions; answers the
// publish warnings.
func SetSells(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, sells SellSpec, notice *string) ([]string, error) {
	var kind string
	var front uuid.UUID
	var form, raw []byte
	if err := tx.QueryRow(ctx, `SELECT kind, front_id, form, options FROM storefront_actions WHERE id = $1 AND tenant_id = $2`, id, tenant).Scan(&kind, &front, &form, &raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if kind != "order" {
		return nil, fmt.Errorf("%w: only an order form sells", ErrInvalid)
	}
	items, list, err := FrontItems(ctx, tx, tenant, front)
	if err != nil {
		return nil, err
	}
	extra := map[string]json.RawMessage{}
	var f struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(EffectiveForm(form, kind), &f)
	for _, q := range FormFields(EffectiveForm(form, kind)) {
		extra[q] = f.Properties[q]
	}
	next, err := sells.Form(list, extra)
	if err != nil {
		return nil, err
	}
	if err := CheckSells(items, next); err != nil {
		return nil, err
	}
	var opts ActionOptions
	_ = json.Unmarshal(raw, &opts)
	if notice != nil {
		if err := CheckNotice(*notice); err != nil {
			return nil, err
		}
		opts.Notice = *notice
	}
	optsRaw, _ := json.Marshal(opts)
	if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET form = $2, options = $3, updated_at = now() WHERE id = $1`, id, next, optsRaw); err != nil {
		return nil, err
	}
	return SellWarnings(items, next), nil
}

// PublishWarnings are what a publish says without refusing: the enabled
// order forms' packages dearer per item than their product's own price,
// and a product no longer on its list.
func PublishWarnings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT a.front_id, a.label, a.form FROM storefront_actions a JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
		WHERE a.tenant_id = $1 AND a.enabled AND a.kind = 'order' AND a.form IS NOT NULL ORDER BY f.position, a.position`, tenant)
	if err != nil {
		return nil, err
	}
	type sells struct {
		front uuid.UUID
		label string
		form  []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sells, error) {
		var s sells
		return s, r.Scan(&s.front, &s.label, &s.form)
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range list {
		if _, ok := FormPackages(s.form); !ok {
			continue
		}
		items, _, err := FrontItems(ctx, tx, tenant, s.front)
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				continue
			}
			return nil, err
		}
		if CheckSells(items, s.form) != nil {
			out = append(out, fmt.Sprintf("%s sells a product that is not on its list now, so it takes no orders until you choose another.", s.label))
			continue
		}
		out = append(out, SellWarnings(items, s.form)...)
	}
	return out, nil
}
