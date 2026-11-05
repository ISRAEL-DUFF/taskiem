package moneylog

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Table is the kept data table the log is copied to.
const Table = "money_log"

// Columns are money_log's columns (spec 7.2): what the owner searches,
// exports and reads from templates. Amounts are kobo, as in the log.
var Columns = []datatables.Column{
	{Name: "entry_id", Title: "Entry", Type: "text", Required: true},
	{Name: "occurred_at", Title: "When it happened", Type: "datetime"},
	{Name: "recorded_at", Title: "When Taskiem recorded it", Type: "datetime", Required: true},
	{Name: "direction", Title: "In or out", Type: "text", Required: true},
	{Name: "kind", Title: "Kind", Type: "text", Required: true},
	{Name: "amount_kobo", Title: "Amount (kobo)", Type: "integer"},
	{Name: "fee_kobo", Title: "Fee (kobo)", Type: "integer"},
	{Name: "currency", Title: "Currency", Type: "text", Required: true},
	{Name: "status", Title: "Status", Type: "text", Required: true},
	{Name: "channel", Title: "Channel", Type: "text", Required: true},
	{Name: "reference", Title: "Reference", Type: "text", Required: true},
	{Name: "payer", Title: "Payer", Type: "text", Personal: true},
	{Name: "source", Title: "Source", Type: "text", Required: true},
	{Name: "note", Title: "Note", Type: "text"},
	{Name: "corrects", Title: "Corrects entry", Type: "text"},
}

// Rows writes and finds rows of a tenant's data tables project.
type Rows interface {
	Call(ctx context.Context, tenant uuid.UUID, action string, in map[string]any, key string) (map[string]any, error)
}

// Mirror copies the log's entries to each business's kept money_log
// table, once each: an entry is looked for by its id before it is
// inserted, the insert carries a key derived from the id, and the entry
// is marked copied only after.
type Mirror struct {
	Log    *Log
	Tables *datatables.Service
	Rows   Rows
	Logger *slog.Logger

	mu          sync.Mutex
	kept        map[uuid.UUID]bool
	keptMatches map[uuid.UUID]bool
}

func (m *Mirror) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Run copies every 30 seconds until ctx ends.
func (m *Mirror) Run(ctx context.Context) error {
	for {
		if err := m.Tick(ctx); err != nil && ctx.Err() == nil {
			m.log().Error("money log: copying to money_log", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(30 * time.Second):
		}
	}
}

// Tick copies what is waiting for every tenant. A tenant whose table
// cannot be made or written waits for the next tick; the others go on.
func (m *Mirror) Tick(ctx context.Context) error {
	if m.Tables == nil {
		return nil
	}
	rows, err := m.Log.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_money_unmirrored_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		err := m.Tenant(ctx, t)
		if errors.Is(err, datatables.ErrUnavailable) {
			// No data tables for this business: the log keeps its entries,
			// and copies them once there are.
			continue
		}
		if err != nil && ctx.Err() == nil {
			m.log().Warn("money log: copying to money_log", "tenant", t, "err", err)
		}
	}
	// The matches (T5), after the entries they name.
	rows, err = m.Log.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_money_unmirrored_payment_tenants()`)
	if err != nil {
		return err
	}
	if tenants, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
		return err
	}
	for _, t := range tenants {
		err := m.Matches(ctx, t)
		if errors.Is(err, datatables.ErrUnavailable) {
			continue
		}
		if err != nil && ctx.Err() == nil {
			m.log().Warn("money log: copying to money_matches", "tenant", t, "err", err)
		}
	}
	return nil
}

// MatchesTable is the kept data table matches are copied to (spec 8.3).
const MatchesTable = "money_matches"

// MatchColumns are money_matches' columns: one row per match and per
// undoing of one, never changed.
var MatchColumns = []datatables.Column{
	{Name: "match_id", Title: "Match", Type: "text", Required: true},
	{Name: "entry_id", Title: "Money log entry", Type: "text"},
	{Name: "target", Title: "Matched to", Type: "text", Required: true},
	{Name: "amount_kobo", Title: "Amount (kobo)", Type: "integer", Required: true},
	{Name: "how", Title: "How", Type: "text", Required: true},
	{Name: "by", Title: "By", Type: "text", Required: true},
	{Name: "reasons", Title: "Why", Type: "text"},
	{Name: "undoes", Title: "Undoes match", Type: "text"},
	{Name: "matched_at", Title: "When", Type: "datetime", Required: true},
}

// Matches copies up to 200 of a tenant's waiting matches to
// money_matches, each once, as entries are copied.
func (m *Mirror) Matches(ctx context.Context, tenant uuid.UUID) error {
	m.mu.Lock()
	done := m.keptMatches[tenant]
	m.mu.Unlock()
	if !done {
		if _, err := m.Tables.EnsureKept(ctx, tenant, MatchesTable, MatchesTable, MatchColumns); err != nil {
			return fmt.Errorf("the money_matches table: %w", err)
		}
		m.mu.Lock()
		if m.keptMatches == nil {
			m.keptMatches = map[uuid.UUID]bool{}
		}
		m.keptMatches[tenant] = true
		m.mu.Unlock()
	}
	type match struct {
		id    uuid.UUID
		row   map[string]any
		when  time.Time
		table string
	}
	var list []match
	err := db.InTenantTx(ctx, m.Log.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// Invoices' matches and storefront orders' (order:<reference>).
		rows, err := tx.Query(ctx, `SELECT * FROM (
				SELECT p.id, p.entry_id, 'invoice:' || i.number AS target, p.amount_kobo, p.how, p.by, p.reasons, p.undoes, p.created_at, 'customer_invoice_payments' AS tbl
				FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
				WHERE p.tenant_id = $1 AND p.mirrored_at IS NULL
				UNION ALL
				SELECT p.id, p.entry_id, 'order:' || o.reference, p.amount_kobo, p.how, p.by, p.reasons, p.undoes, p.created_at, 'storefront_order_payments'
				FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id
				WHERE p.tenant_id = $1 AND p.mirrored_at IS NULL) m
			ORDER BY created_at, id LIMIT 200`, tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (match, error) {
			var x match
			var entry, undoes *uuid.UUID
			var target, how, by string
			var amount int64
			var reasons []string
			if err := r.Scan(&x.id, &entry, &target, &amount, &how, &by, &reasons, &undoes, &x.when, &x.table); err != nil {
				return x, err
			}
			x.row = map[string]any{"match_id": x.id.String(), "target": target, "amount_kobo": amount, "how": how, "by": by,
				"matched_at": x.when.UTC().Format(time.RFC3339Nano)}
			if entry != nil {
				x.row["entry_id"] = entry.String()
			}
			if undoes != nil {
				x.row["undoes"] = undoes.String()
			}
			if len(reasons) > 0 {
				x.row["reasons"] = strings.Join(reasons, "; ")
			}
			return x, nil
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, x := range list {
		found, err := m.Rows.Call(ctx, tenant, "read_rows", map[string]any{"table": MatchesTable, "limit": 1, "select": "match_id",
			"filters": []any{map[string]any{"column": "match_id", "op": "eq", "value": x.id.String()}}}, "")
		if err != nil {
			return err
		}
		if rows, _ := found["rows"].([]any); len(rows) == 0 {
			if _, err := m.Rows.Call(ctx, tenant, "insert_rows", map[string]any{"table": MatchesTable, "rows": []any{x.row}, "return": "minimal"}, rowKey(x.id)); err != nil {
				return err
			}
		}
		if err := db.InTenantTx(ctx, m.Log.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			q := `UPDATE customer_invoice_payments SET mirrored_at = now() WHERE tenant_id = $1 AND id = $2 AND mirrored_at IS NULL`
			if x.table == "storefront_order_payments" {
				q = `UPDATE storefront_order_payments SET mirrored_at = now() WHERE tenant_id = $1 AND id = $2 AND mirrored_at IS NULL`
			}
			//sqlcheck:built q is one of two literals, chosen by the table name the query above gives
			_, err := tx.Exec(ctx, q, tenant, x.id)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// Tenant copies up to 200 of a tenant's waiting entries, oldest first.
func (m *Mirror) Tenant(ctx context.Context, tenant uuid.UUID) error {
	if err := m.ensure(ctx, tenant); err != nil {
		return err
	}
	type waiting struct {
		e     Entry
		payer map[string]any
	}
	var list []waiting
	err := db.InTenantTx(ctx, m.Log.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rs, err := tx.Query(ctx, `SELECT id, occurred_at, recorded_at, direction, kind, amount_kobo, fee_kobo, currency, status, channel, reference,
			COALESCE(event, ''), payer, source, COALESCE(note, ''), corrects FROM money_entries
			WHERE tenant_id = $1 AND mirrored_at IS NULL ORDER BY recorded_at, id LIMIT 200`, tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rs, func(r pgx.CollectableRow) (waiting, error) {
			var w waiting
			e := &w.e
			err := r.Scan(&e.ID, &e.OccurredAt, &e.RecordedAt, &e.Direction, &e.Kind, &e.AmountKobo, &e.FeeKobo, &e.Currency, &e.Status, &e.Channel,
				&e.Reference, &e.Event, &w.payer, &e.Source, &e.Note, &e.Corrects)
			return w, err
		})
		if err != nil {
			return err
		}
		for i := range list {
			if list[i].payer == nil {
				continue
			}
			v, err := m.Log.PII.OpenTx(ctx, tx, tenant, list[i].payer)
			if errors.Is(err, pii.ErrErased) {
				continue
			}
			if err != nil {
				return err
			}
			if p, ok := v.(map[string]any); ok {
				list[i].e.PayerName, _ = p["name"].(string)
				list[i].e.PayerAccount, _ = p["account"].(string)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, w := range list {
		if err := m.copy(ctx, tenant, w.e); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mirror) ensure(ctx context.Context, tenant uuid.UUID) error {
	m.mu.Lock()
	done := m.kept[tenant]
	m.mu.Unlock()
	if done {
		return nil
	}
	if _, err := m.Tables.EnsureKept(ctx, tenant, Table, Table, Columns); err != nil {
		return fmt.Errorf("the money_log table: %w", err)
	}
	m.mu.Lock()
	if m.kept == nil {
		m.kept = map[uuid.UUID]bool{}
	}
	m.kept[tenant] = true
	m.mu.Unlock()
	return nil
}

// copy writes one entry to money_log unless it is there, then marks it.
func (m *Mirror) copy(ctx context.Context, tenant uuid.UUID, e Entry) error {
	id := e.ID.String()
	found, err := m.Rows.Call(ctx, tenant, "read_rows", map[string]any{"table": Table, "limit": 1, "select": "entry_id",
		"filters": []any{map[string]any{"column": "entry_id", "op": "eq", "value": id}}}, "")
	if err != nil {
		return err
	}
	if rows, _ := found["rows"].([]any); len(rows) == 0 {
		if _, err := m.Rows.Call(ctx, tenant, "insert_rows", map[string]any{"table": Table, "rows": []any{Row(e)}, "return": "minimal"}, rowKey(e.ID)); err != nil {
			return err
		}
	}
	return db.InTenantTx(ctx, m.Log.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE money_entries SET mirrored_at = now() WHERE tenant_id = $1 AND id = $2 AND mirrored_at IS NULL`, tenant, e.ID)
		return err
	})
}

// Row is an entry as a money_log row.
func Row(e Entry) map[string]any {
	r := map[string]any{"entry_id": e.ID.String(), "recorded_at": e.RecordedAt.UTC().Format(time.RFC3339Nano), "direction": e.Direction,
		"kind": e.Kind, "currency": e.Currency, "status": e.Status, "channel": e.Channel, "reference": e.Reference, "source": e.Source}
	if e.OccurredAt != nil {
		r["occurred_at"] = e.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	if e.AmountKobo != nil {
		r["amount_kobo"] = *e.AmountKobo
	}
	if e.FeeKobo != nil {
		r["fee_kobo"] = *e.FeeKobo
	}
	payer := e.PayerName
	if e.PayerAccount != "" {
		if payer != "" {
			payer += ", "
		}
		payer += e.PayerAccount
	}
	if payer != "" {
		r["payer"] = payer
	}
	if e.Note != "" {
		r["note"] = e.Note
	}
	if e.Corrects != nil {
		r["corrects"] = e.Corrects.String()
	}
	return r
}

// rowKey is the insert's idempotency key: the same entry, the same key.
func rowKey(id uuid.UUID) string {
	h := sha256.Sum256([]byte("taskiem money_log " + id.String()))
	return "tsk-" + base64.RawURLEncoding.EncodeToString(h[:])[:22]
}

// XenibaseRows is Rows over the xenibase@1 connector on the data tables
// connection, as Taskiem's own writers call it (not through a workflow
// step, which refuses kept tables).
type XenibaseRows struct {
	Registry *connector.Registry
	Vault    *secrets.Vault
	Guard    *egress.Guard
}

// Call implements Rows.
func (x *XenibaseRows) Call(ctx context.Context, tenant uuid.UUID, action string, in map[string]any, key string) (map[string]any, error) {
	reg, err := x.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	conn, ok := reg.Get(datatables.Connector + "@1")
	if !ok {
		return nil, errors.New("moneylog: the Xenibase connector is not installed")
	}
	creds, err := x.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "moneylog.mirror"}),
		tenant, datatables.Environment, conn.Manifest.ID, datatables.ConnectionName)
	if err != nil {
		return nil, err
	}
	g := x.Guard
	if g == nil {
		g = &egress.Guard{}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:" + conn.Manifest.ID}, 30*time.Second)
	req := connector.Request{Input: in, Credentials: creds, HTTP: hc, Attempt: 1}
	if key != "" {
		req.IdempotencyKey, req.KeyFirstSent = key, time.Now()
	}
	resp, err := conn.Actions[action].Execute(ctx, req)
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}
