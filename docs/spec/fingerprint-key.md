# Paying by fingerprint: a key on this phone

Status: built (25 November 2026). The founder's decision 3 of that day: the
fingerprint is its own way to approve a payment, separate from the passkey
and the PIN.

## What it is

A person turns it on in the phone app, in Settings, or from the Pay sheet.
The app makes a key pair in the phone's secure hardware (the Android
Keystore, StrongBox where the phone has it; the iPhone's Secure Enclave),
which can be used only after the phone's own fingerprint or face check. The
private key never leaves the phone. Its public key is sent to Taskiem and
kept for that person and that phone. When a fingerprint or face is added to
or removed from the phone, the phone throws the key away, and the person
turns it on again.

To pay with it, Taskiem gives the phone words that name one payment (its id,
its amount and a one-time number), good once for two minutes. The phone asks
for the fingerprint, signs the words with the key, and Taskiem checks the
signature with the public key it kept.

The web has no fingerprint: it pays with a passkey or the PIN. It lists the
phones the fingerprint is on for, and removes them.

## What it pays

| | PIN | Fingerprint | Passkey |
| --- | --- | --- | --- |
| One payment | ₦100,000 | ₦500,000 | the role's limit |
| A day, every business together | ₦200,000 | ₦1,000,000 | the role's limit |

The limits are fixed in the server, beside the PIN's
(`api/payments_fingerprint.go`), as the PIN's are: they are the person's,
across every business they pay for, not a business's setting. The role's
own limits apply too; over them the owner's OK is asked, and the
fingerprint never leads to that.

Like the PIN, it never pays:

- a payment to an account changed since it was last paid;
- anything that would wait for the owner's OK;
- a row of a list (a batch).

Those take the passkey.

## Turning it on

Turning it on needs what setting the PIN needs: a passkey or an
authenticator code, the PIN (a wrong PIN counts against the PIN's five
tries), or a one-time code sent to the person's verified phone number (the
phone codes' own purpose, `pay_key`). Someone signed in by a code on a new
phone proves that phone first, as for every other way in. Turning it off
needs nothing.

## As built

Server:

- Migration `00740_pay_fingerprint.sql`: `pay_keys` (person, phone, phone's
  name and platform, the ECDSA P-256 public key, when turned on and last
  used; one key a phone) and `pay_key_challenges` (the hash of the words,
  the key, the payment, two minutes), both reached only through their
  functions; `payments.confirmed_with` may be `fingerprint`, and
  `taskiem_fingerprint_paid_today` counts the fingerprint's own day; the
  phone codes' purposes gain `pay_key` (added to whatever list is there).
- `GET /v1/me/fingerprint`: the phones, the limits, what is left today,
  and the ways to prove turning it on. `POST /v1/me/fingerprint` turns it on
  for a phone (`public_key`, `device`, and `passkey`, `totp`, `pin` or
  `code`); `POST /v1/me/fingerprint/code` sends the code;
  `DELETE /v1/me/fingerprint/{id}` removes a phone (`api/fingerprint_me.go`).
- `POST /v1/payments/{id}/fingerprint` gives the words to sign (refused
  `fingerprint_limit`, `fingerprint_passkey_only` or `no_fingerprint`);
  `POST /v1/payments/{id}/fingerprint/confirm` checks the signature and
  sends the payment as the passkey's confirm does, the payer's own OK given
  with step-up `fingerprint` (refused `fingerprint_expired` for words used,
  old, or for another payment or key; `fingerprint_wrong` for a signature
  that does not check) (`api/payments_fingerprint.go`). These are routes of
  their own: the passkey's and the PIN's confirm are unchanged.
- Audited: `fingerprint.add` (how it was proved), `fingerprint.remove`,
  `fingerprint.pin.fail`, `payment.fingerprint.fail`, and `payment.confirm`
  with `step_up: fingerprint` and the key.

The phone app:

- `lib/app/pay_key.dart`: the key, through the `biometric_signature`
  plugin (MIT; ECDSA P-256, signatures over SHA-256; its own R8 rules ship
  with it; the app's `MainActivity` is already a `FlutterFragmentActivity`).
  The key is invalidated when the phone's fingerprints or faces change, and
  cannot be used with the phone's screen-lock PIN instead.
- Settings › Fingerprint (`lib/screens/you_fingerprint.dart`, from You's
  Settings and the business's Settings): this phone on or off, what it
  pays and what is left today, and every phone it is on for, each with
  Remove.
- The Pay sheet: with a key on this phone, Pay uses the fingerprint for a
  payment within ₦500,000 that is not to a changed account or a row of a
  list, with "Use my passkey or PIN instead"; otherwise "Pay with my
  fingerprint". When the server refuses, the sheet says why and goes back to
  the passkey or PIN. With no key, and a phone with a fingerprint or face
  set up, "Pay by fingerprint from now on" turns it on there and then.

The web: Account › Fingerprint on your phones lists the phones and removes
them (`web/src/pages/PayFingerprint.tsx`).

## Not done

- Android's key attestation (proof that the key was made in the phone's
  hardware) is not asked for: the plugin can return the certificate chain,
  but checking it on the server is a step of its own.
- WhatsApp has no fingerprint: its form stays the PIN's.
