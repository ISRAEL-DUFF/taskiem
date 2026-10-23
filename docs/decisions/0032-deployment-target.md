# 0032 — Deployment target: one VM until a named trigger, then Kubernetes with the Helm chart

Date: 2026-10-15 · Status: In effect for staging (https://staging.ispend.name.ng, running by 23 October 2026); production's provider and region pending P4-C1

## Context

The spec names Kubernetes and a Helm chart as the cloud deployment (15.4, 17) and Docker Compose for self-hosted and development (2.3), but no decision weighed Kubernetes against anything else. Decision 0024 assumes it. The repository now has a chart, generated manifests and a Compose file, and nothing is deployed: D5 (a deployment with a public URL) is open, and with it the partner sandbox tests that need Payrolla and iswallet to call us (W3, N4, T1).

What actually depends on Kubernetes:

- **Container steps** (spec 7.5, [container steps](../container-steps.md)). The only production runner creates one Pod per attempt in a sandbox namespace under a gVisor RuntimeClass, with NetworkPolicies and a per-attempt Secret (trust boundary B15). The other runner, `local`, is not a sandbox and refuses to start without `TASKIEM_CONTAINER_LOCAL_DEV=1`.
- Nothing else. Every role is the same binary (`taskiem serve --role`); `--role all` runs them in one process. Scheduler leadership is a Postgres advisory lock, workers claim with `SKIP LOCKED`, migrations are forward-only and compatible with the previous release, and workers drain on shutdown. None of this needs an orchestrator.

What Kubernetes adds beyond that: separate scaling per role and per worker pool (0024), rolling upgrades with no gap in ingest, pod disruption budgets, and one install that runs the same on any provider, a customer's infrastructure included (spec 2.3, "Enterprise dedicated").

The load today is small: the Payrolla pilot pays 2 people (₦3,000), Payrolla has no production customer yet, and 100 requests a minute to iswallet covers it (partner tracker N1).

## Decision

1. **Kubernetes with the Helm chart stays the target** for the production cloud and for enterprise dedicated installs. The chart remains the supported way to run Taskiem at scale; CI keeps linting it and regenerating the plain manifests.
2. **Until a trigger in point 4 is met, staging and the first production environment may run on one VM**: the same signed image, every role in one process (`taskiem serve --role all`), behind a TLS-terminating reverse proxy. Same image and same environment variables, so moving to Kubernetes later changes where Taskiem runs, not what runs.
3. **A VM deployment must still keep the platform's guarantees.** Each of these is required, not optional:
   - Postgres 16 on a managed service or a separate host with backups and point-in-time recovery (spec 15.4); not a container on the same VM's disk.
   - OpenBao with persistent, unsealed storage (`TASKIEM_KMS=openbao`). Never the Compose file's dev-mode OpenBao, which loses every secret on restart; never `TASKIEM_KMS=local`.
   - HTTPS only: `TASKIEM_PUBLIC_URL` on `https://`, secure cookies, HSTS (on by default with an https URL), admins held to passkeys (the default).
   - Container steps off: `TASKIEM_WORKER_QUEUES` without `container`, and no `TASKIEM_CONTAINER_*` set. The `local` runner is never used outside a developer's machine.
   - `TASKIEM_ANCHOR_DIR` and `TASKIEM_ARCHIVE_DIR` on a persistent volume, ideally write-once for anchors.
   - Hosted in Nigeria (spec 2.3, P4-C1), with the same metrics, alerts and status page as the cloud (0023).
4. **Move to Kubernetes when the first of these happens:**
   - container steps are offered to any tenant;
   - a tenant needs a dedicated worker pool, or an enterprise customer needs a dedicated install;
   - a customer contract carries the 99.9% availability target for API and webhook ingest (spec 15.3), which one VM cannot meet through its own upgrades and host maintenance;
   - one VM's CPU or memory, or one process's Postgres connection pool, is the bottleneck in the load test (`make load`) at the expected peak.
5. **The image is published by CI** to the registry the chart names (`ghcr.io/israel-duff/taskiem`), signed, on every merge to main. Both targets pull it; neither builds on the host.

## Alternatives considered

- **Kubernetes from day one, for staging too.** Rejected for now: it needs a cluster, cert-manager, an ingress controller and a gVisor node pool before the first partner call, to run a pilot one VM serves. Staging is what unblocks Payrolla and iswallet (D5, W3, N4); it should not wait for the production platform decision (P4-C1).
- **A provider's managed container service** (Cloud Run, ECS, App Platform and the like). Rejected: availability in a Nigerian region is uncertain, each is specific to one provider (enterprise dedicated installs need something portable), and none offers the gVisor sandbox container steps rely on without Kubernetes underneath.
- **HashiCorp Nomad.** Rejected: its licence is source-available (BUSL), which the licence policy avoids for the same reason as Vault and Redis (0007, spec 17).
- **The Compose file as it is, for staging.** Rejected: its OpenBao runs in dev mode and its Postgres has no backups. A VM deployment uses Compose only for what point 3 allows.
- **A long-lived Compose stack as production.** Rejected beyond the first environment: it has none of the chart's disruption budgets, per-role scaling or rolling upgrades, and keeping two production shapes in step costs more than it saves once a trigger is met.

## Consequences

- An upgrade on the VM is a short stop: the process drains workers for up to 30 seconds (`TASKIEM_WORKER_DRAIN`), migrations run, and the new process starts. Webhooks sent during the gap get connection errors; Payrolla and iswallet retry, and accepted events are never lost. Upgrades are scheduled outside payroll runs.
- The VM's public address is the platform's egress address, which suits partners who want IPs for forensics (P4) until NAT exists (P4-C5).
- Container steps stay unavailable on the cloud until the move. The feature is built and tested; it is not offered.
- Two follow-ups before any deployment: a CI job that pushes the signed image (point 5), and a VM deployment guide with a Compose file that meets point 3 (persistent OpenBao, no bundled database, a reverse proxy for TLS).
- The penetration test (X1) can target the VM staging, but the move to Kubernetes adds boundaries it would not have tested (the container sandbox, B15; network policies); the test scope should say which deployment it covered.

## Amendment, 2026-10-15: the VM runs itself

Point 3's guarantees stay; the manual steps around them go. `deploy/vm/install.sh` and `taskiem-vps` ([one VM](../vm.md)) install, upgrade, back up, restore and watch the VM:

1. **Auto-unseal with a static key** (OpenBao 2.4+), held in a root-only file on the VM and left out of backups; it is on an offline recovery sheet. On one VM a manual unseal protects only against a stolen disk or backup, at the price of downtime after every reboot until key holders respond; root on the VM already reaches Taskiem's token. A stolen backup alone still opens nothing.
2. **Channels instead of hand-edited tags.** CI moves `staging` on every merge to main; a version tag moves `production` only after approval in the `production` GitHub environment. Servers follow their channel's digest, accept only images signed by this repository's CI workflow, upgrade inside a window, and roll back on a failed migration or an unhealthy release, alerting either way.
3. **The kit travels with the release.** An upgrade installs the Compose file, configuration and `taskiem-vps` from the commit the image was built from, so the stack and the release never drift apart.
4. **Nightly encrypted backups** (restic, to S3-compatible storage) of OpenBao's storage, Taskiem's archive and anchors, and the settings; `taskiem-vps restore` rebuilds on a new VM from a backup and the recovery sheet.
5. **CI rehearses it.** The `vps` job installs on a fresh runner with a real Postgres, restarts OpenBao (auto-unseal), rejects and rolls back a broken release, upgrades and rolls back a good one, and restores from a backup after wiping the deployment.
6. **Taskiem does not renew its OpenBao token.** The VM's `openbao-renew` service does, daily, and alerts on failure. The Helm chart needs the same (or Taskiem renewing its own token) before a Kubernetes deployment.
