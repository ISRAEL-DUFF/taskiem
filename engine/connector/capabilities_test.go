package connector

import (
	"errors"
	"strings"
	"testing"
)

// railManifest is a small rail with a payout, a lookup and a webhook;
// each case below breaks one thing in its capabilities.
const railManifest = `
manifest: connector/v1
id: rail
version: 1.0.0
name: Rail
category: payments
kind: rail
auth:
  type: api_key
  fields: [{ key: secret_key, label: Secret key, secret: true }]
base_url: https://api.rail.example
actions:
  balance:
    title: Balance
    class: read
    input: { type: object, properties: {} }
    output: { type: object, properties: { available: { type: integer } } }
  payout:
    title: Pay out
    class: idempotent_write
    idempotency: { field: reference, encoding: base32_lower, length: 32, prefix: "tsk_" }
    input:
      type: object
      required: [amount, account_number, bank_code, wallet_id]
      properties:
        amount: { type: integer }
        account_number: { type: string }
        bank_code: { type: string }
        name: { type: string }
        wallet_id: { type: string }
        reference: { type: string }
        note: { type: string }
    output: { type: object, properties: { id: { type: string }, status: { type: string }, name: { type: string } } }
    pii:
      - { field: account_number, category: account_number }
      - { field: name, category: name }
  send:
    title: Send without a key
    class: unsafe_write
    input: { type: object, properties: { amount: { type: integer } } }
    output: { type: object, properties: { id: { type: string } } }
  lookup:
    title: Look up
    class: read
    input: { type: object, required: [id], properties: { id: { type: string } } }
    output: { type: object, properties: { status: { type: string } } }
triggers:
  hook:
    type: webhook
    verify: { scheme: hmac_sha256, header: x-sig, secret_field: secret_key }
    events: [paid, failed]
    event_type: =body.event
    correlation: =body.id
`

const goodCapabilities = `
capabilities:
  balance:
    action: balance
    input: {}
    output: { available_kobo: "=output.available", currency: NGN }
    unsupported: [account]
    fixed: { currency: NGN }
  pay_bank:
    action: payout
    input:
      amount: "=input.amount_kobo"
      account_number: "=input.account_number"
      bank_code: "=input.bank_code"
      name: "=input.account_name"
      wallet_id: "=input.source"
      note: "=input.narration"
    output: { payout_id: "=output.id", status: "=output.status == 'ok' ? 'settled' : 'pending'" }
    requires: [source]
    fixed: { currency: NGN }
    settles_by: { trigger: hook, lookup: get_payout }
  get_payout:
    action: lookup
    input: { id: "=input.payout_id" }
    output: { status: "=output.status == 'ok' ? 'settled' : 'pending'" }
events:
  payout.settled: { trigger: hook, when: "=body.event == 'paid'", fields: { payout_id: "=body.id" } }
  payout.failed: { trigger: hook, when: "=body.event == 'failed'", fields: { payout_id: "=body.id", reason: "=body.?why" } }
`

func TestCapabilitiesValid(t *testing.T) {
	m, probs := Parse([]byte(railManifest + goodCapabilities))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if got := m.Capabilities["pay_bank"].Plan(); len(got) != 1 || got[0].Action != "payout" {
		t.Fatalf("plan %+v", got)
	}
}

func TestCapabilitiesRefused(t *testing.T) {
	for _, c := range []struct {
		name, from, to, want string
	}{
		{"unknown capability", "  get_payout:\n", "  get_payouts:\n", `/capabilities/get_payouts: not a Taskiem capability`},
		{"unknown action", "action: lookup", "action: look", `unknown action "look"`},
		{"wrong class", "  balance:\n    action: balance", "  balance:\n    action: payout", `only reads; its actions must be read actions`},
		{"read for a write", "  pay_bank:\n    action: payout", "  pay_bank:\n    action: lookup", `changes something; one of its actions must be a write`},
		{"unsafe undeclared", "  pay_bank:\n    action: payout", "  pay_bank:\n    action: send", `"send" is unsafe_write, but pay_bank needs idempotent_write or reconcilable_write`},
		{"required action input", `      bank_code: "=input.bank_code"` + "\n", "", `"payout" needs bank_code`},
		{"unknown action input", `note: "=input.narration"`, `memo: "=input.narration"`, `"payout" has no input "memo"`},
		{"idempotency field", `note: "=input.narration"`, `note: "=input.narration"` + "\n      reference: x", `the idempotency field is Taskiem's to set`},
		{"bad expression", `"=input.amount_kobo"`, `"=input.amount_kobo +"`, `/capabilities/pay_bank/input/amount`},
		{"unknown root", `"=input.amount_kobo"`, `"=output.amount_kobo"`, `output cannot be read here`},
		{"missing output", `output: { payout_id: "=output.id", status:`, `output: { status:`, `pay_bank must answer payout_id`},
		{"extra output", `output: { payout_id: "=output.id",`, `output: { payout_id: "=output.id", total: 1,`, `pay_bank has no output "total"`},
		{"unused input", `      note: "=input.narration"` + "\n", "", `input narration is never used`},
		{"requires", "    requires: [source]\n", "", `reads the optional input source; list it under requires`},
		{"requires a required one", "requires: [source]", "requires: [source, narration]", `"narration" is a required input of pay_bank already`},
		{"pii", `name: "=input.account_name"`, `note2: "=input.account_name"`, `"payout" has no input "note2"`},
		{"pii lost", `note: "=input.narration"`, `note: "=input.narration + input.account_name"`, `carries account_name (personal: name), but "payout" does not mark note as pii`},
		{"no settles_by", "    settles_by: { trigger: hook, lookup: get_payout }\n", "", `a payout that can be pending says how it settles`},
		{"settles_by trigger", "trigger: hook, lookup", "trigger: hooks, lookup", `unknown trigger "hooks"`},
		{"settles_by lookup", "lookup: get_payout }", "lookup: balance }", `must be get_payout, declared by this rail`},
		{"settles_by events", "  payout.failed: { trigger: hook", "  payout.reversed: { trigger: hook", `"hook" must produce payout.failed`},
		{"unknown event", "  payout.failed:", "  payout.lost:", `/events/payout.lost: not a Taskiem event`},
		{"event field", `fields: { payout_id: "=body.id" }`, `fields: { id: "=body.id" }`, `payout.settled has no such field`},
		{"event needs", `fields: { payout_id: "=body.id" }`, `fields: { amount_kobo: 1 }`, `payout.settled needs payout_id`},
		{"event trigger", "payout.settled: { trigger: hook", "payout.settled: { trigger: hooks", `unknown trigger "hooks"`},
		{"not a rail", "kind: rail", "kind: tool", `only a rail (kind: rail) declares capabilities`},
	} {
		src := railManifest + goodCapabilities
		if !strings.Contains(src, c.from) {
			t.Fatalf("%s: %q is not in the manifest", c.name, c.from)
		}
		src = strings.Replace(src, c.from, c.to, 1)
		_, probs := Parse([]byte(src))
		if !strings.Contains(strings.Join(probs, "\n"), c.want) {
			t.Errorf("%s: want %q among %q", c.name, c.want, probs)
		}
	}
}

func TestCapabilityMapping(t *testing.T) {
	m, probs := Parse([]byte(railManifest + goodCapabilities))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	in, err := m.CapabilityInput("pay_bank", map[string]any{"amount_kobo": 5000, "bank_code": "058", "account_number": "0123456789", "account_name": "Ada Obi", "narration": "Salary", "source": "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if in["currency"] != "NGN" {
		t.Fatalf("default currency: %v", in)
	}
	act, err := m.CapabilityStepInput("pay_bank", 0, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if act["amount"] != int64(5000) && act["amount"] != 5000 || act["wallet_id"] != "w1" || act["name"] != "Ada Obi" {
		t.Fatalf("action input %v", act)
	}
	out, err := m.CapabilityOutput("pay_bank", in, map[string]any{"id": "p1", "status": "ok"}, nil)
	if err != nil || out["payout_id"] != "p1" || out["status"] != "settled" {
		t.Fatalf("output %v, %v", out, err)
	}
	for _, bad := range []map[string]any{
		{"amount_kobo": 5000, "bank_code": "058", "account_number": "1", "account_name": "A", "narration": "n"},                                    // no source
		{"amount_kobo": 5000, "bank_code": "058", "account_number": "1", "account_name": "A", "narration": "n", "source": "w1", "currency": "USD"}, // fixed
		{"amount_kobo": 0, "bank_code": "058", "account_number": "1", "account_name": "A", "narration": "n", "source": "w1"},                       // schema
	} {
		if _, err := m.CapabilityInput("pay_bank", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := m.CapabilityInput("balance", map[string]any{"account": "a2"}); err == nil {
		t.Error("an unsupported input is accepted")
	}
	name, fields, err := m.EventOf("hook", map[string]any{"body": map[string]any{"event": "failed", "id": "p1"}, "headers": map[string]any{}})
	if err != nil || name != "payout.failed" || fields["payout_id"] != "p1" || len(fields) != 1 {
		t.Fatalf("event %s %v %v", name, fields, err)
	}
	if _, _, err := m.EventOf("hook", map[string]any{"body": map[string]any{"event": "other", "id": "p1"}}); !errors.Is(err, ErrNoEvent) {
		t.Fatalf("other delivery: %v", err)
	}
}

// transferCapability adds a one-time account to the rail.
const transferAction = `  account:
    title: One-time account
    class: idempotent_write
    idempotency: { field: reference, encoding: base32_lower, length: 32, prefix: "tsk_" }
    input:
      type: object
      required: [amount, email]
      properties:
        amount: { type: integer }
        email: { type: string }
        minutes: { type: integer }
        reference: { type: string }
    output: { type: object, properties: { reference: { type: string }, number: { type: string }, bank: { type: string }, amount: { type: integer }, until: { type: string } } }
    pii:
      - { field: email, category: email }
      - { field: output.number, category: account_number }
`

const transferCapability = `  collect_transfer:
    action: account
    input: { amount: "=input.amount_kobo", email: "=input.customer.email", minutes: "=input.?expires_minutes" }
    output: { payment_id: "=output.reference", account_number: "=output.number", bank_name: "=output.bank", amount_kobo: "=output.amount", expires_at: "=output.until" }
    requires: [customer.email]
    ignores: [customer.name, customer.phone, customer.bvn, customer.nin, description]
    fixed: { currency: NGN }
    transfer: { on_late: returned, on_wrong_amount: returned, min_minutes: 15, max_minutes: 480, default_minutes: 30 }
`

func TestCollectTransferFacts(t *testing.T) {
	base := strings.Replace(railManifest, "triggers:\n", transferAction+"triggers:\n", 1) +
		strings.Replace(goodCapabilities, "events:\n", transferCapability+"events:\n", 1)
	m, probs := Parse([]byte(base))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if f := m.Capabilities["collect_transfer"].Transfer; f == nil || f.OnLate != "returned" || f.MaxMinutes != 480 {
		t.Fatalf("facts %+v", f)
	}
	for _, c := range []struct{ name, from, to, want string }{
		{"missing", "    transfer: { on_late: returned, on_wrong_amount: returned, min_minutes: 15, max_minutes: 480, default_minutes: 30 }\n", "", `collect_transfer must say what the rail does with late and wrong-amount transfers`},
		{"late", "on_late: returned", "on_late: kept", `on_late`},
		{"wrong amount", "on_wrong_amount: returned", "on_wrong_amount: kept", `on_wrong_amount`},
		{"range", "min_minutes: 15, max_minutes: 480", "min_minutes: 15, max_minutes: 10", `max_minutes at least min_minutes`},
		{"default", "default_minutes: 30", "default_minutes: 600", `default_minutes: 600 is outside 15 to 480`},
		{"elsewhere", "    fixed: { currency: NGN }\n  pay_bank:", "    fixed: { currency: NGN }\n    transfer: { on_late: returned, on_wrong_amount: returned, min_minutes: 1, max_minutes: 2 }\n  pay_bank:", `only collect_transfer says how transfers are handled`},
	} {
		if !strings.Contains(base, c.from) {
			t.Fatalf("%s: %q is not in the manifest", c.name, c.from)
		}
		_, probs := Parse([]byte(strings.Replace(base, c.from, c.to, 1)))
		if !strings.Contains(strings.Join(probs, "\n"), c.want) {
			t.Errorf("%s: want %q among %q", c.name, c.want, probs)
		}
	}
}
