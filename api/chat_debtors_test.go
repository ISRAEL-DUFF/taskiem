package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// The debtor book in chat (decision 0060, R5): "Ada owes 5,000" records a
// notebook debt after the owner's credit terms and a yes; "Ada paid 2,000
// cash" counts cash towards it, oldest first, the extra kept as credit;
// "who owes me?" lists one balance per customer; a transfer into an
// account Taskiem does not see is entered under that account's name; two
// customers of one name are picked by number; a phone given later is
// kept; someone without invoice.manage is refused.
func TestChatDebtorBook(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	const on, bn, cn, vn = "+2348010000051", "+2348010000052", "+2348010000053", "+2348010000054"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "bisi@mamaput.test", "admin"), bn)
	w.bind(t, w.member(t, owner, "chidi@mamaput.test", "admin"), cn)
	w.bind(t, w.member(t, owner, "vic@mamaput.test", "viewer"), vn)
	all := func(from, text string) string {
		t.Helper()
		var out []string
		for _, m := range w.send(t, from, whatsapptest.Text(text)) {
			out = append(out, m.Text)
		}
		return strings.Join(out, "\n")
	}
	invoice := func(number string) map[string]any {
		t.Helper()
		return owner.must(200, "GET", "/v1/invoices/"+number, nil)["invoice"].(map[string]any)
	}

	if r := w.say(t, vn, "Ada owes 5,000"); !strings.Contains(r, "invoice.manage") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, on, "who owes me?"); !strings.Contains(r, "Nobody owes you") {
		t.Errorf("nobody: %q", r)
	}

	// A new customer: the phone asked once (skipped), the owner's terms
	// asked once, then the read-back and yes.
	if r := w.say(t, on, "Ada owes 5,000"); !strings.Contains(r, "Ada is new") || !strings.Contains(r, "*skip*") {
		t.Fatalf("new customer: %q", r)
	}
	if r := w.say(t, on, "skip"); !strings.Contains(r, "How long do you give customers to pay") || !strings.Contains(r, "2. 14 days") || !strings.Contains(r, "4. No fixed time") {
		t.Fatalf("terms asked: %q", r)
	}
	r := all(on, "2")
	if !strings.Contains(r, "Customers now have 14 days") || !strings.Contains(r, "Record that Ada owes ₦5,000?") || !strings.Contains(r, "Due ") ||
		!strings.Contains(r, "Ada will owe ₦5,000 in all") {
		t.Fatalf("read back: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Recorded: Ada owes ₦5,000 (INV-0001)") {
		t.Fatalf("recorded: %q", r)
	}
	x := invoice("INV-0001")
	if x["status"] != "recorded" || x["kind"] != "credit" || x["due_on"] == nil || x["sent_at"] != nil {
		t.Fatalf("the line: %v", x)
	}
	// A second debt: the terms are not asked again.
	if r := w.say(t, on, "Ada owes 1,000 for soap"); !strings.Contains(r, "Record that Ada owes ₦1,000?") || !strings.Contains(r, "₦6,000 in all") {
		t.Fatalf("second: %q", r)
	}
	w.say(t, on, "yes")

	// Cash: counted oldest first; the money is in the log.
	if r := w.say(t, bn, "Ada paid 2,000 cash"); !strings.Contains(r, "Record ₦2,000 from Ada in cash?") || !strings.Contains(r, "Ada will owe ₦4,000") {
		t.Fatalf("paid read back: %q", r)
	}
	if r := w.say(t, bn, "yes"); !strings.Contains(r, "Recorded ₦2,000 from Ada. Ada owes ₦4,000 now.") {
		t.Fatalf("paid: %q", r)
	}
	if x := invoice("INV-0001"); x["status"] != "part_paid" || x["paid_kobo"] != float64(200000) {
		t.Fatalf("oldest first: %v", x)
	}
	var cash int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM money_entries WHERE kind = 'cash' AND direction = 'in' AND amount_kobo = 200000`).Scan(&cash); err != nil || cash != 1 {
		t.Errorf("cash in the log: %d %v", cash, err)
	}
	if r := w.say(t, bn, "who owes me"); !strings.Contains(r, "1 customer(s) owe you ₦4,000") || !strings.Contains(r, "1. Ada: ₦4,000 (2 line(s))") {
		t.Fatalf("who owes: %q", r)
	}

	// A transfer into an account Taskiem does not see: named once, entered
	// under it, counted now.
	if r := w.say(t, cn, "Ada paid 1,000 transfer"); !strings.Contains(r, "Into which account") || !strings.Contains(r, "1. Another account") {
		t.Fatalf("which account: %q", r)
	}
	if r := w.say(t, cn, "1"); !strings.Contains(r, "gtbank/shop") {
		t.Fatalf("name it: %q", r)
	}
	if r := w.say(t, cn, "GTBank Shop"); !strings.Contains(r, "small letters") {
		t.Errorf("bad name: %q", r)
	}
	if r := w.say(t, cn, "gtbank/shop"); !strings.Contains(r, "by transfer into gtbank/shop") {
		t.Fatalf("transfer read back: %q", r)
	}
	if r := w.say(t, cn, "yes"); !strings.Contains(r, "Ada owes ₦3,000 now") {
		t.Fatalf("transfer: %q", r)
	}
	var entered int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM money_entries WHERE channel = 'gtbank/shop' AND source = 'entered'`).Scan(&entered); err != nil || entered != 1 {
		t.Errorf("entered transfer: %d %v", entered, err)
	}

	// More than she owes: the extra is her credit.
	if r := w.say(t, on, "Ada paid 4,000 cash"); !strings.Contains(r, "₦1,000 more than they owe is kept as their credit") {
		t.Fatalf("over: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Ada owes ₦0 now") || !strings.Contains(r, "Credit kept for them: ₦1,000") {
		t.Fatalf("credit: %q", r)
	}
	if x := invoice("INV-0002"); x["status"] != "paid" {
		t.Errorf("both paid: %v", x)
	}

	// Names: the exact one, else a numbered pick, or someone new.
	if r := w.say(t, bn, "Ada owes 500"); !strings.Contains(r, "Record that Ada owes ₦500") {
		t.Fatalf("one Ada: %q", r)
	}
	w.say(t, bn, "cancel")
	w.say(t, bn, "Ada Obi owes 700")
	w.say(t, bn, "skip")
	w.say(t, bn, "yes")
	// The exact name is one customer; words several names start are a pick.
	if r := w.say(t, cn, "Ad owes 300"); !strings.Contains(r, "Which Ad?") || !strings.Contains(r, "1. Ada") || !strings.Contains(r, "2. Ada Obi") || !strings.Contains(r, "3. Someone new") {
		t.Fatalf("pick: %q", r)
	}
	if r := w.say(t, cn, "2"); !strings.Contains(r, "Record that Ada Obi owes ₦300?") || !strings.Contains(r, "₦1,000 in all") {
		t.Fatalf("picked: %q", r)
	}
	w.say(t, cn, "yes")

	// A statement, and a phone given later.
	if r := w.say(t, on, "balance ada obi"); !strings.Contains(r, "Ada Obi owes ₦1,000:") || !strings.Contains(r, "INV-0003: ₦700") {
		t.Fatalf("statement: %q", r)
	}
	if r := w.say(t, on, "Ada Obi's number is 0803 123 4567"); !strings.Contains(r, "Ada Obi's number is +234") || strings.Contains(r, "1234567") {
		t.Fatalf("phone: %q", r)
	}
	if c := invoice("INV-0003")["customer"].(map[string]any); c["phone"] == nil {
		t.Errorf("the phone on her line: %v", c)
	}
	if r := w.say(t, on, "credit terms 30 days"); !strings.Contains(r, "Customers now have 30 days") || !strings.Contains(r, "Apply them to the 2 debt(s)") {
		t.Fatalf("terms changed: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "2 open debt(s) now follow") {
		t.Errorf("applied: %q", r)
	}
	if r := w.say(t, vn, "who owes me?"); !strings.Contains(r, "owe you ₦1,000") {
		t.Errorf("viewer reads: %q", r)
	}

	// Her balance in one message: read back, sent once, her lines sent
	// with it, her payment link in it.
	if r := w.say(t, vn, "send Ada Obi her balance"); !strings.Contains(r, "invoice.manage") {
		t.Errorf("viewer sends: %q", r)
	}
	if r := w.say(t, on, "send Ada her balance"); !strings.Contains(r, "Ada owes nothing") {
		t.Errorf("nothing owed: %q", r)
	}
	r = w.say(t, on, "send Ada Obi her balance")
	if !strings.Contains(r, "Send Ada Obi what they owe, ₦1,000 (INV-0003, INV-0004), to •••567, with a link to pay it, all or part?") ||
		!strings.Contains(r, "2 notebook line(s) will show as sent") || strings.Contains(r, "1234567") {
		t.Fatalf("send read back: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Sent Ada Obi their balance of ₦1,000 by SMS") {
		t.Fatalf("sent: %q", r)
	}
	if x := invoice("INV-0003"); x["status"] != "sent" || x["sent_at"] == nil {
		t.Errorf("sent with it: %v", x)
	}
	var links int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM invoice_customer_links`).Scan(&links); err != nil || links != 1 {
		t.Errorf("her link: %d %v", links, err)
	}
	// Someone with no phone yet is asked for one first.
	w.say(t, on, "Bola owes 200")
	w.say(t, on, "skip")
	w.say(t, on, "yes")
	if r := w.say(t, on, "send balance to Bola"); !strings.Contains(r, "Bola has no phone number yet") {
		t.Errorf("no phone: %q", r)
	}
}

// The debtor book in Yoruba, Hausa and Igbo (R5f): the name first, then
// the language's words, matched without accents; a sentence about the
// business itself ("mo ti san …") is not a customer's.
func TestChatDebtorBookLanguages(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.YO, lang.HA, lang.IG}}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	const yn, hn, in = "+2348010000061", "+2348010000062", "+2348010000063"
	w.bind(t, owner, yn)
	w.bind(t, w.member(t, owner, "musa@adafoods.test", "admin"), hn)
	w.bind(t, w.member(t, owner, "nkem@adafoods.test", "admin"), in)
	w.say(t, yn, "language yoruba")
	w.say(t, hn, "language hausa")
	w.say(t, in, "language igbo")
	tr := func(l lang.Tag, id string, kv ...string) string { return lang.Default().Text(l, id, kv...) }

	// Yoruba: "Adé jẹ mí 5,000", the name as written.
	if r := w.say(t, yn, "Adé jẹ mí 5,000"); !strings.Contains(r, tr(lang.YO, "wa.debt.ask_phone", "name", "Adé")) {
		t.Fatalf("yo owes: %q", r)
	}
	w.say(t, yn, "skip")
	w.send(t, yn, whatsapptest.Text("2"))
	if r := w.say(t, yn, "bẹ́ẹ̀ni"); !strings.Contains(r, "INV-0001") {
		t.Fatalf("yo recorded: %q", r)
	}
	// Without accents, and paid in cash.
	if r := w.say(t, yn, "ade ti san 2,000 cash"); !strings.Contains(r, "₦2,000") || !strings.Contains(r, "Adé") {
		t.Fatalf("yo paid: %q", r)
	}
	w.say(t, yn, "beeni")
	if r := w.say(t, yn, "ta ló jẹ mí?"); !strings.Contains(r, "₦3,000") {
		t.Errorf("yo who: %q", r)
	}
	if r := w.say(t, yn, "mo ti san 2,000"); strings.Contains(r, tr(lang.YO, "wa.debt.no_one", "name", "mo")) {
		t.Errorf("the business taken for a customer: %q", r)
	}
	// Hausa and Igbo.
	if r := w.say(t, hn, "Adé yana bina 1,000"); !strings.Contains(r, "₦1,000") || !strings.Contains(r, "Adé") {
		t.Fatalf("ha owes: %q", r)
	}
	w.say(t, hn, "na'am")
	if r := w.say(t, hn, "wa ke bina"); !strings.Contains(r, "₦4,000") {
		t.Errorf("ha who: %q", r)
	}
	if r := w.say(t, in, "onye ji m ugwo"); !strings.Contains(r, "₦4,000") {
		t.Errorf("ig who: %q", r)
	}
	if r := w.say(t, in, "Adé akwụọla 4,000 cash"); !strings.Contains(r, "₦4,000") {
		t.Fatalf("ig paid: %q", r)
	}
	w.say(t, in, "ee")
	if r := w.say(t, in, "onye ji m ugwo"); strings.Contains(r, "₦4,000") {
		t.Errorf("ig after paying: %q", r)
	}
}
