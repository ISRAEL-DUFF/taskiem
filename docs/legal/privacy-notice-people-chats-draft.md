# DRAFT: privacy notice, chats between people

> **DRAFT FOR COUNSEL. Not in force.** Engineering wrote this from [decision 0069](../decisions/0069-people-chatting-with-people.md) and what the product does ([people chatting](../spec/people-chatting.md), its As built sections). It is for the lawyer reviewing the privacy notice before stage 5a ([needs people](../needs-people.md), PC1) to correct, complete and put into the notice's own structure and legal terms (controller, lawful bases under the NDPA 2023, retention, transfers, rights, contact for the data protection officer). Where it says **[counsel]**, a legal answer is needed. Bracketed notes are for counsel, not for the published text.

## Chats with other people

You can chat with other people on Taskiem: one to one now, and in groups later.

### What we keep, and who can read it

- **We keep your chats with people on our servers** so they show on every phone and computer you sign in on, with nothing to back up. Each person's copy is sealed (encrypted) under a key of its own.
- **Our servers can open them.** Your chats with people are **not end-to-end encrypted**. If you need chats no company can read, use an app that offers end-to-end encryption.
- **No one at Taskiem reads them,** except:
  - a moderator handling a report: when you or the person you chat with reports the chat, the last 20 messages, with the photos and files in them, are sent to our moderators with the reporter's agreement;
  - a moderator reviewing a message our automated checks held (below);
  - when the law requires it, under the process below.

  Each time a person at Taskiem opens a report, a held message or a file in them, we record who, what, why and when.
- **We never use your chats with people to train AI models or for advertising,** and we do not sell them. Business features that read chats with businesses (order drafts, translations) are off in chats with people.

### Automated safety checks

When someone writes to a person they have not chatted with before (a message request, or a new chat until the other answers), automated checks look at the words for known scams, such as asking for a code, promising a prize or asking for money to be sent. They keep only their decision (allowed, warned or held) and its reasons, not the words. A message they hold is not delivered; it is kept for up to 90 days so a moderator can release it, if it was held by mistake, or confirm the hold. **[counsel: lawful basis, legitimate interests and the balancing test; automated decision-making under the NDPA.]**

We also look at how accounts behave: how many requests they send and how many are accepted, deleted, blocked or reported, how new the account is, and how many accounts are used on one device. These set how many message requests an account may send.

### Reports and moderators' decisions

If you report someone, we keep your report, the messages and files sent with it, and what moderators did. A report is kept until a moderator has reviewed it and for one year after, then deleted. It stays if you delete your account, no longer linked to you. **[counsel: retention period; whether the reported person may see the report.]**

Moderators can warn, limit, suspend or close an account. If they decide something about you, we tell you in the app, in plain words, with the reason, and you can appeal; a different moderator reads your appeal. **[counsel: rights to contest automated or human decisions.]**

### Who you can find, and who can find you

**[Engineering note: the contact-matching words are stream B's, §5.3 of the spec; include them here: keyed hashes with a rotated key, nothing kept from matching, the method's honest limits.]**

### Requests from the authorities

Nigerian law can require us to give data, including the words of chats between people, to the police, courts or regulators. We answer only proper legal process; we give the least that is asked; a second person at Taskiem approves each request before anything is given; we tell you where the law allows; and we publish how many requests we receive, answer and refuse every six months at **taskiem.com/transparency** **[the real address]**. **[counsel: the laws that apply (Cybercrimes Act 2015, NDPA 2023, others), when telling the person is forbidden, cross-border requests.]**

### How long we keep it

| What | Kept until |
| --- | --- |
| Your copy of a chat, with its photos and files | You delete the chat or your account |
| The other person's copy | They delete it or their account; deleting yours does not delete theirs |
| A report and what was sent with it | A year after a moderator reviews it |
| A message the checks held | 90 days after it was held or decided |
| The checks' decisions (not the words) | **[engineering: 00475's retention]** |
| Moderators' decisions and appeals | While the account exists **[counsel]** |
| Records of who opened what | **[counsel: as audit records]** |

### Security

Chats are sealed at rest under each person's key, and only our servers can open them. An outside security review is done before chats between people launch. If there is a breach, we tell you and the regulator as the law requires. **[counsel: NDPA breach notification wording.]**
