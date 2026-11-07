package api

import (
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// Per-table readers (decision 0065, docs/data-tables.md#who-may-read-a-table).
// A table naming readers has its rows read and changed only by members
// holding one of those roles, and the owner, on top of table.read and
// table.edit: everywhere a person reaches rows (the rows API, the export
// and import, chat's find, add, change, delete and questions), which all
// go through tableRowsFor. A restricted table's name and columns are
// still listed to every table.read holder, with its readers, so people
// know it is there and whom to ask; its row count is not. Workflows
// reach the rows through the data_tables connection as before.
//
// Readers change with role.manage (they say what a role grants, as a
// custom role's permissions do; member.manage decides who holds a role),
// on top of table.read, and only by someone who may read the table now,
// so setting them never lets the setter into rows they could not read.

// tableRoles are the membership roles a principal reads tables with: a
// member's own, and an API key's owner's.
func (p *Principal) tableRoles() []string {
	if p == nil {
		return nil
	}
	if p.KeyID != uuid.Nil {
		return p.KeyOwnerRoles
	}
	return p.Roles
}

// readsTable reports whether the principal may read (and, with
// table.edit, change) the table's rows.
func (p *Principal) readsTable(t datatables.Table) bool {
	return datatables.MayRead(t, p.tableRoles())
}

// checkTableReaders refuses a person who holds none of a restricted
// table's readers (datatables.ErrRestricted). The context's principal is
// the person: a request's, or the chat's (waTablesCtx). Without one (a
// public storefront page, a background job) or for a workflow run
// (link@1), the caller is Taskiem itself, which readers do not limit.
func checkTableReaders(ctx context.Context, t datatables.Table) error {
	p := principalFrom(ctx)
	if p == nil || p.Run != uuid.Nil {
		return nil
	}
	return datatables.CheckRead(t, p.tableRoles())
}

type readersReq struct {
	Readers []string `json:"readers"`
}

// putTableReaders sets a table's readers: a list of one or more roles.
func (s *Server) putTableReaders(w http.ResponseWriter, r *http.Request) {
	var req readersReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	readers, err := datatables.CheckReaders(req.Readers)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	s.setTableReaders(w, r, readers)
}

// deleteTableReaders clears a table's readers: every table.read holder
// reads it again.
func (s *Server) deleteTableReaders(w http.ResponseWriter, r *http.Request) {
	s.setTableReaders(w, r, nil)
}

func (s *Server) setTableReaders(w http.ResponseWriter, r *http.Request, readers []string) {
	t, err := s.loadTable(r)
	if err == nil {
		err = datatables.Writable(t)
	}
	if err == nil {
		err = checkTableReaders(r.Context(), t)
	}
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	before := t.Readers
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := datatables.SetReaders(r.Context(), tx, t.ID, readers); err != nil {
			return err
		}
		action, detail := "table.readers.set", map[string]any{"table_id": t.ID, "readers": readers, "before": nonNil(before)}
		if readers == nil {
			action, detail = "table.readers.clear", map[string]any{"table_id": t.ID, "before": nonNil(before)}
		}
		if err := auditTx(r, tx, action, t.Name, detail); err != nil {
			return err
		}
		t, err = datatables.GetTable(r.Context(), tx, t.ID)
		return err
	})
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tableView(t, principalFrom(r.Context())))
}

// readersOf is a table's readers as the API answers them: null for every
// table.read holder.
func readersOf(t datatables.Table) any {
	if !t.Restricted() {
		return nil
	}
	return slices.Clone(t.Readers)
}
