**Subject:** Payrolla × Taskiem: the connection is live, integration docs attached

Hi Taskiem team,

Following your change of plan: everything Taskiem needs to connect to Payrolla is now live in production, and the full integration pack is attached. Only one system pays a payroll, and that is Payrolla. Taskiem reads, reacts, sends links and, if a customer allows it, shows Payrolla's screens inside Taskiem.

**What's live, against your six points**

1. **API access, per company.** A company owner connects Taskiem in Payrolla → Settings → Connected apps and chooses what Taskiem may do. They then enter the key into Taskiem's connection screen themselves, so no key ever passes through email, chat or us.
   - **Scopes:** read payroll runs and per-employee payment status, read employees, send payslip links, manage webhooks, and embed.
   - **Base URL:** `https://synledger.name.ng/payrolla/api/v1`. The OpenAPI file is attached and also served at `https://synledger.name.ng/payrolla/api/v1/connect/openapi.yaml`.
2. **Webhooks.**
   - **Events:** 15 types across runs, per-employee payouts, employees and payslips.
   - **Signing and delivery:** each event is signed with HMAC-SHA256 over a timestamp and the raw body. Failed deliveries are retried over about two days, and you can replay the last 30 days.
   - **What's left out:** no bank account numbers, salary breakdowns or ID numbers are ever included.
   - **Subscribing:** subscriptions are REST hooks, created and deleted per company with that company's key, so your triggers map onto them directly.
3. **Approvals.** As agreed, there's no approve API.
   - `run.computed` carries an `approval_url` you can send on WhatsApp. It opens that run in Payrolla, where the approver confirms with their own factor. The link carries no credential.
   - If the customer allows embedding, the approver can also approve inside Taskiem (point 5).
4. **Payslips.** `POST /connect/payslips/{id}/link` returns a 7-day link.
   - **Opening it:** the employee confirms the last four digits of their phone before it opens.
   - **Our own links:** the employee's SMS and email links from Payrolla keep working alongside it.
   - **Off by default:** each company owner switches it on.
5. **Embedding.** Taskiem can show Payrolla inside Taskiem for any company that allows it:
   - **Screens:** the full payroll app, or single screens: one run's review, the team, one employee's payslips, or a new "pay people" screen for paying contractors and vendors from the company wallet.
   - **Flow:** your server requests a short-lived launch token for the signed-in user, and our 2 KB SDK mounts the frame on your origin.
   - **Payments in the frame:** approving and paying work there. By default each one is confirmed with a one-time code sent to the person's own phone, email or authenticator, set up in Payrolla. Taskiem can't see or change that factor. An owner can choose instead to trust Taskiem's sign-in for their company.
6. **Subscriptions.** Per company, matching your one sub-tenant per customer. A key only ever sees its own company.

**In the pack**
- `02-taskiem-integration-guide.md`: building the connector. It covers authentication, triggers, actions, sample payloads, five ready-made workflows, embedding, errors and limits.
- `03-connecting-taskiem-for-company-owners.md`: a one-page guide for our shared customers. Please reuse it in your help centre.
- `04-go-live-checklist.md`: what we each confirm before the first customer goes live.
- `api/`: the OpenAPI file, and the webhook and embedding reference guides. The webhook guide includes verification code in Node and Python, and a test vector.

**Testing**

We've set up a test company for you in production. It has test employees and a funding wallet with no money in it, so runs, approvals, events and embedding all work, and nothing can be paid out. Tell us who on your team needs access and we'll invite them. Each person then creates their own key in Payrolla.

**Going live**
- **Pilot:** we'd like to start with `Chuks Electronics` from `Friday 23 October`.
- **Payroll days:** our contact is duffisrael29@gmail.com.

**Two things we didn't build yet**
- **Fully hands-off provisioning.** Today the owner copies the key once into Taskiem's connection screen. You described Payrolla provisioning the connector server-to-server so customers never see a credential. We can add that next if the copy-once step gets in your way; tell us what your provisioning endpoint expects.
- **Embedding Taskiem's builder inside Payrolla** (your items E1–E5). Keep We'll come back to you on this separately; nothing in this pack depends on it.

**Questions for you**
- Which workflows are you launching first? We'll make sure the pilot customer has the matching scopes switched on.
- For WhatsApp messages to employees, will Taskiem record each employee's consent, or should Payrolla?
- Roughly how many API calls per company per day do you expect? The limit is 120 a minute per key, and we can raise it.

Happy to walk through it on a call.

Engineer 1
Payrolla
