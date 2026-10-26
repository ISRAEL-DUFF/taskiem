package schools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	conn "github.com/israel-duff/taskiem/connectors/schools"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

// What the fakes were asked to send.
var (
	receiptsSent []string
	accountsSent []string // student:key
)

func (o *ops) SendReceiptOnce(_ context.Context, _ uuid.UUID, idOrNumber, _ string) (schools.ReceiptSent, error) {
	receiptsSent = append(receiptsSent, idOrNumber)
	switch idOrNumber {
	case "RCT-2026T1-0007", "0192f0a8-0000-7000-8000-0000000000a1":
		return schools.ReceiptSent{Receipt: schools.Receipt{ID: uuid.MustParse("0192f0a8-0000-7000-8000-0000000000a1"), Number: "RCT-2026T1-0007", StudentID: ebube, AdmissionNumber: "GCA/001",
			Name: "Ebube Eze", InvoiceNumber: "INV-0007", AmountKobo: 4_000_000, BalanceAfterKobo: 7_800_000}, Sent: schools.FamilySent{Channels: map[string]int{"whatsapp": 1, "sms": 1}}}, nil
	case "RCT-2026T1-0008":
		return schools.ReceiptSent{}, errors.Join(schools.ErrState, errors.New("receipt RCT-2026T1-0008 is voided: the payment was undone"))
	}
	return schools.ReceiptSent{}, schools.ErrNotFound
}

func (o *ops) SendAccountNumberOnce(_ context.Context, _ uuid.UUID, idOrNumber, key, _ string) (schools.AccountSent, error) {
	accountsSent = append(accountsSent, idOrNumber+":"+key)
	switch idOrNumber {
	case "GCA/001":
		fam := uuid.New()
		return schools.AccountSent{Student: schools.Student{ID: ebube, AdmissionNumber: "GCA/001", Name: "Ebube Eze", FamilyID: &fam},
			Account: schools.StudentAccount{Connection: "iswallet/main", AccountNumber: "9912345678", BankName: "Wema Bank", AccountName: "GREENFIELD / EBUBE EZE"},
			Sent:    schools.FamilySent{Channels: map[string]int{"sms": 1}}}, nil
	case "GCA/002":
		return schools.AccountSent{Student: schools.Student{ID: uuid.New(), AdmissionNumber: "GCA/002", Name: "Chidi Eze"},
			Account: schools.StudentAccount{Connection: "iswallet/main", AccountNumber: "9912345679", BankName: "Wema Bank"}, Sent: schools.FamilySent{Channels: map[string]int{}, Nobody: true}}, nil
	case "GCA/003":
		return schools.AccountSent{}, errors.Join(schools.ErrState, errors.New("student GCA/003 has no account number yet"))
	}
	return schools.AccountSent{}, schools.ErrNotFound
}

// send_receipt: by id or number, under the step's key, answering the
// receipt and the channels reached; a voided or unknown receipt fails
// the step plainly.
func TestSendReceipt(t *testing.T) {
	c := conn.New(&ops{})
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	if _, err := c.Actions["send_receipt"].Execute(ctx, connector.Request{Input: map[string]any{"receipt": "RCT-2026T1-0007"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no key: %v", err)
	}
	if _, err := c.Actions["send_receipt"].Execute(ctx, connector.Request{Input: map[string]any{}, IdempotencyKey: "tsk_step"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no receipt: %v", err)
	}
	r, err := c.Actions["send_receipt"].Execute(ctx, connector.Request{Input: map[string]any{"receipt": " RCT-2026T1-0007 "}, IdempotencyKey: "tsk_step"})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["number"] != "RCT-2026T1-0007" || out["receipt_id"] != "0192f0a8-0000-7000-8000-0000000000a1" || out["admission_number"] != "GCA/001" || out["invoice_number"] != "INV-0007" ||
		out["amount_kobo"] != int64(4_000_000) || out["nobody"] != false || out["channels"].(map[string]any)["whatsapp"] != 1 || out["channels"].(map[string]any)["sms"] != 1 {
		t.Fatalf("sent %v", out)
	}
	if _, named := out["name"]; named {
		t.Errorf("the output names the student: %v", out)
	}
	if receiptsSent[len(receiptsSent)-1] != "RCT-2026T1-0007" {
		t.Errorf("asked for %v", receiptsSent)
	}
	for _, bad := range []string{"RCT-2026T1-0008", "RCT-2026T1-9999"} {
		if _, err := c.Actions["send_receipt"].Execute(ctx, connector.Request{Input: map[string]any{"receipt": bad}, IdempotencyKey: "tsk_step2"}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// send_account_number: once for the step's key, answering the account
// and the channels; a student in no family reaches nobody with the
// reason; one without a number fails the step.
func TestSendAccountNumber(t *testing.T) {
	c := conn.New(&ops{})
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	if _, err := c.Actions["send_account_number"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/001"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no key: %v", err)
	}
	if _, err := c.Actions["send_account_number"].Execute(ctx, connector.Request{Input: map[string]any{}, IdempotencyKey: "tsk_step"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no student: %v", err)
	}
	r, err := c.Actions["send_account_number"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/001"}, IdempotencyKey: "tsk_step"})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["student_id"] != ebube.String() || out["admission_number"] != "GCA/001" || out["account_number"] != "9912345678" || out["bank_name"] != "Wema Bank" ||
		out["account_name"] != "GREENFIELD / EBUBE EZE" || out["connection"] != "iswallet/main" || out["nobody"] != false || out["channels"].(map[string]any)["sms"] != 1 {
		t.Fatalf("sent %v", out)
	}
	if _, has := out["reason"]; has {
		t.Errorf("a reason with someone reached: %v", out)
	}
	if accountsSent[len(accountsSent)-1] != "GCA/001:step:tsk_step" {
		t.Errorf("asked for %v", accountsSent)
	}
	r, err = c.Actions["send_account_number"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/002"}, IdempotencyKey: "tsk_step2"})
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["nobody"] != true || out["reason"] != "the student is in no family yet" || out["account_number"] != "9912345679" {
		t.Fatalf("no family %v", out)
	}
	for _, bad := range []string{"GCA/003", "GCA/999"} {
		if _, err := c.Actions["send_account_number"].Execute(ctx, connector.Request{Input: map[string]any{"student": bad}, IdempotencyKey: "tsk_step3"}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}
