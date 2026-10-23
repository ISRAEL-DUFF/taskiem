# 0046 — Templates compose: a template can be a step of another workflow

Date: 2026-10-23 · Status: **Proposed**

## Context

Templates are Taskiem's best-tested units of work. Each one is a wd/v1 definition with named, typed parameters and run inputs, its own workflow tests (`templates/tests/`), and checks that hold it to its promises: it uses exactly the connectors it declares, its capabilities expand on every rail that can run it ([0044](0044-rails-and-products.md), R4), and a template around a product moves no money (R7). The library now holds about fifty of them: Run payroll, Pay a list, payment links, refunds, the Payrolla templates, and Send an invoice from [0045](0045-tally-in-taskiem.md).

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
   - A workflow around a product (R7) may not use a template that moves money.

4. **The template version is pinned when the calling workflow is published.** A library update never changes a published workflow. The workflow's page offers the update with its differences, as `superseded_by` offers an upgrade today. Only the next publish takes it.

5. **`subflow` stays for calling another workflow of the business**, by id and version: one that is deployed, owned and changed on its own. It runs as a child run (`parent_run_id`, a kind that tells it from a fork), and is built in Phase 3 as planned. Its child's idempotency seeds include the parent run and step, so a retried parent does not repeat the child's writes. Template steps do not wait for it.

6. **The AI builder composes before it drafts.** Callable templates join its catalogue as actions with their inputs, outputs and effect. When a goal is answered by more than one template, the builder joins them with `template` steps. A new eval suite measures this: two-template and three-template goals, graded on the templates used and the rules above.

7. **A template may use other templates.** Expansion is recursive, at most three levels deep, refuses a cycle, and stays within the 500-step limit after expansion. The template tests run on the expanded definition.

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
- wd/v1 gains a `template` step type. `engine/expansion` gains its expansion, beside capability expansion and in the same pass (a template's capability steps are expanded after it), with step-id prefixes and the existing limit on composite ids (`MaxCapabilityStepID`) reviewed for two levels of prefix.
- The canvas shows a `template` step folded, with its expanded steps one click away. Run history shows the expanded steps under it.
- Readiness and the publishing checks read the expanded definition, so nothing a template needs or does can hide behind its step.
- Flow code (the SDK) gains `template(id, params, input)`, and the round trip keeps the step unexpanded.
- The builder's catalogue, prompt and evals gain callable templates (point 6).
- `subflow` (point 5) is unchanged here and stays in Phase 3.
- **Not decided here:** a business's own workflows as callable components (a private library), and templates in the connector catalogue ([0020](0020-connector-catalogue.md)) for other publishers. Both would use the same contract.

## Open questions

1. **Which templates first.** Proposed: Pay a list, Run payroll, the Payrolla summary and records, Send an invoice. Each needs `outputs` written and tested.
2. **Failure inside a template step.** Proposed: the template's own `on_error` paths run as written. A failure that escapes the template fails the `template` step, so the caller's `on_error` and retry apply to the step as a whole. A retry restarts the template's unfinished steps, never its finished ones.
3. **Params set by the caller at run time.** Params are filled when the calling workflow is built, as on the Start page, and never from run data, because they can change which connections and capabilities the template needs. Is any template's param something a caller would need per run? If so, it should be a run input instead.
4. **Showing the effect to people.** Should the Start page and the Guide say "this uses Run payroll, which pays your staff after approval" for a composed workflow? Proposed: yes, as the plain steps already name each step.

## Provenance

From a discussion on 2026-10-23: the founder asked whether templates could become higher-level connectors that join to do a complex task, and whether n8n, Zapier and similar products have this. They do, as sub-workflows and callable workflows (table above), but none of them composes its template library. This entry proposes doing that with Taskiem's own guarantees.
