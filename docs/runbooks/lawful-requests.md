# Lawful requests for people's data

How Taskiem answers a request from the police, a court, a regulator or another authority for a person's data, above all their chats with other people ([decision 0069](../decisions/0069-people-chatting-with-people.md) point 11; [people chatting](../spec/people-chatting.md) §8.5). **Draft for counsel:** the process and its words are agreed with a lawyer before stage 5a ([needs people](../needs-people.md), PC3). Until then, every request goes to the founders and counsel before step 3.

People chats are kept by Taskiem, sealed on its servers and readable by them; they are not end-to-end encrypted, so Taskiem can be required to give their words. That is why this process is narrow, recorded, and counted in public.

## The rules

1. **Only proper legal process.** A written order or request under a law that lets the authority ask for it (for example the Cybercrimes Act 2015 or the NDPA 2023, as counsel advises), naming who asks, their reference and the account. Not a phone call, a WhatsApp message or a letter without a legal basis. Emergencies (a life at risk) go to the founders and counsel at once; they decide.
2. **The least that is asked.** One account, the dates asked for, only the chats with one other person when the request names one, photos and files only when it asks for them. If a request is wider than its legal basis or than the case needs, ask the authority to narrow it, or refuse.
3. **Two people.** One operator records the request; a second, never the same, approves it before anything is exported. The console enforces both.
4. **Tell the person where the law allows,** as soon as it allows: what was asked, by whom, and what was given. Record when, or why the law forbids it (a court's non-disclosure order, with its reference and end date).
5. **Everything is recorded and counted.** Each step is in the platform audit chain with the operator's name. The number of requests received, answered and refused each half-year is published at `/transparency` once that half-year has ended (so a new request never moves the count) (and `GET /v1/transparency/lawful-requests`), linked from the privacy pages. Nothing else about a request is published.

## Steps in the operator console

**Lawful requests** in the console. Every step asks for your passkey.

1. **Check it.** Is it from who it says (call the authority back on a number you find yourself, not one in the request)? Does it name a legal basis, a reference, one account and dates? Send it to counsel.
2. **Record it** (Record a request): the authority, their reference, the law and order, the person's account id (find it from the number the request gives with the founders' help; the console does not search chats), the other person's account id if the request names one, the first and last day asked for (Lagos days, the last included), whether photos and files are asked for, anything else about the scope, and the day it was received. Recording counts it in the published figures.
3. **Approve** (a second operator): check the scope is exactly what the order asks and no wider. Or **Refuse**, with why (it is counted as refused).
4. **Export**: the person's own copies of their people chats in the scope, opened, as a JSON file (each chat with the other person's id and name, each message with who sent it, when, its words, card, place, file name and, if asked, the file). Each export is recorded with the number of messages given, and marks the request answered. Nothing outside the scope is in the file: not other chats, not other dates, not the person's business chats, contacts or settings. If the account was deleted, there is nothing to export: answer that the data no longer exists.
5. **Send** the file only to the authority, by the channel counsel agrees (encrypted, never plain email), and delete your local copy once it is sent.
6. **Telling the person:** record the day they were told, or why not. When a non-disclosure order ends, tell them then and record it.

## What is not done

- No access outside this process: no engineer reads a person's chats for an authority, and no one exports from the database by hand.
- No standing access, bulk requests or requests about everyone who used a word or a place. Refuse them and tell counsel.
- No more than the order asks, even if the authority asks informally for "anything else useful".

## Publishing the count

The count publishes itself from the recorded requests, by the half-year (Lagos time) they were received in: received, answered, refused; requests still being looked at are counted as received only. Record every request, including those refused at step 1, so the count is honest.
