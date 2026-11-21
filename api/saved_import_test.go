package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func stringOf(v any) string { return fmt.Sprint(v) }

// Saved things from a file (saved objects S3, §9.6): the plan first, each
// thing with its masked hint; saved with the plan's hash, each as one added
// by hand; a file with a bad line, a name saved already or a changed file
// saves nothing; only those who manage saved things import.
func TestSavedImport(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@savedimport.test")
	viewer := w.member(t, owner, "vic@savedimport.test", "viewer")
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "mum", "title": "Mum", "kind": "person", "details": map[string]any{"phone": "0803 111 2222"}})

	file := "Title,Bank,Account number,Phone,Street,Area\n" +
		"Dangote Cement,GTBank,0123456789,,,\n" +
		"Bola Stores,,,0803 123 4567,,\n" +
		"Apapa warehouse,,,,4 Creek Road,Apapa\n"
	viewer.must(403, "POST", "/v1/saved/import", map[string]any{"csv": file})
	plan := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file})
	rows := plan["rows"].([]any)
	if plan["saved"] != false || len(rows) != 3 || plan["total"] != float64(3) {
		t.Fatalf("plan: %v", plan)
	}
	if r := rows[0].(map[string]any); r["name"] != "dangote-cement" || r["kind"] != "bank_account" || r["hint"] != "GTBank ••6789" {
		t.Fatalf("an account: %v", r)
	}
	if strings.Contains(stringOf(plan), "0123456789") || strings.Contains(stringOf(plan), "1234567") {
		t.Fatalf("the plan shows a number in full: %v", plan)
	}
	if got := owner.must(200, "GET", "/v1/saved", nil)["saved"].([]any); len(got) != 1 {
		t.Fatalf("the plan saved: %d", len(got))
	}
	// Another file than the one shown.
	owner.must(409, "POST", "/v1/saved/import", map[string]any{"csv": file + "Tolu,,,0803 999 0000,,\n", "confirm": plan["hash"]})
	done := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file, "confirm": plan["hash"]})
	if done["saved"] != true || len(done["added"].([]any)) != 3 || len(done["failed"].([]any)) != 0 || len(done["left"].([]any)) != 0 {
		t.Fatalf("saved: %v", done)
	}
	// No payments connection: the account is saved, its name not checked,
	// and the answer says how to check it.
	if un := done["unchecked"].([]any); len(un) != 1 || un[0].(map[string]any)["name"] != "dangote-cement" || un[0].(map[string]any)["line"] != float64(2) ||
		!strings.Contains(stringOf(un[0].(map[string]any)["problem"]), "connect a payments account") ||
		!strings.Contains(stringOf(done["check_words"]), "1 account's name is not checked yet") {
		t.Fatalf("unchecked: %v", done)
	}
	got := owner.must(200, "GET", "/v1/saved", nil)["saved"].([]any)
	if len(got) != 4 {
		t.Fatalf("after: %d", len(got))
	}
	if r := owner.must(200, "GET", "/v1/saved/resolve?name=apapa-warehouse", nil); !strings.Contains(stringOf(r), "Creek Road") {
		t.Errorf("the place: %v", r)
	}

	// Again: its things are saved already as the same kinds, so each is
	// skipped, and nothing is left to save.
	again := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file})
	if again["total"] != float64(0) || len(again["rows"].([]any)) != 3 {
		t.Fatalf("again: %v", again)
	}
	for _, x := range again["rows"].([]any) {
		if r := x.(map[string]any); r["skip"] != true || !strings.Contains(stringOf(r["warning"]), "is saved already: skipped") {
			t.Fatalf("again: %v", r)
		}
	}
	// A name saved as another kind is a problem; and a bad line.
	other := owner.must(422, "POST", "/v1/saved/import", map[string]any{"csv": "title,street,area\nMum,4 Creek Road,Apapa\n"})
	if ps := other["problems"].([]any); len(ps) != 1 || !strings.Contains(ps[0].(map[string]any)["message"].(string), "@mum is saved already as another kind") {
		t.Fatalf("another kind: %v", other)
	}
	bad := owner.must(422, "POST", "/v1/saved/import", map[string]any{"csv": "title,account_number,bank\nTolu Bello,12345,GTBank\n"})
	if ps := bad["problems"].([]any); len(ps) != 1 || ps[0].(map[string]any)["line"] != float64(2) || bad["code"] != "import_unreadable" {
		t.Fatalf("a bad line: %v", bad)
	}
}

// A file saved in part is sent again: what is saved is skipped, the rest
// saved, the skipped said. A file over the limit is refused in words; one
// at the limit, its line breaks doubled as JSON escapes them, is read.
func TestSavedImportAgain(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@savedimport2.test")
	file := "Title,Phone\nBola Stores,0803 123 4567\nChi Stores,0803 123 4568\n"
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bola-stores", "title": "Bola Stores", "kind": "person", "details": map[string]any{"phone": "0803 123 4567"}})
	plan := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file})
	rows := plan["rows"].([]any)
	if plan["total"] != float64(1) || rows[0].(map[string]any)["skip"] != true || rows[1].(map[string]any)["skip"] != nil {
		t.Fatalf("plan: %v", plan)
	}
	done := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file, "confirm": plan["hash"]})
	if a, s := done["added"].([]any), done["skipped"].([]any); len(a) != 1 || a[0] != "chi-stores" || len(s) != 1 || s[0] != "bola-stores" {
		t.Fatalf("saved: %v", done)
	}

	tooBig := owner.must(422, "POST", "/v1/saved/import", map[string]any{"csv": "title,phone\n" + strings.Repeat("a", 300<<10)})
	if tooBig["code"] != "import_unreadable" || tooBig["error"] != "The file is too big: up to 256 KB." {
		t.Fatalf("too big: %v", tooBig)
	}
	body := owner.must(422, "POST", "/v1/saved/import", map[string]any{"csv": "title,phone\n" + strings.Repeat("\"", 600<<10)})
	if body["code"] != "import_unreadable" || body["error"] != "The file is too big: up to 256 KB." {
		t.Fatalf("too big a body: %v", body)
	}
	// Near 256 KB of blank lines: over 500 KB as JSON, and read.
	big := "title,phone\n" + strings.Repeat("\n", 250<<10) + "Dayo,0803 123 4569\n"
	if p := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": big}); p["total"] != float64(1) || p["rows"].([]any)[0].(map[string]any)["line"] != float64(250<<10+2) {
		t.Fatalf("a big file: %v", p)
	}
}

// Banks in a file: named as the bank list names them (the short name in
// the hint), a bank the list does not know by that name a warning, not a
// refusal; each account's name checked as it is saved, those past the
// per-minute limit saved unchecked and said.
func TestSavedImportBanks(t *testing.T) {
	w, owner := savedWorld(t)
	rails := &savedRails{names: map[string]string{}}
	w.srv.AccountRails = rails
	var b strings.Builder
	b.WriteString("Title,Bank,Account No.\n")
	b.WriteString("Ada Foods,GTB,0100000000\n")
	for i := 1; i <= 30; i++ {
		n := fmt.Sprintf("01%08d", i)
		rails.names[n] = fmt.Sprintf("SUPPLIER %d", i)
		bank := "GTBank"
		if i%2 == 0 {
			bank = "Zenith"
		}
		fmt.Fprintf(&b, "Supplier %d,%s,%s\n", i, bank, n)
	}
	file := b.String()
	plan := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file})
	rows := plan["rows"].([]any)
	if plan["total"] != float64(31) || len(rows) != 31 {
		t.Fatalf("plan: %v", plan)
	}
	if r := rows[0].(map[string]any); r["warning"] != "GTB is not a bank Taskiem knows by that name: its name will not be checked." || r["hint"] != "GTB ••0000" {
		t.Fatalf("an unknown bank: %v", r)
	}
	if r := rows[1].(map[string]any); r["warning"] != nil || r["hint"] != "GTBank ••0001" {
		t.Fatalf("a known bank: %v", r)
	}
	if r := rows[2].(map[string]any); r["warning"] != nil || r["hint"] != "Zenith Bank ••0002" {
		t.Fatalf("a known bank: %v", r)
	}
	done := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file, "confirm": plan["hash"]})
	if len(done["added"].([]any)) != 31 || len(done["failed"].([]any)) != 0 {
		t.Fatalf("saved: %v", done)
	}
	// The unknown bank's account unchecked for good; of the 30 known, the
	// name check's limit (30 a minute, the unknown bank's turn one of
	// them) leaves the last unchecked for now.
	un := done["unchecked"].([]any)
	if len(un) != 2 || un[0].(map[string]any)["name"] != "ada-foods" || !strings.Contains(stringOf(un[0].(map[string]any)["problem"]), "does not list GTB") ||
		un[1].(map[string]any)["name"] != "supplier-30" || !strings.Contains(stringOf(un[1].(map[string]any)["problem"]), "checked again later") ||
		!strings.Contains(stringOf(done["check_words"]), "2 accounts' names are not checked yet: Check names on the Saved page") {
		t.Fatalf("unchecked: %v", done)
	}
	// Saved with the list's name and code: asked by its code.
	if len(rails.asked) != 29 || rails.asked[0]["bank_code"] != "000013" || rails.asked[1]["bank_code"] != "000015" {
		t.Fatalf("asked: %d %v", len(rails.asked), rails.asked)
	}
	if r := owner.must(200, "GET", "/v1/saved/resolve?name=supplier-1", nil); !strings.Contains(stringOf(r), "GUARANTY TRUST BANK") {
		t.Errorf("the bank's name: %v", r)
	}
}

// After a new customer's first order (saved objects S3): the order says it
// is their first, and its customer is saved as a person, their details read
// on the server; again, the name is taken.
func TestSavedFromOrderCustomer(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ref := w.order(t, "none")
	if got := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil); got["customer_orders"] != float64(1) {
		t.Fatalf("first order: %v", got["customer_orders"])
	}
	if got := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil); got["customer_saved"] != "" {
		t.Fatalf("not saved yet: %v", got["customer_saved"])
	}
	x := ada.must(201, "POST", "/v1/storefront/orders/"+ref+"/save-customer", map[string]any{})["saved"].(map[string]any)
	if got := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil); got["customer_saved"] != "bola-ade" {
		t.Fatalf("saved: %v", got["customer_saved"])
	}
	if x["name"] != "bola-ade" || x["title"] != "Bola Ade" || x["kind"] != "person" {
		t.Fatalf("saved: %v", x)
	}
	if r := stringOf(ada.must(200, "GET", "/v1/saved/resolve?name=bola-ade", nil)); !strings.Contains(r, "12 Admiralty Way") {
		t.Errorf("its address: %s", r)
	}
	ada.must(409, "POST", "/v1/storefront/orders/"+ref+"/save-customer", map[string]any{})
	ada.must(201, "POST", "/v1/storefront/orders/"+ref+"/save-customer", map[string]any{"name": "bola"})
}
