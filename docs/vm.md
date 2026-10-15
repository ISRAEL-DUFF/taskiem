# Running Taskiem on one VM

[Decision 0032](decisions/0032-deployment-target.md) runs staging and the first production environment on one VM until a trigger moves them to [Kubernetes](kubernetes.md). Installing, upgrading, backing up and watching that VM is automated by `taskiem-vps` (in `deploy/vm`): you answer a few questions once, and the server then keeps itself on the newest signed release of its channel, rolls back a release that fails, backs itself up every night and alerts you when something is wrong.

What runs: Taskiem with every role in one process, OpenBao (auto-unsealed) for the key that wraps every secret, and Caddy for HTTPS. Postgres is a managed service, outside the VM. Container steps are off (their only safe runner is Kubernetes).

## Before you start

| You need | Notes |
| --- | --- |
| A VPS | Ubuntu 22.04 or 24.04 (or Debian 12), x86_64, hosted in Nigeria (P4-C1). At least 2 vCPU and 4 GiB; 4 vCPU and 8 GiB is comfortable |
| A DNS name | An A record pointing at the VPS, so Caddy can get a certificate |
| A Postgres 16 database | Managed, with point-in-time recovery turned on. An empty database and its owner, who must have `CREATEROLE` (managed services' admin users usually do) |
| SMTP | For the owner's set-your-password link, invitations, password resets and alerts |
| Backup storage | Any S3-compatible bucket (restic repository), with an access key limited to it. Optional, but without it nothing on the VM survives losing the VM |
| Once per repository | In GitHub: make the `taskiem` package public (Packages → taskiem → Package settings), and create the `production` environment with required reviewers (Settings → Environments). See [Releases](#releases) |

## Install

On the VPS:

```sh
curl -fsSL https://raw.githubusercontent.com/ISRAEL-DUFF/taskiem/main/deploy/vm/install.sh | sudo bash
```

It asks for the host name, the owner's email, the organisation's name, the database URL, SMTP, the release channel (staging or production), the upgrade window, where to send alerts, and the backup bucket. Then, without further questions, it:

1. installs Docker, cosign and the firewall rules (SSH, HTTP and HTTPS in; nothing else);
2. picks the newest release on the channel and checks its signature: only images signed by this repository's CI are accepted;
3. generates every secret itself (OpenBao's unseal key, the audit-anchor key, the database password, the backup password);
4. initialises OpenBao, creates Taskiem's transit key and a token limited to it, and revokes the root token;
5. migrates the database and creates the restricted role Taskiem signs in as;
6. starts everything behind HTTPS, creates the organisation and its owner, and emails the owner a link to set their password (the owner then adds a passkey: administrators are held to passkeys);
7. initialises the backup repository and turns on the timers: updates every 15 minutes, health checks every 5, a backup every night at 02:30.

It ends by writing a **recovery sheet** to `/root/taskiem-recovery-<date>.txt`: OpenBao's unseal and recovery keys, the anchor key and the backup password. Put it in two offline places, then delete it (`shred -u`); the health checks remind you until you do. Without the unseal key, a backup of OpenBao cannot be opened.

Re-run `sudo taskiem-vps setup` any time to change an answer; it keeps every secret and skips what is done.

**Unattended install.** Every answer can come from the environment instead (`sudo -E`): `TASKIEM_VPS_DOMAIN`, `_OWNER_EMAIL`, `_ORG`, `_DATABASE_URL`, `_SMTP_URL`, `_MAIL_FROM`, `_CHANNEL`, `_WINDOW` (`any` or `01:00-05:00`), `_ALERT_EMAIL`, `_ALERT_WEBHOOK`, `_BACKUP_REPO`, `_BACKUP_KEY_ID`, `_BACKUP_SECRET`, with `TASKIEM_VPS_NONINTERACTIVE=1`.

## Releases

CI publishes a signed image for every merge to main and every version tag. Servers follow a channel:

| Channel | Moves when | Upgrades |
| --- | --- | --- |
| `staging` | Every merge to main that passes CI | Within 15 minutes, at any time unless a window is set |
| `production` | A version tag (`git tag v1.2.0 && git push --tags`) is **approved** in the `production` environment on GitHub | In the server's upgrade window (default 01:00–05:00 Lagos time) |

An upgrade checks the signature, fetches the matching deployment kit, runs the migration, starts the new release and waits for it to be healthy behind HTTPS. If the migration fails or the release is not healthy within four minutes, the server **rolls back** to the release it was on, alerts you, and does not try that release again. Every release works with the schema of the one before it, so rolling back is always safe.

An upgrade stops Taskiem for about 30 seconds while workers drain, so keep payroll-heavy hours outside the window. Webhooks sent in that gap are retried by their senders.

## Every day

| Command | Does |
| --- | --- |
| `sudo taskiem-vps status` | Channel, release and commit, last upgrade and backup, services, problems |
| `sudo taskiem-vps update --now` | Upgrade now, outside the window |
| `sudo taskiem-vps rollback` | Back to the release before the current one |
| `sudo taskiem-vps backup` | Back up now |
| `sudo taskiem-vps check` | Run the health checks |
| `sudo taskiem-vps logs [service]` | Follow logs (`taskiem`, `openbao`, `caddy`, `openbao-renew`) |
| `sudo taskiem-vps owner-reset` | Email the owner a new set-your-password link |

## Alerts

`taskiem-vps check` runs every 5 minutes and sends an email (through Taskiem's SMTP) and a Slack-compatible webhook when the set of problems changes, and again when it clears:

- Taskiem unhealthy, or `https://<domain>/readyz` not answering;
- OpenBao sealed or down, or its token renewal failing (Taskiem never renews its own OpenBao token; the `openbao-renew` service does, daily);
- a disk 85% full;
- the certificate expiring within 14 days;
- no successful backup for 36 hours;
- a release that failed to upgrade and was rolled back;
- the recovery sheet still on the server.

Each upgrade, successful or rolled back, is reported too.

## Backups and restore

Every night, encrypted with restic into the bucket: OpenBao's storage, Taskiem's archive and audit anchors, and the server's settings, kept 14 daily, 8 weekly and 12 monthly. The unseal key is left out on purpose: it is on the recovery sheet. The database is backed up by its provider (point-in-time recovery).

**To rebuild on a new VPS** (the old one is gone): restore the database first if it was lost too, then on the new VPS

```sh
curl -fsSL https://raw.githubusercontent.com/ISRAEL-DUFF/taskiem/main/deploy/vm/install.sh | sudo TASKIEM_VPS_RESTORE=1 bash
```

It asks for the bucket and its key, and from the recovery sheet the backup password and the unseal key; restores the newest backup; starts the release that was running; and turns the timers back on. Point the DNS name at the new VPS. CI rehearses this on every change.

## Security notes

- Auto-unseal keeps OpenBao's unseal key on the VPS, in a root-only file outside the backups. Someone with root on the VPS can read every secret, as they could anyway through Taskiem's own token; a stolen backup alone cannot.
- Ports 8200 (OpenBao) and 9090 (metrics, on `127.0.0.1`) are never public. Docker publishes only 80 and 443.
- The database user Taskiem signs in as is a member of `taskiem_app` only; row-level security applies to it ([operations](operations.md#database-roles)). The owner's URL is used for migrations only.
- Images are accepted only with a cosign signature from this repository's CI workflow on main or a version tag.

## What is in deploy/vm

| File | Is |
| --- | --- |
| `install.sh` | The one-line installer |
| `taskiem-vps` | Setup, update, rollback, backup, restore, checks (installed to `/usr/local/bin`) |
| `docker-compose.yml`, `Caddyfile`, `openbao.hcl`, `openbao-taskiem-policy.hcl` | The stack, installed to `/opt/taskiem` and replaced on each upgrade with the release's own copy |
| `systemd/` | The update, check and backup timers |

The server's settings are in `/opt/taskiem/*.env` and `/etc/taskiem` (root only); its state (current and previous release, last backup) is in `/var/lib/taskiem-vps`.
