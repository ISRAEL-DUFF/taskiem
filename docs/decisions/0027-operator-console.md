# 0027 — An operator identity apart from tenants, a console on it, and a platform audit chain

Date: 2026-10-08 · Status: Accepted (who gets operator accounts and the SSO issuer pending P4-X1, P4-X2)

## Context

Operators, the people who run a Taskiem installation, act through the CLI with database access: catalogue reviews (`taskiem catalogue`, decision 0020), the status page (`taskiem status`, decision 0023), billing grants, limits and pools. The API had no operator identity. Decision 0020 rejected reviews in the tenant API for that reason and noted that a reviewer is only asserted with `--as` (the database checks the email against the reviewer list and the publisher's members, but not that the person typing is that reviewer). Decision 0023 gave the status admin API hashed per-person tokens and left operator sign-in for later. The self-review listed an operator console as missing. Reviewers working from a terminal with database access is also more access than reviewing needs.

## Decision

1. **Operators are a platform-level identity, not a tenant role.** Their own tables (`operators`, `operator_credentials`, `operator_sessions`, `operator_challenges`, `operator_enrolments`, `operator_sso_requests`; migration 00142), reachable by the application role only through definer functions owned by `taskiem_dispatch`, as for sign-in challenges and the reviewer list. A person who is both a member of an organisation and an operator has two separate identities with separate passkeys.
2. **Accounts are made by the CLI only.** `taskiem operators add|enrol|reset|disable`. The API has no route that creates, changes or lists operators. An account starts with a one-time enrolment link (256-bit token, kept hashed, 24 hours by default and at most 7 days, in the URL fragment), which registers a passkey with user verification required. Disabling ends sessions and unused links at once.
3. **Passkeys sign in; single sign-on optionally.** WebAuthn through `engine/webauthn`, with challenges in their own table, so a tenant's challenge cannot be answered on the operator side or the reverse, and credentials looked up only among operators'. OpenID Connect through `engine/oidc` from one issuer configured on the deployment (`TASKIEM_OPS_OIDC_*`): only an existing, active operator, by verified email; the first sign-in pins the issuer's subject and a later different subject is refused; no accounts made by sign-in. State, nonce, PKCE and a browser-binding cookie, as tenant SSO.
4. **Sessions of their own.** A `tsk_ops_` token, kept hashed, in an `HttpOnly`, `SameSite=Strict` cookie whose path is `/v1/ops`, lasting one hour by default (`TASKIEM_OPS_SESSION_TTL`, at most eight; a `CHECK` holds the database to eight) and not extended by use. The operator middleware takes that cookie only and refuses any request with an `Authorization` header; the tenant middleware ignores `tsk_ops_` tokens and never reads the operator cookie. Non-GET requests need the CSRF header.
5. **Every write needs a passkey, bound to it.** Each operator write carries a passkey assertion for a challenge naming its operation and target (`ops.catalogue.review <id>/approve`, `ops.status.update <id>/resolved`, ...), as tenants' step-up (decision 0026). Signing in with SSO does not stand in for it.
6. **The console reuses the definer functions the CLI uses**, so its guarantees are the database's. The reviewer is the signed-in operator's email instead of `--as`; four eyes stays in `taskiem_catalogue_review`, which now also refuses an approval without every checklist item (migration 00143, for the CLI too). Tenants are read-only: a definer function lists what `taskiem_dispatch` may already read (id, name, status, plan and subscription state, pool), and one tenant's subscription dates, limits and usage are read in its own scope as `taskiem tenants limits` does. Limits, plans, pools and the reviewer list still change only from the CLI.
7. **A platform audit chain.** Operator actions are appended to the existing hash chain (`audit_log`, `taskiem_audit_append`, actor type `platform_admin`) under a reserved chain id, `ffffffff-ffff-ffff-ffff-ffffffffffff`, that a `CHECK` on `tenants` keeps any tenant from having. The anchoring job, exports and `taskiem audit verify` work on it unchanged; anchors no longer reference `tenants` by foreign key for that. Catalogue decisions are recorded in the publisher's chain as well, as the CLI records them.
8. **Status tokens stay for automation.** `TASKIEM_STATUS_TOKENS` keeps working on `/v1/status/admin` for scripts. They are not accepted by the console, and an operator session is not accepted there.
9. **Out of the public API document.** `/v1/ops/` is listed in `notInSpec` (`api/openapi_test.go`): it is for a deployment's own operators, not integrators.

## Alternatives considered

- **An operator role inside a special tenant.** Rejected: operators would get a tenant session that every tenant route accepts, membership, roles, API keys and invitations would all need exceptions, and a bug in tenant scoping would reach the platform. Separate tables and a separate cookie make the boundary structural.
- **Fold status tokens into operator API tokens.** Rejected for now: tokens serve scripts that cannot do a passkey ceremony; giving them a general operator scope would make a long-lived bearer secret able to review connectors. They stay narrow (incidents only) and separate.
- **SSO only.** Rejected: it would make the identity provider able to sign anyone in as an operator, and step-up for writes still needs a second factor the platform checks itself. SSO is a convenience for sign-in; the passkey is the operator's.
- **A separate platform audit table.** Rejected: it would need its own anchoring, export and verifier. A reserved chain id reuses all three.
- **Let the console manage the reviewer list and operators.** Rejected: a stolen operator session plus a passkey prompt the operator is tricked into would then be enough to grant review rights. Keeping account and list changes in the CLI keeps them behind database access.

## Consequences

- Operators need a passkey to change anything; one who loses their device is reset from the CLI and enrols again.
- The passkey relying party is the platform's public URL for both identities; a person's browser may offer their member passkey on the operator sign-in, which is refused.
- Reviewing no longer needs database access, so the reviewer list can include people without it.
- The CLI's own actions on operator accounts are in the platform chain; its catalogue and status commands still record where they did before (the publisher's chain, the status tables), not in the platform chain.
- Restricting `/ops` to an operators' network at the load balancer is recommended but not required.

## Provenance

Separate administrative identities, short sessions and re-authentication for privileged actions are general practice from public security guidance (OWASP session management and authentication cheat sheets, NIST SP 800-63B on reauthentication). The design reuses Taskiem's own passkeys, SSO, step-up and audit chain; no other product's source was consulted.
