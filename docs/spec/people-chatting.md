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

**Presence on the connection.** While one of the person's live connections is in front, they are online: `Touch` on connecting and every 30 seconds. `GET /v1/me/live?away=1` opens one that starts away. `POST /v1/me/presence {online, connection}` (the ready event's id) says that connection is in front (`true`: online now) or away (`false`: away, last seen now, unless another of theirs is in front). When the last connection in front closes, the person is away, last seen then. While their connections are open and all away, using the people chats no longer makes them online (`peopleHere`). `POST /v1/me/presence {online}` without `connection` is the heartbeat as before, for clients without the connection; a `connection` that is not open is taken as the heartbeat. What the other sees follows the last seen settings both ways, as before (`presence` events, `PersonCard`). The connections are kept in the database (`person_live`), so every server sees all of a person's: they are away only when the last connection in front closes, on whichever server, or says it is away; one that ended without saying so (a server stopped) is taken as closed after 2 minutes without a beat (`liveStale`), and the safety job prunes such rows (Second review fixes, server).

**Typing: `POST /v1/me/people/{id}/typing`** (`{typing?: bool}`, true when left out; `false` stops it). Always 204 (404 for a chat that is not the person's), so nothing tells them whether it went. It is passed on at most every 3 seconds per chat (more often changes nothing; call it while typing), shows for 6 seconds, and ends when they stop or send. It goes only within an accepted chat: never while either copy is a request (the sender's own waiting messages included), never when either blocked the other (and a mark made before a block is not shown after it), never to a deleted or suspended account or a chat the other deleted. Clients: say it every 3 seconds or so while the composer changes, `false` when it empties; show "typing…" while the chat is in the last `typing` event.

**Search: `GET /v1/me/people/search?q=&chat=&before=`.** The person's own copies, the newest first, 30 a page: `{results: [PeopleFound], more, next?}` (`next` goes in `before`). A message is found when every word of `q` (of two letters or more) begins one of its words, case and accents aside ("osun" finds "Ọ̀ṣun"), in its words or caption, a file's name, a card's title, business or what for (a share's `what`), or a place's label. `chat` keeps to one chat. `PeopleFound` is `{chat_id, chat: PeopleChat, message: PeopleMessage, snippet?, starred_at?}`; `snippet` is up to 120 characters around the first match, with "…" where cut, opened at read time. Never another person's copy, a chat the person deleted, or a message deleted for them or for everyone. 400 when `q` has no word of two letters (or is over 200 characters); 429 when searching very fast.
- **The index holds no words.** Each message is indexed, in each copy, under HMAC-SHA-256 tokens (16 bytes) of its words' first 2 to 8 letters (normalised: NFKD, accents dropped, lower case; at most 600 a message), keyed by `SearchKey(person's data key, person)`. A query is hashed the same way; candidates are then opened and checked against the words, so a hash shared by chance never shows a message that does not match, and a query word longer than 8 letters still matches only words it begins. Without the person's key the index still shows which of the person's messages share a word or a word's beginning, how often each token is used (word frequencies), how many tokens each message has and its conversation; with the key (which Taskiem's servers hold), words can be guessed and tested, and a word's chain of beginnings (its first 2, 3, … 8 letters) rebuilds short words letter by letter. That is acceptable under §6.2 (kept by Taskiem, readable by its servers, not end-to-end encrypted): a holder of the key could open the sealed words themselves (Second review fixes, server).
- **Kept as the messages are:** written on send and receive (both copies, in the send's transaction), gone with delete for me, delete for everyone (both copies), deleting a chat, declining a request and deleting the account. Messages kept before 00479 are indexed on the person's first search (500 a transaction), then never again.

**Starred: `GET /v1/me/people/starred?before=`.** The person's starred messages across their people chats, the most recently starred first, 50 a page: `{starred: [PeopleFound], more, next?}`, each with `starred_at` and its chat; not those of a chat they deleted. One chat's stays `GET /v1/me/people/{id}/messages?starred=1`.

**The trust line on the one Chats list.** A business row of `GET /v1/me/conversations` (`ChatListItem`) has `verified: {phone, business, payments, months}`, the same values and source as the business chat's head (`GET /v1/me/chats/{id}`'s `verified`): a verified phone, a check of the business, a payment provider connected, whole months on Taskiem. None while the business is not taking the person's messages. Clients say it in the head's words.

**The push preview** is each device's own (`push_subscriptions.preview`), honoured for kinds `person` and `request` ("Someone wrote to you", "You have a new message request", title "Taskiem"). The web's registration (`PUT /v1/me/push`, side `you` in a personal session) already takes `preview` (and `PATCH /v1/me/push/{id}` changes it); nothing new was needed. The web stream adds the switch.

**Places.** `location` on send: `lat` -90 to 90, `lng` -180 to 180 (NaN refused), a label trimmed and at most 200 characters, a place with a card refused (400); the label goes past the stranger checks as words do (Review fixes) and is searchable.

**Tests.**
- `api/people_gaps_test.go`: `TestPeopleLive` (a message and its chat on the other's connection, read marks and reactions back, a star only on the person's other device and never the other's, muting and delete for me on the other device, delete for everyone, a request and a deleted request, nothing of anyone else's on a third person's, `Last-Event-ID`, signed out 401); `TestPeopleTyping` (shown, gone after 6 s, at most every 3 s, stopped, ended by a message, both ways; never across a request either way; 404 for another's chat; never across a block either way with the same 204, and a mark made before a block not shown); `TestPeopleLivePresence` (online while connected, away with the connection kept, not online again by using the chats while away, back, away with last seen on closing, Nobody hiding both, the heartbeat still working); `TestPeopleSearch` (own copy only, accents, case, beginnings, not the middle of a word, places and shares, one chat, the newest first, no words in the index and no token alike between two people, delete for me and for everyone and a deleted chat with their tokens gone, backfill, pages, account deletion); `TestPeopleStarred`; `TestPeopleListTrustLine`; `TestPeoplePushPreviewOnRegister`; `TestPeopleLocationLimits`.
- `engine/peoplechats`: `TestSearchTokens`, `TestSnippet`. `engine/db`: `TestPeopleLiveMigrationUpDownUp`.
- `TestDiscover`'s check that no search is kept now leaves out `people_search*` (keyed tokens of a person's own messages, not searches).

**For the web and the app.**
- Open `GET /v1/me/live` while signed in and in front; on `message`, `chat` and `gone` update the open chat and the lists; stop the 4-second polling while it is open and poll again when it fails or closes. Keep the ready event's `connection`; say `POST /v1/me/presence {online: false, connection}` on going to the background (or close it) and `{online: true, connection}` on coming back; the 30-second heartbeat is not needed while connected.
- `POST /v1/me/people/{id}/typing` while typing, `{typing: false}` on clearing; "typing…" from `typing` events.
- Search: `GET /v1/me/people/search?q=` (and `&chat=` inside a chat), pages by `before`; show `snippet` and open `chat_id` at `message.id`.
- Starred: `GET /v1/me/people/starred`.
- The one list: a business row's `verified` in the head's words.
- The web: the push preview switch on `PUT /v1/me/push` / `PATCH /v1/me/push/{id}` `preview`.

**Left over.**
- The live connection carries people chats only; You's business chats keep their polling (a `person_conversations` change feed would be the next step).
- Typing does not follow "Last seen and online" (§5.4 has no setting for it).
- Search ranks by time only; no phrase or fuzzy search; one-letter words are not searched.

## As built: stage 5a gaps, safety and moderation

Branch `stage5a-gaps-safety`, on ffa3743c (stage 5a with the school and the chats' redesign merged): what §6.3, §8, §14 and §16 and decision 0069's points 3, 5 and 11 and its Consequences still needed, for the server, the operator console and the web; the app's screens for Your standing are left over (below). Migration `00480_people_safety.sql`; packages `engine/peoplesafety` and `engine/telemetry` (`people.go`); `api/people_safety.go` (the person's side and the safety job), `api/ops_people.go` (moderators), `api/lawful.go` (lawful requests). Where this differs from the sections above, it holds.

**People chats are kept by Taskiem, sealed at rest and readable by its servers. They are not end-to-end encrypted, and nothing built here says they are.**

### Data (00480)

- **Reports keep their evidence.** At report time the last 20 messages of the reporter's copy, opened, are sealed under the report's own data key (32 random bytes, kept sealed by the platform key for `people-report:{id}` alone: `box_key`, `kms_key`), in `people_reports.sealed`; `messages` stays `'[]'`. The photos and files in them are copied, opened from the reporter's copy and sealed again under the report's key, into `people_report_files` (newest first, up to 50 MB a report, `peoplesafety.ReportFilesMax`; a message whose file did not fit says `file_left_out`). Reports made before 00480 are sealed by the safety job. `people_reports.person_id` and `customer_reports.person_id` are now `ON DELETE SET NULL`: deleting an account runs `taskiem_people_reports_detach` in its transaction, which sets them NULL with `detached_at`, so a report outlives its reporter. Reviewed reports carry `reviewed_by`, `reviewed_at`, `review_note`.
- **Held messages kept for review.** A message the checks hold (§8.4) is still refused to the sender with the same words and nothing reaches the recipient, but is now kept in `people_held`: the message as the sender meant it (words, card, place, file name; and the file) sealed under its own key (`people-held:{id}`), with the chat, reply, forwards and the checks' `reasons`, and its `state` `held`, `released` or `confirmed` with who and when.
- **Moderators' actions and appeals.** `people_moderation` (`warn`, `limit`, `suspend`, `close`; reason, operator, report, `until`, lifted or closed, `seen_at`) and `people_appeals` (one per action; `open`, `upheld`, `overturned`, with the decision's note). `person_profiles.suspended` is set while a suspension or closure is in force (`taskiem_people_standing_sync`) and keeps `active` false whatever `users.status` says, so 00477's and 00478's suspended state (`suspended` in the lists and chats, 409 "This account can't get messages right now." to writers) is what moderators set. The profile trigger keeps it when the user row changes.
- **Lawful requests** in `lawful_requests` (authority, reference, legal basis, the person, the dates, one other person or all, files or not, a note, received; recorded by, approved by, a second operator by a check constraint; answered, refused, messages given; told or why not). The app role reaches none of it; only `taskiem_lawful_request_counts()` leaves the console.
- **Every moderator's function** (`taskiem_ops_people_*`, `taskiem_ops_customer_*`, `taskiem_ops_report_review`, `taskiem_ops_lawful_*`, the safety job's) is a definer owned by `taskiem_dispatch` (by `taskiem_safety` since 00481: Second review fixes, server) that refuses unless the platform chain is in scope (`taskiem_moderator_check`, the operator console's transaction, as Discover's moderation). They answer no words in lists.
- **Retention** (`taskiem_people_safety_purge`, from the safety job): reviewed reports (people and business) a year after review with their files (`peoplesafety.ReportsKept`); held messages 90 days after they were held or decided (`HeldKept`); the checks' verdicts 90 days after (they were kept until the account went). Each purge that removes something is audited `people.safety.purge`.

### The safety job

`Server.RunSafety` (every 10 minutes, in `serve`'s API role) runs `SafetyTick`: seals old plain reports, ends suspensions whose time ran out, closes accounts whose closure came (30 days, `AppealWindow`) with no open appeal (deleting the account as the person would, then `taskiem_people_closure_done`, audited `people.account.close` with the outcome), and purges by the retention above.

### Every human access is logged

Opening a report (`GET /v1/ops/people/reports/{id}?why=`), a file in it (`…/files/{message}?why=`), a report of a business (`GET /v1/ops/customer-reports/{id}?why=`), a held message (`GET /v1/ops/people/held/{id}?why=`) or its file (`…/file?why=`) needs a reason of 3 to 500 characters (400 `{why: true}` without) and is written to the platform audit chain, in the same transaction that reads it, as `ops.people.report.open`, `ops.people.report.file`, `ops.customer.report.open`, `ops.people.held.open`, `ops.people.held.file` with the operator (`operator:{email}`), the target, `why` and the time; nothing is shown if it cannot be written. A lawful export is audited `ops.lawful.export` with the reference, scope and counts before it is answered. Every decision below is audited under its step-up operation name. Files are answered `no-store`, `nosniff`, sandboxed, and downloaded unless they are photos. Tests prove each path writes its entry.

### The person's side (client API: the app and web must use these)

| Call | What |
| --- | --- |
| `GET /v1/me/standing` | `PersonStanding` `{state: ok\|warned\|limited\|suspended\|closing, words, until, unseen, appeal_words, actions: [{id, action, reason, at, until, in_force, lifted_at, seen, words, can_appeal, appeal: StandingAppeal\|null}]}`: what moderators decided, newest first (50). `state` is the strongest in force (a warning counts 30 days); `words` say it plainly; `unseen` counts decisions not yet seen. Personal sessions only |
| `POST /v1/me/standing/appeals` `{action_id, text}` | Appeals one decision once (text 1 to 2,000; 400 without). 201 `{appeal: StandingAppeal, said}`; 404 not theirs; 409 already appealed or no longer in force; 429 after five an hour. Audited `people.appeal` in the platform chain |
| `POST /v1/me/standing/seen` | 204: the notices are seen (`unseen` 0) |
| `GET /v1/transparency/lawful-requests` | For anyone: `{periods: [{year, half: 1\|2, label, received, answered, refused}], words}` by half-year (Lagos time), newest first; only half-years that have ended and had a request (the current one shows once it ends; no `pending`: Second review fixes, server) |

`StandingAppeal` is `{id, text, state: open|upheld|overturned, at, decided_at, note}`. **Telling the person:** each action, lift and appeal decision sends a push of kind `you` (title such as "Your account can't message people for now", the reason and its words, link `/you/settings/standing`, tag `standing`) when the person has push on You; the notice itself is the standing (`unseen > 0`), which You shows until `seen`. Operation ids: `getMyStanding`, `appealMyStanding`, `seenMyStanding`, `getLawfulRequestCounts` (`mobile/lib/api/api.g.dart`). A suspended person's own send or new chat answers 403 "Taskiem's moderators have stopped your account from messaging people for now. See Your standing in Settings to find out why and to appeal."

### Moderators' API (operator console; not in the public OpenAPI)

All under `/v1/ops`, with the operator's session; every write carries `step_up` (a passkey assertion for the operation and target), 403 `{step_up: "passkey"}` without.

| Call | Step-up operation (target) |
| --- | --- |
| `GET /people/overview`: open counts and storage | — |
| `GET /people/reports?status=open\|reviewed\|all`, `GET /customer-reports?status=` (no words) | — |
| `GET /people/reports/{id}?why=` `{report, messages, files, sealed}`; `GET …/files/{message}?why=` | — (audited) |
| `GET /customer-reports/{id}?why=` | — (audited) |
| `POST /people/reports/{id}/review`, `POST /customer-reports/{id}/review` `{note}` | `ops.people.report.review`, `ops.customer_report.review` (report) |
| `GET /people/held?state=held\|released\|confirmed\|all`; `GET /people/held/{id}?why=`; `GET …/file?why=` | — (opening audited) |
| `POST /people/held/{id}/release\|confirm` `{note}`: release sends it then as the sender meant, past the checks and limits | `ops.people.held.release\|confirm` (held id) |
| `GET /people/verdicts` (no words) | — |
| `GET /people/persons/{id}`: name, state, signals, actions with appeals; never chats | — |
| `POST /people/persons/{id}/actions` `{action, reason, days?, report_id?}`: a limit 7 days by default, a suspension for days or until lifted (0), a closure 30 days on | `ops.people.warn\|limit\|suspend\|close` (person) |
| `POST /people/actions/{id}/lift` `{reason}` | `ops.people.lift` (action) |
| `GET /people/appeals?state=`; `POST /people/appeals/{id}/uphold\|overturn` `{note}` (required; the person is told it). The moderator who decided is refused (403) | `ops.people.appeal.uphold\|overturn` (appeal) |
| `GET /people/storage` | — |
| `GET /lawful-requests`, `GET /lawful-requests/{id}`; `POST /lawful-requests` (record); `POST …/{id}/approve` (a second operator: the recorder gets 403), `…/refuse {reason}`, `…/told {told_at}\|{reason}`, `…/export` (once approved: the scope asked, JSON, marks it answered) | `ops.lawful.record` (person), `ops.lawful.approve\|refuse\|told\|export` (request) |

### Abuse signals (§8.2, §8.4)

`peoplesafety.Allowance(Signals)` replaces `peoplechats.RequestAllowance` for new requests: waiting at once and sent in a day are 2 and 3 in the first week, 5 and 10 to 30 days, 8 and 15 to 90 days, then 10 and 20; plus half the accepted (up to 15) and the accepted (up to 40); a third (at least one) when three or more were refused and they are most of them; a third when two to four people reported the sender in 30 days, and one and one from five; one and one for a first-week account on a device three or more accounts signed in on (`sessions.device_id`, `taskiem_people_device_accounts`); one waiting and at most two a day while a moderator's limit is in force. `Slowed` (a first week, two or more reports, or a limit) gives the first week's new-chat and per-minute limits. A suspended person cannot start a chat or send.

### Storage watched (Consequences, §16)

`taskiem_people_storage_rows{table}` and `taskiem_people_storage_bytes{table}` (from the catalogue and statistics at scrape: no scan) for `people_messages`, `people_files`, `people_report_files` and `people_held`; a "People chats' storage" row in the Grafana dashboard; `TaskiemPeopleStorageLarge` (ticket: over 100 GiB together, or over 5 GiB more in a day) with its runbook in `docs/reliability.md`; and the figure on the console's People safety page.

**Should `people_files` leave Postgres?** Not for stage 5a. Files are sealed per person in `bytea`, at most 25 MB each, and deleting a chat or an account deletes them in the same transaction as the messages, which keeps "kept until the people delete them" exact and needs no second store's credentials or lifecycle. The cost is that every photo is in the database's backups, WAL and replicas: past roughly 100 GB of `people_files` (when the alert fires), or if the 5b load test shows backup, restore or replication lag growing with it, move the sealed blobs to object storage (the business chats' media store), keeping their keys, sizes and the per-person key in Postgres and deleting the blob when its row goes (a delete queue, as runs' archive does).

### Web

- **Operator console** (`web/src/ops/People.tsx`, `Lawful.tsx`; nav People safety and Lawful requests): `/ops/people` (what waits, storage), `/ops/people/reports` (people, or `?kind=business`; open, reviewed, all), `/ops/people/reports/:id` and `/ops/customer-reports/:id` (asked why first; messages oldest first; a photo shown only on Show the photo, a file downloaded, each opening recorded; Mark reviewed), `/ops/people/held` and `/ops/people/held/:id` (release or confirm; the latest verdicts), `/ops/people/persons/:id` (signals; warn, limit, suspend and close, asked again for suspend and close; lift), `/ops/people/appeals` (uphold or overturn with a note), `/ops/lawful` and `/ops/lawful/:id` (record; approve, disabled for its recorder; refuse; export as a file; told or why not). Every decision asks for the passkey.
- **You**: `/you/settings/standing` (Your standing: the state in words, each decision with its reason and appeal; opening it marks them seen), a notice at the top of You while `unseen > 0`, a Your standing row in Settings' Chats with people, and a Chats with people section on Privacy with the kept words and a link to `/transparency`.
- **`/transparency`**, signed out or in: the half-years with received, answered and refused.

### Docs

`docs/runbooks/people-moderators.md` (the moderators' runbook), `docs/runbooks/lawful-requests.md` (draft for counsel), `docs/legal/privacy-notice-people-chats-draft.md` and `docs/legal/terms-people-chats-draft.md` (marked drafts for the lawyer), and `docs/needs-people.md` PC1 to PC7 (the privacy notice and terms with a lawyer and what they must cover, legal review of the in-app words, the lawful-request process, an outside security review, app-store privacy labels, the moderation policy, moderators named).

### Tests

- `api/people_safety_test.go`: `TestPeopleReportsKeepEvidence` (sealed messages and photo, listed without words, opened only with a reason and audited, the file audited, surviving the chat's and the reporter's deletion detached, reviewed with a passkey once, purged a year after review, an old plain report sealed, business reports listed, opened, audited, reviewed and surviving their reporter, no tenant credential); `TestPeopleHeldForReview` (held and refused, kept sealed with reasons, listed and verdicts without words, opened and audited, released and delivered with its photo, confirmed, purged at 90 days with verdicts, gone with the sender's account); `TestPeopleModeration` (each action with its passkey, plain words, the suspended state seen by others as 409, the person's own sends refused, appeal once, the deciding moderator refused, overturned by another, a limit, lift, a closure held by an open appeal, upheld, closed by the job); `TestPeopleStandingPush` (a push of kind `you` with the reason and the standing link; `unseen`, then seen); `TestPeopleAbuseSignals`; `TestLawfulRequests` (record, the recorder refused approval, exactly the scope exported, audited, told or why not, the public counts); `TestPeopleStorageWatched`.
- `engine/peoplesafety`: `TestAllowance`, `TestBoxIsBoundToItsOwnerAndRow`, `TestHalfLabel`. `engine/db`: `TestPeopleSafetyMigrationUpDownUp`; definers and dispatch columns cover 00480. `engine/slo`: the alert, its rule test and the dashboard row regenerated.
- Web: `src/lib/peoplesafety.test.ts`; `e2e/ops-people.spec.ts` on the real server (a report opened with a reason and audited, reviewed, the person warned and suspended, the appeal refused to the deciding moderator and overturned by the other; a lawful request recorded, approved by a second moderator, exported, not-told recorded, counted on `/transparency`); `e2e/accessibility.spec.ts` (every new ops page, Your standing with its notice, and `/transparency`, at 390 and 360 px in both themes).

### Review fixes

Branch `stage5b-groups-fix`, on 7db3cce1, migration `00483_people_groups_review_fixes.sql` (00482 is not changed). Each finding of the review, with its test in `api/people_groups_review_test.go`:

1. **Stale join requests** (an admin's approve re-adding someone who left, past "Who can add me to groups"): a person's request to join goes whenever they join (a link join and an accepted invite in `taskiem_group_join` and `taskiem_group_accept`, an admin's add in `addPeopleTx`) and when they leave or are removed. Approving a request older than the person's latest `joined_at` or `left_at` lets no one in: the request goes and the admin is answered 409. The person takes their own request back: `DELETE /v1/me/group-links/{code}/join` (204, waiting or not; 404 no such link). Test `TestGroupsReviewJoinRequestsDoNotOutliveJoining`.
2. **A removed person back in by deleting their copy:** `group_removed` (group, person, by whom, when; backfilled from 00482's removed memberships) is kept when an admin removes someone and stays after they delete their copy; `taskiem_group_join` makes their link join wait for an admin while it is there. An admin's add, an approval or an invite they accept clears it; their account's deletion takes it. The membership guard keeps `left_at`, `left_how`, `removed_by` and `left_sealed` as they were once set (for admins too) until the person is let back in. Test `TestGroupsReviewRemovedStaysRemoved`.
3. **A block told by the link's preview:** `GET /v1/group-links/{code}` no longer has `approve` (nor does `taskiem_group_link_find` read it); members see `approve_joins` on the group. Test `TestGroupsReviewPreviewTellsNoBlock`.
4. **A link join racing turning the link off or replacing it:** `taskiem_group_join` takes the group's row first, then reads the link as it is now: 410 `gone`. Test `TestGroupsReviewJoinRacesLinkOff` (the admin's transaction holding the group while it turns the link off, and while it replaces it).
5. **Activity seen after leaving:** row-level security hides from someone who left every reaction and read or delivered mark (`group_reads`) made after their `left_at` (the reactions list and counts follow); their own messages' ticks and "Read by" are as they stood when they left (from `group_reads` up to then, of those in the group then); the live connection sends them no message changed after they left; blocking from a group takes only someone whose time in it overlapped theirs (anyone else is answered as an unknown id, 404). Test `TestGroupsReviewLeftSeesNothingNew`.
6. **Unseen invites outliving an account:** `taskiem_group_forget()` (a definer, refused while any session of the person is live, so only in the deletion's transaction after `taskiem_person_delete`) takes every invite to the person, the unseen ones across a block too, their requests and their `group_removed` rows. Test `TestGroupsReviewDeletionTakesHiddenInvites`.
7. **Row-level security holds the API's rules, not only who:** `taskiem_group_member_guard` applies the own-row rules to admins too (an admin may give up their own role, nothing more of their own row); another's row is an admin's only to add (a member, from now: `visible` from the transaction's start), remove (now, `left_how` removed, by them), let back (from now) or change a current member's role, never their own settings or marks; once `left_at` is set, `visible` must lie within the old `visible` less the time from `left_at` on. A new trigger `taskiem_group_guard` on `people_groups`: settings by admins, the name, description and photo by editors (A2), `last_message_at` only forward; deleting `people_groups` only by its last member; `group_photos` written by editors; `group_files` and `group_search` written and deleted by the message's sender in the group. Leaving and removal take one time for `left_at` and `visible`. Test `TestGroupsReviewRowLevelSecurityHoldsTheRules` (direct SQL as `taskiem_app` in each person's scope: refused, or no rows).
8. **Pushes at 256 members:** a send keeps only the group, message, title and words; after the commit, off the request (a goroutine, at most 256 at once, else on the request's own; never dropped), `taskiem_group_push` picks who in one statement (members now who see the message, not muting it or mentioned with mention pushes on, `groups` pushes on, no block either way, a live device for You) and queues them in `push_outbox` for the existing push sender, once per message (`group_pushed`). Forwarding to several groups queues each the same way. Test `TestGroupsReviewPushInOneStatement` (six members: mute, a mention through mute, mentions off, `groups` off, a block, once only, not another's message, a forward to two groups), with `TestGroupsPush`.
9. **Live ticks:** `group_members.marks_at` moves only with a member's read or delivered mark or their time in the group (`taskiem_group_marks_changed`); the receipts look at it, so another member's mute, archive or mentions wakes no one. Only messages not yet shown read are recomputed, and each connection keeps a group's read-ticks members for 30 seconds instead of reading every member's card on each tick. Test `TestGroupsReviewMuteWakesNoTicks` (no event and no recomputing for others' settings; reads still turn the ticks).
10. **Adding many:** `addPeopleTx` reads who is or was in the group and who are the admin's contacts in two queries (`people.ContactsAmongTx`), and clears invites, requests and removals of those added in one statement each. Test `TestGroupsReviewAddManyAtOnce`.

`engine/db`: `TestPeopleGroupsReviewMigrationUpDownUp`; `definers.tsv` lists `taskiem_group_forget()` and `taskiem_group_push(…)`; `dispatch_columns.txt` the columns `taskiem_group_push` reads (`group_messages`' id, sender, kind, time and mentions; `group_members.muted`, `mentions`; `person_notify.groups`; `push_subscriptions`' kind, kinds, failure times and device) and `group_removed`'s.

**For clients:** the preview loses `approve`; `DELETE /v1/me/group-links/{code}/join` is new (a "Cancel request" on the waiting screen); approve may answer 409 for a stale request.

### Left over

- **The app**: Your standing (`/you/settings/standing` in its router, from the push's link), the notice on You, the appeal sheet, and a link to the published count, using the calls above (generated in `api.g.dart`).
- The people items PC1 to PC7 in `docs/needs-people.md`; the moderation policy (PC6) replaces the runbook's interim rules.
- Held messages are seeded only by the server (they are sealed), so the real-server e2e covers reports, actions, appeals and lawful requests; held messages are covered by the Go test and the stood-in accessibility test.
- A closure deletes the account as the person would; there is no separate ban on signing up again with the same number beyond the abuse signals.

### Second review fixes (server)

Branch `stage5a-rev2-server`, on the stage 5a PR head (200e6529): the server's findings of the second review of stage 5a. Migration `00481_people_review_fixes_2.sql`.

**Closures (S1, S2).** A closure is carried out by `Server.closeAccount`, in one transaction: `taskiem_people_closure_due(action)` locks the closure (`FOR UPDATE`) and checks it is still due (not lifted, not carried out, its day come, no open appeal, the last read after the lock), then the users row is locked, the account deleted as the person would delete it (`deletePersonTx`), the closure marked done (`taskiem_people_closure_done`) and audited `people.account.close`, all or nothing. An account already deleted is marked done with that note. If the deletion fails, nothing is kept: the closure stays in force, `taskiem_people_standing_sync` keeps a closure in force until `users.deleted_at` is set (so the person stays suspended, writes to no one and is shown suspended), the failure is audited `people.account.close.failed` with the error, and the next tick (10 minutes) tries again. Appealing (`POST /v1/me/standing/appeals`), lifting (`taskiem_ops_people_lift`) and deciding an appeal (`taskiem_ops_people_appeal_decide`) take the same lock first, so an appeal made as the closing day comes either holds the closure or is refused, and a closure being carried out finishes before a lift or an overturn, which then answers 409. The closure's lock is taken before the account's, because an appeal's row holds a key share of the account. An appeal of a closure whose day has passed is refused (409 "The time to appeal the closing of your account has ended."), and `can_appeal` is false for it; of a limit or suspension that has ended, 409 "no longer in force".

**A suspended person reaches no one (S3).** Wherever another person's copy would be written, a person whose account is suspended (or closing, or not active) is refused: a reaction (`PUT …/messages/{message}` with `reaction`), marking a share paid (`POST …/paid`) and accepting a request (`POST …/accept`) answer **403** with `errPeopleYouSuspended`'s words ("Taskiem's moderators have stopped your account from messaging people for now. See Your standing in Settings…"), as sending already did. Typing answers 204 and writes nothing; reading marks the person's own copy only; a star is their own. Deleting their own message for everyone still removes it from the other's copy (a suspension stops messaging, not forgetting). `taskiem_people_reach` also refuses a caller whose own account is not active (hint `self`).

**The reach answers (C1).** `taskiem_people_reach_try(chat, forget)` answers `(peer, state, reached)` instead of raising, `state` one of `ok`, `none`, `gone`, `suspended` (the other) or `self` (the caller); the other's copy is put in reach when `ok`, and with `forget` also past a suspension on either side, never to a deleted account. `peopleReachTx` uses it, so a refusal no longer aborts the caller's transaction (it was a 500 for read, react, delete for everyone, mark paid, accept, typing and the number lookup with a deleted or suspended peer). With the other person deleted or suspended: reading, reacting, marking paid, delete for me and opening the chat work on the person's own copy; typing is 204; delete for everyone removes the message from a suspended person's copy, with its search tokens (`peopleForgetReachTx`), and a deleted account has no copy; accepting a suspended stranger's request works, and their copy shows it accepted (they still write to no one).

**Reports counted once (S4).** `people_reports.reporter_mark` is the server's keyed mark of the reporter (HMAC-SHA-256 of their id with a key derived from the phone index key; plain SHA-256 where none is set up), written at report time and kept when the reporter deletes their account (`taskiem_people_reports_detach(person, mark)` fills it on older reports); `peer_wrote` says whether the reported person had written to the reporter (the reporter's copy's `peer_wrote_at`). The sender's signals (`taskiem_people_sender_signals`) count each reporter once (`taskiem_people_reporter(mark, person, id)`), whatever the number of reports, and only reports of a person who wrote to the reporter: one account reporting someone who never wrote to it, many times, then deleting itself, weighs nothing. Moderators' counts (`reports_against`) count each reporter once too, all reports. Reports made before 00481 count as written to (`peer_wrote` true).

**The safety tables' own role (S5).** `taskiem_safety` (NOLOGIN, NOBYPASSRLS, a member of `taskiem_dispatch` so it reads what every definer reads, with no members; the migrator is granted it only when it is not a superuser, to hand it the definers) owns every moderator's and the safety job's definer and `taskiem_people_standing_sync`, and alone has privileges and policies (`safety_all`) on `people_reports`, `customer_reports`, `people_report_files`, `people_held`, `people_moderation`, `people_appeals` and `lawful_requests`. `taskiem_dispatch`, and every login that is a member of it, no longer reads or changes any of them: their columns left `dispatch_columns.txt`, and `TestDispatchRoleCannotReadPayloads` checks report and appeal words, actions' reasons, held messages, sealed evidence and keys, lawful requests, and writes, and that no login (and neither app role) is a member of `taskiem_safety`. **A person's report of a business** is sealed at rest as a people report is: the messages under the report's own key (`customer-report:{id}`, `customer_reports.sealed`, `box_key`, `kms_key`, `message_count`; `messages` `'[]'`), opened by the server for `GET /v1/ops/customer-reports/{id}` (audited as before); older ones are sealed by the safety job (`taskiem_ops_customer_reports_unsealed`, `taskiem_ops_customer_report_seal`), 100 a tick.

**The published count (S6).** `taskiem_lawful_request_counts()` answers only half-years that have ended (Lagos time), and no `pending`; `GET /v1/transparency/lawful-requests` dropped `pending` (OpenAPI and `api.g.dart` regenerated) and its `words` say "A half-year is shown once it has ended."

**Search (C2, C3).** The backfill keeps its progress in `people_search_ready` (`through_at`, `through_id`; `done_at` null until finished, then set), so one cut short goes on from there. Each batch's messages are held `FOR SHARE` until it commits, and `forgetMessageTx` empties the message before removing its files and tokens, so a delete for everyone during a backfill waits for it and then removes what it indexed (or the backfill skips a message already emptied), and a delete for me during one waits instead of failing the backfill's insert (it was a 500). What the index reveals is said above (Search) and in `engine/peoplechats/search.go`.

**The sweep (C4).** The safety job also runs `taskiem_people_sweep(liveStale)` (owned by `taskiem_dispatch`, platform chain in scope; `taskiem_dispatch` may read `people_gone.at`, `people_typing.until`, `person_live.seen_at` and delete those rows, nothing else of them): gone records older than an hour, typing marks past their time and live connections not beaten for 2 minutes, for everyone.

**Presence and typing (C5).** `liveClose` marks the person away when their last connection in front closes unless the account was deleted (it skipped a person suspended meanwhile, who stayed online). Typing's in-memory limit is each person's and chat's own (`ownLimiter`): with the limiters full it never falls back to one bucket shared by everyone; the mark's own time limits it then (`people_typing` is refreshed only when it is due to end within 3 seconds, so at most every 3 seconds either way). The presence wording above now says what the code does: connections are counted across servers.

**For the web and the app.**
- A suspended person's reaction, mark paid and accept answer 403 with the standing words (as sending does): show them, with the link to Your standing, and do not retry.
- `GET /v1/transparency/lawful-requests`: drop `pending`; show `words` (the current half-year shows once it ends).
- `POST /v1/me/standing/appeals` may answer 409 "The time to appeal the closing of your account has ended."; `can_appeal` is false then.

**Tests.** `api/people_rev2_test.go`: `TestPeoplePeerAway` (deleted and suspended: typing, read, react, mark paid, opening the chat, accept, delete for me, delete for everyone, with the suspended copy forgetting and nothing else reaching it), `TestPeopleSuspendedReachesNoOne`, `TestPeopleClosureRetried` (a failing deletion, twice: still closing, recorded, then closed), `TestPeopleClosureRaces` (a late appeal refused; an appeal made while the job waits for the closure's lock holds it; carried out after upholding; no lift after), `TestPeopleReportsCountOnce` (the report-then-delete attack; two reporters, one twice and one deleted, count two), `TestCustomerReportsSealed`, `TestPeopleSweep`, `TestPeopleLiveAndTypingLimits`; `api/people_rev2_search_test.go`: `TestPeopleSearchBackfill` (resumed from its cursor; delete for everyone and delete for me while a batch holds them). `TestLawfulRequests` (the current half-year left out, then shown once ended, no `pending`) and `TestPeopleAbuseSignals` follow. `engine/db`: `TestPeopleReviewFixes2MigrationUpDownUp`, `TestDispatchRoleCannotReadPayloads` extended, definers and dispatch columns.

**Left over.** A closure that keeps failing is retried every tick and audited each time until someone looks (`people.account.close.failed`). The reporter mark falls back to plain SHA-256 where the phone index key is not set up, and changes if that key is first set up later (a reporter may then count twice across the change). Requests waiting in a published half-year are counted as received only (no `pending`).

## As built: stage 5a gaps, app

Branch `stage5a-gaps-app`, on 4691022d (stage 5a with the server's gaps, chat features and safety joined): the Flutter app's side of every gap left over above (the app's "Left over", the chat features' "For the web and the app", and safety's "The app"). Where this differs from the sections above, it holds. People chats are still kept by Taskiem, sealed at rest and readable by its servers; nothing added here says they are end-to-end encrypted.

**Generated client.** `mobile/api.txt` gains `typingPeople`, `searchPeople` and `starredPeople`, and `api.g.dart` was written again by `go run ./tools/mobilegen` (the live connection is read as a stream, not through the generated client).

**The live connection** (`lib/chat/people_live.dart`, `PeopleLive`).
- `GET /v1/me/live` read as server-sent events with the business chat's reader (`readSse`, `LiveTiming`, `lib/chat/live.dart`): `ready` (its `connection`), `message`, `chat`, `gone`, `typing`, `presence`, `ping`. Reopened after a drop or a 30-second silence with `Last-Event-ID` (the last event's id); a 429 waits at least 30 seconds; what came twice is merged by id.
- You (`YouHome`) holds one per session (`PeopleLive.hold`/`of`, so screens opened from links and pushes find it); a business session has none and polls as before.
- Open only while the app is in front: `Presence` (`lib/app/presence.dart`) starts it on resuming and, on going to the background, says `POST /v1/me/presence {online: false, connection}` and closes it. When `ready` comes in front it says `{online: true, connection}`. The 30-second heartbeat (`{online}` alone) runs only while the connection is not open (refused, offline, not yet ready), and once at once on coming back.
- A chat (`PeopleChatData`): a `message` is merged (and read, while the chat is seen), `gone` takes one away (or, for a request, the whole chat), `chat` replaces the head or says it is no longer here; `typing` and `presence` show in the head (`withPresence` lays what the connection said over the person's card).
- The Chats list (`MyChats`) and Requests (`PeopleRequestsScreen`) read again at once on an event that concerns them (a burst once); rows show "typing…" and the online dot from the connection.
- Polling stays as the fallback: while connected, a chat, the list and Requests read again only every 30 seconds (`SlowWhileLive`), to reconcile; otherwise at their usual pace (Requests now polls every 10 seconds too).

**Typing.** While the box changes, `POST /v1/me/people/{id}/typing` at most every 3 seconds (`typingBeat`), `{typing: false}` when it empties; a send ends it. Never from a request, a waiting, blocked, deleted or suspended chat. "typing…" in the chat's head (with the typing bubble) and on the list's row.

**Search and starred** (`lib/chat/people_search.dart`).
- Search from the bar at the top of You's Chats (Search chats with people; not a fourth head button, which large text cut off) and from a chat's ⋯ (Search this chat, `chat=`). Searched 400 ms after typing rests, only with a word of two letters (`searchable`); 30 a page by `before` (Show more as the end comes into view); each row the chat's name, who wrote it and the `snippet`. The server's refusals said (`searchFailure`).
- Starred messages (`GET /v1/me/people/starred`), from the search screen, read again after one is opened.
- A hit opens its chat at the message: `PeopleChatData(focus:)` reads older pages until it is there (at most 40), then the thread is placed at it and flashes it (`ThreadView.focus`). Inside a chat, a hit jumps there (`jump`, the thread placed again).

**The trust line on the one list.** A business row with `verified` says it in the head's words (`listTrustWords`, through `verifiedWords`: "Phone checked · Payments through Taskiem · Trading 8 months") over its last line, and a screen reader hears it.

**A place** (`lib/chat/people_place.dart`).
- The chat's + has **A place**. Only then is the phone asked (geolocator 14.1.2; `ACCESS_COARSE_LOCATION`/`ACCESS_FINE_LOCATION`, `NSLocationWhenInUseUsageDescription`), and it is read once.
- The place is shown before it goes (where, how close, See it on the map), with an optional name (200 characters), and sent as `location` with a client id.
- Refused, refused for good, the phone's location off, or not found: plain words (`placeProblemWords`), Try again, and the phone's settings where they help.
- A place in a chat opens in the phone's map app (`geo:` on Android, Apple Maps on an iPhone, the web's map when nothing takes it).
- The phone's location is behind `PlaceFinder`.

**Sharing from outside a chat** (§10.1), into chats picked (`shareWithPeople`):
- Discover's profile: Share with someone (the business's card).
- A product's Share in a business's shop (in its chat).
- An order's page: Share the delivery, only once it has a delivery (a rider). The order itself was already shared from there.

**A QR code scanned in the app** (`lib/chat/people_scan.dart`).
- New chat's By their link has **Scan their QR code**. The camera (mobile_scanner 7.5.0; `CAMERA`, the camera words) is asked for only then.
- A code that is a person's Taskiem link (`scannedPersonCode`) opens who it is (`PersonLinkScreen`); anything else says so.
- Beside it: type, **Paste** (the clipboard's link) and Open the link.
- The camera is behind `QrScanner`.

**Contacts checked again** (§5.3).
- Once the person agrees and their contacts are checked, the day is kept (`Prefs.contactsChecked`, Lagos's day; forgotten on signing out).
- When You opens, they are checked again by themselves (`recheckContacts`) only if that day is kept and is not today, and the phone still allows reading them without asking (`PhoneContacts.allowed`).
- The people found who are not in the person's contacts are offered on Chats ("N of your contacts are on Taskiem"), opening the same found list. They are kept in memory only, until You closes or the list is opened.
- The explainer and the found list say so: the day is all that is kept.

**Delete for everyone** says over its choices that copies, forwards or screenshots they made before stay (`deleteForEveryoneNote`, `showTkModalChoices(note:)`).

**Your standing** (`lib/chat/people_standing.dart`), in the web's words.
- `YourStandingScreen` reads `GET /v1/me/standing`: the state's title and words, then each decision with when, why, whether it is in force or lifted, its appeal's outcome, and **Appeal** while `can_appeal`. Opened with unseen decisions, it says `POST /v1/me/standing/seen`.
- The appeal sheet (`POST /v1/me/standing/appeals`) needs words. Its answers are said plainly (`appealFailure`): 400 write why, 404 no longer on the account, 409 already appealed or no longer in force, 429 many this hour. Sent, the server's `said` is toasted.
- The notice heads You's Chats while `unseen > 0`. It is read quietly (nothing shown when it cannot be read), and opened, it goes.
- `/you/settings/standing` is a page of You (`YouPage.standing`): a push of kind `you` (tag `standing`) opens it on You, or over a business's home. Settings' You and people has Your standing.
- A send refused with the suspended person's 403 shows the server's words and a note with **Your standing** (`pointsToStanding`); starting a chat refused so toasts with Your standing.
- Privacy with people and Your standing link to the published count, `/transparency` on the web (`openTransparency`).

**Tests.**
- `mobile/test/people_gaps_test.dart`:
  - the connection's events and `Last-Event-ID`, a 429 waiting;
  - presence on the connection, and the heartbeat only without it;
  - a chat live (a message read as it comes, a reaction, gone, typing, online, hidden);
  - slow polling while connected; typing throttled and stopped, never in a request;
  - the list's trust line, typing and online, read again once on a burst; Requests live;
  - search (a word of two letters, once when it rests, pages, snippets, opening at the message), its refusal; starred; a chat opened at a message by older pages; search this chat jumping;
  - a place refused, refused for good, allowed, sent and opened in maps, and the map links;
  - the scanner (only on tap, put away, a person's link) and Paste;
  - the daily re-check's guard (never agreed, today, no longer allowed, Lagos's day) and You offering what it found once a day;
  - delete for everyone's words;
  - Your standing, the appeal's refusals and sending once, the notice, the push's link, a suspended sender's 403, the published count from Privacy.
- `mobile/test/people_share_gaps_test.dart`: a business from Discover, a product from its shop, a delivery from an order (only with a rider).
- `mobile/test/notify_test.dart`: a push of kind `you` to `/you/settings/standing` routed.

**Left over.**
- The connection carries people chats only (the server's); You's business chats keep their polling.
- The place is not drawn on a map in the app (no map tiles); the preview gives where it is and See it on the map opens the phone's map app.
- Typing does not follow "Last seen and online" (the server's rule).
- The scanner and the location were tested through their interfaces; on a phone they need a device run.

### Second review fixes (app)

Branch `stage5a-rev2-app`, on 200e6529: the app's findings of the second stage 5a review. Behaviour only: no screen changes how it looks (the founder will redesign them, `docs/spec/app-screens-to-redesign.md`); the one new row uses the switch row Privacy with people already has. Each has a test in `mobile/test/people_review2_test.dart` (the camera's also in `order_view_test.dart`, `team_pages_test.dart` and `customer_app_test.dart`). Where this list differs from the sections above, it holds.

- **A live change to an old message.** A message the connection tells (a reaction, a read, a star, a delete, or a reconnect's replay) is merged into the chat only when it is loaded already, is not older than the oldest loaded from the server, or the chat has no older pages. Before, one far back joined the loaded thread, and Load older (and opening at a search's or a star's hit) paged from it, skipping everything between. Those heard before the first page wait for it and are merged by the same rule.
- **The camera or photos refused.** CAMERA (for the scanner; mobile_scanner declares it too) stays. Declared, it makes Android's image picker ask for the camera before every photo taken and throw `camera_access_denied` when refused (an iPhone throws the same once refused). Every place that takes or picks a photo now catches it: the composer's camera and clip, a product's photos, a delivery's proof, a task done with a photo (which threw uncaught and left the task silently not done), a people chat's and a customer thread's + A photo. The words are plain (`pickProblemWords`: not allowed, no camera, or it did not open) in an error toast, with **Open settings** (the phone's settings for Taskiem) when refused (`lib/chat/pick_problem.dart`, `sayPickProblem`).
- **The connection ended cleanly.** The server ends each connection after 30 minutes. A clean end after `ready` now reopens at once, spread by up to half a second; only errors back off, from the server's `retry:` (2 seconds, read by `readSse`'s `onRetry`), doubling while errors go on, up to 30 seconds (a 429 still waits at least 30). A connection that ends before it is ready counts as an error, so nothing loops.
- **Presence from the connection** is cleared when it closes and used only while it is connected (`PeopleLive.presenceOf`; a `presence` event before `ready` is passed over): a chat's head and the list's dot fall back to what the chat's own read says.
- **The daily contacts re-check goes round.** With more numbers than `max_per_day`, each day's re-check takes that many starting further round the sorted numbers, worked out from the day alone (`dayShare`), so over the days all are checked; nothing more than the day is kept.
- **Check my contacts daily.** Privacy with people has the switch (on this phone, not the server's): on while the day of the last check is kept; off forgets it (`Prefs.setContactsChecked(null)`), and nothing is checked by itself. It comes on again only by checking From your contacts, after the explainer. The explainer and iOS's `NSContactsUsageDescription` say the daily re-check and where to turn it off.
- **Covered screens read once.** You's Chats and Requests no longer read again on every live event while a chat or request covers them: the event marks them stale, and they read once when on top again (`Polling.readWhenSeen`, told through `ModalRoute.isCurrentOf`) or when the app comes back.
- **Search's Show more** pages the words last searched, not new words typed but not yet searched.
- **Typing.** `{typing: false}` when the chat closes or the app goes to the background with words in the box; words given back after a failed send are not typing; nothing is said while the person's own account is stopped from messaging people (`toStanding`); the beat is 2.5 seconds (`typingBeat`), under the server's 3.
- **Words.** The location words (iOS's `NSLocationWhenInUseUsageDescription`, the Android manifest's comment) say "A place", as the + menu does. The standing notice on Chats reads again once Your standing marked its decisions seen, however it was opened (its notice, a push, Settings: `standingSeen`).

**Left over.** Typing from a suspended sender writing nothing, and the lawful counts' `pending`, are the server's (a parallel stream). Whether Android's camera refusal is for good cannot be told without a permissions plugin: Open settings is offered on every refusal (the next tap asks again where Android still would).

## As built: stage 5a gaps, web

Branch `stage5a-gaps-web`, on 4691022d (stage 5a with the server's gap work, chat features and safety joined): the web's part of what stage 5a left over, on You, using the calls of "As built: stage 5a gaps, chat features (server)" and "…, safety and moderation" as they are. No server change was needed. Where this section differs from "As built: stage 5a, web" (its **Left over** list above all), it holds.

**You's live connection** (`web/src/pages/PeopleLive.tsx`, `PeopleLiveProvider` around `YouApp`): one `EventSource` to `GET /v1/me/live` while the person is signed in on You, opened with `?away=1` when the tab is hidden.
- `message` puts the message in the open chat at once (`threadWithLive`: our own message told before its send was answered takes the place of the copy on its way, so it shows once), and marks theirs read while the tab is seen; `gone` takes a message out (`threadWithGone`), or leaves a request deleted on another device; `chat` puts the chat's row in its place on the one Chats list (`rowsWithChat`: newest first, requests and the other archive part kept out, business rows left as they are) and in Requests (`requestsWithChat`, `requestsWithGone`), and the open chat reads its head again; the list reads its first page again half a second later for what is the whole list's (unread, requests).
- **Polling stays as the fallback**: the open chat every 4 seconds and the lists every 8 while the connection is not open; once a minute while it is (`livePoll`). A connection silent for 30 seconds is closed and opened again; one refused (429) is tried again after 30 seconds. The browser's own reconnection (the server ends each connection after half an hour) sends `Last-Event-ID`, so what was missed comes then; one the page opens itself sends `ready` to the pages, which read their first page again.
- **Presence**: the `ready` event's `connection` is kept; on the tab hiding `POST /v1/me/presence {online: false, connection}`, on its showing `{online: true, connection}`, and on leaving the page the same `false` with `keepalive`. `presence` events change online and last seen in the open chat's head (`chatWithPresence`). The 30-second heartbeat is not sent while connected (the server Touches on every people request as before).

**Typing**: the composer's changes say `POST /v1/me/people/{id}/typing` (`TypingGate`: at once, then at most every 3 seconds while the words change; `{typing: false}` once when the box empties and on leaving the chat; a send ends it), only in a chat that takes messages and is not a request or waiting. The chat's head shows "typing…" (`people-typing`) and the Chats list's row its preview "typing…" (`people-row-typing`) while the chat is in the last `typing` event.

**Search** (`web/src/pages/PeopleSearch.tsx`, `/you/people/search`): "Search messages" on the Chats list, and "Search in this chat" in a chat's menu (`?chat=&name=`). Searched 0.4 seconds after the typing stops when the words have one of two letters (`searchable`), kept in the address; rows of the chat's name, the time and the snippet with the words searched for in bold (`snippetParts`, accents and case aside), "N messages found" (so far, while there are more); the next page (`before`) when the list's end comes into view. A row opens the chat at the message (`foundPath`: `/you/people/{chat}?at={message}`, a request's in Requests).

**Opening a chat at a message** (`?at=`): the chat reads older pages until the message is in (at most 20, `FIND_PAGES`), is placed with it in the middle and flashes it (the thread's `reset(id)` and `flashId`); "That message is no longer in this chat." when it is not found.

**Starred** (`/you/people/starred`, from the Chats list's Starred and Settings' "Starred messages" under Chats with people): `GET /v1/me/people/starred`, the latest starred first, a page at a time; each opens its chat at the message.

**The trust line** on a business row of the one Chats list: `ChatListItem.verified` in the business chat head's words (`trustWords`: "Phone checked · Business checked · Payments through Taskiem · Trading 3 months"), a line under the preview (`ChatRow`'s `trust`, `chat-row-trust`).

**Sending a place** (the share sheet's "A place", `PlaceSheet`): the browser's location is asked for only on "Use where I am now"; what was found is shown before it is sent (the figures, how close, and "Check it on a map"), with an optional name (up to 200 letters, `checkPlace`), then `POST …/messages {location: {lat, lng, label?}}`. Refused, unavailable or too slow is said in plain words (`placeProblem`) with Try again; a refused send is said above the composer as other refusals are. A place in a chat is its name (or "A place") with "See it on a map", opening OpenStreetMap (`mapURL`) in a new tab. A new `place` icon (a map marker) is used for it; `pin` stays the pushpin.

**The push preview** (You's Settings, Notifications, "On this browser", `YouBrowserPush` in `web/src/pages/YouAccount.tsx`): offered where the browser and the server can push. "Notify me on this browser" registers it as the person's own device (`enableYouPush(true)`); once it is, "Show what the message says" is its preview, read from the server (`findYouDevice`, `PUT /v1/me/push?side=you {known: true}`) and changed with `PATCH /v1/me/push/{id}?side=you {preview}` (`setYouPreview`), with the words of each (`previewWords`: off, a notification says only "Someone wrote to you").

**Safety links** (checked): Your standing is a row of Settings' Chats with people and the notice on You (both from the safety stream); the privacy page (`/you/settings/privacy`) and Privacy with people (`/you/settings/people`) now link both Your standing and `/transparency`. A send refused because the sender is suspended (403, the server's words name Your standing) shows "Open Your standing" beside the words (`people-problem-standing`).

**Delete for everyone** still says "A copy or a screenshot they made before stays." (checked).

**Tests.**
- `web/src/lib/peoplelive.test.ts`: the live reducers (a message added and changed, another chat's left alone, our own shown once, gone, rows in place newest first with businesses kept, requests and archived kept apart, Requests, presence), the typing gate (at once, every 3 seconds, stopped once, after a send, on leaving), search (what may be searched, addresses, pages without repeats, where a row opens, the count's words, the snippet's marks with accents), places (on the earth, the label's length, the problems' plain words, the map and figures). `plainwords.test.ts` now covers `People.tsx`, `PeopleChat.tsx`, `PeopleLive.tsx` and `PeopleSearch.tsx`.
- `web/e2e/people.spec.ts`, a new last test on the real server: with Bola's list reading nothing but the live connection, Ada's typing shows on Bola's row and goes when the box empties, and her message changes the row; in the chat, with its own reading stopped, "typing…" in the head and the message live; the tab hiding and showing says away and in front for its connection; a search for "zob" finds the message with "zobo" in bold, a word not said finds nothing, and the row opens the chat flashing the message in view; a star, then Settings' Starred messages opens it again; a place sent from a browser at a fixed position (granted), seen and named first, shows on the other side with its map link, and a browser that refuses says so; a suspended sender's refusal (stood in) links Your standing; Ada's business row carries "Phone checked · Business checked · New on Taskiem"; the privacy page's links; a new person's browser (push stood in as `chat-push.spec.ts` does) turns notifications on with the preview, turns it off (`PATCH`, kept on the server) and finds it off after a reload. Axe in both themes at each new step, 390 and 360 px.
- `web/e2e/accessibility.spec.ts`: the Chats list with a business's trust line, a place in a chat, the place sheet asked and found, the search page (all and in one chat) and the starred list, in both themes at 390 and 360 px; You's settings with "Notify me on this browser". The You tests stand in `/v1/me/push/key` (a 404, or a key where the browser's part is drawn), since it is now read on Settings.

**Left over.**
- The live connection carries people chats only (the server's left over): You's business chats keep their polling.
- The open chat reads its head again on a `chat` event rather than taking the event's `PeopleChat` (one more request).
- Typing is said once per change: a single paste shows "typing…" for the server's 6 seconds, and a page reopened within 3 seconds of the last mark is not passed on until the next change after them (the server's limit per chat).
- Search pages of found messages are read on Show more or scroll; a search is not kept across devices. Sending a place picks only where the person is now, not a place on a map.

### Second review fixes (web)

Branch `stage5a-rev2-web`, on 200e6529: the web findings of the second stage 5a review. No server change. Where this list differs from the web's sections above, it holds.

- **A live event for an old message** (a reaction, star or delete on one from months ago) changes the open thread only when the message is in it; one not in it is added only when it is not older than the oldest message read (`threadWithLive`). Before, it was put in, and the thread's older pages were then read from it, skipping everything between.
- **Presence after the browser's reconnection.** The browser reconnects to the address the connection was opened with, `?away=1` and all; `ready` now says in front or away whenever the tab differs from that address (`readySays`).
- **Presence is cleared** when the connection closes and on each `ready`, and given to the pages only while connected: a closed connection never leaves "online" in a chat's head.
- **The one Chats list's pace.** The live connection tells of people chats only, so while the list has a business row it is read every 8 seconds, connected or not (`chatsPoll`, `hasBusinessRow`); once a minute while connected only when it has none.
- **The notice of Your standing** is read again on each move between You's pages, and once Your standing says it was marked seen (`STANDING_SEEN`, a window event); it is not shown on Your standing itself (`standingNoticeShows`). It goes once read.
- **Opening at a message.** While the chat's first reading runs (reading older pages for `?at=`), a poll, a reconnection's `ready` and Accept's reading wait, so the jump is not lost. A message not found once 20 pages are read and more remain says "That message is further back in this chat than has been read so far." with **Keep looking** (20 pages more, placed at it when found); "That message is no longer in this chat." only once every page was read (`atFindState`, `AT_FIND_WORDS`).
- **Read on return.** Coming back to the tab reads the open chat at once, and marks what came while it was hidden read, rather than at the next look (a minute while connected).
- **Typing for screen readers.** Who is typing is said in a polite live region in the chat (`people-typing-live`) and on the Chats list (`typingWords`: "Ada is typing", "Ada and Bola are typing", "3 people are typing").
- **More rows** (`MoreRows`, now given `count`, the rows read so far) watches the list's end afresh after each page lands, so an end still in view brings the next page; a page that failed is not asked for again by itself.
- **A list's read against a live patch.** `usePagedList` numbers its reads (`ReadOrder`): an answer older than one shown is dropped, and one asked for before a live patch (Requests, the Chats list) is dropped and the list read again when no later read is on its way.
- **/transparency** shows half-years that have ended only, latest first (`endedHalves`), and says "Each half-year is counted here once it has ended. {the current half-year} shows here once it ends."; it no longer reads `pending` or adds the current half-year at zero (the server is dropping both).
- **The operator console.** A report's or held message's reason (`WhyGate`) is keyed by what is opened; an opening that fails says why with Try again, which asks the reason once more, kept to change. Lifting a decision asks why (the server's `reason`, in the audit) and is confirmed before the passkey, as the other decisions are. A lawful request's account ids are checked as ids before the passkey is asked for (`isAccountID`, said under the field). The export's file link is put in the page while it is clicked and let go a second later.

**Tests.** `web/src/lib/peoplelive.test.ts`: an old message left out and one read changed, presence only while connected (`shownPresence`), `readySays`, `chatsPoll`, `atFindState`, `typingWords`. `web/src/lib/usePagedList.test.ts`: `ReadOrder`. `web/src/lib/peoplesafety.test.ts`: `endedHalves`, `currentHalfWords`, `periodWords` without pending, `standingNoticeShows`, `isAccountID`. `web/e2e/people.spec.ts`, a new last test: a message that came while hidden is marked read on return; "Bola (school) is typing" in the polite region; the Chats list with a business row read at least twice in 17 seconds while live; a reconnection to `?away=1` (the stream stood in) said in front; opening at a message 6 slow pages back with polls running, flashed in view; one 25 pages back says it is further back, Keep looking finds it; pages of three read while the end stays in view. `accessibility.spec.ts`: the standing notice gone once Your standing is read; lifting a decision. `ops-people.spec.ts`: a failed opening retried with the reason kept; a limit lifted with a reason, not when the confirmation is dismissed, the reason in the audit; lawful ids that are not ids said before the passkey; `/transparency` without the current half-year.

**Left over.** Presence given only while connected is checked in the unit tests (`shownPresence`), not in a browser: a live stream stood in cannot stay open.

## As built: stage 5b, groups (server)

Branch `stage5b-groups-server`, on 67ed1dd4 (stage 5a done, with the 5b checklist and the founder's answers): the server's part of groups (§9, §13, §14, §17 Q7, Q16, Q17; [the 5b checklist](people-chatting-5b.md) A and D, with A2 to A8 as the founder settled them; decision 0069 point 7). Not in this slice (the next): reports of groups and members, moderators' actions on groups, the scam checks in groups (B1), limits on making groups and adding people who aren't contacts, lawful exports of group messages, storage watching of groups, and ordering together. Migration `00482_people_groups.sql`; code in `api/people_groups.go` (groups, members, invites, links, deletion), `api/people_group_messages.go` (messages, read state, search, starred, forward) and `api/people_group_live.go` (the live connection), `engine/peoplechats/groups.go` (the group's key), `engine/people` (the new setting, group link codes) and `engine/push` (two kinds). Where this section differs from those above, it holds.

**Groups are kept by Taskiem, sealed at rest under each group's own key and readable by Taskiem's servers. They are not end-to-end encrypted, and nothing in the API says they are.**

### Data (00482)

- **One copy for the group.** `people_groups` (the name and description sealed together under the group's key, `admins_only_post`, `admins_only_edit` (on by default, A2), `approve_joins` (off by default, A4), `photo_at`, `last_message_at`), `group_keys` (the group's 32-byte data key, sealed with the platform's root key for that group alone, `Vault.SealPlatform` purpose `group-key:{id}`, as a person's key is), `group_messages` (one row per message: `sender_id`, `kind` text, media, card, location or event, `sealed` under the group's key bound to the group and the row `gm:{id}`, `event` {type, actor, target} for an event, `attachment`, `card_type`, `reply_to`, `forwards`, `client_id`, `mentions` (member ids, A7), `deleted_at`, `paid_at`, `paid_by`, `created_at` (`clock_timestamp()`, taken after the group's row is locked, so messages are in the order kept)), `group_files` (the bytes sealed `gf:{id}`), `group_photos` (sealed `gp`), `group_search` (keyed tokens, below), `group_typing`.
- **Each member's own marks.** `group_members` (one row per person who is or was in the group: `role` admin or member, `how` created, added, invite or link, `added_by`, `joined_at`, `left_at` and `left_how` left or removed, `removed_by`, **`visible`, a `tstzmultirange` of the times whose messages they see**, `muted`, `archived`, `mentions` (a mention pushes while muted), `hidden_at` (the chat deleted for them), `read_upto`, `delivered_upto`); `group_marks` (theirs alone: `starred_at`, `hidden_at` = deleted for me); `group_reactions` (one per member per message, seen by the members); `group_reads` (when each member's read or delivered mark moved, for "Read by"; written for reads only while the member's read ticks are on; a member's own rows older than 31 days go as new ones come).
- **Invites and joins.** `group_invites` (person, `invited_by`, `hidden`: an add across a block, kept so the admin sees it as any invite and never shown to the person, A8), `group_join_requests` (link joins waiting for an admin), `group_links` (the code's keyed hash and nonce, one live link per group, `replaced_at` for a replaced or turned off one).
- **`person_privacy.groups_add`** (`everyone`, `contacts` the default, `nobody`: "Who can add me to groups", A3); **`person_notify.groups`** (pushes of group messages); **`push_outbox`** kinds `group` and `group_invite`.
- **Row-level security does the seeing, not the API alone.** A person sees a group's messages (and their files, tokens and reactions, through the messages' own policy) only where their `visible` contains the message's time: from when they joined (§17 Q16), up to when they left or were removed (A5), less what they deleted for themselves (deleting the chat takes the time up to then out of it). Only a member in the group now writes (a message as themself, a reaction, typing, a file); only an admin adds a membership or changes another's (`taskiem_group_member_guard`: a member changes their own settings and marks, and may leave, never their role, their join or what they see, nor come back by themselves); someone invited sees the group's name, key and photo (never its messages) while an invite they may see waits; a business's door open reaches nothing. Definers (each in `definers.tsv`): `taskiem_group_member`, `taskiem_group_admin`, `taskiem_group_invited` (the policies'), `taskiem_group_create`, `taskiem_group_link_find`, `taskiem_group_link_photo`, `taskiem_group_join`, `taskiem_group_accept`, `taskiem_group_may_add`, `taskiem_group_phones`. Group ids are uuids apart from any business's, so the next slice's reports name `group_id` (and `person_id` for a member).
- **Locks.** Every writer of a group takes the group's row first (`FOR UPDATE`: a message, a membership or settings change, the link, a join or accept through the definers), so writes to one group are in one order; a transaction touching several groups (deleting an account) takes them in id order; nothing of a group is written in a transaction that holds a one-to-one conversation's lock (blocking from a group and forwarding are their own transactions), so 5a's order (conversation, then sender, then copies) is never crossed. `peopleTx`'s retries cover a deadlock as a backstop. Test: `TestGroupsAtOnce` (seven members sending five each and reading at once while the admin adds two: none fails, each sender's in order).

### Rules (the founder's answers and this slice's decisions)

- **Making a group:** a name (up to 80 characters) and a description (up to 500); the maker is its first member and admin; up to 256 people (§17 Q7) by any way in (409 "This group is full").
- **Admins** add and remove people, make and unmake admins (a group keeps one: 409), choose who posts (`admins_only_post`), change the settings, turn the link on, replace or turn it off, and let in or turn away people waiting. **Info** (name, description, photo) by admins only unless `admins_only_edit` is off, when every member may (A2). Each change is an event message ("Ada changed the group's info"). **The last admin leaving** makes the member there longest an admin ("Ada made Bola an admin").
- **Adding (A3):** a person is added at once only when the admin has them as a contact **and** their "Who can add me to groups" lets the admin (`contacts`: only people they added; `everyone`; `nobody`: no one). Otherwise an invite waits in their Requests (Accept, Decline; the inviter is not told a decline). The admin's answer per person is `added`, `invited`, `member`, `full` or `unavailable` (no such active account); nothing else about the person's settings.
- **Blocking (A8):** a person still sees a blocked member's group messages; the blocked person can't message them one to one (one tick, as 5a), can't add them to groups (the add is answered `invited` as any invite and kept as an invite they never see; asked again once the block is gone, it becomes an ordinary invite), and doesn't see their last seen or photo (people cards hide both ways across a block, as 5a). No push goes to a member from someone either of them blocked. **Block from a group** (`POST /v1/me/groups/{id}/members/{person}/block`) keeps the block as 5a's does, with the person's own (hidden, empty) one-to-one copy so it is listed in Blocked people and unblocked there. A link join by someone an admin of the group blocked, or removed before (kept so after they delete their copy, until an admin lets them back), waits for an admin (`waiting`, the same as "Approve new members"), which tells them nothing more; the link's public preview doesn't say whether joining waits.
- **Joining by the link (A4):** anyone holding the link sees the name, photo and member count (not the members); joining is at once by default, or waits for an admin with "Approve new members". A replaced or turned off link answers 410 (for 30 days, then 404). Joins are limited: 10 an hour a person (in memory) and 60 an hour a group (`groupLinkJoinsPerHour`, 429).
- **Leaving and removal (A5):** the copy up to then stays readable until the person deletes it (`DELETE /v1/me/groups/{id}`, which then removes their membership and marks for good); nothing new reaches them and they can't write, react or see the members (403); they see the group's name and description as they were when they left (`group_members.left_sealed`), no photo and no count. Someone in the group deleting the chat clears their copy up to now (their spans) and hides it until a new message. **The last one out closes the group**: all of it is deleted (§14), the copies of those who left with it.
- **Messages** are as 5a's: text up to 4,000 characters, photos, voice notes and files (engine/media's checks), places, cards (business, product, order, delivery, share), replies, reactions (one per member, counted), stars (each one's own), forward to and from groups (caps and marks as §8.2), delete for me, delete for everyone (the sender's own, within 24 hours, while in the group), `client_id`, 20 a minute (10 in a first week or while limited), 50 a page, the latest last. A suspended person sends, reacts, accepts, joins and makes groups nowhere (403, the standing's words). A share card in a group is marked paid by any member (`paid_by_me` says whose mark). The transfer warning: a share card naming an account from someone the reader has not added, or words asking for a transfer, show `warn_transfer`.
- **Read state (A6):** each member's `delivered_upto` moves when their app has the message (the messages page, the lists, their live connection) and `read_upto` on `POST …/read`. A sender's message is `delivered` once every member now in the group (who was in it when it was sent) has it, and `read` once every one of them with read ticks on, both ways as 5a (theirs and the sender's), has read it; it never turns read when none has ticks on. "Read by" (`…/info`, the sender only) lists only those members, with when; a member with ticks off who read it is listed only as having it.
- **Mentions (A7):** `mentions` on a message are member ids (kept only for members in the group now); a mentioned member is pushed even while they mute the group, unless they turned `mentions` off for it; `mentions_me` and the group's `mentioned` show it.
- **Numbers** stay hidden in groups unless a member chose to show theirs (`show_number`), never across a block (`taskiem_group_phones`). Names are as each member sees the others: the name they gave them in contacts, else their own; "Deleted account" once deleted; photos and last seen by each one's settings (`PersonCard`).
- **Deleting an account (§14, §17 Q17)** leaves every group in the deletion's transaction (the last admin hands on, the last one out closes it, an event "Deleted account left"), deletes every membership, mark, reaction, read time, invite and wait of the person's, and keeps their messages, shown as from "Deleted account".
- **Push (§13):** kind `group` (title the group's name, body "Ada Obi: words" or "Ada Obi mentioned you: words", link `/you/groups/{id}`, tag `group:{id}`) to members not muting it (and to those mentioned, as above), under their `groups` switch; kind `group_invite` (title the group's name, "Ada Obi invited you to the group", link `/you/groups/invites`) under their `requests` switch. A device's preview off says "Taskiem" and "New message in a group" or "You were invited to a group".
- **Search (D):** the index holds 16-byte HMAC tokens of each message's words' beginnings (as 5a's, `peoplechats.Tokens`), **keyed per group** (`GroupSearchKey`, from the group's own key), one set per message; a member's search hashes the query under each of their groups' keys and reads only messages they see (the tokens' policy goes through the messages'), then opens and checks each. **Deviation:** the checklist says "keyed per person"; one copy of tokens per member would be 256 index rows per word of every message. Keyed per group, a token is alike only within the group (never across groups or with a person's own index), and only the group's members, within their own time, can read it. Never a message deleted for them or for everyone (its tokens go).

### Endpoints (OpenAPI tag People; `mobile/api.txt` and `api.g.dart` regenerated)

A personal session reaches all of them (`groupPersonRoutes`), and a business session for its person; an API key is refused (403).

| Endpoint | What |
| --- | --- |
| `GET /v1/me/groups?archived=1&before=` | `{groups: [Group], unread, invites, more, next?}`, 200 a page, newest first |
| `POST /v1/me/groups` `{name, description?, person_ids?}` | 201 `{group: Group, added: [GroupAdded]}` |
| `GET /v1/me/groups/invites` | `{invites: [GroupInvite]}` |
| `POST /v1/me/groups/invites/{id}/accept`, `…/decline` | `Group` (409 full; 404 no invite); 204 |
| `GET /v1/me/groups/search?q=&group=&before=` | `{results: [GroupFound], more, next?}`, 30 a page |
| `GET /v1/me/groups/starred?before=` | `{starred: [GroupFound], more, next?}`, 50 a page |
| `POST /v1/me/forward` `{chat_id \| group_id, message_id, to_chats?, to_groups?}` | `{sent: [{kind: person\|group, id, message_id}], failed: [{kind, id, error}]}`; 429 `forwarded_many`; 409 a share card |
| `POST /v1/me/group-links/{code}/join` | `{state: joined\|waiting\|member, group?: Group}`; 409 full; 429; 410 `gone`; 404; 503 no key |
| `DELETE /v1/me/group-links/{code}/join` | take back your request to join (`waiting`): 204, waiting or not; 404 no such link |
| `GET`, `PUT`, `DELETE /v1/me/groups/{id}` | `Group` (with `kept`); `{name?, description?, admins_only_post?, admins_only_edit?, approve_joins?}` → `Group`; delete for me 204 |
| `PUT /v1/me/groups/{id}/me` `{muted?, archived?, mentions?}` | `Group` |
| `POST /v1/me/groups/{id}/leave` | `{left, closed, group: Group\|null}` |
| `GET`, `PUT` (multipart `file`), `DELETE /v1/me/groups/{id}/photo` | the image; `Group` |
| `GET /v1/me/groups/{id}/members` | `{members: [GroupMember], invited?: [GroupPending], waiting?: [GroupPending]}` (the last two to admins); 403 once left |
| `POST /v1/me/groups/{id}/members` `{person_ids}` | `{added: [GroupAdded]}` |
| `PUT /v1/me/groups/{id}/members/{person}` `{admin}`; `DELETE …` | `{person_id, admin}`; remove 204 |
| `POST /v1/me/groups/{id}/members/{person}/block` | `{blocked: true, chat_id}` (unblock: `DELETE /v1/me/people/{chat_id}/block`) |
| `DELETE /v1/me/groups/{id}/invites/{person}` | an admin takes an invite back, 204 |
| `GET`, `POST`, `DELETE /v1/me/groups/{id}/link`; `GET …/link/qr[?format=png]` | `GroupLink` `{on, code?, url?, created_at?, words?, qr?}` (POST 201 replaces); off 204; SVG or PNG |
| `GET /v1/me/groups/{id}/joins`; `POST …/joins/{person}/approve\|decline` | `{requests: [GroupPending]}`; 204 (approve: 409 full, or the request is older than their latest joining or leaving: it goes, no one is let in) |
| `GET /v1/me/groups/{id}/messages?before=&starred=1` | `{messages: [GroupMessage], more}` |
| `POST /v1/me/groups/{id}/messages` `{text?, client_id?, reply_to?, location?, card?, mentions?}` | 201 `GroupMessage` (200 for a repeated `client_id`) |
| `POST /v1/me/groups/{id}/files` (multipart `file`, `text`, `client_id`, `reply_to`, `mentions` comma-separated); `GET …/files/{message}` | 201 `GroupMessage`; the file |
| `PUT /v1/me/groups/{id}/messages/{message}` `{starred?, reaction?}`; `DELETE …?for=me\|everyone` | `GroupMessage`; 204 |
| `GET /v1/me/groups/{id}/messages/{message}/info` | the sender only: `{read_by: [GroupReader], delivered_to: [GroupReader], waiting}` |
| `GET /v1/me/groups/{id}/messages/{message}/reactions` | `{reactions: [{person_id, name, reaction, at}]}` |
| `GET /v1/me/groups/{id}/messages/{message}/card`; `POST …/paid` `{paid}` | as people chats' card; `GroupMessage` |
| `POST /v1/me/groups/{id}/read`; `POST …/typing` `{typing?}` | 204; 204 |
| `GET /v1/group-links/{code}`, `…/photo` (anyone) | `{code, link, name, photo, members, full}`; 410 `gone`; 404; the image |

Changed: `GET /v1/me/conversations` rows may be `kind: group` (open `/v1/me/groups/{id}`) with `group: Group`, and the answer has `invites`; its `unread` counts groups. `PeoplePrivacy` has `groups_add`; `YouNotifications` (`GET`/`PUT /v1/me/notifications`) has `groups`.

**Shapes.**
- `Group`: `id, kind: group, name, description, photo {url, updated_at}|null, members, role (admin|member), left, left_how? (left|removed), admins_only_post, admins_only_edit, approve_joins, can_post, can_edit, muted, archived, mentions, unread, mentioned, last, last_mine, last_sender?, last_at, joined_at, join_requests? (admins), kept?`.
- `GroupMessage`: `id, group_id, mine, sender_id|null, sender ("You", the name as you see them, "Deleted account"), kind (text|media|card|location|event), body (an event in words, or "This message was deleted"), event? {type (created|added|joined|left|removed|admin|unadmin|info|photo|settings), actor_id, target_id?}, card?, location?, attachment?, reply_to?, forwarded?, forwarded_many?, mentions?, mentions_me?, status? (sent|delivered|read, your own), starred?, my_reaction?, reactions? [{reaction, count}], deleted?, warn_transfer?, created_at`.
- `GroupMember`: `person_id, name, role, joined_at, you, person?: PersonCard, phone?`. `GroupPending`: `person_id, name, at`. `GroupAdded`: `person_id, state (added|invited|member|full|unavailable)`. `GroupInvite`: `group_id, name, photo, invited_by, by, at`. `GroupFound`: `group_id, group: Group, message: GroupMessage, snippet?, starred_at?`. `GroupReader`: `person_id, name, at|null`.

### The live connection (`GET /v1/me/live`)

Beside 5a's events, read in the person's own transaction (so only what they see):

| Event | Data | When |
| --- | --- | --- |
| `group_message` | `{group_id, message: GroupMessage}` | a message of one of their groups, new or changed (a reaction, deleted for everyone, a share marked paid, their own star on another device); sending it marks it delivered to them |
| `group` | `{id, hidden: false, group: Group}` or `{id, hidden: true}` | a group changed (a new message, info, settings, their own membership: muted, archived, left, removed), or deleted for them until a new message; not the group's changes after they left |
| `group_receipts` | `{group_id, messages: [{id, status}]}` | the ticks of their own latest 50 messages in a group moved (another member's marks moved) |
| `group_invite` | `{group_id, invite: GroupInvite}` | an invite waits for them |
| `group_typing` | `{groups: [{group_id, people: [ids]}]}` | who is typing in their groups now (never themselves), when it changes |

`gone` with the group's id as `chat_id` is a group message deleted for them on another device (`id`), or a group whose copy they deleted (`id: null`). Typing (`POST /v1/me/groups/{id}/typing`) is passed on at most every 3 seconds, shows 6 seconds, ends with a message; not from someone who left, nor in a group where only admins post (a member).

### For the web and the app

- The Chats tab: `GET /v1/me/conversations` rows of `kind: group` (open the group; `group.photo`, `group.members`, `group.mentioned`, `group.left`); `invites` beside `requests` for the Requests badge; `GET /v1/me/groups/invites` in Requests with Accept and Decline.
- A group: `GET /v1/me/groups/{id}`, its messages (`sender` above others' bubbles, events as centred lines, `status` on your own, `reactions` counts, `mentions_me`), `POST …/read` when shown, typing, `…/info` for "Read by" on your own message, `…/reactions`.
- Group info: members (`GroupMember`, admins first; `invited` and `waiting` for admins), add people (say each `GroupAdded.state` in words: "Added", "Invited: they'll see it in their Requests", "Already in the group", "The group is full", "Not on Taskiem"), make admin, remove, leave (with `closed`), delete for me, the link with its QR, approve joins, mute, `mentions` ("Notify me when I'm mentioned, even when muted"), the settings switches (admins), photo.
- Privacy: "Who can add me to groups" (`groups_add`: My contacts, Everyone, Nobody) with the words "People you haven't added send you an invite instead." Notifications: `groups`.
- Links: `/g/{code}` (the web's page and the app's route: `GET /v1/group-links/{code}` for the preview, then `POST /v1/me/group-links/{code}/join`).
- Forward from and to groups with `POST /v1/me/forward`; search and starred in groups with `GET /v1/me/groups/search` and `GET /v1/me/groups/starred` beside the people chats' (the clients merge by time).
- Live: the five group events above; merge by id as 5a's.

### Tests

`api/people_groups_test.go`:
- `TestGroupsMakeAndAdd`: the maker an admin; added only with a contact and the person's setting (contacts, everyone without being added, nobody), otherwise invited; across a block "invited" just the same, never seen by the person, listed for the admin; the invite's name and inviter; nothing of the messages while invited; accepted, from the join; a decline not told; only admins add; the setting's 400; the group in the one list.
- `TestGroupsHistoryLeaveAndRemove`: one copy sealed; from the join, through the API and through row-level security in the database (an outsider sees and writes nothing, even by SQL; a member can't make themself an admin by SQL); leaving and removal keep the copy up to then and nothing after, can't write; delete for me, for good once left; re-added from the new join only; cleared and hidden until a new message; the last one out closes it and every row goes.
- `TestGroupsRoles`: info by admins only by default, the switch letting a member; admins only post; make and unmake admins; the last admin can't unmake themself; the last admin leaving promotes the longest member, with its event.
- `TestGroupsLinks`: off at first, on, QR; the preview for anyone with the photo; joined at once, `member` again; admins only; replaced 410 (preview and join), unknown 404; approve on: waiting, listed, counted, approved, from then; declined; someone removed waits; turned off 410. `TestGroupsLinkJoinsPerHour`: 429 past the group's hour. `TestGroupsFull`: 256 by invite and link, `full` in the preview.
- `TestGroupsMessages`: `client_id`, replies, reactions counted per member and who reacted, stars each one's own and the starred list, delete for me (and off the starred list), for everyone (the sender's only, within 24 hours), a place, a share card marked paid with whose mark, a photo with a mention, forward from a group to a chat and a group and from a chat to a group with the mark, a share card not forwarded, the cap, the per-minute limit.
- `TestGroupsReadBy`: sent, delivered once all have it, "Read by" only with ticks on and with when, a member with ticks off as having it, read when all those with ticks on read it, only the sender sees it, the sender's ticks off hide it, unread per member and in the one list.
- `TestGroupsPush`: the invite's push; kind group with the group's name and words; muted none but a mention, mentions off none; the preview off; `groups` off; a member who blocked the sender gets none and still sees the message.
- `TestGroupsBlocking`: the blocked member's messages still seen; numbers hidden unless shown, and across the block; photo hidden across the block; one to one one tick; their add an unseen invite; listed in Blocked people and unblocked there; then added; a later unseen invite seen once asked again after the block.
- `TestGroupsAccountDeletion`: messages "Deleted account" with reactions kept, the member count and the last admin handed on, the event, every row of theirs gone and their own group closed.
- `TestGroupsSearch`: found from the join, not after leaving, not deleted for everyone, no words in the index.
- `TestGroupsLive`: a message and the group on a member's connection, typing and read ticks on the sender's, nothing on an outsider's.
- `TestGroupsAtOnce` (above). `TestGroupsInTheOneList`: groups and a people chat paged together, archived apart, muted, unread.

`engine/db`: `TestPeopleGroupsMigrationUpDownUp`; the definers, dispatch columns (now with the group columns its definers read) and table checks cover 00482.

### Left over

- **The next slice (B, the checklist):** reports of a group or a member (with consent, the last 20 messages), moderators' actions on groups (remove a link, close a group, act on members), the scam checks in groups (B1), limits on making groups and on adding people who aren't contacts (and lower in a first week), lawful exports of group messages, groups in the storage watching, the load test.
- **Ordering together** (C) and the privacy notice's paragraph (D).
- The web's and the app's screens; the server's own `/g/{code}` page (as `/p/{code}`) and `appPaths` for `/you/groups/*` and `/g/*`.
- Business chats on You still poll (the live connection carries people chats and groups).
- "Read by" times older than 31 days are not kept (the member is still listed, `at: null`); a link join's hourly count includes approved joins.
- A member's reactions go when they delete their copy after leaving, or their account.
- Someone who left sees their own messages' ticks from the marks kept with their time (`group_reads`): a member whose read ticks are off and who read without their app having had the message shows as not having it, and marks older than 31 days are pruned, so such ticks can fall back to sent. By SQL (not the API) someone who left can still read the current rows of the group's memberships.
- A live connection that showed a message read does not take it back if a member then turns read ticks on.

## As built: stage 5b, group safety (server)

Branch `stage5b-group-safety`, on 8763bffa (stage 5a, groups slice 1 and its review fixes): the server's part of [the 5b checklist](people-chatting-5b.md) B (§8.2 to §8.5, §14, the founder's B1; decision 0069 points 3 and 11), with the operator console's screens. Migration `00484_people_group_safety.sql`; code in `api/people_group_safety.go` (limits, reports, the admins' appeal, the scam checks), `api/ops_people_groups.go` (moderators and groups, the closure carried out), `api/lawful_groups.go`, `engine/peoplesafety/groups.go` (`GroupAllowance`), `engine/people/screen.go` (`ScreenGroupTx`), `engine/telemetry`, `engine/slo`. Not in this slice: the load test before 5b ships (B), and the app's and the web's group screens (report, the closed notice, the appeal).

**Groups stay kept by Taskiem, sealed at rest and readable by its servers; not end-to-end encrypted.**

### Data (00484)

- **`group_ledger`** (person, kind `create`, `invite` or `link_join`, group, when): what a person did with groups, counted for their limits; they read and add to their own rows only (never delete them), `taskiem_people_sweep` prunes rows past two days and `taskiem_group_forget` takes them with the account.
- **Reports of a group or a member**: `people_reports.group_id` (no foreign key: kept after the group goes) and `group_sealed` (the group's name, description and members then, sealed under the report's own key, row `g`); `reported_id` is the member, or NULL for the group. A person reports a group only if they are or were in it (the insert policy).
- **Held group messages**: `people_held.group_id` and `mentions`; `chat_id` is NULL for them (a check says one of the two). **Verdicts**: `people_screen_verdicts.group_id` (one verdict for the group's one copy, `to_person` NULL). **Warned messages**: `group_messages.screen` (`warn`).
- **Moderators' actions on a group**: `people_moderation.group_id` with the actions `group_link_off` and `group_close` (`person_id` NULL for them, and only for them; a closure has `until`). A group's admins read its actions (`group_admin_read`) and may take a closure's lock (`group_admin_lock`, `WITH CHECK (false)`: they change nothing); `people_appeals`' insert policy lets one of the group's admins appeal its action, and its admins read the appeal.
- **`group_standing`** (closed, until, the closure, link stopped): what is in force, written only by `taskiem_safety`'s definers through `taskiem_group_standing_sync` (from the actions; a closure stops the link too; it turns the live link off and keeps an event from Taskiem when the standing changes), read by members and anyone invited.
- **`lawful_requests.scope_groups`** (`none`, `sent`, `seen`).
- **Definers**, each owned by `taskiem_safety` with `taskiem_moderator_check`: `taskiem_ops_group_moderate`, `taskiem_group_closures_due`, `taskiem_group_close_carry`, `taskiem_ops_group`, `taskiem_ops_group_members`, `taskiem_ops_group_actions`, `taskiem_ops_group_reports`; `taskiem_ops_people_lift` and `taskiem_ops_people_appeal_decide` now answer `(person_id, group_id)` and sync a group's standing; the reports' list (with `group_id` and, for a group report, its reporters counted once), one report (`group_id`, `group_sealed`), held (`group_id`, `mentions`), verdicts, appeals (`group_id`) and lawful requests (`scope_groups`) gained columns. `taskiem_safety` may delete a group, insert Taskiem's event messages (`kind = 'event'`, no sender) and turn links off; nothing else of groups.
- **Locks.** A moderator's action takes the group's row first, as every writer does; a lift, an appeal's decision and the closure's carrying out take the action's lock, then the group's; an admin's appeal takes only the action's lock, so it never waits on a group writer that waits on it.

### Limits (§8.2)

`peoplesafety.GroupAllowance(Signals)` (the same signals as requests: age, reports, a shared first-week device, a moderator's limit):

| | groups an hour | a day | people who aren't contacts invited a day | link joins an hour |
| --- | --- | --- | --- | --- |
| first week | 2 | 3 | 10 | 3 |
| to 30 days | 3 | 6 | 30 | 5 |
| to 90 days | 5 | 10 | 60 | 8 |
| then | 5 | 20 | 100 | 10 |

A third of each (at least one) when two to four people reported them in 30 days; 1, 1, 2, 1 from five; 1, 1, at most 3, 1 for a first-week shared device; 1, 1, at most 2, at most 2 while a moderator's limit is in force. Counted from the ledger in the person's transaction under their sender lock (the group's row first where there is one): 429 "You have made several groups in the last hour. Try again later." / "You have made many groups today. Try again tomorrow." / "You have invited many people you haven't added to your contacts today. Add them to your contacts first, or try again tomorrow." (nothing of the add is done; people already invited don't count again) / "You have joined many groups by a link in the last hour. Try again later." (a join that waits for an admin counts). The in-memory limits stay as a first gate; a group's 60 joins an hour is unchanged.

### Reports (§8.3)

`POST /v1/me/groups/{id}/report {reason, note?, consent, person_id?}` (400 without consent; 404 for a person never in the group; 12 in two hours with the person's other reports): the last 20 messages of the group the reporter sees (row-level security: only their own time in it; never an event or one they deleted for themselves), opened, with who sent each by their own name, and the photos and files in them copied from the group's key to the report's (50 MB, newest first), sealed at report time as 5a's. `peer_wrote` is whether the report weighs in the member's request allowance: only when earned, and then half (Review fixes, 4). The report survives the reporter's deletion (detached), the member's and the group's, and is purged a year after review with 5a's.

### Moderators (operator console; not in the public OpenAPI)

| Call | Step-up operation (target) |
| --- | --- |
| `GET /v1/ops/people/reports` rows have `group_id`; `GET /people/reports/{id}?why=` adds `group` `{name, description, members, left?}` and each message's `sender_id`, `sender` (audited `ops.people.report.open` with the group) | — |
| `GET /people/groups/{id}?why=` `{group: {id, created_at, created_by, creator_name, members, admins, link_on, last_message_at, closed_at, close_until, link_stopped_at, reports, held} \| null, gone, members, reports, actions}`: never the name or messages; 400 without why; audited `ops.people.group.open` in the reading transaction | — |
| `POST /people/groups/{id}/actions` `{action: link_off\|close, reason, report_id?}`: 409 when it is in force already | `ops.people.group.link_off\|close` (group) |
| `POST /people/actions/{id}/lift` lifts a group's action too (`{person_id, group_id}`) | `ops.people.lift` |
| `GET /people/held` rows have `group_id`; releasing a group's sends it to the group then (its mentions, past the checks and the per-minute limit, once by `client_id`), 409 if the sender left or the group is closed | as before |
| `GET /people/appeals` rows have `group_id`; deciding answers `group_id` | as before |

**Members told.** Turning the link off, closing, reopening and letting the link back each keep an event in the group from Taskiem (`sender: "Taskiem"`, `sender_id` null, `event.type` `taskiem_link_off`, `taskiem_closed`, `taskiem_reopened`, `taskiem_link_back`) with its words ("Taskiem's moderators closed this group. No one can send messages in it, and it will be deleted on Fri 18 Dec 2026 unless its admins appeal."). The group's current admins get a push of kind `you` (link `/you/groups/{id}`) for each action, lift and appeal decision.

**A closed group.** No one sends, joins by a link or accepts an invite (403 "Taskiem's moderators closed this group, so no one can send messages in it or join it. Its admins can appeal from the group's info."), nor changes it or reaches its members (409 in the same words; see Review fixes); members still read it, leave and delete it for themselves. `Group.closed` `{at, until, words, can_appeal}` for members, with `action_id`, `reason` and `appeal` for admins; `can_post` and `can_edit` are false. `Group.link_stopped` while the link is off (403 to a new link).

**The admins' appeal.** `POST /v1/me/groups/{id}/appeal {text}`: one of its current admins, once for the closure, before its day (`201 {appeal, said}`; 403 not an admin; 409 not closed, already appealed, or "The time to appeal the closing of this group has ended."; five an hour). Audited `people.group.appeal`. Decided by a different moderator, as 5a's; overturned, the group reopens.

**Carried out.** The safety job (`SafetyTick`, every 10 minutes) takes the group closures due with no appeal open, each in its own transaction (`taskiem_group_close_carry` locks the closure and checks it again after the lock), deletes the group and all of it (§14: messages, files, members, marks, keys; reports keep their own copies) and audits `people.group.close` with the outcome (`deleted`, or `gone` if the group had closed itself). The group's page still answers its decisions (`gone: true`).

### Scam checks in groups (B1)

In `groupSend`, for a text, caption, card or place (`screenText`) not released by a moderator: only when the group is younger than a week (`GroupCheckYoung`) or the sender joined it by its link in the last day (`GroupCheckJoined`), and only when a member now in it hasn't added the sender (`TheyAddedYou` of the sender's cards). `People.ScreenGroupTx` keeps one verdict (group, no recipient), counted with the sender's 5a verdicts for the repeat hold. **Hold:** refused with 5a's words (403) and nothing else is written: no member gets it; it is kept in `people_held` with the group and mentions. **Warn:** delivered once, with `screen = 'warn'`; `GroupMessage.warn` is shown only to members who haven't added the sender (never to the sender). A share card with `pay_to` from someone the member hasn't added shows `warn` and `warn_transfer`, as 5a's.

### Lawful requests (§8.5)

Recording takes `groups` (`none` by default, `sent`, `seen`; 422 otherwise); the request shows `groups`. The export adds `groups: [{group_id, name, messages: [{id, sender_id, sender, from_person, kind, body, card?, location?, attachment?, file?, deleted?, at}]}]`, read in the person's own transaction, so only within their own time in each group and never what they deleted for themselves: `sent` their own messages, `seen` every message they could see (with `with_person_id`, only groups and times both were in, and for `seen` only theirs and that person's); files only with `files`; within the dates. `messages_given` and `files_given` count them; the audit `ops.lawful.export` has `groups` and `group_count`.

### Storage watched

`group_messages` and `group_files` join `PeopleStorageTables` (`taskiem_people_storage_rows|bytes{table}`; the console's figure: "People chats and groups take …"). `TaskiemPeopleStorageLarge` now fires over 150 GiB together or over 8 GiB more in a day (was 100 and 5); the dashboard's row has "People chats and groups on disk", "Groups on disk" (groups' tables and their day's growth) and the rows; the rule tests sample every 5 minutes, with a case where groups' files carry it past the line; `docs/reliability.md`'s runbook names the group tables and a query for the groups growing fastest.

### Deleting an account

As before, and: their ledger goes (`taskiem_group_forget`); their undecided held group messages go and their reports of groups stay, detached (`taskiem_people_reports_detach`); an appeal they made for a group stays with the closure.

### For the web and the app

- Group info: "Report group" and, on a member, "Report" (`POST /v1/me/groups/{id}/report`, with the consent words "The last 20 messages you can see, with their photos and files, go to Taskiem's moderators"), then offer to leave or block.
- A closed group: `closed.words` in place of the composer; for admins, "Appeal" while `closed.can_appeal`, then the appeal's state. `link_stopped`: say Taskiem turned the link off instead of offering a new one. Events with `sender: "Taskiem"` as centred lines.
- `GroupMessage.warn`: the 5a warning ("Never send money to someone you have not met") on the bubble.
- The 429s' and 403s' words as they come.

### Web (operator console)

`/ops/people/groups/:id` (asked why first; standing, members linking to their pages, reports, decisions with Lift; turn the link off or close the group with the passkey, closing asked again), reached from a group's report ("The group: its members and actions") and from a group's appeal; the reports list says "The whole group" or "A member, from a group"; a report shows the group's name and each sender; held messages and verdicts say "to a group"; the storage hint names groups and the new lines; a lawful request records and shows its group scope. `src/lib/peoplesafety.ts` has the group actions' labels, help and step-up (tested); `e2e/accessibility.spec.ts` covers the group page in both themes at 390 and 360 px.

### Tests

`api/people_group_safety_test.go`: `TestGroupLimits` (a first week's two an hour and three a day with their words; five an hour later on; ten strangers a day, none invited past it, contacts and those already invited not counted; three link joins an hour; the sweep and the deletion take the ledger), `TestGroupReports` (consent; only the reporter's own time; 20 messages with who sent each; sealed with the group's name; listed without words; opened and the photo opened only with why, recorded; kept detached after the reporter's deletion and the group's closing), `TestGroupModeration` (the page with why, recorded; the link off: 410 to join, 403 to a new link, the members told, lifted; closed: 403 to send and edit, the members told with the day, the admins' view; a member warned; the appeal by an admin only, once, recorded; held by the open appeal past its day; the deciding moderator refused, overturned by another, reopened; closed again and deleted by the safety job with every row, recorded; the report kept; the page `gone`), `TestGroupScamChecks` (warn only for those who haven't added the sender; a share card warned; held: no one gets it, sealed with group and mentions, one verdict a message, listed without words, released once with its mention, confirmed never delivered; no checks when all added the sender or in an old group; checked for a link joiner of the last day; holds gone with the sender's account), `TestLawfulGroupExport` (none unless asked, `sent`, `seen` within the person's time, with one other person, files, counts, audited), `TestGroupStorageWatched`; `TestPeopleStorageWatched` counts six tables; `TestGroupsBlocking` and `TestGroupsInTheOneList` make their groups as accounts past their first week. `engine/peoplesafety`: `TestGroupAllowance`. `engine/db`: `TestPeopleGroupSafetyMigrationUpDownUp`; definers, dispatch columns (`group_ledger.person_id`, `at`) and `TestDispatchRoleCannotReadPayloads` (group report info, held group messages, group actions, standing, the ledger, lawful scope, deleting a group). `engine/slo`: the rule test.

### Left over

- The load test before 5b ships (§13, B).
- The app's and the web's group screens for reporting, the closed notice and the appeal (the calls are generated in `api.g.dart`).
- A closure deletes the group; nothing stops its admins making a new one beyond their limits and standing.
- Someone in a group past the checks' window is no longer checked even if no one there added them (B1 as settled).

### Review fixes

Branch `stage5b-gsafety-fix`, on b2cebca0, migration `00485_people_group_safety_review_fixes.sql` (00484 is not changed). Each finding of the review, with its test in `api/people_group_safety_review_test.go` unless named:

1. **A lawful export naming one other person widened to every group.** With `with_person_id`, only the groups both people have membership rows in, and only messages of the times both were in each (the other person's times read in their own scope for a moment: what they see, plus their latest time whole, so what they deleted for themselves within it still counts; earlier times they cleared for themselves do not), for `sent` and `seen` alike; `seen` keeps only theirs and the person's. Test `TestLawfulGroupExportWithPerson` (a shared group, one without the other person, a message after they left).
2. **A closed group still took writes.** `groupRow.changeable()` answers 409 in the closed words to every write that changes the group or reaches its members: reactions (the group's row taken first), a share marked paid, making or unmaking an admin, removing a member, its rules, info and photo, adding people, taking an invite back, a new link or turning it off, approving or declining a join request. Typing answers 204 and writes nothing. Still allowed: leaving, the person's own settings (mute, archive, mentions), starring, deleting for themselves, deleting their own message for everyone, reading, reporting, blocking a member (their own block; no event in the group), and the admins' appeal. Sending, joining by a link and accepting an invite stay 403. Test `TestGroupClosedRefusesChanges` (each refusal and that nothing was written; each allowed write; only the leave's event).
3. **The storage alert counted each scraped pod.** `TaskiemPeopleStorageLarge` sums `max by (table) (taskiem_people_storage_bytes)` in both terms and the day's offset. Rule test "two pods report the same tables, counted once" (5-minute samples; added up across two pods it would be past both lines from the start; counted once it fires only after the files pass 150 GiB, an hour on).
4. **Group reports were too cheap a signal.** A report of a member from a group weighs in their `ReportedBy` only when the reporter's account is past its first week, they had been in the group a day or more (`peoplesafety.GroupReportAfter`, their latest time in it) and the member wrote within what the reporter sees (`peer_wrote`, set by `groupReportEarnedTx`); and then half: `taskiem_people_sender_signals` counts each reporter once, whole if any of their reports of the person in 30 days is one to one, half if all came from groups, and rounds the sum down (two such reporters make one). Test `TestGroupReportsWeighHalf` (five new accounts and two older ones join by the link and report: nothing; one genuine member is half, two are one, a reporter twice counts once, one who also reports one to one counts whole). `TestGroupReports` now expects its minutes-old reporter's report not to weigh.
5. Smaller:
   - (a) `people_reports.group_member` marks a member's report (backfilled), so a member's report whose account is later deleted (`reported_id` NULL) is never listed, opened or counted as a report of the whole group; the reports' list, one report and a group's page answer `group_member`, and the whole group's count is of whole-group reports only. The operator console tells them by it. Test `TestGroupReportsMembersAndReach`.
   - (b) Reporting a member asks, as blocking does, that their times in the group overlap the reporter's (or the reporter sees a message of theirs); anyone else is 404 as an unknown id. Same test.
   - (c) `people_held.client_id`: a held send retried with the same `client_id` (groups and one to one) is answered 403 in the same words with no new held row and no new verdict (so the repeat hold comes no nearer), through `taskiem_people_held_again` (a definer owned by `taskiem_safety`, the person's own only); once released, the retry answers the message it became. Test `TestHeldSendRetried`.
   - (d) Groups never keep 5a's `transfer` mark: 5a sets it at send time because its one recipient is known; in a group, whether a share card naming an account warns is decided for each member as they read it (not added: `warn` and `warn_transfer`), so the dead branch went and `group_messages.screen` is `warn` only. Test `TestGroupShareCardWarnedWhenRead` (warned for a member who hasn't added the sender, not once they do; the mark refused).
   - (e) A moderator's action on a group names only one of that group's reports (`taskiem_ops_group_moderate`, 422 otherwise). Test `TestGroupReportsMembersAndReach`.
   - (f) A lawful export's reads (chats and groups), the request's answer and its `ops.lawful.export` record are one transaction: the operator's (`opsTx`), the person's scope set for the reads and cleared before the record. Test `TestLawfulGroupExportWithPerson` (records match what was given), with `TestLawfulRequests` and `TestLawfulGroupExport`.

`engine/db`: `TestPeopleGroupSafetyReviewMigrationUpDownUp`; `definers.tsv` lists `taskiem_people_held_again(uuid,uuid,text)`.

**For clients:** a closed group's changes answer 409 (sending and joining stay 403); ops report rows and a group's reports have `group_member`.
