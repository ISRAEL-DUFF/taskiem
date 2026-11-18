# Stage 5b checklist: groups and ordering together

Date: 18 November 2026 · Plan: [People chatting with people](people-chatting.md) §8, §9, §10, §11, §13, §14, §17 · Decision [0069](../decisions/0069-people-chatting-with-people.md) points 7, 8, 9

Every sentence of the plan that applies to stage 5b, as a checklist, so nothing is left out and nothing is called "left on purpose" without the founder agreeing. Each line ships on Android, iPhone and the web together, with server tests, web end-to-end tests and app tests. App screens are built to the app's own look ([mobile-app-look.md](mobile-app-look.md)), never the web's.

Status key: ☐ to build · ? needs the founder's answer first (see the end).

## A. Groups (§9, §13, §17 Q7, Q16, Q17)

- ☐ **Create a group:** name, optional photo, optional description, up to 256 people (§17 Q7).
- ☐ **Admins:** the creator is an admin. Admins can:
  - add people;
  - remove people;
  - make others admins;
  - choose who can post (everyone, or admins only) (§9).
- ☐ **Edit info:** name, photo and description; who may edit them (?A2).
- ☐ **Adding people:** an admin can add a person only if the admin has them as a contact (§9).
- ☐ **Who can add me to groups:** a new privacy setting, "Who can add me to groups" (everyone I've added as a contact, or nobody). When someone isn't allowed to add a person, that person gets an invite instead (§9 "people can choose that only contacts may add them") (?A3).
- ☐ **Invite link** for a group, which an admin can turn on, replace or turn off, with a QR code (§9).
- ☐ **Joining by the link:** the joiner sees the group's name, photo and member count before joining (?A4).
- ☐ **Leave a group;** remove a member; a member removed or leaving keeps their own copy up to that point (?A5).
- ☐ **History from joining:** a member sees the group's messages only from when they joined (§17 Q16).
- ☐ **Deleted accounts:** their messages stay, shown as "Deleted account" (§17 Q17, §14).
- ☐ **Storage:** one copy for the group, sealed under a group key, plus each member's own marks: read, deleted for me, starred, muted, archived (§13).
- ☐ **Numbers stay hidden** in groups unless a member chose to show theirs (§5.4, §17 Q5). Members see names and photos as each member's settings allow.
- ☐ **Messages in a group:** everything one-to-one chats have:
  - text, photos, voice notes, files, places and cards;
  - replies, reactions, star and forward (to and from groups);
  - delete for me, and delete for everyone within 24 h;
  - live updates and typing;
  - search and the starred list across groups.
- ☐ **Read state:** sent and delivered ticks, and "Read by …" per message for the sender (?A6).
- ☐ **Mentions:** @name in a group, with a push to the person mentioned even when the group is muted (?A7).
- ☐ **The Chats list:** groups sit in the one list with business and people chats, marked as groups, with paging, unread counts, mute and archive.
- ☐ **Group info screen:** members, admins, the invite link, media, mute, leave and report.
- ☐ **Push:** a new kind for group messages, with the preview setting honoured (§13).
- ☐ **Blocking inside a group:** what blocking someone means inside a group they share with you (?A8).
- ☐ **Businesses don't join people groups** (§9). A group can share a business's card and order from it (§10).

## B. Safety in groups (§8.2, §8.3, §8.4)

- ☐ **Limits:**
  - on creating groups;
  - on adding people who aren't contacts;
  - on invite-link joins per hour;
  - lower for accounts in their first week (§8.2).
- ☐ **Forward caps and marks** apply to groups (§8.2).
- ☐ **Report a group or a member,** with consent; the last 20 messages go to moderators with their files (§8.3).
- ☐ **Moderators** see group reports. A moderator can:
  - remove a group's invite link;
  - close a group;
  - act on members, as for people.

  Every access is logged (0069 point 3).
- ☐ **Scam checks:** automated checks look at messages from people who aren't contacts in new or small groups (?B1).
- ☐ **Lawful requests:** exports can cover a person's group messages within the scope asked (0069 point 11).
- ☐ **Storage watching** includes groups (0069 consequences).
- ☐ **Load test before 5b ships** (§13): many connections, group fan-out, storage growth.

## C. Ordering together (§10.3, §11 C, §17 Q8)

- ☐ **Start:** "Order together from {business}" in a group posts a shared basket card (§10.3).
- ☐ **The basket:** each member adds their own items from the business's shop through the customer door, and the card shows who wants what (§10.3). It includes:
  - sizes and colours as the shop has them;
  - live prices and stock;
  - delivery or pick-up, and the zone.
- ☐ **Closing the basket:** who may close it and place the order, and whether it can be locked before placing (?C1).
- ☐ **One person places the order** through the door, as their own order. The business sees one order from that person, with a note of whose lines are whose, and never sees the group (§10.3).
- ☐ **Paying:** either the placer pays all, or each person pays the business for their share (§11 C, §17 Q8) (?C2).
- ☐ **After placing:** the basket card follows the order live (state, paid parts, delivery). Each member sees their share.
- ☐ **Splitting with share cards:** if the placer paid everything, they can send each member a "Your share" card from the basket in one step (§11 A).
- ☐ **One-to-one too:** ordering together works between two people as well as in a group (?C3).

## D. Carried over from 5a, now for groups

- ☐ **Share cards** (business, product, order, delivery, your share) can be sent into a group (§10.1).
- ☐ **The live connection** carries group events. Business chats on You move onto it too, replacing polling.
- ☐ **Search's index** covers group messages, keyed per person, for messages they can see.
- ☐ **Deleting an account** removes the person from groups and keeps their messages as "Deleted account".
- ☐ **The privacy notice drafts** in `docs/legal/` gain a groups and ordering-together paragraph for the lawyer.

## Questions for the founder (the plan leaves these open)

Each has a recommendation; a "yes" to the recommendation is enough.

- **A2 Who edits a group's name, photo and description?** Recommended: admins only by default, with a switch to let everyone.
- **A3 "Who can add me to groups"?** Recommended: default "My contacts", where only people I've added can add me. Others send an invite that waits in Requests, like a stranger's first message.
- **A4 Joining by invite link:** join at once, or wait for an admin? Recommended: join at once by default; admins can switch on "Approve new members".
- **A5 After you leave or are removed,** do you keep what you saw? Recommended: yes, your copy up to that moment stays until you delete it; you see nothing new.
- **A6 Read state in groups.** Recommended: the sender can see "Read by" per message, but only members with read ticks on are listed.
- **A7 Mentions.** Recommended: @mentions in 5b, reaching you even when the group is muted (you can turn that off).
- **A8 Blocking someone who's in a group with you.** Recommended: you still see their group messages (hiding them breaks the conversation), but they can't message you one-to-one, can't add you to groups, and don't see your last seen or photo.
- **B1 Scam checks in groups.** Recommended: check messages from members who aren't your contacts, in groups younger than 7 days or with someone who joined by link in the last day; keep only the verdict, as for 5a.
- **C1 Closing the basket.** Recommended: the person who started it closes it and places the order; they can hand that to another member. A closed basket can be reopened until the order is placed.
- **C2 Each paying their share.** Storefront orders already take part payments: each payment counts towards the total, and a checkout page can be made for an amount (`docs/spec/fronts.md` §5.4; `engine/storefront` `CountPayment`, `pay_step_kobo`). Recommended:
  - each member gets their own checkout for their lines plus an equal part of delivery;
  - the order moves on when its parts reach the total (or the business's deposit);
  - the placer can pay what's left for anyone who hasn't paid, then send them a "Your share" card;
  - where a business takes no online payment, it's "the placer pays all, then share cards".
- **C3 Ordering together one-to-one.** Recommended: yes, the same basket works in a chat between two people.
