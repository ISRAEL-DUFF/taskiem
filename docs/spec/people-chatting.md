# People chatting with people (stage 5 of Taskiem for everyone)

Status: **draft for review** (17 November 2026). Stage 5 of [Taskiem for everyone](taskiem-for-everyone.md) (§16, §20), under decision [0068](../decisions/0068-taskiem-for-everyone.md), which left this stage "its own plan and decisions". Nothing here is built. The questions in §17 are for the founder; a decision record (0069) follows the answers.

## 1. Summary

Stages 1 to 4 made the Taskiem app a chat app for everyone: a person signs up with a phone, chats with the businesses they buy from, finds new ones in Discover, and sees their orders in one place. Stage 5 lets people chat **with each other**.

The proposal, in one paragraph: one-to-one chats between people first, found by phone number, a person's link or (with consent) the phone's contacts; groups next; calls last. A stranger's first message waits as a **request** until it is accepted. Chats between people are **end-to-end encrypted by default**: Taskiem cannot read them, unlike chats with businesses, and the app says so plainly. What makes Taskiem worth using for this is what WhatsApp lacks: **the businesses are already here**. A person can share a product, an order, a business or a pay link into a chat, a group can order together from one business, and friends can split what they paid.

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
| Media (stage 2) | Checked, per side, limits | Reused; for encrypted chats the server stores only ciphertext (§6) |
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

## 6. Encryption

### 6.1 The choice

Chats with businesses cannot be end-to-end encrypted: Taskiem reads them to draft orders, translate and remind (§14 of the plan), and the business keeps a copy under its retention. People will expect chats with each other to be private, as on WhatsApp.

| Option | What it means | Cost and risk |
| --- | --- | --- |
| **A. End-to-end encrypted by default** (proposed) | Each device holds keys; the server stores and forwards only ciphertext. Built on the Signal protocol (X3DH key agreement, double ratchet; groups with sender keys), using an audited library, not our own cryptography | The largest build: keys per device, a new device needs the chats moved to it or starts empty, backups need a person-held key, moderation cannot read reports' context unless the reporter forwards it (§8.3). Web chat is harder (keys in the browser) |
| **B. Encrypted on the server, readable by Taskiem** | As business chats today: sealed at rest, read only by the server | Much cheaper, full moderation and backup, search on the server; but people's private chats are readable by Taskiem and reachable by a lawful request. Out of step with what people expect of a chat app |
| **C. B by default, with opt-in "secret chats"** (Telegram's model) | Ordinary chats are B; a person can start an end-to-end chat on one device | Two kinds of chat to explain; most people never turn it on, so most chats stay readable |

Proposed: **A**, because the app is asking people to trust it with private conversations, and a business-run company reading friends' chats is the worst headline it could have. The business features in people chats (§10) work without reading messages: a shared card is the card's data, sent like any message.

### 6.2 What A means in the product

- **Shown plainly.** In a people chat: "Messages here are end-to-end encrypted. Taskiem can't read them." In a business chat: "Messages with businesses can be read by Taskiem to help with your orders." (as today).
- **Devices.** The phone app is the main device. The web chat and a second phone are linked from it (scan a code); history moves from the phone to the new device if the person asks, encrypted end to end.
- **Backup.** Off by default; on, it is encrypted with a key or passphrase the person keeps. A person who loses their phone without a backup loses their people chats (not their business chats or orders).
- **Safety numbers.** A person can compare a code with a contact to be sure no one is in the middle; a change of a contact's keys is shown in the chat.
- **What the server still sees:** who talks to whom and when, sizes, and device keys, which is enough to deliver and push. Push carries no message words for people chats; the app decrypts on the device (§17 Q2 on preview).

### 6.3 What A rules out or makes harder

- Server-side search of people chats (search runs on the device).
- Reading reports' context without the reporter (§8.3).
- Translations and drafts by Taskiem in people chats (they could run on the device later).
- A quick web-only start: web chat needs a linked phone.

## 7. One-to-one chats (stage 5a)

- Text, photos, voice notes, files and location; replies, reactions, read ticks, typing, delete for me and delete for everyone (within a time limit), forward (with a "forwarded" mark), star.
- **Requests.** A message from someone who is not a contact and has not chatted before arrives in **Requests**: the person sees the name, no photo unless allowed, and the first message, and can Accept, Delete or Block and report. Nothing tells the sender it was read until accepted. A sender can have a limited number of requests waiting (§8.2).
- **Lists.** You's Chats tab holds business chats and people chats together, newest first, each marked by what it is (a business has its name and trust line; a person their name). §17 Q6 asks whether they should be split.
- **Each copy is the person's.** As stage 2: each person keeps, archives and deletes their own. "Delete for everyone" asks the other's devices to remove it; it cannot guarantee that a device that saved it forgets it.

## 8. Safety at the scale of strangers

### 8.1 Blocking

Block a person from the chat or the request: they can no longer message, call or see last seen and photo; nothing tells them except that messages show one tick. Unblock at any time.

### 8.2 Limits

- New accounts: few requests a day for the first week; more as the account ages and others accept their requests.
- Requests waiting per sender: capped; a sender whose requests are often deleted or blocked gets fewer.
- Groups: limits on creating groups and adding people who are not contacts (§9).
- Messages per minute per chat, and forwards: "forwarded many times" mark and a cap on forwarding the same message to many chats at once (as WhatsApp does against rumours).

### 8.3 Reporting

Report a person or a group from the chat or a request. With end-to-end encryption the server cannot read the chat, so the reporter's device sends the **last messages it holds** (proposed: 20, as for business chats), decrypted, with the reporter's consent, to Taskiem's moderators. Moderators can warn, limit, suspend or close accounts; their decisions are appealable.

### 8.4 Spam and abuse without reading

Signals that need no message words: how fast an account sends requests, how often they are declined, blocked or reported, new accounts messaging many strangers, many accounts from one device. Thresholds slow or stop the sender; moderators review.

### 8.5 Lawful requests

Under Nigerian law (NDPA 2023, the Cybercrimes Act 2015) Taskiem may be asked for data. For people chats it can give only what it holds: account details, who talked to whom and when, and reported messages. It cannot give message words it never had. A transparency note in the privacy notice says this; legal review first (§17 Q9).

### 8.6 Age

The minimum age is 18 (§23 of the plan). People chats do not change that.

## 9. Groups (stage 5b)

- Up to 256 people at first (§17 Q7), with admins who add, remove and choose who can post.
- **Joining:** added by an admin who has them as a contact, or by a group's invite link; people can choose that only contacts may add them to groups.
- **Encryption:** sender keys (the Signal protocol's group method), re-keyed when someone leaves.
- **Ordering together** (§10.3) works in groups.
- **A business in a group:** not in this stage. A group can share a business's card and order from it (§10.3), but the business does not join a people group; it chats with the person who orders, in its own conversation.

## 10. Businesses inside people chats

### 10.1 Sharing cards

From any business chat, Discover profile, product, order or delivery: **Share** → pick a person or group. The card is sent as a message (encrypted like any other), carrying what is needed to show it and open it: the business, the product and its price when shared, an order's reference. Opening it reads the live data through the door as the person opening it (a product's current price; an order only if it is theirs to see, else just what was shared).

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

Voice, then video, one-to-one, then group: end-to-end encrypted (WebRTC with keys from the chat's session), through relays (TURN) that cannot read them. Calls need their own infrastructure and cost, a push channel that wakes the phone (VoIP push on iPhone) and app-store review of calling. Planned last; its own short spec when stages 5a and 5b are in use.

## 13. The server

- **Delivery:** a queue per device; messages kept (encrypted) until every device of the recipient has them, then deleted from the server after a short time (proposed 30 days if undelivered). The server keeps no history for people chats: the devices do.
- **Presence and typing:** over the app's live connection, sent only to people allowed to see them (§5.4).
- **Push:** a person's devices (stage 2's push on You), kinds for a person's message, a request, a group; no words in the push.
- **Media:** uploaded encrypted by the sender's device; the server stores ciphertext with a size limit and deletes it after delivery plus a short time. The malware scan businesses' files get cannot run on encrypted media; the receiving app shows files as "from a person" and never opens them automatically.
- **Scale:** the business chat server sized for consumers: more connections, fan-out for groups, rate limits. A load test before stage 5b.
- **Web chat:** a linked device of the phone (§6.2), with keys in the browser's storage; signing out removes them.

## 14. Privacy and data

| What | Who keeps it | Deleted when |
| --- | --- | --- |
| People chats' messages and media | Each person's devices (encrypted); the server only until delivered | The person deletes them; the server after delivery or 30 days |
| Who talked to whom, when | The server, as delivery records | 90 days (proposed), except what a report or lawful hold keeps |
| Contacts sent for matching | Nobody: answered and forgotten | At once |
| Reports | Taskiem's moderators | Per the moderation policy |
| Device keys | The server (public keys only) | With the device or the account |

Deleting an account (stage 3) also deletes the person's people-chat records on the server and their keys; the other people keep their own copies, with the person shown as "Deleted account".

## 15. Stages

Each ships on Android, iPhone and the web together, as before:

1. **5a — One-to-one people chats:** finding people (number, link, contacts opt-in), requests, privacy settings, end-to-end encryption on the phone, linking the web, blocking, reporting, sharing business cards, splitting cards.
2. **5b — Groups and ordering together:** groups, invite links, sender keys, the shared basket.
3. **5c — Calls.**

Before 5a: the encryption choice settled (§17 Q1), the library chosen and reviewed, a security review of the design, the privacy notice and terms rewritten (legal), and app-store listings updated.

## 16. Risks

- **Encryption done wrong** loses people's chats or protects nothing. Mitigation: an audited library, an outside security review, no home-made cryptography.
- **Losing chats with a lost phone** surprises people used to WhatsApp's backup. Mitigation: offer backup at the first people chat, plainly.
- **Abuse by strangers,** including fraud ("send me money for this"). Mitigation: requests, limits, the transfer warning businesses' chats have (§15 of the plan) shown in people chats too, reporting.
- **Nobody comes.** People stay on WhatsApp. Mitigation: build only what businesses make better (sharing, ordering together, splitting) first; measure use before groups and calls.
- **Cost:** consumer-scale delivery, media and calls. Mitigation: keep no server history; delete media after delivery; calls last.
- **Lawful requests and law changes** on encryption in Nigeria. Mitigation: legal review; the transparency note.

## 17. Questions for the founder

1. **Encryption:** end-to-end by default (A), readable by Taskiem like business chats (B), or B with opt-in secret chats (C)? Proposed: A.
2. **Push previews** for people chats: show the sender's name only, or name and words (decrypted on the phone where the platform allows)? Proposed: name and words when the phone can decrypt them, as WhatsApp does; the setting to hide words stays.
3. **Who can message me by default:** everyone as a request, or contacts only? Proposed: everyone, as a request.
4. **Contact matching:** keyed hashes with a rotated key (cheaper; Taskiem could in principle reverse them), or private set intersection (much costlier; Taskiem learns nothing)? Proposed: keyed hashes first, stated honestly in the privacy notice.
5. **Showing numbers:** hide a person's number from people they chat with unless they choose to show it, or show it as WhatsApp does? Proposed: hide by default.
6. **The Chats list:** one list with businesses and people together, or two (Businesses / People)? Proposed: one list, marked.
7. **Group size** at first: 256, or smaller? Proposed: 256.
8. **Money between people:** share cards only (A) plus paying the business for your share (C), or a licensed partner wallet (B)? Proposed: A and C now.
9. **Lawful requests and the transparency note:** agreed with a lawyer before 5a.
10. **Backups:** offered at the first people chat (encrypted with a passphrase), or later? Proposed: at the first people chat.
11. **Web without a phone:** allow people chats on the web for someone with no phone app (keys in the browser only, no linking)? Proposed: no; the web is a linked device.
12. **Calls:** keep them for last (5c), or are they needed to launch? Proposed: last.
