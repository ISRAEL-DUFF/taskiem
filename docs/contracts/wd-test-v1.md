# Contract: Workflow tests `wd-test/v1`

A workflow test (spec 10.5) declares a trigger, what the outside world does (step outcomes, signals, approval decisions, form answers), and what the run must do. `taskiem test` runs them offline; `taskiem dev` runs them on every change and will not deploy a workflow whose tests fail. Git-led deploys will require them (Phase 2, milestone 2).

Tests run the real orchestrator (`engine/decide`) over an in-memory history on a virtual clock that starts at 2026-01-05T09:00:00Z. Nothing is called: every task step needs a mock. Backoffs, waits, and timeouts take no real time, so a test of a 72-hour signal timeout runs in milliseconds.

## File

`<name>.test.json`, anywhere (by convention a `tests/` directory next to the workflows):

```json
{
  "schema": "wd-test/v1",
  "workflow": "../supplier-payouts.wd.json",
  "env": { "ops_phone": "+2348000000000" },
  "cases": [
    {
      "name": "a refused payment does not stop the others",
      "trigger": { "body": { "batch_id": "B-1", "...": "..." } },
      "env": {},
      "mocks": {
        "balance": { "output": { "available_kobo": 90000000, "currency": "NGN" } },
        "pay_all[0].pay": { "error": { "kind": "fatal", "message": "destination account is frozen" } },
        "pay": { "output": { "payout_id": "po_1", "status": "pending", "amount": "=input.amount_kobo" } }
      },
      "signals": { "confirmed": { "payload": { "event": "payout.settled", "fields": { "payout_id": "po_1" } } } },
      "approvals": { "approve": { "decision": "approved", "by": "checker" } },
      "expect": {
        "status": "completed",
        "steps": { "pay_all[0].pay_failed": "completed" },
        "outputs": { "summary": { "paid": 1, "failed": 1 } },
        "inputs": { "report": { "body": { "paid": 1 } } },
        "calls": { "pay": 2 }
      }
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `workflow` | The `*.wd.json` under test, relative to the test file. A template's own tests (`templates/tests/<id>.test.json`) name the `template` and its `params` instead ([templates](../templates.md)) |
| `env` | Tenant variables (`env.*`) for every case; a case's `env` adds to and overrides them |
| `trigger` | The run's `trigger`. For webhook workflows, `trigger.body` is checked against the workflow's inputs schema first; for a `form` trigger `trigger.body` is the answers, checked the same way, and `trigger.form` (`submission_id`, `respondent`, ...) may be given as the form host would send it |
| `mocks` | Outcomes of task steps (`connector`, `capability`, `http`, `code`), keyed by step id or instance id (`pay_all[2].pay`, `compensate:pay`); an instance key wins. A mock is `{"output": ...}` or `{"error": {"kind", "message"}}`; a list answers successive attempts and its last entry repeats. Output values may be expressions over `input`, the step's resolved input |
| `signals` | `{"payload": ...}` delivers the signal (the step's output); `{"timeout": true}` lets its timeout fire |
| `approvals` | `{"decision": "approved" \| "rejected", "by": "..."}`, or `{"timeout": true}` |
| `forms` | Answers to `form` steps (an addition for [forms](../spec/forms.md)), keyed like `signals`: `{"answers": {...}}` submits them, checked against the step's `schema` (a mismatch fails the case); the step's output is the answers plus `respondent`, which is `respondent` when the mock gives one and otherwise who the step asked (`{"kind": "link", "phone"}` or `"email"`, or `{"kind": "member", "member_id": "test-member"}` for a role). `{"timeout": true}` lets the step's timeout (7 days by default) fire: with `on_timeout: continue` its output is `{"timed_out": true}` |
| `expect.status` | `completed`, `failed`, `needs_reconciliation` (a step parked for an operator), or `blocked` (waiting on something the case does not answer) |
| `expect.error` | Text the run's failure message must contain |
| `expect.steps` | Instance or step id to `completed`, `failed`, `parked`, `skipped`, `cancelled`, or `not_run` |
| `expect.outputs` | Instance id to expected output |
| `expect.inputs` | Instance id to what the task sent (its resolved input; for `http` steps `{method, url, headers, query, body}`). Secret references stay as written: tests never see secret values |
| `expect.calls` | Number of attempts sent, by instance id, or by step id across all its instances |

Objects in `outputs` and `inputs` match when every expected key matches (extra keys are fine); lists and scalars must match exactly.

## Errors

A mocked error is classified as the worker would classify it from the real connector: the action's class comes from the connector manifests compiled into the binary (an `http` step uses its `class`, a `code` step counts as a read). So `unknown_outcome` on an `idempotent_write` retries under the same key, on an `unsafe_write` it parks, and `fatal` fails the step. Kinds: `retryable`, `fatal`, `unknown_outcome`, `not_sent`, `indeterminate`.

## Not covered

- Real provider behaviour: connector fixtures cover that (`connectors/*`).
- Code steps run only as mocks; their own logic is tested in the code.
- Run timeouts fire only while something else is still pending: a run left with nothing to wait for is reported as `blocked`, which says more about why.
