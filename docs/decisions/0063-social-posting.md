# 0063 — Social posting: a person or a business posts to their own social accounts from workflows and from chat, on the connectors and the Meta app that exist

Date: 2026-11-06 · Status: **Proposed** · Plan: [social posting](../spec/social-posting.md)

## Context

Taskiem connects a business's Facebook Page and Instagram account to answer the people who write to them ([business connectors §6](../spec/business-connectors.md#6-instagram-and-facebook-messages), K5, on Taskiem's Meta app, [0051](0051-platform-apps.md)). It cannot post. The businesses Taskiem is for sell on Instagram and WhatsApp, announce on Facebook, and post by hand from a phone; so do the tailors, churches, creators and professionals who are one person and would use Taskiem for this alone.

The founder, on 6 November 2026: social media posting and adverts should be "a generic feature for anybody or business, just a regular person creating automations". A person alone is already an organisation on Taskiem (sign-up on the web or in chat makes one), so nothing in tenancy stands in the way. What stands in the way is what the networks allow (Meta posts only to Pages and professional accounts, never personal profiles; LinkedIn and TikTok to a person's own account), and their reviews, which already gate K5.

## Decision

1. **A post is a workflow action.** Publishing is an action on a network's connector (`instagram@1`, `messenger@1` now; `linkedin@1`, `tiktok@1`, `youtube@1` and `x@1` later, each a platform app on the K1 pattern); a schedule is a schedule trigger; the caption a text or AI step; the yes an approval; the record two known tables, `posts` and `social_accounts`. No new engine, no second product. Everything the chat, the templates and the web do is a row in `posts`, which one platform workflow per organisation publishes when due.
2. **One Meta app, one connection, one review.** Posting uses the Page and Instagram connections K5 made, with seven permissions added to the same App Review. The messaging rules of K5 are unchanged.
3. **Chat-first.** A photo with a caption on WhatsApp or Telegram becomes a draft read back with its accounts and time, posted on **yes**; drafts are moved and cancelled as row changes after a yes. An AI step drafts captions; it never posts. A person whose Instagram is personal is walked to a Creator account in the chat, with Meta's own steps, because no app can do otherwise.
4. **Taskiem holds the schedule**, on every network alike, and never publishes a post twice: Instagram's container id and Facebook's recent posts give the retry its memory, and an unknown outcome parks for a person, as every unsafe write does.
5. **For anyone.** Nothing here needs a storefront, an invoice or a bank. What it needs is what the network needs: a Page or a professional account on Meta, the person's own account elsewhere.
6. **Only the person's own accounts, with a yes, within the network's rules.** No following, liking, mass commenting or DMs to people who did not write first. Moderation's checks run on every draft; a suspended organisation posts nothing. Commenters' words are sealed and never reposted.
7. **A monthly posting limit per plan**, held not lost, beside the networks' own rates.
8. **Adverts are decided separately.** The shape is known (an ad connector on the person's own ad account, every spend an approval, a monthly cap, first-spend warnings, Taskiem never holding ad money), and it is built only after posting has users and the Marketing API reviews are in hand.
9. **Order:** G0 the reviews with posting in them; G1 Facebook and Instagram posting; G2 chat; G3 comments; G4 the Posts page and insights; G5 LinkedIn, then TikTok, YouTube when asked, X only if asked and paid for; G6 adverts on its own decision.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| A separate scheduling product in the web app with its own store | Two records of the same post; the chat would be a second client of a second system; the workflow engine already holds schedules, approvals, retries and limits |
| Use Facebook's own scheduled posts | Instagram, LinkedIn and TikTok have none; one calendar with one set of words needs Taskiem to hold every schedule |
| Post to personal Facebook profiles and personal Instagram accounts | No app can; Meta's API does not allow it. The chat says so and walks the person to a Page or a Creator account |
| Build every network at once | Each has its own review with weeks of lead time, and X costs money monthly; one at a time, by where people post |
| Adverts with posting | Spending money needs approvals, caps and warnings the way payouts have them, and a separate, stricter review; posting should not wait for it |
| Engagement automation (follow, like, comment campaigns) | Forbidden by the networks, and not what Taskiem is for; a business that wants reach posts good content and answers the people who write |

## Consequences

- K5's Meta review grows by seven permissions and four screencasts; submitting it is the first task, and it decides when posting opens to the public.
- Two known tables, six templates, a platform workflow per organisation, a monthly limit, a storage prefix with signed public links, and the chat commands in five languages.
- The Posts page is the first web page whose only job is a view over a known table; the pattern should serve other known tables later.
- Each later network is a connector and an app, on the pattern 0051 set, with its review started early.
- When adverts are decided, they take a decision of their own and build on §12 of the plan.
