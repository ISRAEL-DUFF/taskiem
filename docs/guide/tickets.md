# Customer tickets

When customers write to your business with a question or a complaint, Taskiem can turn each one into a ticket with a number, such as **T-1042**, send it to the department that should answer, remind people when an answer is late, and close it when it is done. The customer is told their number at once, and gets your team's answers the way they wrote to you.

Everything about your tickets is kept in two tables Taskiem makes for you: **Tickets** (one row per ticket: who, what, which department, how urgent, where it stands) and **Ticket messages** (every message of every ticket). Open them under **Tables** at any time.

## Choose where tickets come from

Pick one of these in **Templates**:

| Template | Customers write to |
| --- | --- |
| **A helpdesk on your Taskiem email address** | The email address Taskiem gives the workflow. Give it to customers, or forward your support mailbox to it. |
| **A helpdesk on your Zoho Mail mailbox** | Your own Zoho Mail address. Also add **Send ticket answers from your Zoho Mail mailbox**, so answers go out from your mailbox. |
| **A helpdesk on your Gmail mailbox** | Your own Gmail address. Also add **Send ticket answers from your Gmail mailbox**. |

Then add the two that work for every kind:

- **Answer customers' tickets**: the form your team answers tickets with.
- **Follow up tickets nobody has answered**: reminders, and closing old tickets.

Tickets from your website's form, WhatsApp or your staff come in the same tickets table; those templates are added separately.

## Your departments

When you pick a helpdesk template, you write your departments once, one per line: the department's name, the role whose people answer it, what it looks after, and words that send a ticket there, separated by a **|**:

```
Billing | accounts | Payments, invoices and refunds | refund, invoice, payment
Delivery | dispatch | Orders on their way, late or damaged deliveries | delivery, late, rider
General | support | Anything else |
```

Give each role to the right people under **Members**. Then choose:

- **Department when none fits**: where a ticket goes when nothing else fits (General, above).
- **How a ticket finds its department**:
  - **ai**: Taskiem's AI reads the message and picks the department, says how urgent it is (low, normal, high or urgent) and writes a one-line summary. The customer's name, email address and phone number are hidden from it; the words of their message are not, since it has to read them.
  - **rules**: the first department with one of its words in the message.
  - **chosen**: the department the customer picked, where they could pick one.
- **What the customer is told first**: their ticket's number is added after it.
- **Days a resolved ticket can be reopened**: a customer who writes back within these days reopens their ticket; later, they get a new ticket that says which one it follows.

## When a ticket comes in

1. It gets the next number and is saved in your tickets table, with the customer's message.
2. The department's people are told on WhatsApp or Telegram (or by email if they linked neither): the number, the subject, how urgent it is, the summary, and a link to answer it.
3. The customer gets your first answer with their number, by email in the same conversation.

When the customer replies to that email, the reply is added to their ticket, the ticket is open again, and the department (or the person who has the ticket) is told.

## Answering a ticket

Open **Answer customers' tickets** from the link in the message about the ticket, or from **Workflows**. You need to be signed in to Taskiem. Type the ticket's number (the link fills it in), write your answer, and choose:

- **Answer and wait for the customer**: the customer gets your answer; the ticket waits on them.
- **Answer, and the ticket is resolved**: the customer gets your answer; the ticket is resolved.
- **A note for the team**: saved with the ticket; the customer does not see it.

The first person to answer a ticket is the one it is assigned to; from then on, they are the one told when the customer replies.

**Put the link in every message.** After you publish **Answer customers' tickets**, copy its form's link and save it in **Settings**, under **Settings your workflows read**, as `ticket_reply_form`. Every message about a new ticket then has a link that opens the form with the ticket's number filled in.

## Follow-ups

**Follow up tickets nobody has answered** looks at your tickets every 15 minutes:

- A ticket nobody has answered within **Hours to give the first answer** (4 unless you change it) is sent to its department again.
- After twice as long, the person or role you choose (a manager) is told.
- A ticket waiting on the customer for more than **Days to wait for the customer** is resolved, and the customer gets your closing message.
- A resolved ticket is closed after **Days before a resolved ticket is closed**.

## Good to know

- Emails Taskiem sends come from "*Your business* via Taskiem", with your helpdesk address to reply to, and count in your plan's messages like every message to customers. With Zoho Mail or Gmail, answers come from your own mailbox.
- Customers who wrote on WhatsApp, or gave only a phone number, get answers on WhatsApp, Telegram or by text.
- Tickets, messages and notes are your business's records, kept in your tables. Customers' names, emails, phone numbers and messages are personal data: they are hidden in run history from people who may not see personal data.
