# Signing in on the web for owners who started in chat

Status: **built** (§11 has where it differs from this plan). It builds on
[chat-way-in.md](chat-way-in.md) §10.4 (decision 0058).

## 1. The problem

A business that signed up on WhatsApp, Telegram or Slack (a *chat-born* owner) has
an account with no password and a hidden sign-in name. That name is a placeholder
such as `owner+3fa2…@phone.taskiem.invalid` (`ownerPlaceholderEmail`; `@telegram…`
and `@slack…` for the others).

Today the way onto the web is:
1. Send `email you@example.com` in chat, and open the confirmation link. The real
   address replaces the placeholder (`waEmail`).
2. Send `sign in`. A link, good for one hour and once, lets the owner choose a
   password (`waSignInLink`).
3. Sign in at `/login` with that email and password.

**It breaks when step 1 is skipped:**
- `sign in` sends the password link anyway, and the reset page sets the password
  without signing in (`ResetPassword`). It only shows **Sign in**.
- The login form then asks for an email the owner has never seen, so the password
  they just chose cannot be used.
- Nothing tells them to add an email first.

**The way onto the web also does not fit these owners.** Most use WhatsApp or
Telegram all day and neither email nor passwords.

## 2. What we will build

- **Part A (the small fix):** `sign in` asks for the email first when there is none,
  as **Pay** already does. The path above then works end to end.
- **Part B (chat sign-in):** signing in with the chat itself. The chat proves the
  same thing sign-up did: that the person controls it.
  - **On the web:** `/login` offers **Sign in with WhatsApp or Telegram**. The owner
    types their phone number, and a six-digit code comes to that chat.
  - **From chat:** `sign in` sends a link that opens the web app already signed in.
  - **Choosing a password** moves to `password` in chat (and Account on the web), and
    keeps Part A's email requirement.

Nothing changes for people who sign in with an email and password, a passkey or
single sign-on.

## 3. Part A: `sign in` asks for the email first

- **No real email** (the sign-in name is a placeholder, `emailPlaceholder`):
  - `sign in` answers "First add your email: send *email you@example.com*. Open the
    link we send, then send *sign in* again." (`door.signin_email`)
  - No password link is sent.
- **An email waiting to be confirmed:** the same answer, naming the address it went
  to (`door.signin_confirm`).
- **A confirmed email:** the password link, as today. The reply now also says what to
  type at the login page: "…then sign in with *ada@example.com* and that password."
  (`wa.signin.link` gains `{email}`.)
- **The password page:** once the password is set, it says "Sign in with your email
  and this password." It does not name the address, since the page does not know
  whose link it was.
- **Unchanged:** organisations that sign in with single sign-on (`wa.signin.sso`), the
  rate limit (three an hour per person), the audit (`auth.signin_link`), and the
  deployment with no public URL (`wa.signin.unavailable`).
- **After Part B:** `sign in` sends the sign-in link of §5, and this behaviour moves
  to `password` (§6).

## 4. Part B: a code to the chat, on the web

### 4.1 The flow

1. `/login` has a second way in: **Sign in with WhatsApp or Telegram**. The person
   types their phone number, in any form `whatsapp.Normalise` reads, with the
   deployment's default country.
2. `POST /v1/auth/chat/start` with `{phone}`. The answer is always the same: "If this
   number is on a Taskiem account, we have sent a code to it on WhatsApp or
   Telegram." The answer does not depend on whether there is an account (§7).
3. If the number is on an account (§4.2), a six-digit code goes to that person's
   chat (§4.3).
4. The page asks for the code. `POST /v1/auth/chat/verify` with `{phone, code}` starts
   a session (`startSession`, method `chat`).
   - After that, it behaves like a password login: the business picker when there
     are several, and `tenant_id` when asked.
   - A wrong or expired code says "That code is not right, or it has expired" and
     nothing else.

### 4.2 Finding the person by number

- **Normalise the number,** then look in order:
  1. `whatsapp_bindings.number`: someone whose WhatsApp is bound to the account,
     whether chat-born or linked from the web.
  2. A chat-born account whose sign-in name is that number's placeholder
     (`users.email = ownerPlaceholderEmail(number)`). This is how a **Telegram**
     owner, who shared their number at sign-up, is found.
- **One number, one person:** `whatsapp_bindings.number` is unique, and a placeholder
  is made from one number. If both find someone and they are different people, the
  WhatsApp binding wins and the case is logged. Sign-up already refuses this, so it
  should never happen.
- **Accounts that sign in another way** are not sent a code:
  - accounts that are not active;
  - accounts that must sign in with single sign-on;
  - administrators with a passkey where `RequireAdminPasskeys` is on, as for a
    password.

  Only the start answer is given; the person is told nothing different.

### 4.3 Delivering the code

- **WhatsApp, if bound:** the `taskiem_otp` authentication template, approved on the
  shared number and outside the 24-hour window. It has a **Copy code** button.
  - Its stated purpose (`TplOTP.Purpose`) widens to "binding a number, and signing in
    on the web".
  - It is sent from Taskiem's shared number even when the business has its own: the
    code is for the person's Taskiem account, not for a business.
- **Telegram, otherwise, if the person has a binding:** a bot message, "Your Taskiem
  sign-in code is 123456. It expires in 10 minutes. If you did not ask for it, ignore
  this message." Telegram has no window.
- **Neither:** nothing is sent. This covers a Telegram owner found by placeholder whose
  binding has since gone, or a Slack-only person. The answer is the same as always.
- **Each send** is recorded in the chat history like any other Taskiem message, with
  the code masked (`••••••`).

### 4.4 The code

- Six random digits.
- **Stored only as a hash:** a SHA-256 of `user:number:code`, so a code is good only
  for the number it was sent to.
- **Lifetime:** valid for 10 minutes and used once.
- **Limits:**
  - five wrong tries lock it;
  - at most five codes an hour per person;
  - a new code replaces the old.
- **Storage:** a table of its own, `chat_signin_codes`, one row per person. It is
  kept apart from `whatsapp_otps`, so binding a number from the web and signing in
  never clobber each other's codes.

## 5. Part B: `sign in` from chat sends a link that signs in

- **The link:** `sign in` (on WhatsApp, Telegram or Slack) answers with
  `https://<public URL>/signin#token=…`. It is one-time, good for 10 minutes, and for
  this person only.
  - It goes only to the chat that asked, which is already bound to them: the same
    proof as `waSignInLink` today.
  - It works for chat-born owners and for everyone else with a bound chat (§9 has the
    choice).
- **The token** is 24 random bytes, kept only as its SHA-256 (`chat_signin_links`:
  `token_hash`, `user_id`, `channel`, `expires_at`, `used_at`).
- **The page:**
  - The page reads the token from the fragment, which is never sent in a request or
    logged, and clears it from the address bar, as `ResetPassword` does.
  - It shows **Sign in to Taskiem as {first name}** with one button.
  - **Only the button's POST** to `/v1/auth/chat/link` uses the token and starts the
    session. Chat apps and browsers that fetch links ahead of time cannot use it up
    or sign anyone in.
- **Who is refused:** the same accounts as in §4.2.
  - Single sign-on: the reply is today's `wa.signin.sso`.
  - An administrator with a passkey: "Administrators sign in with their passkey"
    and a link to `/login`.
- **Unchanged:** the rate limit (three an hour per person), the audit (`auth.signin_link`)
  and the deployment with no public URL.

## 6. Choosing a password, after Part B

- **`password` in chat** is Part A's behaviour:
  - the email first;
  - then the link to choose a password, with the email to sign in with.
- **Account > Password on the web** stays as it is. It is already offered to people
  who sign in without one, confirmed with a passkey or authenticator code when they
  have one, or now with a chat sign-in session.
- **`sign in` in chat:** its help line and the owners' guide now say it is the quick
  way in. `password` is for people who want to sign in with an email.

## 7. Security

- **The same proof as sign-up:** the code or link goes only to a chat already bound
  to the account.
- **Not more than a password:**
  - a chat sign-in never gets past the administrator passkey rule or an
    organisation's single sign-on;
  - sessions record `auth_method = 'chat'`.
- **Step-ups are unchanged:** an approval that needs a passkey still asks for it.
- **No finding out who is on Taskiem:**
  - `/start` answers the same for every number, at the same speed. The look-up and
    the send happen in the background after the answer.
  - Limits are per IP (10 an hour) and per number (5 an hour), counted whether or
    not the number has an account.
- **Guessing:** five wrong codes lock the code, and `/verify` is limited per IP and
  per number like `/login`. A six-digit code with five tries gives one chance in
  200,000 per code.
- **Telling the owner:**
  - Every chat sign-in sends a line to the person's chat: "You signed in to Taskiem
    on the web (Chrome on Windows, Lagos). Not you? Send *sign out everywhere*."
  - If they have a confirmed email, it is sent there too.
  - `sign out everywhere` in chat revokes every web session of theirs (it is new
    here).
- **Audit:** `auth.chat_signin` (with the channel and how: code or link) and
  `auth.chat_signin_refused`, in every business of theirs, with the IP and agent as a
  password login records them.
- **A stolen SIM:** whoever holds the number can sign in, as they could already
  approve things in chat. Someone who wants more turns chat sign-in off for
  themselves (§9).

## 8. Where it lives

- **Migration 00314:**
  - `sessions.auth_method` allows `'chat'`;
  - `chat_signin_codes` and `chat_signin_links`, with no row-level security since
    they belong to a person, not a business, like `whatsapp_otps`, reached through
    definers;
  - `taskiem_auth_chat_signin_issue`, `_check` and `_link_use`;
  - a full Down section.
- **API:** `api/chat_signin.go`.
  - `POST /v1/auth/chat/start`, `POST /v1/auth/chat/verify` and `POST
    /v1/auth/chat/link`, added to the OpenAPI document;
  - `waSignInLink` becomes the link of §5;
  - `waPassword` is Part A.
- **Web:**
  - `web/src/pages/Login.tsx`: the second way in, with number then code;
  - `web/src/pages/SignInLink.tsx`: the page of §5;
  - `Password.tsx`: the line of §3.
- **Words, in five languages:**
  - `door.signin_email` and `door.signin_confirm`;
  - `wa.signin.link` with `{email}`, then the sign-in link's text;
  - `wa.password.*`;
  - `chat.signin.code` (Telegram) and `chat.signin.told`;
  - `wa.help.signin` and `wa.help.password`.
- **Docs:**
  - the owners' guide: how to get onto the web from chat;
  - `docs/whatsapp.md`: the template's widened purpose;
  - `chat-way-in.md` §10.4: an As built.

## 9. To confirm

1. **Who can use chat sign-in.** Recommended: everyone with a bound WhatsApp or
   Telegram. It is the same proof chat approvals already rest on, and each sign-in
   is told to the chat and to the email.
   - The alternative is chat-born owners only, so anyone with a password keeps
     signing in with it.
   - Either way, a person may turn it off for themselves: Account > **Sign in with
     WhatsApp or Telegram**, on or off.
2. **A link from chat, or a code.** Recommended: both, as planned. The code on the web
   works when the phone and the computer are different devices. The link is one tap
   when they are the same.
3. **The new-sign-in notice.** It is sent on every chat sign-in (recommended), and
   only to the chat and the email, never as a separate SMS.

## 10. Slices and tests

| Slice | What | Tests |
| --- | --- | --- |
| A | `sign in` asks for the email first; the link's reply names the email; the password page's line | In chat, on WhatsApp and Telegram: placeholder, then `email`, then confirm, then `sign in`, then reset, then `/login` with the email; an email waiting; single sign-on unchanged |
| B1 | Migration 00314; `/start`, `/verify`; the code on WhatsApp (template) and Telegram; limits; refusals; the notice and the audit | The same answer for a known and an unknown number, and no send for the unknown; a code signs in; the code for another number fails; five wrong lock it; expiry; an admin with a passkey and single sign-on refused; Telegram-only owner by placeholder; `auth_method = 'chat'` |
| B2 | `sign in` sends the link; the link page; `/link`; `sign out everywhere`; `password` takes Part A | The link signs in once; a second use, an expired one and a fetched-but-not-posted one; refusals; sign out everywhere revokes; e2e: from chat to the web app signed in |
| C | The Login page's second way in; Account's switch (if §9.1 keeps it); docs | `web` unit tests; e2e `chat-signin.spec.ts`: number, then code, then the dashboard |

## 11. As built

The founder took the recommendations of §9:
- everyone with a bound WhatsApp or Telegram may sign in with it, and each person may
  turn it off on the Account page;
- both the code and the link are built;
- the notice goes to the chat and to the email on every chat sign-in.

**Where it lives:**
- migration 00314;
- `api/chat_signin.go`;
- `web/src/pages/Login.tsx` (`ChatSignIn`), `web/src/pages/SignInLink.tsx` and
  `web/src/pages/Account.tsx` (`ChatSignIn`);
- the tests in `api/chat_signin_test.go`.

**Where it differs from the plan:**
- **Telegram owners are found by a number Taskiem now keeps,** not by their
  placeholder (§4.2). A Telegram sign-up's placeholder is made from the Telegram
  account, not the number, and the number was kept only as a hash.
  - The number a Telegram person shares at sign-up, proven by Telegram's own-contact
    check, is now kept on their binding (`telegram_bindings.number`, unique;
    `taskiem_chat_telegram_number`).
  - People who linked Telegram before this, or from the web, have none. They sign in
    from chat with `sign in`.
- **The link's page does not greet the person by name** (§5). The role that runs the
  definers may never read a person's name (`TestDispatchRoleCannotReadPayloads`). So
  `POST /v1/auth/chat/link/peek` answers 204 or 404 and says nothing of whose the link
  is, and the page says "Sign in to Taskiem" with its button.
- **`password` names only the email's domain** (§3): "sign in on the web with your
  email (the one at *salon.test*)". Every message chat sends has its addresses hidden
  (`engine/pii`), so a whole or half-hidden address cannot be shown.
- **One reply covers no email and an email waiting** (§3). `door.signin_email` says to
  send `email …`, open the link, then `password` again; there is no separate
  `door.signin_confirm`. It also offers `sign in`.
- **Account > Password** still needs a password, a passkey or an authenticator code to
  set one (§6), as before. Someone who has only ever signed in with their chat chooses
  a password with `password` in chat.
- **The notice names the browser and system** ("Chrome on Windows"), not a place
  (§7). There is no address-to-place lookup.
  - On WhatsApp, outside the 24-hour window, it goes as `taskiem_alert`.
  - With a confirmed email, it also goes there.
- **Two audit actions:**
  - `auth.chat_signin_code` for each code sent, in every business of the person;
  - `auth.chat_signin` (channel, and `code` or `link`) beside the usual `auth.login`
    with method `chat`.
  - `auth.signout_everywhere` records the sessions ended, and
    `auth.chat_signin_setting` the switch.
- **Limits:**
  - asking for codes: 10 an hour per address and 5 an hour per number, and the
    database's 5 codes an hour per person;
  - checking codes and links: the login limiter, per address and per number;
  - links from chat: 3 every 10 minutes per person, and 3 an hour in the database.
- **Administrators** follow the password's rules:
  - one with a passkey is sent no code and is refused the link
    (`wa.signin.passkey`);
  - one without signs in only to enrol a passkey (`EnrolOnly` now holds for `chat` as
    for `password`).
- **Not built:** the end-to-end browser test `chat-sign-in.spec.ts` of §10. The page
  `/signin` with a link that is not good is in the accessibility check, and the flows
  are covered by the API tests.
