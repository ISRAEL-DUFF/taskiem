# AI builder evaluation suite

**Synthetic seeds pending review by people.** Every request here was written by engineering from the dogfood flows, the connector catalogue and common Nigerian and African fintech and small-business operations. They stand in for real requests until [needs-people AI2](../../docs/needs-people.md#phase-3) replaces and extends them with requests from dogfooding and design partners, reviewed by the people who make them. The gate (≥ 70% valid and test-passing on the first try against the real model) is measured on the reviewed suite.

How to run it, what the grader checks, the gate and the baselines: [AI: evaluation](../../docs/ai.md#evaluation).

## Format

One JSON object per line; lines starting with `//` are comments (every file starts with the synthetic-seeds marker).

```json
{"id": "payout-loan-disbursal-lenco",
 "request": "When the credit engine posts a disbursement instruction to our webhook, ...",
 "expect": {"connectors": ["lenco@1"], "steps": ["approval", "connector"], "trigger": "webhook",
            "properties": ["approval_before_payment", "idempotency", "webhook_auth", "reads_before_paying"]},
 "tags": ["payouts", "lending", "approval"], "difficulty": "medium", "source": "synthetic"}
```

| Field | Meaning |
| --- | --- |
| `id` | Unique, stable (baselines compare by id) |
| `request` | What a person would type, in their words |
| `expect.connectors` | Connector refs a good answer must use (in a step or as its trigger); only connectors in the catalogue |
| `expect.steps` | Step types it must use somewhere |
| `expect.trigger` | The trigger type it must start with, when the request says |
| `expect.properties` | Rules it must satisfy (`tools/aieval/rules.go`): `approval_before_payment`, `idempotency`, `webhook_auth`, `dedup`, `bounded_concurrency`, `approval_timeout`, `error_handling`, `reads_before_paying`, `inputs_declared`, `no_hardcoded_contacts` (`no_inline_secrets` is checked on every case) |
| `tags` | The first is the case's group (payouts, collections, reconciliation, kyc, notifications, approvals, reporting, messaging, multistep, sme); the rest are chips for the per-tag breakdown |
| `difficulty` | easy (one step), medium (a few steps, an approval), hard (branches, loops, parallel work, several providers) |
| `source` | synthetic, dogfood or partner |

Expectations are minimums, never an exact shape: a draft may use more connectors or steps.

## Rules for adding cases

- Never copy a request, or any eight words of one, into a prompt, the worked example or a template: `TestNoEvalRequestInPrompts` fails the build.
- Name only connectors in `connectors/` today; a case for a connector that does not exist yet waits for it.
- Changing or adding cases changes the offline baseline: regenerate it with `go run ./tools/aieval -baseline-out evals/builder/baseline.json` in the same change, and say why in the pull request.
- Real requests from people keep their meaning and difficulty but lose anything identifying (names, numbers, accounts).
