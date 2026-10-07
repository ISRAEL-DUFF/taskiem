# AI repair evaluation suite

**Synthetic seeds pending review by people.** Failure fixtures written by engineering from the dogfood flows and the classifier's rules ([decision 0016](../../docs/decisions/0016-repair-and-resume.md)); [needs-people AI2](../../docs/needs-people.md#phase-3) adds real failures from dogfooding and design partners, and AI1 a real model.

Run with `go run ./tools/aieval -mode repair` ([AI: evaluation](../../docs/ai.md#evaluation)).

## Format

```json
{"id": "data-missing-trigger-field", "tags": ["data"],
 "definition": "../../flows/dogfood/ops-payout-failure-alert.wd.json",
 "failure": {"reason": "run_failed", "run_status": "failed", "step": "sms", "error": {"kind": "expression", "message": "no such key: customer", "next": "fail"}},
 "regression": {"trigger": {"body": {"id": "signup-1"}}, "mocks": {"sms": [{"output": {}}]}, "expect": {"status": "completed"}},
 "expect": {"class": "data", "certain": true}}
```

| Field | Meaning |
| --- | --- |
| `definition` | A path relative to this file, or the wd/v1 document itself |
| `failure` | What the repair service records (`repair.Failure`): reason, run status, step, connector action, error kind and message, drift findings; values redacted |
| `run` | Optional redacted run view the model sees |
| `regression` | Optional wd-test/v1 case reproducing the failure: a patch must make it pass (a do-nothing patch fails it) |
| `expect.class` | transient, credential, data, schema_drift, logic or unknown_outcome |
| `expect.certain` | Whether the rules should decide alone, without asking the model |

Measured: class accuracy (final and rules alone), whether the rules are sure exactly when expected, model calls, and for classes that patch, whether the patch passes the publishing checks, changes the definition, and passes the regression case. Regenerate `baseline.json` with `-baseline-out` when a change to the fixtures or the classifier is intended.
