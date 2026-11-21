package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/saved"
)

// importSaved serves POST /v1/saved/import {csv, confirm?} (saved objects
// S3; §9.6): a business's saved things from a file, as its students are
// imported. Without confirm it answers the plan (each thing with its name,
// title, kind and masked hint, and the file's hash); with the plan's hash
// it saves them, each as one added on the Saved page is (a bank account
// checked with its bank). A file with any line that cannot be taken (a
// name taken already, or past the plan's limit) saves nothing and answers
// 422 with each line and why.
func (s *Server) importSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	var req struct {
		CSV     string `json:"csv"`
		Confirm string `json:"confirm"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, saved.MaxImportBytes+64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.fail(w, r, errors.Join(errBadRequest, err))
		return
	}
	refused := func(ie *saved.ImportError) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ie.Message, "code": "import_unreadable", "problems": ie.Problems, "total": ie.Total})
	}
	rows, err := saved.ReadImport(req.CSV)
	var ie *saved.ImportError
	if errors.As(err, &ie) {
		refused(ie)
		return
	}
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	// The names the business has already, and its limit.
	limit, err := s.savedLimit(ctx, o)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	taken := map[string]bool{}
	var count int
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, owner := ownerWhere(o)
		list, err := tx.Query(ctx, `SELECT name FROM saved_objects WHERE `+where, owner)
		if err != nil {
			return err
		}
		names, err := pgx.CollectRows(list, pgx.RowTo[string])
		for _, n := range names {
			taken[n] = true
		}
		count = len(names)
		return err
	}); err != nil {
		s.savedFail(w, r, err)
		return
	}
	var problems []saved.ImportProblem
	for _, x := range rows {
		if taken[x.Name] {
			problems = append(problems, saved.ImportProblem{Line: x.Line, Message: fmt.Sprintf("@%s is saved already: give this one another name in the name column.", x.Name)})
		}
	}
	if len(problems) > 0 {
		refused(&saved.ImportError{Message: "Some lines cannot be saved: put them right and send the file again.", Problems: problems, Total: len(rows)})
		return
	}
	if limit > 0 && count+len(rows) > limit {
		refused(&saved.ImportError{Message: fmt.Sprintf("Your plan keeps up to %d saved things, and you have %d: this file would go past it.", limit, count), Total: len(rows)})
		return
	}
	hash := saved.ImportHash(req.CSV)
	if req.Confirm != hash {
		if req.Confirm != "" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "The file changed since it was shown: look at it again.", "code": "import_changed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": false, "hash": hash, "total": len(rows), "rows": rows})
		return
	}
	// Each saved as one added by hand is: checked, sealed and recorded.
	who := savedWhoOf(r)
	type failed struct {
		Line    int    `json:"line"`
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	var added []string
	fails := []failed{}
	for _, x := range rows {
		in := savedIn{Name: &x.Name, Title: &x.Title, Kind: x.Kind}
		if x.Kind == saved.KindBankAccount {
			bank, number, note := x.Account.BankName, x.Account.AccountNumber, x.Account.Note
			in.BankName, in.AccountNumber = &bank, &number
			if note != "" {
				in.Note = &note
			}
		} else {
			in.Details = x.Details
		}
		_, _, err := s.addSavedNew(ctx, who, o, in)
		switch {
		case err == nil:
			added = append(added, x.Name)
		case refusedWords(err) != "":
			fails = append(fails, failed{x.Line, x.Name, refusedWords(err)})
		default:
			var pe interface{ SQLState() string }
			if errors.As(err, &pe) && pe.SQLState() == "23505" {
				fails = append(fails, failed{x.Line, x.Name, "That name was taken meanwhile."})
				continue
			}
			s.Logger.Warn("saved: importing a line", "line", x.Line, "err", err)
			fails = append(fails, failed{x.Line, x.Name, "It could not be saved just now."})
		}
	}
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		return auditTx(r, tx, "saved.import", hash, map[string]any{"added": len(added), "failed": len(fails)})
	}); err != nil {
		s.Logger.Warn("saved: recording an import", "err", err)
	}
	if added == nil {
		added = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "hash": hash, "total": len(rows), "added": added, "failed": fails})
}
