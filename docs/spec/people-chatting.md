# People chatting with people (stage 5 of Taskiem for everyone)

Status: **plan, settled** (17 November 2026; every question settled the same day, §17; decision [0069](../decisions/0069-people-chatting-with-people.md) accepted the same day). Stage 5 of [Taskiem for everyone](taskiem-for-everyone.md) (§16, §20), under decision [0068](../decisions/0068-taskiem-for-everyone.md), which left this stage "its own plan and decisions". Stage 5a's server for finding people is built (see "As built" at the end); the rest is not. The founder's answers are in §17.

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
