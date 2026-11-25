package storefront

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The One product kind of front (docs/spec/forms-that-sell.md §5.4): the
// whole page of a pay-on-delivery seller made from one catalogue row. Its
// settings are the front's (storefront_fronts.options): the promise line
// at the top, up to six feature chips each with an icon from a fixed set,
// and whether the stock line ("Only 14 left", from the item's stock and
// never a made-up number) shows. Its headline and reason to buy are the
// front's name and line; its photos the item's; its form an order action
// selling the item in packages. The public host draws it
// (engine/publicweb/oneproduct.go).

// OneProduct is the One product kind's name.
const OneProduct = "one-product"

// Limits on a One product front's settings.
const (
	MaxPromise  = 80
	MaxChips    = 6
	MaxChipText = 40
)

// ChipIcons are the icons a feature chip may have: a fixed set, so a page
// never carries a picture the owner uploaded as a badge (a fake seal, a
// bank's logo).
var ChipIcons = []string{"truck", "cash", "shield", "star", "clock", "check", "gift", "leaf", "phone", "sparkle", "heart", "return"}

// Chip is one feature chip: an icon and a short line.
type Chip struct {
	Icon string `json:"icon"`
	Text string `json:"text"`
}

// OneProductSettings are a One product front's settings.
type OneProductSettings struct {
	// Promise is the line at the top, in the storefront's accent ("Pay on
	// delivery nationwide"); empty shows none.
	Promise string `json:"promise,omitempty"`
	Chips   []Chip `json:"chips,omitempty"`
	// StockLine shows how many are left, where stock is kept.
	StockLine bool `json:"stock_line"`
}

// DefaultPromise is the promise line a new One product front has.
const DefaultPromise = "Pay on delivery nationwide"

// CleanOneProduct checks a One product front's settings: a promise of one
// line up to 80 characters with no links, up to six chips each an icon of
// the set and a line up to 40 characters with no links.
func CleanOneProduct(s OneProductSettings) (OneProductSettings, error) {
	s.Promise = strings.TrimSpace(s.Promise)
	if len([]rune(s.Promise)) > MaxPromise || hasLink(s.Promise) || strings.ContainsAny(s.Promise, "\n\r") {
		return s, fmt.Errorf("%w: the promise line is up to %d characters on one line, with no links", ErrInvalid, MaxPromise)
	}
	if len(s.Chips) > MaxChips {
		return s, fmt.Errorf("%w: up to %d feature chips", ErrInvalid, MaxChips)
	}
	out := make([]Chip, 0, len(s.Chips))
	for _, c := range s.Chips {
		c.Text = strings.TrimSpace(c.Text)
		if !slices.Contains(ChipIcons, c.Icon) {
			return s, fmt.Errorf("%w: a chip's icon is one of %s", ErrInvalid, strings.Join(ChipIcons, ", "))
		}
		if c.Text == "" || len([]rune(c.Text)) > MaxChipText || hasLink(c.Text) || strings.ContainsAny(c.Text, "\n\r") {
			return s, fmt.Errorf("%w: a chip's words are 1 to %d characters on one line, with no links", ErrInvalid, MaxChipText)
		}
		out = append(out, c)
	}
	s.Chips = out
	return s, nil
}

// CheckFrontSettings checks a front's settings for its kind: a One
// product front's are OneProductSettings, read strictly; other kinds
// take none.
func CheckFrontSettings(kind string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if kind != OneProduct {
		return nil, fmt.Errorf("%w: only a One product front has settings of its own", ErrInvalid)
	}
	var s OneProductSettings
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%w: a One product front's settings are its promise, its chips and its stock line", ErrInvalid)
	}
	s, err := CleanOneProduct(s)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// ReadOneProduct reads a One product front's settings as stored (checked
// when they were saved); settings that do not read are none.
func ReadOneProduct(raw json.RawMessage) OneProductSettings {
	var s OneProductSettings
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// NewOneProduct is a One product front to make from one catalogue row:
// its name (the item's by default), its reason to buy (the item's words
// by default), the item and its packages (one of one at the item's price
// when none are given), and the order form's button and notice.
type NewOneProduct struct {
	Name     string
	Tagline  string
	Item     string
	Packages []Package
	Choose   string
	Label    string
	Notice   string
}

// OneProductFront is what CreateOneProduct made: the front, its order
// form, the item it sells, and the warnings of its packages.
type OneProductFront struct {
	Front    Front
	Action   Action
	Item     Item
	Warnings []string
}

// errNoProducts is a One product page asked for with no products.
var errNoProducts = fmt.Errorf("%w: add your products first: a One product page sells one of them", ErrInvalid)

// CreateOneProduct makes a One product front (forms that sell §5.4) from
// a catalogue row, off: its list is the catalogue's own table (shared with
// the home page, so a hero item is not copied), its settings the kind's,
// and its order form sells the item in packages, delivered and paid at
// the door, with the home order form's delivery areas and payments.
func CreateOneProduct(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in NewOneProduct) (OneProductFront, error) {
	k, ok := oneProductKind()
	if !ok || len(k.Actions) == 0 {
		return OneProductFront{}, fmt.Errorf("%w: no One product kind", ErrInvalid)
	}
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return OneProductFront{}, err
	}
	if sf.CatalogueTable == nil {
		return OneProductFront{}, errNoProducts
	}
	var table string
	if err := tx.QueryRow(ctx, `SELECT name FROM data_tables WHERE id = $1`, *sf.CatalogueTable).Scan(&table); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OneProductFront{}, errNoProducts
		}
		return OneProductFront{}, err
	}
	items, err := Catalogue(ctx, tx, tenant)
	if err != nil {
		return OneProductFront{}, err
	}
	var it Item
	for _, x := range items {
		if x.ID == in.Item && in.Item != "" {
			it = x
		}
	}
	switch {
	case it.ID == "" || it.VariantOf != "":
		return OneProductFront{}, fmt.Errorf("%w: choose a product from your list", ErrInvalid)
	case it.PriceKobo == nil:
		return OneProductFront{}, fmt.Errorf("%w: %s has no price on your list: give it one first", ErrInvalid, it.Name)
	}
	pkgs := in.Packages
	if len(pkgs) == 0 {
		pkgs = []Package{{Qty: 1, PriceKobo: *it.PriceKobo}}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = trimRunes(it.Name, 80)
	}
	tagline := strings.TrimSpace(in.Tagline)
	if tagline == "" && !hasLink(it.Description) {
		tagline = strings.Join(strings.Fields(it.Description), " ")
		if r := []rune(tagline); len(r) > 120 {
			tagline = strings.TrimSpace(string(r[:119])) + "…"
		}
	}
	cols := sf.CatalogueColumns
	f, err := CreateFront(ctx, tx, tenant, NewFront{Name: name, Kind: OneProduct, Table: sf.CatalogueTable, TableName: table, Columns: &cols, Tagline: tagline, NoActions: true})
	if err != nil {
		return OneProductFront{}, err
	}
	ka := k.Actions[0]
	label := in.Label
	if label == "" {
		label = ka.Label
	}
	notice := in.Notice
	if notice == "" {
		notice = ka.Options.Notice
	}
	a, warnings, err := AddOrderAction(ctx, tx, tenant, f.ID, NewOrderForm{Label: label, Sells: SellSpec{Item: it.ID, Packages: pkgs, Choose: in.Choose}, Notice: notice})
	if err != nil {
		return OneProductFront{}, err
	}
	// Delivered and paid at the door, as the video's page is (§5.4), with
	// the home order form's areas and payments kept.
	a.Options.Pickup, a.Options.Delivery, a.Options.PayOnDelivery = false, true, true
	raw, _ := json.Marshal(a.Options)
	if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET options = $2, updated_at = now() WHERE id = $1`, a.ID, raw); err != nil {
		return OneProductFront{}, err
	}
	return OneProductFront{Front: f, Action: a, Item: it, Warnings: warnings}, nil
}

// oneProductKind is the One product kind's preset.
func oneProductKind() (FrontKind, bool) {
	for _, k := range AllFrontKinds() {
		if k.Name == OneProduct {
			return k, true
		}
	}
	return FrontKind{}, false
}
