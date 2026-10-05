# Clean-room policy

Status: **draft for counsel review** (build plan, Phase 0). This is a practical policy, not legal advice. It takes effect for a contributor once they have signed the acknowledgement at the end and an IP assignment agreement.

## Why

Taskiem competes with established workflow products. Its value, and its defence against an IP claim, depends on every line of code, connector definition, UI element and document being provably original. This policy is how we keep that provable.

## Rules

1. **No source reading.** While working on Taskiem, do not read the source code of n8n, Zapier, Make, Activepieces, Windmill, Temporal, Pipedream, Node-RED, Airflow, Prefect, Inngest, Trigger.dev, or any similar workflow, automation, or durable-execution product. This includes their GitHub repositories, code snippets in issues or blog posts, and decompiled or minified bundles.
   - Allowed: public user documentation, pricing pages, marketing pages, product demos, conference talks about concepts, and academic or general literature (for example on event sourcing, sagas, or CEL).
   - If you read such source before joining, or by accident, tell the founder. It is recorded in the decision log; it is not a disciplinary matter.
2. **No copied artefacts.** Do not copy code, connector or node definitions, workflow JSON, UI layouts, icons, illustrations, docs text, error messages, or test fixtures from another product.
3. **Original naming.** Product names, step type names, and UI terms are chosen independently and checked against competitors' trademarks before they ship.
4. **Decision log.** When a design resembles an existing product, record in `docs/decisions/` what was chosen, why, and where the idea came from (for example "event sourcing, from public literature"). Write the entry when the decision is made, not later.
5. **Licence gate.** Only licences on the allow-list in `tools/licencecheck/policy.json` may enter the dependency tree. CI blocks anything else. Adding a licence to the allow-list needs a decision-log entry and founder approval.
6. **IP assignment.** Every employee and contractor signs an IP assignment and the acknowledgement below before their first commit.
7. **AI assistants.** Code produced with AI assistance is held to the same rules. Do not prompt an assistant to reproduce or imitate a named competitor's code, and review generated code as if you had written it.

## Acknowledgement

> I have read the Taskiem clean-room policy. I have not used, and will not use, the source code or other protected artefacts of competing products in my work on Taskiem. I will record design decisions that resemble existing products in the decision log, and report any accidental exposure.

| Contributor | Role | Date signed | IP assignment on file |
| --- | --- | --- | --- |
| | | | |

The signed copies are kept outside the repository; this table only records that they exist. Gate G0 requires a row for every contributor.
