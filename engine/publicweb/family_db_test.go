package publicweb

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

// The family page through the database, as the public role: a link
// minted for a family opens the page with both children, their fees,
// account number, credit and receipts; "I have paid" goes through the
// inbox to the api role and becomes a claim the bursar sees, resolved
// when a payment for the student is counted; a plan request becomes a
// request the bursar decides; an expired link offers a new one, which the
// api role sends. Nothing a parent sends counts as a payment.
func TestFamilyPageThroughTheDatabase(t *testing.T) {
	env := rt.New(t)
	ctx := context.Background()
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET name = 'Greenfield Court Academy' WHERE id = $1`, env.Tenant); err != nil {
		t.Fatal(err)
	}
	inv := &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://" + formHost,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}
	s := &schools.Schools{Pool: env.DB.App, Vault: env.Vault, Invoices: inv, InboxKey: inboxKey, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	schools.WireReceipts(inv, s)
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, env.DB.App, []uuid.UUID{env.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	var term schools.Term
	var fam schools.Family
	var ebube, chidi schools.StudentDetail
	in(func(tx pgx.Tx) error {
		if _, err := schools.SetEnabled(ctx, tx, env.Tenant, true, "user:ada"); err != nil {
			return err
		}
		class, err := s.AddClass(ctx, tx, env.Tenant, schools.ClassIn{Name: "JSS 1", Arm: "A", Order: 7}, "user:ada")
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
		if fam, err = s.AddFamily(ctx, tx, env.Tenant, []schools.GuardianIn{{Name: "Ngozi Eze", Phone: "08031234567"}}, "user:ada"); err != nil {
			return err
		}
		if ebube, err = s.AddStudent(ctx, tx, env.Tenant, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: class.ID, FamilyID: &fam.ID}, "user:ada"); err != nil {
			return err
		}
		if chidi, err = s.AddStudent(ctx, tx, env.Tenant, schools.StudentIn{Name: "Chidi Eze", AdmissionNumber: "GCA/002", ClassID: class.ID, FamilyID: &fam.ID}, "user:ada"); err != nil {
			return err
		}
		if _, err := s.StartTerm(ctx, tx, env.Tenant, term.ID, "user:ada"); err != nil {
			return err
		}
		if _, err := s.SetFees(ctx, tx, env.Tenant, term.ID, class.ID, []schools.FeeItemIn{{Name: "Tuition", AmountKobo: 10_000_000, Kind: schools.Required},
			{Name: "WAEC fee", AmountKobo: 1_800_000, Kind: schools.Required, PassThroughFor: "WAEC"}}, "user:ada"); err != nil {
			return err
		}
		_, err = s.InvoiceTerm(ctx, tx, env.Tenant, term.ID, "", false, "user:ada")
		return err
	})
	if _, failed, err := s.SendTerm(ctx, env.Tenant, term.ID, "user:ada"); err != nil || len(failed) != 0 {
		t.Fatalf("send: %v %v", err, failed)
	}
	// A school's invoice went out with the family page's link, not its own.
	var sms string
	if err := env.DB.Admin.QueryRow(ctx, `SELECT count(*)::text FROM message_outbox`).Scan(&sms); err == nil && sms == "0" {
		t.Error("no message queued for the invoices")
	}
	// Ebube paid ₦25,000.50 at the desk: a receipt.
	if _, parts, err := s.PostPayment(ctx, env.Tenant, schools.PostingIn{Student: "GCA/001", AmountKobo: 2_500_050, How: "cash", Date: "2026-10-20"}, "user:ada"); err != nil || len(parts) != 1 {
		t.Fatalf("post: %v", err)
	}
	// A link for the family, as a message would mint it.
	var link schools.FamilyLinkMinted
	in(func(tx pgx.Tx) error {
		var err error
		link, err = s.MintFamilyLink(ctx, tx, env.Tenant, fam.ID, "message", "user:ada")
		return err
	})
	token := strings.TrimPrefix(link.URL, "https://"+formHost+"/s/")
	if len(token) != 32 || link.ExpiresAt.Before(time.Now().Add(71*time.Hour)) {
		t.Fatalf("link %+v", link)
	}

	pool := env.DB.PublicPool(t, 2)
	srv := &Server{Lookup: DBLookup(pool), Brands: DBBrands(pool), Families: DBFamilies{Pool: pool}, FormHost: formHost, InboxKey: inboxKey,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour), Submissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	h := srv.Handler()
	page := func(path string) string {
		t.Helper()
		res := get(h, "GET", formHost, path)
		if res.Code != 200 {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	body := page("/s/" + token)
	for _, want := range []string{"Greenfield Court Academy", "Ebube Eze", "Chidi Eze", "GCA/001", "JSS 1 A", "Tuition", "Collected on behalf of WAEC.", "₦118,000", "₦25,000.50", "₦92,999.50",
		"RCT-2026T1-0001", "/r/", ">Valid<", "has no account number yet", "/i/", "Pay this invoice by transfer or card", "I have paid some other way", "Ask for a payment plan"} {
		if !strings.Contains(body, want) {
			t.Errorf("page: no %q", want)
		}
	}
	if strings.Contains(body, "8031234567") || strings.Contains(body, "Ngozi") {
		t.Error("the page shows the guardian")
	}
	if b2 := page("/s/" + token + "?child=" + chidi.ID.String()); !strings.Contains(b2, "₦118,000") || strings.Contains(b2, "RCT-2026T1-0001") {
		t.Errorf("child 2: %s", b2)
	}

	// I have paid: through the inbox to a claim; never a payment.
	res := postForm(h, formHost, "/s/"+token+"/paid", url.Values{"child": {ebube.ID.String()}, "idem": {"c1"}, "amount": {"50,000"}, "date": {"2026-10-21"}, "how": {"transfer"}, "reference": {"GTB/4471"}, "note": {"To the school account"}})
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "done=paid") {
		t.Fatalf("claim: %d %s", res.Code, res.Header().Get("Location"))
	}
	// The same idem again leaves one ask.
	postForm(h, formHost, "/s/"+token+"/paid", url.Values{"child": {ebube.ID.String()}, "idem": {"c1"}, "amount": {"50,000"}, "date": {"2026-10-21"}, "how": {"transfer"}})
	if err := s.DrainFamilyInbox(ctx); err != nil {
		t.Fatal(err)
	}
	var claims []schools.Claim
	in(func(tx pgx.Tx) error {
		var err error
		claims, err = s.Claims(ctx, tx, env.Tenant, schools.ClaimFilter{})
		return err
	})
	if len(claims) != 1 || claims[0].AmountKobo != 5_000_000 || claims[0].How != "transfer" || claims[0].Reference != "GTB/4471" || claims[0].Note != "To the school account" || claims[0].Name != "Ebube Eze" || claims[0].Status != "open" {
		t.Fatalf("claims %+v", claims)
	}
	var paid int64
	in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT sum(paid_kobo) FROM customer_invoices WHERE tenant_id = $1`, env.Tenant).Scan(&paid)
	})
	if paid != 2_500_050 {
		t.Errorf("a claim counted as a payment: paid %d", paid)
	}
	if body := page("/s/" + token); !strings.Contains(body, "₦50,000 by bank transfer on 21 Oct 2026") || !strings.Contains(body, "The school is looking for it") {
		t.Error("the claim is not on the page")
	}
	// The matcher lists it beside the unmatched money.
	in(func(tx pgx.Tx) error {
		list, err := s.OpenClaims(ctx, tx, env.Tenant, 50)
		if err == nil && (len(list) != 1 || list[0].Student != "GCA/001") {
			t.Errorf("open claims %+v", list)
		}
		return err
	})
	// The bursar finds the transfer and posts it: the claim is matched.
	if _, _, err := s.PostPayment(ctx, env.Tenant, schools.PostingIn{Student: "GCA/001", AmountKobo: 5_000_000, How: "transfer", Date: "2026-10-21"}, "user:ada"); err != nil {
		t.Fatal(err)
	}
	in(func(tx pgx.Tx) error {
		c, err := s.Claim(ctx, tx, env.Tenant, claims[0].ID)
		if err == nil && (c.Status != "matched" || c.ResolvedAt == nil) {
			t.Errorf("claim after the payment: %+v", c)
		}
		return err
	})
	if body := page("/s/" + token); !strings.Contains(body, "Found and counted") {
		t.Error("the matched claim is not on the page")
	}
	// Dismissing needs a reason; a resolved claim is not resolved again.
	in(func(tx pgx.Tx) error {
		if _, err := s.ResolveClaim(ctx, tx, env.Tenant, claims[0].ID, "no such transfer", "user:ada"); err == nil {
			t.Error("a matched claim was dismissed")
		}
		return nil
	})

	// A plan request: one at a time per student; decided, the family is told.
	d1, d2 := time.Now().AddDate(0, 1, 0).Format(time.DateOnly), time.Now().AddDate(0, 2, 0).Format(time.DateOnly)
	res = postForm(h, formHost, "/s/"+token+"/plan", url.Values{"child": {chidi.ID.String()}, "idem": {"p1"}, "amount1": {"50,000"}, "date1": {d1}, "amount2": {"68,000"}, "date2": {d2}, "message": {"Salary comes on the 25th"}})
	if !strings.Contains(res.Header().Get("Location"), "done=plan") {
		t.Fatalf("plan: %s", res.Header().Get("Location"))
	}
	if err := s.DrainFamilyInbox(ctx); err != nil {
		t.Fatal(err)
	}
	if res := postForm(h, formHost, "/s/"+token+"/plan", url.Values{"child": {chidi.ID.String()}, "idem": {"p2"}, "amount1": {"50,000"}, "date1": {d1}}); !strings.Contains(res.Header().Get("Location"), "err=pending") {
		t.Errorf("a second plan: %s", res.Header().Get("Location"))
	}
	var plans []schools.PlanRequest
	in(func(tx pgx.Tx) error {
		var err error
		plans, err = s.Plans(ctx, tx, env.Tenant, schools.PlanFilter{Status: "requested"})
		return err
	})
	if len(plans) != 1 || plans[0].Message != "Salary comes on the 25th" || len(plans[0].Instalments) != 2 || plans[0].Instalments[1].AmountKobo != 6_800_000 || plans[0].Name != "Chidi Eze" {
		t.Fatalf("plans %+v", plans)
	}
	if body := page("/s/" + token + "?child=" + chidi.ID.String()); !strings.Contains(body, "Waiting for the school") {
		t.Error("the plan request is not on the page")
	}
	var decided schools.PlanRequest
	in(func(tx pgx.Tx) error {
		var err error
		decided, err = s.DecidePlan(ctx, tx, env.Tenant, plans[0].ID, schools.PlanDecision{Approve: true, Note: "Pay the first part before resumption"}, "user:ada", nil)
		return err
	})
	if decided.Status != "approved" || len(decided.DecidedInstalments) != 2 {
		t.Fatalf("decided %+v", decided)
	}
	sent, err := s.PlanDecidedAfter(ctx, env.Tenant, decided)
	if err != nil || sent.Channels["sms"] != 1 {
		t.Fatalf("telling the family: %v %+v", err, sent)
	}
	if body := page("/s/" + token + "?child=" + chidi.ID.String()); !strings.Contains(body, ">Approved<") || !strings.Contains(body, "Pay the first part before resumption") {
		t.Error("the decision is not on the page")
	}
	in(func(tx pgx.Tx) error {
		if _, err := s.DecidePlan(ctx, tx, env.Tenant, plans[0].ID, schools.PlanDecision{Approve: false, Note: "no"}, "user:ada", nil); err == nil {
			t.Error("decided twice")
		}
		return nil
	})

	// Expired: the page offers a new link; asking sends one by SMS to the
	// guardian, through the inbox, with a fresh token.
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE school_family_links SET expires_at = now() - interval '1 hour' WHERE id = $1`, link.ID); err != nil {
		t.Fatal(err)
	}
	res = get(h, "GET", formHost, "/s/"+token)
	if res.Code != http.StatusGone || !strings.Contains(res.Body.String(), "Send me a new link") || strings.Contains(res.Body.String(), "Ebube") {
		t.Fatalf("expired: %d %s", res.Code, res.Body.String())
	}
	if res := postForm(h, formHost, "/s/"+token+"/paid", url.Values{"child": {ebube.ID.String()}, "idem": {"x"}, "amount": {"5,000"}, "date": {"2026-10-21"}, "how": {"cash"}}); res.Code != http.StatusSeeOther {
		t.Errorf("a claim on an expired link: %d", res.Code)
	}
	var before int
	if err := env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM school_family_links WHERE family_id = $1`, fam.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if res := postForm(h, formHost, "/s/"+token+"/link", url.Values{"idem": {"n1"}}); !strings.Contains(res.Header().Get("Location"), "done=link") {
		t.Fatalf("new link: %s", res.Header().Get("Location"))
	}
	if err := s.DrainFamilyInbox(ctx); err != nil {
		t.Fatal(err)
	}
	var after int
	var channel string
	if err := env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM school_family_links WHERE family_id = $1`, fam.ID).Scan(&after); err != nil || after != before+1 {
		t.Errorf("links %d -> %d (%v)", before, after, err)
	}
	if err := env.DB.Admin.QueryRow(ctx, `SELECT channel FROM school_family_messages WHERE family_id = $1 AND purpose = 'link'`, fam.ID).Scan(&channel); err != nil || channel != "sms" {
		t.Errorf("the new link's message: %q %v", channel, err)
	}
	// Three an hour: the fourth is limited (the page says the same).
	for i := 2; i <= 4; i++ {
		postForm(h, formHost, "/s/"+token+"/link", url.Values{"idem": {"n" + string(rune('0'+i))}})
	}
	var waiting int
	if err := env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM school_family_inbox WHERE family_id = $1`, fam.ID).Scan(&waiting); err != nil || waiting != 2 {
		t.Errorf("inbox after the limit: %d %v", waiting, err)
	}
	// An unknown token answers the same page, with no button.
	if res := get(h, "GET", formHost, "/s/"+strings.Repeat("B", 32)); res.Code != http.StatusGone || strings.Contains(res.Body.String(), "Send me a new link") {
		t.Errorf("unknown: %d", res.Code)
	}
}
