package schools

import (
	"context"
	"fmt"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Messages to a family (docs/schools.md#receipts, #account-numbers): a
// receipt and a student's account number, each sent through the school's
// one family path (schools.SendFamily): on each guardian's chat app, as
// its own WhatsApp template outside the 24-hour window (taskiem_receipt,
// taskiem_account_number), otherwise by SMS, always with a fresh family
// page link. Like tell_plan_decision, each answers the channels reached.

// sendReceipt sends the family a receipt, once per receipt whoever asks.
func (a *actions) sendReceipt(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("send_receipt runs as a workflow step, with its key: %w", effects.ErrFatal)
	}
	receipt, _ := req.Input["receipt"].(string)
	if strings.TrimSpace(receipt) == "" {
		return connector.Response{}, fmt.Errorf("the receipt's id or number is required: %w", effects.ErrFatal)
	}
	r, err := a.ops.SendReceiptOnce(ctx, c.Tenant, strings.TrimSpace(receipt), "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"receipt_id": r.Receipt.ID.String(), "number": r.Receipt.Number, "student_id": r.Receipt.StudentID.String(),
		"admission_number": r.Receipt.AdmissionNumber, "invoice_number": r.Receipt.InvoiceNumber, "amount_kobo": r.Receipt.AmountKobo,
		"channels": channelsOut(r.Sent), "nobody": r.Sent.Nobody}}, nil
}

// sendAccountNumber sends the family a student's account number, once
// for the step.
func (a *actions) sendAccountNumber(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("send_account_number runs as a workflow step, with its key: %w", effects.ErrFatal)
	}
	student, _ := req.Input["student"].(string)
	if strings.TrimSpace(student) == "" {
		return connector.Response{}, fmt.Errorf("a student's id or admission number is required: %w", effects.ErrFatal)
	}
	r, err := a.ops.SendAccountNumberOnce(ctx, c.Tenant, strings.TrimSpace(student), "step:"+req.IdempotencyKey, "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := map[string]any{"student_id": r.Student.ID.String(), "admission_number": r.Student.AdmissionNumber, "connection": r.Account.Connection,
		"account_number": r.Account.AccountNumber, "bank_name": r.Account.BankName, "channels": channelsOut(r.Sent), "nobody": r.Sent.Nobody}
	if r.Account.AccountName != "" {
		out["account_name"] = r.Account.AccountName
	}
	if r.Student.FamilyID == nil {
		out["reason"] = "the student is in no family yet"
	}
	return connector.Response{Output: out}, nil
}

// channelsOut is who was reached by channel, as a step's output.
func channelsOut(s schools.FamilySent) map[string]any {
	out := map[string]any{}
	for k, v := range s.Channels {
		out[k] = v
	}
	return out
}
