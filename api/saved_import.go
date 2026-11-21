package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/saved"
)

// importDeadline is how long saving a file may take: below the server's
// write timeout (120 s), so the answer says what was saved and what was
// not reached rather than the connection being cut.
const importDeadline = 90 * time.Second

// importSaved serves POST /v1/saved/import {csv, confirm?} (saved objects
// S3; §9.6): a business's saved things from a file, as its students are
// imported. Without confirm it answers the plan (each thing with its name,
// title, kind and masked hint, and the file's hash); with the plan's hash
// it saves them, each as one added on the Saved page is (a bank account
// checked with its bank). A file with any line that cannot be taken (a
// name saved already as another kind, or past the plan's limit) saves
// nothing and answers 422 with each line and why.
//
// A file saved in part (cut by the time limit, or a line the bank refused)
// can be sent again: a thing saved already by its name and kind is marked
// skip in the plan and not saved twice. A bank named as Taskiem's bank
// list does not know it is a warning on its line, not a refusal: it is
// saved, its name not checked.
func (s *Server) importSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	var req struct {
		CSV     string `json:"csv"`
		Confirm string `json:"confirm"`
	}
	refused := func(ie *saved.ImportError) {
		if ie.Problems == nil {
			ie.Problems = []saved.ImportProblem{}
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ie.Message, "code": "import_unreadable", "problems": ie.Problems, "total": ie.Total})
	}
	tooBig := &saved.ImportError{Message: fmt.Sprintf("The file is too big: up to %d KB.", saved.MaxImportBytes>>10)}
	// The body has room for a file of the largest size as JSON escapes it
	// (a quote or a line break is two characters); the file itself is
	// held to MaxImportBytes once decoded.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*saved.MaxImportBytes+64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			refused(tooBig)
			return
		}
		s.fail(w, r, errors.Join(errBadRequest, err))
		return
	}
	if len(req.CSV) > saved.MaxImportBytes {
		refused(tooBig)
		return
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
	// The names the business has already (with their kinds), and its
	// limit.
	limit, err := s.savedLimit(ctx, o)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	taken := map[string]string{}
	var count int
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, owner := ownerWhere(o)
		list, err := tx.Query(ctx, `SELECT name, kind FROM saved_objects WHERE `+where, owner)
		if err != nil {
			return err
		}
		type nk struct{ Name, Kind string }
		names, err := pgx.CollectRows(list, pgx.RowToStructByPos[nk])
		for _, n := range names {
			taken[n.Name] = n.Kind
		}
		count = len(names)
		return err
	}); err != nil {
		s.savedFail(w, r, err)
		return
	}
	var problems []saved.ImportProblem
	toSave := 0
	for i, x := range rows {
		kind, ok := taken[x.Name]
		switch {
		case !ok:
			toSave++
		case kind == x.Kind:
			rows[i].Skip, rows[i].Warning = true, fmt.Sprintf("@%s is saved already: skipped.", x.Name)
		default:
			problems = append(problems, saved.ImportProblem{Line: x.Line, Message: fmt.Sprintf("@%s is saved already as another kind of thing: give this one another name in the name column.", x.Name)})
		}
	}
	if len(problems) > 0 {
		refused(&saved.ImportError{Message: "Some lines cannot be saved: put them right and send the file again.", Problems: problems, Total: len(rows)})
		return
	}
	if limit > 0 && count+toSave > limit {
		refused(&saved.ImportError{Message: fmt.Sprintf("Your plan keeps up to %d saved things, and you have %d: this file would go past it.", limit, count), Total: toSave})
		return
	}
	// Each account's bank, as the list the Saved page offers names it.
	connected, err := s.importBanks(ctx, o, rows)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	hash := saved.ImportHash(req.CSV)
	if req.Confirm != hash {
		if req.Confirm != "" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "The file changed since it was shown: look at it again.", "code": "import_changed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": false, "hash": hash, "total": toSave, "rows": rows})
		return
	}

	// Each saved as one added by hand is: checked, sealed and recorded,
	// until the deadline (or the client going away); those not reached are
	// said, to send again.
	sctx, cancel := context.WithTimeout(ctx, importDeadline)
	defer cancel()
	who := savedWhoOf(r)
	type failed struct {
		Line    int    `json:"line"`
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	type unchecked struct {
		Line    int    `json:"line"`
		Name    string `json:"name"`
		Problem string `json:"problem"`
	}
	type left struct {
		Line int    `json:"line"`
		Name string `json:"name"`
	}
	added, skipped := []string{}, []string{}
	fails, notChecked, notReached := []failed{}, []unchecked{}, []left{}
	for _, x := range rows {
		if x.Skip {
			skipped = append(skipped, x.Name)
			continue
		}
		if sctx.Err() != nil {
			notReached = append(notReached, left{x.Line, x.Name})
			continue
		}
		in := savedIn{Name: &x.Name, Title: &x.Title, Kind: x.Kind}
		if x.Kind == saved.KindBankAccount {
			bank, code, from, number, note := x.Account.BankName, x.Account.BankCode, x.Account.CodeFrom, x.Account.AccountNumber, x.Account.Note
			in.BankName, in.AccountNumber = &bank, &number
			if code != "" {
				in.BankCode, in.CodeFrom = &code, &from
			}
			if note != "" {
				in.Note = &note
			}
		} else {
			in.Details = x.Details
		}
		row, problem, err := s.addSavedNew(sctx, who, o, in)
		switch {
		case err == nil:
			added = append(added, x.Name)
			if x.Kind == saved.KindBankAccount && !o.mine() && row.CheckState != saved.Checked {
				notChecked = append(notChecked, unchecked{x.Line, x.Name, uncheckedWords(problem, row, connected)})
			}
		case sctx.Err() != nil:
			// Cut by the deadline: saved or not, sending the file again
			// saves it once.
			notReached = append(notReached, left{x.Line, x.Name})
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
	// Recorded even when the client went away.
	actx := context.WithoutCancel(ctx)
	if err := s.savedTx(actx, o, func(tx pgx.Tx) error {
		return auditTx(r, tx, "saved.import", hash, map[string]any{"added": len(added), "failed": len(fails), "skipped": len(skipped), "left": len(notReached), "unchecked": len(notChecked)})
	}); err != nil {
		s.Logger.Warn("saved: recording an import", "err", err)
	}
	out := map[string]any{"saved": true, "hash": hash, "total": toSave, "added": added, "failed": fails,
		"skipped": skipped, "unchecked": notChecked, "left": notReached}
	if n := len(notChecked); n > 0 {
		what := fmt.Sprintf("%d accounts' names are not checked yet", n)
		if n == 1 {
			what = "1 account's name is not checked yet"
		}
		if connected {
			out["check_words"] = what + ": Check names on the Saved page checks them."
		} else {
			out["check_words"] = what + ": connect a payments account, then Check names on the Saved page checks them."
		}
	}
	if len(notReached) > 0 {
		out["left_words"] = "Not saved yet: send the file again; what is saved is skipped."
	}
	writeJSON(w, http.StatusOK, out)
}

// uncheckedWords is why a saved account's name was not checked, in plain
// words.
func uncheckedWords(p savedProblem, x savedRow, connected bool) string {
	switch {
	case p.Words != "":
		return p.Words
	case x.CheckProblem != nil && *x.CheckProblem != "":
		return *x.CheckProblem
	case !connected:
		return "Not checked yet: connect a payments account to check names with the bank."
	}
	return "Not checked yet."
}

// importBanks names each account's bank as the bank list the Saved page
// offers does (GET /v1/banks: the payments connection's own, else the
// banks by name), with its code in that list, and puts a warning on an
// account whose bank the list does not have by that name: it is saved by
// the name given, and its name cannot be checked. It answers whether the
// business has a payments connection to check names through.
func (s *Server) importBanks(ctx context.Context, o savedOwner, rows []saved.ImportRow) (bool, error) {
	needed := false
	for _, x := range rows {
		needed = needed || x.Kind == saved.KindBankAccount && !x.Skip
	}
	connected := false
	items, from := builtInBanks(), ""
	if !o.mine() {
		rl, err := s.savedRail(ctx, o.tenant)
		if err != nil {
			return false, err
		}
		connected = rl != nil
		if rl != nil && needed {
			if banks, err := s.savedBanks(ctx, o.tenant, *rl); err == nil {
				if list := bankItems(banks); len(list) > 0 {
					items, from = list, rl.Channel()
				}
			}
		}
	}
	for i, x := range rows {
		if x.Kind != saved.KindBankAccount || x.Skip {
			continue
		}
		b, ok := matchBank(items, x.Account.BankName)
		if !ok {
			rows[i].Warning = fmt.Sprintf("%s is not a bank Taskiem knows by that name: its name will not be checked.", x.Account.BankName)
			continue
		}
		name := b.Name
		if len([]rune(name)) > saved.MaxBankName {
			name = string([]rune(name)[:saved.MaxBankName])
		}
		rows[i].Account.BankName = name
		if from != "" {
			rows[i].Account.BankCode, rows[i].Account.CodeFrom = b.Code, from
		}
		rows[i].Hint = rows[i].Account.Hint()
	}
	return connected, nil
}

// matchBank is the one bank of items a file's bank name is: the same once
// case, punctuation and the words "Bank", "PLC", "Limited", "Nigeria",
// "of" and "the" are set aside ("Zenith", "First Bank"), or the short name
// lists show ("GTBank", "GT Bank", "UBA"). None, or two, is no match.
func matchBank(items []bankItem, typed string) (bankItem, bool) {
	words := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	key := func(s string) string {
		var out []string
		for _, w := range words(s) {
			switch w {
			case "bank", "plc", "limited", "ltd", "nigeria", "of", "the":
			default:
				out = append(out, w)
			}
		}
		return strings.Join(out, "")
	}
	all := func(s string) string { return strings.Join(words(s), "") }
	k, a := key(typed), all(typed)
	if a == "" {
		return bankItem{}, false
	}
	var found *bankItem
	for i, b := range items {
		short := saved.ShortBank(b.Name)
		if k != "" && k == key(b.Name) || a == all(short) || k != "" && k == key(short) {
			if found != nil && found.Name != b.Name {
				return bankItem{}, false
			}
			found = &items[i]
		}
	}
	if found == nil {
		return bankItem{}, false
	}
	return *found, true
}
