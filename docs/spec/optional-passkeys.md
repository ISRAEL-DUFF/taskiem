# Passkeys for owners and administrators: optional, with two settings

Status: **built** (§12 has where it differs from this plan). It amends
[decision 0026](../decisions/0026-bound-step-up-and-cookie-sessions.md) ("owners are
held to passkeys by default") and [decision 0032](../decisions/0032-deployment-target.md)
("admins held to passkeys (the default)"), and [governance](../governance.md#passkeys).

## 1. The problem

**Who is held to passkeys today.** On a deployment with a public address,
`TASKIEM_REQUIRE_ADMIN_PASSKEYS` is on (`cmd/taskiem/serve.go`), and every administrator
is held to passkeys. An administrator is anyone holding `member.manage`,
`role.manage`, `secret.manage`, `policy.manage`, `git.manage`, `connector.manage`,
`pii.reveal` or `pii.erase`, and every owner is one.

**What that means for them:**
- **Signed in with a password or their chat,** they can only add a passkey. The web app
  shows nothing else (`EnrolOnly`, `EnrolPasskey`).
- **Once they have a passkey,** their password and chat no longer sign them in, in any
  business.

**Why it does not fit Taskiem's owners:**
- Many use a cheap Android phone, a shared phone, or a cyber café's computer, where
  passkeys are confusing or unavailable.
- An owner who signed up on WhatsApp and opens the web app for the first time meets a
  wall before seeing anything, which undoes chat sign-in
  ([chat-sign-in.md](chat-sign-in.md)).

## 2. What we will build

1. **Off by default.** No one is made to add a passkey. The Account page and a banner
   that can be dismissed suggest one.
2. **A personal setting: "Only sign in with my passkey" (opt-in).**
   - When it is on, the person's password and chat sign-in are refused, in every
     business, as today's rule does once someone has a passkey.
   - Turning it off needs the passkey, so whoever gets hold of the account by another
     way cannot turn it off.
3. **A business setting: "Require passkeys for administrators" (opt-in).**
   - When it is on, administrators of that business are held to passkeys in it, as
     today's rule does.
   - Only owners change it.
4. **Approvals and key changes keep their step-ups,** whatever the settings: an
   approval rule that asks for a passkey, an authenticator code or the chat PIN still
   does (`step_up`, [governance](../governance.md)).
5. **The deployment switch stays,** but off by default.
   `TASKIEM_REQUIRE_ADMIN_PASSKEYS=true` holds every business's administrators to
   passkeys, whatever their settings. It is for a deployment that wants that: a bank
   running its own Taskiem, say.
6. **Taskiem's own operators** (the operator console, decision 0027) are not affected,
   and keep signing in with passkeys.

## 3. The rule, as built

A session's person is **held to passkeys in a business** when any of these holds:
- the deployment switch is on, and they are an administrator there;
- the business requires passkeys for administrators, and they are an administrator
  there;
- they turned on "Only sign in with my passkey" (in every business).

Someone held to passkeys:
- **without a passkey,** and signed in with a password, their chat, or single sign-on
  (as today for single sign-on), can only add one (`EnrolOnly`, as today);
- **with a passkey,** is refused a password or chat sign-in to that business: "sign in
  with your passkey".

| Where | Today | After |
| --- | --- | --- |
| Login with a password (`login`) | Refused for an administrator with a passkey, in any business | Refused only for someone held to passkeys in the business asked for; with no business asked for, the first business the person is not held in, else refused |
| Chat sign-in (`chatSigninFinish`, `chatSigninSend`) | As the password | As the password |
| Each request (`EnrolOnly`, `api/auth.go`) | Deployment switch, and an administrator in this business | The rule above, for the session's business |
| Single sign-on (`EnrolOnly` after `sso`) | An administrator with no passkey enrols one | The rule above |
| Passkey sign-in | Always allowed | Always allowed |

**Today's rule says it applies to the person, not to one business.** After this,
the personal setting applies to the person; the business setting applies to that
business only. Someone who is an administrator in two businesses, one of which
requires passkeys, signs in with a password to the other.

A session moving between businesses is checked again on its next request, since
`EnrolOnly` is worked out per request for the session's business. A password
session that reaches a business requiring passkeys can do nothing there but add one,
or, with a passkey already, is told to sign in with it.

## 4. The personal setting

- **Where:** Account > **Passkeys**, a switch "Only sign in with my passkey". Off by
  default.
- **Turning it on:**
  - needs at least one passkey ("Add a passkey first");
  - is confirmed with that passkey (step-up `account.reauth`, target
    `passkey_only.on`).
- **Turning it off:** confirmed with a passkey (`passkey_only.off`). An authenticator
  code or the password is not enough: that is the point of the setting.
- **Losing every passkey:**
  - Removing the last passkey turns the setting off. Removing a passkey already needs
    a passkey or authenticator code, so the setting cannot be dodged this way.
  - A reset by an owner (`DELETE /v1/members/{id}/passkeys`) or an operator
    (`taskiem passkeys reset`) turns it off, as recovery.
- **Each change** is audited (`auth.passkey_only`), and told to the person's chat and
  confirmed email, like a chat sign-in (chat-sign-in.md §7).
- **API:**
  - `PUT /v1/me/passkey-only` with `{on, passkey}`, where `passkey` is the step-up
    assertion;
  - `GET /v1/me` shows `passkey_only`.

## 5. The business setting

- **Where:** Settings > **Security**, a switch "Require passkeys for administrators".
  Off by default. The card lists who the rule covers: owners and the roles with
  administrative permissions.
- **Who changes it:** owners only (`role.manage`), confirmed with the owner's own
  passkey or authenticator code, as a factor change is (target
  `business.require_passkeys`).
- **Turning it on needs a passkey of your own.** Otherwise the owner's very next
  request would be held to adding one.
- **What turning it on does for the others:** administrators without a passkey are
  asked to add one when they next do anything on the web. The card says how many are
  without one, so the owner can tell them first.
- **Each change** is audited in the business (`settings.require_passkeys`).
- **API:**
  - `GET` and `PUT /v1/settings/security`, with `{require_admin_passkeys, proof}`;
  - `GET /v1/me` shows the business's setting, for the banner.

## 6. Suggesting a passkey

- **The banner:** "Add a passkey: sign in with your fingerprint or face, and keep
  your account safe even if your password is found out." It has **Add a passkey**
  and **Not now**.
- **Who sees it:** an administrator with no passkey, on a device that can make one
  (`passkeysSupported()`).
- **Not now** hides it for 30 days on that device (browser storage, per viewer).
- **Not shown** to someone held to passkeys, who sees today's enrol screen instead.

## 7. Storage

- **Migration 00315:**
  - `users.passkey_only boolean NOT NULL DEFAULT false`;
  - `tenants.require_admin_passkeys boolean NOT NULL DEFAULT false`;
  - read by sign-in through a definer: `taskiem_auth_passkey_rule(p_user, p_tenant)`
    returns `passkey_only`, `require_admin_passkeys` and `has_passkey`, so the
    password and chat paths ask once;
  - the definer and the dispatch columns added to their allow-lists;
  - a full Down section.
- **Existing deployments:** nothing is migrated.
  - Today, every administrator on a deployment with the switch on is held. After this,
    none is, unless the operator keeps `TASKIEM_REQUIRE_ADMIN_PASSKEYS=true`.
  - Passkeys people already added keep working, and those people may now sign in with
    their password again.
  - The release notes say this plainly, with how to keep today's rule.

## 8. Security

- **What changes:** an owner's account is guarded by their password, or their chat,
  until they choose a passkey. This is the risk the founder accepts for the owners
  Taskiem serves.
- **What still protects them:**
  - every chat sign-in is told to the chat and the email;
  - passwords are rate limited and checked as before;
  - approvals that move money keep their step-up (passkey, authenticator code or chat
    PIN);
  - adding factors, changing keys and revealing personal data keep their
    re-authentication;
  - administrators who want more turn on the personal setting, and businesses with
    staff turn on the business setting.
- **Two who want different things:** an attacker with the password cannot turn either
  setting off. Turning off the personal setting needs the passkey. Turning off the
  business setting needs an owner's passkey or authenticator code, and an owner held
  by it has one.
- **The threat model and governance pages** are updated: the "admins held to passkeys"
  lines become the settings.

## 9. Words and pages

- **Web:**
  - `Account.tsx`: the switch under Passkeys, and the banner (`PasskeyNudge`).
  - `Settings` gets **Security** with the business switch. It is added to the
    accessibility check and to the plain-words test (owner pages).
  - The enrol screen (`EnrolPasskey`) explains why: "This business requires a passkey
    for administrators", or "You chose to sign in only with your passkey".
- **Chat:** a passkey cannot be made in chat.
  - `sign in` for someone held to passkeys answers as now (`wa.signin.passkey`).
  - `passkey` in chat answers with a link to Account > Passkeys.

## 10. Slices and tests

| Slice | What | Tests |
| --- | --- | --- |
| P1 | The rule (§3) with the deployment switch off by default; the login, chat sign-in and per-request checks moved onto it; migration 00315 | An owner with a password and no passkey works normally; with a passkey, still signs in with a password; the deployment switch on behaves as today (the existing passkey tests run with it on) |
| P2 | The personal setting (§4): API, step-up, reset turns it off, notices | On needs a passkey; then password and chat are refused and passkey works; off needs a passkey (an authenticator code or password refused); removing the last passkey and a reset turn it off |
| P3 | The business setting (§5) | Owner turns it on (needs own passkey and proof); another administrator without a passkey is enrol-only in that business and free in another; a non-owner cannot change it; off restores |
| P4 | The banner and pages (§6, §9); docs: governance, decisions 0026 and 0032 amended, threat model, release note | Web unit tests; accessibility; plain words |

## 11. To confirm

1. **Who changes the business setting:** owners only (recommended), or anyone with
   `policy.manage`.
2. **The banner's "Not now":** 30 days (recommended), or until the next sign-in.
3. **Existing deployments:** the switch turns off by default, so administrators already
   held are released (recommended, as asked). The alternative is to keep it on where
   it is on today and let operators turn it off.

## 12. As built

The founder took the recommendations of §11:
- owners only change the business setting;
- "Not now" hides the banner for 30 days on that device;
- the deployment switch is off by default.

**Where it lives:**
- migration 00315;
- `api/passkey_rule.go`;
- the checks in `api/auth.go` (`login`, and each request) and `api/chat_signin.go`;
- `web/src/pages/PasskeyNudge.tsx`;
- `Account.tsx` (`PasskeyOnly`), `Settings.tsx` (`Security`) and `App.tsx`
  (`EnrolPasskey`, with the reason);
- the tests in `api/passkey_rule_test.go`.

**Where it differs from the plan:**
- **Security is a card on the Settings page,** not a page of its own (§5, §9). The
  Settings page is the business's settings, one card each.
- **A password or chat session already open** when its person becomes held, with a
  passkey, is refused with 401 `passkey_required`, not shown a screen (§3). The web
  app signs out on 401, and the login page says to sign in with the passkey. Without
  a passkey, the session can only add one, as before.
- **The personal setting is told** to the chat and email (`account.passkey_only.on`
  and `.off`, in five languages). The business setting is audited only.
- **Turning on the personal setting** does not end the person's other sessions: each
  of their requests checks the rule, so a password or chat session is refused at its
  next request.
- **Cost:** each request of a session that did not sign in with a passkey reads the
  rule once (`taskiem_auth_passkey_rule`, one indexed row each from `users`,
  `tenants` and `webauthn_credentials`).
- **The tests of today's rule** (`TestAdminsAreHeldToPasskeys` and the single sign-on
  and chat sign-in ones) run with the deployment switch on and pass unchanged.
  - `TestPasskeysOptional` covers the switch off and the personal setting.
  - `TestBusinessRequiresPasskeys` covers the business setting.
