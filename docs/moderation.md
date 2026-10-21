# Trust and safety for storefronts

Storefronts are public pages made by businesses Taskiem has often never met, so what goes on them is checked, people can report them, and Taskiem can take them down ([storefronts spec 7](spec/storefronts.md#7-trust-and-safety), decision 0036, 13). This is Phase 5, S8.

## Verification

| Level | How | Unlocks |
| --- | --- | --- |
| **Phone verified** | An owner or admin has linked WhatsApp or Telegram, or the owner agreed to a [field agent](agents.md) from their number | Publishing a page |
| **Business verified** | The owner asks (`POST /v1/storefront/verification`: legal name, address, a CAC number if any) and a person at Taskiem checks it | The **Verified business** badge; payment when customers order (`payment: at_order`); more than 50 orders and bookings a day; a first publish without waiting |
| **Agent onboarded** | A field agent recorded their visit: a photo of the place and where it is | Counts as business verified |

Each verification is a record (who, how, when) in `tenant_verifications` and in the audit chain. `GET /v1/storefront/trust` shows them, with what waits for Taskiem.

## The checks

Every way of publishing (the web app, chat, a field agent, a settings change that republishes) goes through the same checks, and so does every catalogue item as it is copied from its table:

1. **A word list** catches the obvious first: "double your money", "verify your account", weapons, drugs, counterfeit and the like, after undoing look-alike characters (`d0uble`, `p1stol`). Some words block; others (loans, "first copy", tramadol) only flag, since they are often innocent.
2. **Prices:** ₦0, or above ₦10,000,000, is flagged.
3. **A model** through Taskiem's AI layer (`TASKIEM_AI_MODERATION_MODEL` when set, else the building model) judges the words against a written policy (below), in the five languages. A piece of text is checked once per organisation: the verdict is kept by its digest. If no model is configured, or it fails, the words and prices still apply.

Each answers **allowed**, **flagged** (a person looks) or **blocked** (refused), with a category and a reason.

| | First publish | A later change | A catalogue item |
| --- | --- | --- | --- |
| Blocked | Refused, with what and why | Refused; the page stays as it was | Left off the page |
| Flagged | Waits for review | Goes live, and waits for a look | Shown, and waits for a look |
| Business not verified | Waits for review | — | — |

A page **waiting for review** (`in_review`) is not served. Photos are not judged by the model yet (the AI layer reads text only): a reviewer sees them, and approving a page approves its photos.

### What is not allowed

Weapons and ammunition; illegal drugs, and prescription drugs sold without a pharmacy; counterfeit and replica branded goods; investment, "doubling", money-flipping, forex-signal and crypto schemes; loans, savings or payments that need a licence the business does not hold; adult and sexual services; asking for passwords, PINs, OTPs, BVN or card and bank details; pretending to be a bank, fintech, telco, government agency or well-known brand.

## Reports

Every page's footer has **Report this page**: a reason (a scam, pretending to be someone else, selling something not allowed, offensive, something else), an optional message and an optional phone or email. It works without JavaScript, has a honeypot, and takes three a day from one address for one page. The contact is sealed by the public host; an operator reveals it only to ask the reporter more, and that is audited. Hosted forms' **Report this form** reaches the same list.

When **three different people** have open reports on a page in the last 30 days, it is **suspended pending review** within a few minutes, and the owner is told.

## Suspension

A suspended page answers "This page is unavailable" on every address, and its forms take nothing. The owner is told on WhatsApp or Telegram, and **appeals** by replying `APPEAL` and their message (or `POST /v1/storefront/appeal`). A suspended page cannot be published, closed or reopened until it is restored.

## Paused orders

Orders and bookings pause, and the forms say "not taking orders online right now", when:

- the month's orders or bookings reach the plan's `storefront_orders_monthly` or `storefront_bookings_monthly`;
- or a business not yet verified has taken 50 orders and bookings today (Lagos time).

The owner is told once; the pause lifts by itself when the month or day turns, the plan changes, or the business is verified. The scheduler checks every few minutes, so a busy page can go a little over.

## The review queue (operators)

The [operator console](operator-console.md) lists what waits, oldest first, with the page, its catalogue and photos, what the checks found, whether the business is verified, its agent and its open reports. Every decision needs the operator's passkey, is in the platform chain and the business's, and the owner is told.

| Kind | Approve | Reject (a note is required) |
| --- | --- | --- |
| `first_publish` | The page goes live | It returns to a draft with the note |
| `edit`, `catalogue` | Nothing changes | The page is suspended with the note |
| `reports` | The page is restored, the reports dismissed | It stays suspended, the reports marked acted on |
| `appeal` | A suspended page is restored | It stays as it is |
| `business` | The business is verified | Not verified, with the note |

A decision can also reject single photos (`reject_photos`). Operators suspend and restore pages by hand too (`POST /v1/ops/storefronts/{tenant}/{suspend,restore}`), and dismiss or act on reports. At pilot scale one person in operations staffs the queue part-time, aiming at a decision within 4 working hours.

## API

| Endpoint | |
| --- | --- |
| `GET /v1/storefront/trust` | Verification, reviews, and why the page is held, suspended or paused |
| `POST /v1/storefront/verification` | Ask Taskiem to verify the business |
| `POST /v1/storefront/appeal` | Appeal a suspension or a turned-down page |
| `GET /v1/ops/storefront-reviews`, `GET /v1/ops/storefront-reviews/{id}`, `GET /v1/ops/storefront-reviews/{id}/image?src=`, `POST /v1/ops/storefront-reviews/{id}/{approve,reject}` | The review queue (the image route shows the business's own stored photos to the console) |
| `GET /v1/ops/reports`, `POST /v1/ops/reports/{id}/{dismiss,action,contact}` | Reports |
| `POST /v1/ops/storefronts/{tenant}/{suspend,restore}` | Suspension by hand |

## Plans

The **Shop** and **Shop Plus** plans (prices are placeholders until decision B1) set the storefront's limits ([billing](billing.md)):

| | Shop | Shop Plus | Starter and above |
| --- | --- | --- | --- |
| Products and services shown (`storefront_items`) | 50 | 500 | 2,000 |
| Orders and bookings a month | 300 each (fair use) | 1,500 each | No limit |
| SMS to customers a month (`sms_monthly`) | 100 | 500 | 1,000 |
| Websites that may embed the pages (`embed_sites`) | 1 | 3 | 10 (the default) |
| Payment when customers order | No | Yes (with a verified business) | Yes |

Beyond `sms_monthly`, texts to customers are not sent (an email, if the customer gave one, still is) and the step's output says `sms_over_allowance`; verification codes always go. Beyond `storefront_items`, a publish is refused and the copied catalogue stops at the limit. Top-ups are not built yet.

## Not yet

- The model judging photos (the AI layer reads text only); photos are reviewed by people.
- Business verification through ID providers (Dojah, Prembly, Youverify) with automatic checks: a person checks for now.
- Proof-of-work and phone codes on orders for a page under attack.
- SMS top-ups.
