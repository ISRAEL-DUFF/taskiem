# Schools: what is not covered yet

Everything built for [decision 0046](../decisions/0046-balans-in-taskiem.md) so far (B0 to B9 of the [schools plan](schools.md)) that is not done, not verified, or done differently from the plan, with what would close it; and, at the end, the [pilot checklist](#pilot-checklist): the ordered steps for the first school. Update this list as items close; the plan's "Built" notes say how each milestone differs from it, and the matching, invoices and money log items every business shares are in the [invoices open items](invoices-open-items.md).

Status as of 25 October 2026: B0 to B9 are all built. Nothing has run against a real rail yet: every rail is tested against fakes and fixtures from its documentation. What is left is below.

## Needs people, not code

| Item | Why it matters | What closes it | Who |
| --- | --- | --- | --- |
| **Rail questions** ([rail facts](schools-rail-facts.md#questions-for-the-founder)): iswallet's (its credit notice's fields and naming answered 27 October; still: whether a listed deposit can be pending, its own fee, limits per wallet, the name's length limit); Paystack's customer validation, one account per email (may a student get a derived address), its fee's unit and settlement, what it answers for a customer who has an account; Flutterwave's BVN or NIN per sibling, which `charge.completed` field is what the payer sent, whether `narration` names the account, whether a static account takes any amount any number of times, v3 or v4; Moniepoint's name prefix and limit | The mappings follow the documentation where it is clear and nothing where it is not: naming holds only where a rail lets the business set it; gross or net counting rests on each rail's fields | Ask each rail; for the sandbox-only questions, try them (next row); then change the mappings and their conformance cases | Founders / partnerships, then engineering |
| **Sandbox runs on each rail** (iswallet, Paystack, Flutterwave, Moniepoint) | B4's and B5's "done when": a student is issued an account and a transfer into it lands on the child. Issuing, the credit event, its gross or net amount, the look-back and a reversal are tested against fakes only | Sandbox `main` connections on staging; issue one student and a batch; pay into an account by transfer; a credit found by the look-back only; a credit reversed; a second credit into the same account on the same day | Founders (credentials via the Connections page), then engineering |
| **A pilot school** | The plan's §16: a school can start after B5, with B6 to B8 before its first resumption day | A school with a rail connection of its own (the rail's KYC: the director's BVN, the registration), its students as a CSV and its fee structures; then the [pilot checklist](#pilot-checklist) | Founders |
| **Meta's approval of the WhatsApp templates** `taskiem_fee_reminder` (one template for the three rungs, since Meta approves fixed wording), `taskiem_plan_decision`, `taskiem_family_link`, `taskiem_receipt`, `taskiem_account_number` and `taskiem_daily_close` | The templates are defined and submitted from Taskiem ([the registry](../whatsapp.md#the-template-registry)); reminders, receipts, account numbers, plan decisions and family links go as their templates the moment Meta approves them, and families get SMS until then; the close goes as its template the same way, and as `taskiem_alert` cut to one line until then | Set `TASKIEM_WHATSAPP_WABA_ID`, run `taskiem whatsapp templates submit all` (or submit in WhatsApp Manager under the same names), and wait for Meta | Founders (Meta Business account) |
| **Reviewing the translations**: the schools line of the daily chat summary and the monthly email in Pidgin, Yoruba, Hausa and Igbo (B9) are unreviewed drafts; the reminder ladder's default wording, the family page, the gate screen and the receipt are English only | A family in Kano gets the reminder in English unless the school writes its own wording | A speaker reviews the summary lines ([words](../languages.md#reviewing-a-language)); decide whether the family page and the reminders' defaults need the five languages (a school setting, later) | Founders / reviewers |
| **Paystack and Flutterwave for a school**: only a registered Nigerian business that has gone live can issue dedicated accounts on Paystack (at most 1,000 unless raised); Flutterwave needs a parent's BVN or NIN per account | A school on either rail cannot issue a batch for every student on day one | The school's own onboarding with the rail; iswallet or Moniepoint for a school that needs every student issued at once | Each school; founders for the advice |

## Rail facts and account mappings (B0)

- **iswallet's credit webhook is mapped** ([iswallet account numbers](iswallet-account-numbers.md) I1): credits reach the log in seconds, and the look-back (48 hours, every 15 minutes) stays as the safety net, counting a credit heard both ways once.
- **Naming**: Paystack names accounts itself ("Business name/Customer name") and Flutterwave documents no name, so §6.3's "the school's name shortens and the child's never does" holds only on iswallet (whose `account_name` is what payers see, 27 October) and Moniepoint. Names are capped at 31 characters on every rail, since no rail documents its limit.
- **Fees**: no rail documents a fee per account issued; each charges per payment (iswallet's partner 1.4% at most ₦2,000; Paystack 1% at most ₦300; Flutterwave 2% plus VAT). Whether iswallet adds its own `fee` on deposits is unknown. The plan's "shown before a batch is issued" shows these words, not a figure.
- **Gross or net**: counted from each rail's fields as read from the documentation (`amount_kobo` what the payer sent, `settled_kobo` and `fee_kobo` where given); Flutterwave's `amount`, `charged_amount` and `app_fee` are a reading to confirm in sandbox.

## Matching for customers' accounts (B1)

Built for every business; the general items are in the [invoices open items](invoices-open-items.md#matching-t5). For a school:

- **A credit that is not a student's** (the reference or account disagree) is held with the reason and never refunded from Taskiem: the school refunds it outside and matches nothing, or marks it **Not this school's**. The plan's §7.5 "Not this school's (refund outside Taskiem)" is the hold plus a reject; there is no refund button.
- **Siblings share credit** only when the school turns it on (Schools > Settings, off by default); off, what a student's account takes beyond their own fees waits for their next invoice.
- **Payer memory's weights are a first guess**, to tune with a real school's payments; payers are remembered from a person's confirm or match only, never from the automatic path.
- **A student's account number is stored in the clear** as the business's collection account (it is meant to be given to parents), the account's name sealed.

## The Schools area and its record (B2)

- **A form parents fill in themselves**, reviewed by the bursar before it becomes a student, is a later template on forms; today students come from a file, one by one, or over the API.
- **A guardian needs a phone**: it is what makes a guardian belong to one family. A student with two guardians is two lines in an import; a guardian in two families (remarried parents) is refused by design (decided 2026-10-23).
- **The kept copies** (`school_students`, `school_classes`, `school_term_fees`) are updated in place (a student changes class), while the kept table's page still says corrections are new rows, as the money log's are. Needs a sentence on the page, or the copies' own page.
- **Only owners can give the gate role**, since only owners hold `school.gate` besides it.

## Fees and term invoices (B3)

- **Only the current term can be invoiced**, since enrollments are made when a term starts: next term's invoices cannot be prepared early. A school that wants them before the term starts needs an "enrol for the next term" step.
- **A sent invoice's lines only grow**: each amendment is a new line; the total never goes below what was paid; discounts never come off pass-through lines. A fee item removed from the structure after sending stays on sent invoices until amended one by one.
- **Editing a school draft in the general invoice editor** is not supported (drafts change through the fees, the student's options and discounts).
- **A student held for a discount** at or above the threshold has no invoice until a second person approves; the plan's count says so, and a school whose threshold is low sees many held.

## Students' account numbers (B4)

- **Lost answers on Paystack and Flutterwave** (a call cut short on a rail where asking again could open a second account) stop at **Check with the rail** for a person; nothing reads the rail's own list of accounts back to settle them.
- **Flutterwave students are issued one at a time** on the student's page, with a parent's BVN or NIN typed in, never in a batch.
- **Two partial refunds of one payment are one entry** (the money log keys a refund by the payment it refunds), so only the first is taken back; the second is a correction by hand.
- **The look-back's "the webhook missed money" notice** goes to the owners once a day; no page lists which credits the look-back found first (the daily close counts them).

## Cash, approvals and receipts (B5)

- **The cash threshold is unset by default** (no second person) and **four-eyes is strict**: a school with one owner cannot approve its own cash posting or discount at or above a threshold until a second person with `school.approve` exists (an owner, or a custom role given the permission). The pilot checklist says to add one first.
- **Requests older than 7 days** are no longer pushed to approvers' phones, though still decidable in the web app.
- **`/r/<token>` shares its path with form links**: a receipt token is tried first; a guess gets the form link's not-found.
- **The receipt's font** is the one exception to the rule against binaries in the repository (accepted 2026-10-24).

## The family page (B6)

- **Receipts issued after a link was minted show without their check link** until the next message mints a new one (what the page shows is sealed under the link itself).
- **A claim is matched to a counted payment of the same amount, else the oldest open claim**; there is no hand matching of a claim to an entry, and a claim for a different amount than what landed stays open until the bursar dismisses it.
- **Rate limits per family** are fixed in the database (claims 5 an hour, plans 3 an hour and one undecided per student, new links 3 an hour), not a setting.
- **Unknown and expired links read the same**; only a real expired link offers to send one, to the guardian's number on file.
- **The page is English only**, in the three themes.

## Chasing (B7)

- **One message per guardian names every selected child** with their balances summed; a family is sent a rung at most once a day, whatever is selected.
- **Promises and plans are checked hourly**, so "the day after" is up to an hour into it; a promise made on its due date is decided the day after.
- **A plan's grace is 0 days by default**; a plan with an instalment one day late is in arrears until the owner sets a grace in Settings.
- **The reminders' reasons for skipping, and matching's reasons, are English in every language.**
- **No reminder goes by WhatsApp outside the 24-hour window** until Meta approves `taskiem_fee_reminder` in the number's account: families who tapped in get SMS instead, and the school's own rung wording applies inside the window and by SMS only (the template's wording is fixed).

## Clearance and the gate (B8)

- **An override's end defaults to the term's end** rather than being required; a long override is one choice away.
- **The gate's recent list lives in the phone's session** and is cleared on sign-out; nothing is kept of who was checked, by design.
- Nothing else of substance differs from §12.

## The proprietor, templates and AI (B9)

- **There is no student-added trigger**, so `school-issue-accounts` runs every 15 minutes over students without an account (and by hand for a whole school), up to 15 minutes after a student is added.
- **`school-receipt` sends the receipt's check link and the family page link**, not the PDF (messages carry no attachments; the family page has the PDF).
- **The close is taken by the hourly invoice tick**, on the first tick after midnight Lagos, rather than a separate nightly job; the matches it counts are those made that day, not the day the money landed, so a day's credits matched after midnight count on the next day's close; the "does not reconcile" checks look over everything, not only the day.
- **The Overview's debtors and aging are every open term's**, as on the Debtors page; collected, expected and outstanding are the chosen term's.
- **Close texts and the summaries name no student**: admission, invoice and receipt numbers only.
- **The monthly email is English**; the chat summary's schools line is in five languages, four unreviewed (above).
- **The AI builder's fourteen schools cases are measured offline** (the heuristic); the real-model eval needs the AI account, as the invoice cases do.

## The demo school (test fixture only)

The founder decided (25 October 2026) that a first school is tested from scratch on staging, not with seeded data, so the demo school is a **test fixture only**: `engine/schools/demo` seeds one through the engine's own services, and the `taskiem demo school` command that drives it exists only in a binary built with `-tags e2e` (`web/e2e/serve.sh` and the Go tests). The image, the release build and `make build` carry no such command, so no deployed Taskiem can seed one. It is kept because `web/e2e/pilot.spec.ts` walks the whole school path over it on every run, and its own test checks the Overview, clearance and close over a full term's data.

## Pilot checklist

The ordered steps for a first school, each with the page or command. Walk them on staging from scratch, with the school's own roster and a sandbox rail; `web/e2e/pilot.spec.ts` walks the same path on a phone over the test fixture.

| Step | What | Where |
| --- | --- | --- |
| 1 | **Turn the area on**, with the Bursar role; give the bursar their role, and make sure a second person with `school.approve` exists (a second owner) for cash and discounts | Settings > Schools (an owner); Members & keys |
| 2 | **Connect the rail** that will issue the students' account numbers, in prod, with the school's own keys; the rail's own checks (the director's BVN, the registration) are done with the rail. Put the rail's webhook URL in its dashboard | Connections; [rail facts](schools-rail-facts.md) for what each needs |
| 3 | **Classes, the session and its terms**, each term with its dates and the day fees are due | Schools > Classes and terms |
| 4 | **Import the students** as a CSV (name, admission number, class, guardian name, phone; email and relationship optional): check the file, confirm the families, save; re-importing updates by admission number | Schools > Import students (`POST /v1/schools/students/import`) |
| 5 | **Start the term**: every active student is enrolled once | Schools > Classes and terms > Start |
| 6 | **Fees per class**: tuition and levies as required items, transport and lessons as optional, exam fees as pass-through lines naming the body; choose each student's options; standing discounts | Fees for the term; a student's page |
| 7 | **Thresholds and links**: the cash threshold (0 for every posting), the discount threshold, how long a family page link works, the reminder wording, the plan grace | Settings > Schools |
| 8 | **Issue account numbers**: choose the issuing connection (and its wallet), check who gets one, issue the batch, fix what the rail refused and check again | Settings > Schools > Students' account numbers; Schools > Account numbers; or the `school-issue-accounts` template |
| 9 | **Invoice the term**: check the plan (who is left out and why), then invoice and send, or keep drafts and send from the page | The term's Invoices; or the `school-invoice-term` template (`schools@1` `invoice_term`) |
| 10 | **Parents' links**: the invoice message carries each family's page link; send one by hand to any family | Schools > Families > Send a link |
| 11 | **The gate user**: one person with the gate role, signed in on the shared phone at the gate | Members & keys (an owner gives the role); the gate screen opens at any address |
| 12 | **Templates to turn on**: the receipt, the daily close (to the owners), the fee reminders (two days a week), the plan decisions, the gate list (the day before resumption), the account issuing | Start > Your school, or Templates |
| 13 | **Every day**: post cash and transfers, decide what waits, match what was held and look at the claims, chase | Schools > Payments; Approvals; Invoices > To match; Schools > Debtors |
| 14 | **The close**: read each morning's close (the template sends it), fix what it flags on the page it names | Schools > Overview > The daily close |
| 15 | **Before resumption**: clearance by class, overrides with reasons, the teachers' lists | Schools > Clearance; the `school-gate-list` template |
| 16 | **End of session**: promote the classes, with the exceptions chosen first | Schools > Classes and terms > Promotion |
