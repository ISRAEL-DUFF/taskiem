# 0030 — Partner onboarding: people and workflow packages in sub-tenants

Date: 2026-10-14 · Status: Accepted

Spec 13.1, 13.4. Builds on [0015](0015-embedding-tenancy.md) (sub-tenants, `taskiem_partner_enter`). The partner guide is [embedding.md](../embedding.md#14-people-in-a-sub-tenant); the Payrolla onboarding calls are in [the Payrolla guide](../integrations/archive/payrolla.md#one-sub-tenant-per-company).

## Context

Payrolla wants one sub-tenant per customer company, set up by an API call when the company turns Taskiem on. Each company has its own people (an account holder and payroll approvers, who are the company's staff, not Payrolla's), its own webhook signing secret and report token, its own funding wallet and its own approvers. Until now the partner API could create and observe sub-tenants but could not put people into one, nor put a workflow into one; both needed Taskiem staff.

## Decision

1. **People join a sub-tenant by an ordinary invitation.** `POST /v1/partner/sub-tenants/{sub}/invitations` writes a `member_invitations` row in the sub-tenant (new `source` value `partner`), through `taskiem_partner_enter`, so it is audited in both chains (`partner.subtenant.invitation.create`, and `member.invite` in the sub-tenant's). The person accepts with their own credentials and becomes a member of that sub-tenant only: memberships are per tenant, and nothing gives them the partner or a sibling. The sub-tenant's admins see the invitation in their own list; the partner lists and cancels only its own.

2. **Someone with no account gets a single-use link, by email only.** The account is created without any way to sign in, and `taskiem_invitation_link_issue` stores a link (16-byte selector, 256-bit secret kept as SHA-256, seven days, five wrong secrets lock it) in `invitation_links`, a table only definer functions reach (migration 00171). The link opens `/accept-invitation`, where the person creates a passkey; `POST /v1/auth/invitation/accept` registers it, accepts the invitation and signs them in, in one transaction. A link works only while the invitation stands, the tenant is active and the account is **unclaimed** (no password, no passkey, no membership anywhere): someone who already has an account is asked by email to sign in and accept as usual, so a link can never add a credential to an account someone already uses. The partner's answer is the same whether or not the email had an account, and never carries the link: the partner gets neither a credential nor a session.

3. **The partner may make a sub-tenant's first owner, and only that one.** A company should run its own sub-tenant: its account holder invites colleagues, sets secrets, changes governance. So the partner may invite `owner` while the sub-tenant has no owner and none is invited (409 otherwise); later owners are made by the sub-tenant's owners, as everywhere ("only an owner can grant owner"). Every other built-in role, and the sub-tenant's custom roles, the partner may offer at any time.

4. **Custom roles are defined, not implied.** A role the partner offers must be built in or a custom role of the sub-tenant; a business role (one nobody defined, which decides who approves) is refused, so what a role allows is always written down. The partner creates roles with `PUT /v1/partner/sub-tenants/{sub}/roles/{name}` (any permission but `partner.*`, with the plan's custom roles feature) and changes only roles it created that nobody holds yet: once a member holds one, only the sub-tenant's admins change what it allows. Roles the sub-tenant's own people made are theirs.

5. **A workflow package is a shared workflow; an install is a copy.** The partner shares one of its own workflows (`shared_workflows`, migration 00172) and installs a version of it into a sub-tenant: the definition is copied into a new workflow of the sub-tenant, created and published inside `taskiem_partner_enter`. The copy belongs to the sub-tenant: it runs with the sub-tenant's variables, secrets, connections and egress allow-list, under its key, and the partner's tenant is out of scope for every read it makes. `package_installs` remembers which package a workflow came from, as an id only.

6. **Publishing follows the sub-tenant's governance.** A first install publishes to the ungated environments the partner names, or, with four-eyes on, opens a publish request in the key owner's name, which a second person decides (the key's owner cannot). Installing a newer version adds a **draft**: production changes only when someone in the sub-tenant (or a publish request) publishes it. Installing the same version again changes nothing.

7. **The partner sets configuration, never secrets.** An install takes values for the variables the definition reads (`env.<name>`; any other name is refused) and the roles it needs (missing ones created, existing ones kept). Its answer names the webhook URLs per environment and which secrets are set or missing, never a value. Secrets, including the webhook's signing secret, are set by the sub-tenant's own people: the signing secret with "New signing secret" on the workflow's Endpoints page (`POST /v1/workflows/{wf}/signing-secret`, `secret.manage`), shown once. Because http steps reach only hosts on the sub-tenant's egress allow-list, which only its own people change, a variable the partner sets cannot send the company's secrets to a host the company has not allowed.

8. **Payload size stays an operator decision with a partner ceiling.** `PUT /v1/partner/sub-tenants/{sub}/limits` already sets `max_payload_bytes`, within the partner's own value and the platform's 10 MiB. A partner that needs more than the 1 MiB default asks an operator to raise its own (`taskiem tenants limits <partner> --set max_payload_bytes=…`).

## Why

- **Invitations rather than a new membership path.** The invitation machinery already has the right shape: the person, not the inviter, decides to join, with their own credentials; admins see what is pending; acceptance is audited. Reusing it keeps one way into a tenant.
- **Links only for unclaimed accounts.** A link that could add a passkey to an existing account would let whoever controls the inviting API take that account over (the partner, or anyone with the partner's key). Limiting links to accounts nobody can sign in to makes the link worth exactly what a new signup is.
- **First owner only.** Without an owner a company depends on the partner for every change; with an unlimited right to make owners, a partner could add its own staff as owners behind the company's back. One owner, then the company's own say, is the middle.
- **Copies, not references.** A sub-tenant running the partner's workflow by reference would make the partner's edits live in every company at once and blur whose data a run reads. A copy keeps the sub-tenant's governance and history intact and makes upgrades explicit drafts.

## Consequences

- A partner can still put its own staff into a sub-tenant by inviting them. They act as members under their own names, visible in the sub-tenant's member list and audit chain; that is a choice the company sees, not a hidden channel.
- Creating an account for an email that has none reserves the address until someone sets it up, as inviting does elsewhere (`POST /v1/members`). The owner of the address can still accept from the emailed link.
- A sub-tenant's later changes to an installed workflow are its own; a newer package version arrives as a draft beside them, merged by the sub-tenant's people.
- Without email (`TASKIEM_SMTP_URL`, `TASKIEM_ALERT_FROM`, `TASKIEM_PUBLIC_URL`), invitations are recorded (`delivery: none`) but someone with no account cannot set one up.
- Approvals under four-eyes in a fresh sub-tenant need a person in it, or a second person at the partner with their own key.
