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
  - The server hashes the verified phones with the same key in one query and answers the people who let themselves be found from contacts, never the asker. It keeps nothing of the request: a test counts every row of every table before and after.
  - Limits: 500 hashes a request; 5000 hashes and 20 requests a day per person; 30 requests per address an hour.
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
- `MayMessage`: B's `Decision` as `(Allowed, AsRequest)`. A refusal for a block (`blocked`) is let through as a request: the chats keep the message on the sender's side with one tick and tell them nothing, as A does for blocks; a block is never a 403.
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
