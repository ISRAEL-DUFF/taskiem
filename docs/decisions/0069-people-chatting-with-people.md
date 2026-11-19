# 0069 — People chatting with people: chats kept by Taskiem, businesses inside them

Date: 2026-11-17 · Status: **Accepted** (17 November 2026; not built) · Plan: [People chatting with people](../spec/people-chatting.md)

## Context

[0068](0068-taskiem-for-everyone.md) made the Taskiem app a chat app for everyone and left chats between people "a later stage, with its own plan and decisions: contacts, groups, encryption, moderation at the scale of strangers" (its point 11). Stages 1 to 4 have built what such chats need:
- personal accounts with verified phones and devices;
- the person's own scope;
- two-copy conversations;
- push on You;
- blocking and reporting;
- the cards a business sends.

Two facts shape the choice:
- **WhatsApp already holds people's chats in Nigeria.** People will not move their chats; they will add Taskiem for what WhatsApp does badly.
- **Business chats are read by Taskiem** to draft orders and translate. They cannot be end-to-end encrypted.

The founder settled the plan's questions on 17 November 2026 ([plan §17](../spec/people-chatting.md#17-settled)).

## Decision

1. **People can chat with each other in Taskiem,** one-to-one first (stage 5a), then groups (5b), then calls (5c). Each stage ships on Android, iPhone and the web together.
2. **People chats are kept by Taskiem, encrypted at rest and readable by Taskiem's servers,** as business chats are. They are not end-to-end encrypted, and the app never says they are.
   - Each person's copy is sealed under their own key.
   - Every device and the web show the whole history.
   - Search runs on the server, and push can carry the words.
   - Files get the checks business chats' files get.
3. **What Taskiem does with them:**
   - **People:** no one at Taskiem reads a people chat except a moderator handling a report of it, or under a lawful request. Each access is logged.
   - **Machines:** automated checks may look at strangers' messages (requests and new chats) for scams and spam, keeping only their verdict. Order drafts and translations do not run on people chats.
   - **Training and advertising:** people chats are never used for either.
   - **Saying so:** the privacy notice says all of this.
4. **Finding people:**
   - **By phone number.**
   - **By a person's own replaceable link and QR code.**
   - **From the phone's contacts,** only if the person agrees. Contacts are matched by keyed hashes with a rotated key, nothing is kept, and the privacy notice states the method's limits honestly.
   - **Settings:** a person chooses who can find them, message them, and see their last seen and photo.
   - **Numbers are hidden** from the people they chat with unless they choose to show them.
5. **Strangers write as requests.** Anyone may send a first message, which waits in Requests until accepted, deleted, or blocked and reported. Abuse is held back by:
   - limits on new accounts and on requests waiting;
   - marks and caps on forwarding;
   - blocking;
   - reporting, which sends the last 20 messages with the reporter's consent;
   - the warning on messages that ask for a transfer to a personal account.
6. **One Chats list** on You holds business chats and people chats together, each marked by what it is.
7. **Groups** start at 256 people, with admins and invite links. A member sees a group from when they joined. A deleted account's messages stay, shown as "Deleted account". Businesses do not join people groups.
8. **Businesses inside people chats** are the reason to use Taskiem for this:
   - **Sharing:** a product, a business, an order or a delivery can be shared into a chat as a live card. Opening it reads through the customer door as the person opening it.
   - **Ordering together:** a group can order together from one business through a shared basket. One person places the order through the door, and the business never sees the group.
9. **Taskiem moves no money between people.** Splitting what was paid is a "your share" card with the payer's details and "Mark as paid". When ordering together, each person may pay the business for their share. A wallet with a licensed partner would need its own plan and decision.
10. **People chats are kept until the people delete them or their account.**
11. **Lawful requests** are answered only under proper legal process:
    - the least that is asked is given;
    - the person is told where the law allows;
    - the number of requests is published.
    
    The process and its words are agreed with a lawyer before stage 5a.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| End-to-end encryption by default (the Signal protocol, keys on each device) | The plan's first proposal. People's private chats would be unreadable by Taskiem. It was set aside by the founder for B, because it is the largest build (keys per device, linked devices, backups held by the person), it makes the web chat depend on a phone, it limits moderation to what reporters forward, and it keeps business features such as live cards and search harder to offer everywhere |
| B by default with opt-in "secret chats" (Telegram's model) | Two kinds of chat to explain, and most people never turn it on. It can be added later if people ask for it (plan §6.4) |
| Chats between people only through WhatsApp (no people chats in Taskiem) | The app's own reason to exist for people would stay one-sided: they could chat with businesses but share them only by copying links to WhatsApp |
| A wallet for paying friends | It makes Taskiem part of money flows between people, needing a licensed partner, KYC on both people and fees. Share cards cover splitting without moving money |
| Showing phone numbers to everyone chatted with (WhatsApp's way) | People meeting through groups and Discover would expose their numbers to strangers |
| Private set intersection for contact matching | Taskiem would learn nothing of a person's contacts, but at a much higher cost. Keyed hashes, stated honestly, come first |

## Consequences

- **The privacy notice and terms** must say that people chats are kept and readable by Taskiem's servers, what Taskiem does and does not do with them, and how lawful requests are answered. A lawyer reviews this before stage 5a.
- **Trust risk:** some people will expect private chats to be end-to-end encrypted, and the app must never suggest they are. A breach would expose kept chats. That makes sealing per person, server-only access, logging of every human access, and an outside security review before 5a essential.
- **New data:**
  - people conversations and messages, sealed per person, plus groups with a group key;
  - requests;
  - a person's link;
  - privacy settings;
  - contact-matching keys (rotated; nothing kept from matching);
  - shared baskets;
  - share cards.
- **Reused:** personal scope, two-copy conversations, push on You, media checks, blocking and reporting, cards and the customer door.
- **Storage grows** with every kept chat, so it is watched on the operator's dashboard.
- **Calls (5c)** need their own short spec when 5a and 5b are in use: relays, VoIP push, and app-store review.
- **0068's point 11 is answered:** chats between people are this decision.

## Provenance

The founder's direction for 0068, a general chat app like WhatsApp and Telegram that anyone can use, and the founder's answers of 17 November 2026: encryption B, then acceptance of every other proposal in the plan. Nothing here copies another product's code or design. Requests for strangers, hidden numbers and forwarding marks are common practice in chat apps, and are described here in Taskiem's own words.
