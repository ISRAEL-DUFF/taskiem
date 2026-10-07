# Connector submissions and the catalogue

The public connector catalogue lets an organisation publish a connector it wrote for every Taskiem customer to install. Third-party code that handles other organisations' credentials and money is a supply chain, so every version goes through the same pipeline: a signed package, automated checks in the sandbox, a review by Taskiem by someone outside the publisher, then publication. Installing organisations pin a version and consent to where its data goes. Design: [decision 0020](decisions/0020-connector-catalogue.md); threats: boundary B18 in the [threat model](security/threat-model.md).

Write and test the connector first: [Connector SDK](connector-sdk.md).

```
submitted ──▶ automated checks ──┬─▶ checks_failed ──▶ (fix, submit again)
                                 └─▶ in_review ──▶ human review ──┬─▶ rejected
                                                                  └─▶ approved ──▶ published ──▶ revoked
                    (withdrawn: the publisher, any time before publication)
```

## For publishers

### 1. A namespace

Catalogue connectors are `p_<publisher>_<name>`. The namespace (2 to 30 lower-case letters and digits) is yours alone: no one else can publish under it, and it can never collide with a built-in (`paystack`) or with an organisation's own connectors (`x_...`). Reserved words (`taskiem`, `official`, `builtin`, `system`, `admin`, `platform`, `verified`, `support`) are refused.

```sh
taskiem connector keygen -o publisher.key           # keep publisher.key secret
taskiem connector publisher --slug acme --name "Acme Ltd" --public-key <public key printed by keygen>
```

The namespace starts **pending**. Taskiem verifies the publisher (who you are, a contact that answers, that the name is yours to use) before you can submit; until then submissions are refused. The same command with a new `--public-key` rotates the signing key (audited); packages signed with the old key no longer verify. You need `connector.manage`, and an API key not limited to one environment.

### 2. Package and submit

```sh
taskiem connector build ./ledger
taskiem connector test ./ledger
taskiem connector package --key publisher.key --licence Apache-2.0 --contact dev@acme.example --original ./ledger
taskiem connector submit ./ledger/p_acme_ledger-1.0.0.tcpkg
```

`package` refuses to write a package whose manifest fails the strict lint or whose conformance suite fails, leaves an action uncovered, or never sends an idempotent write's key. `submit` prints each automated check:

| Check | Passes when |
| --- | --- |
| `signature` | The digest matches the content and the signature verifies with your registered key |
| `manifest` | The strict lint has no errors (classes honest, hosts declared, personal data declared, triggers verified), the id is in your namespace, and the package's id and version match the manifest's |
| `module` | Under 32 MiB; imports only WASI and the host functions `input_read`, `http_request`, `http_response_read`, `log`; exports `taskiem_execute_v1`; defines its own memory, starting within the cap (128 MiB); loads with the manifest |
| `licence` | On the [accepted list](#licences) |
| `attestation` | `--original` given, and a contact email address |
| `hosts` | Every declared host resolves, and only to public addresses (no private, loopback, link-local, metadata or documentation ranges) |
| `semver` | The version is newer than every published version of its major, and does not remove actions, input or output fields, or change a class within the major ([rule 11](contracts/connector-v1.md)) |
| `conformance` | Every case passes in the sandbox, every action has a case, and every idempotent write's key reaches the provider |

A package that fails is kept as `checks_failed` with the reasons (`taskiem connector submissions`, or `GET /v1/catalogue/submissions/{id}`); fix it and submit again, under the same version if you like. One that passes is `in_review`. A version that is in review, approved, published or revoked can never be submitted again: raise the version.

### 3. Review, publish, revoke

A reviewer approves or rejects with a note, which you see on the submission. Once approved, **you** publish it, when you are ready:

```sh
taskiem connector publish p_acme_ledger@1.0.0       # or the submission id
taskiem connector withdraw p_acme_ledger@1.1.0      # before publication
taskiem connector revoke p_acme_ledger@1.0.0 --reason "sends amounts in naira, not kobo"
```

**Revoking** is the kill switch for a version you find is wrong: it leaves the catalogue, new steps stop using it at once (within a minute on every engine), and every organisation that installed it is alerted on all its alert channels with your reason. It cannot be undone; publish a fixed version. Taskiem can revoke a version too.

A published version never changes. Fix things in a new version: a patch or minor for compatible changes, a new major for anything rule 11 forbids. Installing organisations stay on the version they pinned until they upgrade.

### Licences

Accepted (decision 0020): MIT, MIT-0, Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC, PostgreSQL, 0BSD, Unlicense, CC0-1.0, BlueOak-1.0.0, MPL-2.0, and `LicenseRef-Proprietary` (closed source, licensed to Taskiem and installing organisations under the publisher agreement). GPL, AGPL, SSPL, BUSL and other licences are refused: Taskiem runs the module for other organisations, and their terms would reach them.

### Rules

- Original work. Do not copy another product's connector definitions, code or fixtures ([clean-room policy](clean-room-policy.md)); you attest this with every package.
- The connector talks only to the provider it names, sends credentials only as the provider's documentation says, and never returns credentials in outputs or logs.
- Fixtures contain no real personal data, keys or account numbers.
- Respond to the contact address: Taskiem and installing organisations report problems there.
- The publisher agreement ([needs people](needs-people.md#phase-4), P4-E1) governs liability, support and takedown.

## For installing organisations

Connector catalogue (in the web app), or the API:

| | |
| --- | --- |
| `GET /v1/catalogue` | Published connectors: publisher, versions (newest first), each with its hosts, actions and classes, personal fields, licence, package digest, and the consent it asks for; and which versions you installed |
| `GET /v1/catalogue/connectors/{id}/{version}` | One published or revoked version |
| `POST /v1/catalogue/installs` | `{connector, version, consent: {hosts, writes}}`: the consent must cover the version's hosts and write actions with their classes (the client shows them and echoes what the person agreed to). `connector.manage` |
| `GET /v1/catalogue/installs` | Your installs: the pinned version, its state (`published`, `revoked` with the reason), and a newer version of the same major when there is one |
| `GET /v1/catalogue/installs/{id}/{major}/upgrade?to=VERSION` | What changes: added and removed hosts, actions, writes, class changes, removed fields, personal fields, and whether consent is needed again |
| `POST /v1/catalogue/installs/{id}/{major}/upgrade` | `{version, consent}`; consent is required when the new version adds hosts or writes or changes a class |
| `DELETE /v1/catalogue/installs/{id}/{major}` | Uninstall; workflows that use it stop validating |

An installed version is used exactly: new runs use the pinned version, never a newer one, until you upgrade. Each major is installed separately (`p_acme_ledger@1` and `@2` side by side), since workflows pin the major. Add a connection for it and use it like any connector. The contract-drift monitor watches its outputs as it does every connector's. Installs, upgrades and uninstalls are audited with the package digest and the consent given. If a version you installed is revoked, or its publisher suspended, steps using it fail ("not installed") and you are alerted; upgrade to a good version or uninstall.

## For reviewers

Reviewers are Taskiem operators on the reviewer list, working from the operator CLI (database access, as for `taskiem billing` and `taskiem tenants`). Every decision is audited in the publisher's chain.

```sh
taskiem catalogue reviewers add reviewer@taskiem.example
taskiem catalogue publishers                           # pending namespaces
taskiem catalogue publishers verify acme
taskiem catalogue queue
taskiem catalogue show <submission id>
taskiem catalogue review <id> --as reviewer@taskiem.example --approve --confirm all --note "Checked against the provider's API reference"
taskiem catalogue review <id> --as reviewer@taskiem.example --reject --note "transfer is idempotent_write but the provider only deduplicates for 24h: use reconcilable_write"
taskiem catalogue revoke p_acme_ledger 1.0.0 --as reviewer@taskiem.example --reason "..."
taskiem catalogue publishers suspend acme --note "..."    # every version stops loading
```

**Four eyes.** The reviewer must be on the list, and must not be the submitter or a member of the publisher's organisation; the database refuses otherwise. Only a review moves a submission to approved or rejected (no API, and not the publisher's own database role, can).

### Review checklist

`taskiem catalogue show` prints the manifest summary, the lint warnings, every automated check and conformance case, and this checklist. To approve, confirm every item (`--confirm all`, or the keys):

| Key | Confirm |
| --- | --- |
| `identity` | The publisher is who the namespace says, and the contact address answers |
| `classes` | Every action's class is honest. Reads change nothing (look hard at any read flagged for sending POST). Idempotent writes send the engine's key in the field where the provider deduplicates, and the provider's deduplication outlasts Taskiem's retries; otherwise it is reconcilable or unsafe. Reconcilable writes have a lookup that really finds the effect by something the engine controls. Anything else is `unsafe_write` |
| `hosts` | The declared hosts belong to the provider the connector names (its documentation, its domains), and nothing else is reached; any extra host has a reason |
| `pii` | Every field holding personal data, in inputs and outputs, is declared, including ones the lint cannot recognise by name (a `beneficiary` object, a `narration` that carries names) |
| `credentials` | Credentials go only to the provider, the way its documentation says, and never into outputs, errors or logs |
| `conformance` | The cases come from the provider's real behaviour (its sandbox or documentation) and cover success, refusals, unknown outcomes and, for reconcilable writes, the lookup finding and not finding |
| `licence` | The licence and the attestation of original work are credible; nothing is copied from another product's connector |
| `docs` | Name, description, action titles and field descriptions are clear to a builder who has not read the provider's documentation |

Reject with a note that says what to change. A reviewer who is unsure asks the publisher at the contact address before deciding; the submission waits in review.

### Revocation

Revoke a published version when it is found to be harmful or badly wrong: credential leakage, calls to undeclared purposes, a dishonest class that risks double payments, a provider takedown request, a licence problem. Give a reason the installing organisations can act on. The CLI reports how many organisations it alerted. Suspending a publisher stops all its versions at once and takes them out of the catalogue; reinstating brings them back (revoked versions stay revoked).

The review service level, the publisher agreement and who reviews are open: [needs people](needs-people.md#phase-4) P4-E1 to P4-E3.
