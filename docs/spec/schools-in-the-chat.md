# Schools in the chat, and parents on Taskiem

Status: **notes for a plan** (17 November 2026; brought up to date on 19 November 2026, once [Taskiem for everyone](taskiem-for-everyone.md) merged in PR #198), written with the founder. Nothing here is built. It is settled beside the automation choices.

Builds on: [Schools](schools.md) (B0 to B9 built, [open items](schools-open-items.md)), [the phone app's school](mobile-app-look.md#the-school-as-drawn), [chat tools](chat-tools.md), [Taskiem for everyone](taskiem-for-everyone.md) (stages 1 to 4 built), [people chatting with people](people-chatting.md) (stage 5a built; 5b, [groups and ordering together](people-chatting-5b.md), largely built).

## What Schools has today

Schools is a full area a business can turn on, built in Taskiem as stages B0 to B9 ([schools.md](schools.md)):

- every student has their own bank account number, so a parent's transfer lands on the right child by itself;
- a term's fees become invoices;
- money nobody can place waits in To match, with suggestions;
- receipts with a number and a QR code;
- a family page parents open with no login;
- a reminder ladder, promises to pay, and payment plans;
- a second person must approve cash and discounts;
- clearance, and a gate screen with no amounts;
- the proprietor's overview and a nightly daily close.

**In the phone app** ([mobile-app-look.md, "The school, as drawn"](mobile-app-look.md#the-school-as-drawn), built 17 November 2026):

- **School** takes Orders' place in the bottom bar for those who read the school (Chats, Today, Money, School). The tab shows:
  - the term, and what's collected of what's expected;
  - what's still owed and due by today;
  - this week's money, and how parents paid;
  - owing by how late, and who owes the most;
  - the day's close, with Fix it on each item.
- **Students** and one student's page.
- **Money at the desk:**
  - recording a payment, which waits for a second person above the owner's amount;
  - payments and cash;
  - receipts.
- **Chasing:**
  - Debtors by how late, with reminders to up to 50 families at once;
  - promises to pay;
  - payment plans, approved in 2, 3 or 4 parts.
- **Clearance and the gate:**
  - the clearance list, with overrides and Withdraw;
  - the gate screen, which says CLEARED or SEE THE BURSAR, never an amount. A gate session opens on it and nothing else.
- **Setup:**
  - classes and terms, a class's fees, invoicing a term;
  - importing students, account numbers, promotion to the next class, settings.
  - The school's wallet is set up on the web only.

**In the chat:**

- **The briefing card** (app and web) has a school part:
  - the term's fees collected of expected, and this week's;
  - who owes, cleared of the term's students;
  - what waits for this person as the second person;
  - yesterday's close and what of it isn't matched.
- **Second-person requests** come as a `school_approval` card, with Approve and Decline, in the app and on the web.
- **The phone's tools panel** starts with the school's tiles: Students, Record a payment, Debtors, Clearance.
- **"tools" on WhatsApp, Telegram and Slack** lists the school's pages on the web.
- **Over WhatsApp and Telegram:**
  - second-person approvals;
  - the schools line in the daily summary;
  - the templates sent to parents: reminders, receipts, account numbers, plan decisions, family links.

## Where it doesn't reach the chat yet

- **Words in the Taskiem chat.** There are no school words. A bursar can't ask "who owes in JSS2?", "how much came in today?", "Ada Obi's balance" or "send a reminder to the Okafors". "who owes me" answers from the invoice debtor book, not the school's (`api/chat_debtors.go`).
- **The web's chat tools** have no school tiles. The phone has four.
- **Student and family cards and tags.** A student or family can't be tagged, as in "#Ada Obi paid half; plan for the rest?", and there's no card to show for one.
- **Parents** have no account and no conversation with the school. They get SMS or WhatsApp messages and a family page link.
- **School automations** beyond the reminder ladder and the nightly close (below).

So Schools is now fully in the app and partly in the chat. What remains is asking in words, tagging students and families, the parents' side, and automations.

## 1. For the school's staff (bursar, proprietor, front desk)

**Words in the Taskiem chat**, answered from the school's own record, in the app, on the web, and on WhatsApp and Telegram:

- "who owes in JSS2", "today's collections", "Ada Obi";
- "remind the Okafors", and "cash ₦50k from Mrs Bello for Tunde", which goes to the second person as recording a payment does now;
- "clear Tunde", which asks first, as an override does (a reason and an end).

**School tiles in the web's chat tools,** as the phone has:

- Students, Record a payment, Debtors and Clearance;
- and on both, Families, Fees, Receipts and To match where a sheet serves.

Each opens a sheet as the shop tools do, or the page where the school's is fuller.

**Student and family cards:**

- "#Ada Obi" (or "/student ada") shows a row with class, balance and clearance.
- It opens for the fee lines, payments, and the actions Remind, Record a payment and Payment plan.
- It uses the fold-and-open card built for orders and invoices ([chat-five-more-flows.md](chat-five-more-flows.md), tagged cards, 17 November 2026).
- A family's card lists its children, the family's balance and the account numbers.
- Who sees a card follows `school.read`. A gate session never sees one.

**The phone app:** built (above). What's left there is the words and cards, so the app and the web chat stay the same.

## 2. For parents

Today a parent gets an SMS or WhatsApp message and a family page link.

**What #198 already gives parents** ([Taskiem for everyone](taskiem-for-everyone.md)):

- **A personal account** with their phone (stage 1). Parents and guardians are the users. Students are not: accounts are for those 18 and over (§15 there).
- **Chatting with the school** like any business, in the app or the phone's browser, from its chat link or Discover (stage 2). The school's staff answer in its customer chats, as a shop's do.
- **One parent across the app and WhatsApp** (stage 4). A parent writing on the school's own WhatsApp number and in the app is one customer to the school, joined by their verified phone. Families who stay on WhatsApp keep working as they do.

**What it does not give them yet: their fees.** The customer door (§8.4 there) lets a person reach "their own orders, bookings and invoices" with a business. A school's fees are invoices on a student's family, tied to the guardian's phone, not to the person, so a parent sees none of them. The new work is mostly here:

- **The customer door gains the family:** the students whose guardian phone is the person's verified phone, their fee invoices, receipts, payment plans and promises. It is read-only except for promising and asking for a plan. It is audited like the door's other entries, never shows another family, and is closed by a business that blocked the person.
- **Matching a parent to their family** uses the guardian's phone, which already ties a guardian to one family ([schools-open-items.md](schools-open-items.md)). It is confirmed the way #198 confirms a person's orders by their verified phone: a waiting count, then "These are mine" (§13 there). A second guardian on the same family is their own person, matched by their own phone.
- **"Fees" on You**, beside Your orders:
  - each child's balance;
  - the account number to pay into;
  - receipts;
  - the payment plan and its next date.
- **In the chat with the school:**
  - ask about fees and get answers from their own family's balance;
  - a family's card;
  - **promise to pay or ask for a plan**, which the bursar answers with a tap. The school's side already decides these (Debtors, plans).
- **Notices** (term starting, fee invoices, clearance, receipts on payment) by push in the app, through #198's push on You, instead of SMS. This also saves the school's message costs. WhatsApp or SMS is kept for families not in the app.

**Groups** (stage 5b) are people's own: parents may make a class group among themselves, and the school is not in it. A school speaking to a class's parents (term dates, an event) is a different thing: a notice from the school to the families of a class, sent into each family's own chat with the school, never a group the school runs. Whether that is wanted is a question below.

## 3. School automations

These sit with the automation discussion; the choice of which to plan first is made there.

- **A term's invoicing on autopilot:** term dates come in, invoices are drafted from the fee structure, and the bursar checks and sends them with one tap (the plan, then confirmed, as now).
- **The reminder ladder, aimed at the app first:** it goes by push, falls back to WhatsApp or SMS, and stops when the family pays or promises.
- **Clearance follows payment by itself:** a parent pays, the child is cleared, and the gate list updates. The rule already makes clearance follow the balance. What's new is telling the family and the gate at once.
- **The proprietor's evening summary** is already the daily close and is on the School tab. In the Taskiem chat it becomes a card each evening, not only a line in the briefing the next morning.
- **Saved things for schools:** @bursar, @school-account, and templates like #fee-schedule, #uniform-list and #resumption-date, from the saved-things idea.

## Questions

1. **Who first:** the school's staff (words and cards in the chat), parents on You, or both together?
2. **Parents' conversations:** they already follow #198's customer conversations (one conversation per school, the same safety rules). Should the family's fees come through the customer door, with "Fees" on You as a school's version of Your orders? Suggested: yes, rather than a separate parent area.
3. **Notices to a class:** should a school be able to send one notice to the families of a class (or the whole school), arriving in each family's own chat with it? This would follow the offers rules where it is not about the family's own fees: only to families who chat with the school, at most so often.
4. **Priority against the automation list:** schools near the top? Its staff side is mostly built, and parents are a large audience now that #198 has landed.

(The question of the gate screen in the app is settled: it is built.)
