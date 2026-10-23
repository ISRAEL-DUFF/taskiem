# Payrolla × Taskiem: go-live checklist

Tick each item before the first customer goes live. Tests can run against the test company if you have one, or the pilot customer's account otherwise.

## Payrolla confirms

- [ ] The Connect API is live at `https://synledger.name.ng/payrolla/api/v1`, and `https://synledger.name.ng/payrolla/api/v1/connect/openapi.yaml` names that address.
- [ ] The embed SDK loads from `https://synledger.name.ng/payrolla/widget/sdk/payroller-embed.umd.js`.
- [ ] The pilot customer's owner has connected Taskiem with the scopes the first workflows need.
- [ ] The pilot owner has switched on payslip links, if workflow 3 is in the pilot.
- [ ] The pilot owner has set up embedding with Taskiem's exact origins, if embedding is in the pilot.
- [ ] Every approver at the pilot customer can confirm payments: Settings → Connected apps → Payment confirmation shows no **Set up** left.
- [ ] Our payroll-day contact is agreed: duffisrael29@gmail.com.

## Taskiem confirms

**Connection**
- [ ] Taskiem's Connections screen stores the key encrypted, and tests it with `GET /connect/company`.
- [ ] A `401 API_KEY_INVALID` marks the connection disconnected instead of retrying.

**Webhooks**
- [ ] Signature verification passes the test vector in `api/webhooks.md`, and rejects a changed body and a timestamp older than 5 minutes.
- [ ] Taskiem creates the subscription on connect and deletes it on disconnect. A `ping` from `POST /connect/webhooks/{id}/test` arrives and verifies.
- [ ] Events are de-duplicated on `id`. A repeated delivery doesn't run a workflow twice.
- [ ] Catch-up from `GET /connect/events?after=` runs after any outage, and works from the last event id stored.

**Workflows**
- [ ] Approval: `run.computed` sends the `approval_url`, and the approver can open it and approve in Payrolla.
- [ ] Payslips: `payslip.issued` creates the link, and handles `PAYSLIP_LINKS_DISABLED` and `PAYSLIP_NOT_FINAL` with a clear message to the customer.
- [ ] Employee consent to WhatsApp messages is recorded before any employee is messaged.

**Embedding** (if it's in the pilot)
- [ ] Launch tokens are minted server-side only.
- [ ] The frame loads on each production origin.
- [ ] An approver approves inside the frame with a code.
- [ ] Revoking the key in Payrolla closes the frame on its next action.

## Together

- [ ] One full payroll at the pilot customer, watched by both teams: computed → approved → locked → paid → payslips, with every event received once.
- [ ] Contacts and escalation for payroll days are exchanged.
- [ ] The pilot's length is agreed, and when the next customers are added.
