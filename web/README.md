# Web app

React, TypeScript, Vite, and React Flow (spec 15.1): sign-in, workflows with a canvas editor (steps, dependencies, configuration forms generated from connector schemas, trigger and run settings, JSON view, published endpoints), runs with a step-by-step inspector (sealed personal data shown as 🔒, reveal for `pii.reveal`, cancel, resolve parked steps), the approvals inbox, connections, secrets, variables and egress hosts, the audit log with chain verification and export, and members and API keys.

The API serves the built app from `TASKIEM_WEB_DIR` (`/web` in the image).

```sh
pnpm --filter @taskiem/web dev     # Vite on :5173, proxying /v1 and /hooks to taskiem serve on :8080
pnpm --filter @taskiem/web build   # dist/, and the embeddable bundle dist/embed/v1/taskiem.js (docs/embedding.md)
pnpm --filter @taskiem/web test    # unit tests (graph, timeline, schema fields)
make e2e                           # Playwright against the real binary and a fresh database
```

Set `PLAYWRIGHT_CHROMIUM_EXECUTABLE` to use an installed Chromium instead of `playwright install`.

## Limits in Phase 1

- The canvas shows top-level steps; steps nested in `branch`, `foreach`, and `on_error` are edited as JSON in their parent's panel.
- Every save creates a new immutable version; canvas positions are saved on the current version.
- No live run view on the canvas (Phase 2); the inspector refreshes every 2 seconds while a run is active.
