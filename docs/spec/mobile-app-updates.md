# Updates: Status and Channels, as WhatsApp's

The home's bottom bar has Chats, **Updates**, Money and Orders. Updates is
WhatsApp's Updates tab, for businesses: **Status** at the top (what a
business shows for 24 hours) and **Channels** below (a business's
broadcast, followed). Settled with the founder 12 November 2026: **both**,
seen by **linked businesses and customers**, and a post may **tag a
product** that is ordered from it.

This replaces the first draft, which took Updates for a feed of the
business's own events.

**Not built for now** (12 November 2026, the founder): the backend it
needs is large (video above all) and its customers' WhatsApp messages cost
money. The tab is **Today** instead (the web's Home: the day's money and
orders, what needs you, the latest runs, the set-up left, the workflows
running; `lib/chat/today_tab.dart`), and **Team** (staff and tasks) comes
next. This plan stays for when Updates is built.

## 1. How WhatsApp does it

**Status**:
- A person posts a photo, a short video or a line of text on a colour.
- Each post is gone after 24 hours.
- Their contacts see a row of rings at the top of Updates: green for unseen posts, grey for seen. "My status" comes first, with a + to post.
- Tapped, a ring plays its posts full screen in turn:
  - a bar at the top for each post;
  - a tap on the right goes on, on the left goes back;
  - holding pauses, and a swipe down closes.
- A viewer replies (it lands in the chat with the poster) or reacts.
- The poster sees who viewed each post.
- The poster chooses who sees their status: all contacts, all but some, or only some.
- A viewer can mute someone's status.

**Channels**:
- A one-way broadcast: the account posts, and followers read and react with an emoji. Followers do not reply in it.
- Posts stay; they do not expire after 24 hours.
- Channels are found in a directory and followed or unfollowed.
- A channel shows its followers' count and its unread posts.

## 2. What it is in Taskiem

**A business's status**
- **What a post is**:
  - What a business shows today: fresh stock, a new product, a price for the day, "open late tonight".
  - A photo, a short video (up to 30 seconds), or a line of text on one of the brand's colours.
  - It is gone after 24 hours.
  - It may **tag products** from its price list or catalogue.
- **Who posts**: the business's people who may sell (`storefront.manage`). A post is the business's, not the person's, and shows with the business's name and logo.
- **Who sees it**:
  - the businesses linked with it, in their Updates;
  - its customers, through a link (§4).
- **What a viewer can do**:
  - **Reply.** The reply goes into the conversation with that business, quoting the post, as a WhatsApp status reply does. That is where a sale starts.
  - **React** with an emoji.
  - **Order** a tagged product. A linked business gets the order sheet with the product in the bag. A customer gets the storefront's order page for it.
- **What the poster sees**:
  - who viewed each post (each linked business by name; customers as a count);
  - the replies, in the chats.
- **Who can see it**: everyone, all but some, or only some, chosen from the linked businesses. Customers either see it or do not.
- **Muting**: a business's status can be muted in Updates.

**A business's channel**
- **What it is**: the business's broadcast. It holds its news, new products and offers, and a weekly price list.
- **What a post is**: text, photos, a video, or tagged products. Posts stay.
- **Who follows it**:
  - linked businesses, from Updates or the business's profile;
  - customers, from the business's page or by saying FOLLOW in its chat (§4).
- **What a follower can do**:
  - react with an emoji;
  - **Order** a tagged product, as on a status.
- **Finding channels**: in Explore, which becomes the channels' directory too. Followers' counts show there.

## 3. In the app

The Updates tab:

| Part | What it is |
| --- | --- |
| **Status** | A row of rings across the top: My status first (with + to post, and how many viewed), then the linked businesses with a status, unseen ones first with a lime ring, seen ones grey. Muted ones at the end, faded |
| **The viewer** | Full screen, a business's posts in turn: bars at the top, tap on the right or left, hold to pause, swipe down to close; its name, logo and how long ago; a tagged product as a card at the bottom with **Order**; a reply box and the reactions |
| **Posting** | A photo or video from the camera or the phone's photos, or text on a colour; a caption; tag products (the price list's or catalogue's, searched); who sees it; Post |
| **My status** | Each post with its viewers (businesses by name, customers as a count), its replies, and Delete |
| **Channels** | Below the status: the channels followed, each a row with its logo, its latest post and its unread count, as a chat row. Then **Find channels** (Explore's channels). The business's own channel first, with + to post |
| **A channel** | Its posts, newest at the bottom as a chat; reactions under each; a tagged product's card with **Order**; Follow or Unfollow; the followers' count; Mute |
| **The tab's count** | Unseen statuses (a dot) and unread channel posts (a number), as WhatsApp's |

## 4. Reaching customers

Customers talk to a business through WhatsApp or Telegram, not the app, so
they reach its status and channel in two ways.

**A page for each.** Both are public, in the business's look, on the
storefront's address:
- The status page (`/s/<business>/status`) plays today's posts, with Order on a tagged product, and "Chat with us" opening the business's WhatsApp.
- The channel page (`/s/<business>/channel`) holds its posts and a **Follow on WhatsApp** button.

A business shares either link anywhere: its own WhatsApp status, its bio,
its chat replies.

**The chat app, for those who ask.** A customer who follows the channel gets its posts as messages from the business's number:
- **Following.** A customer follows from the page, or by saying FOLLOW in the business's chat.
- **Leaving.** STOP, which the reach book already keeps, ends it.
- **Within 24 hours.** Inside the 24 hours after the customer last wrote, a post goes as a normal message.
- **After 24 hours.** Outside them, WhatsApp takes only a template Meta has approved, paid for by the message.
  - The post goes as a `business_update` template: the post's first line and a **See it** button to the channel page.
  - The template is counted against the business's plan, as templates are today.
  - A business chooses how often its followers may be messaged: every post, or one digest a day.

Customers do **not** see the status in WhatsApp's own Updates: the WhatsApp
Business Platform cannot post a status. The status page is how they see it.

## 5. What the server needs

| | What | Notes |
| --- | --- | --- |
| U-S1 | **Posts**: a status post or a channel post | The business; the kind (status or channel); text, colour, media, caption; the tagged products (price-list or catalogue items); who may see it (status); its expiry (status: 24 hours) |
| U-S2 | **Media** | Photos and videos uploaded as chat files are, with a thumbnail; a video up to 30 seconds and 16 MB; purged with the post (a status's a day after it ends) |
| U-S3 | **Seeing** | Statuses of the linked businesses who may see them, unseen first; seen marks; viewers for the poster; mute |
| U-S4 | **Replies and reactions** | A reply is a message in the link's conversation with the post quoted (a new message kind, as a document card is); reactions per post and viewer |
| U-S5 | **Channels** | One per business, made with its first post; follows (linked businesses, and customers by their chat number); followers' count; unread per follower; the directory in Explore |
| U-S6 | **Customers** | The public status and channel pages; FOLLOW and STOP in the customer chat; posts sent to following customers inside the 24 hours, as the template outside them, or a daily digest; the `business_update` template submitted with the others |
| U-S7 | **Ordering from a post** | A tagged product opens the order sheet (linked businesses) or the storefront's order page (customers) with it; the order notes the post it came from, so the business sees what sold from its status |
| U-S8 | **Live** | New statuses and channel posts on the chat's live stream, so the rings and counts change as they happen |
| U-S9 | **Limits and safety** | Posts per day per business by plan; media checked as chat files are; a post reported by a viewer; audit of posting and deleting |

## 6. The web

The web chat gets the same Updates: the rings above the chats list, the viewer, posting, and the channels. The rule that the app and the web chat stay the same (mobile-app.md §9) holds here too: each part is built in both, and the parity tests cover both.

## 7. Order of the work

| # | Part | Notes |
| --- | --- | --- |
| UP1 | Status between linked businesses: posting text and photos, the rings, the viewer, reply and react, viewers, who can see, mute | Server U-S1 to U-S4, U-S8; app and web |
| UP2 | Video in a status | U-S2 for video |
| UP3 | Tagged products and Order from a status | U-S7 |
| UP4 | Channels for linked businesses: posting, following, the list, a channel, reactions, Explore's directory | U-S5 |
| UP5 | Customers: the status and channel pages, FOLLOW and STOP, posts to following customers, the template and the digest | U-S6 |
| UP6 | Limits, reporting, the plan's counts | U-S9 |

Each part: the server's tests, widget tests for what each button sends,
large text and touch targets, the web's e2e and parity, committed on its
own; an audit after the last.

## 8. Still to settle

These have suggested answers; each is the founder's to change.

1. **Customers' chat messages cost money**: a template outside the 24 hours is paid for. Suggested: a business chooses every post or a daily digest; the digest by default.
2. **Who posts**: suggested `storefront.manage`, and a new permission if staff should post without managing the shop.
3. **How long a channel's posts are kept**: suggested 90 days, as notifications.
4. **Video**: suggested up to 30 seconds and 16 MB, after photos and text (UP2).
5. **Status for customers by default**: suggested on, so a status is on its page from the first post; a business can turn the page off.
