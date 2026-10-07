# 0022 — Self-serve onboarding: confirm email without blocking the first run, and measure G4 where runs end

Date: 2026-10-07 · Status: Accepted

## Context

Gate G4 asks that a new user goes from self-serve signup to a first successful run in under 15 minutes. Signup has existed since Phase 0 but was off and unhardened: anyone could create tenants without limit, from throwaway addresses, with any name. A public form needs abuse protection, but every check placed before the first run eats into the 15 minutes, and email delivery can take minutes. There was also no way to tell whether G4 was met.

## Decision

1. **Signup is hardened without third parties.** JSON only, a hidden field bots fill, a built-in list of throwaway mailbox domains plus the operator's, names that cannot carry links or addresses, and a per-address daily limit counted in the database (`taskiem_signup_admit`, migration 00095), so every replica shares it.
2. **Email confirmation gates reach, not building.** Until the person who signed up confirms their email, the tenant cannot invite members or create API keys: the two ways a throwaway account reaches other people or acts outside the browser. Building, publishing and running are open at once, so the first run never waits for a mailbox. Where the deployment cannot send email, nothing is gated.
3. **Confirmation links reuse the password-reset token design** (selector, 256-bit secret stored as SHA-256, single use, lock after five wrong secrets) but are tenant rows under row-level security and are used by a signed-in session. No new function runs as `taskiem_dispatch` for them.
4. **The checklist is derived, not stored.** Each step is a query over what the tenant has (connections, workflows, published versions, completed runs, members). Only the dismissal and the first-run time are stored.
5. **G4 is measured where runs end.** `endRun` stamps `tenant_onboarding.first_run_at` once, for self-serve tenants, in the transaction that completes the run, and observes `taskiem_onboarding_first_run_seconds`. The stamp survives run retention; the histogram gives the platform-wide share under 900 seconds without a cross-tenant query.

## Alternatives considered

- **Confirm email before anything works.** Rejected: it puts mail delivery inside the 15 minutes and loses people at the inbox.
- **A third-party email validation or captcha service.** Rejected for now: a new processor of personal data and a dependency for signup to work. Revisit with the abuse policy (P4-O2) if the built-in checks are not enough.
- **A database trigger on `runs` for the first-run stamp.** Rejected: it cannot emit the metric, and a trigger on the hottest table is harder to see than one statement in `endRun`.
- **A cross-tenant report function for G4.** Rejected: the histogram answers the gate's question without giving any role a cross-tenant read of onboarding rows.

## Consequences

- Completing a run costs one more primary-key `UPDATE` that matches no row for nearly every run.
- A throwaway account can still run workflows during its trial, within its plan's limits and its own connections' credentials. Deleting unconfirmed, unused tenants is left to the abuse policy (P4-O2).
- Browser tests point a connector at a fake on loopback; the worker's egress guard allows exactly that connector and port (`egress.Guard.Loopback`) when an operator's base URL names a loopback address.
