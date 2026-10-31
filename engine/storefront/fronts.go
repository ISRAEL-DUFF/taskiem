package storefront

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Fronts (docs/spec/fronts.md §5, decision 0059): a storefront has fronts,
// pages under it, each with blocks and actions. The first front, whose
// slug is empty, is the storefront's home page: today's page, with the
// catalogue and the services as its list blocks and the four built-in
// actions. Until fronts can be made (F3) the storefront's catalogue and
// services settings stay what the owner edits, and the first front's list
// blocks follow them (syncFirstFront).

// Front is one front.
type Front struct {
	ID       uuid.UUID `json:"id"`
	Slug     string    `json:"slug"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Tagline  string    `json:"tagline,omitempty"`
	Photo    string    `json:"photo,omitempty"`
	Position int       `json:"position"`
	Enabled  bool      `json:"enabled"`
}

// Block is one thing a front shows.
type Block struct {
	ID       uuid.UUID       `json:"id"`
	Position int             `json:"position"`
	Kind     string          `json:"kind"` // text, photos, list
	Config   json.RawMessage `json:"config"`
}

// ListConfig is a list block's: the table, its mapping and its role on the
// first front (catalogue or services) while those are the storefront's.
type ListConfig struct {
	Role    string     `json:"role,omitempty"`
	Table   *uuid.UUID `json:"table"`
	Columns Columns    `json:"columns"`
}

const frontCols = `id, slug, kind, name, COALESCE(tagline, ''), COALESCE(photo, ''), position, enabled`

func scanFront(r pgx.Row) (Front, error) {
	var f Front
	err := r.Scan(&f.ID, &f.Slug, &f.Kind, &f.Name, &f.Tagline, &f.Photo, &f.Position, &f.Enabled)
	return f, err
}

// Fronts are the storefront's fronts, the first first.
func Fronts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Front, error) {
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	if _, err := ensureFirstFront(ctx, tx, sf); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+frontCols+` FROM storefront_fronts WHERE storefront_id = $1 ORDER BY slug <> '', position, created_at, id`, sf.ID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Front, error) { return scanFront(r) })
}

// Blocks are a front's blocks in order.
func Blocks(ctx context.Context, tx pgx.Tx, front uuid.UUID) ([]Block, error) {
	rows, err := tx.Query(ctx, `SELECT id, position, kind, config FROM storefront_blocks WHERE front_id = $1 ORDER BY position, id`, front)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Block, error) {
		var b Block
		var cfg []byte
		err := r.Scan(&b.ID, &b.Position, &b.Kind, &cfg)
		b.Config = cfg
		return b, err
	})
}

// ensureFirstFront is the storefront's first front, made when it has none
// (a storefront made after the migration).
func ensureFirstFront(ctx context.Context, tx pgx.Tx, sf Storefront) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM storefront_fronts WHERE storefront_id = $1 AND slug = ''`, sf.ID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	name := sf.Draft.Name
	if name == "" {
		name = sf.Slug
	}
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	id = uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, position, enabled)
		SELECT $1, tenant_id, id, '', 'shop', $3, 0, true FROM storefronts WHERE id = $2
		ON CONFLICT (storefront_id, slug) DO NOTHING`, id, sf.ID, name); err != nil {
		return uuid.Nil, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM storefront_fronts WHERE storefront_id = $1 AND slug = ''`, sf.ID).Scan(&id)
	return id, err
}

// syncFirstFront keeps the first front following the storefront: its name,
// and its catalogue and services list blocks from the storefront's tables
// and mappings.
func syncFirstFront(ctx context.Context, tx pgx.Tx, sf Storefront) error {
	front, err := ensureFirstFront(ctx, tx, sf)
	if err != nil {
		return err
	}
	if sf.Draft.Name != "" {
		name := sf.Draft.Name
		if r := []rune(name); len(r) > 80 {
			name = string(r[:80])
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_fronts SET name = $2, updated_at = now() WHERE id = $1 AND name <> $2`, front, name); err != nil {
			return err
		}
	}
	for i, l := range []struct {
		role    string
		table   *uuid.UUID
		columns Columns
	}{{"catalogue", sf.CatalogueTable, sf.CatalogueColumns}, {"services", sf.ServicesTable, sf.ServicesColumns}} {
		if _, err := tx.Exec(ctx, `DELETE FROM storefront_blocks WHERE front_id = $1 AND kind = 'list' AND config->>'role' = $2`, front, l.role); err != nil {
			return err
		}
		if l.table == nil {
			continue
		}
		cfg, _ := json.Marshal(ListConfig{Role: l.role, Table: l.table, Columns: l.columns})
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_blocks (id, tenant_id, front_id, position, kind, config)
			SELECT $1, tenant_id, id, $3, 'list', $4 FROM storefront_fronts WHERE id = $2`, uuid.Must(uuid.NewV7()), front, i, cfg); err != nil {
			return err
		}
	}
	return nil
}
