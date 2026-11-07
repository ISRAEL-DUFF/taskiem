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
| **A contact form on your website that opens tickets** | A form at a link, or on your own website ([below](#a-contact-form)). |
| **A helpdesk on your WhatsApp number** | Your business's own WhatsApp number. Also add **Send ticket answers from your WhatsApp number** ([below](#whatsapp)). |
| **Tickets from your app or website** | Your app, or your website's server, sends each message to Taskiem ([below](#your-app-or-website)). |

Then add the two that work for every kind:

- **Answer customers' tickets**: the form your team answers tickets with.
- **Follow up tickets nobody has answered**: reminders, and closing old tickets.

You can use several: every ticket, however it came, is in the same tickets table, gets the next number and is answered with the same form.

For your own people's requests to IT or HR, there are two more ([below](#it-and-hr-requests-from-your-staff)).

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

## A contact form

**A contact form on your website that opens tickets** gives you a form with its own link: put the link on your website, or show the form on a website of yours that you have verified with Taskiem. Customers give their name, a phone number if they like, a subject and their message. They type their email address first and get a code to confirm it, so your answers reach the real person and nobody can use your form to send mail to someone else. When they send it, the page shows their ticket number.

- If you chose **chosen** for how a ticket finds its department, the form asks which department too. Customers type it; anything that is not one of your departments goes to your department when none fits.
- **Your helpdesk's Taskiem email address** (optional): if you also use **A helpdesk on your Taskiem email address**, give its address here. Customers then get your answers by email from it, and their replies join their ticket. Without it, they get your answers on WhatsApp, Telegram or by text when they gave a phone number, else by email.

## WhatsApp

**A helpdesk on your WhatsApp number** turns messages to your business's own WhatsApp number into tickets. You need the number connected with the WhatsApp Cloud API (see [WhatsApp](../integrations/whatsapp.md)); then pick the template and its connection.

- A customer who writes for the first time gets a ticket and its number on WhatsApp.
- Their next messages join that ticket until it is resolved, and for the **Days a resolved ticket can be reopened** after; the department (or the person who has the ticket) is told each time. After that, a new message opens a new ticket that says which one it follows.
- Add **Send ticket answers from your WhatsApp number** too: it sends your team's answers from your number. WhatsApp lets a business write freely for 24 hours after the customer's last message; an answer later than that goes as a Taskiem message instead (WhatsApp or Telegram where the customer asked you for updates, otherwise a text message from your business's name).
- Reactions and WhatsApp's own notices do not open tickets. Taskiem does not see the customer's WhatsApp name, so their tickets show their number.

## Your app or website

**Tickets from your app or website** gives you a web address your app, or your website's server, calls with each customer's message. It is for your developers:

- Under **Endpoints**, make the workflow's key. Each call is a `POST` of JSON, signed with it: the header `X-Taskiem-Signature: sha256=` and the HMAC-SHA256 of the body. Keep the key on your server; never put it in a web page or an app people download.
- The body: `subject` and `message` (both needed), and `name`, `email`, `phone`, `department` (used when you chose **chosen**) and `external_id` (your own id for the message), as far as you have them.
- The answer is the ticket: `201` with `{"reference": "T-1042", "department": "Billing", "priority": "normal", "told_by": "email", "external_id": "…"}`. If the ticket takes more than 30 seconds (Taskiem's AI was slow), the answer is `202` with the run's id; the ticket is still made and the customer still gets its number.
- A call sent again with the same body (a retry) does not make a second ticket.
- **Your helpdesk's Taskiem email address** (optional) works as for the contact form.

## IT and HR requests from your staff

**An IT help desk for your staff** and **An HR help desk for your staff** are forms your own people fill in, signed in to Taskiem: what they need and the details. Their name and email come from their sign-in. Each request gets a ticket number, shown when they send it, and goes to the people who look after that kind of request; the member who asked hears every answer on WhatsApp, Telegram or by email, like a message to your team.

- **Kinds of request** work like departments. IT's are Accounts and access, Hardware, Software, Network and Other, answered by the **it** role; HR's are Leave, Payroll, Benefits and Policy, answered by **hr**, and Confidential, answered by **hr_lead**, so a complaint or personal matter reaches only the people you give that role. Change them as you like, and give the roles to the right people under **Members**.
- HR requests are sorted by your words (**rules**) unless you choose **ai**, since they can be personal: with rules, nothing your staff write goes to an AI model.
- **Kinds of request a manager approves** (HR only; Leave unless you change it, or **none**): those requests first go to the people with the **Who approves** role (manager unless you change it) to approve or decline in Taskiem, WhatsApp or Telegram. Nobody approves their own request. The member hears the decision; HR is told, and finishes an approved request; a declined one, or one no manager decided within three days, is resolved.
- They answer with **Answer customers' tickets**, and **Follow up tickets nobody has answered** reminds them, as for customers.
- Staff requests are kept in the same **Tickets** table as your customers' tickets. Anyone who may open your tables can read them, HR's included: give table access only to people who may see them.

## Good to know

- Emails Taskiem sends come from "*Your business* via Taskiem", with your helpdesk address to reply to, and count in your plan's messages like every message to customers. With Zoho Mail or Gmail, answers come from your own mailbox.
- Customers who wrote to your WhatsApp number get answers from it (within WhatsApp's 24 hours). Customers who gave only a phone number get answers on WhatsApp, Telegram or by text.
- Your public form and web address are protected the way every Taskiem form and webhook is: a form checks for bots, limits how often one connection can send, and asks for a phone code when it is flooded; a web address takes only calls signed with your key. Each customer gets at most 5 messages an hour and 20 a day from Taskiem, from all businesses together.
- Tickets, messages and notes are your business's records, kept in your tables. Customers' names, emails, phone numbers and messages are personal data: they are hidden in run history from people who may not see personal data.
