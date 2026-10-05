# 0007 — Two-tier licence policy

Date: 2026-10-05 · Status: Accepted

## Decision

- **Linked or bundled** (compiled into the binary, web app, SDKs): MIT, Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC, PostgreSQL, 0BSD, Unlicense, CC0-1.0, BlueOak-1.0.0, Python-2.0 (TypeScript's dependency tree only).
- **Separate services** (unmodified upstream software run in its own process, reached over the network, optional): additionally MPL-2.0 and AGPL-3.0.

`tools/licencecheck` enforces the first tier in CI for Go modules and npm packages. Exceptions are listed per package in `tools/licencecheck/policy.json`, each with a reason.

## Why

Copyleft or source-available code linked into the product would compromise both the clean-room position and the freedom to license Taskiem as we choose. Separate services do not create a derivative work, and some best-in-class operational tools (Grafana, OpenBao) are AGPL or MPL.

## Provenance

Our own policy. Licence identifiers are SPDX.
