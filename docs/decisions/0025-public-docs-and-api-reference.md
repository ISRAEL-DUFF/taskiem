# 0025 — Public docs and API reference: a hand-written OpenAPI document checked against the router, a static site from docs/

Date: 2026-10-07 · Status: Accepted (domain, hosting and review owner pending P4-D1 to P4-D3)

## Context

Phase 4 opens Taskiem to people who have never spoken to the team (P4-7). They need the guides that live in `docs/`, and an API reference for the routes partners, scripts and embedded builders call. The web app's help panels already link to `TASKIEM_DOCS_URL/<page>` (decision 0022). `docs/` mixes those guides with the team's own records (phase status, the build plan, the threat model), and nothing described the API in a form tools can read. About 240 routes sit under `/v1`, each with a permission, sometimes a plan feature, and a rule about environment-limited keys, all in `api/server.go` and the route functions it calls.

## Decision

1. **The OpenAPI 3.1 document is written by hand** (`api/openapi/openapi.yaml`) and kept honest by tests, not generated from handlers. `api/openapi_test.go` walks the chi router and fails on a public route missing from the document or a documented route the router does not serve; the routes left out are listed with their reasons (edge callbacks, SCIM, probes, the embed script, payment webhooks). The same test reads the router's source and fails when an operation's `x-taskiem-permission`, `x-taskiem-plan-feature`, `x-taskiem-all-environments` or security differs from the middleware the route actually has. Every answer the API tests receive is validated against the document's schema for its route and status.
2. **Schemas are detailed where clients depend on them, and honest elsewhere.** Workflows, versions, validation, publishing, runs, sign-in, signup, `/me`, API keys, connectors and onboarding have full schemas. Other operations declare a JSON object and point at their guide; they get detailed schemas as they are touched, with the response check catching mistakes.
3. **The site is static and built by `tools/docsite`** (Go, goldmark for Markdown, MIT): one HTML file per page, navigation, a JSON search index searched in the browser, and the API reference rendered from the document. No build output is committed.
4. **Which docs are public is a list in code.** `tools/docsite/pages.go` names the public pages (the navigation) and the internal ones with reasons: phase status, needs-people, the PGDock plan, the clean-room policy, `spec/`, `security/`, `decisions/`, and partner correspondence. A doc in neither list fails a test, so each new doc is a choice. Links from public pages to internal ones become plain text, or point at the repository with `-source`.
5. **URLs are the doc's path without `.md`**: `/templates`, `/integrations/slack`, `/contracts` for a directory's README. They match the web app's help links, which a test checks.
6. **Links are checked before anything is built**, in CI and as a test: every relative link in `docs/`, README.md and CONTRIBUTING.md reaches a file in the repository, and every anchor a heading (GitHub's ids).

## Alternatives considered

- **Generating the document from handler code or annotations.** Rejected: the handlers decode into anonymous structs and `map[string]any`, so generation would need annotations on about 240 handlers that drift as easily as a document does, and still could not describe errors. A written document with tests on its routes, permissions and answers stays accurate with less machinery.
- **Publishing everything in `docs/`.** Rejected: the threat model, self-review and pen-test scope are not for attackers, and the build plan and status pages describe the team's work, not the product.
- **A static-site generator in Node or Hugo.** Rejected: another toolchain for contributors and CI, and a bigger licence surface. A Go tool with one MIT dependency runs wherever the tests run.
- **Hosted search.** Rejected for now: a third party for a few hundred kilobytes of index.

## Consequences

- Every new route changes `api/openapi/openapi.yaml` in the same change, or CI fails.
- Hosts must serve `/name` from `name.html` (GitHub Pages and Cloudflare Pages do; nginx needs `try_files $uri $uri.html $uri/ =404`).
- Renaming a public doc changes its URL; help links and saved links break unless the host redirects.
