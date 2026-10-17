// Package datatables is the engine side of data tables (decision 0033,
// docs/data-tables.md): records a business keeps (customers, debtors,
// staff, stock) as Postgres tables in a Xenibase project, one project per
// organisation, reached by workflows through the ordinary pgdock@1
// connection named data_tables in prod.
//
// Taskiem keeps the table's definition (data_tables, data_table_columns)
// and the project's state (data_table_projects); the rows live only in
// Xenibase. This package provisions the project in the platform's
// Xenibase organisation on the first table (project.go), changes a
// table's schema through Xenibase's schema preview and apply, never SQL
// text written by Taskiem (schema.go), and renews the connection's token
// before it expires (renew.go). Rows are read and written by the API
// through the connector's own actions, so nothing here knows a row.
//
// It must not import engine/ai: the builder sees tables through the
// context the API gives it.
package datatables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

const (
	// ConnectionName is the pgdock@1 connection every data table is
	// reached through, in Environment.
	ConnectionName = "data_tables"
	// Environment is the environment the connection lives in.
	Environment = "prod"
	// Connector is the connector id of the connection.
	Connector = "pgdock"
	// ProvisionedBy marks the connection as Taskiem's own (the Connections
	// page shows it as "Data tables" and refuses edits).
	ProvisionedBy = "taskiem:data_tables"
	// TokenName is the name of the project-restricted token Taskiem mints
	// for the connection.
	TokenName = "taskiem-data-tables" //nolint:gosec // a token's name, not a token
	// RenewBefore is how long before the connection's token expires that
	// the renewal job replaces it.
	RenewBefore = 14 * 24 * time.Hour
	// RenewEvery is how often the renewal job runs.
	RenewEvery = 6 * time.Hour
	// callTimeout bounds one management API call.
	callTimeout = 20 * time.Second
)

// Platform is the platform's Xenibase: the organisation Taskiem makes
// projects in and the unrestricted token it does it with. The token is
// never stored in a tenant's vault, never logged and never used by a
// workflow.
type Platform struct {
	URL    string // TASKIEM_XENIBASE_URL
	Token  string // TASKIEM_XENIBASE_TOKEN: read, write and admin scopes
	Org    string // TASKIEM_XENIBASE_ORG
	Region string // TASKIEM_XENIBASE_REGION; empty takes the platform's home region
}

// Configured reports whether the platform can provision projects.
func (p Platform) Configured() bool {
	return strings.TrimSpace(p.URL) != "" && strings.TrimSpace(p.Token) != "" && strings.TrimSpace(p.Org) != ""
}

// Service provisions and keeps the organisation's project.
type Service struct {
	Pool   *pgxpool.Pool
	Vault  *secrets.Vault
	Egress *egress.Guard
	Logger *slog.Logger
	// Platform is the platform's Xenibase; unset, tables are unavailable
	// unless an organisation brought its own project.
	Platform Platform
	// ConnectorURL is the pgdock connector's default server
	// (TASKIEM_PGDOCK_URL): the Xenibase a connection naming no base_url
	// reaches; empty takes Platform.URL.
	ConnectorURL string
	// Now is the clock (tests).
	Now func() time.Time
	// PollInterval is how often an operation is polled (default 2 s).
	PollInterval time.Duration
	// ProvisionTimeout bounds waiting for one operation (default 5 min).
	ProvisionTimeout time.Duration
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) guard() *egress.Guard {
	if s.Egress != nil {
		return s.Egress
	}
	return &egress.Guard{Logger: s.log()}
}

// Available reports whether the platform can provision a project for an
// organisation that has none.
func (s *Service) Available() bool { return s != nil && s.Platform.Configured() }

// Errors the API maps to answers.
var (
	// ErrUnavailable: no platform Xenibase is configured and the
	// organisation brought no project (503 tables_unavailable).
	ErrUnavailable = errors.New("data tables are not available on this platform: no Xenibase is configured")
	// ErrProvisioning: another request is making the project now (409).
	ErrProvisioning = errors.New("the organisation's data project is being created; try again in a minute")
	// ErrProjectFailed: provisioning failed in a way a retry will not fix.
	ErrProjectFailed = errors.New("the organisation's data project could not be created")
	// ErrNotFound: no such table or column.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: a definition or a value that does not fit.
	ErrInvalid = errors.New("invalid")
	// ErrConflict: the state does not allow it.
	ErrConflict = errors.New("conflict")
)

// --- types and names ---

// Types are the column types a table may declare, in display order, with
// the Postgres type each becomes.
var Types = []struct{ Name, PG string }{
	{"text", "text"}, {"integer", "bigint"}, {"number", "numeric"}, {"boolean", "boolean"},
	{"date", "date"}, {"datetime", "timestamptz"}, {"phone", "text"}, {"email", "text"}, {"json", "jsonb"},
}

// PGType is the Postgres type of a column type ("" for an unknown one).
func PGType(t string) string {
	for _, x := range Types {
		if x.Name == t {
			return x.PG
		}
	}
	return ""
}

// PersonalByDefault reports whether a type holds personal data unless
// the table says otherwise.
func PersonalByDefault(t string) bool { return t == "phone" || t == "email" }

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// reservedNames are column names every table already has, or that
// Xenibase and Postgres keep for themselves.
var reservedNames = []string{"id", "created_at", "updated_at"}

// CheckName checks a table or column name: lower-case letters, digits
// and underscores, starting with a letter, at most 63 characters, not a
// reserved name and not one starting xb_ or pg_.
func CheckName(what, name string) error {
	switch {
	case !nameRe.MatchString(name):
		return fmt.Errorf("%w: %s name %q must be lower-case letters, digits and underscores, starting with a letter (at most 63 characters)", ErrInvalid, what, name)
	case slices.Contains(reservedNames, name), strings.HasPrefix(name, "xb_"), strings.HasPrefix(name, "pg_"):
		return fmt.Errorf("%w: %s name %q is reserved", ErrInvalid, what, name)
	}
	return nil
}

// Column is one declared column.
type Column struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Personal bool   `json:"personal"`
}

// CheckColumn checks a column's definition and fills its defaults
// (personal for phone and email).
func CheckColumn(c *Column) error {
	if err := CheckName("column", c.Name); err != nil {
		return err
	}
	if PGType(c.Type) == "" {
		names := make([]string, len(Types))
		for i, t := range Types {
			names[i] = t.Name
		}
		return fmt.Errorf("%w: column %q: type %q is not one of %s", ErrInvalid, c.Name, c.Type, strings.Join(names, ", "))
	}
	if len(c.Title) > 120 {
		return fmt.Errorf("%w: column %q: the title is at most 120 characters", ErrInvalid, c.Name)
	}
	return nil
}

// CheckColumns checks a table's columns together: at least one, no two
// with the same name, at most limit (0: no limit).
func CheckColumns(cols []Column, limit int) error {
	if len(cols) == 0 {
		return fmt.Errorf("%w: a table needs at least one column", ErrInvalid)
	}
	if limit > 0 && len(cols) > limit {
		return fmt.Errorf("%w: a table may have at most %d columns (max_data_table_columns)", ErrInvalid, limit)
	}
	seen := map[string]bool{}
	for i := range cols {
		if err := CheckColumn(&cols[i]); err != nil {
			return err
		}
		if seen[cols[i].Name] {
			return fmt.Errorf("%w: column %q is listed twice", ErrInvalid, cols[i].Name)
		}
		seen[cols[i].Name] = true
	}
	return nil
}

// --- values ---

var (
	phoneRe = regexp.MustCompile(`^\+?[0-9]{6,15}$`)
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// CheckValue checks one value against its column's type and returns it
// as the data API should get it: text as a string, integer as an int64,
// number as a float64, boolean as a bool, date as YYYY-MM-DD, datetime as
// RFC 3339, phone as digits with an optional leading +, email as given,
// json as any JSON value. Strings are accepted for every type (CSV cells
// come as strings); an empty string is null for every type but text.
func CheckValue(c Column, v any) (any, error) {
	if s, ok := v.(string); ok && c.Type != "text" && c.Type != "json" {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		v = s
	}
	if v == nil {
		return nil, nil
	}
	bad := func(want string) error {
		return fmt.Errorf("%w: column %q takes %s", ErrInvalid, c.Name, want)
	}
	switch c.Type {
	case "text":
		s, ok := v.(string)
		if !ok {
			return nil, bad("text")
		}
		return s, nil
	case "integer":
		switch x := v.(type) {
		case float64:
			if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
				return nil, bad("a whole number")
			}
			return int64(x), nil
		case int:
			return int64(x), nil
		case int64:
			return x, nil
		case json.Number:
			n, err := x.Int64()
			if err != nil {
				return nil, bad("a whole number")
			}
			return n, nil
		case string:
			n, err := strconv.ParseInt(x, 10, 64)
			if err != nil {
				return nil, bad("a whole number")
			}
			return n, nil
		}
		return nil, bad("a whole number")
	case "number":
		switch x := v.(type) {
		case float64:
			return x, nil
		case int:
			return float64(x), nil
		case int64:
			return float64(x), nil
		case json.Number:
			f, err := x.Float64()
			if err != nil {
				return nil, bad("a number")
			}
			return f, nil
		case string:
			f, err := strconv.ParseFloat(x, 64)
			if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, bad("a number")
			}
			return f, nil
		}
		return nil, bad("a number")
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			switch strings.ToLower(x) {
			case "true", "yes", "1", "y":
				return true, nil
			case "false", "no", "0", "n":
				return false, nil
			}
		}
		return nil, bad("true or false")
	case "date":
		s, ok := v.(string)
		if !ok || !dateRe.MatchString(s) {
			return nil, bad("a date as YYYY-MM-DD")
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, bad("a date as YYYY-MM-DD")
		}
		return s, nil
	case "datetime":
		s, ok := v.(string)
		if !ok {
			return nil, bad("a time as RFC 3339 (2026-10-17T09:30:00+01:00)")
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			if d, err2 := time.Parse("2006-01-02 15:04:05", s); err2 == nil {
				t = d.UTC()
			} else {
				return nil, bad("a time as RFC 3339 (2026-10-17T09:30:00+01:00)")
			}
		}
		return t.Format(time.RFC3339), nil
	case "phone":
		s, ok := v.(string)
		if !ok {
			if f, isNum := v.(float64); isNum && f == math.Trunc(f) {
				s, ok = strconv.FormatFloat(f, 'f', -1, 64), true
			}
		}
		if !ok {
			return nil, bad("a phone number")
		}
		s = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(s)
		if !phoneRe.MatchString(s) {
			return nil, bad("a phone number (digits, with the country code: 2348012345678)")
		}
		return s, nil
	case "email":
		s, ok := v.(string)
		if !ok {
			return nil, bad("an email address")
		}
		s = strings.TrimSpace(s)
		if a, err := mail.ParseAddress(s); err != nil || a.Name != "" || a.Address != s {
			return nil, bad("an email address")
		}
		return s, nil
	case "json":
		if s, ok := v.(string); ok {
			t := strings.TrimSpace(s)
			if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") || t == "null" || t == "true" || t == "false" {
				var parsed any
				if err := json.Unmarshal([]byte(t), &parsed); err == nil {
					return parsed, nil
				}
			}
			if f, err := strconv.ParseFloat(t, 64); err == nil && !strings.HasPrefix(t, "+") {
				return f, nil
			}
			return s, nil
		}
		return v, nil
	}
	return nil, bad(c.Type)
}

// CheckRow checks a row's values against the table's columns and returns
// the row as the data API should get it. Unknown columns are refused;
// id, created_at and updated_at are refused unless allowID (an import
// upserting on id) says otherwise, and the timestamps are never written.
// With insert, every required column must be present and not null.
func CheckRow(cols []Column, row map[string]any, insert, allowID bool) (map[string]any, error) {
	out := make(map[string]any, len(row))
	byName := map[string]Column{}
	for _, c := range cols {
		byName[c.Name] = c
	}
	for k, v := range row {
		c, ok := byName[k]
		if !ok {
			switch {
			case k == "id" && allowID:
				n, err := CheckValue(Column{Name: "id", Type: "integer"}, v)
				if err != nil {
					return nil, err
				}
				if n != nil {
					out["id"] = n
				}
				continue
			case k == "created_at" || k == "updated_at":
				continue // kept by Postgres
			}
			return nil, fmt.Errorf("%w: the table has no column %q", ErrInvalid, k)
		}
		x, err := CheckValue(c, v)
		if err != nil {
			return nil, err
		}
		out[k] = x
	}
	for _, c := range cols {
		v, present := out[c.Name]
		if c.Required && (insert && !present || present && v == nil) {
			return nil, fmt.Errorf("%w: column %q is required", ErrInvalid, c.Name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the row has no values", ErrInvalid)
	}
	return out, nil
}

// Mask replaces the values of personal columns in a row (for logs and
// audit details; the API shows rows to table.read holders as they are).
func Mask(cols []Column, row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	personal := map[string]bool{}
	for _, c := range cols {
		personal[c.Name] = c.Personal
	}
	for k, v := range row {
		if personal[k] && v != nil {
			out[k] = "[personal]"
		} else {
			out[k] = v
		}
	}
	return out
}

// --- records ---

// Table is a data table as Taskiem records it.
type Table struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Columns     []Column  `json:"columns"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListTables reads the tenant's tables with their columns. tx must have
// the tenant in scope.
func ListTables(ctx context.Context, tx pgx.Tx) ([]Table, error) {
	rows, err := tx.Query(ctx, `SELECT id, name, title, description, created_by, created_at, updated_at FROM data_tables ORDER BY name`)
	if err != nil {
		return nil, err
	}
	tables, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Table, error) {
		var t Table
		return t, r.Scan(&t.ID, &t.Name, &t.Title, &t.Description, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]int{}
	for i := range tables {
		tables[i].Columns = []Column{}
		byID[tables[i].ID] = i
	}
	crows, err := tx.Query(ctx, `SELECT table_id, name, title, type, required, personal FROM data_table_columns ORDER BY table_id, position`)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var id uuid.UUID
		var c Column
		if err := crows.Scan(&id, &c.Name, &c.Title, &c.Type, &c.Required, &c.Personal); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			tables[i].Columns = append(tables[i].Columns, c)
		}
	}
	return tables, crows.Err()
}

// GetTable reads one table with its columns.
func GetTable(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Table, error) {
	var t Table
	err := tx.QueryRow(ctx, `SELECT id, name, title, description, created_by, created_at, updated_at FROM data_tables WHERE id = $1`, id).
		Scan(&t.ID, &t.Name, &t.Title, &t.Description, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, fmt.Errorf("table %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return t, err
	}
	rows, err := tx.Query(ctx, `SELECT name, title, type, required, personal FROM data_table_columns WHERE table_id = $1 ORDER BY position`, id)
	if err != nil {
		return t, err
	}
	t.Columns, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Column, error) {
		var c Column
		return c, r.Scan(&c.Name, &c.Title, &c.Type, &c.Required, &c.Personal)
	})
	if t.Columns == nil {
		t.Columns = []Column{}
	}
	return t, err
}

// InsertTable records a table and its columns. The caller checked them.
func InsertTable(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, t *Table) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.Must(uuid.NewV7())
	}
	err := tx.QueryRow(ctx, `INSERT INTO data_tables (id, tenant_id, name, title, description, created_by) VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at, updated_at`,
		t.ID, tenant, t.Name, t.Title, t.Description, t.CreatedBy).Scan(&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return err
	}
	for i, c := range t.Columns {
		if _, err := tx.Exec(ctx, `INSERT INTO data_table_columns (tenant_id, table_id, name, title, type, required, personal, position) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			tenant, t.ID, c.Name, c.Title, c.Type, c.Required, c.Personal, i); err != nil {
			return err
		}
	}
	return nil
}

// InsertColumn records a column after the table's others.
func InsertColumn(ctx context.Context, tx pgx.Tx, tenant, table uuid.UUID, c Column) error {
	_, err := tx.Exec(ctx, `INSERT INTO data_table_columns (tenant_id, table_id, name, title, type, required, personal, position)
		SELECT $1, $2, $3, $4, $5, $6, $7, COALESCE(max(position), -1) + 1 FROM data_table_columns WHERE table_id = $2`,
		tenant, table, c.Name, c.Title, c.Type, c.Required, c.Personal)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE data_tables SET updated_at = now() WHERE id = $1`, table)
	return err
}

// DeleteColumn forgets a column.
func DeleteColumn(ctx context.Context, tx pgx.Tx, table uuid.UUID, name string) error {
	tag, err := tx.Exec(ctx, `DELETE FROM data_table_columns WHERE table_id = $1 AND name = $2`, table, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("column %s: %w", name, ErrNotFound)
	}
	_, err = tx.Exec(ctx, `UPDATE data_tables SET updated_at = now() WHERE id = $1`, table)
	return err
}

// DeleteTable forgets a table and its columns.
func DeleteTable(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM data_tables WHERE id = $1`, id)
	return err
}

// UpdateTable changes a table's title and description.
func UpdateTable(ctx context.Context, tx pgx.Tx, id uuid.UUID, title, description *string) error {
	tag, err := tx.Exec(ctx, `UPDATE data_tables SET title = COALESCE($2, title), description = COALESCE($3, description), updated_at = now() WHERE id = $1`, id, title, description)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("table %s: %w", id, ErrNotFound)
	}
	return nil
}

// CountTables is how many tables the tenant has.
func CountTables(ctx context.Context, tx pgx.Tx) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM data_tables`).Scan(&n)
	return n, err
}
