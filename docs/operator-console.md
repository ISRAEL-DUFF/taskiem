# Operator console

The operator console is where Taskiem's own operators review connector submissions, verify and suspend publishers, run the status page, and look at tenants' plans and limits, in the browser at `/ops`. It is for the people who run a Taskiem installation (Taskiem's staff on the cloud, your platform team on a self-hosted install), not for organisations using Taskiem: they sign in at `/login` and never see it.

Operators are their own identity, separate from every organisation's members. An operator account is not a role in a tenant, an operator's session is never accepted by the tenant API, and a tenant's session, API key or passkey never opens the console. Design: [decision 0027](decisions/0027-operator-console.md); threats: boundary B19 in the [threat model](security/threat-model.md).

The CLI keeps working for everything the console does (`taskiem catalogue`, `taskiem status`, `taskiem tenants`, `taskiem billing`, `taskiem pools`), and for what it does not: changing limits, plans, pools and the reviewer list stays in the CLI.

## Accounts

Accounts are made from the CLI only, by someone with database access. There is no sign-up, no invitation and no API to create one.

```sh
taskiem operators add ada@taskiem.example --name "Ada Obi"    # prints a one-time enrolment link
taskiem operators                                              # list: status, passkeys, SSO, last sign-in
taskiem operators enrol ada@taskiem.example                    # a new link: another passkey, or after a reset
taskiem operators reset ada@taskiem.example                    # lost device: remove passkeys, end sessions
taskiem operators disable ada@taskiem.example                  # leaver: sessions and unused links end at once
```

The enrolment link (`https://<public URL>/ops/enrol#<token>`) works once and lasts 24 hours (`--ttl`, at most 7 days). The token is in the URL fragment, which browsers never send to a server or in a `Referer`; the page removes it from the address bar. Give the link to the operator over a channel you trust: whoever opens it first enrols a passkey on the account. Opening it creates a passkey (fingerprint, face or PIN; user verification is required) and signs the operator in. `add` on an existing email reactivates it.

To review catalogue submissions an operator must also be on the reviewer list (`taskiem catalogue reviewers add EMAIL`); the console shows the list but does not change it.

## Signing in

| Way | How |
| --- | --- |
| Passkey | **Sign in with your passkey** at `/ops/login`. The passkey must be one enrolled as an operator: a passkey the same person uses as a member of an organisation does not sign them in here, and an operator's passkey does not sign in to an organisation. |
| Single sign-on | Optional: OpenID Connect from one issuer you configure (`TASKIEM_OPS_OIDC_*`). Only an existing, active operator signs in, by the verified email address; there are no accounts made by sign-in. The first sign-in pins the issuer's subject to the account, and a different subject with the same email is refused. SSO signs in only: writes still need the operator's passkey. |

A session lasts one hour (`TASKIEM_OPS_SESSION_TTL`, 5 minutes to 8 hours) and is not extended by use. It is an `HttpOnly`, `SameSite=Strict` cookie (`taskiem_ops_session`, `Secure` in production) sent only to `/v1/ops`, so the browser never sends it to the tenant API. Requests other than `GET` need the `X-Taskiem-Request` header, as in the tenant console. Signing out ends the session in the database.

## Every write asks for the passkey again

Each change asks the browser for the operator's passkey, for that change only: the challenge names the operation and its target, and an assertion made for one is refused for any other (as tenants' step-up, [decision 0026](decisions/0026-bound-step-up-and-cookie-sessions.md)).

| Operation | Target |
| --- | --- |
| `ops.catalogue.review` | `<submission id>/approve` or `/reject` |
| `ops.catalogue.revoke` | `<connector>@<version>` |
| `ops.publisher.verify`, `ops.publisher.suspend`, `ops.publisher.reinstate` | the publisher's slug |
| `ops.status.open` | `incident` or `maintenance` |
| `ops.status.update` | `<incident id>/<new status>` |

An operator signed in with single sign-on who has no passkey can read but not change anything.

## What the console does

| Page | What | Same as |
| --- | --- | --- |
| Review queue | Submissions in review (or in every state); a submission's package digests, signing key, licence and attestation, the manifest summary (hosts, actions and their classes, personal fields), every automated check and conformance case, lint findings, its history, and the [review checklist](connector-submissions.md#review-checklist). Approve with every one of the eight items confirmed and a note; reject with a note. | `taskiem catalogue queue`, `show`, `review` |
| Publishers | Namespaces and their status; verify, suspend (with a note the publisher sees: every version stops loading at once), reinstate | `taskiem catalogue publishers` |
| Reviewers | Who may review (read-only) | `taskiem catalogue reviewers` |
| Status page | Open an incident or schedule maintenance, post updates, resolve; the last 90 days with who posted each update (never shown on the public page) | `taskiem status` |
| Tenants | Search by name or id: status, plan, subscription state, worker pool; one tenant's subscription dates, effective limits (and which are the tenant's own) and usage. Read-only. | `taskiem tenants limits`, `taskiem pools` |
| Platform audit | The platform audit chain, whether it verifies, and an export | — |

**The reviewer is who signed in.** In the console the reviewer is the signed-in operator's email, not an email typed in (`--as` in the CLI). The database still decides: the reviewer must be on the list, must not be the submitter or a member of the publisher's organisation, and needs a note; approving needs every checklist item confirmed, which the database now checks too (migration 00136), for the CLI as well. A revocation from the console alerts every installing organisation, as from the CLI.

**What the console shows about tenants** is what the CLI already shows: a tenant's id, name, status, plan, subscription state and dates, limits, usage counts and worker pool. Never its members, workflows, runs, secrets, invoices or card details. Opening a tenant is recorded in the platform audit chain.

## The platform audit chain

Every operator action is appended to the platform audit chain: sign-ins and sign-outs, enrolments, refused single sign-ons, reviews, revocations, publisher changes, status declarations and updates, tenant views, exports, and the CLI's account changes. It is the same hash chain as an organisation's ([audit anchoring](compliance.md#audit-chain-anchoring)), under the reserved chain id `ffffffff-ffff-ffff-ffff-ffffffffffff`, which no tenant can have (a check on `tenants`). The anchoring job signs its head and writes it outside the database with every tenant's (`anchors/ffffffff-ffff-ffff-ffff-ffffffffffff.jsonl`), so the platform chain is as tamper-evident as theirs.

Catalogue decisions are also recorded in the publisher's own chain, as the CLI records them (`reviewer:<email>`), so a publisher sees who reviewed its connector.

Download the chain from **Platform audit** (`GET /v1/ops/audit/export`) and check it offline:

```sh
taskiem audit verify audit-platform.jsonl
taskiem audit verify --anchors anchors/ffffffff-ffff-ffff-ffff-ffffffffffff.jsonl --key <anchor public key> audit-platform.jsonl
```

## Automation: status tokens

Scripts keep using the status admin API with operator tokens (`TASKIEM_STATUS_TOKENS`, [reliability](reliability.md#status-page)): a token is a bearer credential for incidents only, kept as its SHA-256, and is not accepted by the console. The console does not replace them; an operator session is for a person at a browser.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `TASKIEM_OPS_CONSOLE` | `true` | Serve the console's API (`/v1/ops`); `false` answers 404 there |
| `TASKIEM_OPS_SESSION_TTL` | `1h` | How long an operator session lasts (5m to 8h) |
| `TASKIEM_OPS_OIDC_ISSUER` | — | The one OpenID Connect issuer operators may sign in with (https); unset is passkeys only |
| `TASKIEM_OPS_OIDC_CLIENT_ID`, `TASKIEM_OPS_OIDC_CLIENT_SECRET` | — | The console's client at that issuer; register the redirect URI `https://<public URL>/v1/ops/auth/sso/callback` |
| `TASKIEM_OPS_OIDC_NAME` | `single sign-on` | The sign-in button's label |

Passkeys need `TASKIEM_PUBLIC_URL` (the relying party), as for organisations. The console's API is not in the public API reference: it is for a deployment's own operators, not integrators.

On a shared deployment, consider allowing `/ops` and `/v1/ops` only from your operators' network at the load balancer; the console's controls do not depend on it.
