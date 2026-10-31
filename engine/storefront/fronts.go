package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	Role  string     `json:"role,omitempty"`
	Table *uuid.UUID `json:"table"`
	// TableName is the table's name, which its copy is kept by (§5.2).
	TableName string  `json:"table_name,omitempty"`
	Columns   Columns `json:"columns"`
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
		var tableName string
		if err := tx.QueryRow(ctx, `SELECT name FROM data_tables WHERE id = $1`, *l.table).Scan(&tableName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		cfg, _ := json.Marshal(ListConfig{Role: l.role, Table: l.table, TableName: tableName, Columns: l.columns})
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_blocks (id, tenant_id, front_id, position, kind, config)
			SELECT $1, tenant_id, id, $3, 'list', $4 FROM storefront_fronts WHERE id = $2`, uuid.Must(uuid.NewV7()), front, i, cfg); err != nil {
			return err
		}
	}
	return nil
}

// PublicFront is a front as its published snapshot holds it (§5.1): what
// the public host renders, the actions with their ids so a submission
// names the action it is for.
type PublicFront struct {
	ID      uuid.UUID      `json:"id"`
	Slug    string         `json:"slug"`
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Tagline string         `json:"tagline,omitempty"`
	Photo   string         `json:"photo,omitempty"`
	Blocks  []PublicBlock  `json:"blocks,omitempty"`
	Actions []PublicAction `json:"actions,omitempty"`
}

// PublicBlock is a published block.
type PublicBlock struct {
	Kind   string          `json:"kind"`
	Config json.RawMessage `json:"config"`
}

// Home is the snapshot's first front: the storefront's home page. A
// snapshot published before fronts (none left after migration 00281, but
// a public host may read one mid-deploy) reads as one front with the
// snapshot's actions, without ids.
func (m Model) Home() PublicFront {
	for _, f := range m.Fronts {
		if f.Slug == "" {
			return f
		}
	}
	return PublicFront{Kind: "shop", Name: m.Card.Name, Actions: m.Actions}
}

// publicFronts are the fronts a publish snapshots: the enabled ones, each
// with its blocks and its actions (those of actions, the enabled built-in
// ones, given their ids and slugs on the first front).
func publicFronts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actions []PublicAction) ([]PublicFront, error) {
	fronts, err := Fronts(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	ids := map[string]PublicAction{}
	rows, err := tx.Query(ctx, `SELECT kind, id, slug, form FROM storefront_actions WHERE storefront_id = $1 AND builtin`, sf.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, slug string
		var id uuid.UUID
		var form []byte
		if err := rows.Scan(&kind, &id, &slug, &form); err != nil {
			rows.Close()
			return nil, err
		}
		ids[kind] = PublicAction{ID: &id, Slug: slug, Form: EffectiveForm(form, kind)}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []PublicFront
	for _, f := range fronts {
		if !f.Enabled {
			continue
		}
		pf := PublicFront{ID: f.ID, Slug: f.Slug, Kind: f.Kind, Name: f.Name, Tagline: f.Tagline, Photo: f.Photo}
		blocks, err := Blocks(ctx, tx, f.ID)
		if err != nil {
			return nil, err
		}
		for _, b := range blocks {
			pf.Blocks = append(pf.Blocks, PublicBlock{Kind: b.Kind, Config: b.Config})
		}
		if f.Slug == "" {
			for _, a := range actions {
				if x, ok := ids[a.Kind]; ok {
					a.ID, a.Slug, a.Form = x.ID, x.Slug, x.Form
				}
				pf.Actions = append(pf.Actions, a)
			}
		}
		// The front's other actions that are on (fronts F2), after the
		// built-in ones.
		others, err := otherActions(ctx, tx, f.ID)
		if err != nil {
			return nil, err
		}
		pf.Actions = append(pf.Actions, others...)
		out = append(out, pf)
	}
	return out, nil
}

// otherActions are a front's actions that are on but not built in, for a
// snapshot, in their order.
func otherActions(ctx context.Context, tx pgx.Tx, front uuid.UUID) ([]PublicAction, error) {
	rows, err := tx.Query(ctx, `SELECT id, slug, kind, label, payment, options, form FROM storefront_actions
		WHERE front_id = $1 AND NOT builtin AND enabled ORDER BY position, slug`, front)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PublicAction, error) {
		var a Action
		var id uuid.UUID
		var opts, form []byte
		if err := r.Scan(&id, &a.Slug, &a.Kind, &a.Label, &a.Payment, &opts, &form); err != nil {
			return PublicAction{}, err
		}
		_ = json.Unmarshal(opts, &a.Options)
		pa := publicAction(a)
		pa.ID, pa.Slug, pa.Form = &id, a.Slug, EffectiveForm(form, a.Kind)
		return pa, nil
	})
}

// ReservedFrontSlugs are the addresses the public host routes itself
// (§5.1): a front may not take one. TestFrontSlugsMissTheRoutes walks the
// router's cases.
var ReservedFrontSlugs = []string{"a", "o", "f", "r", "pay", "report", "embed", "static", "qr", "api", "admin", "www", "status"}

var frontSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// FrontSlug is the address a name gives: lower-case letters and digits,
// hyphens between words, up to 40.
func FrontSlug(name string) string {
	s := strings.Trim(frontSlugChars.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if r := []rune(s); len(r) > 40 {
		s = strings.TrimRight(string(r[:40]), "-")
	}
	return s
}

// CheckFrontSlug refuses an address a front cannot have.
func CheckFrontSlug(slug string) error {
	switch {
	case slug == "":
		return fmt.Errorf("%w: a front's address is letters and digits, with hyphens between words", ErrInvalid)
	case !frontSlug.MatchString(slug):
		return fmt.Errorf("%w: a front's address is lower-case letters and digits, with hyphens between words, up to 40", ErrInvalid)
	case slices.Contains(ReservedFrontSlugs, slug):
		return fmt.Errorf("%w: %q is an address the page uses itself: choose another", ErrInvalid, slug)
	}
	return nil
}

var frontSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// NewFront is a front to make: its name and kind, and the table the kind
// made for it (nil for a kind with none, or the API's own choice).
type NewFront struct {
	Name      string
	Kind      string
	Table     *uuid.UUID
	TableName string
}

// CreateFront makes a front of a kind (§6), off, at the end: its address
// from its name (the next free one), a list block of the table made for
// it, and the kind's actions, off, each with the kind's options.
func CreateFront(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in NewFront) (Front, error) {
	k, ok := FrontKindOf(in.Kind)
	if !ok {
		return Front{}, fmt.Errorf("%w: no kind of front %q", ErrInvalid, in.Kind)
	}
	name := strings.TrimSpace(in.Name)
	if r := []rune(name); len(r) == 0 || len(r) > 80 || hasLink(name) {
		return Front{}, fmt.Errorf("%w: a front's name is 1 to 80 characters, with no links", ErrInvalid)
	}
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return Front{}, err
	}
	if _, err := ensureFirstFront(ctx, tx, sf); err != nil {
		return Front{}, err
	}
	var taken []string
	rows, err := tx.Query(ctx, `SELECT slug FROM storefront_fronts WHERE storefront_id = $1`, sf.ID)
	if err != nil {
		return Front{}, err
	}
	if taken, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return Front{}, err
	}
	base := FrontSlug(name)
	if CheckFrontSlug(base) != nil {
		base = "page"
	}
	slug := base
	for n := 2; slices.Contains(taken, slug); n++ {
		slug = fmt.Sprintf("%s-%d", strings.TrimRight(string([]rune(base)[:min(len([]rune(base)), 36)]), "-"), n)
	}
	id := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, position, enabled)
		SELECT $1, $2, $3, $4, $5, $6, COALESCE(max(position), 0) + 1, false FROM storefront_fronts WHERE storefront_id = $3`,
		id, tenant, sf.ID, slug, k.Name, name); err != nil {
		return Front{}, err
	}
	if in.Table != nil {
		cfg, _ := json.Marshal(ListConfig{Table: in.Table, TableName: in.TableName, Columns: k.Mapping})
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_blocks (id, tenant_id, front_id, position, kind, config) VALUES ($1, $2, $3, 0, 'list', $4)`,
			uuid.Must(uuid.NewV7()), tenant, id, cfg); err != nil {
			return Front{}, err
		}
	}
	for i, a := range k.Actions {
		payment := "none"
		if takesPayment(a.Kind) {
			payment = "after_accept"
		}
		opts, _ := json.Marshal(a.Options)
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_actions (id, storefront_id, front_id, tenant_id, kind, slug, builtin, enabled, label, payment, options, position)
			VALUES ($1, $2, $3, $4, $5, $5, false, false, $6, $7, $8, $9)`, uuid.Must(uuid.NewV7()), sf.ID, id, tenant, a.Kind, a.Label, payment, opts, i+1); err != nil {
			return Front{}, err
		}
	}
	return scanFront(tx.QueryRow(ctx, `SELECT `+frontCols+` FROM storefront_fronts WHERE id = $1`, id))
}

// FrontPatch is what a change to a front sets; nil leaves it.
type FrontPatch struct {
	Name     *string `json:"name"`
	Tagline  *string `json:"tagline"`
	Slug     *string `json:"slug"`
	Enabled  *bool   `json:"enabled"`
	Position *int    `json:"position"`
}

// UpdateFront changes a front. The first front, the home page, keeps its
// empty address and stays on.
func UpdateFront(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, p FrontPatch) (Front, error) {
	f, err := scanFront(tx.QueryRow(ctx, `SELECT `+frontCols+` FROM storefront_fronts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Front{}, ErrNotFound
	}
	if err != nil {
		return Front{}, err
	}
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if r := []rune(name); len(r) == 0 || len(r) > 80 || hasLink(name) {
			return Front{}, fmt.Errorf("%w: a front's name is 1 to 80 characters, with no links", ErrInvalid)
		}
		f.Name = name
	}
	if p.Tagline != nil {
		t := strings.TrimSpace(*p.Tagline)
		if len([]rune(t)) > 120 || hasLink(t) {
			return Front{}, fmt.Errorf("%w: a front's line is up to 120 characters, with no links", ErrInvalid)
		}
		f.Tagline = t
	}
	if p.Slug != nil && *p.Slug != f.Slug {
		if f.Slug == "" {
			return Front{}, fmt.Errorf("%w: the home page keeps its address", ErrInvalid)
		}
		if err := CheckFrontSlug(*p.Slug); err != nil {
			return Front{}, err
		}
		f.Slug = *p.Slug
	}
	if p.Enabled != nil {
		if f.Slug == "" && !*p.Enabled {
			return Front{}, fmt.Errorf("%w: the home page is always on; close the page instead", ErrInvalid)
		}
		f.Enabled = *p.Enabled
	}
	if p.Position != nil && f.Slug != "" {
		f.Position = max(*p.Position, 1)
	}
	_, err = tx.Exec(ctx, `UPDATE storefront_fronts SET name = $2, tagline = NULLIF($3, ''), slug = $4, enabled = $5, position = $6, updated_at = now() WHERE id = $1`,
		id, f.Name, f.Tagline, f.Slug, f.Enabled, f.Position)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Front{}, fmt.Errorf("%w: another front has the address %q", ErrInvalid, f.Slug)
	}
	return f, err
}

// DeleteFront removes a front that has taken nothing: one with
// submissions, or an action a workflow is deployed for, is turned off
// instead. The home page stays.
func DeleteFront(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	var slug string
	var used bool
	err := tx.QueryRow(ctx, `SELECT slug, EXISTS (SELECT 1 FROM storefront_orders o WHERE o.front_id = f.id)
		OR EXISTS (SELECT 1 FROM triggers t JOIN storefront_actions a ON a.id = t.storefront_action_id WHERE a.front_id = f.id)
		FROM storefront_fronts f WHERE f.id = $1`, id).Scan(&slug, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	switch {
	case slug == "":
		return fmt.Errorf("%w: the home page stays", ErrInvalid)
	case used:
		return fmt.Errorf("%w: this front has orders or a workflow: turn it off instead", ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM storefront_actions WHERE front_id = $1`, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM storefront_fronts WHERE id = $1`, id)
	return err
}

// Lists are the list blocks the page copies besides the catalogue (§5.2):
// the other fronts' lists, one per table (a table shown twice is copied
// once), the catalogue's table and the services left to their own copies.
func Lists(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]ListConfig, error) {
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	var catalogue string
	if sf.CatalogueTable != nil {
		if err := tx.QueryRow(ctx, `SELECT name FROM data_tables WHERE id = $1`, *sf.CatalogueTable).Scan(&catalogue); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT b.config FROM storefront_blocks b JOIN storefront_fronts f ON f.id = b.front_id
		WHERE f.storefront_id = $1 AND b.kind = 'list' AND COALESCE(b.config->>'role', '') = '' ORDER BY f.position, b.position`, sf.ID)
	if err != nil {
		return nil, err
	}
	raws, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, err
	}
	var out []ListConfig
	seen := map[string]bool{catalogue: catalogue != ""}
	for _, raw := range raws {
		var lc ListConfig
		if json.Unmarshal(raw, &lc) != nil || lc.Table == nil || lc.TableName == "" || seen[lc.TableName] {
			continue
		}
		seen[lc.TableName] = true
		out = append(out, lc)
	}
	return out, nil
}

// SyncList replaces a list's copy with items.
func SyncList(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, table string, items []Item) error {
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM storefront_catalogue WHERE storefront_id = $1 AND table_name = $2`, sf.ID, table); err != nil {
		return err
	}
	for i, it := range items {
		raw, _ := json.Marshal(it)
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (storefront_id, table_name, row_id) DO NOTHING`, sf.ID, table, it.ID, tenant, i, raw); err != nil {
			return err
		}
	}
	return nil
}

// PruneLists removes the copies of tables no list shows any more, keeping
// the catalogue's and those named in keep.
func PruneLists(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, keep []string) error {
	sf, err := Get(ctx, tx, tenant)
	if err != nil {
		return err
	}
	var catalogue string
	if sf.CatalogueTable != nil {
		if err := tx.QueryRow(ctx, `SELECT name FROM data_tables WHERE id = $1`, *sf.CatalogueTable).Scan(&catalogue); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM storefront_catalogue WHERE storefront_id = $1 AND table_name <> $2 AND NOT (table_name = ANY ($3))`, sf.ID, catalogue, keep)
	return err
}

// ListItems reads a list's copy, for the owner's preview.
func ListItems(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, table string) ([]Item, error) {
	rows, err := tx.Query(ctx, `SELECT c.item FROM storefront_catalogue c JOIN storefronts s ON s.id = c.storefront_id
		WHERE s.tenant_id = $1 AND c.table_name = $2 ORDER BY c.position`, tenant, table)
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

// ListTables are the tables of a published front's lists, in order.
func (f PublicFront) ListTables() []string {
	var out []string
	for _, b := range f.Blocks {
		var lc ListConfig
		if b.Kind == "list" && json.Unmarshal(b.Config, &lc) == nil && lc.TableName != "" && !slices.Contains(out, lc.TableName) {
			out = append(out, lc.TableName)
		}
	}
	return out
}

// ListTable is a published front's first list's table ("" for none): what
// its page and its basket show.
func (f PublicFront) ListTable() string {
	if t := f.ListTables(); len(t) > 0 {
		return t[0]
	}
	return ""
}
