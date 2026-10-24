# Templates

Ready workflows for small businesses (spec 11.1, SME tier): pick one, fill in a few details, and it becomes a draft workflow to review and publish. The same library is the AI builder's first stop ([AI](ai.md#what-it-does)) and what [`build` on WhatsApp](whatsapp.md#build) starts from, and it is what embed apps' `allowed_templates` name ([embedding](embedding.md)).

## The library

| Id | What it does | Connectors |
| --- | --- | --- |
| `daily-reminder` | Every day at the time you choose, remind the owner of what they asked: nothing to connect, the first starter | Tell me |
| `debtor-reminder-sms` | Every week, text each customer on the debtors sheet how much they still owe | Google Sheets, Tell me |
| `debtor-reminder-whatsapp` | The same by an approved WhatsApp template | Google Sheets, WhatsApp |
| `debtor-reminder-table` | Every week, text each customer in the debtors [data table](data-tables.md) who still has a balance; no spreadsheet | Xenibase (the `data_tables` connection), Tell me |
| `send-invoice` | Make an [invoice](invoices.md) for a customer, send it with its page, and tell the owner when it is paid, or that it is still unpaid after the days chosen | Invoices, Tell me |
| `invoice-follow-up` | Replaced by `chase-overdue-invoices` (`superseded_by`): no longer offered; workflows made from it (Paystack only, invoices kept in another tool) keep working and are offered the upgrade | Paystack, Tell me |
| `chase-overdue-invoices` | Every morning, remind each customer whose [invoice](invoices.md) is overdue by SMS and email with its link, every few days up to a number of reminders, then tell the owner once. Each reminder goes once however often the workflow runs (its `effect.idempotency_seed` names the invoice and the reminder) | Invoices, Tell me |
| `invoice-paid-receipt` | The moment an invoice is paid in full ([invoice trigger](invoices.md#starting-a-workflow-when-an-invoice-is-paid)), thank the customer once with the link to the invoice (now showing it paid), and tell the owner. Workflows made from its earlier hourly version keep running as they were | Invoices, Tell me |
| `transfer-received-notify` | Tell the owner about each payment received (a payment page paid, or a transfer into a customer's account number) and log it in a sheet | Any rail that sends payment events (Paystack, Moniepoint, Flutterwave), Tell me, Google Sheets |
| `daily-sales-summary` | Every evening, add up today's sales from the sales sheet and tell the owner | Google Sheets, Tell me |
| `sales-ledger-sheet` | Record every Paystack payment in a ledger sheet | Paystack, Google Sheets |
| `weekly-balance-email` | Every week, email the owner the payments account's balance | Any rail with a balance (Paystack, Moniepoint, iswallet, Flutterwave or Anchor), Gmail |
| `payment-thank-you-sms` | Thank customers by SMS when Paystack confirms their payment | Paystack, Tell me |
| `payment-receipt-whatsapp` | Send customers an approved WhatsApp receipt when they pay | Paystack, WhatsApp |
| `new-customer-welcome` | Welcome new sign-ups by SMS and add them to the customer sheet | Tell me, Google Sheets |
| `new-order-alert-sms` | Tell the owner each new order from the online shop (needs a developer: it starts from a web address the shop calls) | Tell me |
| `low-balance-alert` | Tell the owner when the payments account's balance drops below a level | Any rail with a balance (Paystack, Moniepoint, iswallet, Flutterwave or Anchor), Tell me |
| `failed-transfer-alert` | Tell the owner and email the accountant when a payment sent fails or comes back | Any rail that reports payouts (Paystack, Moniepoint, iswallet, Flutterwave or Anchor), Tell me, Gmail |
| `stock-reorder-alert` | Every morning, tell the owner the items at or below their reorder level | Google Sheets, Tell me |
| `payroll-reminder` | Before payday, remind the owner on WhatsApp or Telegram and the accountant by email | Tell me, Gmail |
| `run-payroll` | On payday, check the staff table and every account's name with the bank, get the payroll approved, pay each person once through whichever payments account is connected, wait for the bank to confirm, record every payment and the payroll, tell each person and the owner, and watch each bank payment for 35 days in case it comes back ([rails and products](spec/rails-and-products.md#51-run-payroll)) | Any rail that can pay bank accounts (Paystack, Moniepoint, iswallet, Flutterwave, Anchor), Xenibase (the `data_tables` connection), Tell me |
| `salary-payout-approval` | Replaced by `run-payroll` (`superseded_by`): no longer offered; workflows made from it keep working and are offered the upgrade | Google Sheets, Paystack, Tell me |
| `supplier-payment-approval` | Check the name on a supplier's account, pay the invoice after approval, follow it until the bank confirms, and tell the owner | Any rail that pays bank accounts (Paystack, Moniepoint, iswallet, Flutterwave or Anchor), Tell me |
| `pay-a-list` | Pay each row of a batch (suppliers, refunds owed, prizes, commissions) once after one approval, follow each to its end and record it | Any rail that pays bank accounts (Paystack, Moniepoint, iswallet, Flutterwave or Anchor), data tables, Tell me |
| `payment-link-per-order` | Send a customer a payment link for an order, remind them once, and record the order as paid or unpaid | Any rail that makes payment pages (Paystack, Moniepoint), data tables, Tell me |
| `virtual-account-per-customer` | Open a customer their own account number to pay into, record it and send it to them | Moniepoint, Paystack, Flutterwave or iswallet, data tables, Tell me |
| `refund-with-approval` | Look a payment up, refund it once after approval, and tell the owner | Paystack, Tell me |
| `payrolla-tell-staff` | When Payrolla pays an employee, tell them the amount and send their payslip link | Payrolla, Tell me |
| `payrolla-payroll-summary` | When Payrolla finishes a payroll, tell the owner who was paid, the total, and who was not and why | Payrolla, Tell me |
| `payrolla-approve-by-chat` | When a payroll is ready in Payrolla, send the approvers its total and approval link; remind them the day before payday | Payrolla, Tell me |
| `payrolla-reversal-alert` | When the bank returns a salary Payrolla paid, alert the owner and the payroll admin | Payrolla, Tell me |
| `payrolla-record-payroll` | Record each finished Payrolla payroll and every payment in data tables for the accountant | Payrolla, data tables |
| `payrolla-payday-check` | Every morning, compare payrolls due soon with the funding account's balance and warn if short | Payrolla, any rail with a balance, Tell me |
| `payrolla-lock-reminder` | Every morning, remind the payroll admin of approved payrolls near payday that are not locked | Payrolla, Tell me |
| `kyc-bvn-check` | Check an applicant's BVN with Dojah; welcome them if the name matches, flag it otherwise | Dojah, Tell me, Gmail |
| `pos-sales-record` | Every transaction your Moniepoint POS terminals make becomes a row in the `pos_sales` [data table](data-tables.md); the record the next three read ([POS terminals](strategy/pos-terminals.md)) | Moniepoint POS terminals, Xenibase (the `data_tables` connection) |
| `pos-daily-summary` | Every evening, add up today's POS sales per terminal and per kind from the table and tell the owner | Xenibase, Tell me |
| `pos-sale-alert` | Tell the owner at once about a POS sale at or above an amount, or a payment that was declined or reversed | Moniepoint POS terminals, Tell me |
| `pos-cashup` | At closing, compare a terminal's day with the sales book, tell the owner the difference and keep the check in the `pos_cashups` table | Xenibase, Tell me |
| `pos-opay-sales-record` | Every payment OPay reports on the business account (terminals included) becomes a row in `pos_sales`, with the fee and the amount settled | OPay Business, Xenibase |
| `pos-opay-nightly-sync` | Late every night, ask OPay for each terminal's successful payments of the day and add any a notification missed | OPay Business, Xenibase |
| `pos-opay-backfill` | On demand: a terminal's successful OPay payments between two days (up to a week) into `pos_sales` | OPay Business, Xenibase, Tell me |
| `pos-opay-settlement-check` | Every evening, what OPay took today, the fees, what was settled, and the account balance, to the owner | Xenibase, OPay Business, Tell me |
| `xenibase-new-row-whatsapp` | When a row is inserted into a Xenibase table, send the number in it an approved WhatsApp template (Taskiem creates and removes the Xenibase webhook itself) | Xenibase, WhatsApp |


**Tell me** is `notify@1`, built in: no connection or account elsewhere ([Tell me](integrations/notify.md)). Messages to the owner and staff go to the WhatsApp or Telegram they linked to Taskiem, or by email; texts to customers go by Taskiem's own SMS and count in the plan's `sms_monthly`. Termii stays available for a business texting its own customers in bulk from its own account. Templates that start from a web address another system calls (a webhook) are marked **needs a developer** (`needs_developer` in `GET /v1/templates`) and offered after the others; the first starters need no developer.

Every template that moves money does so after an approval step and with an idempotency key, so a retry never pays twice. A template that pays through **capability steps** ([wd-v1](contracts/wd-v1.md#capability-steps)) lists `capability:<name>` among its `connectors` (`capability:pay_bank`): it runs on any rail that can, and is `available` when one the tenant can use declares each. A template may have **workflow tests** of its own in `templates/tests/<id>.test.json` (`{schema: "wd-test/v1", template, params, cases}`, each case optionally with its own `params`), run against the template filled with its examples and those params; `run-payroll`'s re-express the old Payrolla flow's cases. A template around a product a business already runs names it in `product` (`payrolla`, [decision 0044](decisions/0044-rails-and-products.md)): retrieval offers it only for a goal that names the product, and then above the rest, the Start page offers it only to a business that connected the product, and `templates_test.go` checks that it moves no money (its capability steps and its steps on rails only read). A template about one thing names, in `requires`, the words a goal must use for retrieval to offer it: a word, or a phrase whose words must come in that order with only stop words between ("bill a customer" meets "bill my customers", never "pay a customer's bill"). It is then offered above templates that merely share its words, and only then: the invoice templates require "invoice" (and `send-invoice` also "bill a customer", "bill a client" or "bill someone"), so loans overdue in a database never meet `chase-overdue-invoices`. A template replaced by another names it in `superseded_by`: it leaves the gallery, the website and the AI builder's matches, stays loadable, and `GET /v1/workflows/{wf}` answers `upgrade: {template, title}` for workflows made from it. Every one is a valid wd/v1 definition: `templates_test.go` fills each with its example values and checks that it passes the publishing checks, uses exactly the connectors it declares, has no policy finding, runs its generated dry-run happy path, reads aloud as plain steps without an expression, and round-trips through flow code (spec 10.2) unchanged.

## Tables a template makes

A template that keeps records in a [data table](data-tables.md) declares the tables it works on, with their columns (`tables` in the template file). When the template is used, Taskiem makes any that the business does not have yet, after the draft is saved: a table that exists already is left exactly as it is, and one that cannot be made (no data tables on the platform, the person lacks `table.edit`, the limit is reached) is reported with the reason, never a reason to lose the draft. A declared table's name may be the marker of a parameter with the table picker (`{{table}}`), so the person can point the template at a table of their own; the Templates page says which tables a template keeps its records in, and the API answers what became of each (`tables: [{name, status: created | exists | skipped | failed, error?}]`). The `pos-*` and `pos-opay-*` templates declare `pos_sales`, `pos_terminals` and `pos_cashups` this way; `debtor-reminder-table` and `bank-alert-email` still expect their table to exist.

## Parameters

Each template has named, typed and described parameters:

| Type | Accepts |
| --- | --- |
| `string`, `text` | One line (at most 200 characters unless the template says otherwise) or several (900); an optional pattern or list of values |
| `integer`, `number` | Numbers, also as typed by people ("1,000", "₦2,500.50"), within the template's minimum and maximum |
| `boolean` | yes or no |
| `time` | A time of day: "09:30", "9am", "5:30 pm" |
| `weekday` | A day: "Friday", "fri", "Fridays" |
| `connection` | The name of the tenant's connection for the template's connector (default `main`); or, with `capability` (`["pay_bank"]`) instead of `connector`, any connection whose rail can do those, where an empty value is the only one that can (the Start page and readiness offer the rails) |

**Pickers** (no-technical-steps N8). A parameter's `picker` says how the web app has a person choose it instead of typing an id, and `from` names the parameter it depends on:

| `picker` | Chosen as | `from` |
| --- | --- | --- |
| `sheet` | A pasted Google Sheets link, opened to show the sheet's name (`POST /v1/sheets/resolve`). The value may also be the link: its id is taken | |
| `sheet_tab` | A tab of that sheet, by name. A bare name is the whole tab; a name with spaces is quoted (`'Staff list'`) | the `sheet` parameter |
| `table`, `column` | One of the business's tables, and a column of it | `column`: the `table` parameter |
| `role` | One of the business's roles (`GET /v1/approver-roles`) | |
| `whatsapp_template`, `whatsapp_language` | An approved WhatsApp template by what it says; its language is filled in (`GET /v1/whatsapp/templates`) | the language: the template parameter |

A run form's field with `"format": "paystack-recipient"` asks for a bank and a 10-digit account number, shows the name on the account (`POST /v1/paystack/resolve`) and, once the person confirms it, saves the recipient (`POST /v1/paystack/recipients`); the field's value is Paystack's recipient code, never shown. Amounts are in naira; phone numbers are taken in any form. `TestEveryDetailIsOwnerShaped` keeps every template that does not need a developer free of ids, ranges, kobo and patterns to type.

Required parameters without a default must be given; optional ones take their default. A `string` whose default is empty may be left blank (`run-payroll`'s wallet to pay from, only for rails that hold several). A value that does not fit is refused by name (`400 invalid_params`, with `params: [{param, message}]`), as is a parameter the template does not have. **No value may start with `=`** or contain `{{`: a parameter is data, never an expression.

**Personal data is not a parameter.** Phone numbers and email addresses of the owner or the accountant are tenant variables the template reads (`env.owner_phone`, `env.owner_email`, `env.accountant_email`), listed with the template, so no definition carries them. Customers' numbers come from the run's data (the sheet, the payment event).

### How a template is written

A template is a JSON file in `templates/library/<id>.json` with `id`, `title`, `summary`, `description`, `category`, `tags`, `keywords` (for retrieval; optionally `requires`, above), `connectors`, `variables`, `params` (each with a `title`, `description` and `example`), `tables` (the data tables it works on, with their columns; made when the template is used) and `definition`, a wd/v1 document where `{{name}}` marks a parameter:

- a whole string `"{{name}}"` becomes the typed value (`"max_concurrency": "{{limit}}"` becomes a number);
- inside an expression (a string starting with `=`) the marker becomes a CEL literal, strings quoted and escaped, so a value cannot change the expression around it (`"=… < {{threshold:kobo}}"`);
- inside any other string, the value's text (`"cron": "{{send_time:minute}} {{send_time:hour}} * * {{day:cron}}"`).

Modifiers: `:hour` and `:minute` of a time, `:cron` of a weekday (0 for Sunday to 6), `:kobo` of a naira amount (× 100). Text parameters are best put in a `transform` step's output (`{"business_name": "{{business_name}}"}`) and read by expressions from there: values stay data, and flow code keeps them as written. The library refuses to load a template whose markers name no parameter or whose parameters are unused; ids are lowercase letters, digits, `.`, `_` and `-` (the pattern embed apps' `allowed_templates` accept). Never copy a request of the [evaluation suite](ai.md#evaluation) into a template's text.

## Callable templates

A template marked `callable` can be a step of another workflow ([decision 0047](decisions/0047-composable-templates.md)): a [`template` step](contracts/wd-v1.md#template-steps) names it, fills its parameters, and gives its run inputs from the calling workflow's data. Its steps run in the caller's run, with their ids prefixed by the step's, so one run keeps one history and one set of exactly-once rules, and every approval, payment and wait inside it is the caller's own. Pay a list, Run payroll, Send an invoice, and the Payrolla payroll summary and records are callable.

A callable template's interface is:

- **Run inputs**: its definition's `inputs` schema, which a callable template must have. Its expressions read them as `trigger.body`; any other trigger field it reads only under `has()`, since used as a step it reads the calling workflow's trigger (Run payroll names the month from a schedule's fire time when there is one, and otherwise from its `period` input or the run's start).
- **Outputs**: `outputs`, each with a `name`, a `type` (`string`, `integer`, `number`, `boolean`, `list` or `object`), a `title`, a `description` and a `value`, an expression over the template's own top-level steps read as `steps.<id>.output`. A step that may not run (it has a `when` or an `on_error`, or needs one that may not run) is read with an optional chain, `steps.?<id>.output…orValue(…)`, so the result never fails on a step that was skipped. The step's output in the calling workflow is these values by name.
- **Effect**: computed from its steps, never declared: `read`, `write` or `moves_money`, whether it waits for a person (an approval or a form) or for an event, and its run timeout.

A workflow keeps each template it uses as the library had it when the version was saved, so a library update never changes it; the workflow's page offers the update with its differences, and the next version saved takes it. What a template needs (its connections, capabilities, tables and variables) is the workflow's readiness, and what it does is the workflow's: a payment inside it is checked as the workflow's own, and a workflow around a product such as Payrolla cannot use a template that moves money.

The library refuses to load a callable template that breaks these rules. Its step ids are at most 31 characters, its capability steps' at most 8, none contains `__`, and none is `input` or `body` (the ids expansion gives its own steps), so two templates' prefixes always fit. Each callable template's [workflow tests](#the-library) (`templates/tests/<id>.test.json`) also run with the template used as a step, and its outputs are checked against their types.

## API

| Route | Permission | What it does |
| --- | --- | --- |
| `GET /v1/templates` | any member | Every template: summary, parameters, variables, its steps in plain words (filled with the examples), and `available` (every connector it uses is available to the tenant). `?q=` ranks by keyword match, `?category=` filters, `?callable=true` keeps the [callable](#callable-templates) ones. A callable template also has `callable`: its `inputs` (a JSON Schema), the `params` a template step fills (those only its trigger uses are left out), its `outputs` and its computed `effect` |
| `GET /v1/templates/{id}` | any member | One template, with its definition (markers included) |
| `POST /v1/templates/{id}/instantiate` | `workflow.edit` | `{"params": {...}, "name"?}`: a new workflow with a `draft` version 1, `created_by` the person, the template recorded on the version (`workflow_versions.template_id`), `workflow.create` audited with the template; answers the draft's `problems`, its `variables`, and `tables`: what became of each data table the template declares (above) (publishing checks) and the variables to set. Plan limits apply (`max_workflows`). Never publishes |
| `GET /v1/embed/{app}/templates` | end user | The app's `allowed_templates`, `available` only when the app also allows every connector they use |
| `POST /v1/embed/{app}/templates/{id}/instantiate` | end user, `workflow.edit` | As above, for one of the app's templates (403 otherwise, and 403 `not_allowed` when the filled definition uses a connector the app does not allow) |

Registering or updating an embed app refuses an `allowed_templates` id that is not in the library.

## Web

**Templates** in the navigation (every member): templates by category, a search box, and for each its description, steps in plain words, the variables to set, and a form for its parameters that creates the draft and opens it in the editor. "Set it up step by step" opens the guided first workflow instead: details, connection and variables, publish, and a test run on one page ([onboarding](onboarding.md#getting-started)). The AI panel says when a draft started from a template, and which details the goal did not give.

In the editor, **Use a ready workflow** adds a template step: choose a callable template, fill its settings (as on the Start page) and what it starts with (values, or what earlier steps gave), and the panel shows what it gives back, what it does ("Moves money · waits for a person to approve or answer") and its steps. The canvas shows it as one step; the steps after it can use what it gives back. When the library has changed a template the workflow uses, the editor says so with what changed; saving the workflow again takes it. A run's timeline shows each template step's own steps together under it, and the run canvas colours the template step by them.

In flow code, `template(id, params, input)` is a template step (`template("pay-a-list", { approver_role: "finance" }, { batch: ({ steps }) => steps.names.output.batch })`), kept unexpanded both ways; `flows/examples/month-end-pay-and-tell.flow.ts` runs payroll and then pays suppliers with two templates.
