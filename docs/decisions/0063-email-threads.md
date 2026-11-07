# 0063 — Email threads: tagged reply addresses, threading headers and replies that wake the ticket's run

Date: 2026-11-07 · Status: **Accepted** 2026-11-07 (built: [email threads](../email.md#threads), [tickets](../spec/tickets.md)) · Amends: [0038](0038-inbound-email.md) ("replying from a workflow is a natural next step")

## Context

Support-ticket templates need an email conversation per ticket: the customer writes in, the business answers from the workflow, the customer replies, and the reply reaches the run that handles that ticket. Today an email trigger exposes neither `In-Reply-To` nor `References`, its address takes no per-ticket tag, every message starts a new run, and Taskiem's own customer email (`notify@1 message`) sets no Reply-To or threading headers. Zoho Mail threads, but through a business's own mailbox and its `thread_id`; its trigger's `correlation` wakes a run waiting on a `signal` step, which is the engine mechanism to reuse.

## Decision

1. **Reply tags.** An email address takes a subaddress, `<id>+<tag>@<domain>` (lower-case letters, digits and hyphens, at most 40 characters), exposed as `trigger.email.tag`. For threads Taskiem makes the tag itself: the ticket key in lower case, then 12 base32 characters of an HMAC of the address and key under a secret of the address (`email_endpoints.reply_key`, 32 random bytes). A tag that does not verify gives nothing: a stranger who guesses `t-1043` cannot write into that ticket. Unknown base addresses are still refused at `RCPT`.
2. **Threading headers in.** `trigger.body.in_reply_to` (one Message-ID) and `trigger.body.references` (at most 20, newest last), each id at most 250 printable characters.
3. **Sending in a thread is `email@1 send_in_thread`**, beside `file_url`, not a `notify@1` action: it needs the workflow's address, its secret and the thread records, which belong to the email trigger, and the email connector already acts for the run's tenant with no connection. It sends through the platform outbox exactly as `notify@1 message` sends email (from the platform's sending address in the business's name, the same per-recipient limits and counting, sealed recipient and text), adding Reply-To (the tagged address), a Message-ID Taskiem makes (derived from the step's idempotency key under the address's secret, so a retry sends the same one), In-Reply-To and References, and the subject tag `[#T-1042]` once. It needs `threads: true` on the workflow's email trigger.
4. **Replies wake the ticket's run.** The opt-in is the email trigger's `threads: true`; without it nothing changes. With it, the ticket is found by the verified tag, else by `In-Reply-To`/`References` matching a Message-ID Taskiem sent from that address, else by a `[#…]` subject tag from a proven sender Taskiem emailed in that thread. The reply is a signal, event `email.reply`, correlated by the ticket key, delivered **only to runs of the same workflow and environment** (two workflows of a business may both have a `T-1042`). If none waits but a run that sent in the thread is still going, the message waits in the inbox and is retried every 5 minutes for up to 7 days; if no such run is going, a new run starts with `trigger.email.thread` set.
5. **What was sent is recorded** in `email_thread_messages` (tenant-scoped, row-level security): address, ticket key, Message-ID, the run that sent it and the recipient's personal-data pseudonym (never the address). Rows are deleted with the run at the end of its retention; erasing the recipient clears the pseudonym.

## Alternatives considered

- **Correlating by `In-Reply-To` alone.** Mail apps drop or rewrite it (forwards, "new message to the same person", some webmail), and it says nothing when the customer starts a fresh email; the tagged Reply-To survives all of those. Kept as the second way.
- **A ticket key in the address with no secret** (`+t-1042`). Anyone could write into any ticket by counting.
- **Buffering the reply as an ordinary signal** when no run waits yet. Signals are buffered per business, not per workflow, and a buffered reply for a run that then ends is never read; holding the message in the inbox until the run waits or ends loses nothing.
- **A `notify@1 email_thread` action.** Tell me does not know the workflow's address; the thread state would live in two places.
- **The subject tag from anyone.** A subject is free text; trusting it would make the tag's secret pointless. It counts only from a proven sender already in the thread.

## Consequences

- wd/v1: the email trigger's `threads` (an addition); `trigger.body.in_reply_to`, `references`, `trigger.email.tag`, `thread` and `matched_by` (additions).
- The mail role keeps the tag of the address a message came to; the api role verifies it.
- A message listed in the trigger's tab may be **Replied** (it woke a run) besides started, refused and confirmation.
- Phase 2 (the ticket templates) builds on this; nothing here makes tickets.
