# Templates

Ready workflows for small businesses (spec 11.1, SME tier): pick one, fill in a few details, and it becomes a draft workflow to review and publish. The same library is the AI builder's first stop ([AI](ai.md#what-it-does)) and what [`build` on WhatsApp](whatsapp.md#build) starts from, and it is what embed apps' `allowed_templates` name ([embedding](embedding.md)).

## The library

| Id | What it does | Connectors |
| --- | --- | --- |
| `debtor-reminder-sms` | Every week, text each customer on the debtors sheet how much they still owe | Google Sheets, Termii |
| `debtor-reminder-whatsapp` | The same by an approved WhatsApp template | Google Sheets, WhatsApp |
| `invoice-follow-up` | After an invoice, wait a few days, check with Paystack, remind the customer if unpaid | Paystack, Termii |
| `transfer-received-notify` | Text the owner about each transfer into the Moniepoint (Monnify) account and log it in a sheet | Moniepoint, Termii, Google Sheets |
| `daily-sales-summary` | Every evening, add up today's sales from the sales sheet and text the owner | Google Sheets, Termii |
| `sales-ledger-sheet` | Record every Paystack payment in a ledger sheet | Paystack, Google Sheets |
| `weekly-balance-email` | Every week, email the owner the Paystack and Moniepoint balances | Paystack, Moniepoint, Gmail |
| `payment-thank-you-sms` | Thank customers by SMS when Paystack confirms their payment | Paystack, Termii |
| `payment-receipt-whatsapp` | Send customers an approved WhatsApp receipt when they pay | Paystack, WhatsApp |
| `new-customer-welcome` | Welcome new sign-ups by SMS and add them to the customer sheet | Termii, Google Sheets |
| `new-order-alert-sms` | Text the owner each new order from the online shop | Termii |
| `low-balance-alert` | Text the owner when the Paystack balance drops below a level | Paystack, Termii |
| `failed-transfer-alert` | Text the owner and email the accountant when a transfer fails or is reversed | Paystack, Termii, Gmail |
| `stock-reorder-alert` | Every morning, text the owner the items at or below their reorder level | Google Sheets, Termii |
| `payroll-reminder` | Before payday, remind the owner by SMS and the accountant by email | Termii, Gmail |
| `salary-payout-approval` | On payday, pay each person on the staff sheet by Paystack transfer after the owner approves, and text them | Google Sheets, Paystack, Termii |
| `supplier-payment-approval` | Pay a supplier invoice by Paystack transfer after approval and text the owner the outcome | Paystack, Termii |
| `kyc-bvn-check` | Check an applicant's BVN with Dojah; welcome them if the name matches, flag it otherwise | Dojah, Termii, Gmail |
| `pgdock-new-row-whatsapp` | When a row is inserted into a PGDock table, send the number in it an approved WhatsApp template (Taskiem creates and removes the PGDock webhook itself) | PGDock, WhatsApp |

Every template that moves money does so after an approval step and with an idempotency key, so a retry never pays twice. Every one is a valid wd/v1 definition: `templates_test.go` fills each with its example values and checks that it passes the publishing checks, uses exactly the connectors it declares, has no policy finding, runs its generated dry-run happy path, reads aloud as plain steps without an expression, and round-trips through flow code (spec 10.2) unchanged.

## Parameters

Each template has named, typed and described parameters:

| Type | Accepts |
| --- | --- |
| `string`, `text` | One line (at most 200 characters unless the template says otherwise) or several (900); an optional pattern or list of values |
| `integer`, `number` | Numbers, also as typed by people ("1,000", "₦2,500.50"), within the template's minimum and maximum |
| `boolean` | yes or no |
| `time` | A time of day: "09:30", "9am", "5:30 pm" |
| `weekday` | A day: "Friday", "fri", "Fridays" |
| `connection` | The name of the tenant's connection for the template's connector (default `main`) |

Required parameters without a default must be given; optional ones take their default. A value that does not fit is refused by name (`400 invalid_params`, with `params: [{param, message}]`), as is a parameter the template does not have. **No value may start with `=`** or contain `{{`: a parameter is data, never an expression.

**Personal data is not a parameter.** Phone numbers and email addresses of the owner or the accountant are tenant variables the template reads (`env.owner_phone`, `env.owner_email`, `env.accountant_email`), listed with the template, so no definition carries them. Customers' numbers come from the run's data (the sheet, the payment event).

### How a template is written

A template is a JSON file in `templates/library/<id>.json` with `id`, `title`, `summary`, `description`, `category`, `tags`, `keywords` (for retrieval), `connectors`, `variables`, `params` (each with a `title`, `description` and `example`) and `definition`, a wd/v1 document where `{{name}}` marks a parameter:

- a whole string `"{{name}}"` becomes the typed value (`"max_concurrency": "{{limit}}"` becomes a number);
- inside an expression (a string starting with `=`) the marker becomes a CEL literal, strings quoted and escaped, so a value cannot change the expression around it (`"=… < {{threshold:kobo}}"`);
- inside any other string, the value's text (`"cron": "{{send_time:minute}} {{send_time:hour}} * * {{day:cron}}"`).

Modifiers: `:hour` and `:minute` of a time, `:cron` of a weekday (0 for Sunday to 6), `:kobo` of a naira amount (× 100). Text parameters are best put in a `transform` step's output (`{"business_name": "{{business_name}}"}`) and read by expressions from there: values stay data, and flow code keeps them as written. The library refuses to load a template whose markers name no parameter or whose parameters are unused; ids are lowercase letters, digits, `.`, `_` and `-` (the pattern embed apps' `allowed_templates` accept). Never copy a request of the [evaluation suite](ai.md#evaluation) into a template's text.

## API

| Route | Permission | What it does |
| --- | --- | --- |
| `GET /v1/templates` | any member | Every template: summary, parameters, variables, its steps in plain words (filled with the examples), and `available` (every connector it uses is available to the tenant). `?q=` ranks by keyword match, `?category=` filters |
| `GET /v1/templates/{id}` | any member | One template, with its definition (markers included) |
| `POST /v1/templates/{id}/instantiate` | `workflow.edit` | `{"params": {...}, "name"?}`: a new workflow with a `draft` version 1, `created_by` the person, the template recorded on the version (`workflow_versions.template_id`), `workflow.create` audited with the template; answers the draft's `problems` (publishing checks) and the variables to set. Plan limits apply (`max_workflows`). Never publishes |
| `GET /v1/embed/{app}/templates` | end user | The app's `allowed_templates`, `available` only when the app also allows every connector they use |
| `POST /v1/embed/{app}/templates/{id}/instantiate` | end user, `workflow.edit` | As above, for one of the app's templates (403 otherwise, and 403 `not_allowed` when the filled definition uses a connector the app does not allow) |

Registering or updating an embed app refuses an `allowed_templates` id that is not in the library.

## Web

**Templates** in the navigation (every member): templates by category, a search box, and for each its description, steps in plain words, the variables to set, and a form for its parameters that creates the draft and opens it in the editor. "Set it up step by step" opens the guided first workflow instead: details, connection and variables, publish, and a test run on one page ([onboarding](onboarding.md#getting-started)). The AI panel says when a draft started from a template, and which details the goal did not give.
