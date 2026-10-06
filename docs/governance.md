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
| `step_up` | `totp`: each approver enters a fresh code from an authenticator app when voting. Passkeys arrive with passkey sign-in (Phase 2, milestone 5) |
| `constraints` | `forbid_self_approval`: whoever started the run, or wrote or published the workflow version, cannot approve, nor can anyone acting for them. `distinct_approvers`: one person approves one level at most. Both default to true |
| `timeout`, `on_timeout` | Used when the step sets none: `reject` (default), `fail`, or `escalate:<role>` (once, to a single approver with that role) |

The step names it: `{"type": "approval", "config": {"policy": "high_value", "subject": {"amount_kobo": "=trigger.body.amount"}}}`.

**Versions.** Saving a policy creates a new version (`PUT /v1/policies/{name}`, or Approval policies in the web app; `policy.manage`). Each run keeps the versions that were active when it started, recorded in its history, so a policy edit never changes an approval already under way, and replays and audits see exactly what governed each decision. A workflow naming a policy with no active version cannot be published. In a Git-led repository, policies live in `policies/<name>.policy.json` and are activated by the sync, with Git review in place of four-eyes.

**Workflow tests** supply policies in the test file (`"policies": {"high_value": {...}}`); `taskiem test` also reads `policies/*.policy.json`.

## Delegation

A member can hand approval roles they hold to a colleague for up to 90 days, with a reason (`POST /v1/delegations`, or Approvals → Delegations). The colleague sees those approvals in their inbox marked "covering for …". A vote under a delegation records both people; maker-checker applies to both, and the person delegating cannot also vote on the same level. The delegator or a member admin can revoke it. Every delegation, revocation and decision is audited.

## Step-up with an authenticator app

Each member enrols an authenticator under **Account** (`POST /v1/me/totp`, then `/v1/me/totp/confirm` with a code). The secret is an encrypted tenant secret. A code works once: the time step it used is recorded, so it cannot be replayed. An approval that needs step-up returns `403 {"step_up": "totp"}` without a valid code. Eligibility (role, maker-checker, levels) is checked first, so nobody is asked for a code to cast a vote that would be refused.

## Passkeys

Members add passkeys under **Account** (WebAuthn: the device's fingerprint, face or PIN; synced passkeys work). A passkey signs in without a password (**Sign in with a passkey**) and answers step-up: an approval whose policy asks for `passkey` needs one, and a passkey also satisfies `totp`, being the stronger factor (`POST /v1/me/step-up/options`, then the vote with `passkey`). Challenges are single-use, last five minutes, and are bound to their purpose and person, so a sign-in assertion cannot pass step-up. A signature counter that does not advance is refused as a possible copied authenticator.

**Administrators are held to passkeys** when the deployment sets `TASKIEM_PUBLIC_URL` (on by default then; `TASKIEM_REQUIRE_ADMIN_PASSKEYS=false` turns it off). An administrator is anyone holding `member.manage`, `role.manage`, `secret.manage`, `policy.manage`, `git.manage`, `connector.manage`, `pii.reveal` or `pii.erase`. Signed in with a password, they can only add a passkey; once they have one, their password no longer signs them in. A member who loses their passkeys is reset by someone able to grant all their roles (Members, `DELETE /v1/members/{id}/passkeys`), which also ends their sessions; when no such person can, an operator runs `taskiem passkeys reset --email …`. Every addition, removal and reset is audited.

## Single sign-on

Owners and admins connect an OpenID Connect or SAML 2.0 identity provider under **Members → Single sign-on** (`POST /v1/sso`). It needs `TASKIEM_PUBLIC_URL`, which the provider's settings name: the OIDC redirect URI is `<public URL>/v1/auth/sso/oidc/callback`; for SAML, Taskiem publishes its metadata (entity id, ACS URL) at the address the page shows.

- **Domains.** A connection serves email domains the tenant has proven with a DNS TXT record (`_taskiem-verify.<domain>`). An unverified domain routes nobody, a domain belongs to one tenant, and a provider can only sign in people whose email is on its verified domains: a tenant cannot claim another's people. The sign-in page offers single sign-on once the email's domain is verified.
- **Members and roles.** With JIT on, the first sign-in creates the member. Everyone gets the connection's default roles, plus the roles mapped to their groups (OIDC groups claim, SAML groups attribute). At each sign-in, roles single sign-on granted follow the groups; roles granted by hand stay. No one can map a group to a role they could not grant themselves.
- **Enforcement.** With enforce on, members whose email is on the connection's domains cannot sign in with a password or a passkey. Owners are exempt, so a broken provider cannot lock the tenant out ("Owner? Use your password" on the sign-in page).
- **Checks.** OIDC: authorization code with PKCE, state and nonce single-use within ten minutes; ID tokens are RS256, PS256 or ES256 only, checked for issuer, audience, expiry and nonce; the email must be verified by the provider. SAML: SP-initiated only; the signed assertion must answer our request, for our audience and ACS URL, within its validity window. Provider calls go through the egress guard to the hosts found when the connection was saved. Every sign-in is audited with the groups seen and the roles granted or removed.

## Provisioning (SCIM 2.0)

An identity provider (Okta, Microsoft Entra ID, others) keeps members in step with the directory over SCIM 2.0 (RFC 7643, RFC 7644) at `<public URL>/scim/v2`. It authenticates with an API key holding `scim.provision` (**Members → Provisioning → Create SCIM token**); sessions are refused there, and so are keys without that permission.

- **Users.** `POST /Users` creates a member, or links an existing person with the same email. The sign-in email is the primary email (else the user name, when it is an email) and stays as provisioned: one tenant's provider cannot rename someone. Filters: `userName`, `externalId`, `emails.value` with `eq`. `PATCH` takes the usual operations, with or without a path, including `active` sent as a string.
- **Roles.** An administrator decides the roles: default roles for everyone provisioned (`viewer` until changed) and roles per group, by group name (`PUT /v1/scim`, held to the no-escalation rule; `owner` cannot be provisioned). The key only moves people between groups. SCIM manages only the roles it granted; roles given by hand stay while the person is active. Changing the mapping, or renaming a group, re-applies it to everyone provisioned.
- **Deprovisioning.** `active: false` or `DELETE` removes all the person's roles in the tenant, including those given by hand, and signs them out; single sign-on will not let a deactivated person back in. Reactivated, they get only what SCIM grants. **Owners are never deprovisioned by SCIM**: an owner removes an owner.
- Every change is audited with the roles granted and removed. Not supported: bulk operations, sorting, ETags, password changes.

## Custom roles

Besides the built-in roles, owners and admins define roles as named sets of permissions (Members → Roles, `PUT /v1/roles/{name}`). No one can create, widen, grant or take away a role carrying a permission they do not hold; this also applies to built-in roles. A role someone holds cannot be deleted. Narrowing a role takes effect on its members' next request.

## Four-eyes on change

Owners turn these on under **Secrets & settings → Four-eyes** (`PUT /v1/governance`); every change is audited with its before and after.

| Setting | Effect |
| --- | --- |
| Publishing needs a second publisher | Publish returns `202 pending_approval` and opens a publish request. Another member with `workflow.publish`, who neither asked nor wrote the version, publishes or rejects it (Approvals → Publishing to review) |
| A new policy version needs a second person | A saved policy version is `pending` until someone other than its author approves it; the previous version stays active meanwhile |

Git-led environments publish from merged commits: the repository's branch protection and review are the second pair of eyes there.
