# Payrolla × Taskiem: Phase C is live (one-step connect, self-service, settings and reports)

Payrolla → Taskiem · follows Phase B (`06-phase-b-response.md`)

This completes what you asked for in P7. With Phase C:
- a business connects Taskiem in one step, and can sign up for Payrolla in the same step;
- employees can change their own salary account from Taskiem, still held for an approver;
- Taskiem can read the company's payroll settings and its statutory schedules.

Nothing about money control changes: Payrolla still does every payment, and every approval, lock and payout is confirmed by the person doing it with a code from Payrolla.

The reference is `{{PAYROLLA_API}}/connect/openapi.yaml`. Its "Connecting with OAuth 2.0" section has the full detail.

## 1. Connecting in one step (OAuth 2.0)

No more copying keys. The owner clicks "Connect Payrolla" in Taskiem, allows it in Payrolla, and is sent back to you.

### What we set up for you

We register Taskiem as an app on our side. For that we need your **redirect URIs**: production, and any test ones. They must be `https`, and they must match exactly what you send.

We'll give you a `client_id` and a `client_secret`. **The secret goes through a secrets manager or a shared vault, never email, chat or a ticket**, as with keys. Keep it on your servers; the token calls must come from there.

### The flow

**1. Send the owner's browser to Payrolla:**

```
{{PAYROLLA_APP}}/oauth/authorize
  ?response_type=code
  &client_id=pyc_…
  &redirect_uri=https://…/payrolla/callback
  &scope=payroll:read employees:read employees:write payroll:write payroll:approve webhooks:manage
  &state=<random, tied to the owner's Taskiem session>
  &code_challenge=<BASE64URL(SHA256(code_verifier))>
  &code_challenge_method=S256
```

- **PKCE is required.** `code_challenge_method` must be `S256`. Keep the `code_verifier` (43–128 characters) on your server until step 3.
- **Scopes** are space-separated and within what we register Taskiem for. The owner sees each one in plain words.
- **A business that isn't on Payrolla yet:** add `company_name`, `rc_number` and `email`. Payrolla opens on sign-up with those filled in; the owner signs up, verifies their email and comes back to the same "Connect Taskiem" screen. An existing owner just signs in.

**2. The owner allows it.** They see Taskiem's name, the company it will connect to, and what it will be able to do, and confirm with their Payrolla password and authenticator code. Only a company **owner** can connect an app.

**3. Payrolla sends the browser back:**
- allowed: `https://…/payrolla/callback?code=…&state=…`
- refused: `https://…/payrolla/callback?error=access_denied&state=…`

Check that `state` is the one you sent.

**4. Swap the code for tokens, from your server, within 10 minutes:**

```http
POST {{PAYROLLA_API}}/oauth/token
Authorization: Basic base64(client_id:client_secret)
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code&code=…&redirect_uri=https://…/payrolla/callback&code_verifier=…
```

```json
{
  "access_token": "pyt_…",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "pyr_…",
  "scope": "payroll:read employees:read …",
  "company_id": "…",
  "company_name": "Amara Foods Ltd"
}
```

Unlike the rest of the API, OAuth answers are standard OAuth JSON, not wrapped in `{"data": …}`, so ordinary OAuth libraries work. Errors are `{"error", "error_description"}`.

**5. Use the access token** exactly as you used the key: `Authorization: Bearer pyt_…` on every `/connect` call. Everything from Phases A and B works unchanged: idempotency, acting users, linked approvers, codes and webhooks.

### Keeping the connection

- **Access tokens last 1 hour.** Refresh with `grant_type=refresh_token&refresh_token=pyr_…` (same endpoint, same client authentication).
- **Each refresh token works once** and comes back replaced. Store the new one before you use the new access token. If an old refresh token is ever used again, Payrolla assumes it was copied and **ends that token chain**; the owner then connects again.
- **A code works once.** Sending the same code a second time also cancels the tokens it gave the first time.
- **Refresh tokens last 90 days** from their last use, so an active connection doesn't expire.
- **Connecting again** (for example to ask for more scopes) keeps the same connection and replaces its scopes with what the owner allowed this time. One company has one connection with Taskiem.

### Ending the connection

- **From Taskiem:** `POST {{PAYROLLA_API}}/oauth/revoke` with `token=<a current access or refresh token>`. A token that no longer works is ignored, so an old one can't end a connection you've since made again.
- **From Payrolla:** the owner sees "Taskiem" under Settings → Connected apps and can revoke it there.
- Either way, your webhooks for that company get one last event, **`company.disconnected`**, with `reason` `revoked_by_app` or `revoked_by_owner`. Nothing follows it, and the tokens stop working.

### Companies already connected with a key

Their keys keep working. To move a company to OAuth, have the owner connect through the flow above, switch to the tokens, then ask the owner to revoke the old key. The OAuth connection is a separate connection, so **linked approvers link again** (once each) and **webhooks are set up again** on the new tokens.

## 2. Employees changing their own bank account

New scope: **`self_service`**, labelled "Employee self-service".

An employee using Taskiem can ask to be paid into a new account. Payrolla checks the account with the bank and texts **the employee** a code that names the account. Taskiem never sees the code; the employee types it into Taskiem.

```http
POST /connect/employees/{id}/bank-change              { "bank_code": "044", "account_number": "2222222222" }
→ 202 { "challenge": { "challenge_id": "…", "channel": "SMS", "destination_hint": "•••• 7801",
                       "account_name": "ZAINAB A. BELLO", "last4": "2222", "expires_at": "…" } }

POST /connect/employees/{id}/bank-change/confirm      { "challenge_id": "…", "code": "482910" }
→ 200 { "bank_change": { "id": "…", "status": "PENDING", "requested_via": "EMPLOYEE", … } }
```

- **Show `account_name`** to the employee before they confirm, so they can check it's theirs.
- **Still held.** The change waits for an Owner or Approver exactly like any other (`employee.bank_change_requested` with `requested_via: "employee"`). The employee keeps being paid into their current account until it's approved, and is told by SMS and email when it takes effect.
- **By SMS only,** to the phone number on Payrolla. Email isn't used, because an app can change an employee's email but not their phone. An employee with no phone on Payrolla gets `EMPLOYEE_UNREACHABLE`; their employer changes the account in Payrolla.
- **Limits:** a code lasts 10 minutes, allows 5 tries and works once. At most 3 codes per employee in 30 minutes.
- **Headers:** both calls take `Idempotency-Key`, `X-Acting-User` and `X-Acting-User-Name` like other writes. Use the employee's Taskiem id as the acting user. Use a new `Idempotency-Key` for each attempt at a code.
- **What it returns:** only the held change. To show the employee's record, use `employees:read` as before.

## 3. Settings and reports (read-only)

```http
GET /connect/settings                       (any connection)
```

Pay frequency, tax state, whether separation of duties is on, the funding model, whether a PAYE TIN is on file, and the statutory deductions in force (PAYE, pension rates, NHF, NHIS). Changing settings stays in Payrolla.

```http
GET /connect/remittances?status=PAYABLE      (reports:read)
GET /connect/remittances/{id}                (reports:read)
```

The PAYE, pension, NHF and NHIS schedules for each run, and one line per person. Pension schedules include each person's PFA and **pension PIN**, which is why reports have their own scope, **`reports:read`** ("Read statutory reports"). Treat these like payslips.

## New event

| Event | When | `data` |
|---|---|---|
| `company.disconnected` | This connection ended. Sent only to this connection's own webhooks, as their last delivery. Not returned by `GET /connect/events`. | `key_id`, `reason` (`revoked_by_owner` or `revoked_by_app`) |

`employee.bank_change_requested` now also has `requested_via: "employee"` for an employee's own request.

## Errors you'll meet

| Where | Code | Meaning |
|---|---|---|
| Connect screen | `UNKNOWN_CLIENT`, `INVALID_REDIRECT_URI`, `INVALID_SCOPE`, `PKCE_REQUIRED` | The request to `/oauth/authorize` doesn't match what we registered. The owner sees this, not you. |
| `/oauth/token` | `invalid_client` (401) | Wrong `client_id` or secret |
| `/oauth/token` | `invalid_grant` (400) | The code or refresh token is wrong, expired, already used, or for another `redirect_uri`; or the connection was revoked. Have the owner connect again. |
| `/oauth/token` | `unsupported_grant_type`, `invalid_request` (400) | The request isn't one we accept |
| Self-service | `EMPLOYEE_UNREACHABLE` (422) | No phone number on Payrolla |
| Self-service | `SAME_ACCOUNT`, `EMPLOYEE_NOT_ACTIVE` (409) | Nothing to change, or the employee has left |
| Self-service | `CODE_INVALID`, `CODE_EXPIRED`, `CODE_LOCKED`, `CODE_USED` (403) | Wrong, expired, out of tries, or already used; ask for a new code |
| Self-service | `CHALLENGE_NOT_FOUND` (404) | No such code for this employee from this connection |
| Self-service | `TOO_MANY_CODES` (429) | More than 3 codes in 30 minutes for this employee |

## What we need from you

1. **Redirect URIs** for production and testing (also asked in our Phase B note).
2. **The scopes Taskiem should be allowed to ask for.** We'll register what you list; you can ask for fewer per company.
3. **Your plan for companies already connected with a key**, so we can help their owners move over.

And still open from Phase B: does the two-step code flow fit your screens and chat, and do you want short-wallet alerts in any form beyond the event?

[YOUR NAME]
Payrolla
