# People chatting with people (stage 5 of Taskiem for everyone)

Status: **plan, settled** (17 November 2026; every question settled the same day, §17; decision [0069](../decisions/0069-people-chatting-with-people.md) accepted the same day). Stage 5 of [Taskiem for everyone](taskiem-for-everyone.md) (§16, §20), under decision [0068](../decisions/0068-taskiem-for-everyone.md), which left this stage "its own plan and decisions". Stage 5a's server (finding people, and one-to-one chats) is built and joined (see the "As built" sections at the end); the rest is not. The founder's answers are in §17.

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

"Find me from contacts" works within "Find me by my number": a contact match is the number found, so with "My contacts" only people the person added find them by matching, and with "Nobody" no one does.

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
- **Shown plainly.** The app never calls people chats end-to-end encrypted. In a people chat's info: "Messages are stored securely by Taskiem. Taskiem does not read your chats with people except to keep you safe: when you report them, to stop spam, or when the law requires it" (wording for legal review, §17 Q13).

### 6.3 What Taskiem does and does not do with people chats

Readable by Taskiem's servers does not mean read. Proposed rules (§17 Q13–Q15):

- **No one at Taskiem reads people chats** except a moderator handling a report of that chat, or under a lawful request (§8.5), each logged.
- **No machine reads them for business features.** Order drafts, translations and reminders, which business chats get, are not run on people chats unless the person turns them on in that chat (§17 Q14).
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

Under Nigerian law (NDPA 2023, the Cybercrimes Act 2015) Taskiem may be required to give data. With B, that can include people chats' words. Taskiem answers only proper legal process, gives the least that is asked, tells the person where the law allows, and publishes how many requests it received. The process and the words in the privacy notice are agreed with a lawyer before 5a (§17 Q9).

### 8.6 Age

The minimum age is 18 (§23 of the plan). People chats do not change that.

## 9. Groups (stage 5b)

- Up to 256 people at first (§17 Q7), with admins who add, remove and choose who can post.
- **Joining:** added by an admin who has them as a contact, or by a group's invite link; people can choose that only contacts may add them to groups.
- **Kept on the server** like one-to-one chats; someone who joins sees the group from when they joined (§17 Q16).
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
- **Retention:** kept until the people delete them or their account (§17 Q12).

## 14. Privacy and data

| What | Who keeps it | Deleted when |
| --- | --- | --- |
| A person's copy of a people chat, with its media | Taskiem, sealed under the person's key | The person deletes the chat or their account (or after the retention in §17 Q12) |
| A group's messages | Taskiem, sealed under the group's key | The group is deleted, or each message per the same retention |
| Who talked to whom, when | Taskiem, with the messages | With them |
| Contacts sent for matching | Nobody: answered and forgotten | At once |
| Reports | Taskiem's moderators | Per the moderation policy |

Deleting an account (stage 3) deletes the person's copies of people chats and their messages in groups' copies are shown as from "Deleted account" (§17 Q17); the other people keep their own copies.

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
- **Cost:** consumer-scale storage, delivery, media and calls. Mitigation: retention (§17 Q12), media size limits, calls last.

## 17. Settled

All settled by the founder on 17 November 2026; decision [0069](../decisions/0069-people-chatting-with-people.md) records them.

| # | Question | Answer |
| --- | --- | --- |
| 1 | Encryption | **B: encrypted on the server, readable by Taskiem's servers**, as business chats. Not end-to-end; said plainly (§6) |
| 3 | Who can message me by default | Everyone, as a request |
| 4 | Contact matching | Keyed hashes with a rotated key, stated honestly in the privacy notice |
| 5 | Showing numbers | Hidden from people chatted with unless the person chooses to show it |
| 6 | The Chats list | One list with businesses and people, each marked |
| 7 | Group size at first | 256 |
| 8 | Money between people | Share cards (Taskiem moves no money), and paying the business for your share when ordering together |
| 9 | Lawful requests | The process in §8.5, agreed with a lawyer, with the privacy notice, before 5a |
| 12 | Retention of people chats | Kept until the people delete them or their account |
| 13 | What Taskiem does with people chats | No human reads them except for a report or a lawful request, each logged; never used to train models or for advertising; in the privacy notice |
| 14 | Business features in people chats | Off (order drafts, translations); opt-in per chat later |
| 15 | Automated safety checks | Yes, on strangers' messages (requests and new chats), keeping only the verdict |
| 16 | Joining a group | See it from when you joined |
| 17 | A deleted account's messages in groups | Kept, shown as "Deleted account" |

Questions 2, 10 and 11 of the first draft fell away with B (push carries the words as the person's setting says; no backup step; the web works without a phone).

## As built: stage 5a, server (finding people)

Built on branch `stage5a-finding`, on 093b2f44 (stages 1 to 4, decision 0069 accepted): the server of §5 (finding people and the privacy settings), presence (§13), the automated checks on strangers' messages (§8.4) and their part of deleting an account (§14). The chats between people, requests and person blocks are stream A (`stage5a-chats`, migrations 00471 to 00474). This stream is migration `00475_people_finding.sql` and the package `engine/people`. The web and app screens are later streams.

**Data (00475).** All are the person's own rows (policy `person_own`: the person in scope, no door open), `ON DELETE CASCADE` from `users`:
- `person_privacy`: the §5.4 settings. No row means the defaults: find by number `contacts`, find from contacts on, message `everyone` (as a request), last seen `contacts`, read ticks on, photo `contacts`, number not shown.
- `person_contacts (person_id, contact_id, name, source: number|link|contacts|chat, added_at)`: at most 5000 a person.
- `person_links`: a person's link, kept as a business's chat link is (`engine/chatlinks`): the code's keyed hash and the nonce it is spelt from, one live code (`person_links_live`), `replaced_at`. Codes use chat links' alphabet and length, with keys of their own.
- `person_presence (last_seen_at, online_until)`.
- `people_find_counts (person, kind: number|contacts|contacts_batch, day, n)`: how many lookups and hashes a person sent each day (Lagos days; older than yesterday deleted as they go). Never what was looked up.
- `people_screen_verdicts (from_person, to_person, verdict: warn|hold, reasons, at)`: written and read by the sender only. Never the words; nothing for `ok`.
- Definers, each in `engine/db/definers.tsv`:
  - `taskiem_people_cards(viewer, owners[])`: what a person may see of up to 500 others; called by the viewer in scope, or about one owner by that owner;
  - `taskiem_people_find_number(viewer, number)`;
  - `taskiem_people_match(viewer, key, hashes[])`;
  - `taskiem_people_photo(viewer?, owner)`;
  - `taskiem_person_link_find(hash)`, for anyone: the person, when replaced, their name, whether their photo is everyone's;
  - `taskiem_people_forget(person)`.
  
  `taskiem_people_allows(rule, owner, viewer)` is their shared rule, not callable by the app. taskiem_dispatch may now read `users.deleted_at` and the new tables' columns (`dispatch_columns.txt`), but still not `users.name` (`TestDispatchRoleCannotReadPayloads`): the name people are shown is `person_names`, a copy of `users.name` written only by a trigger on `users` (filled once from it), which the application cannot read.

**Rules (decisions of this stream):**
- **"My contacts" is the owner's own list**: the people the owner added, whether or not they added the owner back. Mutual contacts are not needed: a person decides who is theirs.
- **Last seen and online are hidden both ways.** A viewer sees an owner's only when the owner's setting lets the viewer see it **and** the viewer's own setting lets the owner see theirs, so "Nobody" means seeing no one's. Online means a heartbeat within 75 seconds (`OnlineFor`).
- **Read ticks** go between two people only when both have them on.
- **The photo** follows the photo setting alone (not both ways). The public (a person's link, signed out) sees it only when it is "Everyone".
- **The number** is shown only when its owner chose to (`show_number`), to someone they chat with (stream A's `Chatting`) or added, never across a block.
- **Who can message me.** `everyone` allows a first message, as a request unless the recipient added the sender. `contacts` allows only people the recipient added (not a request). `nobody` refuses every new chat. Chats the person accepted or started go on: MayMessage is asked only for a chat its recipient has not accepted.
- **A block** (stream A's) hides the photo, last seen, online and number, and hides the person from lookups and matching, both ways.
- **Finding by number** answers the same for a number that is nobody's and one whose person hides from the finder: "Not on Taskiem yet", with the words to invite them (the finder's own link and where to get the app). The app sends them from the phone; Taskiem sends nothing.
- **Adding a contact** needs proof that the person was found. Every lookup, link and match answer carries an `add_token` (an HMAC of the finder, the person and an expiry a day away), so person ids alone cannot be collected into contacts. Stream A adds `chat` contacts itself (`AddContact`).
- **Contact matching** (§5.3):
  - The app hashes each number as `SHA-256(key ‖ "taskiem-contacts:" ‖ E.164)`, 64 hex digits, with the week's key (`k{week}`, derived from the platform key `TASKIEM_MESSAGE_HASH_KEY`). Last week's key is still answered for 48 hours after it changes.
  - The server hashes the verified phones with the same key in one query and answers the people who let themselves be found from contacts, never the asker (and, since the review fixes, only those whose "Find me by my number" lets the asker find them). It keeps nothing of the request: a test counts every row of every table before and after.
  - Limits: 500 hashes a request; 5000 hashes (1000 in an account's first week, since the review fixes) and 20 requests a day per person; 30 requests per address an hour.
  - **The honest limit**, in the words the key comes with: Taskiem holds the key, so this protects a person's contacts if what is sent is seen by someone else, not from Taskiem itself.
  - The server hashes every verified phone for each request (a scan of `user_phones`). A per-week table of hashes is the next step when that grows slow.
- **Limits on lookups**: 50 numbers a day per person, counted whatever the answer (so retrying does not help); and, in memory per server, 10 at once per person, 30 at once and 300 a day per address.
- **The checks on strangers' messages** (`Screen`, `ScreenStranger`):
  - What they look for: the transfer warning's rule (moved from `api/transfer_warning.go` to `people.AsksForTransfer`, which the business chats still use); requests for money ("send money to this account", "pay into my account"); prizes ("you have won", "claim your prize", lottery); fees to release something ("processing fee"); requests for a code, PIN, BVN or card; and "double your money" investments.
  - Any one gives `warn`. `hold` for a request for a code, for money asked with a prize, fee or investment, or for such bait with a link.
  - A `warn` becomes `hold` when the sender already had three warned or held messages to strangers in the last day.
- **Deleting an account** removes the person from every other person's contacts, rather than leaving a "Deleted account" in their lists that could never be messaged. Their settings, contacts, link, presence, counts and verdicts go, and verdicts about messages to them no longer name them. By id (`GET /v1/people/{id}`) they are `deleted: true`, "Deleted account", for stream A's chats and groups. `account.delete` is audited with `contacts_left`.

**For stream A** (`engine/people`; `*people.People` implements `people.PeoplePolicy`). At merge, set `srv.People.Blocked` and `srv.People.Chatting` (`cmd/taskiem/serve.go` makes `srv.People`; tests set it):

```go
type People struct {
    Pool *pgxpool.Pool; Key []byte; Base string
    Blocked  func(ctx context.Context, a, b uuid.UUID) (bool, error) // either blocked the other
    Chatting func(ctx context.Context, a, b uuid.UUID) (bool, error) // a people chat no longer a request
    Now func() time.Time
}
func (p *People) MayMessage(ctx context.Context, from, to uuid.UUID) (people.Decision, error) // {Allowed, AsRequest, Refused: self|gone|blocked|contacts|nobody}
func (p *People) MayMessageTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (people.Decision, error) // tx in from's (or to's) scope
func (p *People) IsContact(ctx context.Context, a, b uuid.UUID) (bool, error)          // a added b
func (p *People) NumberShownTo(ctx context.Context, owner, viewer uuid.UUID) (string, error) // "" when not shown
func (p *People) ReadTicks(ctx context.Context, a, b uuid.UUID) (bool, error)
func (p *People) ScreenStranger(ctx context.Context, from, to uuid.UUID, text string) (people.Verdict, error) // {Verdict: ok|warn|hold, Reasons}
func (p *People) Resolve(ctx context.Context, code string) (uuid.UUID, error) // a person's link; people.ErrLinkReplaced, ErrLinkNotFound, ErrOff
func (p *People) CardOf(ctx context.Context, viewer, owner uuid.UUID) (people.Card, error)
func (p *People) CardsTx(ctx context.Context, tx pgx.Tx, viewer uuid.UUID, owners []uuid.UUID) ([]people.Card, error) // tx in viewer's scope
func (p *People) AddContact(ctx context.Context, person, contact uuid.UUID, name, source string) error // source people.FromChat on accepting
func (p *People) Touch(ctx context.Context, person uuid.UUID) error // the live connection: here now
func (p *People) Away(ctx context.Context, person uuid.UUID) error  // the live connection closed
func ForgetTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (int64, error) // deletePerson calls it already
```

**Endpoints.** They are in the OpenAPI document under You, and `mobile/api.txt` and `api.g.dart` are regenerated. Each is reachable from a personal session and from a business session for its person; API keys get 403.

| Endpoint | Body | Answer |
| --- | --- | --- |
| `GET`, `PUT /v1/me/privacy/people` | `{find_by_number?, find_from_contacts?, message?, last_seen?, read_ticks?, photo?, show_number?}` (audiences `everyone`, `contacts`, `nobody`) | `PeoplePrivacy`; 400 for an audience that is not one |
| `GET /v1/me/contacts` | | `{contacts: [PersonContact]}`, by the name shown |
| `POST /v1/me/contacts` | `{person_id, add_token, from: number\|link\|contacts, name?}` | 201 `PersonContact`; 403 `{add_token: true}`; 400; 404 for a deleted account; 503 with no key |
| `PUT /v1/me/contacts/{person}` | `{name: string\|null}` | 200 `PersonContact`; 404 |
| `DELETE /v1/me/contacts/{person}` | | 204; 404 |
| `POST /v1/people/lookup` | `{phone}` | `{found: true, self?, person: PersonCard, add_token}` or `{found: false, invite: {words, link}}`; 400 `phone_invalid`; 429 `{limit: day\|rate}` |
| `GET /v1/people/{id}` | | `PersonView` (`PersonCard` and `number`); 404 |
| `GET /v1/people/{id}/photo` | | the image when the photo setting allows; 404 |
| `GET /v1/people/links/{code}` | | `PersonFound` with `add_token` (none for one's own link, which has `self`); 410 `{gone}`; 404 |
| `GET /v1/me/link`, `POST /v1/me/link` (a new one) | | `{code, url, created_at, words, qr: "/v1/me/link/qr"}`; 503 with no key |
| `GET /v1/me/link/qr[?format=png]` | | SVG or PNG |
| `GET /v1/me/contacts/key` | | `{key_id, key (base64), expires_at, hash, max_batch: 500, max_per_day: 5000, words}` |
| `POST /v1/me/contacts/match` | `{key_id, hashes: [64 hex]}` (at most 500) | `{matches: [{hash, person: PersonCard, add_token}]}`; 409 `{key_changed}`; 400; 429 |
| `POST /v1/me/presence` | `{online}` | 204. The heartbeat, about every 30 seconds; `false` on going to the background |
| `GET /v1/person-links/{code}` (anyone) | | `{code, link, name, photo: {url, updated_at}\|null, app?}`; 410 `{gone}`; 404 |
| `GET /v1/person-links/{code}/photo` (anyone) | | the image when it is everyone's; 404 |
| `/p/{code}` | | The server's own page where no web app is served (as `/m/{code}`): the name, the photo when it is everyone's, **Chat on Taskiem** (`/login?next=/p/{code}`) and **Get the Taskiem app**; 410 "This link no longer works"; 404. `/p/*` is in `appPaths` |

`PersonCard` is `{id, name, deleted, contact_name, in_contacts, they_added_you, photo: {url: "/v1/people/{id}/photo?v=…", updated_at}|null, last_seen_at|null, online|null, read_ticks}`; `name` is "Deleted account" for a deleted one.

**Tests.**
- `engine/people/people_db_test.go`:
  - `TestSettingsDefaultsAndChanges`, `TestContacts`;
  - `TestFindByNumber`: each audience, the same answer for hidden and no one, blocks, oneself, deleted accounts, the add token's person and day, 50 a day counted whatever the answer;
  - `TestPersonLink`: read back, replaced, 410 then nothing after 30 days, the public photo, deleted accounts, no key;
  - `TestMatchContacts`: only allowed people, never the asker, another key matches nothing, the grace, nothing written but the counts, batch and day caps;
  - `TestPresence`: both ways, away, nobody, everyone, blocks;
  - `TestPolicy`: MayMessage, NumberShownTo, ReadTicks;
  - `TestScreenStranger`: verdicts, the repeat hold, no words kept, the sender's own only;
  - `TestForget`.
- `api/people_test.go`: `TestPeoplePrivacySettings`, `TestPeopleLookupAndContacts`, `TestPersonLinkAndPage`, `TestContactMatching`, `TestPeoplePresence`, `TestDeleteAccountForgetsPeople`.
- `engine/db`: `TestPeopleFindingMigrationUpDownUp`; the definer, dispatch column and table checks cover 00475.

**Left for later or for others:**
- Stream A:
  - person blocks (`Blocked`) and chats (`Chatting`);
  - calling `MayMessage` and `ScreenStranger` for requests and new chats;
  - `Touch` and `Away` on its live connection;
  - adding `chat` contacts on accepting;
  - showing "Deleted account" in its chats.
- The web's and the app's screens:
  - You → Privacy (people), Contacts, Find by number with Invite, the link and its QR;
  - the contacts permission with its words, and the heartbeat;
  - the app's `appPageOf` for `/p/{code}`, and the web's own `/p/{code}` page where it serves the web app.
- Presence from any request: only the heartbeat, and stream A's connection, record it.
- A per-week hash table for contact matching at scale (above). The limits per address are per server.
- The privacy notice's words on contact matching and the checks (§15, with the lawyer), and the moderators' view of held messages and verdicts (§8.3, §8.4).

## As built: stage 5a, server (chats)

Built on branch `stage5a-chats`, on 093b2f44 (stages 1 to 4 merged; decision 0069 accepted): the server of §7, §8.1 to §8.3, §10.1, §11 A, §13 and §14 for one-to-one chats. Finding people (§5: by number, a person's link, contacts), the privacy settings (§5.4), presence and last seen, and the automated stranger checks are stream B's (`stage5a-finding`, migrations 00475 and after); this stream's migration is `00471_people_chats.sql` alone (00472 to 00474 are unused). The web and app screens are later streams; the API is written for them.

**People chats are kept by Taskiem, sealed at rest under each person's own key and readable by Taskiem's servers. They are not end-to-end encrypted, and nothing in the API says they are.**

**Data (00471).**
- **Each person's copy**: `people_chats` (the person, the other person, the conversation, who started it, `request_at` for a request waiting for this person, `waiting_at` for this person's own first messages waiting, `accepted_at`, `muted`, `archived`, `hidden_at` for deleted for me), `people_messages` (the same id on both copies; `mine`; `kind` text, media, card or location; `sealed`; `attachment` {kind, type, size}; `card_type`; `reply_to`; `forwards`; `client_id`; `delivered_at`; `read_at`; `starred_at`; `my_reaction`; `their_reaction`; `deleted_at`; `screen`; `paid_at`; `paid_by_me`) and `people_files` (a file's bytes, sealed). The conversation id is the pair's (`taskiem_people_pair`, checked on every copy), so neither copy reads the other to name it.
- **Sealing per person** (`engine/peoplechats`): each person has a 32-byte data key in `person_keys`, sealed with the platform's root key for that person alone (`Vault.SealPlatform`, purpose `person-key:{id}`); opened keys are kept in memory five minutes. A message's words, card, place and file name are one AES-GCM value bound to its owner and its row (`m:{id}`), a file's bytes another (`f:{id}`), the saved payout account a third. A copy of the database opens nothing; deleting the account deletes the key.
- **Scope**: every table is the person's own (`person_own`: `taskiem_person_scope()`, and never with a business's door open: `taskiem_person_business()` null). The one crossing is `taskiem_people_reach(chat)`: from a person's own transaction with no business in scope, for one of their own copies, it puts that one conversation of the other person's copy in reach (`app.people_reach`, `app.people_peer`) to read, write and update, never to delete (but a file of a message deleted for everyone); with the other's key (read, and made the first time) and their block of this person (read). It refuses (`P0002`, hint `gone`) when the other account is no longer active. A business's transaction reaches nothing.
- `people_blocks` (the person's own; the other reads only whether they are blocked, through the reach); `person_payout_accounts` (sealed); `people_reports` (insert only, as the reporter); `people_requests`, the requests' ledger (sender, recipient, sent, outcome `waiting`, `accepted`, `deleted` or `blocked`), read and written only by definers; `person_profiles`, a person's name and whether the account is active or deleted, kept from `users` by a trigger, since the dispatch role may never read `users.name` (`TestDispatchRoleCannotReadPayloads`).
- **Definers** (in `engine/db/definers.tsv`): `taskiem_people_reach`; `taskiem_people_writable` (an active account other than the asker); `taskiem_people_names` (names of the people the asker has a copy with; a deleted account's empty, `gone`); `taskiem_people_phone` (the number of someone the asker chats with: the API asks the policy first); `taskiem_people_request_mark`; `taskiem_people_request_counts` (the asker's own, as counts); `taskiem_people_card_enter` (the card door, below). `taskiem_person_delete` also ends the person's requests.
- **Pushes**: `push_outbox` takes the kinds `person` and `request` on the person's side (a person's device takes them as it takes `you`); `person_notify` gains `people` and `requests`.

**Who may message whom: the interface for stream B.** `engine/peoplechats.Policy`:

```go
MayMessage(ctx, from, to) (allowed, asRequest bool, err error) // asked only when the two have no accepted chat
IsContact(ctx, a, b) (bool, error)                             // b is in a's contacts
NumberShownTo(ctx, owner, viewer) (bool, error)                // owner's number shown to viewer
ScreenStranger(ctx, from, to, text) (Verdict, error)           // Allow; Warn (delivered, marked warn); Refuse (403 with Reason)
```

- `peoplechats.Stub` is the default: everyone may write, always as a request; nobody is a contact; numbers are hidden; nothing is screened.
- `peoplechats.LinkResolver.ResolvePersonLink(ctx, code) (person, error)`, with `ErrLinkUnknown` (404) and `ErrLinkReplaced` (410 `link_replaced`); `NoLinks` until B's lands.
- **Wiring:** set `Server.People` and `Server.PersonLinks` in `cmd/taskiem/serve.go`, beside `ChatCodes`; nil keeps the stub. `TestPeoplePolicy` shows a policy wired in: contacts write straight to chats, "nobody" refuses, numbers shown by choice, a warned and a refused message.

**Endpoints** (in `api/openapi/openapi.yaml`, tag **People**; the app's in `mobile/api.txt`, `mobile/lib/api/api.g.dart` regenerated). A personal session reaches all of them (`peoplePersonRoutes`), and so does a business session for its person; an API key is refused (403).

| Endpoint | What |
| --- | --- |
| `GET /v1/me/conversations?archived=1` | **You's one Chats list** (§17 Q6): `{chats: [ChatListItem], unread, requests}`, business and people chats together, newest first, each marked `kind: business` (open `/v1/me/chats/{id}`) or `person` (open `/v1/me/people/{id}`). **Clients use this for the Chats tab**; `GET /v1/me/chats` stays the business chats alone |
| `GET /v1/me/people?archived=1` | People chats: `{chats: [PeopleChat], unread, requests}` |
| `POST /v1/me/people` `{person_id \| link_code}` | Start (201) or the one you have (200), with `kept`. 404 no such person; 403 not taking messages; 410 `link_replaced`; 429 new chats (10 an hour, 30 a day; half in the first week) |
| `GET /v1/me/people/requests` | Requests waiting for you |
| `GET /v1/me/people/blocked` | `{people: [{person_id, name, chat_id, at}]}` |
| `GET`/`PUT`/`DELETE /v1/me/people/{id}` | One chat (`contact`, `phone` only when shown, `kept`); `{muted?, archived?}`; delete for me (on a request: Delete) |
| `POST /v1/me/people/{id}/accept`, `…/decline` | Accept (read ticks shown from then on); Delete a request (the sender is not told) |
| `POST /v1/me/people/{id}/block` `{report?}`, `DELETE …/block` | Block (and report), unblock |
| `POST /v1/me/people/{id}/report` `{reason, note?, consent: true}` | The last 20 messages of your copy, opened, to Taskiem's moderators |
| `POST /v1/me/people/{id}/read` | Read marks |
| `GET /v1/me/people/{id}/messages?before=&starred=1` | Your copy, 50 a page |
| `POST /v1/me/people/{id}/messages` `{text?, client_id?, reply_to?, location?, card?}` | Write (201; a repeated `client_id` 200) |
| `POST /v1/me/people/{id}/files` (multipart `file`, `text`, `client_id`, `reply_to`); `GET …/files/{message}` | A photo, voice note or file; your copy of it |
| `PUT /v1/me/people/{id}/messages/{message}` `{starred?, reaction?}` | Star (yours); react (both copies; `""` removes it) |
| `DELETE /v1/me/people/{id}/messages/{message}?for=me\|everyone` | Delete for me, or for everyone within 24 hours |
| `POST /v1/me/people/forward` `{chat_id, message_id, to: [chat ids]}` | Forward: `{sent: [{chat_id, message}], failed: [{chat_id, error}]}` |
| `POST /v1/me/people/{id}/messages/{message}/paid` `{paid}` | Mark a share paid, either side |
| `GET /v1/me/people/{id}/messages/{message}/card` | `{card, live: {yours, product?, business?, order?, delivery?}}` |
| `GET`/`PUT`/`DELETE /v1/me/payout-account` | `{account: {bank, account_number, account_name} \| null, checked: false, words}` |
| `GET`/`PUT /v1/me/notifications` | Gains `people` and `requests` |

- `PeopleMessage`: `id, mine, kind (text|media|card|location), body, card?, location? {lat, lng, label?}, attachment? {kind, type, size, name}, reply_to?, forwarded?, forwarded_many?, status? (sent|delivered|read; your own), starred?, my_reaction?, their_reaction?, deleted?, warn_transfer?, warn?, read_at, created_at`.
- `PeopleChat`: `id, kind: person, person_id, name ("Deleted account" when gone), gone, last, last_mine, last_at, unread, muted, archived, request, waiting, blocked, contact?, phone?, kept?`.
- `PeopleCard`: `type (product|business|order|delivery|split), title, business_id?, business?, area?, item_id?, price_kobo?, reference?, state?, delivery?, amount_kobo?, what?, pay_to?, paid?, paid_at?, paid_by_me?`.
- `appPaths` gains `/you/people/*`, so a push's link opens the app where it is installed.

**How it behaves.**
- **A message** is kept in the sender's copy and, unless the other blocked them, in the other's (made the first time), in one transaction, each sealed under its owner's key. `status` is `sent` (one tick) while undelivered, then `delivered`, then `read`. A message into a request, and a new chat's messages until the other has written, go past `ScreenStranger` first.
- **Requests.** A message is direct when the other's copy is an accepted chat, or the two accepted one before; otherwise `MayMessage` decides (the stub: a request).
  - A request waits in the other's Requests; reading it tells the sender nothing.
  - Accept (or writing back) moves it to their chats, clears the sender's `waiting` and shows the read marks made meanwhile.
  - Delete removes the other's copy without telling the sender; a later message is a request again.
  - Each new request is in the ledger. A sender may have `peoplechats.RequestAllowance(newWeek, accepted, refused)` waiting at once and sent in a day: 5 and 10 (2 and 3 in the first week), plus half the accepted (up to 15) and the accepted (up to 40); a third of that (at least 1) when three or more were deleted or blocked and they are more than half of those decided. Past it: 429 "You have many messages waiting…".
- **Limits** (§8.2): 20 messages a minute in a chat, 10 new chats an hour and 30 a day (half in the first week). A message is forwarded to at most 5 chats at once, and one forwarded five times or more on its way (`forwarded_many`) to one at a time (429 with `forwarded_many`). A forwarded file is copied and sealed again for each.
- **Blocking** (§8.1): the blocked person's messages stay on their side with one tick, and nothing else tells them (their chat shows nothing, and there is no push). Their reactions, read marks and deletes for everyone do not reach the blocker, and the blocker's number is never shown to them. Blocking a request removes it (`removed`), keeps the copy hidden so the block is listed by name, and records `blocked` in the ledger. Unblocking delivers nothing sent meanwhile.
- **Reports** (§8.3): with `consent`, the last 20 messages of the reporter's copy, opened (words, cards, places, what each file was and its name), the person reported, and whether it came from a request, in `people_reports`. Twelve every two hours, shared with reports of businesses.
- **Delete for everyone** (within 24 hours): both copies keep only that it was (the sealed value, attachment, file, reactions and star go), shown "This message was deleted".
- **Push** (§13): to the other's own devices when delivered and their chat is not muted.
  - Kind `request` ("Message request: …", link `/you/people/requests/{chat}`) or `person` (their words, link `/you/people/{chat}`); title the sender's name; tag `person:{chat}`.
  - A device's preview off says "Taskiem" and "You have a new message request" or "Someone wrote to you".
  - None when the person turned `requests` or `people` off, and none for a blocked sender. The badge counts people chats with something unread too.
- **The transfer warning** (§8.4): `warn_transfer` on the other's text messages, as in business chats (`warnTransfer`); cards never warn.
- **Cards** (§10.1) are made by the server from what the sharer can see:
  - a product of a business's published shop, or a business, through the **card door** (`taskiem_people_card_enter`: the person in scope, no other door open, an active business with a published shop or a Discover listing shown, that has not blocked them; audited `customer.card` in the business's chain);
  - one of the sharer's own orders (Orders on You's row: business, reference, state), or its delivery once a rider holds it (through the order door).
  - Opening a card reads it live as the person opening it: the product's current price and stock, the business's card, an order only when it is theirs (`yours`); otherwise only what was shared. 403 `blocked` when that business blocked them. Looks are limited per person, as looks at orders are.
- **Share cards** (§11 A): `{type: split, amount_kobo, what, pay_to?}`, with the account as typed, else the person's saved payout account (sealed, never checked by Taskiem, which moves no money). Either side marks it paid; it is not forwarded.
- **Account deletion** (§14): the person's copies, files, blocks, saved account and key go, with their requests. The other person's copy keeps the messages, shown as from "Deleted account" (`gone`), and takes no more (409).

**Deviations from the plan.**
- **Read ticks** are always shown once a chat is accepted: §5.4's "Read ticks off" is stream B's setting, not asked yet.
- **Typing, presence and last seen** are not built here (stream B).
- **People's photos** are not shown in people chats (§5.4's "Profile photo" is stream B's setting).
- A delivery card needs a rider holding the order; before that, the order is shared.
- A message from someone unblocked, into a request that was blocked, is not a new request in the ledger.

**Tests.** `api/people_chats_test.go`:
- `TestPeopleChats`: start; a request; nothing told before accepting; both copies sealed apart; accept and read ticks; a reply; a repeat; reactions and stars; the transfer warning; a place; a photo scanned, checked and sealed; malware refused; delete for everyone and for me; 24 hours; mute and archive; the one list with a business chat; deleting a chat for me.
- `TestPeopleChatsIsolation`: a third person reaches nothing by the API or the database; the reach refuses another's chat and a business's transaction; a door open hides every people row; the reach never deletes the other's copy; a business session sees only its own person's; an API key refused.
- `TestPeopleRequestsAndLimits`: two waiting in the first week; a decline not told and counted; more after accepting; fewer after many refusals; writing back accepts; a request again after a decline; 10 messages a minute and 5 new chats an hour in the first week.
- `TestPeopleBlocksAndReports`: block and report from a request with 20 messages; one tick; nothing told; unblocking delivers nothing sent meanwhile.
- `TestPeopleForwards`, `TestPeoplePush` (kinds, words, links, preview off, settings, mute, block), `TestPeopleCards` (a product and a business opened live and audited; an order opened by someone else showing only what was shared, and by its owner live; a delivery without a rider; a business that blocked the opener; share cards with a typed and a saved account, marked paid on both sides), `TestPeopleAccountDeletion`, `TestPeoplePolicy`.
- `engine/peoplechats`: `TestSealIsBoundToItsOwnerAndRow`, `TestRequestAllowance`. `engine/db`: `TestPeopleChatsMigrationUpDownUp`; the definers, dispatch columns and table checks cover 00471.

**Left over.**
- The web's and the app's screens: the one Chats list, a people chat, Requests, blocked people, cards and share cards, the payout account, Notifications' two new switches, and the push kinds' channels (`person`, `request`) and `/you/people/*` in the app's own router.
- Moderators' screens for `people_reports` (and stage 2's `customer_reports`).
- Stream B's policy and link resolver wired in `serve.go`; the read-ticks setting.
- Search across a person's own chats (§6.2), and starred messages across chats (today per chat).

## As built: stage 5a, the two streams joined

Branch `stage5a-joined`: stream B (finding people, 989edad4, on 093b2f44) with stream A (people chats, `stage5a-chats`) merged into it, and the seams between them made one. Migration `00476_people_joined.sql`; the join in Go is `engine/peoplechats/join.go` and `api/people_join.go`. The sections above are as each stream left them; where they differ from this one, this one holds.

**One table of names.** Each stream kept its own trigger-made copy of `users.name` so that `taskiem_dispatch` never reads `users.name` (`TestDispatchRoleCannotReadPayloads`): A's `person_profiles` (name, active, deleted) and B's `person_names` (name). `person_profiles` serves both, so 00476 keeps it alone:
- `taskiem_people_cards` and `taskiem_person_link_find` read `person_profiles`; `person_names`, its trigger `person_name_sync` and `taskiem_person_name_sync()` go. One trigger on `users` (`users_person_profile`, on name, status and deleted_at) remains.
- `person_profiles` takes `person_names`' narrower grant: `taskiem_dispatch` may update only the columns the trigger writes (name, active, deleted, updated_at); the application still reads nothing of it.

**Blocks and chats for finding people.** `taskiem_people_between(others[])` (in `definers.tsv`) answers, for the person in scope (no business in scope) and up to 500 others, whether either blocked the other and whether a chat between them is no longer a request (either copy accepted); nothing else. `taskiem_dispatch` may read `people_blocks (person_id, blocked_id)` and `people_chats.accepted_at` for it. `peoplechats.JoinPeople(p)` sets `People.Blocked` and `People.Chatting` from it (each in the scope of the person the request is for), and a new `People.BlockedSet` reads many at once in the caller's transaction (cards, contact matching), so a list of 200 chats is one query, not 200. B's `NumberShownTo` now asks about the viewer first, so the request is always answered in the viewer's scope. `cmd/taskiem/serve.go` calls `JoinPeople(srv.People)`; the server's keyless fallback is joined too.

**Who may message whom.** `Server.ChatPolicy` (A's `Server.People`, renamed: B's `Server.People` is the `*people.People`) and `Server.PersonLinks` are left nil in `serve.go`; nil means `peopleJoin` over `People` (`api/people_join.go`). Tests may set another policy. `peoplechats.Stub` and `NoLinks` stay for that.
- `MayMessage`: B's `Decision` as `(Allowed, AsRequest)`. A block is never a 403: the chats keep the message on the sender's side with one tick and tell them nothing, as A does for blocks. (Since the review fixes, the decision is the one the settings make as if there were no block, so nothing else differs either: "Review fixes" below.)
- `ScreenStranger`: `ok` → Allow, `warn` → Warn (delivered with `warn: true`), `hold` → Refuse (403, "This message was not sent: it looks like a scam…"). B keeps the verdict either way.
- `NumberShownTo`: B's number, as whether one is shown; the chat still reads it through `taskiem_people_phone`.
- `ResolvePersonLink`: B's `Resolve`; `ErrLinkReplaced` → 410 `link_replaced`; not found, no such account or a server without the key → 404.

**Read ticks** follow B's `ReadTicks(a, b)` (both have them on), not "always once accepted": a read is not passed to the sender's copy while either has them off (on reading, and on accepting a request), and the sender's own messages show `delivered` with no `read_at` while either has them off, so a read made earlier is hidden too. A read made while off is not told when they are turned back on.

**The other person's card.** `PeopleChat` and You's `ChatListItem` gain `person` (`PersonCard`, from B's `CardsTx` in the viewer's transaction): the name the viewer gave them, the photo by the photo setting, last seen and online by both settings, whether read ticks go between them. None once the account was deleted. A chat's `name` is the name the viewer gave them in contacts, else their own. The number stays `phone` (by `NumberShownTo`).

**Presence.** Neither stream has a person's live connection (the chat stream is the business Chat page's), so presence comes from the app's heartbeat (`POST /v1/me/presence`, which also says when it goes to the background: `Away`) and, now, from using the people chats: every `/v1/me/people…` and `/v1/me/conversations` request `Touch`es the person, at most every 30 seconds per server (`peopleHere`).

**Contacts from chats.** Accepting a request, or writing back to one, adds the sender to the accepter's contacts (`AddContact(…, people.FromChat)`, after the transaction commits; the accept answer's `contact` says so). Only the accepter's: the sender's contacts are theirs to choose. A full list or a deleted account leaves the contacts as they were.

**Deleting an account** runs once, in one transaction in the person's scope, in this order: `taskiem_person_delete` (stages 1 to 4, and A's ending of their requests), `people.ForgetTx` (B: they leave every other person's contacts; their settings, contacts, link, presence and verdicts go), then the person's own people chats, files, blocks, payout account and key (A), the rest of their side, and the tombstone, whose update marks `person_profiles` deleted. Every view then says "Deleted account": the other's chat and the one list (A), `GET /v1/people/{id}` (B), and a deleted account has no card.

**Tests.** `api/people_joined_test.go` `TestPeopleJoined`: a stranger's message asking for a code held (403, nothing delivered) and an account number delivered with `warn`, both verdicts kept; accepting adds a `chat` contact; read ticks shown, then hidden with either side's off (and a read while off not told later); the card's last seen, online and photo hidden until both have each other as contacts, last seen "Nobody" hiding them again and the number shown by choice; "Nobody" refusing a new chat; "My contacts" letting only an added person write, straight to the chats; a block made before any chat answering one tick and nothing more; a chat from a person's link, 410 for a replaced one and 404 for an unknown one; writing back adding a contact; deletion showing "Deleted account" in the chat, the one list and by id, and leaving the other's contacts. `engine/db`: `TestPeopleJoinedMigrationUpDownUp`. A's `TestPeopleBlocksAndReports` now sends words the checks let through (its 20 "send me money" messages were held by the repeat rule, which the stub never had).

**Left over.**
- The screens (web and app) both streams list; the app's `appPageOf` for `/p/{code}` and `/you/people/*` in its router.
- A held message is refused, not kept for moderators (§8.4's "held for review"): there is no moderators' queue yet.
- Typing indicators.

### Review fixes

Branch `stage5a-fix-server`, on c7b0d9ff (stage 5a joined with the web and the app): the server findings of the stage 5a review, each with a Go test. Migration `00477_people_review_fixes.sql`. Where this list differs from the sections above, it holds.

**Two people writing at once.** Each copy was locked by its own owner's transaction, which then updated the other's: two people writing to each other at once deadlocked (half of 30 sends at once failed with 500). Every writer that touches both copies of a conversation (a message, accept, decline, read, a reaction, delete for everyone, mark paid, block, and forward, which sends) now takes the conversation's lock first (`pg_advisory_xact_lock` on the conversation id, in `peopleCopyTx`'s writer path), then its own copy, then the other's. `peopleTx` runs such a transaction again (three times in all) on a deadlock (40P01) or a serialization failure (40001), as a backstop. Test: `TestPeopleSendAtOnce` (15 each at once, none fails, each person's in the order sent; without the lock it fails as the review found).

**One connection a request.** Inside a send's transaction, finding people opened connections of its own (MayMessage, the block and chat checks, ScreenStranger, read ticks, IsContact, NumberShownTo, FindByNumber's block), so a busy pool could starve. Every answer is now read in the caller's transaction: `People.MayMessageTx`, `NumberShownToTx`, `ReadTicksTx`, `TheyAddedTx`, `ScreenStrangerTx`, `people.IsContactTx`, and `People.ChattingTx` and `BlockedSet` (set by `JoinPeople`) under them; the API asks them through `txPolicy` (a test's policy without them is asked outside, as before). The checks' verdict is kept in the send's transaction, so a send that fails keeps none and does not count towards RepeatHold; a held message commits its verdict alone (nothing else is written before the checks) and answers 403 after. Test: `TestPeopleSendOnASmallPool` (20 first messages at once on a pool of 4).

**A block cannot be detected.** `MayMessage` decides by "who can message me" and contacts as if there were no block, and says the block apart (`Decision.Blocked`; `RefusedBlocked` is gone). A blocked sender is answered exactly as anyone: 403 where the settings refuse, `waiting` only where it is a request, the same request limits (a new request is in the ledger whether or not it is delivered, so 429 comes as for anyone). Their message stays on their side with one tick, as before. Test: `TestPeopleBlockNotTold` (Nobody, My contacts not added and added, everyone, added, a held message, and a first week's third request, each for a blocked and an unblocked sender).

**Nothing crosses a block.** Delete for everyone leaves the copy of someone who blocked the person alone, as reactions and read marks do. Test: `TestPeopleDeleteForEveryoneStopsAtABlock`.

**The checks read cards and places.** A stranger's share card (its `what`, and the account it names) and a place's label go past the checks as words do (cards of products, businesses and orders are made by the server and carry no words of the sender's). **Share cards are allowed in requests, and always warned**: a share card naming an account, from someone the recipient has not added to their contacts, is marked on the recipient's copy (`screen = 'transfer'`) and shows `warn` and `warn_transfer`, request or not. Test: `TestPeopleChecksReadCardsAndPlaces` (a held place, a warned share card in a request, a held one asking a fee for a prize, none once added, warned again once removed from contacts).

**Mark as paid** reaches the other's copy only when neither copy is a request and neither blocked the other, so a request's sender learns nothing (and their own mark does not reach the recipient while it waits). Its answer, and a repeated `client_id`'s, hide read ticks as the rest do (`hideRead`). Tests: `TestPeoplePaidOnARequest`, `TestPeopleReadTicksEverywhere` (also: reading a request tells its sender nothing, as the app may now call `/read` on one).

**Forwarding** to one chat at a time applies to a message shown `forwarded_many` (forwarded five times or more on its way to the person), as the mark says: one forwarded four times goes to five chats. Test: `TestPeopleForwards`.

**Limits counted one after the other.** A send that makes a new request or goes past the checks takes a lock on its sender (after the conversation's) before counting, so first messages sent at once stay within the request allowance and RepeatHold. Test: `TestPeopleLimitsAtOnce` (a first week's five at once: two sent, three 429; five warned at once: three sent, two held).

**A friend stays a friend.** Whether the other has written to the person is `people_chats.peer_wrote_at` on the person's copy (set when the other's message arrives, filled from the messages by 00477), not the messages of their copy, which "delete for me" empties. Test: `TestPeopleFriendAfterDeleteForMe`.

**A request whose recipient changed their mind.** While the person's earlier message waits in the other's Requests, `MayMessage` is asked again; refused now ("Nobody", or "My contacts" without them), the message stays on the sender's side with one tick, as a blocked one, and nothing else tells them. If the recipient added the sender meanwhile, it stays a request until accepted. Test: `TestPeopleRequestAfterPrivacyChange`.

**The lists page.** `GET /v1/me/conversations` and `GET /v1/me/people` answer 200 chats a page, newest first (then by id), with `more` and, when there are more, `next`, which `?before=` takes for the page after (opaque; 400 for anything else). `unread` counts every chat of the list (archived or not, as asked), not the page. Test: `TestPeopleListsPage` (205 chats, ties of time, the unread one on the second page).

**A business's photo in the one list.** A business row of `ChatListItem` has `photo: {urls}` (as Orders on You's `OrderPhoto`: its logo by `taskiem_business_photo`'s rule, through the new definer `taskiem_customer_business_photos`), none while the business is not taking the person's messages. Test: `TestPeopleListsEmptyChatsAndPhotos`.

**Empty chats.** A chat opened (`POST /v1/me/people`, from a link or a number) is `empty` until someone writes in it, and the lists leave it out until then (it opens by id as before; requests are never empty). Test: `TestPeopleListsEmptyChatsAndPhotos`.

**A suspended account is not a deleted one.** `taskiem_people_reach` answers hint `suspended` for an account that is not active and not deleted, and a message to it answers 409 "This account can't get messages right now."; `taskiem_people_names` answers its name with `suspended`, and `gone` only once deleted. `PeopleChat` and `ChatListItem` gain `suspended` (the name shown, no card, no number). A deleted account stays "Deleted account". Test: `TestPeopleSuspendedAccount`.

**Contact matching follows "Find me by my number".** A match is a number found, so `taskiem_people_match` answers a person only when their "Find me by my number" lets the asker find them, as well as "Find me from contacts": Nobody matches no one, My contacts (the default) only people they added. An account in its first week sends 1000 hashes a day, not 5000 (`people.MatchHashesFirstWeek`; `GET /v1/me/contacts/key`'s `max_per_day` says which). The words the key comes with say "people whose settings let you find them this way". Tests: `TestMatchContacts`, `TestContactMatching`.

**By id.** `POST /v1/me/people {person_id}` and `GET /v1/people/{id}` take a bare person id with no proof that it was found (unlike adding a contact, which needs an `add_token`); left as is: a chat started so is a request unless the other added the person, and the card shows only what their settings let anyone see.

**Tests.** `api/people_review_test.go` (the tests named above), `TestPeopleForwards` and `TestContactMatching` changed with their fixes; `engine/people` `TestPolicy` (a block beside the decision), `TestMatchContacts` (find by number, the first week's limit); `engine/db` `TestPeopleReviewMigrationUpDownUp`.

**For the web and the app.**
- `GET /v1/me/conversations` and `GET /v1/me/people`: `?before=` (the last page's `next`), and `more` (always) and `next` (with `more`) in the answer; `unread` is the whole list's.
- `ChatListItem.photo` (`{urls}`, business rows) and `ChatListItem.suspended`, `PeopleChat.suspended`; a 409 "This account can't get messages right now." on sending to a suspended account.
- A share card from someone not in the recipient's contacts comes with `warn` and `warn_transfer`.
- A chat just opened is not in the lists until its first message: keep it open from `POST /v1/me/people`'s answer.
- "Find me from contacts" works within "Find me by my number" (the privacy screens' words), and `max_per_day` may be 1000.

## As built: stage 5a, app

Branch `stage5a-app`, on 68001463 (the two server streams joined): the Flutter app's side of stage 5a, on **You** (`mobile/lib`). People chats look and behave as chats with businesses do there: the same thread, bubbles, composer, menus, cards and theme. Nothing in the app calls people chats end-to-end encrypted. Where it says how they are kept, it says they are kept by Taskiem, readable by its servers and so not end-to-end encrypted, and read by no one at Taskiem except a moderator handling a report or under a lawful request (`peopleKeptWords`, §6.2, §6.3).

**Where.**
- `lib/chat/people.dart`: a person's face (their photo through `/v1/people/{id}/photo` when their setting lets you see it, else their letters; a lime dot when online), last seen in words, a message in a line, the warnings under a stranger's message, the report reasons, the kept-by-Taskiem words, and the contact-name modal.
- `lib/chat/people_chat.dart`: `PeopleChatData` and `PeopleChatScreen` (`/you/people/{id}`), `PersonInfoScreen`, the report and block-and-report sheet.
- `lib/chat/people_find.dart`: New chat, a person's link screen (`/p/{code}`), your link and QR code, Requests.
- `lib/chat/people_contacts.dart`: the contacts hash, `PhoneContacts` (the phone's address book behind an interface; `DevicePhoneContacts` uses flutter_contacts), matching, the explainer, Your contacts.
- `lib/chat/people_settings.dart`: Privacy with people, Blocked people.
- `lib/chat/people_cards.dart`: shared cards, opening one live, picking chats to share or forward to, the + menu's cards, "Your share", the bank account for shares.
- `lib/app/presence.dart`: the heartbeat.
- Changed: `my_chat.dart` (the one list; Share {business} with someone), `screens/you.dart` (Chats, New chat, Settings' "You and people"), `screens/you_links.dart`, `app/route.dart`, `app/applinks.dart`, `app/notify.dart`, `main.dart` (a person's link in a business session), `your_orders.dart` (Share with someone), `screens/you_account.dart` (Notifications' two new switches).

**The one Chats list** (§7, §17 Q6) reads `GET /v1/me/conversations` (and `?archived=1` for Archived): business and person rows together, newest first.
- A business's row is marked **Business**; a person's has their face and online dot; a screen reader hears each as a chat with a business or with a person.
- Unread, the last line ("You: …", "You blocked them", "Waiting for them to accept", "This account was deleted"), the time and mute show as before.
- **Message requests**, with its count, heads the list when any wait. **New chat** is in the head.
- A server without the one list (404) falls back to `GET /v1/me/chats`.

**A chat with a person.**
- Sent with a `client_id`. The words go back to the box when a send fails, and are sent again with the same id when the answer never came.
- Ticks: a clock while it goes, one when sent, two when delivered, two lime when read. Read shows only while the chat's `person.read_ticks` says read ticks go between the two (the server hides reads too).
- Reply by swiping or from the menu, with the quote above the bubble ("An earlier message" when it is not loaded); reactions; star; Copy.
- Forward to at most five chats (one at a time for a message forwarded many times), shown "Forwarded" or "Forwarded many times".
- Delete for me; delete for everyone only for your own message within 24 hours ("This message was deleted").
- Photos, PDFs and voice notes as in business chats (`doorFiles`), fetched from `/v1/me/people/{id}/files/{message}`.
- The head: the name you gave them (else theirs), their photo and online or last seen as both settings allow, or "Message request", "Blocked", "This account was deleted". Tapping it opens their card: the number only when the chat's `phone` is given, Rename or add, the kept-by-Taskiem words, Block, Report.
- A stranger's message marked by the checks (`warn`) carries a warning under it, on the receiver's side only; `warn_transfer` carries the transfer warning in people's words.
- A message the checks hold (403) is not sent: the server's own words show, and the words go back to the box.
- A deleted account's chat reads "Deleted account" and has no box.
- The ⋯ menu: About, Mute, Archive, Block or Unblock, Report, Delete this chat.

**Requests** (`/you/people/requests`): listed with the first message, and marked read when opened (`/read`, as any chat), so an opened request is no longer unread; the server tells the sender nothing of it until it is accepted (`TestPeopleReadTicksEverywhere`).
- **Accept**: the box appears; the sender sees reads from then.
- **Delete**: asked first; the sender is not told.
- **Block and report**: a reason, an optional note, consent to send the last 20 messages with their photos and files, and Block ticked.

**Finding people.**
- **By number** (`POST /v1/people/lookup`): the person found, with Chat (`POST /v1/me/people {person_id}`) and Add to contacts (with the answer's `add_token`, `from: number`); oneself; or "Not on Taskiem yet" with **Invite**, the server's words shared from the person's own phone.
- **By link**: pasted, or opened as `/p/{code}` (`appPageOf` passes `/p/{code}` on; Android's intent filter takes `/p/`). A QR code is scanned with the phone's own camera, which opens the link. The link screen shows who it is (`GET /v1/people/links/{code}`), Chat by `link_code`, Add (`from: link`), and plain words for a replaced (410) or unknown (404) link.
- **Your link** (`/v1/me/link`): the words, the QR code (`/v1/me/link/qr?format=png`), Share, Copy, Replace (asked first).
- **From the phone's contacts**, only after the person agrees:
  - The explainer says what is sent and kept, and the method's limit: Taskiem holds the key and numbers are few enough to try them all, so it protects against others seeing what is sent, not against Taskiem.
  - Only after **Agree** is the phone asked (`READ_CONTACTS`, `NSContactsUsageDescription`) and the contacts read.
  - Each number is put in E.164 (`contactE164`, Nigeria by default; see the review fixes below), each once. With `GET /v1/me/contacts/key`, each is hashed as `SHA-256(key ‖ "taskiem-contacts:" ‖ E.164)` in hex (`contactHash`) and sent in batches of the key's `max_batch` (`POST /v1/me/contacts/match`), no more than its `max_per_day`; on 409 only the batches not yet answered are sent again, with the new key; on 429 the matches found so far are kept and shown.
  - Nothing is stored: not the numbers, the hashes or the answer.
  - Found people show by the name the phone has, with Add (`from: contacts`, that name) and Chat.
  - A fixed vector checks the hash against the server's: `engine/people/hash_test.go` (`TestContactHashVector`) and `mobile/test/people_test.dart` hold the same key, number and 64 digits.
- **Your contacts** (`/v1/me/contacts`): by the name you see, with Chat, Rename (empty for their own name) and Remove.

**Settings, "You and people".**
- Privacy with people (`/v1/me/privacy/people`): find me by number (Everyone, My contacts, Nobody), from contacts (on, off), who can message me (Everyone as a request, My contacts, Nobody), last seen and online, profile photo, show my number, read ticks. Each is saved at once and put back if refused.
- Your contacts; your link and QR code; Blocked people (`/v1/me/people/blocked`, Unblock asked first); your bank account for shares.
- Notifications gains Messages from people and Message requests (`people`, `requests`).

**Presence.** While You is open, the app says `POST /v1/me/presence {online: true}` at once, every 30 seconds, and again when it comes back to the front; `{online: false}` when it goes to the background or You closes (`Presence`, a `WidgetsBindingObserver`).

**Cards** (§10.1, §11 A).
- Shared into a person's chat from its +: a business you chat with, a product of its shop (`GET /v1/me/chats/{id}/shop`), one of your orders or its delivery (`GET /v1/me/orders`), or **Your share**. Also from a business chat's ⋯ (Share {business} with someone) and from one of your orders (Share with someone), into chats you pick.
- A card shows its kind, its business, its price or state, and a delivery's progress.
- **Open** reads it live (`…/card`) as the person opening it:
  - a product's price now (and what it was when shared), few left, and Order from their shop;
  - a business's card, and Chat with them;
  - an order live only when it is yours (See your order), else only what was shared.
  
  Chat starts the person's own chat with the business, `from: shared`.
- **Your share**: the amount, what for, and who to pay (your saved account, or one typed). The receiver gets Copy number and Mark as paid; either side can set it back to Not paid yet. "Taskiem moves no money."
- The bank account for shares (`/v1/me/payout-account`) is saved, shown and removed in Settings.

**Pushes and links.**
- `route.dart` knows `/you/people/{chat}` (a person's chat), `/you/people/requests` and `/you/people/requests/{chat}`; `appPageOf` opens them and `/p/{code}`.
- A tapped push of kind `person` opens the chat; of kind `request`, Requests with the request over it. A people chat on the screen shows no push of its own (tag `person:{chat}`).
- Android gains the channels `person` (Chats with people) and `request` (Message requests).

**Server and generator changes made here.**
- `POST /v1/me/chats` refused `from: shared` (400), which §10.2's Chat on a shared card needs. It now takes `shared` (`api/customer_door.go`, the OpenAPI enum); `TestPeopleCards` starts one and refuses an unknown `from`.
- `tools/mobilegen` names a parameter that is one of Dart's own words with a trailing `_`. The `for` of `DELETE …/messages/{m}?for=` had made `api.g.dart` fail to compile (`TestAPIClientReservedParameter`).

**Tests.**
- `mobile/test/people_test.dart`:
  - the hash vector; matching (E.164, each number once, batches, a changed key, no number in readable form); routes and links; the words (no claim of end-to-end encryption anywhere in the people chats' code);
  - the one list with Requests;
  - a chat's head, ticks, quote, Forwarded many times and deleted message; read ticks off; sending with a client id and a reply, and a held message; the warning; delete for everyone within 24 hours; forward and its cap; a deleted account;
  - a request opened without a read, Accept, Delete, Block and report with consent; Requests;
  - the person's card with and without their number;
  - sharing a business, a product, an order;
  - New chat by number with Add, Chat and Invite; a person's link and a replaced one; your link, its QR code and Replace;
  - contacts not read before Agree, then matched and added; the phone saying no; Your contacts renamed and removed;
  - Privacy with people; blocked people;
  - a product opened live and a share marked paid; a share sent with a typed account, and the bank account saved;
  - a link (as a push's) to a request and to a chat; presence.
- `notify_test.dart`: pushes of kinds person and request routed, and none shown for the chat on the screen.
- `you_settings_test.dart`: the two new notification switches.

**Left over.**
- Typing in people chats (the server has none yet), search across a person's own people chats, and starred messages across chats.
- Location messages are shown, not sent from the app.
- Contacts are matched when the person opens From your contacts, not re-checked daily by themselves.
- No scanner in the app: the phone's camera opens a person's QR code as a link.
- Discover's profile has no Share yet.

### App review fixes

Branch `stage5a-fix-app`, on 304d2445 (the server's review fixes): the app's findings of the stage 5a review, each with a test in `mobile/test/people_review_test.dart` (and one in `home_open_test.dart`). Where this list differs from the sections above, it holds.

- **Nothing read while put away or covered.** An Android app's timers run in the background, so a chat's 4-second read went on after Presence said offline (the server's "here" made the person online again) and marked arriving messages read. `PeopleChatScreen`, `MyChatScreen` and You's list now read again through the `Polling` mixin (`kit/polling.dart`): only while the app is in front and the screen is on top, and once at once when the app comes back. `PeopleChatData` and `MyChatData` no longer run timers of their own (`poll()`, called by the screen), and call `/read` only while their `seen` says the app is in front and the chat is on top (`appInFront`, now in `polling.dart`).
- **Older messages stay.** Once the last older page was read, the next read of the latest page dropped them (both in people chats and in chats with businesses). "Older loaded" is now kept apart from "the server has more": older messages read stay across reads of the latest page while the server has more before it, and Load older shows by what the last older page said.
- **Numbers as the phone keeps them.** Contacts are matched through `contactE164` (`app/phone.dart`), for matching only: Unicode format characters (`\p{Cf}`, such as U+202A and U+202C round a number) are dropped, and `234` or `2340` then 10 digits starting 7, 8 or 9 is read as +234; else as `toE164`, which is unchanged for typed numbers.
- **Business logos** in You's Chats and Archived, from `ChatListItem.photo` (`BusinessFace`, as Orders shows them).
- **An opened request is marked read** (`/read` as for any chat; the server passes nothing on to the sender of a request). The Requests line above says so.
- **Contacts matching retries.** On 409 only the batches not yet answered are sent again with the new key (at most twice); on 429 after some batches, the matches found are kept and shown with "Checked N of your M numbers…"; no more numbers than `max_per_day` are sent (1000 in an account's first week), and the rest is said the same way.
- **The person's card.** Block there, when it removes the chat, closes the card and then the chat (it closed only the card before); a blocked person's card has **Unblock**.
- **A business session.** A person's chat opened over a business (`/you/people/…`, a person's link) gets the business home's opener for a shared card's Chat and this phone's notifications (`_openMine`, `_openPerson` in `main.dart`).
- **Retry-safe sends.** A photo or file, a card, and Share with someone (which sent no `client_id`) are sent again with the client id they first went with when no answer came (offline, cut off, 5xx), as words are; a refused send takes a new one.
- **Small.** "Blocked today", "Blocked yesterday", "Blocked on Tue 14 Oct" (`blockedDay`); You's notification question is "Get a notification when a business or a person messages you?"; online is said to a screen reader (the dot's "Online", and a row's "Chat with a person, online"); the forward and share picker lists archived chats too (and no suspended account); Add to contacts by number or link asks for their name in your contacts, as the web does; the contacts explainer says that the person and the name you give when you tap Add are kept (Add asks it, the phone's name filled in), and that only people whose settings let you find them are answered. Head buttons and `TkSmallButton` keep their look and answer a finger in 48 by 48 (`TapArea`).
- **The server's changes adopted.**
  - `GET /v1/me/conversations` (You's Chats and Archived) and `GET /v1/me/people` (the picker) are read a page at a time: `ChatPages` keeps the first page read again with the later pages read by `before` the last `next`; **Show more chats** (`TkLoadMore`, `kit/load_more.dart`) reads the next as the list is scrolled near its end; `unread` is the whole list's. Archived's count has a + while there are more.
  - `suspended` on `ChatListItem` and `PeopleChat`: the head and the person's card say "This account can't get messages right now.", the row says so, the chat says it can be read but not written to, and there is no box (nor reply, nor Mark as paid); a send's 409 is the server's words.
  - A chat just started (`POST /v1/me/people`) opens from that answer (`OpenPeopleChat`'s `started`, `PeopleChatData(started:)`), since it is in no list until its first message.
  - A share card with `warn` (from someone not in the recipient's contacts) carries the transfer warning, as `warn_transfer` does.
  - Privacy with people: "From their contacts" says it works as far as "By my number" lets them.

## As built: stage 5a, web

Branch `stage5a-web`, on `stage5` (68001463, the two streams joined): the web's screens for stage 5a, on You. No server change was needed.

**Words.** No screen says or suggests that people chats are end-to-end encrypted (a unit test checks the words). A chat's Contact info and the privacy page say how they are kept in the app's words (`KEPT_WORDS`, `web/src/lib/people.ts`, the app's `peopleKeptWords`; since the web review fixes, below); the server's `kept` is not shown.

**The one Chats list** (`PersonChatList`, `web/src/pages/PersonChats.tsx`) reads `GET /v1/me/conversations`: business and person rows together, newest first. A business row is marked "Business" (with its logo as before); a person's row has their photo when their setting shows it and is named "(a person)" to a screen reader. Each row has its unread count, last line, time, a draft, muted, and Archived apart. "Message requests" with the count sits at the top when `requests` > 0. **New chat** opens the finding sheet.

**A chat with a person**, `/you/people/:id` (`web/src/pages/PeopleChat.tsx`), built from the business chat's parts (`ChatHead`, `ChatStream`, `BubbleShell`, `ComposerView`, `AppAttachmentView`, `useConfirm`, `Sheet`) and filling the window as a business chat does:
- **The head**: the name the viewer gave them (else theirs), their photo by their setting, "online" or "last seen …" by both settings, and their number only when `phone` is given. Its menu: Contact info, Mute, Archive, Block/Unblock, Report, Delete chat.
- **Sending**: with `client_id` (a send whose answer never came is sent again with the same one); photos, voice notes and PDFs through `…/files`; a reply's `reply_to`.
- **Ticks** (`tickState`): one tick for `sent`, two for `delivered`, coloured only for `read`: the client never infers a read.
- **On a message**: the reply's quote, "Forwarded" or "Forwarded many times", reactions (one of each side), star, Copy, Delete (for me; for everyone within 24 hours, then "This message was deleted"), Forward (a sheet of people chats, at most 5, 1 for one forwarded many times; failures listed).
- **Warnings** on the receiver's side: `warn_transfer` gives the transfer warning business chats use; `warn` gives "Be careful: this message looks like a scam…".
- **Refusals** (403 held, 409, 429) are said in the server's own words above the composer; the words go back in the box.
- **States**: a deleted account's chat says it takes no more messages; a blocked person's offers Unblock; `waiting` says the message waits for them to accept.

**Requests**: `/you/people/requests` lists them; `/you/people/requests/:id` is the same chat with Accept, Delete and Block and report in place of the composer. Opening one marks it read, which tells the sender nothing (the server's rule). Accept moves to `/you/people/:id`.

**Finding people** (`web/src/pages/People.tsx`), in New chat:
- **By number** (`POST /v1/people/lookup`): the person with **Message** and **Add to contacts** (with a name of one's own), or "Not on Taskiem yet" with Invite on WhatsApp, Invite by text and Copy, sent from the person's own device.
- **By link**: a pasted link or code (`GET /v1/people/links/{code}`).
- **Your link**: its QR code (`/v1/me/link/qr`), Copy, Share (the device's share, else WhatsApp) and Replace link.
- **Contacts matching** is the phone app's. The web says so in one line.
- **Contacts**: `/you/people/contacts`, with Message, Rename and Remove; Add someone opens New chat.

**A person's link** `/p/{code}` is the web app's own page:
- **Signed out** (or in a business): their name and photo from `GET /v1/person-links/{code}`, Chat on Taskiem (`/login?next=/p/{code}`, or a move to You) and Get the app.
- **On You**: Message (`POST /v1/me/people {link_code}`) and Add to contacts.
- **410** says "This link no longer works".

**Settings** (on You's Settings, "Chats with people"):
- **Privacy with people** (`/you/settings/people`): the four audiences of §5.4 and the switches for read ticks, showing the number and being found from contacts, each saved as it changes.
- **Blocked people** (`/you/settings/blocked`), with Unblock.
- **Your bank account** (`/you/settings/payout`).
- **Notifications** gain "Messages from people" and "Message requests".

**Block and report**: the report sheet asks why, an optional note, and consent: "Send the last 20 messages of this chat to Taskiem's moderators…". Block too is a tick (on by default from a request's Block and report, which sends `POST …/block {report}`).

**Cards** (§10.1, §11 A):
- **The share button** in a people chat's composer offers a business (one the person chats with), a product (from that business's shop, `/v1/me/chats/{id}/shop`), one of the person's orders or its delivery, or Your share.
- **Elsewhere**: Discover's profile ("Share … with someone") and an order's page ("Share with someone") send a business or order card to a chat picked from a sheet.
- **Opening a card** reads it live (`…/card`): a product's price now and whether it is available, a business's card, an order only when it is yours (with See your order), else what was shared. A product offers Order from their shop and a business Chat with them, each starting the person's own chat with it through the customer door with `from: shared` (§10.2; since the web review fixes, below).
- **Your share**: an amount, what for, and the saved account (or another typed), saying Taskiem moves no money. Either side's **Mark as paid** shows Paid on both.

**Push**: the service worker knows the kinds `person` and `request`; their links `/you/people/{id}` and `/you/people/requests/{id}` open on You as they are. In a business's session, `/you/people/*` offers "Open it on You" (`PeopleOnYou`), as `/you/chats/:id` does.

**Tests.**
- `web/src/lib/people.test.ts`: the words (never end-to-end), names, last seen, numbers, ticks, forward caps, 24 hours, rows, link codes, accounts, merging pages, refusals. `push.test.ts`: the two kinds. `youaccount.test.ts`: the five kinds.
- `web/e2e/people.spec.ts`, on the real server, Ada at 390 px and Bola at 360, axe in both themes at each step:
  - Bola lets everyone find them by number. Ada finds a number that is nobody's (Invite), then Bola, adds them with a name of her own and writes: the message waits.
  - Bola sees one request, opens it (Ada's ticks stay unread), accepts (Ada's turn read) and answers. A reply is quoted on the other side; a message deleted for everyone is gone from both.
  - With Bola's read ticks off, Ada sees no read ticks.
  - Ada's link and QR code: signed out, then Chidi chats from it, then it is replaced and the old one says it no longer works.
  - Dayo's prize-and-OTP message is held and said; an account-number message reaches Ada's requests with the warning. Block and report (consent, a report kept), in the blocked list, unblocked. Bola's "Nobody" refuses Dayo's new chat.
  - A product shared from a business's shop opens live with its new price; a business is shared from Discover; Ada's bank account is saved; Your share ₦3,000 is marked paid by Bola and shows Paid to Ada.
- `accessibility.spec.ts`: every new page and sheet (the list with Requests, a chat with every kind of message, Contact info, a request, Block and report, New chat, contacts, privacy, blocked, bank account) at 390 and 360 px in both themes, and `/p/{code}` signed out. The You tests stand in `/v1/me/conversations`; `customer-chat.spec.ts` and `you.spec.ts` follow the one list.

**Left over.**
- **The parity spec** keeps no people screens: its pictures are what the phone app copies, and `mobile/test/parity_test.dart` fails on a web picture with no app screen. They come with the app's people screens.
- **Not offered on the web**: sending a place (places sent from the app are shown), a starred messages list, search across people chats, typing, and the presence heartbeat (the server Touches the person on every people request).
- **From the web**: a group's ordering together (5b), and calls (5c).

### Web review fixes

Branch `stage5a-fix-web`, on 304d2445: the web findings of the stage 5a review, and the server's "For the web and the app" list (Review fixes, above) taken up. Where this list differs from the web's sections above, it holds.

- **Your share asks what for.** The server refuses a share without what it is for (400), so "What for" is required and Send waits for it and for an amount of at least ₦1 (`splitReady`, "At least ₦1" under a smaller one). Mark as paid is not offered while the chat is a request.
- **A refused card is not called sent.** A card the server refuses (held, not taken, too many) returns false from the chat's share: the sheet closes, the refusal shows above the composer in the server's words, and neither "Shared" nor "Sent" is toasted. Sharing from elsewhere (`ShareToPerson`) says a refusal in the sheet.
- **An order or its delivery.** In a chat's share sheet an order is chosen first; its delivery is offered only when the order has one (`GET /v1/me/orders/{business}/{reference}`'s `delivery`). An order's page offers the order, or its delivery when it has one.
- **Forwarding** says what went (`forwardSaid`, from the answer's `sent`), lists what failed, and keeps only the chats that failed picked, to try again.
- **Honest words.** `KEPT_WORDS` is the app's `peopleKeptWords` word for word: kept by Taskiem and sealed on its servers, which can open them, so not end-to-end encrypted; read by no one at Taskiem but a moderator on a report or when the law requires it; automated checks for scams on messages from people you don't know; never used for ads or to train AI. Shown with an info mark, not a lock. A stranger's message or share card asking for a transfer (`warn_transfer`) carries the people's warning (`PEOPLE_TRANSFER_WARNING`, the app's `peopleTransferWarning`), not the business chats' words about pay links.
- **Chat from a shared card** starts the person's own chat with `from: shared`: `StartChat` passes `shared` through, and a product's Order from their shop opens it with the shop open.
- **Opened cards**: a product's price now and what it was when shared (`priceNowWords`), only so many left (`stockWords`), "They no longer sell this…" when it is gone, Order from their shop; a business's card with Chat with them; an order not yours says so. A received share card has Copy number.
- **Read ticks**: `tickState(m, readTicks)` never shows read while the chat's `person.read_ticks` is off, behind the server's own hiding.
- **The one list**: a business row's logo from `ChatListItem.photo`, its "Business" mark outside the name's ellipsis (`ChatRow`'s `mark`); a row says "This account was deleted" (`gone`) or "Can't get messages right now" (`suspended`).
- **A page at a time.** `GET /v1/me/conversations`, `/v1/me/people` and `/v1/me/people/requests` are read a page at a time (`usePagedList`, `web/src/lib/usePagedList.ts`): the next page when the list's end scrolls into view or with Show more (`MoreRows`), and a poll reads the first page again in place of what it covers, keeping later pages read (`withFirstPage`, `appendPage`, `pageURL`). `unread` and `requests` are the first page's answer, the whole list's. Requests is read again every 8 seconds, as the Chats list is.
- **A chat's head** says what the chat is before when they were last here (`headWords`): "Deleted account", "Message request", "Blocked", "Can't get messages right now" (suspended). A suspended account's chat has no composer ("This account can't get messages right now."), and a send to one says the server's 409 in its words. Pickers leave suspended accounts out (`canSendTo`).
- **Sending**: after the first message to someone (and an answer to a request) the chat is read again at once, so the waiting line shows; the waiting line sits above the composer, not on a blocked, deleted or suspended chat. What a send ran into goes once the words change or a send goes.
- **Tell me when they answer** in a people chat (You's `TellMe`, its `people` form): offered where the browser takes a push; a person's answer is never texted, so nothing is offered where it can't.
- **Share with someone** from a business chat's ⋯ (Share {business} with someone) and from a product in its shop sheet (Share with someone), into a people chat picked (§10.1).
- **Your link and QR code** in You's Settings too, under "Chats with people".
- **Chats just opened** are in no list until their first message: New chat opens the chat by the id `POST /v1/me/people` answers.
- **Contacts matching** words say it works within "Who can find me by my number" (`CONTACTS_ON_PHONE`, the Find me from contacts switch).

**Tests.** `web/src/lib/people.test.ts`: the kept and transfer words, read ticks off, rows of deleted and suspended accounts, the head's words, the pages (append, a poll over later pages, the `before` address), Your share's floor and what for, a product's price then and now and few left, `from=shared`, forwards said, pickers without suspended accounts. `web/e2e/people.spec.ts`: the honest words on the privacy page and Contact info; the waiting line at once; "Message request" in a request's head; a shared product opened with its price then and now and Order from their shop starting a chat `from: shared`, a business card's Chat with them; a new test where Your share needs at least ₦1 and what for, a share held for a prize fee is said and not toasted Sent, a stranger's share card carries the people's transfer warning and Copy number and no Mark as paid on a request, and a forward to two chats with one failing keeps only that one picked.

**Left over.** Opening a chat still reads it by id rather than from `POST /v1/me/people`'s answer (the same chat, one more request). A suspended account's chat in the lists has no photo or last seen, as the server sends none.

## As built: stage 5a gaps, chat features (server)

Branch `stage5a-gaps-chat`, on ffa3743c (stage 5a reviewed and fixed, with main merged): the server's part of what stage 5a left over: You's live connection (§13), typing (§7, §13), presence on the connection (§5.4, §13), search across a person's own people chats (§6.2), starred messages across chats (§7), the trust line on the one Chats list (§7, 0069 §6), and checks of the push preview (§6.2) and of places (§7). Migration `00479_people_live_search.sql`; code in `api/people_live.go`, `api/people_search.go` and `engine/peoplechats/search.go`. Moderation and safety are another stream's (00480); nothing here touches reports, verdicts or the operator's API. Where this section differs from those above, it holds.

**Data (00479).** Every new table is the person's own (`person_own`: the person in scope, no business's door open), as the people chats' are.
- `people_chats.changed_at`, `people_messages.changed_at`: when each row of a copy last changed (set on insert and, by the trigger `taskiem_people_changed`, on every update), indexed per person, for the live connection.
- `people_gone (person_id, chat_id, message_id?, at)`: a message deleted for the person, or (`message_id` null) a request they deleted, for their other devices; kept an hour.
- `person_live (person_id, id, front, seen_at)`: the person's open live connections, each in front or away.
- `people_typing (person_id, chat_id, conversation_id, until)`: who is typing to the person, in which of their chats, until when; written by the one typing through `taskiem_people_reach` (peer policies for select, insert, update and delete of that one conversation), never past the rules below.
- `people_search (person_id, message_id, conversation_id, token)`: the search index, 16-byte tokens only, `ON DELETE CASCADE` with the message; written for the other's copy through the reach (and removed through it on delete for everyone). `people_search_ready (person_id)`: the person's older messages are indexed.
- `taskiem_customer_business_trust(tenants[])` (definer, in `definers.tsv`): for the person in scope and the businesses they chat with in the app (as `taskiem_customer_business_photos`), whether each proved a phone, had the business checked, has a payment provider connected, and since when it is on Taskiem. Nothing of any other business.
- Deleting an account also deletes the person's index, its readiness, gone records, typing marks and live connections.

**You's live connection: `GET /v1/me/live`.** Server-sent events, as the Chat page's stream (`/v1/chat/stream`): a personal session, or a business session for its person (an API key is refused); `Accept: text/event-stream`; each connection asks every second what changed since it last looked, in the person's own transaction, so it only ever carries the person's own copy. Ten open per person (429 with `Retry-After: 30`; the client polls then). It closes after half an hour; clients reconnect with `Last-Event-ID` (what was missed is sent, at most an hour back; an event may come twice across a reconnection, so merge by id). Keep the 4-second polling as the fallback while it is not open.

| Event | Data | When |
| --- | --- | --- |
| `ready` | `{connection}` | first; the connection's id, for presence |
| `message` | `{chat_id, request, message: PeopleMessage}` | a message of one of the person's chats is new or changed: arrived, delivered, read (as read ticks allow), a reaction, a star (on the person's other devices), deleted for everyone. `request`: it waits in Requests |
| `chat` | `{id, hidden: false, chat: PeopleChat}` or `{id, hidden: true}` | a chat is new (a request, a chat started elsewhere once written in), has a new last message or unread count, was accepted, muted, archived, or deleted for the person |
| `gone` | `{chat_id, id}`; `{chat_id, id: null}` | a message deleted for the person on another device; a request they deleted |
| `typing` | `{typing: [chat ids]}` | the person's chats whose other person is typing now; sent when the set changes |
| `presence` | `{people: [{person_id, chat_id, online, last_seen_at}]}` | the people the person chats with (their 200 latest chats) whose online or last seen changed, as both their settings let them be seen (both null when hidden); all of them first, then checked every 5 seconds |
| `ping` | `{}` | at least every ten seconds |

`message`, `chat` and `gone` carry an `id` (the change's time in microseconds). A chat opened and not yet written in sends no `chat` (it is in no list).

**Presence on the connection.** While one of the person's live connections is in front, they are online: `Touch` on connecting and every 30 seconds. `GET /v1/me/live?away=1` opens one that starts away. `POST /v1/me/presence {online, connection}` (the ready event's id) says that connection is in front (`true`: online now) or away (`false`: away, last seen now, unless another of theirs is in front). When the last connection in front closes, the person is away, last seen then. While their connections are open and all away, using the people chats no longer makes them online (`peopleHere`). `POST /v1/me/presence {online}` without `connection` is the heartbeat as before, for clients without the connection; a `connection` that is not open is taken as the heartbeat. What the other sees follows the last seen settings both ways, as before (`presence` events, `PersonCard`). Presence is per server for the connection counts: a person's connections on two servers each keep them online, and the last to close on one server makes them away until the other's next beat (30 seconds).

**Typing: `POST /v1/me/people/{id}/typing`** (`{typing?: bool}`, true when left out; `false` stops it). Always 204 (404 for a chat that is not the person's), so nothing tells them whether it went. It is passed on at most every 3 seconds per chat (more often changes nothing; call it while typing), shows for 6 seconds, and ends when they stop or send. It goes only within an accepted chat: never while either copy is a request (the sender's own waiting messages included), never when either blocked the other (and a mark made before a block is not shown after it), never to a deleted or suspended account or a chat the other deleted. Clients: say it every 3 seconds or so while the composer changes, `false` when it empties; show "typing…" while the chat is in the last `typing` event.

**Search: `GET /v1/me/people/search?q=&chat=&before=`.** The person's own copies, the newest first, 30 a page: `{results: [PeopleFound], more, next?}` (`next` goes in `before`). A message is found when every word of `q` (of two letters or more) begins one of its words, case and accents aside ("osun" finds "Ọ̀ṣun"), in its words or caption, a file's name, a card's title, business or what for (a share's `what`), or a place's label. `chat` keeps to one chat. `PeopleFound` is `{chat_id, chat: PeopleChat, message: PeopleMessage, snippet?, starred_at?}`; `snippet` is up to 120 characters around the first match, with "…" where cut, opened at read time. Never another person's copy, a chat the person deleted, or a message deleted for them or for everyone. 400 when `q` has no word of two letters (or is over 200 characters); 429 when searching very fast.
- **The index holds no words.** Each message is indexed, in each copy, under HMAC-SHA-256 tokens (16 bytes) of its words' first 2 to 8 letters (normalised: NFKD, accents dropped, lower case; at most 600 a message), keyed by `SearchKey(person's data key, person)`. A query is hashed the same way; candidates are then opened and checked against the words, so a hash shared by chance never shows a message that does not match, and a query word longer than 8 letters still matches only words it begins. Without the person's key the index says only how many word beginnings each message has; with it, words can be guessed and tested, as the sealed words could be opened (§6.2: readable by Taskiem's servers).
- **Kept as the messages are:** written on send and receive (both copies, in the send's transaction), gone with delete for me, delete for everyone (both copies), deleting a chat, declining a request and deleting the account. Messages kept before 00479 are indexed on the person's first search (500 a transaction), then never again.

**Starred: `GET /v1/me/people/starred?before=`.** The person's starred messages across their people chats, the most recently starred first, 50 a page: `{starred: [PeopleFound], more, next?}`, each with `starred_at` and its chat; not those of a chat they deleted. One chat's stays `GET /v1/me/people/{id}/messages?starred=1`.

**The trust line on the one Chats list.** A business row of `GET /v1/me/conversations` (`ChatListItem`) has `verified: {phone, business, payments, months}`, the same values and source as the business chat's head (`GET /v1/me/chats/{id}`'s `verified`): a verified phone, a check of the business, a payment provider connected, whole months on Taskiem. None while the business is not taking the person's messages. Clients say it in the head's words.

**The push preview** is each device's own (`push_subscriptions.preview`), honoured for kinds `person` and `request` ("Someone wrote to you", "You have a new message request", title "Taskiem"). The web's registration (`PUT /v1/me/push`, side `you` in a personal session) already takes `preview` (and `PATCH /v1/me/push/{id}` changes it); nothing new was needed. The web stream adds the switch.

**Places.** `location` on send: `lat` -90 to 90, `lng` -180 to 180 (NaN refused), a label trimmed and at most 200 characters, a place with a card refused (400); the label goes past the stranger checks as words do (Review fixes) and is searchable.

**Tests.**
- `api/people_gaps_test.go`: `TestPeopleLive` (a message and its chat on the other's connection, read marks and reactions back, a star only on the person's other device and never the other's, muting and delete for me on the other device, delete for everyone, a request and a deleted request, nothing of anyone else's on a third person's, `Last-Event-ID`, signed out 401); `TestPeopleTyping` (shown, gone after 6 s, at most every 3 s, stopped, ended by a message, both ways; never across a request either way; 404 for another's chat; never across a block either way with the same 204, and a mark made before a block not shown); `TestPeopleLivePresence` (online while connected, away with the connection kept, not online again by using the chats while away, back, away with last seen on closing, Nobody hiding both, the heartbeat still working); `TestPeopleSearch` (own copy only, accents, case, beginnings, not the middle of a word, places and shares, one chat, the newest first, no words in the index and no token alike between two people, delete for me and for everyone and a deleted chat with their tokens gone, backfill, pages, account deletion); `TestPeopleStarred`; `TestPeopleListTrustLine`; `TestPeoplePushPreviewOnRegister`; `TestPeopleLocationLimits`.
- `engine/peoplechats`: `TestSearchTokens`, `TestSnippet`. `engine/db`: `TestPeopleLiveMigrationUpDownUp`.

**For the web and the app.**
- Open `GET /v1/me/live` while signed in and in front; on `message`, `chat` and `gone` update the open chat and the lists; stop the 4-second polling while it is open and poll again when it fails or closes. Keep the ready event's `connection`; say `POST /v1/me/presence {online: false, connection}` on going to the background (or close it) and `{online: true, connection}` on coming back; the 30-second heartbeat is not needed while connected.
- `POST /v1/me/people/{id}/typing` while typing, `{typing: false}` on clearing; "typing…" from `typing` events.
- Search: `GET /v1/me/people/search?q=` (and `&chat=` inside a chat), pages by `before`; show `snippet` and open `chat_id` at `message.id`.
- Starred: `GET /v1/me/people/starred`.
- The one list: a business row's `verified` in the head's words.
- The web: the push preview switch on `PUT /v1/me/push` / `PATCH /v1/me/push/{id}` `preview`.

**Left over.**
- The live connection carries people chats only; You's business chats keep their polling (a `person_conversations` change feed would be the next step).
- Presence counts per server (above). Typing does not follow "Last seen and online" (§5.4 has no setting for it).
- Search ranks by time only; no phrase or fuzzy search; one-letter words are not searched.
