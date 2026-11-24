package api

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/saved"
)

// A business's saved things as its workflows' inputs (saved objects S3;
// §6.4): when a run starts, the engine asks for those its workflow reads
// (saved.price_list, saved["price-list"]), and gets each as its fields,
// read now, so a change is used from the next run. Personal values (a
// phone, an email, an address, an account number) and a made-up kind's
// sensitive ones are sealed in the run's history; fields never to be shared
// are left out. A saved file is a reference to it (file:
// "saved-file:<id>"), never a link: a message step sending it gets a link
// made when it sends (saved_file_links.go; build plan H6). A person's own
// saved things are never read: the run is the business's. Each thing read
// is recorded against the run, with the steps that read it (saved_uses,
// 00641), the latest savedUsesKept for each thing.

// SavedResolver is the engine's reader of saved things for the runs of
// store: set on it wherever runs start (the API, schedules, the workers).
func SavedResolver(store *runtime.Store) runtime.SavedResolver {
	return (&Server{Store: store}).savedForRuns
}

// savedUsesKept is how many of the latest runs that read a saved thing
// are kept for its page (a run's purge takes its rows too, 00643).
const savedUsesKept = 200

// savedForRuns is the engine's runtime.SavedResolver: tx is in the
// business's scope.
func (s *Server) savedForRuns(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, read runtime.SavedRead, names []string) (map[string]any, []pii.Path, error) {
	// Each name as written, by the name it is saved under ("price_list" is
	// @price-list).
	byName := map[string][]string{}
	var want []string
	for _, n := range names {
		name, err := saved.Name(n)
		if err != nil {
			continue
		}
		if byName[name] == nil {
			want = append(want, name)
		}
		byName[name] = append(byName[name], n)
	}
	if len(want) == 0 {
		return nil, nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE tenant_id = $1 AND person_id IS NULL AND archived_at IS NULL AND name = ANY($2)`, tenant, want)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
	if err != nil {
		return nil, nil, err
	}
	o := savedOwner{tenant: tenant}
	out := map[string]any{}
	var paths []pii.Path
	for _, x := range list {
		fields, sealed, err := s.savedRunFields(ctx, tx, o, x)
		if err != nil {
			return nil, nil, err
		}
		for _, as := range byName[x.Name] {
			out[as] = fields
			for k, cat := range sealed {
				paths = append(paths, pii.Path{Segments: []string{"saved", as, k}, Category: cat})
			}
		}
		if read.Run == uuid.Nil {
			continue
		}
		var steps []string
		for _, as := range byName[x.Name] {
			for _, st := range read.Readers[as] {
				if !slices.Contains(steps, st) {
					steps = append(steps, st)
				}
			}
		}
		if steps == nil {
			steps = []string{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saved_uses (tenant_id, object_id, run_id, workflow_id, version, name, steps) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (object_id, run_id) DO NOTHING`, tenant, x.ID, read.Run, read.Workflow, read.Version, x.Name, steps[:min(len(steps), 200)]); err != nil {
			return nil, nil, err
		}
		// Only the latest are kept for the thing's page.
		if _, err := tx.Exec(ctx, `DELETE FROM saved_uses WHERE object_id = $1 AND read_at < (
			SELECT read_at FROM saved_uses WHERE object_id = $1 ORDER BY read_at DESC OFFSET $2 LIMIT 1)`, x.ID, savedUsesKept-1); err != nil {
			return nil, nil, err
		}
	}
	return out, paths, nil
}

// savedRunFields are a saved thing's fields as a run reads them, and the
// category each personal one is sealed under. Every field its kind has is
// there, "" when it holds nothing, so a step reading one never fails for
// want of it. What only those who may pay see in full (an account number,
// a made-up kind's sensitive or account fields) is not a run's: paying a
// saved account comes with payments, which read it on the server (P1).
// A made-up kind's field named as one of every thing's own (name, title,
// kind, note) is read as field_<key>.
func (s *Server) savedRunFields(ctx context.Context, tx pgx.Tx, o savedOwner, x savedRow) (map[string]any, map[string]string, error) {
	f := map[string]any{}
	sealed := map[string]string{}
	str := func(k string) string { v, _ := x.Details[k].(string); return v }
	put := func(k, v, category string) {
		f[k] = v
		if v != "" && category != "" {
			sealed[k] = category
		}
	}
	put("name", x.Name, "")
	// A person's title is their name.
	titleCat := ""
	if x.Kind == saved.KindPerson {
		titleCat = "name"
	}
	put("title", x.Title, titleCat)
	put("kind", x.Kind, "")
	put("note", str("note"), "other")
	switch x.Kind {
	case saved.KindBankAccount:
		put("bank_name", str("bank_name"), "")
		put("bank_code", str("bank_code"), "")
		name := ""
		if x.CheckedName != nil {
			name = *x.CheckedName
		}
		put("account_name", name, "name")
	case saved.KindPerson:
		put("phone", str("phone"), "phone")
		put("email", str("email"), "email")
		put("address", str("address"), "address")
		put("birthday", str("birthday"), "other")
	case saved.KindPlace:
		put("line", str("line"), "address")
		put("area", str("area"), "")
		put("landmark", str("landmark"), "address")
		put("phone", str("phone"), "phone")
		put("address", strings.Trim(strings.Join([]string{str("line"), str("area")}, ", "), ", "), "address")
	case saved.KindText:
		put("body", str("body"), "")
	case saved.KindProviderAccount:
		put("type", str("type"), "")
		put("provider", str("provider"), "")
		put("number", str("number"), "other")
		put("name_on_it", str("name_on_it"), "name")
	case saved.KindFile:
		// What it is, and a link to it a step can send (the file itself
		// never goes into the run).
		put("file_name", str("file_name"), "")
		put("file_type", str("file_type"), "")
		put("file", notify.FileRefPrefix+x.ID.String(), "")
	case saved.KindCustom:
		values, err := s.customValuesTx(ctx, tx, o, x)
		if errors.Is(err, pii.ErrErased) {
			values, err = map[string]string{}, nil
		}
		if err != nil {
			return nil, nil, err
		}
		for _, fl := range x.Fields {
			if fl.NeverShare || fl.Sensitive || fl.Type == "file" || fl.Type == "bank_account" {
				continue
			}
			key := fl.Key
			if savedRunOwnField[key] {
				key = "field_" + key
			}
			switch fl.Type {
			case "phone":
				put(key, values[fl.Key], "phone")
			case "email":
				put(key, values[fl.Key], "email")
			case "place":
				put(key, values[fl.Key], "address")
			default:
				put(key, values[fl.Key], "")
			}
		}
	}
	return f, sealed, nil
}

// savedRunOwnField are the fields every saved thing has in a run, and
// file, a saved file's own (a made-up kind's field so named is read as
// field_file, so only a saved file is ever a message's file).
var savedRunOwnField = map[string]bool{"name": true, "title": true, "kind": true, "note": true, "file": true}
