# Bill subscribers every month

For a business whose customers pay every month for a service (an internet provider, a cloud or hosting company, a water or security service, a gym): keep your subscribers in a table, and let Taskiem invoice them, remind them, switch them off when they do not renew, and switch them back on when they pay. Pick **Bill customers for a service** when you sign up, or turn on **Money and invoices** under What your business uses.

## Your subscribers table

Make a table called **subscribers** (Tables, or let the first workflow below make it for you). Its columns:

| Column | What it holds |
| --- | --- |
| Name, Phone | Who they are. The phone is how Taskiem reaches them and finds them when they pay. |
| Plan, Price each month | What they have, and what it costs in naira. |
| Renews on | The date their service runs to. |
| Status | `active` or `suspended`. |
| Account or username on your system | How your own billing or network system knows them (only for switching off and on). |
| Area | Where they are, so you can tell one area about a fault. |

## The workflows

Pick each one under **Templates**. They work together on the same table.

- **Invoice every subscriber each month.** On the day you choose, each active subscriber gets an invoice for their price, by SMS with a page to pay from. Once for each subscriber each month, even if it runs twice.
- **Remind subscribers before they renew.** Every morning, an SMS to each active subscriber whose renewal is within the days you choose, with the date and how to pay.
- **Switch off subscribers who have not renewed.** Every day, the active subscribers more than your grace days past their renewal date are listed in **one request to you**. Nothing happens until you approve. On approval each one is switched off on your system, marked suspended and told why.
- **Switch a subscriber back on when they pay.** The moment an invoice is paid in full, the subscriber with that phone gets their renewal date moved on (from today, or from their date if that is later), is marked active, is switched back on if they were off, and is told.
- **Tell your subscribers about an outage.** Your network tool calls a web address with a message, and optionally an area. Every active subscriber (or those in that area) is texted. Call it again to say it is fixed.

## Connecting your own system

Switching a customer off or on means Taskiem calls an address your system gives for that, with your key. Before the two workflows that do this will run:

1. Put the key your system gave you in **Settings** as a secret named `billing_system_key`.
2. Allow your system's address in **Settings** (turn **Advanced** on to see the list of allowed addresses).
3. Fill in the address your system gives for switching one customer off, and the one for switching one back on, when you pick the workflow.

Taskiem sends `{"account": "<the account on your system>", "action": "suspend"}` (or `"reconnect"`), signed with your key as `Authorization: Bearer <key>`, and an `Idempotency-Key` so a repeat is never applied twice. Your system must be **reachable on the internet** at an `https` address: Taskiem does not yet reach a system that only has a private address inside your own network.

Without these two workflows, the other three work with nothing connected but SMS and invoices.

## Things to know

- Keep **Renews on** right when you add a subscriber. The reminder and switch-off workflows read it, and the pay-and-reconnect workflow moves it on.
- Taskiem finds the subscriber who paid by **phone number**: the number on the invoice must be the one in the table.
- A subscriber who is switched off and has nothing in **Account or username on your system** is left alone: Taskiem cannot switch them back on for you.
- Every switch-off is approved by a person first. Taskiem never switches anyone off by itself.
