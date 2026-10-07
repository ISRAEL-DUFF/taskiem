# What needs people

Every open item that engineering cannot finish alone, across all phases, in one place. None of them blocks writing code: each lists what the code already does and what the item will unblock once it is done. When an item is done, tick it here and in the status page it came from. Last updated 2026-10-07 (plan limits).

Sources: [Phase 0](phase-0-status.md), [Phase 1](phase-1-status.md), [Phase 2](phase-2-status.md), the [build plan](spec/build-plan.md) gates, the [integration guides](integrations/), and the [dogfood workflows](../flows/dogfood/README.md).

## Legal and naming (gate G0)

| # | Item | Who | Code status | Unblocks |
| --- | --- | --- | --- | --- |
| L1 | [ ] Counsel reviews the [clean-room policy](clean-room-policy.md); every contributor signs it | Counsel, every contributor | Policy text ready; connectors already follow it | G0 |
| L2 | [ ] Contributor IP assignment agreements drafted and signed | Counsel, every contributor | — | G0 |
| L3 | [ ] Trademark search for "Taskiem" in Nigeria (and Ghana and Kenya if targeted) | Leadership or counsel | The name is a working name ([decision 0006](decisions/0006-working-name.md)); renaming is mechanical | G0, public launch |

## Dogfood go-live (gates G0 and G1, Phase 1 milestones 4 and 5)

| # | Item | Who | Code status | Unblocks |
| --- | --- | --- | --- | --- |
| D1 | [ ] Payrolla's owner confirms the payroll workflow's payloads, endpoints and roles | Payrolla product owner | Workflow built, tested end to end against fakes | G0, G1 |
| D2 | [ ] Payrolla implements the [integration guide](integrations/payrolla.md) | Payrolla engineering | Guide and webhook endpoint ready | G1 |
| D3 | [ ] iSpend's owner confirms the wallet-credit-exception workflow, and iSpend builds `POST /internal/wallet-credit-exceptions` honouring `Idempotency-Key` | iSpend product owner and engineering | Workflow built and tested; the fields iswallet sends on `wallet.credit.reversed` to confirm too | G0, G1 |
| D4 | [ ] Ops confirms the payout-failure alert workflow: on-call phone, Slack webhook, Termii sender and channel | Ops lead | Workflow built and tested | G0, G1 |
| D5 | [ ] Production access to the holdco environment; Taskiem deployed with a public URL (`TASKIEM_PUBLIC_URL`) | Infra / ops | Ready to apply: the image builds and runs read-only as non-root; Compose stack verified; Helm chart and plain manifests in `deploy/` ([kubernetes](kubernetes.md)). Needs a cluster, a Postgres 16, OpenBao, a DNS name and a registry to push the image to | D6, D7, passkeys and SSO in production |
| D6 | [ ] Production iswallet credentials entered (Connections and Secrets pages) | iswallet account holder | Connector built against iswallet's answers | G1 |
| D7 | [ ] Webhook URLs and signing secrets generated in Taskiem and registered with iswallet and Payrolla | Ops, with iswallet and Payrolla | Ingest and signature checks ready | G1 |
| D8 | [ ] Four consecutive weeks of the three dogfood workflows in production | Everyone above | — | G1 |

## Production infrastructure and security (gates G1 and G2)

| # | Item | Who | Code status | Unblocks |
| --- | --- | --- | --- | --- |
| I1 | [ ] Target hardware provisioned; load test repeated there (500 steps/s, p95 under 50 ms) | Infra | `make load` ready; 500 steps/s at p95 5.4 ms on a 4-vCPU dev host | G1 |
| I2 | [ ] Synchronous-standby Postgres; failover test during the chaos run | Infra | Chaos and database-crash suites pass on one instance | G1 |
| I3 | [ ] Internal security review, with no critical findings left open | Security reviewer | — | G1 |
| I4 | [ ] Write-once storage for audit anchors (`TASKIEM_ANCHOR_DIR`) and an anchor signing key (`TASKIEM_ANCHOR_KEY`) | Infra | Anchoring and emailed anchors built | Anchors outside the database |
| I5 | [ ] A mail server for alerts (`TASKIEM_SMTP_URL`, `TASKIEM_ALERT_FROM`) | Infra | Email, Slack and webhook alerts built; email reports "not configured" until then | Email alerts and emailed anchors |
| I6 | [ ] A KMS or OpenBao for tenant keys in production (`TASKIEM_KMS`) | Infra | Local KMS for development; OpenBao supported | Production secrets |
| I7 | [ ] Fixed egress IPs through the sidecar proxy (spec 14.2), for providers that allow-list callers | Infra | The in-process egress guard enforces policy today | Providers with IP allow-lists |
| I8 | [ ] Compare the vendored Python interpreter's SHA-256 with VMware Labs' own 3.12.0 release, and record it in `engine/sandbox/pywasm/PROVENANCE.md` | Anyone with GitHub release access | Pinned hash enforced at load | Supply-chain sign-off for Python steps |

## Providers (Phase 2 milestone 4, gate G2)

Each connector is built from the provider's public documentation and tested against recorded fixtures. What remains needs an account with the provider or an answer from them.

| # | Item | Who | Code status | Unblocks |
| --- | --- | --- | --- | --- |
| P1 | [ ] Sandbox or test accounts for every connector, with credentials stored for the nightly sandbox checks: Paystack, Dojah, Termii, iswallet, Flutterwave, Anchor, Lenco, Breet, Monnify (Moniepoint: a disbursement wallet and contract code, two-factor approval off for API transfers), Interswitch (a funded payout wallet and PIN, a merchant code with Web Checkout), OPay, Remita (demo), Mono, Prembly, Youverify, Africa's Talking (sandbox app) | Whoever holds each provider relationship | All 25 connectors built and fixture-tested | G2 ("nightly sandbox checks") |
| P2 | [ ] **Breet: the unit of bank withdrawal amounts** (local currency or USD) | Breet contact | `withdraw_to_bank` refused until a connection records the unit | Breet bank withdrawals |
| P3 | [ ] Breet: whether a repeated `externalId` is refused; whether the one-minute duplicate guard covers bank withdrawals; separate sandbox and production credentials and webhook secrets | Breet contact | Withdrawals reconcile by `externalId` and are never resent blind | Breet go-live |
| P4 | [ ] Anchor: the answer to a repeated `reference` without the idempotency key, and to a by-reference lookup that matches nothing; whether insufficient funds can be a synchronous 4xx; the meaning of statuses beyond PENDING, COMPLETED, FAILED, REVERSED; the idempotency window (24 or 48 hours); how to create virtual accounts now | Anchor contact | Connector handles each case conservatively ([guide](integrations/anchor.md)) | Anchor go-live, virtual accounts |
| P5 | [ ] Flutterwave (in the sandbox): NGN payouts are in naira; how long a reference stays unique; settle test payouts with `event` deliveries | Flutterwave sandbox holder | Whole-naira payouts enforced ([guide](integrations/flutterwave.md)) | Flutterwave go-live |
| P6 | [ ] Lenco: `/transfer` or `/transactions`; what `declined` means and whether it is final; reference length and uniqueness; whether the webhook signature covers the raw body; the unit of virtual-account amounts | Lenco contact | `declined` parks for a person; raw-body signatures verified ([guide](integrations/lenco.md)) | Lenco go-live |
| P8 | [ ] **OPay's payout API documentation** (wallet and bank transfers, transfer status, balance, bank list, name enquiry): not in OPay's public docs | OPay contact | Collections, refunds and verified callbacks built; payouts not | OPay payouts |
| P9 | [ ] **Remita's live host for invoices (echannel)**, and confirmation that `api-gateway.remita.net` is the production base for Funds Transfer | Remita contact | Live invoice calls refused with a clear error until the host is added | Remita live invoices |
| P10 | [ ] **Monnify's live host** (`api.monnify.com`, from a search excerpt: its docs site blocks automated readers) and production IP whitelisting for disbursements | Monnify contact | Built against the documented sandbox | Moniepoint go-live |
| P11 | [ ] Interswitch payout webhooks (undocumented: payouts are confirmed by polling `get_transfer`), the customer-lookup response shape, and which sandbox host batch payouts use | Interswitch contact | Conservative handling ([guide](integrations/interswitch.md)) | Interswitch go-live |
| P12 | [ ] The remaining "To confirm before go-live" questions in each new guide (about 100 in all, mostly reference limits, unusual statuses and webhook details): [Moniepoint](integrations/moniepoint.md) 8, [Interswitch](integrations/interswitch.md) 8, [OPay](integrations/opay.md) 7, [Remita](integrations/remita.md) 9, [Mono](integrations/mono.md) 8, [Prembly](integrations/prembly.md) 6, [Youverify](integrations/youverify.md) 7, [Africa's Talking](integrations/africastalking.md) 7, [Telegram](integrations/telegram.md) 4, [WhatsApp](integrations/whatsapp.md) 5, [Slack](integrations/slack.md) 5, [Gmail](integrations/gmail.md) 4, [Google Sheets](integrations/googlesheets.md) 2, [MySQL](integrations/mysql.md) 4, [S3](integrations/s3.md) 5, [SFTP](integrations/sftp.md) 5 | Each provider relationship, or a sandbox run | Every case is handled conservatively today: an unclear outcome parks for a person rather than repeating a payment | Go-live of each connector |
| P13 | [ ] Accounts for the messaging and workspace connectors' live checks: a Telegram bot, a Meta app with a WhatsApp Business number, a Slack workspace with the Taskiem app installed, a Google Cloud project with the Gmail and Sheets APIs (plus a Workspace admin to grant domain-wide delegation, or a user to consent for a refresh token) | Workspace and platform admins | Built and tested against fakes | Live checks for those connectors |
| P14 | [ ] A real MySQL 8 (and ideally MariaDB 10.11) server to run the MySQL integration test (`TASKIEM_TEST_MYSQL_DSN`); managed services (RDS, Cloud SQL, Azure) to try | Infra | The protocol client is tested against an in-process fake server only | MySQL go-live |
| P7 | [ ] Accounts or partner agreements for connectors that need one before their docs or sandbox open (for example NIBSS through a licensed partner) | Partnerships | Remaining connectors are built from public docs first | G2 ("15 African connectors live") |

## Identity providers and Git (Phase 2 milestones 2 and 5)

| # | Item | Who | Code status | Unblocks |
| --- | --- | --- | --- | --- |
| Y1 | [ ] Test OIDC and SAML applications in a real identity provider (Okta, Entra ID or Google Workspace), and a SCIM app for provisioning | Workspace admin | SSO and SCIM built and tested against fake providers ([governance](governance.md#single-sign-on)) | Verified SSO and SCIM interop |
| Y2 | [ ] DNS access to add `_taskiem-verify` TXT records for a test domain | Domain admin | Domain verification built | Y1 |
| Y3 | [ ] Register a Taskiem GitHub App (one-click install) | GitHub organisation owner | Tenants use a token or their own GitHub App today ([Git](git.md)) | One-click GitHub install |

## External readiness (Phase 2 milestone 6, gate G2)

| # | Item | Who | Code status | Unblocks |
| --- | --- | --- | --- | --- |
| X1 | [ ] Choose and contract a penetration-testing firm; agree scope (API, web app, ingest, sandboxes, tenant isolation) and a test window | Leadership, security | Every Phase 2 feature built; a staging deployment (D5) gives them a target | G2 |
| X2 | [ ] Run the penetration test; triage findings | Testers, engineering | Engineering fixes critical and high findings as they arrive | G2 ("all critical and high findings fixed") |
| X3 | [ ] Sign at least two design partners (regulated fintechs or MFBs) | Sales / partnerships | Multi-tenant, SSO, SCIM, staging and governance ready | G2 |
| X4 | [ ] Onboard each partner: tenant created, SSO connected, their providers' credentials entered, first workflows in production | Partner success, with the partner | `taskiem bootstrap` or signup (`TASKIEM_ALLOW_SIGNUP`) creates tenants | G2 ("running production workflows") |
| X5 | [ ] A design partner's compliance or risk team reviews approvals, audit and reports, and signs off | The partner's compliance team | Policies, four-eyes, audit chain, anchors and reports built ([governance](governance.md), [compliance](compliance.md)) | G2 |

## Phase 3

Code goes ahead without these. Each is needed before its feature works for real users ([Phase 3 status](phase-3-status.md)).

| # | Item | Who | Code status | Unblocks |
| --- | --- | --- | --- | --- |
| W1 | [ ] Meta Business verification and a WhatsApp Business Account for the shared platform number; the number itself | Ops / legal | The `whatsapp@1` connector and the chat interface run against a fake Cloud API | Every WhatsApp feature in production |
| W2 | [ ] Message templates submitted and approved by Meta (one per notification type, approval request, OTP binding), in each launch language | Product / ops | Templates are defined in code with their variables | Notifications outside the 24-hour window, approvals |
| W3 | [ ] A Business Solution Provider decision (become one, or partner with one) for tenants' own numbers through embedded signup | Leadership | Planned for A2 | Own-number onboarding |
| W4 | [ ] A USSD aggregator account (shortcode, sandbox credentials) | Ops | Planned for A3 | USSD fast path |
| W5 | [ ] Native speakers of Pidgin, Yoruba, Hausa and Igbo to write and test intents and replies; a transcription vendor or model decision | Product | Planned for A4 | Language support, voice notes |
| AI1 | [ ] A model API account for the AI builder (Anthropic by default), its data-processing terms, and whether prompts may leave Nigeria; which self-hosted open model to offer tenants with strict residency | Leadership / legal | Model layer is provider-agnostic, with Claude by default and a fake model for tests | AI builder and repair against a real model |
| AI2 | [ ] 200+ real automation requests from dogfooding and design partners for the evaluation suite, with the expected workflows reviewed by a person | Product / design partners | Suite runner and seed requests in code | Gate G3 (70% first-try) |
| AI3 | [ ] Per-plan monthly AI budgets (amounts) | Leadership | Budgets enforced per tenant with a default | Pricing |
| EM1 | [ ] A holdco product team (Payrolla) to embed the builder for its customers, and its partner connector bridge | Payrolla | Planned for C2–C4 | Gate G3 |
| EM2 | [ ] Custom domain and TLS issuance for white-label partners (DNS ownership checks, certificates) | Infra | Planned for C2 | White-label tiers |

## Decisions waiting on someone

| # | Decision | Who | Today |
| --- | --- | --- | --- |
| E1 | [ ] MySQL: should `sslmode=require` (the default) verify the server certificate? Without verification, MySQL 8's default login can send the password in clear to whoever answers if the network is intercepted | Security / leadership | `require` encrypts but does not verify unless `ssl_ca` is set; the guide recommends `verify-full`. (PostgreSQL now defaults to `verify-full`: [postgres.md](integrations/postgres.md)) |
| E2 | [ ] MySQL: send `KILL QUERY` when an `execute` times out? `max_execution_time` only covers SELECT, so a slow write can keep running after the step parks | Engineering lead | Not sent; the step parks as an unknown outcome |
| E3 | [ ] SFTP: on servers without OpenSSH's atomic rename, refuse to overwrite instead of delete-then-rename? | Engineering lead | Overwrites, and reports `atomic: false` |
| E4 | [ ] A decision-log entry recording the clean-room MySQL protocol client (written from Oracle's docs because the common driver is MPL-2.0) | Founder | Noted in the connector's guide and package docs |
| B1 | [ ] **Plan tiers and prices**: which tiers exist, the value of each limit in each (concurrent runs, ingest rate, backlog, workers, retention...), whether any tier has run quotas at all (spec 16 promises flat pricing with no per-execution counts), and the naira prices (spec 18) | Founder, with sales | Every limit is enforceable per tenant and set by operators (`taskiem tenants limits`, [operations](operations.md#plan-limits)); platform defaults are generous and quotas are off. Applying a tier is a list of `--set` values until billing (Phase 4) does it | Phase 4 billing; partner contracts |

## Engineering carries on meanwhile

None of the items above stops code. Work that needs no one:

- **Connectors** are built: 16 African (Paystack, Dojah, Termii, iswallet, Flutterwave, Anchor, Lenco, Breet, Moniepoint, Interswitch, OPay, Remita, Mono, Prembly, Youverify, Africa's Talking), against G2's 15, and the global set (WhatsApp, Telegram, Slack, Gmail, Google Sheets, MySQL, S3, SFTP, PostgreSQL). What remains for each is its live sandbox check (P1).
- **Bitbucket** (done): Bitbucket Cloud for Git-led and platform-led modes ([git](git.md)).
- **Deployment** (done): image, Helm chart and Kubernetes manifests (spec 15.4), so D5 is a matter of applying them ([kubernetes](kubernetes.md)).
- **Known gaps** carried from Phase 1: all done. Incremental decision state for long histories; plan caps and soft ingest limits ([operations](operations.md#plan-limits); the tiers and prices themselves are B1); password reset ([passwords](governance.md#passwords)); per-use `secret.read` auditing ([compliance](compliance.md#secret-use)); and a design for reaching private databases ([decision 0013](decisions/0013-private-network-access.md)).
- **Pen-test preparation**: a threat model and scope document, so X1 can start the day a firm is chosen.
- **Phase 3** deliverables, once G2's engineering criteria are met.
