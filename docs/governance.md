# Governance: approval policies, delegation, step-up, four-eyes

Approvals are a step type (spec 9.1). An approval step either names a role and a count, or names a **policy**: a reusable, versioned object that decides who approves from the data being approved.

## Approval policies

```json
{
  "rules": [
    { "when": "=subject.amount_kobo < 50000000", "levels": [{ "role": "credit_officer" }] },
    { "when": "=subject.amount_kobo >= 50000000",
      "levels": [{ "role": "credit_officer" }, { "role": "head_of_credit", "count": 2 }],
      "step_up": "totp" }
  ],
  "constraints": { "forbid_self_approval": true, "distinct_approvers": true },
  "timeout": "24h",
  "on_timeout": "escalate:head_of_operations"
}
```

| Field | Meaning |
| --- | --- |
| `rules` | Tried in order; the first whose `when` (CEL over the approval step's `subject`) holds applies. A rule without `when` always applies. If none applies, the approval step fails with kind `policy`: nobody can approve what the policy does not cover |
| `levels` | Approved in order: level 2 opens when level 1 has `count` approvals (default 1). Any rejection rejects the step |
| `step_up` | The weakest second factor a vote under the rule needs: `passkey`; `totp` (a fresh authenticator code, or a passkey); or `whatsapp_pin` (the approver's WhatsApp PIN entered in a WhatsApp form bound to that decision, or an authenticator code, or a passkey; [WhatsApp](whatsapp.md#approval-pin)) |
| `constraints` | `forbid_self_approval`: whoever started the run, or wrote or published the workflow version, cannot approve, nor can anyone acting for them. `distinct_approvers`: one person approves one level at most. Both default to true |
| `timeout`, `on_timeout` | Used when the step sets none: `reject` (default), `fail`, or `escalate:<role>` (once, to a single approver with that role) |

The step names it: `{"type": "approval", "config": {"policy": "high_value", "subject": {"amount_kobo": "=trigger.body.amount"}}}`.

**Versions.** Saving a policy creates a new version (`PUT /v1/policies/{name}`, or Approval policies in the web app; `policy.manage`). Each run keeps the versions that were active when it started, recorded in its history, so a policy edit never changes an approval already under way, and replays and audits see exactly what governed each decision. A workflow naming a policy with no active version cannot be published. In a Git-led repository, policies live in `policies/<name>.policy.json` and are activated by the sync, with Git review in place of four-eyes.

**Workflow tests** supply policies in the test file (`"policies": {"high_value": {...}}`); `taskiem test` also reads `policies/*.policy.json`.

## Delegation

A member can hand approval roles they hold to a colleague for up to 90 days, with a reason (`POST /v1/delegations`, or Approvals → Delegations). The colleague sees those approvals in their inbox marked "covering for …". A vote under a delegation records both people; maker-checker applies to both, and the person delegating cannot also vote on the same level. The delegator or a member admin can revoke it. A delegation counts only while the delegator still holds the role: taking the role from them (`DELETE /v1/members/{id}/roles/{role}`) ends their delegations of it, and a role removed any other way (SCIM) stops them counting at once. Every delegation, revocation and decision is audited.

## Step-up with an authenticator app

Each member enrols an authenticator under **Account** (`POST /v1/me/totp`, then `/v1/me/totp/confirm` with a code). The secret is an encrypted tenant secret. A code works once: the time step it used is recorded, so it cannot be replayed. An approval that needs step-up returns `403 {"step_up": "totp"}` without a valid code. Eligibility (role, maker-checker, levels) is checked first, so nobody is asked for a code to cast a vote that would be refused.

Five wrong codes within fifteen minutes lock a member's codes out for fifteen minutes (`429`), right or wrong; each wrong code is audited (`mfa.totp.fail`), and so is the lock (`mfa.totp.locked`). A right code resets the count.

An enrolled authenticator is not replaced: it is removed first, with a current code (`DELETE /v1/me/totp`), then set up again.

## Passkeys

Members add passkeys under **Account** (WebAuthn: the device's fingerprint, face or PIN; synced passkeys work). A passkey signs in without a password (**Sign in with a passkey**) and answers step-up: an approval whose policy asks for `passkey` needs one, and a passkey also satisfies `totp`, being the stronger factor (`POST /v1/me/step-up/options`, then the vote with `passkey`). Challenges are single-use, last five minutes, and are bound to their purpose and person, so a sign-in assertion cannot pass step-up. A signature counter that does not advance is refused as a possible copied authenticator.

**Administrators are held to passkeys** when the deployment sets `TASKIEM_PUBLIC_URL` (on by default then; `TASKIEM_REQUIRE_ADMIN_PASSKEYS=false` turns it off). An administrator is anyone holding `member.manage`, `role.manage`, `secret.manage`, `policy.manage`, `git.manage`, `connector.manage`, `pii.reveal` or `pii.erase`. Signed in with a password, they can only add a passkey; once they have one, their password no longer signs them in. A member who loses their passkeys is reset by someone able to grant all their roles (Members, `DELETE /v1/members/{id}/passkeys`), which also ends their sessions; when no such person can, an operator runs `taskiem passkeys reset --email …`. Every addition, removal and reset is audited.

**Changing factors needs more than a session**, so a stolen session cannot enrol its own passkey or authenticator and pass step-up. Adding or removing a passkey (`POST /v1/me/passkeys/options`, `DELETE /v1/me/passkeys/{id}`) and setting up an authenticator (`POST /v1/me/totp`) take a proof in the body: with a passkey or an authenticator enrolled, a fresh `passkey` assertion (for a challenge from `POST /v1/me/step-up/options`) or a current `totp` code; with neither, the `password`. Without it the answer is `403 {"reauth": [...]}` naming what will do. A person with none of these (signed in only by single sign-on) gives nothing more. An administrator held to passkeys enrols their first one with their password. `GET /v1/me` shows `factors`.

A tenant resets the passkeys only of people who belong to it alone: passkeys are the person's, in every tenant they belong to. Someone who also belongs elsewhere removes their own, or an operator resets them (`taskiem passkeys reset`).

The passkey rule for administrators applies to the person, not to one tenant: someone with a passkey who is an administrator in any tenant cannot sign in with a password to any of them.

## Single sign-on

Owners and admins connect an OpenID Connect or SAML 2.0 identity provider under **Members → Single sign-on** (`POST /v1/sso`). It needs `TASKIEM_PUBLIC_URL`, which the provider's settings name: the OIDC redirect URI is `<public URL>/v1/auth/sso/oidc/callback`; for SAML, Taskiem publishes its metadata (entity id, ACS URL) at the address the page shows.

- **Domains.** A connection serves email domains the tenant has proven with a DNS TXT record (`_taskiem-verify.<domain>`). An unverified domain routes nobody, and a provider can only sign in people whose email is on its verified domains: a tenant cannot claim another's people. Several tenants may claim a domain, but only one can verify it, so a claim left unverified does not keep the domain's owner out. A domain hangs only off a connection of the same tenant. The sign-in page offers single sign-on once the email's domain is verified.
- **Members and roles.** With JIT on, the first sign-in creates the member. Everyone gets the connection's default roles, plus the roles mapped to their groups (OIDC groups claim, SAML groups attribute). At each sign-in, roles single sign-on granted follow the groups; roles granted by hand stay. No one can map a group to a role they could not grant themselves.
- **Enforcement.** With enforce on, members whose email is on the connection's domains cannot sign in with a password or a passkey. Owners are exempt, so a broken provider cannot lock the tenant out ("Owner? Use your password" on the sign-in page).
- **Checks.** OIDC: authorization code with PKCE, state and nonce single-use within ten minutes; ID tokens are RS256, PS256 or ES256 only, checked for issuer, audience, expiry and nonce; the email must be verified by the provider. SAML: SP-initiated only; the signed assertion must answer our request, for our audience and ACS URL, within its validity window. Provider calls go through the egress guard to the hosts found when the connection was saved. A sign-in finishes only in the browser that started it: starting sets a short-lived `taskiem_sso` cookie whose hash the request keeps, and the callback must present it, so no one can send their own sign-in to someone else's browser. The cookie is `SameSite=Lax` for OIDC (the callback is a top-level GET) and `SameSite=None; Secure` for SAML (the ACS receives a cross-site POST, which carries no Lax cookie), so SAML needs HTTPS (or `localhost`). The return address after sign-in must be a path on Taskiem. Every sign-in is audited with the groups seen and the roles granted or removed.

## Provisioning (SCIM 2.0)

An identity provider (Okta, Microsoft Entra ID, others) keeps members in step with the directory over SCIM 2.0 (RFC 7643, RFC 7644) at `<public URL>/scim/v2`. It authenticates with an API key holding `scim.provision` (**Members → Provisioning → Create SCIM token**); sessions are refused there, and so are keys without that permission.

- **Users.** `POST /Users` creates a member, or links an existing person with the same email when they already belong to the tenant or their email is on one of its verified SSO domains; anyone else with an account is invited (see Members) and holds nothing, whatever SCIM says, until they accept. The sign-in email is the primary email (else the user name, when it is an email) and stays as provisioned: one tenant's provider cannot rename someone. Filters: `userName`, `externalId`, `emails.value` with `eq`. `PATCH` takes the usual operations, with or without a path, including `active` sent as a string.
- **Roles.** An administrator decides the roles: default roles for everyone provisioned (`viewer` until changed) and roles per group, by group name (`PUT /v1/scim`, held to the no-escalation rule; `owner` cannot be provisioned). The key only moves people between groups. SCIM manages only the roles it granted; roles given by hand stay while the person is active. Changing the mapping, or renaming a group, re-applies it to everyone provisioned.
- **Deprovisioning.** `active: false` or `DELETE` removes all the person's roles in the tenant, including those given by hand, and signs them out; single sign-on will not let a deactivated person back in. Reactivated, they get only what SCIM grants. **Owners are never deprovisioned by SCIM**: an owner removes an owner.
- Every change is audited with the roles granted and removed. Not supported: bulk operations, sorting, ETags, password changes.

## Members and invitations

`POST /v1/members` grants roles by email. Someone new is created, with the password given (optional: leave it out for people who sign in by single sign-on). Someone who already has a Taskiem account belongs to themselves, not to the tenant: unless they are already a member or their email is on one of the tenant's verified SSO domains, they are **invited**. An invitation grants nothing and is not a membership; the person sees it under **Account** (`GET /v1/me/invitations`) and accepts it (`POST /v1/me/invitations/{tenant}/accept`), which grants the roles offered. The answer to `POST /v1/members` is the same whether or not the email had an account. Invitations and acceptances are audited (`member.invite`, `member.invitation.accept`).

Sessions and API keys stop working while their tenant is [suspended](#suspended-tenants), and sessions while their user is disabled. Sign-in (`/v1/auth/login`, `/v1/auth/passkey`) takes only `application/json`, and attempts are limited per address and per account; SSO discovery and start are limited per address.

## Passwords

Passwords are Argon2id hashes of at least 12 characters. A password belongs to the person, not to a tenant: changing or resetting it applies wherever they belong.

**Forgot password.** The sign-in page links to **Forgot password?** (`POST /v1/auth/password/forgot {"email"}`). The answer is always `202` with the same body, whoever the email belongs to, and the work happens after answering, so neither the answer nor its timing tells whether an account exists. A link goes only to someone who:

- has an active account **with a password**. People who sign in only with passkeys or single sign-on are not given a password this way;
- could use a password somewhere: they belong to at least one active tenant that does not hold them to single sign-on. SSO enforcement exempts owners (the break-glass), so an owner's password can always be recovered, and a member held to SSO in every tenant gets nothing.

The link is `<TASKIEM_PUBLIC_URL>/reset-password#token=…`. The token is in the fragment, which browsers send to no server, so it stays out of access logs and `Referer` headers; the page drops it from the address bar once read. The token is a 128-bit selector and a 256-bit secret; only the secret's SHA-256 is stored. A link works **once**, for **30 minutes**; asking again replaces any unused link; **five wrong secrets** for a link lock it. Requests are limited per address (refused with `429`) and per email (five links, then one every ten minutes; past that the answer is the same `202` and nothing is sent). Without `TASKIEM_SMTP_URL`, `TASKIEM_ALERT_FROM` and `TASKIEM_PUBLIC_URL`, the endpoint still answers `202`, sends nothing, and logs a warning (never the token).

**Reset.** `POST /v1/auth/password/reset {"token", "password"}` (JSON only, limited per address and per link) sets the new password, uses the link, ends **every session** of the person in every tenant, records `auth.password.reset` in the audit log of each tenant they belong to, and emails them that their password changed. It **does not sign in**: the person signs in as usual, so nothing about a reset gets round a second factor. An administrator held to passkeys who has one still cannot sign in with the new password; one who has none gets only the enrol-a-passkey session; SSO enforcement applies as at any sign-in. Passkeys, authenticators and API keys are left as they are (an API key can be revoked under Members & keys).

**Change.** Under **Account**, `POST /v1/me/password {"current_password", "new_password"}` changes the password and ends the person's other sessions (`auth.password.change`, audited in each tenant; the person is emailed). Someone without a password who has a passkey or authenticator can set a first one, proving themselves with a `passkey` assertion or `totp` code as when changing factors. Someone who signs in by single sign-on only has nothing to prove themselves with and gets no password this way (`403`). An administrator's enrol-only session cannot change the password: they add a passkey first. API keys are refused.

## Custom roles

Besides the built-in roles, owners and admins define roles as named sets of permissions (Members → Roles, `PUT /v1/roles/{name}`). No one can create, widen, grant or take away a role carrying a permission they do not hold; this also applies to built-in roles. A role someone holds cannot be deleted. Narrowing a role takes effect on its members' next request.

**Business roles decide who approves.** A role with no permissions of its own (`credit_officer`, say), or a custom role that an approval step or policy names, decides who can approve money. Granting or taking one away (by hand, by SSO group mapping or by SCIM mapping) also needs `approval.decide`, so an admin without it cannot make themselves or a friend a credit officer. Turning a name members already hold into a custom role (`PUT /v1/roles/{name}`) gives them its permissions, so it needs `member.manage` and the standing to grant that role.

**Owners.** A tenant keeps at least one owner; two owners revoking each other at the same moment leave one.

## API keys

An API key belongs to the person who made it (`POST /v1/api-keys`). It acts with its own permissions **and** within its owner's current ones: when the owner loses a permission the key loses it at once, and when the owner leaves the tenant or their account is disabled, the key stops working. For four-eyes and maker-checker a key *is* its owner: a policy or version written, a run started, or a version published or deployed with a key counts as the owner's, and API keys never approve (publishing, policies, Git-led connections, approvals). A key made with a key limited to one environment is limited to it too.

A key limited to one environment (`environment` when created) works only there: secrets, variables and connections of other environments are neither listed nor changed, and actions that reach every environment are refused (`403`): publishing, creating environments or changing gates, Git connections and syncs, policies, members and roles, erasure, audit and reports. It may list its own environment's [secret reads](compliance.md#secret-use) (`GET /v1/secrets/reads`, with `audit.read`).

Runs outside `dev` use the version deployed in their environment; pinning another (`version` on `POST /v1/workflows/{id}/runs`) needs `workflow.publish`.

## Suspended tenants

A tenant is suspended by its partner (a sub-tenant: `POST /v1/partner/sub-tenants/{sub}/suspend`) or by the operator (`tenants.status`). While suspended it does no new work:

- **Sign-in, sessions, API keys and end-user tokens** stop working (sessions and tokens of a sub-tenant are also revoked).
- **Schedules** are not claimed, so they do not fire.
- **Webhook and connector deliveries** are answered `423 Locked` (`"code": "suspended"`), before verification and before anything is stored, and counted per day and kind (`ingest_refusals`; `refused_while_suspended` in `GET /v1/limits`). Providers that retry deliver them again after the resume. An edge replica notices the suspension within a minute; until then the engine refuses the start, with the same answer.
- **Runs**: the engine refuses to start any (`runtime.ErrTenantSuspended`, 423 from the API), whatever the path: webhooks, schedules, WhatsApp, USSD, resumes. Runs it queued (behind its rate or a workflow's concurrency) are not admitted. Runs already running carry on to their end; waits and timers keep their place.
- **Repair jobs** wait.

Resuming (`…/resume`) re-enables all of it. Queued runs are admitted at the tenant's rate, and deliveries are taken again. **Schedules continue from the resume and do not catch up**: a fire that fell due while the tenant was suspended is skipped (logged, `taskiem_ingest_deliveries_total{kind="schedule",result="skipped_suspended"}`) and the schedule moves to its next time after now, so a long suspension never ends in a storm of missed runs. The time of each change is kept (`tenants.suspended_at`, `resumed_at`).

## Four-eyes on change

Owners turn these on under **Secrets & settings → Four-eyes** (`PUT /v1/governance`); every change is audited with its before and after.

| Setting | Effect |
| --- | --- |
| Publishing needs a second publisher | Publish returns `202 pending_approval` and opens a publish request. Another member with `workflow.publish`, who neither asked nor wrote the version, publishes or rejects it (Approvals → Publishing to review) |
| A new policy version needs a second person | A saved policy version is `pending` until someone other than its author approves it; the previous version stays active meanwhile |

The second person is a person, not an API key, and not the one behind the key that wrote the change.

Git-led environments publish from merged commits: the repository's branch protection and review are the second pair of eyes there. Because of that, with either setting on (or for a gated environment), **connecting a repository in Git-led mode, switching to it, or pointing a Git-led connection at another provider, host, repository, branch or path needs a second person too** (see [Git](git.md)).
