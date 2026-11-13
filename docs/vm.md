# Running Taskiem on one VM

[Decision 0032](decisions/0032-deployment-target.md) runs staging and the first production environment on one VM until a trigger moves them to [Kubernetes](kubernetes.md). Installing, upgrading, backing up and watching that VM is automated by `taskiem-vps` (in `deploy/vm`): you answer a few questions once, and the server then keeps itself on the newest signed release of its channel, rolls back a release that fails, backs itself up every night and alerts you when something is wrong.

What runs: Taskiem with every role in one process, OpenBao (auto-unsealed) for the key that wraps every secret, and Caddy for HTTPS. Postgres is a managed service, outside the VM. Container steps are off (their only safe runner is Kubernetes).

Taskiem's own staging is set up with this kit step by step in the [staging runbook](runbooks/staging.md).

## Before you start

| You need | Notes |
| --- | --- |
| A VPS | Ubuntu 22.04 or 24.04 (or Debian 12), x86_64, hosted in Nigeria (P4-C1). At least 2 vCPU and 4 GiB; 4 vCPU and 8 GiB is comfortable, and is what to use with public pages or mail on (their malware scanner alone may take 2.5 GiB) |
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

It asks for the host name, the owner's email, the organisation's name, the database URL, SMTP, a [Telegram](telegram.md) bot token if you want the chat channel (it generates the webhook secret and signing key itself), the iswallet and Payrolla live API URLs (empty keeps each provider's production address; connections are live or test, chosen when made), the release channel (staging or production), the upgrade window, where to send alerts, and the backup bucket. Then, without further questions, it:

1. installs Docker, cosign and the firewall rules (SSH, HTTP and HTTPS in; nothing else);
2. picks the newest release on the channel and checks its signature: only images signed by this repository's CI are accepted;
3. generates every secret itself (OpenBao's unseal key, the audit-anchor key, the database password, the backup password);
4. initialises OpenBao, creates Taskiem's transit key and a token limited to it, and revokes the root token;
5. migrates the database and creates the restricted role Taskiem signs in as;
6. starts everything behind HTTPS, creates the organisation and its owner, and emails the owner a link to set their password (the owner may add a passkey under Account, and may require passkeys for administrators under Settings > Security);
7. initialises the backup repository and turns on the timers: updates every 15 minutes, health checks every 5, a backup every night at 02:30.

It ends by writing a **recovery sheet** to `/root/taskiem-recovery-<date>.txt`: OpenBao's unseal and recovery keys, the anchor key and the backup password. Put it in two offline places, then delete it (`shred -u`); the health checks remind you until you do. Without the unseal key, a backup of OpenBao cannot be opened.

Re-run `sudo taskiem-vps setup` any time to change an answer; it keeps every secret and skips what is done.

**What the installer trusts.** It runs as root, so it checks what it installs:

- Docker comes from Docker's own apt repository, as Docker's documentation installs it: the repository key must have Docker's fingerprint, and apt checks every package against it. (Not the `get.docker.com` script.)
- cosign is checked against its published checksum.
- <a id="the-installers-pins"></a>The deployment kit is the tarball of the release named in `install.sh` (`KIT_REF`, a version tag) and must match the checksum beside it (`KIT_SHA256`); a kit that does not match is not unpacked. Cutting a release fills both in: `KIT_REF=v1.2.0` and `KIT_SHA256=$(curl -fsSL https://codeload.github.com/ISRAEL-DUFF/taskiem/tar.gz/v1.2.0 | sha256sum | cut -d' ' -f1)`. **Until the first release is pinned they are empty, and the installer takes the kit from the main branch unverified, with a warning.** To install another ref, give `TASKIEM_VPS_REF` and `TASKIEM_VPS_KIT_SHA256` together.
- `taskiem-vps setup` then replaces the kit with the one from the commit of the signed release it installs.

The three timers' units have a time limit (update 45 minutes, backup 6 hours, check 4 minutes), so a hung Docker call cannot hold the update lock for ever.

**Unattended install.** Every answer can come from the environment instead (`sudo -E`): `TASKIEM_VPS_DOMAIN`, `_OWNER_EMAIL`, `_ORG`, `_DATABASE_URL`, `_SMTP_URL`, `_MAIL_FROM`, `_CHANNEL`, `_WINDOW` (`any` or `01:00-05:00`), `_ALERT_EMAIL`, `_ALERT_WEBHOOK`, `_BACKUP_REPO`, `_BACKUP_KEY_ID`, `_BACKUP_SECRET`, `_TELEGRAM_BOT_TOKEN`, `_TELEGRAM_BOT_USERNAME`, `_ISWALLET_URL`, `_PAYROLLA_URL`, `_PUBLIC`, `_PREVIEW_DOMAIN`, `_MAIL_DOMAIN`, with `TASKIEM_VPS_NONINTERACTIVE=1`.

With no SMTP there is no email to carry the owner's set-your-password link. At a terminal the first password is shown once; on an unattended install it is written to `/root/taskiem-owner-password-<date>.txt`, readable by root only, and only the file's path is printed, so the password is not in cloud-init or CI logs. Give it to the owner, then delete the file (`shred -u`); the health checks remind you until you do.

## Releases

CI publishes a signed image for every merge to main and every version tag. Servers follow a channel:

| Channel | Moves when | Upgrades |
| --- | --- | --- |
| `staging` | Every merge to main that passes CI | Within 15 minutes, at any time unless a window is set |
| `production` | A version tag (`git tag v1.2.0 && git push --tags`) is **approved** in the `production` environment on GitHub | In the server's upgrade window (default 01:00–05:00 Lagos time) |

An upgrade checks the signature, fetches the matching deployment kit, runs the migration, starts the new release and waits for it to be healthy behind HTTPS. If the migration fails or the release is not healthy within four minutes, the server **rolls back** to the release it was on (its image and its deployment kit), alerts you, and does not try that release again until a newer one is published (`taskiem-vps update --now` retries it, for example after fixing a setting it needed). Every release works with the schema of the one before it, so rolling back is always safe.

An upgrade stops Taskiem for about 30 seconds while workers drain, so keep payroll-heavy hours outside the window. Webhooks sent in that gap are retried by their senders.

## Every day

| Command | Does |
| --- | --- |
| `sudo taskiem-vps status` | Channel, release and commit, last upgrade and backup, services, problems |
| `sudo taskiem-vps update --now` | Upgrade now, outside the window, even to a release that failed and was rolled back |
| `sudo taskiem-vps rollback` | Back to the release before the current one, with its deployment kit |
| `sudo taskiem-vps backup` | Back up now |
| `sudo taskiem-vps check` | Run the health checks |
| `sudo taskiem-vps logs [service]` | Follow logs (`taskiem`, `openbao`, `caddy`, `openbao-renew`; `taskiem-public`, `taskiem-mail` and `clamav` when on) |
| `sudo taskiem-vps owner-reset` | Email the owner a new set-your-password link |


The backup and updates never run at the same time. The backup stops OpenBao for the few seconds it takes to copy its storage, and an update restarts everything, so both take one lock (`/var/lib/taskiem-vps/update.lock`): the backup waits up to 30 minutes for a running update, and an update that finds the backup running tries again at the next tick. The copy is compared with the original, file by file, before OpenBao starts again; a copy that differs is not stored and alerts as a failed backup.

## Alerts

`taskiem-vps check` runs every 5 minutes and sends an email (through Taskiem's SMTP) and a Slack-compatible webhook when the set of problems changes, and again when it clears. An alert that neither channel took is sent again by every check until one does, and is written to the journal as an error (`journalctl -t taskiem-vps -p err`); the problems are:

- Taskiem unhealthy, or `https://<domain>/readyz` not answering;
- OpenBao sealed or down, or its token renewal failing (Taskiem never renews its own OpenBao token; the `openbao-renew` service does, daily);
- a disk 85% full;
- the certificate expiring within 14 days;
- no successful backup for 36 hours;
- a release that failed to upgrade and was rolled back;
- on the staging channel, main ahead of the running release by a merge more than a day old (CI did not tag the release, or the server did not take it);
- the recovery sheet still on the server;
- the public host or the mail server unhealthy, when they are on.

Each upgrade, successful or rolled back, is reported too.

## Backups and restore

Every night, encrypted with restic into the bucket: OpenBao's storage, Taskiem's archive and audit anchors, and the server's settings, kept 14 daily, 8 weekly and 12 monthly. The unseal key is left out on purpose: it is on the recovery sheet. The database is backed up by its provider (point-in-time recovery). OpenBao keeps its keys in an integrated (Raft) database, which is copied with OpenBao stopped for a few seconds so the copy is consistent; anything needing a secret in those seconds waits for its retry.

**To rebuild on a new VPS** (the old one is gone): restore the database first if it was lost too, then on the new VPS

```sh
curl -fsSL https://raw.githubusercontent.com/ISRAEL-DUFF/taskiem/main/deploy/vm/install.sh | sudo TASKIEM_VPS_RESTORE=1 bash
```

It asks for the bucket and its key, and from the recovery sheet the backup password and the unseal key; restores the newest backup; starts the release that was running; and turns the timers back on. Point the DNS name at the new VPS. CI rehearses this on every change.

## Public pages: storefronts and forms

The [public host](public-host.md) serves storefronts and hosted forms (Phase 5). It is off until they ship. To turn it on, answer **yes** to "Public pages" in `taskiem-vps setup` and give a **preview domain** such as `shops.example.com`.

**What setup does:**
- It creates a second database login, `<database>_public`, a member of `taskiem_public` only, which can read no table.
- It writes `/opt/taskiem/public.env` holding that login alone: no OpenBao token and no other secret.
- It starts `taskiem-public` (`--role public`; metrics on `127.0.0.1:9091`) and puts `public.caddy` in `/opt/taskiem/caddy.d/` so Caddy routes it.
- It starts `clamav`, the malware scanner for files sent in [forms](forms.md#files), and points Taskiem at it (`TASKIEM_CLAMD_ADDR=clamav:3310`). It is reachable only on the Compose network, keeps its signatures on the `clamav_db` volume and updates them itself from the ClamAV mirrors. It needs about **1.5 GB of memory**, and up to 2.5 GB while it reloads its signatures, which is its limit. Every service has a memory and a process limit (Taskiem 2 GiB, the scanner 2.5 GiB, OpenBao 512 MiB, the public pages, mail and Caddy 256 MiB each), about 6 GiB in all: use an 8 GiB server with public pages on. A 4 GB server can run short; the scanner is then the first thing stopped and OpenBao the last, and form files wait and are tried again. Its first start downloads the signatures (a few minutes); until then, form files wait and are tried again.

Answering **no** later stops the service, removes the route and disables the login.

**DNS:** point `*.shops.example.com` (a wildcard A or AAAA record) at the server. Businesses' own domains are pointed at it too, by the domain platform or by the business (S6).

**Certificates, on demand:**
- Every host that is not the app's goes to the public host, and Caddy gets its certificate from Let's Encrypt the first time it is visited.
- Before asking, Caddy checks with the public host (`http://taskiem-public:9090/tls/ask`, on the Compose network only): a certificate is requested only for a host Taskiem serves.
- Any other name fails the TLS handshake, so nobody can make the server request certificates for names it does not serve, or run it into Let's Encrypt's limits.

**Let's Encrypt's limits apply to preview addresses:** at most 50 new certificates a week for one registered domain, and each `<slug>.shops.example.com` takes one. Beyond about 50 new storefronts a week:
- use a wildcard certificate for `*.shops.example.com`, which needs a DNS challenge, so a Caddy build with the DNS provider's module; or
- move to Kubernetes, where the [Helm chart's](kubernetes.md) public component sits behind the cluster's ingress (decision 0032's trigger).

Businesses' own domains each count against their own registered domain, not the preview domain's.

`taskiem-vps check` alerts when the public host is unhealthy: existing pages may still be served from Caddy's certificates, but new hosts get none.

## Email triggers

[Email triggers](email.md) start a workflow when mail arrives at its own address, `<id>@<mail domain>`. The server receives that mail itself on port 25. Without a mail domain, email triggers cannot be published, and everything else works as before.

**To turn it on**, at install or at any time later:

1. Pick a mail domain under one you own, such as `in.example.com`. It must differ from the app's host name.
2. At its DNS provider, add an **MX record** for it pointing at the server's host name (the A record the app already has): `in.example.com. MX 10 app.example.com.`
3. Open **inbound TCP 25** in the hosting provider's firewall. Some providers block port 25 until asked, usually only outbound, which this does not need. The server itself needs nothing: Docker publishes the port.
4. Answer "Mail domain for email triggers" in `sudo taskiem-vps setup` with it. On a running server, re-run setup and press Enter to keep every other answer.

**What setup does:**
- It writes `/opt/taskiem/mail.env`: the public host's database login (`<database>_public`, a member of `taskiem_public` only), the public key and the mail domain, and nothing else of the platform's.
- It starts `taskiem-mail` (`--role mail`) on port 25, with metrics on `127.0.0.1:9092`. It greets as the server's host name and offers STARTTLS with a certificate of its own.
- It sets `TASKIEM_MAIL_DOMAIN` in `taskiem.env`, so the app shows addresses and takes the mail the mail server leaves.
- It starts `clamav`, if public pages have not already, so workflows can keep attachments. It needs about 1.5 GB of memory, as for forms.

Emptying the answer later stops `taskiem-mail`. Published email triggers then refuse mail, and senders retry for a few days.

`taskiem-vps check` alerts when the mail server is unhealthy. Senders retry for days, so mail is delayed, not lost, while it is down.

**Upgrading a server from before email triggers:** the upgrade itself (automatic, or `taskiem-vps update --now`) adds the database tables and the `taskiem-mail` service but turns nothing on. Do the four steps above when you want it.

## Security notes

- Auto-unseal keeps OpenBao's unseal key on the VPS, in a root-only file outside the backups. Someone with root on the VPS can read every secret, as they could anyway through Taskiem's own token; a stolen backup alone cannot.
- Ports 8200 (OpenBao) and 9090 (metrics, on `127.0.0.1`) are never public. Docker publishes only 80 and 443, and 25 when email triggers are on.
- The mail server takes mail only for addresses of live email triggers, refusing everything else before the message is sent: it is not a relay, and it sends nothing.
- The public host and the mail server sign in as a separate user, a member of `taskiem_public` only, which reads no table; their env files hold nothing else.
- The database user Taskiem signs in as is a member of `taskiem_app` only; row-level security applies to it ([operations](operations.md#database-roles)). The owner's URL is used for migrations only.
- Images are accepted only with a cosign signature from this repository's CI workflow on main or a version tag. cosign checks it against Sigstore's signed trust metadata, taken from the first source that answers: Sigstore's CDN, the same repository on GitHub Pages (`sigstore.github.io/root-signing`), or the trusted root saved under `/var/lib/taskiem-vps` after the last check that got through. A refused signature is final whichever source gave the verdict; only a source that cannot be fetched moves on to the next. (`TASKIEM_VPS_TUF_SOURCES` overrides the list.)
- **A signature is not enough.** Every build of main is signed the same way, so the signature alone would let whoever can move the `production` tag in the registry point servers at an unreleased build or an old release. When CI moves a channel it also signs a statement (a cosign attestation, keyless like the signature) naming the channel and a release number that only grows. A server installs an image only if it carries that statement for its own channel (for production, signed by a version tag's run, after the approval) and its number is above the one running. Anything else is refused, alerted once and not tried again by the timer. To go back to an older release on purpose, a person runs `taskiem-vps update --now --allow-downgrade`; it is logged to the journal with who ran it. `taskiem-vps rollback` (to the release just before) needs no flag.

## What is in deploy/vm

| File | Is |
| --- | --- |
| `install.sh` | The one-line installer |
| `taskiem-vps` | Setup, update, rollback, backup, restore, checks (installed to `/usr/local/bin`) |
| `docker-compose.yml`, `Caddyfile`, `public.caddy`, `openbao.hcl`, `openbao-taskiem-policy.hcl` | The stack, installed to `/opt/taskiem` and replaced on each upgrade with the release's own copy (`public.caddy` is used only with public pages on). Its third-party images (Caddy, OpenBao, ClamAV), like the helpers `taskiem-vps` runs (psql, restic), are pinned by digest, so a setup or pull gets the image the release was reviewed with; `CLAMAV_IMAGE` and `OPENBAO_IMAGE` in `/opt/taskiem/.env` override them |
| `systemd/` | The update, check and backup timers |

The server's settings are in `/opt/taskiem/*.env` and `/etc/taskiem` (root only); its state (current and previous release, last backup) is in `/var/lib/taskiem-vps`.
