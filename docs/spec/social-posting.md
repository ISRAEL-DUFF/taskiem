# Social posting: a person or a business posts to their social accounts from a workflow, and from WhatsApp

Status: **Plan** (proposed 6 November 2026) · Decision: [0062](../decisions/0062-social-posting.md) (proposed) · Builds on: [business connectors](business-connectors.md) §6 (K5, the Meta app, `messenger@1`, `instagram@1`), [platform apps](../decisions/0051-platform-apps.md), [chat as the way in](chat-way-in.md), [no sheet needed](no-sheet-needed.md) (known tables), [moderation](../moderation.md), [AI steps](../ai.md#ai-steps), [storefronts](../storefronts.md) (the catalogue) · Strategy: [chat-first](../strategy/chat-first-nigeria.md) · Stories: [user stories](../strategy/user-stories.md) Y

## 1. Summary

A tailor sends Taskiem a photo on WhatsApp with "post this to my Instagram and Facebook at 6pm" and it goes out at six, on both, with a caption she read back and said **yes** to. A church posts its Sunday service details every Saturday from a table. A shop announces every new product in its catalogue the moment the owner adds it, once the owner approves the caption. A creator with no business at all signs up, connects an Instagram account and a Page, and schedules a week of posts from the phone, without opening a laptop.

**Posting is a workflow like any other.** A post is an action on a connector (`instagram@1`, `messenger@1`, later `linkedin@1`, `tiktok@1`), the schedule is a schedule trigger, the caption is a text or an AI step, the yes is an approval, and the record is a known table (`posts`). Nothing new in the engine: the plan adds actions, triggers, two known tables, templates, chat commands, a limit, and the platform reviews that gate it all.

**For anyone, not only businesses.** A tenant on Taskiem is an organisation, and a person alone is one too: sign-up (the web form, or chat) makes it. Nothing here needs a storefront, an invoice or a bank. What the platforms themselves require is a professional account: Meta's API posts to a Page and to an Instagram Business or Creator account, never to a personal profile or a personal Instagram account (§5). Switching an Instagram account to Creator is free and takes a minute; making a Page is the same. The chat says so when it is needed, and says how.

**The order of things.** Meta's reviews gate everything that reaches the public (§13), and they are the same reviews K5 already needs, with four more permissions. So: start the review now with posting in it (G0); build Facebook and Instagram posting on the connectors that exist (G1); the chat (G2); comments (G3); the web's calendar and insights (G4); other networks (G5); adverts last and separately, because they spend money (G6).

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Account | One place a post goes: a Facebook Page, an Instagram professional account, a LinkedIn profile or company page, a TikTok account, a YouTube channel. Each is one connection |
| Post | One piece of content on one account: a caption and zero or more media, published at a time. The same words on three accounts are three posts, from one draft |
| Draft | The caption and media before they go out, in the `posts` table with `status = draft` |
| Scheduled | A post with a time, waiting. Taskiem holds it and publishes it then; Facebook's own scheduling is not used, so every network behaves the same (§6.3) |
| Network | A platform: Meta (Facebook and Instagram), LinkedIn, TikTok, X, YouTube |
| Professional account | What a network lets an app post to: a Page, an Instagram Business or Creator account, a LinkedIn member who allowed it, a company page |
| Media | A photo or video, held in the organisation's own storage (§6.2), handed to the network by a link it fetches once |
| Insights | What the network reports about a post or an account: reach, likes, comments, saves, followers |

## 3. Where we are

| Exists | Where |
| --- | --- |
| Taskiem's Meta app, with one-click connections for a Page (`messenger@1`) and an Instagram professional account (`instagram@1`), the Page token kept, webhooks routed to the business by account key | [Messenger and Instagram](../integrations/messenger-and-instagram.md), `connectors/meta` |
| Replying to people who write to a Page or an Instagram account, within Meta's window; never twice | the same |
| Meta's App Review and Business Verification: **not done**. Only people with a role on the app can connect | [business connectors §14](business-connectors.md#14-people-accounts-and-reviews) |
| Schedule triggers, text, transform, approval and AI steps; the WhatsApp **yes** before anything starts; approvals as WhatsApp buttons | [workflow builder](../workflow-builder.md), [WhatsApp](../whatsapp.md) |
| Known tables, defined once and made wherever a template needs them; rows changed from chat after a yes | [no sheet needed](no-sheet-needed.md) |
| A photo sent in chat with a caption becomes a catalogue item (`add product Jollof rice 2000` with a photo), the photo kept in the organisation's storage and served publicly on the storefront | [WhatsApp](../whatsapp.md), [storefronts](../storefronts.md) |
| Moderation of everything a business publishes (storefront pages, forms, catalogue items): scams, impersonation, prohibited goods; verification levels | [moderation](../moderation.md) |
| Email marketing with consent: Mailchimp, Brevo, Resend | [business connectors](business-connectors.md) |
| Plan limits counted per month, with overage and holds (`whatsapp_templates_monthly`) | `deploy/plans.yaml`, `engine/runtime/limits.go` |

Not built, and what this plan adds: publishing on any network; comments; scheduling content; the `posts` and `social_accounts` known tables; the chat commands; the posting limit; LinkedIn, TikTok, X and YouTube; adverts.

## 4. What stays as it is

- **Messages.** `messenger@1` and `instagram@1` keep their messaging triggers and actions, and the 24-hour rule. Posting adds actions to the same connectors and the same connection: a business connects a Page once and gets both.
- **The Meta app.** One app for messages and posts. The review (§13) asks for the posting permissions beside the messaging ones; nothing is built twice.
- **Storefronts.** A catalogue item is not a post. A template (§9) makes a post from one when the owner wants that; the catalogue keeps its own photos and its own words.
- **Consent for marketing.** A post is public on the person's own account and needs nobody's consent. Sending a post's link to customers by WhatsApp or email is marketing, and goes through the existing consent rules; nothing here loosens them.
- **Moderation.** What may not be published on a storefront may not be posted from Taskiem either (§11).

## 5. What the networks allow

Read from each network's public developer documentation on 6 November 2026; every line marked **G0 confirms** is read again and recorded in the integration page before it is built, as K0 did.

| Network | Posts to | Media | What the person needs | App review | Notes |
| --- | --- | --- | --- | --- | --- |
| Facebook | A Page (`/{page-id}/feed`, `/photos`, `/videos`) | Text, link, photo, several photos, video | A Page (free) | `pages_manage_posts`, `pages_read_engagement` (Advanced Access) | Personal profiles cannot be posted to by any app. Facebook can hold a scheduled post itself (`scheduled_publish_time`); not used (§6.3). **G0 confirms** the Reels endpoint |
| Instagram | A Business or Creator account linked to a Page | Photo, video, Reel, carousel (up to 10), story | Switch the account to Creator or Business (free, in the app); link it to a Page | `instagram_basic`, `instagram_content_publish`, `instagram_manage_comments`, `instagram_manage_insights` | Two steps: create a media container from a public link, then publish it. Media must be fetchable by Meta at a public https link. At most 25 posts per account per 24 hours (Meta's rule). No text-only posts. **G0 confirms**: accounts with no Page (Instagram Login), the stories endpoint, carousel limits |
| LinkedIn | A member's own profile, or a company page they administer | Text, link, one or several images, video | A LinkedIn account | Community Management API access (`w_member_social`, `w_organization_social`), applied for per app | Posting to a personal profile is allowed here, unlike Meta. **G0 confirms** the current product name and the review's requirements |
| TikTok | The person's own account | Video; photos (carousel) | A TikTok account | Content Posting API; the app must pass TikTok's audit, or every post is private to the poster | Direct post or "send to inbox" (the person finishes in the app). **G0 confirms** audit terms and limits |
| X | The person's own account | Text, images, video | An X account | Paid API tiers per app; posting access depends on the tier | Cost to Taskiem per month. Built only if asked (§8, G5) |
| YouTube | A channel | Video, Shorts | A Google account with a channel | OAuth verification of the app; daily API quota | Uploads are large; through the organisation's storage, never through chat |
| Adverts (Meta, TikTok, Google) | An ad account | Campaigns, ad sets, ads, budgets | An ad account with a payment method | Marketing API access, separate and stricter | Spends money: §12 and G6 |

What this means for "anyone": a person needs a Page or a Creator account on Meta, which they can make in minutes and which the chat walks them to; on LinkedIn, TikTok and YouTube their own account is enough. WhatsApp Status has no API on any plan, and Taskiem never pretends otherwise.

## 6. How a post works

### 6.1 The record: two known tables

Two [known tables](no-sheet-needed.md#54-known-tables), made in the organisation the first time anything needs them:

**`social_accounts`**: one row per connected account. `network` (`facebook`, `instagram`, `linkedin`, `tiktok`, `youtube`), `account_id`, `name` (the Page's name, the Instagram username), `connection` (the connection's name), `connected_at`, `status` (`connected`, `expired`). Written by the connection, read by the chat and the templates so "my Instagram" resolves to one row. Never edited by hand.

**`posts`**: one row per post per account. `caption` (text), `media` (links into the organisation's storage, 0 to 10), `network`, `account_id`, `publish_at` (time), `status` (`draft`, `scheduled`, `publishing`, `published`, `failed`, `cancelled`), `published_at`, `post_id` and `permalink` (from the network), `error`, `source` (`chat`, `template:<id>`, `web`, `api`), `run_id`. Insights (§6.6) are their own columns filled later: `reach`, `likes`, `comments`, `saves`, `insights_at`.

A post made from chat, from a template or on the web is the same row. Changing a row's `publish_at` or `caption` from chat ("move today's post to 7pm") is a table change after a yes, as R4 defines it; the scheduler reads the row when the time comes, so the change holds.

### 6.2 Media

A photo or video to post is held in the organisation's own storage (the same bucket as storefront photos and form files), under `social/`, cleaned of its metadata, counted against the organisation's storage. The network fetches it by a **signed link on the public host**, good for one hour, made at publish time; the link is never shown to the person and never reused. A photo sent in chat is written there as the draft is made, so nothing is held only in memory. A post's media are deleted when the row is deleted, and after `max_retention_days` for failed and cancelled posts.

A video for Instagram or TikTok is checked for the network's limits (length, size, aspect) before the row is scheduled, with the network's words for what is wrong, so a 20-minute video is refused at the draft, not at six o'clock.

### 6.3 Scheduling and publishing

A scheduled post is a row with `publish_at`. One platform workflow per organisation, `social-publish`, made with the first table (as the storefront's order workflow is), runs on a minute schedule, takes rows due (`status = scheduled`, `publish_at <= now`, claimed with `SKIP LOCKED`), and for each runs the account's `publish` action. Taskiem does the holding, on every network alike, so a person sees one calendar and one set of words; Facebook's own scheduling, which Instagram and the others lack, is not used.

`publish` is an **unsafe write**, as `send_message` is: Meta's publish takes no idempotency key. Instagram's two steps give one anyway: the container id is written to the row (`status = publishing`) before the publish call, and a retry with a container already published is read as done. Facebook's `/feed` returns the post id; a retry after an unknown outcome reads the Page's recent posts for one with the same message and media within the last minutes before posting again, and otherwise parks the row for a person, as other unsafe writes do. A post is never published twice.

Meta's 25 posts a day per Instagram account, and each network's rate, are counted in the row's account; the 26th is kept for tomorrow with a message to the owner, not refused silently.

### 6.4 The chat

The chat is where most people will post from, and every command reads back exactly what will go out, to which accounts, when, and waits for **yes** ([WhatsApp](../whatsapp.md), the conversation's one rule). Commands, in every language the chat has (`engine/lang/intents`):

| Said | What happens |
| --- | --- |
| A photo (or several, or a video) with a caption | A draft: the caption, the media, the accounts it will go to (every connected account unless named), and **Now**, **At a time**, **Keep as draft** |
| "post this to Instagram and Facebook at 6pm" with a photo | A draft to those two accounts at 18:00 today (tomorrow if past), read back, **Yes** |
| "post: We open 8am tomorrow" | A text post to the accounts that take text (Facebook, LinkedIn, X); Instagram is left out and the reply says so |
| "write a caption for this" with a photo | An AI step drafts three captions in the organisation's voice (its name, what it sells, its last captions), the person picks one or writes their own; nothing is posted until the yes |
| "post one item from my catalogue every Monday 9am" | The template of §9 set up from chat, with its params read back |
| "what's going out this week" | The scheduled rows, one line each, with **Change** and **Cancel** per row |
| "move tonight's post to 7pm", "cancel Saturday's post" | A row change after a yes (R4) |
| "how did yesterday's post do" | Insights for the row, if the network has them yet |
| "connect my Instagram", "connect my Page" | The connect link (C2 of chat as the way in), and when the account is personal, the three steps to make it a Creator account, with Meta's own words |
| "stop posting" | Every scheduled row cancelled, the publishing workflow paused; **start posting** resumes |

Captions written in chat are posted as written: no hashtags added, no link shortened, no emoji changed. Nothing is ever posted without a yes, and the yes says where and when.

### 6.5 The web

The Tables page shows `posts` as any table. G4 adds a **Posts** page: a week calendar of scheduled and published rows per account, a draft editor with the media picker and the AI caption, and the insights per post. The page writes the same rows; it is a view, not a second system.

### 6.6 Insights

A `get_insights` read on each connector (Instagram: `/{media-id}/insights`; Facebook: `/{post-id}/insights`; LinkedIn and TikTok where their APIs give them) fills the row's insight columns on a schedule (a day after publishing, then weekly for a month). A template (§9) tells the owner every Monday which posts did best. Insights are the network's numbers as it gives them, with the network's names; Taskiem adds no scores of its own.

## 7. The connectors

### 7.1 Meta: `messenger@1` (Facebook) and `instagram@1`

New actions on the existing connectors, on the existing Page token; the same connection serves messages and posts.

| Action | Class | What it does |
| --- | --- | --- |
| `publish` | unsafe write | A post: `caption`, `media` (0 to 10 signed links; Instagram needs at least one), `kind` (`post`, `reel`, `story`, `carousel`; Facebook: `post`, `photos`, `video`, `reel`), `link` (Facebook). Returns `post_id`, `permalink`, `published_at`. Instagram: create the container(s), poll until ready, publish; the container id is in the output of a parked step |
| `delete_post` | idempotent write | Removes a post by id |
| `get_post` | read | The post as the network has it, and whether it still exists |
| `get_insights` | read | Reach, impressions, likes, comments, saves, shares for a post; followers and reach for the account |
| `list_comments` | read | Comments on a post (sealed: names and text are personal) |
| `reply_comment` | unsafe write | A reply to a comment, by the comment's id; Instagram also `hide_comment` |

New trigger, both connectors, on the app webhook (routed by Page or Instagram account id as messages are): **`comments`**: a comment on a post (`comment_id`, `post_id`, `text`, `from_id`, sealed), for "reply in DM to everyone who commented PRICE" and for a workflow that assigns comments to a member. `from_id` and `text` reach an `ai` step only as placeholders unless let through, as messages do.

Permissions the Meta app asks for in the review, beside the four of K5: `pages_manage_posts`, `pages_read_engagement`, `pages_manage_engagement`, `instagram_content_publish`, `instagram_manage_comments`, `instagram_manage_insights`, `read_insights`. Each with a screencast of the use, from the real product.

### 7.2 `linkedin@1`, `tiktok@1`, `youtube@1`, `x@1` (G5)

Each a platform app on the K1 pattern ([0051](../decisions/0051-platform-apps.md)): one app Taskiem registers, one-click connection with OAuth, the token refreshed by Taskiem, the account key recorded, the same `publish`, `get_post`, `delete_post`, `get_insights` actions with the network's `kind` values, and the same two tables. TikTok has `publish` in two modes (`direct`, `inbox`) as its API does. YouTube's `publish` takes a video from storage, with `title`, `description`, `privacy` and a thumbnail. None has a comments trigger in G5 unless its API pushes comments (LinkedIn and TikTok poll; a later milestone).

Each is built only once its review is in hand, in the order of where the first businesses actually post (§8); X only when asked for, and its monthly cost shown to the founders before the app is registered.

## 8. Milestones

Each milestone ships with its templates, its chat commands, its docs page, and workflow tests on fake services, as every plan before it.

### G0: facts, accounts and reviews (people, S of engineering)

1. **Add the posting permissions to Meta's App Review** with the messaging ones, with screencasts recorded from staging (G1 on staging behind the app's test users). Meta's Business Verification, if still open, first.
2. **Confirm every G0 line in §5** from the networks' current documentation and record it in the integration pages.
3. **Register the LinkedIn and TikTok apps and start their reviews**, so G5 is not waiting on them later. YouTube's and X's when their milestone is chosen.
4. **Test accounts:** a Page and an Instagram Creator account for CI's recorded fixtures, beside K0's.

**Done when:** the Meta review is submitted with posting in it; every G0 line has an answer.

### G1: Facebook and Instagram posting (M; needs the Meta app, not its review, on staging)

The two known tables; media in storage with signed public links; `publish`, `delete_post`, `get_post` on both Meta connectors; the `social-publish` platform workflow and the never-twice rule; the limit (§10); the video checks; the first three templates (§9); the web's Tables view showing posts.

**Done when:** a test Page and Instagram account take a photo post, a carousel, a Reel and a text post (Facebook) from a template on a schedule, each exactly once across a lost answer; a 26th Instagram post in a day waits for tomorrow with a message; the limit holds.

### G2: posting from chat (M; needs G1, chat as the way in C2)

Every command of §6.4 on WhatsApp, Telegram and the web channel, in the five languages; the AI caption; the connect walk-through for personal accounts; drafts, changes and cancellations as row changes after a yes.

**Done when:** a person with no business beyond their own name signs up in chat, connects an Instagram Creator account and a Page by the links the chat sends, posts a photo to both at a chosen time with a caption the AI drafted and they approved, sees it listed, moves it, and sees it published; the chat evaluation suite has the commands in every language.

### G3: comments (S; needs G1, the Meta review for comments)

The `comments` trigger, `list_comments`, `reply_comment`, `hide_comment`; the "reply in DM to everyone who commented PRICE" template; comments assigned to a member as a WhatsApp message.

**Done when:** a comment on a test post starts the workflow once, the DM goes out inside the messaging window's rules, a second delivery of the same comment does nothing.

### G4: the Posts page and insights (M; needs G1)

The week calendar, the draft editor with media and the AI caption, per-post insights; `get_insights` on both connectors; the insights schedule and the Monday template.

**Done when:** the page makes, changes and cancels the same rows the chat does, and a post's reach appears a day after it went out.

### G5: more networks (L; one at a time, each needs its review)

`linkedin@1` first (business owners and professionals already post there; personal profiles allowed), then `tiktok@1` (where the creators are), `youtube@1` when a business asks, `x@1` only if asked and its cost accepted. Each: the app, the connection, the actions, the tables' `network` value, the chat's account names, fixtures, a template.

**Done when, per network:** the G1 and G2 acceptance on that network's test account.

### G6: adverts (L; needs the Marketing API reviews; decided separately before it is built)

A `meta_ads@1` connector on the Meta app's ad account permissions: campaigns, ad sets, ads from a published post ("boost this post"), budgets and schedules, results. Under the organisation's approval policy and a monthly spend limit (§12), with every spend an approval the first time and a **First spend on this account** warning, as payouts have. The person's own ad account and payment method; Taskiem never holds ad money. TikTok and Google Ads after, if asked.

G6 is listed so the shape is known; it is decided on its own when G1 to G4 have users, and nothing before it depends on it.

## 9. Templates

In the library, offered by the chat and the Start page as the others are, each with its tables made wherever used:

| Template | Trigger | What it does |
| --- | --- | --- |
| `social-post-from-table` | schedule | Every day (or the days chosen) at a time, takes the next row of `posts` marked `draft` with the day's date, or the oldest draft, and publishes it to its accounts |
| `social-catalogue-item-weekly` | schedule | Every Monday at 9, picks a catalogue item (round-robin, available ones only), drafts a caption with its name and price, asks the owner on WhatsApp, posts on yes |
| `social-announce-new-product` | table row added (the catalogue) | A new item with a photo becomes a draft announcing it, sent to the owner for a yes, then posted everywhere |
| `social-post-everywhere` | chat, or a form | One caption and media to every connected account at once, or at a time |
| `social-comment-price-dm` | `comments` | Someone comments PRICE (or the words chosen) on a post: a DM with the price and the order link, within the messaging rules; the comment replied to with "sent you a DM" |
| `social-weekly-insights` | schedule | Every Monday, the week's posts ranked by reach, to the owner on WhatsApp |

## 10. Limits and plans

A new monthly limit, `social_posts_monthly`, counted at publish, per organisation across accounts: beyond it posts are held (status `scheduled`, with a message saying why) until the month turns or the plan changes; nothing is lost. Proposed in `deploy/plans.yaml`: Shop 60, Shop Plus 300, Starter Trader 100, Growing Business 500, Business 2,000, Enterprise 0 (no limit). AI captions spend `ai_monthly_tokens` as every AI step does. Media counts against storage. The numbers are settled in G1 with the founders.

## 11. Rules

- **Nothing posts without a yes.** From chat, a template or the web, a post goes out only after a person said yes to those words, those media, those accounts and that time, or set up a template that says so and read its params back. A template's post can also ask for a yes each time (`approval` step), and the catalogue templates do by default.
- **Moderation applies.** The checks that publishing a storefront goes through ([moderation](../moderation.md)) run on a draft: scams, impersonation, prohibited goods, and a post is refused with the reason. A suspended organisation posts nothing.
- **No spam machinery.** Taskiem posts to the person's own accounts only. No following, unfollowing, liking, mass commenting, DM campaigns to people who did not write first, or posting the same content to accounts the person does not administer. The networks forbid these and they are not what the product is for.
- **The network's rules are Taskiem's.** Rates, media limits, the 24-hour messaging window for DMs from comments, and each network's content rules are enforced before a call is made, with the network's own words in the refusal.
- **Personal data.** Commenters' names and text are sealed, reach an `ai` step as placeholders, and are deleted with the comment's row after `max_retention_days`. Nothing a commenter wrote is posted anywhere by Taskiem.
- **Deleting.** Deleting a row that was published asks whether to delete the post on the network too; the connection's own uninstall at the network ends everything.

## 12. Adverts: what is different

An advert spends money from the person's ad account. So G6, when decided: every campaign is an approval under the organisation's policy; a monthly cap (`ads_monthly_kobo`) per organisation set by the owner, with Taskiem refusing a budget that would pass it; **First spend on this ad account** as payouts have **First payment to this account**; daily spend reported to the owner; and never an ad account Taskiem holds for the person. The Marketing API reviews are separate from the posting review and later.

## 13. People, accounts and reviews

| Item | Who | Lead time |
| --- | --- | --- |
| Meta App Review: the seven posting permissions of §7.1 added to K5's four, as a Tech Provider, with screencasts | Founders, with engineering | Weeks; rounds |
| Meta Business Verification (if still open) | Founders | Days to weeks |
| LinkedIn Community Management API access | Founders, with engineering | Weeks |
| TikTok app audit for the Content Posting API | Founders, with engineering | Weeks |
| YouTube OAuth verification and quota | Engineering | Weeks |
| X API tier (a monthly cost) | Founders decide | A day once paid |
| Privacy policy and terms naming each network's data use, and what Taskiem does with insights and comments | Founders, legal | Before the reviews |
| Test accounts: a Page, an Instagram Creator account, LinkedIn and TikTok test accounts | Engineering | A day |

## 14. Testing

| Level | What |
| --- | --- |
| Conformance | Each connector's publish, delete, insights and comments on recorded fixtures from the test accounts |
| Never twice | A publish whose answer is lost on Instagram (container written) and on Facebook (recent posts read) is published once; a parked step resumes right |
| Limits | The 26th Instagram post of a day waits; the plan's monthly limit holds posts and releases them |
| Media | A link expires after an hour; metadata is stripped; a too-long video is refused at the draft with the network's words |
| Chat | Every command of §6.4 in the chat evaluation suite in five languages; nothing posts without the yes; a personal Instagram account gets the walk-through, not an error |
| Moderation | A prohibited draft is refused; a suspended organisation posts nothing |
| Templates | Workflow tests per template on fake Meta services |

## 15. Risks and open questions

| Risk | What we do |
| --- | --- |
| Meta's review refuses or stalls; posting cannot open to the public | G1 and G2 still ship for app test users (the founders' and pilots' accounts) and for staging; the review is resubmitted with what Meta asks; nothing else in the product waits on it |
| Instagram accounts with no Page (Instagram Login) are common among creators | K0's open question, measured with the first people; a second Meta app if Meta requires it, as K5 planned |
| A network changes its API or rules | Each connector reads the network's documentation date in its manifest, as the Meta ones do; a breaking change is a connector version |
| People expect WhatsApp Status posting | The chat says plainly that no app can post a Status, and offers the networks that can |
| A post the person regrets | The yes reads back everything; a scheduled post can be cancelled until the minute it goes; `delete_post` after |
| TikTok's unaudited mode posts private videos | Not offered until the audit passes; the chat does not list TikTok before then |
| Cost of X's API | Decided by the founders with the number in front of them; off by default |

Open: whether a `posts` row for one draft on three accounts is three rows (proposed: yes, one per account, joined by `draft_id`, so each has its own status and insights) or one with three statuses; whether stories, which vanish in a day, belong in the same table (proposed: yes, `kind = story`, no insights); the plan numbers of §10.
