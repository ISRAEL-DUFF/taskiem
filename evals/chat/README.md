# Chat evaluation suite

**Synthetic seeds pending review by people.** Every message here was written by engineering, and every one outside English is a draft until a native speaker reviews it ([needs-people AI2](../../docs/needs-people.md#phase-3)). The suite checks two things the model does in chat ([decision 0060](../../docs/decisions/0060-no-sheet-needed.md), R6):

- **Routing** (`kind: route`, `routing.jsonl`): a message in one of the five languages is routed to the table and debtor commands (`tables`, `add`, `find`, `new_table`, `change_row`, `delete_row`, `owes`, `paid`, `ask`) or to `unknown`. The model never confirms anything; every write waits for the person's own yes.
- **Questions over tables** (`kind: spec`, `questions.jsonl`): a question becomes a query spec over the tables in `tables.json`, whose columns are all the model sees, never a row. A case expects a spec (its table, aggregate, conditions in any order, and first order; not its limit), or `refused`: the model says the tables cannot answer, or its spec fails `datatables.CheckQuery`, so nothing would be read.

Run it with `go run ./tools/aieval -mode chat` (offline, a keyword heuristic, not a model), `-compare evals/chat/baseline.json -tolerance 0` (the CI gate), or `-provider anthropic` for the real model ([AI: evaluation](../../docs/ai.md#evaluation)).

## Format

```json
{"id": "pcm-route-ask", "kind": "route", "language": "pcm", "message": "how many customers dey for Ikeja?", "expect": {"intent": "ask", "arg": "ikeja"}, "tags": ["ask", "pcm"]}
{"id": "en-spec-count-area", "kind": "spec", "language": "en", "message": "how many customers in Ikeja?", "tables": "shop",
 "expect": {"spec": {"table": "customers", "filters": [{"column": "area", "op": "eq", "value": "Ikeja"}], "aggregate": {"fn": "count"}}}, "tags": ["count", "en"]}
```

| Field | Meaning |
| --- | --- |
| `kind` | `route` or `spec` |
| `language` | `en`, `pcm`, `yo`, `ha` or `ig` |
| `message` | What the person sends |
| `tables` | For a spec case, the table set in `tables.json` |
| `expect.intent`, `expect.arg` | The intent a route case must give, and words its argument must contain |
| `expect.spec`, `expect.refused` | The spec a spec case must give (it must pass its own checks), or that it must give none |

Changing or adding cases changes the offline baseline: regenerate it with `go run ./tools/aieval -mode chat -baseline-out evals/chat/baseline.json` in the same change.
