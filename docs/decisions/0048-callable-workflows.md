# 0048. A business's own workflows as callable components

**Status:** Accepted (2026-10-24)

## Context

[0047](0047-composable-templates.md) made the library's callable templates steps of a workflow: a template with typed run inputs and outputs is expanded into the calling workflow at deploy, so its approvals, payments and waits run in one run with one history and one set of exactly-once rules. It left one thing undecided: a business's own workflows as callable components, a private library. The need is the same as for templates. A business builds "Check a customer's credit" once and wants it inside "Approve a loan" and "Open a trade account"; a payroll run it has shaped to its own rules is what its month-end workflow should call, not a copy. Today the only ways are copying the steps (two copies to keep in step) or `subflow`, which is still unsupported at run time and, when it comes, runs a child run with its own history.

## Decision

1. **A workflow with `outputs` is callable.** wd/v1 gains a top-level `outputs`, with the shape a callable template's has: typed values over the workflow's own top-level steps, with an optional chain for a step that may not run. A workflow that declares outputs keeps the rules a callable template keeps (an inputs schema, no types, short step ids, no `respond` step, the trigger read only as `trigger.body` or under `has()`), checked by `engine/wd.Validate` as one set of rules shared with the template library. Without outputs a workflow is what it was.
2. **A template step names it by id.** The `template` step gains `workflow` (the workflow's UUID) as the alternative to `template`; exactly one is given, and a workflow takes no `params`. Everything else is the template step as built: the expansion into `<id>__body`, `<id>__input` and `<id>`, the id budget, the depth and cycle limits, `origin` on the expanded steps, the inherited rules, readiness and the run's pinned expansion.
3. **The published version is the component.** A workflow is callable once it has a published version, and that version is what a step uses: it is read as a template with no parameters (`runtime.WorkflowTemplate`), pinned with the calling version in `workflow_template_pins` under the workflow's id, and offered in `template_updates` when a newer version is published, so a change to the called workflow reaches its users only when they save. A draft that names a workflow with no published version, or one without outputs, carries the problem until there is one.
4. **One resolver.** Every place that finds a template step's template takes a resolver (`expansion.Templates`): the library, or `runtime.Callables`, which answers a UUID from the business's workflows and anything else from the library. The publishing checks, pins, readiness, the run's expansion, the AI builder's dry run and policy rules, and the read-back use it.
5. **Only the business's own.** Another business's workflows are not found; an embedded app's end user may not name one of the partner's customer's workflows (`workflow:<id>` in `not_allowed`); a workflow may not use itself; a chain that comes back to itself is refused as a cycle.
6. **Surfaces.** `GET /v1/workflows/callable` lists the callable workflows with their inputs, outputs and effect. The editor's template step picker gains a "Your workflows" group, and the inputs editor a "What it gives back" editor for the outputs. The SDK gains `outputs` on the header and `callWorkflow(id, input)`. The AI builder's context lists the callable workflows and its prompt says how to use one. The read-back says what a template step uses and what it does, for templates and workflows alike, in the five languages.

## Alternatives considered

| Alternative | Why not |
|---|---|
| `subflow` (a child run) | Its guarantees would have to span two runs, as 0047 found; and it does not exist at run time yet. It stays for workflows that must run and change on their own |
| Promote a workflow into the library | The library is Taskiem's, tested and shipped; a business's workflow is its own, changed on its own schedule. Pinning the published version gives the same safety without a second place to keep it |
| Any version callable, chosen by the caller | Two workflows could call two versions of the same component; the editor would have to explain versions. The published version is the one the business stands behind, and the update offer tells each user when it moved |
| Outputs declared on the version rather than the definition | The definition is what flow code, Git and the SDK carry; outputs belong with the steps they read |

## Consequences

- wd/v1 gains top-level `outputs` and the `workflow` field of a template step ([contract](../contracts/wd-v1.md#callable-workflows)). The template library's `checkCallable` now calls `wd.CheckCallable`, so the two cannot drift.
- `runtime.PinTemplates`, `PinnedTemplates`, `TemplateUpdates`, `wdtest.RunCaseWith`, `wdtext.DescribeWith` and the builder's `PolicyFindingsWith`, `HappyPathWith` and `DryRunWith` take a resolver; the library-only forms remain.
- A pinned workflow is a template keyed by a UUID: the pins table's id check already allows it.
- An expansion with template steps and no capability steps stores an empty list of choices (it stored null, which the table refuses; found while testing this).
- Templates in the connector catalogue for other publishers remain undecided.

## Provenance

From the review of 0047 as built (2026-10-24): the founder asked for the two items left undone there that could be done without `subflow`, the read-back of what a template step does and a business's own workflows as callable components.
