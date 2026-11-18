# Moderating people chats

For Taskiem's moderators: operators in the [operator console](../operator-console.md) who handle reports, held messages, decisions about accounts and appeals in chats between people ([decision 0069](../decisions/0069-people-chatting-with-people.md); [people chatting](../spec/people-chatting.md) §8 and "As built: stage 5a gaps, safety and moderation"). Lawful requests have [their own runbook](lawful-requests.md).

**The rule.** People chats are kept by Taskiem, sealed on its servers and readable by them. They are **not** end-to-end encrypted, and nothing you write to a person may say they are. No one at Taskiem reads a people chat except a moderator handling a report of it, or under a lawful request. You open only what a report, a held message or a request puts in front of you, never a person's chats at large: the console has no way to, and you must not ask an engineer for one.

**Every opening is recorded.** Opening a report, a held message, or a photo or file in them asks why. Your name, your reason, what you opened and the time go into the platform audit chain (Platform audit in the console) in the same transaction that reads it; if it cannot be recorded, nothing is shown. Every decision asks for your passkey again. Write reasons someone else could check later: "Report from the queue", "Second look after the appeal", not "checking".

The moderation policy that sets what is allowed is a people item ([needs people](../needs-people.md), PC6); until it is agreed, use the rules below and escalate anything they do not cover.

## The queue: People safety

The console's **People safety** page shows what waits (reports of people, reports of businesses, held messages, appeals, lawful requests) and how much the kept chats take on disk. Aim to clear reports and held messages within one working day, appeals within three.

### Reports of people

Reports come from a chat or a message request, with the reporter's consent, carrying the last 20 messages of the reporter's copy and the photos and files in them (up to 50 MB, newest first; what did not fit is marked "not kept"). They are sealed under the report's own key at report time, so the report keeps its evidence after either person deletes the chat or their account. The list shows who was reported, why, how many people reported them, and whether it came from a request; never the words.

1. Open the report and give your reason. Read the messages oldest first. Open a photo only when the words do not settle it ("Show the photo" is recorded each time).
2. Decide (below) what, if anything, happens to the person reported. "Warn, limit, suspend or close" takes you to their standing with the report attached to the action.
3. **Mark reviewed** with a note of what you did. A reviewed report is deleted a year after review, with its files; an open one stays until it is reviewed.

A report whose reporter has since deleted their account says so; it stays, detached from them. A report whose reported person deleted their account shows "Deleted account": mark it reviewed.

### Reports of businesses

People's reports from their chats with businesses (stage 2), with the messages they sent. Open them the same way. To act on a business, open it under **Tenants** (suspend its page, remove its Discover listing) as the [storefront moderation](../moderation.md) guide says; then mark the report reviewed.

### Held messages

The automated checks look at messages to strangers (requests and new chats) for scams: asking for a code, a prize, a fee, money to an account, sure returns. A message they hold never reaches the recipient; the sender is told it was not sent. It is kept sealed, with the checks' reasons, for you:

- **Release** one held by mistake (a seller's account number the buyer asked for, a code that is a recipe's). It is delivered then, as the sender wrote it, with its photo or file, past the checks and the limits it would have met. The sender is not told separately.
- **Confirm** a hold that was right. Nothing is delivered. Several confirmed holds from one person are a reason to open their standing.

Held messages go 90 days after they were held or decided, and at once when the sender deletes their account (if undecided). The **Latest verdicts** list shows what the checks decided (held or warned, and why) without the words.

## Decisions about a person

A person's standing page shows their signals: how many people reported them, held and warned messages, how many accounts used their devices, and what became of their message requests. It never shows their chats.

| Action | What it does | When |
| --- | --- | --- |
| **Warn** | They are told, with your reason. Shown as a warning for 30 days | A first report that is clearly true but not dangerous: rudeness, repeated unwanted requests |
| **Limit** | One message request waiting at a time and two a day, and the slower limits of a first week, for the days you set (7 by default) | Spam-like behaviour, many requests deleted or blocked, a warning ignored |
| **Suspend** | They can't message people or get messages; people writing to them are told the account "can't get messages right now". They can still read their chats. For days, or until lifted (0) | Scams, threats, harassment, sexual content sent to strangers; while you look into something serious |
| **Close** | As suspended at once; the account is deleted, as the person would delete it, 30 days later unless an appeal is open or you lift it | Fraud, repeated suspensions, illegal content. Escalate child sexual abuse material at once (below) |

Every action records your reason, your name and the time, and the person is told in plain words: a notification on their devices and a notice on You opening **Your standing**, where they read the decision and can appeal it once. Write the reason to them, plainly and without quoting the reporter: "You sent several people messages asking for their bank codes." **Lift** ends a limit, suspension or closure early; they are told.

## Appeals

An appeal is answered by a moderator other than the one who decided (the console refuses the one who decided). Read the decision, its reason and the report it came from, then:

- **Overturn** lifts the decision; the person is told your note and that it is lifted.
- **Uphold** keeps it; they are told your note.

Your note is sent to the person: say what you looked at and why, in a sentence or two. An open appeal holds a closure until it is answered.

## Escalation

- **A child at risk, or child sexual abuse material:** do not download or forward it. Suspend the account, mark the report reviewed with "escalated", and tell the founders at once; they report it to the authorities. Do not tell the person why.
- **Threats to someone's life:** suspend, and tell the founders the same day.
- **A request from the police or a court:** not here; see [lawful requests](lawful-requests.md).
- **Something the console cannot do** (open more than the report holds, read another chat): it is not allowed. Ask the founders, who decide whether a lawful request or a change to this runbook is needed.

## Storage

People safety shows the people chats' tables and their size; Prometheus has `taskiem_people_storage_bytes` and `taskiem_people_storage_rows`, and `TaskiemPeopleStorageLarge` opens a ticket ([reliability](../reliability.md#runbook-taskiempeoplestoragelarge)).
