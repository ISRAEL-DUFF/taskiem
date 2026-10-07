# 0026 — Step-up bound to its operation, cookie sessions without a body token, key changes behind step-up

Date: 2026-10-07 · Status: Accepted

## Context

The security self-review left three findings on sessions and step-up open (S35, K5):

- A passkey step-up challenge was bound to its purpose and person, not to what it confirmed. An assertion made to approve a small payout could, within its five minutes, approve a large one, change a factor or anything else that took step-up.
- Sign-in answered with the session token in the body as well as in the `HttpOnly` cookie, so any script on the page (an extension, an injected script) could read a session the cookie was meant to hide. The CLI, scripts and the tests used that token.
- Encryption key changes (rotate, bring, replace, remove a customer key) needed only an owner's session.

## Decision

1. **A step-up challenge names its operation and target.** `POST /v1/me/step-up/options` takes `{"operation", "target"}` from a fixed list of operations (`approval.decide`, `account.reauth`, `key.rotate`, `key.byok.enable`, `key.byok.credentials`, `key.byok.disable`) and refuses anything else. The challenge row stores the scope and is taken only for it (migration 00130). The challenge's last 16 bytes are the scope's SHA-256 prefix, so the signed client data itself says what was confirmed. Targets are what the server already knows when it checks: `<run>/<step>/<decision>` for a vote, the change for a factor (`passkey.add`, `passkey.remove/<id>`, ...), the tenant for a key change.
2. **A browser's sign-in gets the cookie only.** Password and passkey sign-in, signup and invitee sessions answer with the `HttpOnly` cookie and no token. A client that asks with `"bearer": true` gets the token in the body and no cookie. Never both. Accepting an invitation keeps the kind of session it came with. The default is the cookie, so a new browser client cannot leak a token by accident.
3. **Key changes reuse the approval step-up.** The same passkey (bound as above) or authenticator code. API keys are refused: a key is not a person and has no second factor. Configuration mistakes are answered before the step-up, so a code is not spent on them.
4. **HSTS by default with an https public URL**, on the platform's own host only.

## Alternatives considered

- **Bind by signing the operation into the challenge only (no stored scope).** Rejected: the server would have to trust the client's description of the scope at verification, or recompute it from the request, which is what the stored scope already gives with less code.
- **Keep the token in the body and let the web app ignore it.** Rejected: the leak is that it is readable, not that it is read.
- **Decide cookie or token by request headers (`Origin`, `Sec-Fetch-*`).** Rejected: implicit, and some non-browser HTTP clients send these headers. An explicit field is one line for a script.
- **Let API keys change keys with a separate `key.manage` grant.** Rejected for now: an API key is exactly the bearer credential step-up is meant to stand behind. Operators keep `taskiem tenants keys ... rotate` for scheduled rotation.
- **Bind TOTP codes too.** Not possible without a second round trip: a code is not derived from a challenge. Codes stay single-use per time step.

## Consequences

- A script that signed in with a password must send `"bearer": true`. The CLI (`taskiem dev`) and the test suites do.
- The web app asks for a step-up challenge per operation; the Approvals, Handoff, Account and Encryption keys pages do.
- A TOTP code is not bound to an operation; passkeys are. Owners are held to passkeys by default.
- During a customer key outage, authenticator codes cannot be checked (their secrets are in the tenant's vault), so key changes then need a passkey. Restoring access and **Check now** need neither.
