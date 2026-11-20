package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// savedRails is a business's Taskiem wallet that names accounts: names
// holds the bank's name for each account number it knows; any other is
// refused as the bank refuses an account it does not have.
type savedRails struct {
	names map[string]string
	asked []map[string]any
	down  bool
	// listing, when set, is the wallet's own list_banks capability (with
	// the world's BillBanks taken away), as a rail that maps it answers.
	listing []any
}

var savedRail = invoicing.Rail{Connector: "iswallet", Connection: "taskiem", Name: "iswallet", Events: map[string]bool{},
	Caps: map[string]connector.CapabilityMap{"resolve_account": {}, "pay_bank": {}}}

func (f *savedRails) rail() invoicing.Rail {
	if f.listing == nil {
		return savedRail
	}
	rl := savedRail
	rl.Caps = map[string]connector.CapabilityMap{"resolve_account": {}, "pay_bank": {}, "list_banks": {}}
	return rl
}

func (f *savedRails) Capable(_ context.Context, _ uuid.UUID, capability string) ([]invoicing.Rail, error) {
	if rl := f.rail(); rl.Maps(capability) {
		return []invoicing.Rail{rl}, nil
	}
	return nil, nil
}

func (f *savedRails) ResolveChannel(_ context.Context, _ uuid.UUID, channel, _ string) (invoicing.Rail, error) {
	if channel == savedRail.Channel() {
		return f.rail(), nil
	}
	return invoicing.Rail{}, invoicing.ErrNoRail
}

func (f *savedRails) Call(_ context.Context, _ uuid.UUID, _ invoicing.Rail, capability string, in map[string]any, _ effects.KeyInput) (map[string]any, error) {
	if capability == "list_banks" && f.listing != nil {
		return map[string]any{"banks": f.listing}, nil
	}
	if capability != "resolve_account" {
		return nil, fmt.Errorf("not mapped: %w", effects.ErrFatal)
	}
	f.asked = append(f.asked, in)
	if f.down {
		return nil, fmt.Errorf("timeout: %w", effects.ErrRetryable)
	}
	if in["bank_code"] != "000013" && in["bank_code"] != "000015" {
		return nil, fmt.Errorf("no such bank: %w", effects.ErrFatal)
	}
	name, ok := f.names[in["account_number"].(string)]
	if !ok {
		return nil, fmt.Errorf("account not found: %w", effects.ErrFatal)
	}
	return map[string]any{"account_name": name}, nil
}

// savedWorld is a business that can keep passkeys, with its owner.
func savedWorld(t *testing.T) (*world, *client) {
	t.Helper()
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) {
		return []map[string]any{{"bank_code": "000013", "bank_name": "GUARANTY TRUST BANK"}, {"bank_code": "000015", "bank_name": "Zenith Bank"}}, nil
	}
	return w, w.tenant(t, "Bola Stores", "bola@saved.test")
}

func auditDetails(t *testing.T, w *world, tenant, action string) []string {
	t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT detail::text FROM audit_log WHERE tenant_id = $1 AND action = $2 ORDER BY chain_seq`, tenant, action)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// S0's done when: an owner with no provider is offered the step to connect
// one, saves a supplier's account as @bayo, unchecked until the business
// has a payments connection, then checked and shown with the bank's name;
// staff see it masked and cannot change it; changing the number asks for a
// passkey and is in the audit; the number is never in an answer, the
// audit, or the row, but sealed.
func TestSavedAccounts(t *testing.T) {
	ctx := context.Background()
	w, owner := savedWorld(t)
	biz := tenantOf(t, owner)

	// No payments connection: the owner is offered the step to connect.
	p := owner.must(200, "GET", "/v1/saved/provider", nil)
	if p["connected"] != nil || p["owner"] != true || p["wallet"] == nil || len(p["providers"].([]any)) != 4 {
		t.Fatalf("provider: %v", p)
	}
	banks := owner.must(200, "GET", "/v1/banks", nil)
	if banks["from"] != "" || !strings.Contains(toJSON(banks), `"Guaranty Trust Bank"`) {
		t.Fatalf("banks by name: %v", banks)
	}
	owner.must(409, "POST", "/v1/name-check", map[string]any{"bank_name": "Guaranty Trust Bank", "account_number": "0123456789"})

	// Saved unchecked.
	add := map[string]any{"name": "@Bayo", "title": "Bayo (rice)", "bank_name": "Guaranty Trust Bank", "account_number": "012 345 6789", "note": "rice supplier"}
	x := owner.must(201, "POST", "/v1/saved", add)["saved"].(map[string]any)
	id := x["id"].(string)
	d := x["details"].(map[string]any)
	if x["name"] != "bayo" || x["owner"] != "business" || x["hint"] != "GTBank ••6789" || d["account_number"] != "••6789" ||
		x["check"].(map[string]any)["state"] != "unchecked" || x["to_change"] != true {
		t.Fatalf("saved: %v", x)
	}
	owner.must(409, "POST", "/v1/saved", add)
	if e := owner.must(400, "POST", "/v1/saved", map[string]any{"name": "x", "title": "X", "bank_name": "Zenith Bank", "account_number": "0123456789"}); !strings.Contains(fmt.Sprint(e["error"]), "2 to 32") {
		t.Fatalf("short name: %v", e)
	}
	owner.must(400, "POST", "/v1/saved", map[string]any{"name": "ada", "title": "Ada", "bank_name": "Zenith Bank", "account_number": "12345"})

	// The business opens its Taskiem wallet: names are checked through it.
	rails := &savedRails{names: map[string]string{"0123456789": "BAYO ADEWALE", "0123456780": "BAYO ADEWALE ENTERPRISES"}}
	w.srv.AccountRails = rails
	p = owner.must(200, "GET", "/v1/saved/provider", nil)
	if c, _ := p["connected"].(map[string]any); c == nil || c["channel"] != "iswallet/taskiem" || c["wallet"] != true || c["name"] != "Your Taskiem wallet" || p["wallet"] != nil {
		t.Fatalf("connected: %v", p)
	}
	if l := owner.must(200, "GET", "/v1/saved", nil); l["can_check"] != true || len(l["saved"].([]any)) != 1 {
		t.Fatalf("list: %v", l)
	}
	if c := owner.must(200, "POST", "/v1/saved/check", nil); c["checked"] != float64(1) || c["unchecked"] != float64(0) {
		t.Fatalf("check: %v", c)
	}
	// The bank was found in the wallet's list by name.
	if len(rails.asked) != 1 || rails.asked[0]["bank_code"] != "000013" {
		t.Fatalf("asked %v", rails.asked)
	}
	x = owner.must(200, "GET", "/v1/saved/"+id, nil)
	if ch := x["check"].(map[string]any); ch["state"] != "checked" || ch["account_name"] != "BAYO ADEWALE" || ch["via"] != "iswallet/taskiem" {
		t.Fatalf("checked: %v", x)
	}
	if l := owner.must(200, "GET", "/v1/saved", nil); l["can_check"] != false {
		t.Fatalf("nothing left to check: %v", l)
	}

	// With the wallet: its own list, and the name checked as it is saved.
	banks = owner.must(200, "GET", "/v1/banks", nil)
	if banks["from"] != "iswallet/taskiem" || !strings.Contains(toJSON(banks), `"code":"000013"`) {
		t.Fatalf("wallet banks: %v", banks)
	}
	if n := owner.must(200, "POST", "/v1/name-check", map[string]any{"bank_name": "GUARANTY TRUST BANK", "bank_code": "000013", "code_from": "iswallet/taskiem",
		"account_number": "0123456780"}); n["account_name"] != "BAYO ADEWALE ENTERPRISES" {
		t.Fatalf("name check: %v", n)
	}
	// An account the bank does not have is never saved.
	if e := owner.must(400, "POST", "/v1/saved", map[string]any{"name": "ghost", "title": "Ghost", "bank_name": "GUARANTY TRUST BANK", "bank_code": "000013",
		"code_from": "iswallet/taskiem", "account_number": "9999999999"}); !strings.Contains(fmt.Sprint(e["error"]), "has no account ••9999") {
		t.Fatalf("unknown account: %v", e)
	}
	// One that could not be checked just now is kept unchecked, with why.
	rails.down = true
	y := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "landlord", "title": "Landlord", "bank_name": "Zenith Bank", "account_number": "1111111111"})
	if y["check_problem"] == nil || y["saved"].(map[string]any)["check"].(map[string]any)["state"] != "unchecked" {
		t.Fatalf("down: %v", y)
	}
	rails.down = false

	// Staff see the business's saved things, masked, and call them up; they
	// do not change them or see a number in full.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "kemi@saved.test", "password": testPassword, "roles": []string{"cashier"}})
	kemi := w.login(t, "kemi@saved.test", testPassword)
	l := kemi.must(200, "GET", "/v1/saved", nil)
	if s := toJSON(l); len(l["saved"].([]any)) != 2 || strings.Contains(s, "0123456789") || !strings.Contains(s, "••6789") || l["can_check"] != false {
		t.Fatalf("staff list: %s", s)
	}
	if r := kemi.must(200, "GET", "/v1/saved/resolve?name=@bayo", nil); len(r["matches"].([]any)) != 1 || r["matches"].([]any)[0].(map[string]any)["to_change"] != false {
		t.Fatalf("resolve: %v", r)
	}
	if r := kemi.must(200, "GET", "/v1/saved/resolve?name=nobody", nil); len(r["matches"].([]any)) != 0 {
		t.Fatalf("resolve none: %v", r)
	}
	kemi.must(403, "PUT", "/v1/saved/"+id, map[string]any{"title": "Mine now"})
	kemi.must(403, "POST", "/v1/saved/"+id+"/reveal", nil)
	kemi.must(403, "DELETE", "/v1/saved/"+id, nil)
	kemi.must(403, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.reveal", "target": id})
	if p := kemi.must(200, "GET", "/v1/saved/provider", nil); p["owner"] != false || p["wallet"] != nil || p["connected"] == nil {
		t.Fatalf("staff provider: %v", p)
	}

	// A title is changed without a passkey; the account itself is not.
	if c := owner.must(200, "PUT", "/v1/saved/"+id, map[string]any{"title": "Bayo Rice"})["saved"].(map[string]any); c["title"] != "Bayo Rice" || c["check"].(map[string]any)["state"] != "checked" {
		t.Fatalf("title: %v", c)
	}
	// Nor its bank's code alone: a code is a bank.
	if e := owner.must(403, "PUT", "/v1/saved/"+id, map[string]any{"bank_code": "000015", "code_from": "iswallet/taskiem"}); e["step_up"] != "passkey" {
		t.Fatalf("code without a passkey: %v", e)
	}
	newNumber := map[string]any{"account_number": "0123456780"}
	if e := owner.must(403, "PUT", "/v1/saved/"+id, newNumber); e["step_up"] != "passkey" || e["operation"] != "saved.change" || e["target"] != id {
		t.Fatalf("change without a passkey: %v", e)
	}
	if e := owner.must(409, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.change", "target": id}); e["no_passkey"] != true {
		t.Fatalf("no passkey: %v", e)
	}
	key := addPasskey(t, owner, "Phone")
	// A passkey asked for seeing the number does not change the account.
	reveal := challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.reveal", "target": id}))
	newNumber["passkey"] = key.Assert(reveal).JSON()
	owner.must(403, "PUT", "/v1/saved/"+id, newNumber)
	change := challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.change", "target": id}))
	newNumber["passkey"] = key.Assert(change).JSON()
	c := owner.must(200, "PUT", "/v1/saved/"+id, newNumber)["saved"].(map[string]any)
	if c["hint"] != "GTBank ••6780" || c["check"].(map[string]any)["account_name"] != "BAYO ADEWALE ENTERPRISES" {
		t.Fatalf("changed: %v", c)
	}
	// The same assertion does not pass twice.
	owner.must(403, "PUT", "/v1/saved/"+id, map[string]any{"account_number": "0123456789", "passkey": newNumber["passkey"]})
	changes := auditDetails(t, w, biz, "saved.changed")
	if len(changes) != 2 || !strings.Contains(changes[1], `"confirmed": "passkey"`) || !strings.Contains(changes[1], "••6789") || !strings.Contains(changes[1], "••6780") {
		t.Fatalf("audit of the change: %v", changes)
	}

	// Seeing the number in full: a passkey for that, recorded.
	owner.must(403, "POST", "/v1/saved/"+id+"/reveal", map[string]any{})
	reveal = challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.reveal", "target": id}))
	if r := owner.must(200, "POST", "/v1/saved/"+id+"/reveal", map[string]any{"passkey": key.Assert(reveal).JSON()}); r["account_number"] != "0123456780" {
		t.Fatalf("reveal: %v", r)
	}
	if n := len(auditDetails(t, w, biz, "saved.revealed")); n != 1 {
		t.Fatalf("reveals audited: %d", n)
	}

	// The number is sealed in the row, and in no audit entry.
	var raw string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT row_to_json(s)::text FROM saved_objects s WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "0123456780") || !strings.Contains(raw, `"$pii"`) {
		t.Fatalf("row: %s", raw)
	}
	var leaked int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND (detail::text LIKE '%0123456789%' OR detail::text LIKE '%0123456780%')`, biz).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("a full number in the audit: %d %v", leaked, err)
	}

	// Removed: gone from the list, the name free again.
	owner.must(204, "DELETE", "/v1/saved/"+id, nil)
	owner.must(404, "GET", "/v1/saved/"+id, nil)
	owner.must(201, "POST", "/v1/saved", add)
	if n := len(auditDetails(t, w, biz, "saved.removed")); n != 1 {
		t.Fatalf("removal audited: %d", n)
	}
	// Another business sees none of it.
	other := w.tenant(t, "Other", "other@saved.test")
	if l := other.must(200, "GET", "/v1/saved", nil); len(l["saved"].([]any)) != 0 {
		t.Fatalf("another business: %v", l)
	}
	other.must(404, "GET", "/v1/saved/"+y["saved"].(map[string]any)["id"].(string), nil)
}

// A person's own saved accounts (Mine) are theirs alone: no business, and
// no one else in it, reaches them; inside a business, @name finds the
// person's own first, then the business's. They are sealed under the
// person's key and logged on their side; never checked through the
// business's connection.
func TestSavedMine(t *testing.T) {
	ctx := context.Background()
	w, owner := savedWorld(t)
	rails := &savedRails{names: map[string]string{"2222222222": "MUM"}}
	w.srv.AccountRails = rails
	biz := uuid.MustParse(tenantOf(t, owner))
	me := personID(t, owner)

	mum := map[string]any{"name": "mum", "title": "Mum", "bank_name": "Guaranty Trust Bank", "account_number": "2222222222"}
	x := owner.must(201, "POST", "/v1/me/saved", mum)["saved"].(map[string]any)
	id := x["id"].(string)
	if x["owner"] != "mine" || x["check"].(map[string]any)["state"] != "unchecked" || len(rails.asked) != 0 {
		t.Fatalf("mine: %v (asked %v)", x, rails.asked)
	}
	owner.must(409, "POST", "/v1/me/saved", mum)
	// The business's list does not hold it; the business may have its own @mum.
	if l := owner.must(200, "GET", "/v1/saved", nil); len(l["saved"].([]any)) != 0 {
		t.Fatalf("business list: %v", l)
	}
	owner.must(404, "GET", "/v1/saved/"+id, nil)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "mum", "title": "Mum's shop", "bank_name": "Guaranty Trust Bank", "account_number": "2222222222"})
	r := owner.must(200, "GET", "/v1/saved/resolve?name=MUM", nil)["matches"].([]any)
	if len(r) != 2 || r[0].(map[string]any)["owner"] != "mine" || r[1].(map[string]any)["owner"] != "business" {
		t.Fatalf("which @mum: %v", r)
	}
	if r := owner.must(200, "GET", "/v1/me/saved/resolve?name=mum", nil)["matches"].([]any); len(r) != 1 {
		t.Fatalf("mine only: %v", r)
	}

	// Someone else in the business never reaches it.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ada@saved.test", "password": testPassword, "roles": []string{"admin"}})
	ada := w.login(t, "ada@saved.test", testPassword)
	ada.must(404, "GET", "/v1/me/saved/"+id, nil)
	ada.must(404, "DELETE", "/v1/me/saved/"+id, nil)
	if l := ada.must(200, "GET", "/v1/me/saved", nil); len(l["saved"].([]any)) != 0 {
		t.Fatalf("ada's own: %v", l)
	}
	if r := ada.must(200, "GET", "/v1/saved/resolve?name=mum", nil)["matches"].([]any); len(r) != 1 || r[0].(map[string]any)["owner"] != "business" {
		t.Fatalf("ada's @mum: %v", r)
	}
	// Nor does the business's own scope.
	var n int
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE person_id IS NOT NULL`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("the business's scope reads a person's: %d %v", n, err)
	}

	// Sealed under the person's key; seen in full with a passkey, logged on
	// their side.
	var raw string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT sealed::text FROM saved_objects WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "2222222222") || !strings.Contains(raw, `"person"`) {
		t.Fatalf("sealed: %s", raw)
	}
	key := addPasskey(t, owner, "Phone")
	ch := challengeFrom(t, owner.must(200, "POST", "/v1/me/saved/step-up/options", map[string]any{"operation": "saved.reveal", "target": id}))
	if rv := owner.must(200, "POST", "/v1/me/saved/"+id+"/reveal", map[string]any{"passkey": key.Assert(ch).JSON()}); rv["account_number"] != "2222222222" {
		t.Fatalf("reveal: %v", rv)
	}
	var actions []string
	rows, err := w.env.DB.Admin.Query(ctx, `SELECT action FROM saved_object_events WHERE person_id = $1 AND object_id = $2 ORDER BY at`, me, id)
	if err != nil {
		t.Fatal(err)
	}
	if actions, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || strings.Join(actions, ",") != "added,revealed" {
		t.Fatalf("log: %v %v", actions, err)
	}
	if n := len(auditDetails(t, w, biz.String(), "saved.revealed")); n != 0 {
		t.Fatalf("a person's own in the business's audit: %d", n)
	}

	// Changing the account: a passkey, logged; still never checked.
	change := map[string]any{"account_number": "3333333333"}
	owner.must(403, "PUT", "/v1/me/saved/"+id, change)
	ch = challengeFrom(t, owner.must(200, "POST", "/v1/me/saved/step-up/options", map[string]any{"operation": "saved.change", "target": id}))
	change["passkey"] = key.Assert(ch).JSON()
	if c := owner.must(200, "PUT", "/v1/me/saved/"+id, change)["saved"].(map[string]any); c["hint"] != "GTBank ••3333" || len(rails.asked) != 1 {
		t.Fatalf("changed: %v (asked %v)", c, rails.asked)
	}
	owner.must(204, "DELETE", "/v1/me/saved/"+id, nil)
	if l := owner.must(200, "GET", "/v1/me/saved", nil); len(l["saved"].([]any)) != 0 {
		t.Fatalf("after removing: %v", l)
	}
	if b := owner.must(200, "GET", "/v1/me/banks", nil); b["from"] != "" || len(b["banks"].([]any)) < 20 {
		t.Fatalf("banks: %v", b)
	}
}

// The review's fixes: a bank code is the bank its connection's list says
// (one the list lacks is refused); the list is the checking connection's
// own; a change made from a stale read is refused; the bank is asked before
// the passkey; a lasting problem is kept, shown and not asked about again;
// a taken name never reaches the bank.
func TestSavedReviewFixes(t *testing.T) {
	ctx := context.Background()
	w, owner := savedWorld(t)
	rails := &savedRails{names: map[string]string{"0123456789": "BAYO ADEWALE", "2222222222": "ZENITH ACCOUNT", "3333333333": "KUDA ACCOUNT"}}
	w.srv.AccountRails = rails

	// A code from the wallet's list names its bank, whatever name is sent.
	x := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "zee", "title": "Zee", "bank_name": "Guaranty Trust Bank", "bank_code": "000015",
		"code_from": "iswallet/taskiem", "account_number": "2222222222"})["saved"].(map[string]any)
	if d := x["details"].(map[string]any); d["bank_name"] != "Zenith Bank" || d["bank_code"] != "000015" || x["hint"] != "Zenith Bank ••2222" {
		t.Fatalf("bank from its code: %v", x)
	}
	zee := x["id"].(string)
	// A code the list does not have is refused; one from another list is
	// dropped and the bank found by name.
	owner.must(400, "POST", "/v1/saved", map[string]any{"name": "nope", "title": "Nope", "bank_name": "Guaranty Trust Bank", "bank_code": "999999",
		"code_from": "iswallet/taskiem", "account_number": "0123456789"})
	x = owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bayo", "title": "Bayo", "bank_name": "Guaranty Trust Bank", "bank_code": "058",
		"code_from": "paystack/main", "account_number": "0123456789"})["saved"].(map[string]any)
	if d := x["details"].(map[string]any); d["bank_code"] != nil || x["check"].(map[string]any)["account_name"] != "BAYO ADEWALE" {
		t.Fatalf("a code from another list: %v", x)
	}
	bayo := x["id"].(string)

	// A taken name is refused before the bank is asked.
	asked := len(rails.asked)
	owner.must(409, "POST", "/v1/saved", map[string]any{"name": "bayo", "title": "Again", "bank_name": "Zenith Bank", "account_number": "2222222222"})
	if len(rails.asked) != asked {
		t.Fatalf("the bank was asked about a taken name")
	}

	// The bank is asked before the passkey: an account it does not have is
	// refused without one.
	if e := owner.must(400, "PUT", "/v1/saved/"+bayo, map[string]any{"account_number": "9999999999"}); !strings.Contains(fmt.Sprint(e["error"]), "has no account") {
		t.Fatalf("refused before the passkey: %v", e)
	}

	// A change worked out from a read someone else's change has overtaken
	// is refused (the bank list is read between the two, as a slow request
	// would be overtaken).
	list := w.srv.BillBanks
	w.srv.BillBanks = func(ctx context.Context, tenant uuid.UUID, c string) ([]map[string]any, error) {
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE saved_objects SET updated_at = updated_at + interval '1 second' WHERE id = $1`, zee); err != nil {
			return nil, err
		}
		return list(ctx, tenant, c)
	}
	// The same bank sent again (as the forms send it): not a change of the
	// account, so no passkey, but refused all the same.
	if e := owner.must(409, "PUT", "/v1/saved/"+zee, map[string]any{"title": "Zee Rice", "bank_name": "Zenith Bank", "bank_code": "000015",
		"code_from": "iswallet/taskiem"}); !strings.Contains(fmt.Sprint(e["error"]), "changed this account just now") {
		t.Fatalf("a stale change: %v", e)
	}
	w.srv.BillBanks = list
	if got := owner.must(200, "GET", "/v1/saved/"+zee, nil); got["title"] != "Zee" {
		t.Fatalf("the stale change was written: %v", got)
	}

	// A bank the wallet does not list by that name: saved, with why, kept
	// until it changes and not asked about again.
	x = owner.must(201, "POST", "/v1/saved", map[string]any{"name": "kuda", "title": "Kuda", "bank_name": "Kuda Microfinance Bank", "account_number": "3333333333"})
	k := x["saved"].(map[string]any)
	if p := k["check"].(map[string]any)["problem"]; p == nil || !strings.Contains(fmt.Sprint(p), "does not list Kuda Microfinance Bank") {
		t.Fatalf("lasting problem: %v", x)
	}
	if l := owner.must(200, "GET", "/v1/saved", nil); l["can_check"] != false {
		t.Fatalf("an account with a lasting problem is offered for checking: %v", l)
	}
	asked = len(rails.asked)
	owner.must(200, "POST", "/v1/saved/check", nil)
	if len(rails.asked) != asked {
		t.Fatalf("a lasting problem was asked about again")
	}
	// Changing its bank is a change of the account: checked, then a passkey.
	if e := owner.must(403, "PUT", "/v1/saved/"+k["id"].(string), map[string]any{"bank_name": "Zenith Bank"}); e["step_up"] != "passkey" {
		t.Fatalf("a bank change without a passkey: %v", e)
	}

	// The list is the checking connection's own (its list_banks), with no
	// stand-in.
	w.srv.BillBanks = nil
	rails.listing = []any{map[string]any{"code": "000013", "name": "Guaranty Trust Bank"}}
	if b := owner.must(200, "GET", "/v1/banks", nil); b["from"] != "iswallet/taskiem" || len(b["banks"].([]any)) != 1 {
		t.Fatalf("the wallet's own list: %v", b)
	}
	var problem *string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT check_problem FROM saved_objects WHERE id = $1`, k["id"]).Scan(&problem); err != nil || problem == nil {
		t.Fatalf("problem kept: %v %v", problem, err)
	}
}
