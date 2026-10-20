package storefront

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CheckServices refuses a services mapping without a name or duration.
func (m *Columns) CheckServices(known func(string) (typ string, ok bool)) error {
	if m.Duration == "" {
		return fmt.Errorf("%w: the services need a duration column (minutes)", ErrInvalid)
	}
	m.Stock = "" // services have availability, not stock
	return m.Check(known)
}

// SaveBookings sets the services table, its mapping and the availability.
// The mapping is checked against the table by the caller (CheckServices).
func SaveBookings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, table *uuid.UUID, columns Columns, av Availability) (Storefront, error) {
	if err := av.Check(); err != nil {
		return Storefront{}, err
	}
	c, _ := json.Marshal(columns)
	a, _ := json.Marshal(av)
	s, err := scan(tx.QueryRow(ctx, `UPDATE storefronts SET services_table = $2, services_columns = $3, availability = $4, updated_at = now()
		WHERE tenant_id = $1 RETURNING `+cols, tenant, table, c, a))
	if err == nil && table == nil {
		_, err = tx.Exec(ctx, `DELETE FROM storefront_services WHERE storefront_id = $1`, s.ID)
	}
	return s, err
}

// AddDayOff takes a date out of the availability (the owner's "off
// <date>"); it reports whether it was new.
func AddDayOff(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, day time.Time) (bool, error) {
	s, err := Get(ctx, tx, tenant)
	if err != nil {
		return false, err
	}
	av := s.Availability
	ds := day.In(Lagos).Format(time.DateOnly)
	if av.off(midnight(day)) {
		return false, nil
	}
	av.Off = append(av.Off, ds)
	if err := av.Check(); err != nil {
		return false, err
	}
	a, _ := json.Marshal(av)
	_, err = tx.Exec(ctx, `UPDATE storefronts SET availability = $2, updated_at = now() WHERE tenant_id = $1`, tenant, a)
	return err == nil, err
}

// SyncServices replaces the services copy with items.
func SyncServices(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, items []Item) error {
	s, err := Get(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM storefront_services WHERE storefront_id = $1`, s.ID); err != nil {
		return err
	}
	for i, it := range items {
		raw, _ := json.Marshal(it)
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_services (storefront_id, row_id, tenant_id, position, item) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (storefront_id, row_id) DO NOTHING`, s.ID, it.ID, tenant, i, raw); err != nil {
			return err
		}
	}
	return nil
}

// Services reads the services copy.
func Services(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Item, error) {
	rows, err := tx.Query(ctx, `SELECT v.item FROM storefront_services v JOIN storefronts s ON s.id = v.storefront_id
		WHERE s.tenant_id = $1 ORDER BY v.position`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) {
		var raw []byte
		var it Item
		if err := r.Scan(&raw); err != nil {
			return it, err
		}
		return it, json.Unmarshal(raw, &it)
	})
}

// ServiceItem finds one service in the copy.
func ServiceItem(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string) (Item, bool, error) {
	items, err := Services(ctx, tx, tenant)
	for _, it := range items {
		if it.ID == id {
			return it, true, err
		}
	}
	return Item{}, false, err
}
