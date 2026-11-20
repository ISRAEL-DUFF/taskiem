package api

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// "deliver order P-7QX4KD to @warehouse", "send order P-7QX4KD to
// @warehouse" (saved objects S2; §6.3): a saved place is where a purchase
// from a linked supplier is delivered: its street, area and landmark, and
// its phone for the rider (else the business's own delivery number). Send
// also sends it, as "send P-7QX4KD" does. Only before the supplier has it:
// what they hold is their own copy.
var deliverToSaved = regexp.MustCompile(`(?i)^\s*(deliver|send)\s+(?:order\s+|purchase\s+)?(P-[A-Z0-9]{6})\s+to\s+@([a-z0-9][a-z0-9_-]{0,31})\s*[.!]?\s*$`)

// waDeliverToSaved acts on "deliver|send P-… to @place"; false when the
// words are not that.
func (s *Server) waDeliverToSaved(ctx context.Context, c *chat, text string) (bool, error) {
	m := deliverToSaved.FindStringSubmatch(text)
	if m == nil {
		return false, nil
	}
	if !c.p.Can(PermStorefrontManage) {
		return true, c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	verb, ref := strings.ToLower(m[1]), strings.ToUpper(m[2])
	name, err := saved.Name(m[3])
	if err != nil {
		return false, nil //nolint:nilerr // not a saved thing's name: not this
	}
	got, err := s.savedFind(ctx, c, name)
	if err != nil {
		return true, err
	}
	if len(got) == 0 {
		return true, c.say(ctx, s, c.t("wa.saved.share_none", "name", name))
	}
	// Which, when one's own and the business's both have the name; one's
	// own gives the business only the copy on the order (§5.3).
	one, ok, err := s.savedPickOne(ctx, c, chatSavedPick{Kind: "deliver", Text: text, Name: name}, got)
	if !ok || err != nil {
		return true, err
	}
	x := one.row
	if x.Kind != saved.KindPlace {
		return true, c.say(ctx, s, c.t("wa.deliver.saved_kind", "name", x.Name, "kind", savedKindWords(x)))
	}
	d, address := placeDeliverTo(x)
	var sent stock.Purchase
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if err := s.riderPhoneTx(ctx, tx, c, &d); err != nil {
			return err
		}
		o, err := stock.PurchaseByReferenceTx(ctx, tx, c.tenant.ID, ref)
		if err != nil {
			return err
		}
		if err := links.SetPurchaseDeliverToTx(ctx, tx, s.Vault, c.tenant.ID, o.ID, d); err != nil {
			return err
		}
		if err := waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.deliver_to", o.Reference, map[string]any{"purchase_id": o.ID, "saved": x.Name}); err != nil {
			return err
		}
		if verb != "send" {
			sent = o
			return nil
		}
		if sent, err = s.Purchases.SendTx(ctx, tx, c.tenant.ID, o.ID, c.p.Actor()); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.send", sent.Reference, map[string]any{"purchase_id": sent.ID})
	})
	switch {
	case errors.Is(err, errNoRiderPhone):
		return true, c.say(ctx, s, c.t("wa.deliver.no_phone", "name", x.Name))
	case errors.Is(err, links.ErrState), errors.Is(err, links.ErrInvalid):
		msg := strings.TrimPrefix(strings.TrimPrefix(err.Error(), links.ErrState.Error()+": "), links.ErrInvalid.Error()+": ")
		return true, c.say(ctx, s, c.t("wa.deliver.not", "reason", whatsapp.SafeText(msg)))
	case err != nil:
		return true, s.purchaseChatFail(ctx, c, err)
	}
	said := c.t("wa.deliver.set", "reference", sent.Reference, "name", x.Name, "address", whatsapp.SafeText(address))
	if verb == "send" {
		if err := s.waDrainOutbox(ctx, c.tenant.ID); err != nil {
			s.Logger.Warn("stock: sending an order", "err", err)
		}
		said = c.t("wa.order.sent", "reference", sent.Reference, "supplier", whatsapp.SafeText(sent.Supplier)) + "\n" + said
	}
	return true, c.say(ctx, s, said)
}

// placeDeliverTo is where a saved place has a purchase delivered: its
// street, area and landmark, and its phone; and its address as said.
func placeDeliverTo(x savedRow) (links.DeliverTo, string) {
	str := func(k string) string { v, _ := x.Details[k].(string); return v }
	address := strings.Trim(strings.Join([]string{str("line"), str("area")}, ", "), ", ")
	if l := str("landmark"); l != "" {
		address += " (" + l + ")"
	}
	return links.DeliverTo{Address: address, Phone: str("phone")}, address
}

// riderPhoneTx gives a place with no phone the business's own delivery
// number; errNoRiderPhone when there is none either.
func (s *Server) riderPhoneTx(ctx context.Context, tx pgx.Tx, c *chat, d *links.DeliverTo) error {
	if d.Phone == "" {
		def, err := links.DeliverToTx(ctx, tx, s.Vault, c.tenant.ID)
		if err != nil {
			return err
		}
		d.Phone = def.Phone
	}
	if d.Phone == "" {
		return errNoRiderPhone
	}
	return nil
}

// deliverTail is "…, deliver to @warehouse" at the end of an order to
// linked businesses (mentions.go): where what it buys is delivered.
var deliverTail = regexp.MustCompile(`(?i)[\s,;.]*(?:and\s+)?(?:deliver|send)(?:\s+(?:it|them|all|everything))?\s+to\s+@([a-z0-9][a-z0-9_-]{0,31})\s*[.!]?\s*$`)

// orderPlace is the saved place an order's words end with ("deliver to
// @warehouse"), read off them: the words without it, and where; ok false
// when the person was told why not (or asked which), so the order waits.
func (s *Server) orderPlace(ctx context.Context, c *chat, text string) (rest string, d *links.DeliverTo, place, address string, ok bool, err error) {
	m := deliverTail.FindStringSubmatchIndex(text)
	if m == nil {
		return text, nil, "", "", true, nil
	}
	name, nerr := saved.Name(text[m[2]:m[3]])
	if nerr != nil {
		return text, nil, "", "", true, nil //nolint:nilerr // not a saved thing's name: the words as they are
	}
	got, err := s.savedFind(ctx, c, name)
	if err != nil {
		return "", nil, "", "", false, err
	}
	if len(got) == 0 {
		return "", nil, "", "", false, c.say(ctx, s, c.t("wa.saved.share_none", "name", name))
	}
	one, picked, err := s.savedPickOne(ctx, c, chatSavedPick{Kind: "order", Text: text, Name: name}, got)
	if !picked || err != nil {
		return "", nil, "", "", false, err
	}
	if one.row.Kind != saved.KindPlace {
		return "", nil, "", "", false, c.say(ctx, s, c.t("wa.deliver.saved_kind", "name", one.row.Name, "kind", savedKindWords(one.row)))
	}
	to, address := placeDeliverTo(one.row)
	return text[:m[0]], &to, one.row.Name, address, true, nil
}

// errNoRiderPhone: a place with no phone, and the business has no delivery
// number either.
var errNoRiderPhone = errors.New("no phone for the rider")
