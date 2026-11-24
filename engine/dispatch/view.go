package dispatch

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// View is what a rider is shown of a delivery, read when a message to
// them is sent: the offer shows where it goes (the area, the landmark and
// the pin) and what it pays; the address and the customer's phone only
// once they hold it.
type View struct {
	Business  string
	Reference string
	Status    string
	RiderID   uuid.UUID
	Rider     string
	// Pickup is the business's address, PickupLink its directions.
	Pickup, PickupLink string
	// The drop: the area, and while held the address, landmark and pin.
	Area, Address, Landmark, Pin string
	// The customer's first name, and phone while held.
	Customer, CustomerPhone string
	Items                   string
	FeeKobo                 int64
	// DueKobo is what the customer pays at the door (0: paid already, or
	// nothing to pay); Door the account number for it, when open.
	DueKobo int64
	Door    *storefront.DoorAccount
	// Proof: the business asks for a photo when they deliver.
	Proof     bool
	ExpiresAt time.Time
}

// Held reports whether the rider holds the delivery (sees the address).
func (v View) Held() bool {
	switch v.Status {
	case "accepted", "picked_up", "arrived", "delivered":
		return true
	}
	return false
}

// ViewTx reads a delivery for its rider.
func (d *Dispatch) ViewTx(ctx context.Context, tx pgx.Tx, tenant, delivery uuid.UUID) (View, error) {
	x, err := d.GetTx(ctx, tx, tenant, delivery)
	if err != nil {
		return View{}, err
	}
	ord, err := d.Orders.GetOrder(ctx, tx, tenant, x.Reference)
	if err != nil {
		return View{}, err
	}
	v := View{Reference: ord.Reference, Status: x.Status, RiderID: x.RiderID, Rider: x.Rider, Area: ord.Zone, Customer: firstName(ord.Customer.Name),
		Items: storefront.Summary(ord.Basket), FeeKobo: x.FeeKobo, ExpiresAt: x.ExpiresAt, Landmark: ord.Customer.Landmark, Pin: ord.Customer.Pin}
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&v.Business); err != nil {
		return View{}, err
	}
	if sf, err := storefront.Get(ctx, tx, tenant); err == nil {
		v.Pickup, v.PickupLink = strings.TrimSpace(sf.Draft.Address), sf.Draft.MapLink()
	}
	// A dispatch company's delivery (phase 5) is picked up at the business
	// that placed it, as its order says.
	if inner, err := innerOrderTx(ctx, tx, ord); err == nil && inner {
		var pickup, link *string
		if err := tx.QueryRow(ctx, `SELECT pickup, pickup_link FROM link_orders WHERE order_id = $1`, ord.ID).Scan(&pickup, &link); err == nil {
			v.Pickup, v.PickupLink = "", ""
			if pickup != nil {
				v.Pickup = *pickup
			}
			if link != nil {
				v.PickupLink = *link
			}
		}
	}
	if v.Held() {
		v.Address, v.CustomerPhone = ord.Customer.Address, ord.Customer.Phone
	}
	if ord.Payment == "on_delivery" && ord.PaidAt == nil {
		v.DueKobo = ord.Payable()
		if a, ok, err := storefront.OpenDoorAccount(ctx, tx, tenant, ord.ID, d.now()); err != nil {
			return View{}, err
		} else if ok {
			v.Door = &a
		}
	}
	s, err := GetSettings(ctx, tx, tenant)
	if err != nil {
		return View{}, err
	}
	v.Proof = s.Proof
	return v, nil
}

// --- a rider's deliveries, for their words and photos ---

// Open is a delivery a rider holds or was offered, for any business.
type Open struct {
	Tenant   uuid.UUID
	Delivery uuid.UUID
	Status   string
	At       time.Time
}

// RiderDeliveries are a rider's open deliveries (and those delivered in the
// last two hours), newest first: the phone their WhatsApp proves, or the
// contact their Telegram is linked to.
func (d *Dispatch) RiderDeliveries(ctx context.Context, phone, channel, addr string) ([]Open, error) {
	var hash []byte
	if phone != "" {
		hash = d.Riders.Book.PhoneHash(phone)
	}
	rows, err := d.Pool.Query(ctx, `SELECT tenant_id, delivery_id, status, changed_at FROM taskiem_rider_deliveries($1, $2, $3)`,
		hash, channel, d.Riders.Book.AddressHash(channel, addr))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Open, error) {
		var o Open
		return o, r.Scan(&o.Tenant, &o.Delivery, &o.Status, &o.At)
	})
}

// SetProof keeps a delivery's photo (its place in the business's storage)
// or the location the rider sent. A photo is kept beside the delivery's
// earlier ones (delivery_proofs); the delivery names the latest.
func (d *Dispatch) SetProof(ctx context.Context, tenant, delivery uuid.UUID, proof, location string) error {
	return d.tx(ctx, tenant, func(tx pgx.Tx) error {
		if proof != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO delivery_proofs (id, tenant_id, delivery_id, path) VALUES ($1, $2, $3, $4)`,
				uuid.Must(uuid.NewV7()), tenant, delivery, proof); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE deliveries SET proof = $2 WHERE id = $1`, delivery, proof); err != nil {
				return err
			}
		}
		if location != "" {
			if _, err := tx.Exec(ctx, `UPDATE deliveries SET location = $2 WHERE id = $1`, delivery, location); err != nil {
				return err
			}
		}
		return audit(ctx, tx, tenant, "dispatch.proof", delivery.String(), map[string]any{"photo": proof != "", "location": location != ""})
	})
}

// ProofMoveWindow is how long after a photo was kept the rider may say it
// was for another of their orders.
const ProofMoveWindow = 30 * time.Minute

// MoveProof moves the photo the delivery's rider sent last, kept for
// another of their deliveries of the business within ProofMoveWindow, to
// this delivery; each delivery then names the latest photo it still has.
// ErrNoDelivery when there is no such photo (it was moved already).
func (d *Dispatch) MoveProof(ctx context.Context, tenant, delivery uuid.UUID) (Delivery, error) {
	var x Delivery
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if x, err = d.GetTx(ctx, tx, tenant, delivery); err != nil {
			return err
		}
		var id, from uuid.UUID
		err = tx.QueryRow(ctx, `SELECT p.id, p.delivery_id FROM delivery_proofs p JOIN deliveries o ON o.id = p.delivery_id
			WHERE p.tenant_id = $1 AND o.rider_id = $2 AND p.created_at > now() - make_interval(secs => $3)
			ORDER BY p.created_at DESC, p.id DESC LIMIT 1 FOR UPDATE OF p`, tenant, x.RiderID, ProofMoveWindow.Seconds()).Scan(&id, &from)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && from == delivery {
			return ErrNoDelivery
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE delivery_proofs SET delivery_id = $2 WHERE id = $1`, id, delivery); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE deliveries d SET proof = (SELECT p.path FROM delivery_proofs p WHERE p.delivery_id = d.id ORDER BY p.created_at DESC, p.id DESC LIMIT 1)
			WHERE d.tenant_id = $1 AND d.id IN ($2, $3)`, tenant, from, delivery); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, "dispatch.proof_moved", delivery.String(), map[string]any{"from": from})
	})
	return x, err
}

// ProofCounts is how many photos each of the deliveries has.
func (d *Dispatch) ProofCounts(ctx context.Context, tenant uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	if len(ids) == 0 {
		return out, nil
	}
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT delivery_id, count(*) FROM delivery_proofs WHERE tenant_id = $1 AND delivery_id = ANY ($2) GROUP BY 1`, tenant, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return err
			}
			out[id] = n
		}
		return rows.Err()
	})
	return out, err
}

// RiderPhoneHashTx is the keyed hash of the rider of a delivery, which a
// pressing chat address must be.
func (d *Dispatch) RiderPhoneHashTx(ctx context.Context, tx pgx.Tx, tenant, delivery uuid.UUID) ([]byte, error) {
	var h []byte
	err := tx.QueryRow(ctx, `SELECT r.phone_hash FROM deliveries x JOIN riders r ON r.id = x.rider_id WHERE x.tenant_id = $1 AND x.id = $2`, tenant, delivery).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoDelivery
	}
	return h, err
}
