# The workflow builder

The builder is where a workflow is made and changed: what starts it, then each step as a card on a canvas, joined by arrows. It fills the window. On a computer the menu shrinks to a rail of icons; on a phone the steps are a list.

Everyone builds the same way. **Advanced** (Settings) adds the technical parts on top: step ids, expressions, and the **Code** and **JSON** tabs. It does not change how the builder works.

## Starting a workflow

**Workflows → New workflow** opens **When should it run?**: a name, and what starts it.

| Card | Trigger it makes | Its first settings |
| --- | --- | --- |
| On a schedule | `schedule` | Weekdays at 9:00, Lagos time |
| A payment comes in | `capability_event` | `payment.received`, from whichever payments account is connected |
| An order comes in | `storefront` | Orders on the storefront |
| Someone fills in a form | `form` | A form with one question, titled after the workflow |
| A WhatsApp message comes in | `whatsapp` | |
| An email comes in | `email` | Mail to the workflow's own address |
| When you start it | `manual` | |
| Another app tells it | `webhook` | Its own path, signed (`hmac`) |

The workflow is saved at once as version 1 with no steps, and the builder opens on it. The same page offers a template, or **Describe it** (the [AI builder](ai.md)). **Settings → When it runs** changes the trigger later, with the same choices.

A version with no steps can be saved but not published: wd/v1 needs at least one step.

## The canvas

- **What starts it** is the pine card on the left. Clicking it opens **Settings**.
- **Each step** is a card: an icon for what it does, its name, and a line on what it uses ("WhatsApp · Send a text message"). Clicking it opens its window.
- **A +** sits on every arrow and after a path's last card. It opens **What happens next?** and adds the step there: between two steps, after one, or as a path's first.
- **An If** has two ways out, **Yes** and **No**. Each starts with a dashed **Add a step** box.
- **Do these together** shows one way out per branch. **For each** shows the steps it repeats.
- **Notes** are yellow cards for whoever works on it next. They are kept in the version's layout (`__notes`) and change nothing when it runs.
- **At the bottom left:** zoom, fit to screen, **Tidy up** (forget the moved positions and lay it out again) and **Add a note**. A map of the whole workflow sits at the bottom right.
- **Change it with AI** opens the AI builder on this workflow. **Add a step** adds one at the end.

Dragging a card keeps its place. Places are saved with the version, without making a new version. Dragging from one card's right edge to another's left makes the second wait for the first, within one list. Selecting an arrow and pressing Delete removes that wait.

**Undo** and **Redo** (Ctrl or ⌘ with Z, and with Shift or Y) go back through every change since the version was opened.

## What happens next?

The panel a + opens has:

- a search over every step's title, its service and its group;
- three suggestions (a WhatsApp message, an approval and a payment link, when they are available);
- every step, in groups: Payments, Messages, Tables and sheets, People, Decide and repeat, AI, Invoices and orders, Your other workflows, Other services, Advanced.

Payments are the capabilities (`schemas/capabilities-v1.json`): what they do, not which provider does it. The other groups come from each connector's category. Picking a step adds it with the smallest setup that works, names it in words, and opens its window.

A new step's id is short words: `code`, `approval` and `wait` for Taskiem's own steps, the action for a connector's (`send_message`), the capability for a payment (`pay_bank`). A second one gets `_2`.

## A step's window

The window has three columns.

1. **What came in.** The values the step can use: what started the run, and what each earlier step gave, at every depth (an If's path sees the steps before the If). After a test, each value shows its sample. Drag one into a setting.
2. **Its settings.** For a connector, a text setting takes words and values together. "Thanks *Name*, we got *Amount*" is saved as `='Thanks ' + string(trigger.body.name) + ', we got ' + string(…)`. Type **/** or press **Add a field** to put a value in. An If's settings are **Go the Yes way when**, built from rules.
3. **What came out.** After a test, whether the step ran and what it gave. **Keep this result for testing the next steps** reuses a connector step's output in later tests.

**Test this step** runs the test below. **Step 2 of 6** with the steps either side moves between steps. **Delete this step** joins the steps after it to the steps before it.

A payment trigger's fields (`trigger.fields`) are offered before any test, its amount in naira (`double(trigger.fields.amount_kobo) / 100.0`, which text with fields reads back as one field). Other triggers without questions show the fields of the last test's sample.

## Test with sample data

**Test workflow** (and **Test this step**) sends the draft as it is now to `POST /v1/workflows/{wf}/try` (`workflow.edit`), without saving it. The draft runs in memory in [wd-test](contracts/wd-test-v1.md)'s orchestrator:

- each connector and capability step gives a sample made from its output schema;
- approvals are approved and forms answered;
- a kept output is used instead of the sample.

Nothing is sent, no money moves, and nothing is stored. The answer has the run's status, what started it, and each step's status, input, output and error.

On the canvas, each step that ran gets a green tick. A step that stopped gets a red mark and its error in its window. A step that did not need to run (the other side of an If) is faded, with "Did not need to run". **Clear** removes the marks.

A workflow that calls itself is refused (422), as on publish. The test uses the business's approval policies and its other workflows, so a step that runs another workflow is tested too.

## An If's empty No

wd/v1 needs at least one step in each list. When an If's **No** has nothing in it, the builder leaves `default` out when it saves or tests, and still draws **No**: a choice without a default carries on. Adding a step to No makes the default again. An empty **Yes**, or an empty path of a choice with more paths, is a problem to fix. The test says which path is empty before it runs.

## Saving and publishing

**Save draft** saves a new version. Someone else's version saved meanwhile is merged, or its conflicts are shown to choose from. **Publish** saves, checks the version and publishes it; what it still needs is listed, each with its fix. A workflow that starts from a storefront form another workflow already starts is not published: the builder says which workflow runs the form and offers **Replace it with this one**, which takes that one out of use and publishes this one in one change ([storefronts](storefronts.md)); under four-eyes publishing it says why it cannot. **Start run** appears once a version is published. The problems the last save found are listed under the bar. A step with a problem has a red border, and opening it shows the problem.

## Tabs

- **Build:** the canvas.
- **Runs:** this workflow's runs, newest first.
- **Settings:** the name, **When it runs**, environments and promotion, the form editor for a form trigger, the email address for an email trigger, and **Where it starts** (its web addresses, schedules and signing secrets).
- **Code** and **JSON** (with Advanced): the workflow as TypeScript or as its wd/v1 definition, both editable. Both are plain text boxes today; a proper editor is planned in [a proper code editor](spec/code-editor.md).

## On a phone

The canvas gives way to a list:

- what starts it, then each step in order;
- an If's Yes and No under it, indented;
- **Add a step here** after each step;
- **Test workflow** at the end.

A step's window opens full screen, with its settings first, then what came in and what came out. The top and tab bars give way to the builder's own bar: back, the name, **Save draft** and **Publish**, and the tabs under it.

## Code

| Piece | Code |
| --- | --- |
| The page, tabs, saving and testing | `web/src/pages/Editor.tsx` |
| When should it run? | `web/src/pages/NewWorkflow.tsx`, `web/src/builder/triggers.ts` |
| The canvas | `web/src/builder/Canvas.tsx`, its model in `web/src/lib/flow.ts` |
| What happens next? | `web/src/builder/Picker.tsx`, `web/src/builder/catalog.ts` |
| A step's window | `web/src/builder/StepWindow.tsx`, `web/src/builder/ContainerSettings.tsx`, `web/src/builder/sources.ts` |
| Text with fields | `web/src/forms/FieldText.tsx`, `web/src/lib/fieldtext.ts` |
| The phone list | `web/src/builder/PhoneSteps.tsx` |
| Test with sample data | `api/workflows.go` (`tryDraft`), `engine/ai/builder/dryrun.go` (`TryDraft`) |
| Tests | `web/src/lib/flow.test.ts`, `web/src/builder/*.test.ts`, `api/try_test.go`, `engine/ai/builder/builder_test.go`, `web/e2e/builder.spec.ts` |
