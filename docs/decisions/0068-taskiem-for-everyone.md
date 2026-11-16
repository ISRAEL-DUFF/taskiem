# 0068 — Taskiem for everyone: a chat app for people and the businesses they deal with

Date: 2026-11-16 · Status: **Proposed** · Plan: [Taskiem for everyone](../spec/taskiem-for-everyone.md)

## Context

Every account in Taskiem today belongs to a business:
- **An account needs an email,** and a signed-in session needs a membership in a business (`users.email`, `memberships`). The one exception is a person with invitations waiting, who sees only those.
- **Every conversation is a business's:** the Taskiem conversation, chats with linked businesses, and customer threads on the business's own WhatsApp number.
- **The phone app is "for owners and their staff"** and "does not replace WhatsApp, Telegram or Slack" ([0066](0066-a-flutter-phone-app.md), [mobile-app.md](../spec/mobile-app.md) §1–2).
- **Customers are outsiders.** Storefronts make customer accounts a non-goal: "a customer is a phone number on an order, never a Taskiem user" ([storefronts §3](../spec/storefronts.md#3-goals-and-non-goals)). [0039](0039-reaching-people-outside.md) lets outsiders opt in to a business's updates on Taskiem's shared WhatsApp number and Telegram bot, with one fixed answer to anything else they send.

Three things came out of reviewing what happens when a customer, not a business, meets Taskiem (founder, 16 November 2026):

1. **Every door assumes a business.** A customer who finds the web sign-up, the app or Taskiem's WhatsApp number either makes a business they do not want or reaches a dead end.
2. **Most businesses cannot chat with their customers in Taskiem.** A customer conversation needs the business's own WhatsApp number, and so Meta's business verification. Everyone else's customers write to the owner's personal WhatsApp, outside Taskiem. Carrying those conversations on Taskiem's shared number was drafted and set aside: one customer chatting with several businesses shares one chat, under Taskiem's name, and every business's conversations put one number's standing with Meta at risk.
3. **Someone who wants only workflows must still be a business.** Web sign-up offers "Just workflows for now" as the last answer to "What does your business do?", after a required business name. Sign-up in chat and in the app makes a business with every area on, to be turned off in Settings.

The founder's direction: make the Taskiem app a full app for customers too, and in time a general chat app like WhatsApp and Telegram that anyone can use. Businesses that have their own WhatsApp number keep using it. The founder chose one app for everyone, with chat in the phone's browser for those who won't install it, and settled the plan's eight questions ([plan §23](../spec/taskiem-for-everyone.md#23-settled-in-review-2026-11-16)).

## Decision

1. **Taskiem is a chat app for everyone.**
   - Anyone may make a personal account with a phone number, proved by a code sent by WhatsApp or SMS, and use the app and the web chat without belonging to a business.
   - Every account is a person's. Running a business, or working in one, is something a person does in Taskiem, not what they must be to use it.
   - Email and password, passkeys and single sign-on remain for those who have them.
   - The minimum age is **18**, confirmed when the account is made, with no parental-consent flow.
2. **A person's own side is separate from every business.** On their own side (**You**), a person has their chats with businesses, their orders and bookings, Discover and their settings. No business sees what a person does with other businesses, or later with other people, unless the person sends it that business. What a person does inside a business stays the business's, as today.
3. **Customers chat with businesses in the app, or in the phone's browser with nothing to install.**
   - Each business is its own conversation, under its own name and photo, with its own notifications.
   - Shopping, booking and paying are tools and cards in the conversation. There is no 24-hour window and no template.
   - A business's chat link opens the app when installed, else the browser.
   - A browser chat that cannot be notified is told by SMS when the business answers, at most once per conversation per hour.
4. **A customer conversation has two copies,** the business's and the person's, as conversations between linked businesses do ([0061](0061-business-links.md)). Each side keeps, archives and deletes its own. The business's copy is its customer thread, sealed under the customer's subject and kept under the business's retention.
5. **A business may write first to anyone who has chatted or ordered with it, for any reason,** in two kinds of message:
   - **About something of the customer's** (an order, a booking, an invoice, a delivery): no limit beyond the ordinary sending limits.
   - **Anything else** (an offer, news, a broadcast): marked as an offer, at most 2 per customer per week, and mutable by the customer in one tap.

   Customers and businesses can each block the other. Every conversation and listing can be reported to Taskiem's moderators.
6. **Businesses with their own WhatsApp number keep it.** A customer who uses both WhatsApp and the app with the same verified phone is **one customer, in one thread**. Each message is marked with where it came from, and replies go where the customer last wrote. Matching needs the phone proved on both sides; a typed phone matches nothing.
7. **Taskiem's shared WhatsApp number and Telegram bot stay one-way for outsiders.** 0039 stands there: updates, STOP and START. They gain a link to chat with the business in the app or the browser.
8. **Discover is the public's way to find businesses,** kept apart from Explore (businesses finding businesses). A business with a published storefront is listed by default and can turn it off. Every other business is listed only when it turns Discover on.
9. **A person's own orders across businesses** are theirs to see. This covers orders made while signed in, and storefront orders made with the same verified phone once the person confirms them. They are read through one narrow function that returns a verified phone's own orders and nothing else. It is the only read across businesses this decision adds.
10. **Starting something is deliberate: a business, or workflows.**
    - **Start something** asks "A business, or workflows?", the same at every door: the web, the app, WhatsApp and Telegram sign-up, and Slack.
    - **Workflows** makes a *workflows workspace*: a tenant with every business area off, no business questions, never listed in Discover or Explore, opening on the workflows. Turning an area on makes it a business, with nothing moved or lost.
    - Sign-up in chat no longer makes a business with every area on. Its areas follow what it sells.
11. **People chatting with people is a later stage,** with its own plan and decisions: contacts, groups, encryption, moderation at the scale of strangers. Nothing in the stages before it depends on them.

[0058](0058-chat-as-a-way-in.md) is unchanged: a business is signed up from WhatsApp, Telegram or Slack as today, and never from inside a customer conversation.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Keep customers as outsiders, and only answer a customer who reaches a door ("you don't need an account to buy") | The plan's first draft. Cheap and safe, but customers' conversations stay on owners' personal WhatsApp, outside Taskiem, and most businesses still cannot chat with their customers in Taskiem |
| Customer conversations on Taskiem's shared WhatsApp number and Telegram bot, routed to a business by a code in its chat link | The second draft. One customer chatting with several businesses shares one chat under Taskiem's name, so every plain message must be routed or asked about ("Who is this for?"). Muting is all or nothing. Every business's conversations ride on one number Meta rates. Set aside by the founder for the app |
| A pool of shared WhatsApp numbers, a different one per business a customer chats with | Separate chats per business, but each number needs Meta's registration, templates and its own quality rating, and it still carries Taskiem's name, not the business's |
| A separate app for customers | Two listings and two reviews, and one person is often both a customer and someone's staff. One app, whose sides follow what the person is, serves both |
| Chat only in the app, with no browser chat | Customers who will not install an app before saying hello would go back to the owner's personal WhatsApp. The web chat already exists, so the browser costs little |
| Businesses write first only about an order, booking or invoice, with no offers | Safer for customers while trust is new; the founder chose to let businesses reach past customers, with offers marked, limited and mutable |
| Workflows on a person's own side, with no workspace | Connections, secrets, approvals, billing and audit all belong to a tenant; a personal copy would rebuild most of the platform. A workflows workspace gives the same result with what exists |
| A minimum age of 13 or 16, with parental consent below 18 | Nigeria's data protection rules treat under-18s as children whose data needs a parent's consent, and the app takes payments. 18 needs no consent flow |

## Consequences

- **Amendments, appended when this is accepted** (decision records are never rewritten):
  - [0066](0066-a-flutter-phone-app.md): the app is for everyone, not only owners and staff, and it no longer "does not replace WhatsApp".
  - [0039](0039-reaching-people-outside.md): its rules hold for outsiders on the shared WhatsApp number and Telegram bot. A person in the app is not an outsider.
  - [storefronts §3](../spec/storefronts.md#3-goals-and-non-goals) and [mobile-app.md](../spec/mobile-app.md) §1–2 are updated to match.
- **Accounts and sessions** ([plan §19](../spec/taskiem-for-everyone.md#19-data-and-server-changes)):
  - `users.email` becomes optional, and verified phones are kept in `user_phones` as keyed hashes with sealed numbers.
  - A session's business becomes optional, and a person's own tables have a row-level scope of their own (`taskiem_person_scope()`).
  - The invitee-only session becomes an ordinary personal session.
  - Phone codes go by WhatsApp or SMS, with limits per number, device and IP.
- **New data:**
  - the person's copies of conversations;
  - a line and a person on customer threads;
  - chat codes per business;
  - Discover's listings and search;
  - `tenants.kind` (`business` or `workflows`);
  - the one read of a verified phone's orders across businesses.
- **Taskiem becomes responsible for consumers' personal data,** not only businesses'. The terms, the privacy notice and the minimum age are reviewed by a lawyer before the first stage ships.
- **Safety at the scale of the public:**
  - reporting on every conversation and listing, which Apple and Google require of apps where people message each other;
  - limits on new conversations and on offers;
  - payment-fraud warnings;
  - moderation of Discover;
  - pausing a business's offers when customers mute or block it far above the norm.
- **Cost:** SMS for phone codes and for browser chats that cannot be notified. WhatsApp is tried first for codes. Sends and failures by country are on the operator's dashboard.
- **The store listings and the first screen** change from "for your business" to "Chat with the businesses you buy from, and run your own."
- **Stages,** each shipped on Android, iPhone and the web together:
  1. personal accounts and Start something;
  2. customers chatting with businesses;
  3. Discover and a person's own orders;
  4. one customer across the app and WhatsApp;
  5. people chatting with people, under its own plan and decision.

## Provenance

The founder's questions of 16 November 2026: what happens when a consumer downloads the app or signs up, how a customer finds and chats with a business on WhatsApp or Telegram today, and how one customer would chat with several businesses on the shared number. The founder chose the app over the shared number, one app with chat in the browser, and a general chat app as the direction, and answered the plan's eight questions the same day. Nothing here copies another product's code or design.
