# 0071 — Saved objects: things a person or a business keeps and calls up by @name

Date: 2026-11-20 · Status: **Proposed** (written with S0, for the founder to accept; the plan was settled 19 November 2026) · Plan: [Saved objects](../spec/saved-objects.md) · Build plan: [S0](../spec/build-plan-saved-tools-payments.md#4-the-phases)

## Context

Taskiem's tags (`@` a business or a person, `#` a product, `/invoice` a document) name only what Taskiem itself keeps. People and businesses also keep their own things: Mum's account, the landlord's account, the warehouse, the bank details sent to every customer. They retype them in every chat.

Payments need a store of accounts to pay ([paying people](../spec/chat-pay-people.md): payees), and that store must be safe: an account number sealed, its name checked with the bank, a passkey to change it. The founder chose to build saved objects in full before payments (19 November 2026), so payees are built once, as saved objects.

The plan's questions were settled with the founder on 19 November 2026 ([its §15](../spec/saved-objects.md#15-settled-19-november-2026)). This record writes down the decisions the code now depends on.

## Decision

1. **Saved objects are one of Taskiem's core parts,** not a feature of one screen. An object has a name (`@mum`: 2 to 32 lowercase letters, digits and `-`, unique for its owner among the objects it keeps), a title, a kind and its details. They are used in every chat, in commands, in forms and, for a business, in workflows, in the phases of the build plan.
2. **Two owners, never mixed.**
   - **A person's own (Mine)**, kept on their side (You): private to them, read only in their own scope and never once a door names a business. No business sees the list. When used inside a business, the business keeps a copy of what was needed then, never the object.
   - **A business's**, shared with its people: everyone in it sees and uses them; owners, admins and a role given **`saved.manage`** add, change and remove them.
3. **Sensitive details are sealed** where they are kept: a person's under their own key (as their people chats are), a business's under its subject keys (as its customers' phones are). Lists show them masked (••6789). Showing one in full, and changing a bank account, need a **fresh passkey for that object and that operation**. Each is recorded: in the person's own log for theirs, in the business's audit chain for its own, with sensitive values masked.
4. **The AI never sees a sensitive value.** It is given an object's name, title and kind only; the server fills in the values.
5. **Payees are saved objects of the kind bank account.** One store: the payments plan's payees are a business's bank account objects; its safety rules (the sealed number, the bank's checked name, a passkey to change it, the changed-account and first-payment warnings) are this store's rules. Staff's and riders' accounts stay on their own records. A payment to an owner's own object keeps a sealed copy on the payment, since the business cannot read the person's object; only an **owner** may pay their own object from the business, booked as personal (owner's drawings).
6. **A business's bank account is checked with its bank** through the business's payments connection: its Taskiem wallet first, else a connection of its own that can name an account. The step to connect one (open a Taskiem wallet, or connect Paystack, Flutterwave, Moniepoint or Anchor) is offered to owners where a business has none. Without one, an account is kept "Name not checked yet" and checked once one is connected. An account the bank says it does not have is not saved.
7. **Removing an object archives it.** Its name is free again; what was done with it stays explained. A person's objects go with their account; copies already shared or used stay where they went, as records.
8. **Limits:** 500 objects for a person; 2,000 for a business on the lowest plan, more on higher plans.
9. **An object never copies what Taskiem already keeps** (a product, a customer, a linked business, an order, a student), and is not a data table: tables are many rows of one shape kept in Xenibase; objects are few named things of many shapes, kept in Taskiem's own database where they can be sealed and checked on every use.

## What S0 built

- `saved_objects` (both owners, row-level security for each) and the person's own log `saved_object_events` (migration 00491).
- The kind **bank account**, its number sealed and masked; change and reveal by passkey; the audit and the log.
- The bank list (`GET /v1/banks`, the connection's own or the banks by name), the name check (`POST /v1/name-check`), and checking names not checked yet (`POST /v1/saved/check`).
- The step to connect a provider (`GET /v1/saved/provider`), which leads to the existing "Receive money with Taskiem" for the wallet.
- `@name` resolved on the server (`GET /v1/saved/resolve`).
- The permission `saved.manage`, held by owners and admins.

## Open

- **A person's own accounts are not checked with a bank in S0.** A person has no payments connection of their own, and using a business's connection for them would send the person's account to that business's provider. Whether Taskiem checks them with its own wallet client is to be decided before P1.
- **A passkey on You.** A person signed in only with their phone on You cannot yet add a passkey there, so they cannot reveal or change their own saved account until they do (in a business they work in, they can). Adding passkeys to You is to be planned with S1.
- **Who may see a business's account number in full:** `saved.manage` in S0; those who may pay (`money.pay`) from P1.

## Alternatives considered

- **Payees as their own table, apart from objects.** Two lists of accounts, two sets of safety rules, and "send 20k to @mum" would need both. Rejected by the founder (§15).
- **Objects as rows of a data table.** Tables live in Xenibase, outside Taskiem's own database, with no sealing per field or check on every use. Rejected (§11).
- **A code by text to change an account.** Weaker than a passkey against a stolen phone number; payments use a PIN only as the fallback, with a lower limit ([paying people](../spec/chat-pay-people.md#settled-19-november-2026), 3). S0 takes a passkey only.
