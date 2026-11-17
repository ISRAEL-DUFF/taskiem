# People chatting with people (stage 5 of Taskiem for everyone)

Status: **draft for review** (17 November 2026; encryption settled the same day, §17). Stage 5 of [Taskiem for everyone](taskiem-for-everyone.md) (§16, §20), under decision [0068](../decisions/0068-taskiem-for-everyone.md), which left this stage "its own plan and decisions". Nothing here is built. The questions in §18 are for the founder; a decision record (0069) follows the answers.

## 1. Summary

Stages 1 to 4 made the Taskiem app a chat app for everyone: a person signs up with a phone, chats with the businesses they buy from, finds new ones in Discover, and sees their orders in one place. Stage 5 lets people chat **with each other**.

The proposal, in one paragraph: one-to-one chats between people first, found by phone number, a person's link or (with consent) the phone's contacts; groups next; calls last. A stranger's first message waits as a **request** until it is accepted. Chats between people are **kept on Taskiem's servers, encrypted at rest and readable by Taskiem's servers**, as chats with businesses are (founder, §17): not end-to-end encrypted, and the app says so plainly. What makes Taskiem worth using for this is what WhatsApp lacks: **the businesses are already here**. A person can share a product, an order, a business or a pay link into a chat, a group can order together from one business, and friends can split what they paid.

## 2. Why this stage, and what brings people's own chats

Competing with WhatsApp for chats between people head-on is hard. In Nigeria nearly everyone is already there. People will not move their chats; they will add Taskiem for what WhatsApp does badly. This stage must lean on that:

- **Shopping together.** "Look at this dress" with the product, its price and sizes as a live card, not a screenshot. The friend taps it and is in the shop.
- **Ordering together.** A family or an office orders lunch from one business in one group: each picks, one person pays, or each pays their share. Today that is a long WhatsApp thread and a spreadsheet.
- **Splitting what was paid.** "I paid ₦18,000 for the cake; your share is ₦3,000": a share card each person can pay. (Who holds the money is §11's question; Taskiem never holds money today.)
- **One place for orders.** Sharing an order or a delivery's progress with whoever is waiting for it.
- **The same app.** Their business chats, orders and Discover are already here; their own chats sit beside them.

So the first version is not "WhatsApp again". It is people chats with businesses inside them.

## 3. What exists today (stages 1 to 4)

| Part | Today | What this stage needs from it |
| --- | --- | --- |
| Personal accounts (§5) | A person with a verified phone, devices, second proofs, deletion | The person is the chat's party; deletion removes their side |
| Person's own scope (`taskiem_person_scope()`) | Rows only the person reaches | People chats live here, for both people |
| Two-copy conversations (stage 2) | Each side keeps its own copy, deletes its own | Kept for people chats: each person's copy is theirs (§9) |
| Push on You (stage 2 fixes) | A person's devices, kinds, preview setting | New kinds: a person's message, a request, a group |
| Blocking and reporting (stage 2) | Person ↔ business | Person ↔ person, with requests (§8) |
| Media (stage 2) | Checked, per side, limits | Reused as is: people chats' media are checked and kept per copy (§6, §13) |
| Phone index (stage 3) | Keyed hashes of phones, platform key | The model for contact matching (§5.3) |
| Cards (stages 2–4) | Order, pay, booking, product, invoice, delivery | Shared into people chats (§10) |

What does not exist: anything between two people, contacts, groups, presence at consumer scale, calls, and any end-to-end encryption.

## 4. Words used here

| Word | Meaning |
| --- | --- |
| A **people chat** | A conversation between people, one-to-one or a group. Not a business chat |
| A **request** | A first message from someone the person has not chatted with and who is not in their contacts; it waits until accepted |
| **Contacts** | People the person can find by the phone's address book, matched without uploading numbers in the clear |
| A **person's link** | `https://<host>/p/{code}`, like a business's chat link, for being found without sharing a number |
| **End-to-end encrypted** | Only the people in the chat can read it: the server keeps what it cannot open |

## 5. Finding people

### 5.1 By phone number

Type a number. If it belongs to a person who lets themselves be found by number (§5.4), their name and photo show, and a chat can start. If not: "Not on Taskiem yet" with **Invite** (an SMS or WhatsApp share with the person's link, sent from the person's own phone, never by Taskiem).

### 5.2 By a person's link

Each person can make a link (and a QR code) to share, replaced at will, like a business's chat link (§8.2 of the plan). Opening it starts a chat, as a request unless already known.

### 5.3 From the phone's contacts (opt-in)

Asked once, with plain words: "Find friends from your contacts? Their numbers are checked without being sent to us in readable form."

- The app hashes each number (E.164) with a **contacts key** fetched from the server and rotated, and sends only the hashes, in batches, rate-limited. The server matches them against verified phones hashed with the same key and answers which hashes are people who allow being found that way. Nothing of the person's address book is stored: no hashes kept after the answer, no graph built.
- Honest limit: a keyed hash of a phone number can be reversed by someone holding the key (Nigerian numbers are few enough to try them all), so this protects against leaks of what is sent, not against Taskiem itself. Stronger options (private set intersection, secure enclaves) exist and cost much more; §17 Q4.
- Re-checked when the person opens Contacts, at most once a day.

### 5.4 Who can find me and message me

In You → Privacy:

| Setting | Choices | Default (proposed) |
| --- | --- | --- |
| Find me by my number | Everyone / My contacts / Nobody | My contacts |
| Find me from contacts | On / Off | On |
| Who can message me | Everyone (as a request) / My contacts / Nobody | Everyone, as a request |
| Last seen and online | Everyone / My contacts / Nobody | My contacts |
| Read ticks | On / Off (then I don't see others' either) | On |
| Profile photo | Everyone / My contacts / Nobody | My contacts |

The person's number is shown to people they chat with only if they choose to (§17 Q5). Names are shown; numbers are not, by default, which is the opposite of WhatsApp and safer for people meeting through Discover or groups.

## 6. Encryption and where chats are kept

### 6.1 The choice (settled: B)

Three ways were weighed:

| Option | What it means |
| --- | --- |
| A. End-to-end encrypted by default | Keys on each device; the server forwards only what it cannot open (the Signal protocol) |
| **B. Encrypted on the server, readable by Taskiem's servers** (chosen) | As business chats today: sealed at rest, opened only by Taskiem's servers |
| C. B by default, with opt-in "secret chats" | Telegram's model |

**The founder chose B** (17 November 2026). People chats work the way business chats already do, so stage 5 reuses what stages 1 to 4 built, and the features that make Taskiem worth it (live cards, ordering together, search, backup, the web chat) work everywhere without a phone holding keys.

### 6.2 What B means in the product

- **Kept on the server.** Every message, photo, voice note and file of a people chat is stored by Taskiem, sealed at rest under the person's own key (as a person's copy of a business chat is in stage 2), and opened only by Taskiem's servers to show it to the people in the chat.
- **Every device has the chats.** A new phone, the web chat or a second phone shows the whole history after signing in; nothing is lost with a phone. No backup step is needed.
- **The web works on its own,** with no phone app needed.
- **Search** runs on the server across the person's own chats.
- **Push** can carry the sender and the words, as the person's preview setting says.
- **Files** get the checks business chats' files get (`engine/media`: the real type, PDFs without scripts, the malware scan), before the other side can open them.
- **Shown plainly.** The app never calls people chats end-to-end encrypted. In a people chat's info: "Messages are stored securely by Taskiem. Taskiem does not read your chats with people except to keep you safe: when you report them, to stop spam, or when the law requires it" (wording for legal review, §18 Q13).

### 6.3 What Taskiem does and does not do with people chats

Readable by Taskiem's servers does not mean read. Proposed rules (§18 Q13–Q15):

- **No one at Taskiem reads people chats** except a moderator handling a report of that chat, or under a lawful request (§8.5), each logged.
- **No machine reads them for business features.** Order drafts, translations and reminders, which business chats get, are not run on people chats unless the person turns them on in that chat (§18 Q14).
- **Automated safety checks** may run on messages to stop spam, scams and abuse (§8.4), and keep nothing but their verdict.
- **Never used to train models or for advertising.**

### 6.4 What this rules out

People who want chats only they can read will not get them here (WhatsApp offers that). The app says so honestly rather than suggesting otherwise. A later stage could add opt-in secret chats (option C) if people ask for it.

## 7. One-to-one chats (stage 5a)

- Text, photos, voice notes, files and location; replies, reactions, read ticks, typing, delete for me and delete for everyone (within a time limit), forward (with a "forwarded" mark), star.
- **Requests.** A message from someone who is not a contact and has not chatted before arrives in **Requests**: the person sees the name, no photo unless allowed, and the first message, and can Accept, Delete or Block and report. Nothing tells the sender it was read until accepted. A sender can have a limited number of requests waiting (§8.2).
- **Lists.** You's Chats tab holds business chats and people chats together, newest first, each marked by what it is (a business has its name and trust line; a person their name). §17 Q6 asks whether they should be split.
- **Each copy is the person's.** As stage 2: each person keeps, archives and deletes their own. "Delete for everyone" removes it from the other person's copy on the server and their devices; a screenshot or a forward made before cannot be undone.

## 8. Safety at the scale of strangers

### 8.1 Blocking

Block a person from the chat or the request: they can no longer message, call or see last seen and photo; nothing tells them except that messages show one tick. Unblock at any time.

### 8.2 Limits

- New accounts: few requests a day for the first week; more as the account ages and others accept their requests.
- Requests waiting per sender: capped; a sender whose requests are often deleted or blocked gets fewer.
- Groups: limits on creating groups and adding people who are not contacts (§9).
- Messages per minute per chat, and forwards: "forwarded many times" mark and a cap on forwarding the same message to many chats at once (as WhatsApp does against rumours).

### 8.3 Reporting

Report a person or a group from the chat or a request. With the person's consent, the **last messages of that chat** (proposed: 20, as for business chats) are sent to Taskiem's moderators from the server's copy, with any photos and files in them. Moderators can warn, limit, suspend or close accounts; their decisions are appealable.

### 8.4 Spam and abuse

Signals from behaviour: how fast an account sends requests, how often they are declined, blocked or reported, new accounts messaging many strangers, many accounts from one device. With B, automated checks may also look at messages from strangers (requests and new chats) for known scam patterns: the warning on a message asking for a transfer to a personal account (§15 of the plan) shows in people chats too. They keep only their verdict. Thresholds slow or stop the sender; moderators review.

### 8.5 Lawful requests

Under Nigerian law (NDPA 2023, the Cybercrimes Act 2015) Taskiem may be required to give data. With B, that can include people chats' words. Taskiem answers only proper legal process, gives the least that is asked, tells the person where the law allows, and publishes how many requests it received. The process and the words in the privacy notice are agreed with a lawyer before 5a (§18 Q9).

### 8.6 Age

The minimum age is 18 (§23 of the plan). People chats do not change that.

## 9. Groups (stage 5b)

- Up to 256 people at first (§17 Q7), with admins who add, remove and choose who can post.
- **Joining:** added by an admin who has them as a contact, or by a group's invite link; people can choose that only contacts may add them to groups.
- **Kept on the server** like one-to-one chats; someone who joins sees the group from when they joined (§18 Q16).
- **Ordering together** (§10.3) works in groups.
- **A business in a group:** not in this stage. A group can share a business's card and order from it (§10.3), but the business does not join a people group; it chats with the person who orders, in its own conversation.

## 10. Businesses inside people chats

### 10.1 Sharing cards

From any business chat, Discover profile, product, order or delivery: **Share** → pick a person or group. The card is sent as a message, carrying what is needed to show it and open it: the business, the product and its price when shared, an order's reference. Opening it reads the live data through the door as the person opening it (a product's current price; an order only if it is theirs to see, else just what was shared).

### 10.2 A friend opens a shop

A shared product or business opens their shop or profile; Chat starts the person's own chat with the business (from: "shared"). The business sees a new customer, not who shared it.

### 10.3 Ordering together (stage 5b, with groups)

- In a group, **Order together from {business}**: a shared basket card. Each person adds their items; the card shows who wants what.
- One person places the order through the door, as their own order, and pays, or each pays their share (§11).
- The business sees one order from the person who placed it, with a note of the lines; it never sees the group.

## 11. Money between people

Taskiem never holds money (business payments go through the business's own provider). Paying a friend is different: there is no business account to pay through. Options:

| Option | What it means |
| --- | --- |
| **A. Share cards only** (proposed for stage 5) | "Your share is ₦3,000": a card with the payer's bank account details (from their own saved details), and a **Mark as paid**. Taskiem moves no money; the friend pays by their bank app. The card keeps the count |
| B. Pay through a licensed partner | A partner wallet or a payment provider's person-to-person transfer, inside the app. Needs a licence partner, KYC on both people and fees, and makes Taskiem part of a money flow between people |
| C. Pay the business directly for your share | In ordering together, each person pays their part of the business's pay link. Possible for orders (the business is the payee); not for splitting a cake one person already bought |

Proposed: A for splitting, C for ordering together. B is its own plan, later, if people ask for it.

## 12. Calls (stage 5c)

Voice, then video, one-to-one, then group, over WebRTC through Taskiem's relays (TURN). WebRTC encrypts calls between the devices in any case; calls are not recorded or kept. Calls need their own infrastructure and cost, a push channel that wakes the phone (VoIP push on iPhone) and app-store review of calling. Planned last; its own short spec when stages 5a and 5b are in use.

## 13. The server

- **Storage:** each person's copy of a people chat is kept on the server, sealed under that person's key, as their copy of a business chat is. A one-to-one chat is two copies; a group keeps one copy for the group, sealed under a group key, with each member's own marks (read, deleted for me).
- **Presence and typing:** over the app's live connection, sent only to people allowed to see them (§5.4).
- **Push:** a person's devices (stage 2's push on You), kinds for a person's message, a request, a group.
- **Media:** checked and stored like business chats' media, per copy.
- **Scale:** the business chat server sized for consumers: more connections, fan-out for groups, storage growth (people chats are kept), rate limits. A load test before stage 5b.
- **Retention:** kept until the people delete them or their account (§18 Q12).

## 14. Privacy and data

| What | Who keeps it | Deleted when |
| --- | --- | --- |
| A person's copy of a people chat, with its media | Taskiem, sealed under the person's key | The person deletes the chat or their account (or after the retention in §18 Q12) |
| A group's messages | Taskiem, sealed under the group's key | The group is deleted, or each message per the same retention |
| Who talked to whom, when | Taskiem, with the messages | With them |
| Contacts sent for matching | Nobody: answered and forgotten | At once |
| Reports | Taskiem's moderators | Per the moderation policy |

Deleting an account (stage 3) deletes the person's copies of people chats and their messages in groups' copies are shown as from "Deleted account" (§18 Q17); the other people keep their own copies.

## 15. Stages

Each ships on Android, iPhone and the web together, as before:

1. **5a — One-to-one people chats:** finding people (number, link, contacts opt-in), requests, privacy settings, blocking, reporting, sharing business cards, splitting cards.
2. **5b — Groups and ordering together:** groups, invite links, the shared basket.
3. **5c — Calls.**

Before 5a: the privacy notice and terms rewritten, including what Taskiem does with people chats (§6.3) and lawful requests (§8.5), with a lawyer; and app-store listings updated.

## 16. Risks

- **Trust.** People may expect private chats to be end-to-end encrypted. Mitigation: say plainly what Taskiem does and does not do (§6.2, §6.3), never claim end-to-end encryption, keep the rules and log every human access.
- **A breach** exposes chats that are kept on the server. Mitigation: sealed at rest under each person's key (as business data is), access only through the server, an outside security review before 5a.
- **Lawful requests** for chats' words. Mitigation: the process in §8.5, legal review, the published count.
- **Abuse by strangers,** including fraud ("send me money for this"). Mitigation: requests, limits, the transfer warning, automated checks on strangers' messages, reporting.
- **Nobody comes.** People stay on WhatsApp. Mitigation: build only what businesses make better (sharing, ordering together, splitting) first; measure use before groups and calls.
- **Cost:** consumer-scale storage, delivery, media and calls. Mitigation: retention (§18 Q12), media size limits, calls last.

## 17. Settled

| # | Question | Answer |
| --- | --- | --- |
| 1 | Encryption | **B: encrypted on the server, readable by Taskiem's servers**, as business chats (founder, 17 November 2026). Not end-to-end; said plainly (§6) |

## 18. Questions for the founder

Still open (proposals in brackets). Q2, Q10 and Q11 of the first draft fell away with B: push carries the words as the person's setting says, there is no backup step, and the web works without a phone.

3. **Who can message me by default:** everyone as a request, or contacts only? (Everyone, as a request.)
4. **Contact matching:** keyed hashes with a rotated key, or private set intersection? (Keyed hashes, stated honestly in the privacy notice.)
5. **Showing numbers:** hide a person's number from people they chat with unless they choose to show it? (Hide by default.)
6. **The Chats list:** one list with businesses and people, or two? (One list, marked.)
7. **Group size** at first: (256.)
8. **Money between people:** share cards plus paying the business for your share, or a licensed partner wallet? (Share cards and pay-your-share now.)
9. **Lawful requests:** the process in §8.5 and the privacy notice agreed with a lawyer before 5a. (Yes.)
12. **Retention of people chats:** kept until the people delete them, or deleted after a time (e.g. 2 years) unless kept? (Kept until deleted, as WhatsApp's backups are; storage cost watched.)
13. **What Taskiem does with people chats** (§6.3): no human reads them except for a report or a lawful request, each logged; never used for training or advertising. (Yes, and in the privacy notice.)
14. **Business features in people chats** (order drafts, translations): off, or opt-in per chat? (Off; opt-in per chat later.)
15. **Automated safety checks** on strangers' messages (requests and new chats) for scams: yes or no? (Yes, keeping only the verdict.)
16. **Joining a group:** see its history from before you joined, or from when you joined? (From when you joined.)
17. **A deleted account's messages in groups:** removed from the group, or kept as "Deleted account"? (Kept, as "Deleted account", so the group's conversation still makes sense.)
