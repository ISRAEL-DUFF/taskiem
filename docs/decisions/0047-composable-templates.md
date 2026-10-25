# 0047 — Templates compose: a template can be a step of another workflow

Date: 2026-10-23 · Status: **Accepted** (built 2026-10-24; see [As built](#as-built))

## Context

Templates are Taskiem's best-tested units of work. Each one is a wd/v1 definition with named, typed parameters and run inputs, its own workflow tests (`templates/tests/`), and checks that hold it to its promises: it uses exactly the connectors it declares, its capabilities expand on every rail that can run it ([0044](0044-rails-and-products.md); milestone R4 of the [rails and products spec](../spec/rails-and-products.md#r4-more-templates-on-rails-m)), and a template around a product moves no money (milestone R7 of the same spec, [Templates around Payrolla](../spec/rails-and-products.md#r7-templates-around-payrolla-sm-needs-r5)). The library now holds about fifty of them: Run payroll, Pay a list, payment links, refunds, the Payrolla templates, and Send an invoice from [0045](0045-tally-in-taskiem.md).

Today a template can only be **copied**: instantiating it makes a draft workflow of its own. Nothing can **use** one. A business that wants "at month end, run payroll, then record it for the accountant, then email the owner the balance" gets three separate workflows, or one hand-built workflow that repeats the three templates' steps and loses their tests. The AI builder has the same problem: given a goal that two templates answer together, it drafts fifteen steps from scratch instead of joining the two it already trusts.

Other automation products let one workflow call another:

| Product | How |
| --- | --- |
| n8n | **Execute Workflow** node calling a workflow that starts with an **Execute Workflow Trigger**, which may declare its input fields; the last node's output comes back |
| Zapier | **Sub-Zaps**: a Zap that starts with "Start a Sub-Zap" and ends with "Return from a Sub-Zap", called by "Call a Sub-Zap" |
| Make | **Scenario inputs and outputs**, with "Run a scenario" waiting for the result |
| Power Automate | **Child flows** with typed inputs and outputs |
| Workato | **Callable recipes** and **recipe functions**: typed input and output schemas, used from other recipes like an action, shared in a library |
| Temporal | **Child workflows**, with their own history, retries and cancellation |

In all of them the reusable unit is a workflow someone built. None of them makes its **template library** a set of components: their templates are starting points to copy and edit. Workato's recipe functions come closest, and they carry no guarantees beyond their schemas.

Taskiem already plans half of this. The `subflow` step type exists in wd/v1 (`config: {workflow, version, wait}`, with an `input`) and in the architecture (call another workflow, pinned to a version, `runs.parent_run_id`). It validates, but fails at run time as `unsupported` until Phase 3 ([wd-v1](../contracts/wd-v1.md)). And deploy-time expansion of a capability step into the rail's own steps (0044, R2) is a working example of one step becoming several, pinned per run.

## Decision

1. **A template may declare itself callable.** Such a template states its interface as a contract:
   - **`inputs`**: the run inputs schema it already has.
   - **`outputs`**: a new result schema, and the expressions over its own steps that fill it.
   - **Its effect**: the strongest effect among its steps, computed from the steps, never declared. That is read, or a write, or a write that moves money, plus whether it waits for a person and how long it can take.

   A template that starts only on an event, with no run inputs, is not callable until it has them. A test fails a callable template whose outputs read steps that may be skipped without an optional chain.

2. **A `template` step uses a callable template inside another workflow.** It takes `template` (the id), `params` (the template's parameters, filled as on the Start page) and `input` (its run inputs, as expressions over the calling workflow). Like a capability step, it is **expanded when the workflow is deployed**:
   - The template's steps go in place of the step, with their ids prefixed `<step>__`.
   - Each `trigger.body` read becomes the step's input.
   - The step's own id holds the template's outputs, so `steps.<id>.output` is the template's result.

   The expansion is pinned with the run, as capability expansion is. **One run, one history, one set of exactly-once rules**: there is no second run to keep in step, and every idempotency seed inside stays unique, because its step ids are prefixed.

3. **The calling workflow inherits the template's rules, not just its steps:**
   - An approval inside it stays an approval.
   - A payment inside it makes the calling workflow one that moves money, so the publishing checks and the builder's rules (`approval_before_payment`, `idempotency`, `no_payment_with_product`) see it.
   - Readiness lists the template's connections, capabilities and tables as the caller's needs.
   - A workflow around a product (milestone R7 of the rails and products spec) may not use a template that moves money.

4. **The template version is pinned when the calling workflow is published.** A library update never changes a published workflow. The workflow's page offers the update with its differences, as `superseded_by` offers an upgrade today. Only the next publish takes it.

5. **`subflow` stays for calling another workflow of the business**, by id and version: one that is deployed, owned and changed on its own. It runs as a child run (`parent_run_id`, a kind that tells it from a fork), and is built in Phase 3 as planned. Its child's idempotency seeds include the parent run and step, so a retried parent does not repeat the child's writes. Template steps do not wait for it.

6. **The AI builder composes before it drafts.** Callable templates join its catalogue as actions with their inputs, outputs and effect. When a goal is answered by more than one template, the builder joins them with `template` steps. A new eval suite measures this: two-template and three-template goals, graded on the templates used and the rules above.

7. **A template may use other templates.** Expansion is recursive, at most three levels deep counting the calling workflow (a workflow, a template it uses, a template that one uses), so a step id carries at most two prefixes. It refuses a cycle, and keeps every scope within the limit of 500 steps per scope ([wd-v1](../contracts/wd-v1.md), rule 11) after expansion: a template's steps are counted in the scope where its `template` step stands, and a nested scope inside the template counts on its own. The template tests run on the expanded definition.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Template steps as child runs (each call a `subflow` run) | Exactly-once, approvals, waits, cancellation and compensation would each have to span two runs, and a payroll inside a child run is the case where getting that wrong costs most. Deploy-time expansion gets composition with today's guarantees unchanged. Child runs remain for workflows that must change on their own (point 5) |
| Only `subflow`, calling instantiated copies of templates | The business would have to instantiate, publish and keep each template as a workflow before using it, and the copy loses its link to the library's tested version. Composition would need the same manual work it is meant to remove |
| Leave composition to the AI builder, copying steps from several templates | The result is a workflow nobody tested, with no record of where its steps came from and no way to take a template's fixes. That is what composition is meant to replace |
| A connector per template (`templates@1` with one action per template) | A connector action is one call to one provider. Templates have approvals, waits and several providers' steps. Hiding them inside an action would hide their steps from the run history, the canvas and the publishing checks, the same reason 0044 refused a `money@1` dispatcher |
| Copy n8n or Zapier as they are: untyped inputs, whatever the last step returns | Taskiem's templates already have typed inputs and tests. Untyped outputs would make composed workflows fail at run time on a missing field the checks could have caught |

## Consequences

- The template format gains `callable` and `outputs`. `templates_test.go` checks each callable template: its outputs against its steps, its effect computed, and its tests run on the expanded form.
- wd/v1 gains a `template` step type. `engine/expansion` gains its expansion, beside capability expansion and in the same pass (a template's capability steps are expanded after it), with step-id prefixes and the limit on composite ids reviewed for two levels of prefix. Step ids are at most 63 characters (`^[a-z][a-z0-9_]{0,62}$`), and each prefix spends the calling step's id plus `__` of that budget. The longest id in the library today is 22 characters (`record_rejected_failed`), so the 40 that `MaxCapabilityStepID` allows a capability step would not fit even one prefix (40 + 2 + 22 = 64). A `template` step therefore gets its own, smaller cap, set against the library's longest id, and the publishing checks also refuse an expanded id over 63 characters, naming the `template` step that produced it.
- The canvas shows a `template` step folded, with its expanded steps one click away. Run history shows the expanded steps under it.
- Readiness and the publishing checks read the expanded definition, so nothing a template needs or does can hide behind its step.
- Flow code (the SDK) gains `template(id, params, input)`, and the round trip keeps the step unexpanded.
- The builder's catalogue, prompt and evals gain callable templates (point 6).
- `subflow` (point 5) is unchanged here and stays in Phase 3. Built since: [0049](0049-subflows.md).
- **Not decided here:** a business's own workflows as callable components (a private library), and templates in the connector catalogue ([0020](0020-connector-catalogue.md)) for other publishers. Both would use the same contract. The first is now [0048](0048-callable-workflows.md).

## Open questions, as answered

1. **Which templates first.** Pay a list, Run payroll, Send an invoice, and the Payrolla payroll summary and records, each with `outputs` checked against its own workflow tests.
2. **Failure inside a template step.** The template's own `on_error` paths run as written. A failure that escapes them fails the template's body, and the `template` step's `on_error`, when it has one, runs for the step as a whole. A template step takes no `retry` of its own: each of the template's steps keeps its own retries, so nothing finished runs again.
3. **Params set by the caller at run time.** Params stay values filled when the calling workflow is built. Run payroll's month was the one value a caller needs per run, so it became a run input (`period`). A parameter only a template's trigger uses (Run payroll's pay day) is not a template step's to give.
4. **Showing the effect to people.** The editor's template step says what the template does ("Moves money · waits for a person to approve or answer"), what it gives back and its steps. The read-back, which the Start page, the Guide and chat show, names the template and what it does ("Use the template: Run payroll from any payments account, which moves money, waits for a person to approve or answer and waits for something to happen"), in each language.

## As built

The decision holds as written, with these choices made while building it:

- **The expansion's shape.** A template step `<id>` becomes `<id>__body`, a `branch` with one path that always runs and holds `<id>__input` (a transform giving the step's input) and the template's steps, then `<id>`, a transform giving its outputs. A flat expansion cannot work: a step whose need was skipped is skipped too (wd-v1, rule 4), so outputs that needed the template's last steps would be skipped whenever the template branches on an approval. A branch completes once its steps have run or been skipped. The template's steps are therefore counted in the body's own scope, not in the template step's.
- **Keys inside a template.** A template's expressions read a foreach iteration's or a handled step's outputs by step id (`o.pay.status`, `has(o.pay_failed)`). Each expanded step carries `origin` (the template, the template step, and its id inside the template), and decide keys its output by that id, so every template expression works unchanged after its ids are prefixed. Capability expansion keeps `origin` on the step that keeps its id.
- **The id budget.** A template step's id is at most 14 characters; a callable template's step ids at most 31, and its capability steps' at most 8. With two prefixes, every expanded id stays a valid step id, and every capability step's stays within the 40 characters its rail's composite needs. A test expands each callable template two deep, under the longest ids allowed, on every rail that can run it.
- **Pinned on save.** Versions are immutable and every save is a new version, so templates are pinned when a version is saved (`workflow_template_pins`), not at publish: publishing publishes a saved version as it was saved. The workflow's page offers an update with its differences, and the next version saved takes it.
- **Outputs.** Each output is typed (`string`, `integer`, `number`, `boolean`, `list`, `object`). A step that may not run is read with an optional chain, and the library refuses to load a template that breaks this. A whole-number `number` reads back as an integer after the run's history, as every step output does.
- **The product rule is enforced.** A workflow around a product (a Payrolla trigger or step) that uses a template that moves money is refused by the publishing checks, not only flagged by the builder's evaluation.
- **Embed apps.** A template step's template must be in the app's `allowed_templates`, and the connectors its steps use count as the workflow's.
- **The builder's evaluation.** `evals/builder/composition.jsonl` holds two- and three-template goals graded on the templates used, and on the expanded draft for connectors, steps and rules. The offline heuristic composes only when the goal sequences two strongly matched callable templates; the real measure is the model's.

## Provenance

From a discussion on 2026-10-23: the founder asked whether templates could become higher-level connectors that join to do a complex task, and whether n8n, Zapier and similar products have this. They do, as sub-workflows and callable workflows (table above), but none of them composes its template library. This entry proposes doing that with Taskiem's own guarantees.
