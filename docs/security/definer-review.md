# Definer review, October 2026

`SECURITY DEFINER` functions run as `taskiem_dispatch`, past row-level security, for lookups that must happen before a tenant is known or across tenants (spec 5.3). The engine review of 27 October 2026 (findings 3) asked for each to be checked once against one rule, **return routing columns only**, starting with sign-in, operator and partner functions, and for any that a tenant-scoped query could do instead to be retired. This page records that review and what changed (migration 00253).

## How

Each function's definition was read from a migrated database, with its Go callers, and judged on: what it returns and whether the callers need all of it; whether it limits what it reads (a secret, a status, the caller's own scope); whether it must be a definer at all; and anything else (dynamic SQL, `search_path`, writes wider than intended, audit). Every function in the three families pins `search_path` and none uses dynamic SQL.

| Family | Functions | Ok | Findings |
| --- | --- | --- | --- |
| Sign-in (identity) | 32 | 26 | 6, all low |
| Operator console | 25 | 16 | 4 (one medium) covering 9 functions |
| Partners and embedding | 12 | 9 | 3 (two medium) |

Every function the app role may call now has a reason of its own in [`engine/db/definers.tsv`](../../engine/db/definers.tsv). The other families (messaging, connectors, money, storefronts, engine) got their reasons in the same pass; reading them turned up the not-needed definers listed below, but they have not had the full review.

## Findings

| # | Finding | Severity | Done |
| --- | --- | --- | --- |
| 1 | The five operator account functions (add, enrol, reset, disable, list), meant for the CLI only, were granted to `taskiem_app`, which the API server holds; SQL running in the server could make itself an operator. The account change and its audit entry were separate statements | Medium | Moved to `taskiem_ops_admin`, which only the CLI's own login is a member of (`TASKIEM_OPS_DATABASE_URL`, [operator console](../operator-console.md#accounts)); each change commits with its audit entry |
| 2 | `taskiem_partner_usage` let a sub-tenant list its sibling sub-tenants and their run counts | Medium | Partner scope only; a sub-tenant's quota check uses `taskiem_partner_run_totals`, the two totals |
| 3 | Thirteen definers were owned by the migrating user, not `taskiem_dispatch`, so they ran with that role's privileges (every table it owns; on a superuser, past every grant and policy) rather than the columns dispatch is given | Medium | Nine moved to `taskiem_dispatch`, with grants and policies on the status tables. The two partition functions must own `run_events`; `taskiem_partner_usage_snapshots` and `taskiem_remote_tenants` read columns the dispatch role is kept from (usage figures, remote ids), so they stay. `TestDefinersAllowList` now fails on any other owner |
| 4 | `taskiem_auth_find_user` gave the password hash to every caller, though only sign-in and password reset need it | Low | `taskiem_auth_user_of` (id and status) for the other nine callers |
| 5 | Two overloads kept for replicas mid-upgrade (00130) had no callers | Low | Dropped |
| 6 | `taskiem_auth_password_reset_use` cleared a person's other links before checking the secret | Low | The link is spent first; the others go only if it was |
| 7 | `taskiem_ops_agents` sent agents' referees' names and phone numbers to the console, which never shows them | Low | Left out of the list |
| 8 | Opening one storefront review or page report searched only the newest 200 | Low | `taskiem_ops_storefront_review` and `taskiem_ops_page_report` by id |
| 9 | A tenant whose name holds another tenant's id hid it from the console's tenant page | Low | An exact id comes first |
| 10 | `taskiem_partner_self` and `taskiem_storefront_lock` read nothing the caller cannot | Low | No longer definers |
| 11 | `taskiem_auth_sso_for_email` returns a tenant id no caller reads | Low | Accepted: the reply never includes it, and a verified domain's tenant is not secret. Remove with the next change to the function |
| 12 | `taskiem_oauth_request_take` returns the sealed verifier with the routing columns | Low | Accepted: it is encrypted, for a request whose state the caller already holds |
| 13 | `taskiem_storefront_change_done`, `taskiem_invoice_paid_starts_done` and `taskiem_invoice_payment_starts_done` change rows the app role may change in the tenant's own scope | Low | Open: moving their callers into the tenant's scope needs the functions kept a release for replicas mid-upgrade, then dropped |

## Open questions

- **Owner of the two remaining functions.** `taskiem_partner_usage_snapshots` and `taskiem_remote_tenants` still run as the migrating user. They work wherever migrations do: the migrating user is a member of `taskiem_dispatch` (migration 00001), so the dispatch policies apply to it, which a fresh Postgres 16 cluster with a non-superuser owner confirmed. They hold more privilege than they need; a role of their own, with only the columns they read, would fix that at the cost of one more role each deployment grants to the migrating user.
- **Deleted sub-tenants** still count towards a partner's `max_subtenants` when a plan changes (`engine/billing/change.go`), while creating one does not count them. A correctness fix, not a security one.

## Also from findings 3

- **SQL built from values**: `tools/sqlcheck` fails CI on any query given SQL that is not a constant, unless its line says why every piece is fixed. All 66 sites were traced; none let a value reach SQL text, and the five that formatted integers into it now pass them as arguments.
- **Columns the dispatch role may read** are pinned in [`engine/db/dispatch_columns.txt`](../../engine/db/dispatch_columns.txt) (`TestDispatchColumnsAllowList`).
