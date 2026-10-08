# 0020 — The connector catalogue: signed packages, automated checks, four-eyes review, pinned installs with consent

Date: 2026-10-07 · Status: Accepted (publisher agreement, reviewers and review SLA pending P4-E1 to P4-E3)

## Context

Phase 4 (P4-6, spec 6) opens connectors to third parties. Until now a tenant could upload its own WebAssembly connector (`x_...`, migration 00018) for itself, and a partner could share its own with its sub-tenants (00056). A public catalogue is different: code written by one organisation runs with other organisations' credentials, against their money and their customers' personal data. The sandbox (wazero, decision 0011's approach for code steps, `engine/wasmconn`) already contains what a module can *do* to the host; what it can do *with what it is given* (send credentials to the wrong host, mislabel a payment as a read so it is retried, leak personal data into history) is a matter of the manifest being honest and someone having checked. Spec 6.4 asks for recorded fixtures replayed in CI and a contract-drift monitor; both exist for built-in connectors (`connectors/internal/fixture`, `engine/drift`).

## Decision

1. **Publisher namespaces.** Catalogue ids are `p_<slug>_<name>` (slug: 2 to 30 lower-case letters and digits, no underscore, so the id splits one way). The `p_` prefix keeps them apart from built-ins (the registry refuses to register `p_` and `x_` ids) and from tenants' own `x_` ids. A tenant asks for one slug (`connector_publishers`, unique, reserved words refused); it can rename itself and rotate its key but never set its status, which only an operator's definer function changes (`pending → verified`, `suspended`). Only a verified publisher submits, and a trigger refuses a version whose id is not in its publisher's verified namespace.

2. **A signed package.** `taskiem-connector-package/v1` is one JSON document: manifest, module, conformance suite (every exchange inline), licence, source URL, attestation (original work, contact). Its digest is SHA-256 over a fixed JSON form of all of it (the module by its own SHA-256); the publisher signs `format, id, version, digest` with Ed25519, as audit anchors are signed (spec 9.2), with the key whose public half it registered. The digest is what a reviewer approves and what an installing tenant pins.

3. **A conformance kit, the same fixtures as built-ins.** Cases name an action, its input and credentials, the exchanges it must make (fixture files in the built-in format, `connectors/internal/fixture` now an alias of `engine/conntest`) and an expected output or error kind. The kit replays them against the compiled module **in the sandbox**, with no network but the replay server (a request elsewhere is refused). Beyond pass or fail it reports what makes a manifest dishonest or incomplete: uncovered actions, idempotent writes whose key never reaches the provider, reads that send other than GET or HEAD, and outputs that drift from the schema (the drift monitor's own check).

4. **Automated checks before any person looks.** Signature; strict manifest lint (`engine/catalogue.Lint`: a read named like a change, undeclared personal fields by name, hosts that are IPs, wildcards or private names, non-https base URLs, unverified triggers); the module's import allow-list, ABI export, own memory within the cap, size; an accepted licence; the attestation; every declared host resolving to public addresses only; semver rule 11 against the publisher's published versions; and the conformance suite passing, covering every action, with every idempotent key sent. A failed package is stored as `checks_failed` with its report, so the publisher sees why.

5. **Human review, four eyes.** Reviewers are Taskiem operators on a list (`catalogue_reviewers`), working from the operator CLI. The review function refuses a reviewer not on the list, the submitter, or anyone who is a member of the publisher's tenant (by email), needs a note, and records the checklist confirmed (identity, classes, hosts, personal data, credentials, conformance, licence, docs). Only the definer functions (running as `taskiem_dispatch`) can move a version to approved or rejected; the trigger refuses it from the application role.

6. **Immutable versions, forward-only states.** `in_review → approved → published → revoked`, with `rejected`, `withdrawn` and `checks_failed` as ends. Content columns never change and rows are never deleted, even for the superuser. A version that is in review, approved, published or revoked can never be submitted again; a rejected or failed one can. The publisher publishes an approved version when ready.

7. **Revocation is a kill switch.** The publisher (its own version) or a reviewer revokes a published version with a reason. The catalogue's read functions only return published versions from verified publishers, and the tenant connector source loads installs only through them, so a revoked version (or a suspended publisher's) stops loading for new steps at once (each engine's cache lives ten seconds). Every tenant that installed it gets an alert on all its enabled channels (whatever its rules) and an audit entry in its chain.

8. **Pinned installs with consent.** A tenant installs one version per major (`catalogue_installs`, its own table under RLS). Its consent, the version's hosts and its write actions with their classes, must be echoed in the request. New runs use exactly the pinned version; an upgrade within the major is explicit, shows a diff (hosts, actions, writes, class changes, removed fields, personal fields), and needs consent again when hosts or writes widen or a class changes. The drift monitor applies as to every connector.

9. **Cross-tenant reads through narrow definer functions.** No tenant reads `catalogue_versions` except its own submissions. Listings, a version's manifest, the installed list and an installed module come through definer functions that check the tenant's scope and the version's state; the module function returns a module only to a tenant that installed exactly that version. `taskiem_dispatch` gets routing columns only of tenant tables (`catalogue_installs`: tenant, connector, version; `connector_publishers`: the public identity and status).

10. **Licences.** Accepted: the linked tier of decision 0007, MPL-2.0 (file-level copyleft), and `LicenseRef-Proprietary` under the publisher agreement. GPL, AGPL, SSPL, BUSL and the rest are refused: Taskiem distributes and runs the module for other organisations.

## Alternatives considered

- **Let tenants share `x_` connectors directly with any other tenant.** Rejected: no review, ids that collide across tenants, and nothing for the receiving tenant to consent to.
- **Review only, no automated checks.** Rejected: a reviewer's time is the scarce part; mechanical problems (a bad signature, a private host, a module importing something the host does not provide, an uncovered action) should never reach one.
- **Automated checks only.** Rejected: whether a class is honest depends on the provider's semantics (how long it deduplicates, whether a lookup finds the effect), which only a person reading the provider's documentation can judge.
- **Reviews in the tenant API with an operator role.** Rejected for now: the platform has no operator identity in the API (operators work from the CLI with database access, as for billing and limits); adding one is its own boundary. The four-eyes rule is enforced in the database, so moving reviews to a web console later changes no guarantee.
- **Floating installs (newest minor, like a tenant's own connectors).** Rejected for third-party code: a publisher's minor release would reach every installing tenant without anyone looking, and a minor can add hosts. Pinning plus an explicit upgrade with a diff keeps the tenant in charge.
- **Signing by Taskiem instead of the publisher.** The publisher's signature proves who submitted the bytes reviewed; a Taskiem countersignature for distribution outside the platform can be added later.

## Consequences

- Third-party modules are compiled per submission in a runtime of their own (started and closed per check), not the engine's shared runtime.
- Revocation reaches running engines within the source cache's ten seconds; a step already executing finishes.
- Host resolution is checked at submission only; at run time the egress guard checks every connection (private addresses refused, the vetted address pinned), so DNS that later points inside is still refused.
- The lint's personal-data and write-verb rules are by name; reviewers cover what names miss.
- Reviewer identity in the CLI is asserted with `--as` by an operator who already has database access; the database checks it against the list and the publisher's members. A stronger identity (SSO for operators) belongs with the operator console.
- Not built: paid connectors, ratings, a publisher-facing web page (the API and CLI do it), countersigned packages for offline distribution, automatic re-checks of published versions when the lint tightens.

## Provenance

Package signing over a content digest follows our own audit anchors (spec 9.2). Namespaced publishers, review before publication and revocation are general software-distribution practice from public literature on package registries and supply-chain security; no other product's source was consulted.

## Amendment 2026-10-08

Reviewers can now work from the operator console ([decision 0027](0027-operator-console.md)), signed in as themselves with a passkey and asked for it again for each decision; there the reviewer is the signed-in operator, not an email given with `--as`. `taskiem_catalogue_review` now also refuses an approval unless every checklist item is confirmed (migration 00136), from the CLI as from the console.
