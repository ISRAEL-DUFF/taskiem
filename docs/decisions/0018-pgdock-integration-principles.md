# 0018 — PGDock integration: public contracts only, each product stands alone

Date: 2026-10-07 · Status: Accepted

## Context

PGDock is a managed Postgres platform built by the same founders. *PGDock × Taskiem: How the Two Platforms Work Together* (7 October 2026, "the joint doc") proposes that the two work together: PGDock row changes start Taskiem workflows, Taskiem reads and writes PGDock data, accounts are linked, and some building blocks (payments, messaging, metering) are shared. The work is tracked in [the PGDock integration plan](../pgdock-integration.md), which runs alongside Taskiem's own phases.

Two products with shared owners can easily become coupled through shared tables, private endpoints or a shared service account. Then neither can ship, fail or be sold on its own, and outside tools can never do what the sibling product does.

## Decision

Taskiem adopts the joint doc's six principles for everything it builds with PGDock:

1. **Each product stands alone.** Neither product needs the other to work. Taskiem's PGDock connector is optional, like any connector.
2. **Public surfaces only.** There are no shared tables, queues, service accounts or private endpoints. Taskiem calls only PGDock's published API and receives only its published webhooks.
3. **The third-party test.** Anything Taskiem does with PGDock, an outside tool must be able to do too. If the integration needs something PGDock does not publish, PGDock publishes it first. Taskiem builds from PGDock's docs, OpenAPI file and changelogs, as for any provider ([clean-room policy](../clean-room-policy.md)).
4. **Separate tenancy and security.** Each product keeps its own organisations, users and secrets. Linking accounts takes the customer's explicit, scoped consent and can be revoked. A PGDock token in Taskiem is a tenant connection secret like any other.
5. **Versioned contracts.** PGDock's deprecation policy covers every PGDock API Taskiem depends on. Taskiem's `pgdock@1` connector has contract tests, and PGDock runs them in its CI.
6. **Independent outages.** An outage of either product degrades the integration and nothing else. PGDock's outbox retries cover Taskiem downtime. Taskiem's retries, and its `unknown` outcome handling, cover PGDock downtime.

## Alternatives considered

- **A private integration path** (shared database, internal endpoints, a platform-level service account). Rejected: it fails principles 2–4, ties releases together, and turns one product's outage or breach into the other's.
- **Treating PGDock as just another connector, with no joint plan.** Rejected: some of what the integration needs is not published yet (parameterised writes, idempotency keys, account-linking consent). Those items need a plan both teams track.

## Consequences

- Some joint-doc features wait for PGDock to publish contracts. Write actions wait for P1-G1, and data tables wait for the V4 data API. Taskiem ships reads and the trigger first.
- Taskiem needs general capabilities that any provider could use, not PGDock-specific shortcuts: remote trigger registration and a generic OAuth connection flow.
- Shared building blocks become "align or adopt" decisions (plan question Q1), not shared code by default.
