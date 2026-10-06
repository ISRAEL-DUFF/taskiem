# Contract: Workflow tests `wd-test/v1`

A workflow test (spec 10.5) declares a trigger, what the outside world does (step outcomes, signals, approval decisions), and what the run must do. `taskiem test` runs them offline; `taskiem dev` runs them on every change and will not deploy a workflow whose tests fail. Git-led deploys will require them (Phase 2, milestone 2).

Tests run the real orchestrator (`engine/decide`) over an in-memory history on a virtual clock that starts at 2026-01-05T09:00:00Z. Nothing is called: every task step needs a mock. Backoffs, waits, and timeouts take no real time, so a test of a 72-hour signal timeout runs in milliseconds.

## File

`<name>.test.json`, anywhere (by convention a `tests/` directory next to the workflows):

```json
{
  "schema": "wd-test/v1",
  "workflow": "../payrolla-salary-disbursement.wd.json",
  "env": { "payrolla_api_url": "https://payrolla.test/api" },
  "cases": [
    {
      "name": "a refused payment does not stop the others",
      "trigger": { "body": { "payroll_id": "pr_1", "...": "..." } },
      "env": {},
      "mocks": {
        "check_balance": { "output": { "balances": [ { "currency": "NGN", "available": 90000000 } ] } },
        "pay_all[0].to_wallet": { "error": { "kind": "fatal", "message": "destination wallet is frozen" } },
        "to_wallet": { "output": { "status": "completed", "amount": "=input.amount" } }
      },
      "signals": { "settle": { "payload": { "event": "wallet.outflow.confirmed", "body": {} } } },
      "approvals": { "approve": { "decision": "approved", "by": "checker" } },
      "expect": {
        "status": "completed",
        "steps": { "pay_all[0].wallet_failed": "completed" },
        "outputs": { "summary": { "paid": 1, "failed": 1 } },
        "inputs": { "report": { "body": { "paid": 1 } } },
        "calls": { "to_wallet": 2 }
      }
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `workflow` | The `*.wd.json` under test, relative to the test file |
| `env` | Tenant variables (`env.*`) for every case; a case's `env` adds to and overrides them |
| `trigger` | The run's `trigger`. For webhook workflows, `trigger.body` is checked against the workflow's inputs schema first |
| `mocks` | Outcomes of task steps (`connector`, `http`, `code`), keyed by step id or instance id (`pay_all[2].to_bank`, `compensate:pay`); an instance key wins. A mock is `{"output": ...}` or `{"error": {"kind", "message"}}`; a list answers successive attempts and its last entry repeats. Output values may be expressions over `input`, the step's resolved input |
| `signals` | `{"payload": ...}` delivers the signal (the step's output); `{"timeout": true}` lets its timeout fire |
| `approvals` | `{"decision": "approved" \| "rejected", "by": "..."}`, or `{"timeout": true}` |
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
