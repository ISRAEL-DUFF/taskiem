# The transaction PIN: four digits, at once after a code

The founder's decisions of 25 November 2026, numbers 1 and 2. They change
the PIN of [paying people](chat-pay-people.md) (Settled row 3) and of
[approvals on WhatsApp](../whatsapp.md#approval-pin).

**The PIN is for paying and approving only.** Later the same day the
founder decided it is no longer a second proof when signing in on a new
device: it is set with a code to the same phone, so it proves no more than
that code. The proofs are a passkey, a link to the email, or the password
([taskiem-for-everyone.md §5.4](taskiem-for-everyone.md), "As built:
signing in without the PIN").

## What was decided

1. **Four digits, one PIN.** The one PIN a person has (`whatsapp_pins`)
   approves on WhatsApp and pays within its limits. It was six digits; it is
   now **four**, everywhere: the server, the web, the app and WhatsApp's PIN
   form. Kept as they were: the weak-PIN rule (one digit repeated, or a run
   up or down such as 1234 or 9876), five wrong tries locking it for 15
   minutes, and its money limits (₦100,000 a payment, ₦200,000 a day; never
   a changed account, anything waiting for the owner's OK, or a list).
2. **It pays at once, after a code.** Setting or changing the PIN sends a
   one-time code to the person's verified phone; once the code is typed the
   PIN is saved and **pays immediately**. A passkey, or an authenticator
   code, still works in place of the code. The 24-hour hold is gone.
   Removing the PIN needs no code. Wherever a screen told the person they
   needed a passkey to set a PIN, it now offers the code.

## As built

**On the server** (`api/pin_me.go`, migration `00730_pin_four_digits.sql`):

- `POST /v1/me/pin/code` (`{via?}`: `whatsapp` or `sms`) sends a six-digit
  code to the account's own phone (`user_phones`), by WhatsApp from
  Taskiem's number, else by SMS. It is a phone code of its own kind
  (`phone_codes.purpose = 'pin'`): a sign-in code, a code to delete the
  account or to change the number never sets a PIN, and a PIN code does
  nothing else. It lasts 10 minutes and works once; the usual limits apply
  (five codes an hour to a number, five wrong codes lock the number for an
  hour). An account with no phone gets 409 `no_phone`.
- `PUT /v1/me/pin {pin, code | passkey | totp}`: four digits, not weak; then
  one of the code, a passkey asked for `account.reauth` / `pin.set`, or an
  authenticator code (in a business). It is saved paying at once
  (`pays_from` = now) and audited `pin.set` with `via` (`code`, `passkey`
  or `totp`). With none of them: 403 `proof_needed`, `reauth` listing the
  ways there are. A wrong or used code: 401 `code_wrong`. A session made by
  a code on a new device, of someone with a passkey or an authenticator,
  still proves itself first (`second_proof_required`).
- `GET /v1/me/pin` says `set`, `set_at`, `locked_until`, `digits` (4),
  `ways` (what can confirm a change now: `code`, `passkey`, `totp`),
  `code_to` (the phone, masked) and `renew` (below). The old fields
  `pays_from`, `change_without` and `change_without_until` are gone.
- Paying by PIN no longer refuses `pin_new`. The sign-in moments ago, the
  "first PIN" and the remembered removal no longer change what setting a
  PIN takes.
- `PUT /v1/me/whatsapp/pin` (the older route, behind a passkey or an
  authenticator) takes four digits and pays at once, as before.

**The six-digit PINs.** No pad takes six digits any more, so migration
00730 removes every PIN set before it. Each removal is remembered
(`pin_removals.why = 'four_digits'`, nothing of the PIN), and the person's
PIN settings say so (`renew`): *PINs are now 4 digits. Your old PIN was
removed. Set a new one.* Until they do, approvals that would take the PIN
send the web link, and payments take the passkey. The Down migration does
not bring the old PINs back.

**WhatsApp.** The PIN form (`taskiem_pin`) asks for four digits, labelled
*Your PIN*, *The 4-digit PIN you set in Taskiem*
(`docs/whatsapp-flows/taskiem_pin.json`: **the operator publishes the new
version of the Flow on Meta**, or the form keeps asking for six). The chat
PIN of a person whose chat is their only credential (send *pin*) is set in
that form when none is set, and pays at once: the form reached the
chat's own number, as a code would. Changing it is done in the app or on
the web, with a code to that number. Its words (`chatpin.web`,
`chatpin.set_already`, `chatpin.form`, `signup.pin_hint`) say so in
English, with drafts in Hausa, Igbo, Pidgin and Yoruba to be reviewed.

**The web** (`web/src/pages/Account.tsx`, `web/src/lib/pin.ts`): Account ›
Your PIN takes the PIN twice (four digits, weak and mismatched ones said
before anything is sent), then **Send me a code**; the code's box says
where it went; **Set PIN** saves it. With a passkey in the browser the
button is **Set PIN with my passkey**, and **Use a code to my phone
instead** is offered; with an authenticator only, its code. With no phone
and no passkey: *To set a PIN, add your phone number above*. The **Pay
sheet** (`PaySheet.tsx`) now takes the PIN as well: when the server offers
it (no passkey in this browser, or the passkey put away), a PIN field says
what it may pay now; a wrong PIN is typed again; a refusal the PIN cannot
get past (locked, over its limit, a changed account) is said, and paying
again asks for the passkey.

**The app** (`mobile/lib/screens/you_pin.dart`): the sign-up step **Set
your PIN** and Settings › PIN share one form: four boxes twice, then the
way to confirm: **Send me a code** (then six boxes for it, *Sent to
+234••••••0000 on WhatsApp*, **Send the code again**), or **Confirm with
your passkey and save** where this phone has the passkey (with **Use a
code to my phone instead**), or an authenticator code. A phone with no
passkey now sets and changes the PIN with the code; before, it could not.
With no phone and no passkey: *Add your phone first*. The PIN page shows
`••••`, when it was set, *Up to ₦100,000 a payment*, and a lock; the old
PIN's removal is said. The Pay sheet's pad has four boxes.

**Tests.** Go with the database: `TestMePin` (no way to prove, a code
asked for and typed, a wrong one, one from sign-in refused, used once, a
passkey, the audit's `via`, removal, `renew`), `TestPayByPin` and its
WhatsApp form (four digits, a PIN set a minute ago offered at once, a
locked one), the chat PIN, approvals by PIN and the second proof with
four digits; the migration up and down. Web: vitest (`pin.test.ts`),
Playwright `pin.spec.ts` (a code by the fake WhatsApp, at 390 and 360 px,
axe in both themes) and the Pay sheet's PIN in `payments.spec.ts`. App:
`pin_test.dart`, `pay_test.dart`, `settings_test.dart`, the pictures
`pin-step` and `pin-settings` drawn again, large text and targets.

## Safety

The PIN now stands on the phone number: whoever can read a code sent to it
can set a PIN and pay up to ₦100,000 at once (₦200,000 a day). That was
the founder's choice for people with no passkey. What still holds: a code
is good for the PIN only, once, for 10 minutes, and five wrong codes lock
the number for an hour; a session started on a new device by a code, for
someone with a passkey or an authenticator, proves itself before it can
ask for one; a changed account, the owner's OK and lists stay passkey
only; every PIN set is audited with how it was confirmed.
