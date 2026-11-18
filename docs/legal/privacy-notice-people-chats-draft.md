# DRAFT: privacy notice, chats between people

> **DRAFT FOR COUNSEL. Not in force.** Engineering wrote this from [decision 0069](../decisions/0069-people-chatting-with-people.md) and what the product does ([people chatting](../spec/people-chatting.md), its As built sections). It is for the lawyer reviewing the privacy notice before stage 5a ([needs people](../needs-people.md), PC1) to correct, complete and put into the notice's own structure and legal terms (controller, lawful bases under the NDPA 2023, retention, transfers, rights, contact for the data protection officer). Where it says **[counsel]**, a legal answer is needed. Bracketed notes are for counsel, not for the published text.

## Chats with other people

You can chat with other people on Taskiem: one to one, and in groups.

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
| A group's messages, photos and files (one copy) | The last member leaves; your own view of it goes when you delete the group or your account |
| The other person's copy | They delete it or their account; deleting yours does not delete theirs |
| A report and what was sent with it | A year after a moderator reviews it |
| A message the checks held | 90 days after it was held or decided |
| The checks' decisions (not the words) | 90 days, or until the sender deletes their account |
| Moderators' decisions and appeals | While the account exists **[counsel]** |
| Records of who opened what | **[counsel: as audit records]** |

### Groups, and ordering together

**[Engineering note: stage 5b. What the product does is in [people chatting](../spec/people-chatting.md), "As built: stage 5b" sections, and [the 5b checklist](../spec/people-chatting-5b.md).]**

- **What we keep.** A group's messages, photos and files are kept **once for the whole group** on our servers, sealed (encrypted) under a key of that group's own, so they show on every phone and computer each member signs in on. **Our servers can open them. Groups are not end-to-end encrypted.**
- **Who sees what.** Members see each other's names and photos as each person's own settings allow (a member's contact name for someone, if they saved one). **Phone numbers are hidden** in groups, unless a member chose to show theirs, and never to someone they blocked. If your account is deleted, your messages stay in the group, shown as from "Deleted account". Admins see who is invited and who is waiting to join; anyone with the group's invite link sees its name, photo and member count before joining.
- **History from joining.** You see a group's messages only from when you joined, never what was said before. If you leave or are removed, **you keep your copy up to then** (until you delete it), and see nothing new. Deleting the group on your phone deletes your copy, not the other members'. When the last member leaves, the group and its messages are deleted.
- **Reports.** If you report a group or one of its members, the **last 20 messages you can see**, with their photos and files and who sent each, go to our moderators, **only if you agree** when you report. The group's name, description and members at that moment go with it. Reports are kept as for chats with people (above).
- **Scam checks in groups.** In a group less than a week old, or from someone who joined by the group's link in the last day, our automated checks look at a message for known scams, for the members who have not added the sender as a contact. As in chats with people, **they keep only their decision** (allowed, warned or held) and its reasons, not the words. A held message reaches no one and is kept for up to 90 days for a moderator. **[counsel: lawful basis, as for the checks above.]**
- **Moderators** can turn a group's invite link off or close a group; its admins are told why and can appeal.
- **Requests from the authorities** may cover groups, under the process above: only messages from the person's own time in each group, and never what they deleted for themselves. **[counsel: whether a request about one member may reach other members' messages in the same group.]**

**Ordering together.** In a chat or a group, people can fill one shared basket from one business, each adding their own items; one person (the placer) places it.

- **What the business sees:** **one order, from the placer**, with the names of whose items are whose (each person's own name on Taskiem, never a nickname someone saved) and the placer's note. **The business never sees the group, its members as a group, or the chat**, and is never told who paid which share.
- **Paying:** either the placer pays all and asks each person for their share with a "Your share" card (Taskiem never checks or moves that money), or each pays the business for their own share through the business's pay link. Taskiem does not move money between people.
- **What we keep:** the basket, who added what, each person's share and whether it was paid, for as long as the order is kept. **[counsel: retention of a shared basket, as for orders.]**

### Security

Chats are sealed at rest under each person's key, and only our servers can open them. An outside security review is done before chats between people launch. If there is a breach, we tell you and the regulator as the law requires. **[counsel: NDPA breach notification wording.]**
