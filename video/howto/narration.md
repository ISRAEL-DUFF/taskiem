# How-to videos: narrator scripts

One spoken line for every on-screen caption of the 17 how-to videos
(`video/howto/NN-*.mjs`), in the same order, so each line can be laid on its
caption's time in the video's `.srt` (`out/howto/<slug>.srt`).

## Reading this file

- `Card:` is a title card (words in `*stars*` on screen). Read it in the
  card's few seconds. These are not in the `.srt`.
- `1.`, `2.`, ... are the captions, numbered in order, **one narrator line
  each**. They match the `.srt` entries one to one. A line is spoken from the
  start of its caption.
- Numbers, naira and symbols are written the way to **say** them ("seventeen
  thousand naira"), so a text-to-speech tool reads them right. Do not change
  them to digits.
- The lines are close to the captions but trimmed for the ear; the screen
  shows the exact words, the narrator tells the story.

## Narrator

Same voice as the chat film (`characters/chat-hybrid-plan.md`, section 2):
warm, calm, friendly, unhurried; Lagos English accent; about 2.2 words a
second; a short pause at each full stop. Taskiem is "TASK-ee-em". For these
how-tos the narrator talks to the viewer as a guide: steps are plain
instructions ("Tap Connect."), the story parts are told in the present tense.

## Fitting the timing

Caption times come from the recording, so they are known only after a video
is recorded. Then run:

```sh
node video/howto/narration-check.mjs 01-link-and-price-list
```

It prints each line, its caption's length and its speed, and flags any line
over 2.4 words a second or any count mismatch. To fix a line that is too
long, shorten the narration first; if the caption itself is too short for a
needed line, raise its `say(text, hold)` in the video's script and record
that act again. Do not speed up the voice.


---

## 01-link-and-price-list: Link with a supplier

Card: How to link with a supplier.

Card: One. Ask to link.

1. Ada runs Ada's Kitchen, and wants to buy from Grace Catering Supplies.
2. In Chat, tap Find a business.
3. Search for the business by name.
4. Tap Connect. The request goes to Grace.
5. The card says Requested. Until Grace answers, Ada can withdraw it.
Card: Two. The supplier accepts.

6. Grace sees Ada's request at the top of her chats.
7. She taps Accept.
8. They're linked. Each now has a chat with the other.
Card: Three. Make a price list, and share it.

9. A price list is what linked buyers see and order from. Grace makes one.
10. Open Menu, then Storefront.
11. Tap Price lists.
12. Name the list, then tap Make a price list.
13. Tap Items. Every item comes from Grace's own stock.
14. Tick what to sell. Each price starts at the stock price.
15. Change a price for this list only. Here, the chafing dish is seventeen thousand naira for trade buyers.
16. Save.
17. Now tap Share, and tick the businesses that get this list.
18. Each buyer sees one list. Sharing this one replaces any list Ada had from Grace.
19. Shared with Ada's Kitchen. Change a price later, and Ada sees it at once.
Card: Four. Order from the list.

20. Back on Ada's side, open the chat with Grace.
21. Open the tools, and tap Order.
22. Grace's list, at Grace's prices for Ada: the chafing dish at seventeen thousand naira.
23. Add what you need.
24. Review the order, then place it.
25. The first order from a new supplier asks two things. First, their phone number.
26. Then, which of Ada's own items each one is. New ones are added to her stock, and both are remembered.
27. Place the order.
28. The order lands in the chat, on both sides, for Grace to accept.


---

## 02-order-in-chat: An order in the chat

Card: How to handle an order in the chat.

Card: One. The seller accepts.

1. Ada's Kitchen has sent Grace an order. It shows up in her chats as a new message.
2. The order card shows what it comes to, and how far it has got.
3. Grace taps Accept. If she can't fill it, Decline asks her why, and Ada is told.
4. Accepted. Ada's card says so too.
5. On Ada's side, every step Grace takes arrives in their chat.
6. Accepted, and the bar under the order moves along: Placed, Accepted, On the way, Delivered.
Card: Two. Ready, sent, delivered.

7. To move the order along, Grace opens it from the card.
8. Its page shows what Ada ordered, and at what price.
9. Packed? Tap It is ready.
10. On the road? Sent on its way.
11. Handed over? Delivered, with how many of each went.
12. Ada then says All came, or Not all came. If she says nothing for a day, Grace's count stands.
13. Lower down, Ada's terms: when the invoice is raised, the days to pay, and a credit limit.
Card: Three. The buyer says it came.

14. Back with Ada: Grace says it's delivered.
15. Everything arrived, so Ada taps All came. If something's missing, she opens the order and says Not all came.
16. Ada's stock goes up by what came, and Grace's invoice is raised by itself…
17. …and lands in the chat as Ada's bill, with Pay on it. Paying a bill is the next video.


---

## 03-bills: Question a bill, and pay it

Card: How to question a bill, and pay it.

Card: One. Question the bill.

1. Grace's invoice for the order is in the chat. On Ada's side, it's a bill to pay.
2. View invoice opens the bill: what it's for, what's left to pay, and when it's due.
3. Two of the chafing dishes came dented. Before paying, Ada taps Question it.
4. Grace is told. Until it's settled, the bill isn't paid from Taskiem.
5. The bill shows Queried, on both sides.
Card: Two. The seller settles it.

6. Grace sees the question in the chat, in Ada's own words.
7. Settle the query opens the invoice.
8. She can say the invoice stands, or take an amount off. Two dishes at seventeen thousand naira: thirty-four thousand off.
9. The reason goes on the invoice, and to Ada.
10. The invoice is now forty-nine thousand naira. A sent invoice is never edited: the change is a line of its own.
Card: Three. Pay, and say so.

11. Ada sees Grace's answer in the chat.
12. The bill is forty-nine thousand naira now, with the thirty-four thousand off as a line.
13. Ada pays from her own bank app, then tells Grace: I paid outside Taskiem.
14. How much, and how: by transfer, in cash, or another way.
15. The bill says Paid, they say, until Grace's money arrives.
16. Grace is told Ada says she paid forty-nine thousand naira.
17. It counts as paid only when the money arrives. When the transfer reaches Grace's account, Taskiem matches it to the invoice, or Grace does, under To match. Then both sides see Paid.


---

## 04-customer-whatsapp-order: A customer's order from WhatsApp

Card: How to take a customer's order from WhatsApp.

1. Chioma, a customer, has sent Ada's Kitchen's WhatsApp number an order, in her own words.
2. Customers' messages come into Ada's chats, beside the businesses she trades with.
3. Taskiem reads the message into an order: each thing she asked for, from Ada's menu, at Ada's prices.
4. Under each line is what she wrote. Change a number with minus and plus, or add an item she asks for.
5. It's for delivery, to the address in her message. Ada adds the delivery fee.
6. How she pays: when it arrives, or here. With a payment page, she can pay now by transfer.
7. Nothing goes to Chioma until Ada taps Confirm.
8. Chioma gets her order on WhatsApp: each line, the delivery, the total, and how she pays. The card says Confirmed.
9. Ada can write back in the same conversation.
10. It goes from Ada's Kitchen's own number. The order is on the Orders page, to pack and send like any other.


---

## 05-staff-and-clocking-in: Add staff by phone, and clock in

Card: How to add staff by phone, and clock in.

Card: One. Add by phone number.

1. Ada has a new cashier, Kemi. She adds her from the Taskiem chat: a name, a phone number, and what she does.
2. Taskiem asks first. Nothing goes to Kemi until Ada says yes.
3. Kemi gets a WhatsApp message: Ada added her to Ada's Kitchen as a cashier, with Join. She needs no email and no password.
Card: Two. Kemi joins.

4. Kemi taps Join on her phone, and Ada is told.
5. Kemi joined as cashier. The Staff page opens, where everyone who works there is.
6. Ada opens Kemi to set her usual hours: Monday to Saturday, eight to four.
7. If Kemi hasn't clocked in fifteen minutes after eight, Ada is told: Kemi has not clocked in. Due at eight.
Card: Three. Clock in.

8. Kemi starts her shift with one word on WhatsApp: in. Tunde, another cashier, uses Taskiem on the web.
9. In the chat's tools: Clock in.
10. He's clocked in. At the end of the day: out, or Clock out.
11. On the Staff page, Today shows who is in now, and since when.
12. From any chat, Ada can ask: who is in.
13. The morning briefing says it too, and who isn't in yet.


---

## 06-tasks-for-staff: Give staff tasks

Card: How to give staff tasks.

Card: One. Tag them in the chat.

1. Ada gives Tunde a task the way she'd tell him: at his name, what to do, and by when.
2. Taskiem asks first. Yes makes it a task.
3. A task that comes back? Say every day, or every Monday, and the time.
4. Each day's is a task of its own, given to Tunde at the start of the day.
Card: Two. They tap Done.

5. Tunde hears in his chat, here or on WhatsApp, with Done and Can't.
6. His tasks are also under Tasks in the chat's tools.
7. The fridge is full. Done.
8. Can't, with a word why, sends it back to Ada instead.
Card: Three. See it done.

9. On the Tasks page, under Everyone's: Tunde did it.
10. The daily one is under Repeats: every day at seven, with Stop.
11. Half an hour before a task is due, Tunde is nudged. If it's late, Ada is told, with Remind.


---

## 07-morning-briefing: Read the morning briefing

Card: How to read the morning briefing.

1. From eight every morning, Ada's Taskiem chat opens on the day's briefing. It greets her by the hour.
2. Sales yesterday, against the same day last week, with the last seven days as bars.
3. Then what she's owed, what's running low, and today's deliveries. With staff, their tasks and who's in.
4. Each one opens what it counts. Low stock opens the Stock tool.
5. Got it puts the card away for the day.
6. It's back any time from the chat's tools: Briefing. Or just type briefing.
Card: Ask how you did.

7. Ada can also ask how the business did, in her own words.
8. The week's sales, day by day, and how they compare with the week before.
9. Tapping Sales yesterday on the card asks the same about yesterday.


---

## 08-riders-and-deliveries: Send an order with a rider

Card: How to send an order with a rider.

1. Chioma's order is confirmed for delivery, paid at the door. The Taskiem chat says it needs a rider.
2. Assign rider shows Ada's riders, and the areas they cover.
3. Next in turn offers it to each rider in turn. Ada picks Kunle.
4. Kunle gets it on WhatsApp: the order, the area and his fee, with Accept and Can't. The address comes once he accepts.
Card: Kunle delivers.

5. Kunle taps Accept and gets the address, then Picked up, then Arrived, one button after the other.
6. At the door he's asked for thirteen thousand six hundred naira. He takes it in cash and taps Cash received: delivered.
7. On the Deliveries page: who has which order, and how far it's got.
8. Chioma hears on WhatsApp when it leaves with Kunle, and when it's delivered, with a link to rate it.


---

## 09-selling-and-handover: Sell at the counter, and hand over

Card: How to sell at the counter, and hand over.

Card: One. Sell.

1. A customer at the counter wants two jollof rice and a zobo. Tunde opens Sell in the chat's tools.
2. Everything Ada's Kitchen sells, at its price. Plus adds one.
3. The total adds up as he goes. Next.
4. How it was paid: cash, transfer, or POS. This customer paid by transfer.
5. Record the sale.
6. The sale is in the chat, the stock comes down, and the money is written to today's sales.
Card: Two. Hand the shift over.

7. At the end of the day, Sam hands the shift over: Handover, in the chat's tools.
8. The shift's sales, how they were paid, and the cash that should be in the till.
9. Sam counts the cash and types what's there.
10. Taskiem says at once if it's short or over, and by how much.
11. A note for the next shift, then Hand over. Ada is told, and Sam's shift ends.


---

## 10-products-and-stock: Add products and keep stock

Card: How to add products and keep stock.

Card: One. Add a product.

1. Ada has a new dish. In the chat's tools: Products.
2. Everything she sells, to search or filter. Tap Add product.
3. Give it a name, a category, the price, what it's sold by, and how many there are.
4. It's in her products, and the chat keeps a line of what changed.
Card: Two. Stock in.

5. Stock, in the same tools, puts what's running low first.
6. Low stock shows only those.
7. Ten bags of rice have come in. Ada opens the rice.
8. How many came in, and a note if she wants one.
9. The count goes up, and the chat says by how much.


---

## 11-invoices-and-payments: Send an invoice, and match the payment

Card: How to send an invoice, and match the payment.

Card: One. Send an invoice.

1. Chinedu's office had lunch from Ada's Kitchen. In the chat's tools: Invoices.
2. What she's owed, what's late, and drafts. Tap New invoice.
3. Who it's for, their phone number, what it's for, and how much.
4. Send texts it to his phone, with a link to see it and pay. Keep as draft saves it for later.
5. The chat keeps a line of it.
Card: Two. Match the payment.

6. Chinedu paid by transfer, but it named no invoice. Money, in the same tools.
7. To match shows what's come in that isn't tied to anything yet.
8. Thirty-seven thousand five hundred naira from Okonkwo C, for lunch. Match to an invoice.
9. Chinedu's invoice, thirty-seven thousand five hundred naira to pay. Match it.
10. The invoice is paid, and the chat says so.


---

## 12-approvals: Approve what needs your OK

Card: How to approve what needs your OK.

1. Tunde asked to pay Bayo Rice Mills. The Taskiem chat shows Ada a card: Needs your OK.
2. Who's asking, for what, how much, and to which account, with Decline and Approve.
3. Approve asks once more. It's the first payment to this account, so Taskiem says to check the number and name first.
4. A comment, if she wants one. Her decision, and what she was shown, are recorded.
5. The card goes, and the chat keeps a line of what she approved.


---

## 13-own-whatsapp-number: Connect your own WhatsApp number

Card: How to connect your own WhatsApp number.

1. Until now, Ada's customers and staff hear from Taskiem's shared number. In Settings: WhatsApp number.
2. Most businesses never need more. To send as Ada's Kitchen, the guide gives each step on Meta's side.
3. From Meta: the phone number ID, the WhatsApp Business Account ID, and a system user's token.
4. On her own Meta app, the app secret too, and a verify token she makes up.
5. Tap Connect.
6. Connected. Taskiem shows two addresses to paste back into the Meta app.
7. And the message templates Meta must approve, for messages outside the twenty-four-hour window.
Card: Customers write to it.

8. Funmi writes to Ada's Kitchen's own number. Her message is in Ada's chat, under Customers.
9. Ada's reply goes from her own number.


---

## 14-deals: Make a deal with a business

Card: How to make a deal with a business.

Card: One. Your prices.

1. Rivers Foods buys from Ada's Kitchen's price list. Ada opens its items.
2. A lower price for more: from fifty small chops packs, two thousand naira a pack.
3. And the lowest she'll go, set in the product, from the chat's tools.
4. Lowest price: one thousand nine hundred naira a pack. Buyers never see it.
Card: Two. An offer.

5. Rivers Foods orders from Ada's Kitchen in their chat: the tools, then Order.
6. Ada's Kitchen takes at least twenty. Rivers wants fifty, so the price for fifty or more applies by itself.
7. Rivers would rather pay less. Make an offer, for the whole order.
8. The offer is a card in the chat, on both sides.
Card: Three. Counter.

9. At Ada's Kitchen, Tunde sees the offer: eighty-eight thousand naira for the fifty.
10. He can accept, decline, or counter. He counters at ninety-seven thousand naira.
11. Rivers counters back: ninety-three thousand naira.
Card: Four. Agree.

12. Ninety-three thousand is under Ada's lowest price for fifty. Tunde accepts it.
13. Below the lowest price, it waits for Ada's OK.
14. Ada's Taskiem chat asks her: Tunde would agree ninety-three thousand naira, under her lowest price. Agree, or Later.
15. Agreed. The deal stands at ninety-three thousand naira.


---

## 15-explore-and-connect: Find a business and connect

Card: How to find a business, and connect.

1. Ada wants a suya seller for her small chops trays. In Chat: Find a business.
2. Explore shows businesses near her first. She searches by what they sell.
3. What they sell, a word about them, and their photos.
4. Connect asks them to link.
5. Requested: they're asked to accept. Withdraw takes it back.
Card: They accept.

6. Mallam Musa sees Ada's Kitchen's request at the top of his chats.
7. Accept.
8. Linked. Ada's Kitchen is in his chats.
Card: When a business asks you.

9. Businesses can find Ada too. Kola's Bakery asked to link: Accept, Decline, or Block.
10. And Mallam Musa Suya Spot accepted hers. Both are in her chats now.


---

## 16-tags-in-the-chat: Tag things in the chat

Card: How to tag things in the chat.

Card: Hash, an item.

1. In her chat with Bayo Rice Mills: the hash sign tags one of his items. Type hash, and a few letters.
2. His items, at his price for her, and the least he sells.
3. It goes as a card for the item, at his price, with Order. Neither side has to guess which.
Card: At, a product.

4. The at sign tags a thing or a business: his products and services together.
5. Her own stock shows on the card too: four bags left of the rice she needs.
Card: Slash, a bill or an order.

6. A slash tags a bill, an order, or an invoice. Slash bill shows Bayo's bills.
7. The bill goes with the message: its number, amount, and whether it's paid.
Card: At, a business.

8. In her Taskiem chat, the at sign with a few letters looks among her products and businesses.
9. Even businesses she doesn't trade with yet: their card, to connect.


---

## 17-business-chat-tools: The tools in a chat with a business

Card: How to use the tools in a chat with a business.

Card: Tools about them.

1. Ada's chat with Bayo Rice Mills. Tap the tools button.
2. The tools are about Bayo: With Bayo Rice Mills.
3. His shop, your orders and bills with him, the statement between you, and an invoice to him.
4. Their shop: Bayo's rice, at the prices he shares with Ada. Order from here.
Card: Their shop.

5. From the other side: Bayo, in his chat with Ada's Kitchen.
6. His tools here are about Ada: her shop, and Book, for her services.
7. Ada shares no price list with Bayo, so he sees her storefront: her prices, and how many are left.
Card: Book their services.

8. Book: Ada's services, as her storefront shows them.
9. Pick a day, and a time she's free.
10. Check it, add a phone number for Ada, and a note. Then Book.
11. Booked. The chat says so, to Bayo and to Ada.
Card: Your own tools: Taskiem.

12. Ada sees Bayo's booking in their chat.
13. Her own tools are in her Taskiem chat.
14. Products, Stock, Sell and the rest: here, not in a chat with another business.

