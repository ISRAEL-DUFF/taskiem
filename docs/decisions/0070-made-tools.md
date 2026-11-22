# 0070 — Made tools: a workflow declares itself a tool, and who may start a workflow is a rule

Date: 2026-11-22 · Status: **Proposed** (D1, for the founder to accept; the plan's choices were settled 19 November 2026) · Plan: [Made tools](../spec/made-tools.md) · Build plan: [M0](../spec/build-plan-saved-tools-payments.md#4-the-phases)

## Context

The tools in the chat (Products, Stock, Orders, Money and the rest) are a list in code. A business cannot add one, and a new kind of work has no tool until Taskiem builds it. Meanwhile any deployed workflow can already be started from every chat app with `run <workflow>`: it finds the workflow, asks its inputs, shows what will run and waits for **yes**. What it cannot do is answer with what the run found, appear as a tile or a `/` command, or know who it is for.

Reading the code for the plan found a second gap. **Nothing limits who may start one workflow and not another.** Anyone holding `run.start` may start every workflow the business has deployed, from the web, the phone app, the API and `run` in WhatsApp, Telegram and Slack. A tool's `roles` with nothing behind it would only hide a tile: `run <workflow>` would walk round it.

The plan's nine questions were settled with the founder on 19 November 2026 ([made tools §14](../spec/made-tools.md#14-for-the-founder-to-decide)), with the review's rule that a tool which moves money always asks for step-up. This record writes down what the code will depend on.

## Decision

1. **A tool is a block of the workflow, not a trigger type.** An optional top-level `tool` in `wd/v1` (`name`, `command`, `about`, `says`, `icon`, `for`, `answer`; [made tools §5](../spec/made-tools.md#5-the-declaration)). The inputs, the outputs and the run stay the workflow's, so one workflow has one way to be started by a person wherever they are. `tool` is in the versioned document, reviewed and published with the steps it describes, and it does not change `decide()`.
2. **Only a workflow started on demand is a tool:** its trigger is `manual`. A tool with an answer declares `outputs` and keeps the callable workflow's rules (decision 0048). The deployed version is the tool; a draft never is.
3. **Who may start a workflow is a rule of the workflow, on every path** (§14, 8). A new optional top-level field, **`roles`**: the roles whose members may start the workflow by hand, beside `run.start`; owners always may; left out, everyone with `run.start` may, as today. It is held wherever a person starts a run: the web's and the app's **Run** and the API (`POST /v1/workflows/{id}/runs`), and `run` in WhatsApp, Telegram, Slack and the web's Taskiem conversation (checked when the workflow is found and again at **yes**). A refusal says which roles may, and is in the audit (`run.start.refused`).
   - **Where it lives (a change from the plan).** The plan put `roles` inside `tool`. It is a field of the workflow instead, and `tool` has no `roles` of its own: a tool's users are its workflow's `roles`. One rule in one place cannot disagree with itself, it is useful with no tool at all, and it can be built first, on its own (the build plan's M0 asks for that).
   - **Who it does not hold.** Runs nobody starts by hand: a schedule, a webhook, an event, a form (which has its own `access` and `roles`), another workflow, a business link. A failed run resumed by a repair someone accepted goes on as the run that was allowed. An API key acts as its owner, with their roles. An embedded app's end user and a field agent hold no roles, so they cannot start a workflow that names any.
   - **Role names are not checked against the business's roles** when a version is validated (roles may be made after the workflow); the editor picks from the business's roles, and a name no member holds simply admits owners only.
4. **Care is worked out from the steps, never declared** ([made tools §8](../spec/made-tools.md#8-care-what-a-tool-asks-before-it-runs)): a tool whose every outside step reads answers at once for members, with an echo line carrying every value read; anything else asks **yes**; the business's policies, **or any step that moves money out of the business**, make it step-up, confirmed on the chat apps by the hand-off link and never by a typed yes. A step whose class is unknown counts as a write; a code step is a read only when every host function it declares is. An owner may make a tool more careful (**always ask**), never less. This changes one row of WhatsApp's security model ("every start needs an explicit yes"), for members' read-only tools only.
5. **Taskiem's own commands always win.** A `command` or a `says` phrase may not be a word of Taskiem's own commands in any language that is on; a word reserved later is Taskiem's from then on, and the owner is told.
6. **Customers and linked businesses.** The nine-workflow public menu becomes customers' tools (`for: "customers"`), by migration; customers always get **yes**, inputs by form only, and only outputs marked `public`. A tool that moves money is refused for customers and linked businesses. Linked businesses' tools are later, [in their own plan](../spec/made-tools-linked.md).
7. **The panel follows the chat tools switch; the commands do not.** `/expense` works wherever `run` works today. A tool's run counts against the run limits as any run.

## What M0's first pull request builds

- `roles` in `schemas/wd-v1.schema.json` (1 to 20 role names, unique) and in the definition model, the contract page, the SDK (`workflow({roles})` and the code it writes), `wddiff` and `wdmerge`.
- The rule at the API's start and at `run` in the chat apps, with the refusal audited and said in each language (`wa.run.not_yours`).
- **Who can start it** in the editor's Settings, picking from the business's roles.

The rest of M0 (`tool` and `answer` in the schema, the validator's rules of [made tools §12](../spec/made-tools.md#12-what-the-validator-refuses), care worked out and stored) follows in its own pull request.

## Alternatives considered

- **A `chat` trigger type.** Gives tools inputs of their own and splits `run` from tools. Rejected (§14, 1).
- **`roles` only on the tool, hiding the tile.** `run <workflow>`, the web's Run and the API would walk round it. Rejected (§14, 8).
- **`roles` both on the workflow and on the tool.** Two rules that could disagree, and the tighter one would be the only one that mattered. Rejected here, in favour of one field on the workflow.
- **Who may start a workflow as a setting outside the definition** (a table the owner edits). Not versioned, not reviewed with four-eyes, and not in Git-led deploys; a change to who may start a workflow is a change a publisher should see. Rejected.
- **A custom permission per workflow** (`run.start:<workflow>`). Would multiply roles' permission lists by workflows, and roles already say who a person is in the business. Rejected.
