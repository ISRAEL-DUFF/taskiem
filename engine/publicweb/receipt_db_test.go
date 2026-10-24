package publicweb

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/invoicing"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A receipt's verification page: its token answers Valid (then Voided
// once the payment is undone), the school, the amount and the date, and
// nothing else: not the student, not the number, not the invoice. The
// number, or any other guess, finds nothing.
func TestReceiptVerificationPage(t *testing.T) {
	env := rt.New(t)
	ctx := context.Background()
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET name = 'Greenfield Court Academy' WHERE id = $1`, env.Tenant); err != nil {
		t.Fatal(err)
	}
	inv := &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://" + formHost,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}
	s := &schools.Schools{Pool: env.DB.App, Vault: env.Vault, Invoices: inv}
	schools.WireReceipts(inv, s)
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, env.DB.App, []uuid.UUID{env.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	var term schools.Term
	in(func(tx pgx.Tx) error {
		if _, err := schools.SetEnabled(ctx, tx, env.Tenant, true, "user:ada"); err != nil {
			return err
		}
		class, err := s.AddClass(ctx, tx, env.Tenant, schools.ClassIn{Name: "JSS 1", Order: 7}, "user:ada")
		if err != nil {
			return err
		}
		sess, err := s.AddSession(ctx, tx, env.Tenant, "2026/2027", "user:ada")
		if err != nil {
			return err
		}
		if term, err = s.AddTerm(ctx, tx, env.Tenant, sess.ID, schools.TermIn{Name: "First term", StartsOn: "2026-09-14", EndsOn: "2099-12-11", FeesDueOn: "2099-09-07"}, "user:ada"); err != nil {
			return err
		}
		fam, err := s.AddFamily(ctx, tx, env.Tenant, []schools.GuardianIn{{Name: "Ngozi Eze", Phone: "08031234567"}}, "user:ada")
		if err != nil {
			return err
		}
		if _, err := s.AddStudent(ctx, tx, env.Tenant, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: class.ID, FamilyID: &fam.ID}, "user:ada"); err != nil {
			return err
		}
		if _, err := s.StartTerm(ctx, tx, env.Tenant, term.ID, "user:ada"); err != nil {
			return err
		}
		if _, err := s.SetFees(ctx, tx, env.Tenant, term.ID, class.ID, []schools.FeeItemIn{{Name: "Tuition", AmountKobo: 10_000_000, Kind: schools.Required}}, "user:ada"); err != nil {
			return err
		}
		_, err = s.InvoiceTerm(ctx, tx, env.Tenant, term.ID, "", false, "user:ada")
		return err
	})
	if _, failed, err := s.SendTerm(ctx, env.Tenant, term.ID, "user:ada"); err != nil || len(failed) != 0 {
		t.Fatalf("send: %v %v", err, failed)
	}
	p, parts, err := s.PostPayment(ctx, env.Tenant, schools.PostingIn{Student: "GCA/001", AmountKobo: 2_500_050, How: "cash", Date: "2026-10-20"}, "user:ada")
	if err != nil || len(parts) != 1 {
		t.Fatalf("post: %v %+v", err, p)
	}
	var rc schools.Receipt
	in(func(tx pgx.Tx) error {
		var err error
		rc, err = s.ReceiptByID(ctx, tx, env.Tenant, p.Result.Parts[0].ReceiptNumber)
		return err
	})
	token := strings.TrimPrefix(rc.Link, "https://"+formHost+"/r/")

	pool := env.DB.PublicPool(t, 2)
	newSrv := func() http.Handler {
		s := &Server{Lookup: DBLookup(pool), Brands: DBBrands(pool), Invoices: DBInvoices(pool), Receipts: DBReceipts(pool), FormHost: formHost,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
		return s.Handler()
	}
	never := []string{"Ebube", "Eze", "GCA/001", rc.Number, rc.InvoiceNumber, "JSS 1", "8031234567"}
	check := func(path string, code int, wants ...string) string {
		t.Helper()
		res := get(newSrv(), "GET", formHost, path)
		body := res.Body.String()
		if res.Code != code && (code != 0 || res.Code == 200) {
			t.Fatalf("%s: status %d, want %d: %s", path, res.Code, code, body)
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q in %s", path, want, body)
			}
		}
		for _, n := range never {
			if strings.Contains(body, n) {
				t.Errorf("%s: the page shows %q", path, n)
			}
		}
		if h := res.Header(); code == 200 && (h.Get("Cache-Control") != "no-store" || h.Get("X-Robots-Tag") != "noindex") {
			t.Errorf("%s: headers %v", path, h)
		}
		return body
	}
	check("/r/"+token, 200, "Valid", "Greenfield Court Academy", "₦25,000.50", "20 October 2026")
	// The number alone, and guesses, find nothing (the form link's answer).
	// (404 from the form link; 503 here, where no form keys are set.)
	check("/r/"+rc.Number, 0)
	check("/r/"+strings.Repeat("A", 32), 0)
	check("/r/"+token+"/x", 0)
	// Undone: Voided, nothing more.
	in(func(tx pgx.Tx) error {
		_, err := inv.UndoPayment(ctx, tx, env.Tenant, rc.InvoiceID.String(), rc.PartID, "user:ada")
		return err
	})
	body := check("/r/"+token, 200, "Voided", "Greenfield Court Academy", "₦25,000.50")
	if strings.Contains(body, ">Valid<") {
		t.Errorf("voided receipt still valid: %s", body)
	}
}
