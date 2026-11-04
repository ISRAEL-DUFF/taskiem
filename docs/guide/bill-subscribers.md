# Bill subscribers every month

For any business whose customers pay every month, or on a regular date, for something they keep: a gym or club (members), a school's lessons or a tutor (classes), a security or cleaning service, a water supplier, an internet or hosting company. Keep your subscribers in a table, and let Taskiem invoice them, remind them before they renew, mark the ones who do not renew, and renew them when they pay. Pick **Bill customers for a service** when you sign up, or turn on **Money and invoices** under What your business uses.

Some businesses also have a system that switches the service off and on (an internet provider's billing panel or router, say). Taskiem can call it too; the workflows below say which ones do.

## Your subscribers table

Make a table called **subscribers** (Tables, or let the first workflow below make it for you). Its columns:

| Column | What it holds |
| --- | --- |
| Name, Phone | Who they are. The phone is how Taskiem reaches them and finds them when they pay. |
| Plan, Price each month | What they have, and what it costs in naira. |
| Renews on | The date their service runs to (a date, such as 2026-11-30). |
| Status | `active` or `suspended` (capitals do not matter): `suspended` is a plan that has ended. |
| Account or username on your system | How your own billing or network system knows them. Only needed if you switch a service off and on in another system; leave it empty otherwise. |
| Area | Where they are, so you can tell one area about a fault. |

## The workflows

Pick each one under **Templates**. They work together on the same table.

- **Invoice every subscriber each month.** On the day you choose, each active subscriber gets an invoice for their price, by SMS with a page to pay from, due in the days you choose (so your overdue reminders can chase it). Once for each subscriber each month, even if it runs twice, and two subscribers who share a phone number each get their own.
- **Remind subscribers before they renew.** Every morning, an SMS to each active subscriber whose renewal is within the days you choose, with the date and how to pay.
- **Mark subscribers who have not renewed as expired.** Every day, the active subscribers more than your grace days past their renewal date are listed in **one request to you**. Nothing happens until you approve. On approval each one is marked suspended in your table (so invoices and reminders stop for them) and texted that their plan has ended and how to renew. Nothing is called outside Taskiem.
- **Renew a subscriber when they pay.** The moment an invoice is paid in full, and the payment is at least the subscriber's price for a month, the subscriber with that phone (matched on its last ten digits, so +234 and 0 numbers match) gets their renewal date moved on (from today, or from their date if that is later), is marked active and is thanked.
- **Tell your subscribers something.** Another tool of yours (a monitoring tool, your booking system, a form) calls a web address with a message, and optionally an area, sending the key Taskiem shows with the workflow. Every active subscriber (or those in that area) is texted. Use it for a fault and its fix, a closed day or a new price.

**If you switch the service off and on in another system**, use these two instead of the two above them:

- **Switch off subscribers who have not renewed, on your system.** The same request to you, and on approval each one is also switched off on your system (a call to an address it gives you), marked suspended and told why. If your system fails for one, you are told, that one stays active, and the others carry on.
- **Switch a subscriber back on, on your system, when they pay.** As renewing, and a subscriber who was switched off is switched back on in your system first. A subscriber with nothing in **Account or username on your system** is renewed without calling it.

## Connecting your own system

Switching a customer off or on in another system means Taskiem calls an address your system gives for that, with your key. Before the two "on your system" workflows will run:

1. Put the key your system gave you in **Settings** as a secret named `billing_system_key`.
2. Allow your system's address in **Settings** (turn **Advanced** on to see the list of allowed addresses).
3. Fill in the address your system gives for switching one customer off, and the one for switching one back on, when you pick the workflow.

Taskiem sends `{"account": "<the account on your system>", "action": "suspend"}` (or `"reconnect"`), signed with your key as `Authorization: Bearer <key>`, and an `Idempotency-Key` so a repeat is never applied twice. Your system must be **reachable on the internet** at an `https` address: Taskiem does not yet reach a system that only has a private address inside your own network.

Without these two workflows, the others work with nothing connected but SMS and invoices.

## Things to know

- Keep **Renews on** right when you add a subscriber. The reminder and switch-off workflows read it, and the workflow that renews on payment moves it on.
- Taskiem finds the subscriber who paid by **phone number**: the number on the invoice must be the one in the table.
- Every ending of a plan is approved by a person first. Taskiem never marks or switches anyone off by itself.
- A run covers up to **1,000 subscribers**. With more, Taskiem tells you the rest were left out; it does not skip them silently.
- A payment smaller than the subscriber's monthly price (an equipment sale, the end of an old part-payment) renews no one.
