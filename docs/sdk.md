# Workflow code: `@taskiem/sdk`

Workflows can be written as TypeScript (spec 10.1) and edited on the canvas; both are the same `wd/v1` definition. `.flow.ts` files sit beside their `.wd.json` definitions in a repository, and both are committed.

```ts
import { cel, foreach, steps, transform, webhook, workflow } from "@taskiem/sdk";
import { iswallet } from "@taskiem/connectors";

export default workflow({
  id: "wf_payEmployees",
  name: "Pay employees",
  trigger: webhook({ path: "/payroll", auth: "hmac", dedup: ({ trigger }) => trigger.body.payroll_id }),
})
  .step("balance", iswallet.get_balance({ wallet_id: ({ trigger }) => trigger.body.wallet_id }))
  .next("pay", foreach({ items: ({ trigger }) => trigger.body.employees, max_concurrency: 5 }, steps()
    .step("transfer", iswallet.transfer({
      from_wallet_id: ({ trigger }) => trigger.body.wallet_id,
      to_wallet_id: ({ item }) => item.wallet_id,
      amount: ({ item }) => item.net_pay_kobo,
    }, { effect: { idempotency_seed: ({ trigger, item }) => trigger.body.payroll_id + ":" + item.employee_id } }))))
  .next("count", transform(({ steps }) => cel.size(steps.pay.output)));
```

## Building blocks

| Piece | Meaning |
| --- | --- |
| `workflow({ id, name, trigger, description?, version?, inputs?, types?, settings? })` | The definition's header |
| `.step(id, spec, opts?)` | A step. It starts as soon as the steps in `opts.needs` have settled; with no `needs` it starts at once, as in the definition |
| `.next(id, spec, opts?)` | A step that needs the step before it (plus any in `opts.needs`) |
| `opts` | `name`, `description`, `needs`, `when`, `retry`, `timeout`, `on_error: steps()...` |
| Step helpers | `transform`, `http`, `code`, `wait`, `signal`, `approval`, `branch`, `parallel`, `foreach`, `subflow`, `ai`, and `connector(ref, action, input, extra)` |
| Connector helpers | `@taskiem/connectors` has one typed helper per action of every built-in connector: `iswallet.transfer(input, { effect, compensate, connection })` |
| Triggers | `webhook`, `schedule`, `connectorEvent`, `manual`, and `trigger(type, config)` |
| Nested steps | `steps().step(...)` for foreach bodies, branch paths, parallel branches, and `on_error` |

## Expressions

A value is a literal, a CEL string (`"=trigger.body.amount"`), or an arrow function over the context `{ trigger, steps, env, run, secrets, item, index }`, compiled to CEL when the workflow is built:

| Code | CEL |
| --- | --- |
| `({ trigger }) => trigger.body.amount * 100` | `trigger.body.amount * 100` |
| `({ steps }) => steps.approve.output.decision === "approved"` | `steps.approve.output.decision == 'approved'` |
| `` ({ trigger }) => `Salary ${trigger.body.period}` `` | `'Salary ' + string(trigger.body.period)` |
| `({ item }) => cel.has(item.wallet_id)` | `has(item.wallet_id)` |
| `({ trigger }) => cel.size(trigger.body.xs.filter((x) => x.ok))` | `size(trigger.body.xs.filter(x, x.ok))` |
| `.some(...)`, `.every(...)`, `.map(...)` | `.exists(...)`, `.all(...)`, `.map(...)` |
| `cel.in(x, list)` | `x in list` |

Only what has a CEL meaning compiles. `==` (use `===`), `?.`, `??`, `.length` (use `cel.size`), JavaScript library calls, and variables from outside the function are refused with the field they are in and what to use instead. Comparisons inside comparisons need parentheses, because JavaScript and CEL rank them differently. Anything beyond expressions belongs in a `code` step.

The spec's sketch passes `(t) => t.body...` and `(s) => s.approve...`. The SDK uses one context object instead, so a function can read the trigger and earlier steps together, and `steps.<id>.output` is written out as in the definition, so code and canvas read the same.

## Code and canvas

| From | To | How |
| --- | --- | --- |
| `.flow.ts` | `.wd.json` | `taskiem build` (the CLI compiles with the SDK built into the binary; no Node needed). `taskiem build --check` fails CI when a definition is stale |
| `.wd.json` | `.flow.ts` | `taskiem codegen --write`. Output is deterministic, so Git diffs show only the change |
| Canvas | Code | The editor's Code tab shows the workflow as code; edits there are compiled and applied |

Generated code always builds to exactly the definition it came from: an expression is printed as an arrow function only when compiling that function gives back the identical CEL text, and is kept as a CEL string otherwise. Every workflow in `flows/` is checked in both directions by `go test` and `pnpm test`.

Flow code runs in the sandbox when it is built. It may import `@taskiem/sdk`, `@taskiem/connectors`, and (from the CLI) other local files; other packages are refused.

Editors: `flows/tsconfig.json` maps the two modules to `sdk/src`. Flow data is untyped, so it turns off `noImplicitAny` for callback parameters such as `(e) => e.amount`.
