# Ready to publish

Before a workflow is published, Taskiem checks that everything it needs is in place where it will run, so a missing connection or setting is fixed then rather than failing a run later. The web app's publish button, the AI review, templates and chat use the same check.

## What it checks

| Need | When | Blocks publishing | The fix offered |
| --- | --- | --- | --- |
| **Connection** | A step uses a service (Paystack, Google Sheets…) that has no connection, an expired or switched-off one, or several when the step names none | Yes | **Connect** it in place: the service and name are filled in, nothing to match by hand |
| **Secret** | An expression or code step reads a private value that is not stored | Yes | Type it in place; it is encrypted when saved |
| **Table** | A step writes or reads a data table that does not exist | Yes | **Make it**, with the columns the workflow writes, filters and orders by (each typed from the form field that fills it, else text); from the AI review; or on the Tables page |
| **Setting** (variable) | An expression reads `env.<name>` and it is not set | No: it can be set later | Type it in place |
| **Role** | An approval or form step names a role no member holds | No: owners decide meanwhile | Give it to a member in place |

Publishing deploys to every environment without a promotion step (normally dev and prod). Gaps in **prod** block a publish; gaps in **dev**, a rehearsal, are listed but do not.

A publish refused for these answers `422` with `code` `not_ready` and the `needs`, each with a plain message, the steps that need it and whether it blocks. `GET /v1/workflows/{wf}/versions/{v}/readiness` and `POST /v1/readiness` (for a definition not saved yet) answer the same list without publishing.

## The AI builder

A draft's review lists what it needs, with each fix beside it, and checks again after each one. The builder is told the roles the business has and how many people hold each, never who, so it uses those roles, or `owner` in a one-person business, instead of inventing one. In chat, the build's reply lists what is needed in plain words.

## Roles

Wherever a step names a role, the role is picked from the business's roles (`GET /v1/approver-roles`), or a new one is written in words. A business role (one that is not built in, like `credit_officer`) lets its holders decide the approvals that name it, without the separate `approval.decide` permission; the web inbox, chat requests and buttons follow the same rule. Owners count for every role ([governance](governance.md#one-person-businesses)).

## In plain words

Every problem the publishing checks find carries `plain`, the problem in plain words naming the step by its name, and `fix`, where it is put right (`/connections?connect=paystack`, `/policies`, or `#step=<id>` for the step in the editor), beside its `path` and `message` (`engine/plain`, one map for the web app, the AI review and chat). A run's page has `plain_errors`, each failed step's error the same way, and chat says why a run failed when it tells the person who started it. The web app shows the plain words, with the technical message under **Details**; the AI review keeps the model, rounds and tokens there too. Settings are typed as plain text (a phone number in any form is kept as `+234…`), and a run is started from a form built from the workflow's inputs, with JSON under **Advanced**.

A test fails if an owner's page or a chat message uses the words an owner never sees (`web/src/lib/plainwords.test.ts`, `TestMessagesUseNoTechnicalWords`): JSON, schema, expression, cron, kobo, token, webhook, endpoint, payload, API key, step id, raw paths, and the like, outside **Details** and **Advanced**.
