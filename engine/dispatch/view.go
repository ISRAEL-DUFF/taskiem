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
// or the location the rider sent.
func (d *Dispatch) SetProof(ctx context.Context, tenant, delivery uuid.UUID, proof, location string) error {
	return d.tx(ctx, tenant, func(tx pgx.Tx) error {
		if proof != "" {
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
