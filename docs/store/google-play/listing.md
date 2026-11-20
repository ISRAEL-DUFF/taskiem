# Taskiem on Google Play: the store listing

Status: **draft for the founder** (19 November 2026). Paste each part into Play Console. Words follow decision [0068's amendment](../../decisions/0068-taskiem-for-everyone.md#amendment-2026-11-19-what-taskiem-is-for): Taskiem is described as a way to run a business, never as a replacement for a chat app.

| | |
| --- | --- |
| Package name | `app.taskiem.chat` (permanent once the first bundle is uploaded) |
| App or game | App |
| Category | **Business** |
| Free or paid | Free (businesses pay for plans on the web; nothing is sold in the app) |
| Contact email | (the support address) |
| Website | (the public site) |
| Privacy policy | `<site>/privacy` (web/src/pages/Legal.tsx) |

## Main store listing

**App name** (30 characters at most):

> Taskiem: Run your business

**Short description** (80 at most):

> Orders, money, stock and staff, run from the chat your customers already use.

**Full description** (4,000 at most):

> Taskiem runs the busy work of your business for you, right beside the conversations you already have with customers and suppliers.
>
> Customers order, book and pay in the chat. Taskiem turns their messages into orders, keeps count of your stock, matches the money that comes in, chases what you're owed and tells you each morning what needs you. Less copying from WhatsApp into notebooks, fewer orders lost, and you always know where your money is.
>
> **Sell and take orders**
> • Customers order and book from your shop page or in the chat, in the app or on WhatsApp
> • Every order in one place, from new to on the way, with a rider assigned
> • Products with prices, photos, sizes and colours
>
> **Know where your money is**
> • Money in and out today, this week and this month
> • Payments matched to invoices for you; what's left to match, flagged
> • Invoices sent from the chat, with reminders until they're paid
> • Leaks found: money that should have come in and didn't
>
> **Keep stock right**
> • Every sale counted; low items flagged before they run out
> • Orders to your suppliers, and their bills, in the same chat
> • Shelf counts with what went missing, and why
>
> **Work with your team**
> • Staff with their own roles; approvals for cash, discounts and payments
> • Tasks given in the chat, with a photo when they're done
> • Riders and deliveries, step by step
>
> **Schools**
> • Every student their own account number, so a parent's transfer lands on the right child
> • Term fees, receipts, reminders, payment plans and clearance at the gate
>
> **Automate the rest**
> • Workflows that run on a schedule or when something happens: reminders, reports, follow-ups
> • Ask Taskiem in plain words: "invoice Ada 25,000", "how did we do yesterday?"
>
> **For your customers**
> Your customers chat with you in the Taskiem app or their phone's browser, with nothing to install, and see their orders with every business in one place. Customers who prefer WhatsApp keep using it: they are one customer to you, whichever line they write on.
>
> Your data stays yours: it is kept encrypted, never sold, and never used for ads.
>
> Taskiem is made in Nigeria, for businesses that run on their phones.

**Ready to paste** into Play Console's full description: plain text, with `<b>…</b>` for the headings (Play shows a few HTML tags such as `<b>` and `<i>`; it does not read Markdown, so `**` would show as typed). 2171 of 4,000 characters, tags included.

```text
Taskiem runs the busy work of your business for you, right beside the conversations you already have with customers and suppliers.

Customers order, book and pay in the chat. Taskiem turns their messages into orders, keeps count of your stock, matches the money that comes in, chases what you're owed and tells you each morning what needs you. Less copying from WhatsApp into notebooks, fewer orders lost, and you always know where your money is.

<b>Sell and take orders</b>
• Customers order and book from your shop page or in the chat, in the app or on WhatsApp
• Every order in one place, from new to on the way, with a rider assigned
• Products with prices, photos, sizes and colours

<b>Know where your money is</b>
• Money in and out today, this week and this month
• Payments matched to invoices for you; what's left to match, flagged
• Invoices sent from the chat, with reminders until they're paid
• Leaks found: money that should have come in and didn't

<b>Keep stock right</b>
• Every sale counted; low items flagged before they run out
• Orders to your suppliers, and their bills, in the same chat
• Shelf counts with what went missing, and why

<b>Work with your team</b>
• Staff with their own roles; approvals for cash, discounts and payments
• Tasks given in the chat, with a photo when they're done
• Riders and deliveries, step by step

<b>Schools</b>
• Every student their own account number, so a parent's transfer lands on the right child
• Term fees, receipts, reminders, payment plans and clearance at the gate

<b>Automate the rest</b>
• Workflows that run on a schedule or when something happens: reminders, reports, follow-ups
• Ask Taskiem in plain words: "invoice Ada 25,000", "how did we do yesterday?"

<b>For your customers</b>
Your customers chat with you in the Taskiem app or their phone's browser, with nothing to install, and see their orders with every business in one place. Customers who prefer WhatsApp keep using it: they are one customer to you, whichever line they write on.

Your data stays yours: it is kept encrypted, never sold, and never used for ads.

Taskiem is made in Nigeria, for businesses that run on their phones.
```

**Graphics** (all in this folder, made by `make.mjs`):

| Asset | File | Play's rule |
| --- | --- | --- |
| App icon | `icon-512.png` | 512 × 512, 32-bit PNG, up to 1 MB; Play rounds the corners |
| Feature graphic | `feature-1024x500.png` | 1024 × 500, JPEG or 24-bit PNG, no transparency |
| Phone screenshots | `phone-1.png` … `phone-8.png`, in that order | 2 to 8, 1080 × 1920 (9:16) |

Tablet screenshots are optional and left out: the app is made for phones.

## App content

| Section | Answer |
| --- | --- |
| **Privacy policy** | `<site>/privacy`; the account deletion page is `<site>/delete-account` (Data safety asks for it) |
| **App access** | Restricted: sign-in needed. Give reviewers a test account that does not need a WhatsApp or SMS code: an email and password, with a demo business that has orders, money and stock (see "For the reviewers" below) |
| **Ads** | No ads |
| **Content rating** | Questionnaire: category "Utility, Productivity, Communication or Other"; users can communicate with each other (yes: chats); shares location (yes: a person can share a place in a chat); no violence, sexual content, gambling or drugs. Expected rating: Everyone / PEGI 3, with "Users Interact" |
| **Target audience** | 18 and over only (the terms require it; Taskiem for everyone §15) |
| **News app** | No |
| **COVID-19 contact tracing** | No |
| **Data safety** | Below |
| **Government app** | No |
| **Financial features** | The app helps businesses take payments, send invoices and record money; it does not lend, hold deposits or trade. Declare "Payments and transfers" (payments are processed by the business's own payment provider) and "Invoicing" if offered; not a bank, wallet, loan or crypto app |
| **Health** | No |
| **User-generated content** | Yes: messages between people and businesses. Taskiem has reporting, blocking and moderation (People chatting, §8), which Play's policy asks for |

## Data safety

Draft from the code and [PC5](../../needs-people.md) ("App-store privacy labels updated"); **to be checked by the founder and counsel** before production, with the privacy notice.

**Overview:**
- Data is encrypted in transit: **yes** (HTTPS only).
- People can ask for their data to be deleted: **yes**, in the app (You › Settings › Delete your account) and on the web page `/delete-account`.
- Shared with third parties: **no**. Firebase (Google) delivers notifications and the business's payment provider processes payments, as service providers acting for Taskiem, which Play does not count as sharing.
- Ads, analytics or crash reporting libraries: **none**.

**Data collected** (all "collected", none "shared"; all processed on Taskiem's servers):

| Play's data type | Collected | Required? | Why |
| --- | --- | --- | --- |
| Personal info: Name | Yes | Required | Account, shown to the businesses and people one chats with |
| Personal info: Email address | Yes | Optional | Signing in with email; receipts |
| Personal info: Phone number | Yes | Required | Signing in, finding a person's orders with businesses |
| Personal info: Address | Yes | Optional | Saved delivery addresses, copied onto an order |
| Financial info: Purchase history | Yes | Optional | Orders and payments with businesses; a business's sales |
| Financial info: Other financial info | Yes | Optional | A business's invoices, bills and money records |
| Location: Approximate and precise | Yes | Optional | Only when a person shares a place in a chat; not tracked |
| Messages: Other in-app messages | Yes | Required | Chats with businesses and people (kept encrypted at rest, readable by Taskiem's servers, not end-to-end encrypted) |
| Photos and videos | Yes | Optional | Sent in chats; product photos |
| Audio: Voice or sound recordings | Yes | Optional | Voice notes sent in chats |
| Files and docs | Yes | Optional | Files sent in chats; invoices and receipts |
| Contacts | Yes | Optional | Only after the person agrees: numbers are sent as keyed hashes to find friends on Taskiem, and not stored after the answer |
| App activity: Other user-generated content | Yes | Required | Orders, tasks, workflows a business makes |
| Device or other IDs | Yes | Required | The device's notification token and the device's own id, for notifications and keeping the account safe |

For each: purpose "App functionality" (and "Account management" for name, email and phone; "Fraud prevention, security and compliance" for device IDs); none for advertising or analytics.

## Permissions the app asks for

Play asks why some are needed; these are the answers to give where it asks.

| Permission | When the app asks | What for |
| --- | --- | --- |
| Contacts (`READ_CONTACTS`) | Only when the person taps **Find friends from your contacts**, after a screen saying what happens | To find which of their contacts are on Taskiem, sent as keyed hashes; nothing stored. Also to pick a customer from the phone's contacts |
| Location (`ACCESS_FINE_LOCATION`, `ACCESS_COARSE_LOCATION`) | Only when the person taps **Share a place** in a chat | To send where they are, once. Never in the background |
| Camera | When the person takes a photo or scans a QR code | Photos in chats, product photos, a delivery's proof, scanning a person's code |
| Microphone (`RECORD_AUDIO`) | When the person holds to record a voice note | Voice notes |
| Notifications (`POST_NOTIFICATIONS`) | Once after signing in | Messages, orders and payments as they happen |
| Fingerprint or face (`USE_BIOMETRIC`) | When the person turns on the app lock | Opening the app with the phone's own lock |

## For the reviewers (App access)

Google's reviewers cannot receive a WhatsApp or SMS code. Before production:

1. Make a reviewers' account on the production server with an **email and password**, and a demo business (Ada's Kitchen, as the tests' data) with products, orders, invoices and stock, so every screen in the screenshots can be seen.
2. In Play Console › App content › App access: "All or some functionality is restricted", with the email, the password and these words: "Choose Sign in with email. The account belongs to a demo business with sample orders, money and stock."
3. Keep the account working: Play re-reviews every update.

## Releases

| Track | Build | Server |
| --- | --- | --- |
| Internal testing | 1.0.0 (1), `taskiem-1.0.0-1-staging.aab` | Staging (`staging.ispend.name.ng`) |
| Production | 1.0.x, a higher version code | Production (when it is up) |

Each upload needs a version code higher than any before (`--build-number`; mobile/README.md, "Releasing to Google Play").
