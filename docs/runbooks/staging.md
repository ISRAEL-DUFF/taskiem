# Runbook: deploy staging on one VM

Staging runs on one VM with the [VM kit](../vm.md) (`taskiem-vps`), on the **staging** release channel: every merge to main that passes CI publishes a signed image, and the server upgrades itself within 15 minutes, rolling back a release that is not healthy. This runbook takes a fresh VPS to a staging environment where storefronts, forms, field agents and moderation can be tried end to end, over Telegram.

Each step ends with a **check**. Stop at the first check that fails and see [When something fails](#when-something-fails).

`$APP` below is the app's host name (`staging.example.com`) and `$PREVIEW` the storefronts' preview domain (`shops-staging.example.net`).

## 0. What you need

| | Notes |
| --- | --- |
| A VPS | Ubuntu 22.04 or 24.04, or Debian 12, x86_64. 4 vCPU and 8 GB is comfortable; 2 vCPU and 4 GB is the minimum with public pages on (the virus scanner for form files takes about 1.5 GB). 40 GB of disk. A user with `sudo` and SSH key login |
| The provider's firewall | Inbound 22, 80 and 443 open. The kit closes everything else on the server itself |
| Postgres 16 | Managed, with point-in-time recovery. An **empty** database and its owner, who must have `CREATEROLE`. The VPS must be allowed to connect (the provider's allow-list) |
| Two domains | `$APP`, and `$PREVIEW`, a **separate registrable domain**, never a subdomain of the app's (storefront pages must never share the app's cookies). Staging and production need different preview domains |
| SMTP | An account that can send from the sender address you will use (owners, agents and operators get their set-your-password links by email) |
| A Telegram bot | Made with [@BotFather](https://t.me/BotFather) (`/newbot`): its token and its username |
| Backups (optional) | An S3-compatible bucket and an access key limited to it. Without it, nothing survives losing the VPS. Fine to skip for a first staging |
| Optional, for the full Phase 5 flows | A Xenibase instance and an admin token (catalogues and photos), an Anthropic API key (the moderation model and AI building), Termii test credentials (SMS to customers) |

**Never paste secrets into chat or tickets.** Type them into the installer's prompts, or into the env file on the server.

## 1. DNS

Create, at the DNS providers of each domain:

| Record | Points to |
| --- | --- |
| `A $APP` | the VPS's IPv4 address |
| `A *.$PREVIEW` (a wildcard) | the same address |
| `AAAA` for both | only if the VPS has IPv6 and its firewall allows it |

**Check** (from your computer; allow a few minutes after creating them):

```sh
dig +short $APP
dig +short anything.$PREVIEW
```

Both print the VPS's address.

## 2. The server

```sh
ssh <you>@<vps>
```

**Check** on the VPS:

```sh
. /etc/os-release && echo "$PRETTY_NAME $(uname -m)"   # Ubuntu 22.04/24.04 or Debian 12, x86_64
nproc && free -h && df -h /                            # CPUs, memory, disk
sudo -v && echo sudo ok
sudo ss -ltnp | grep -E ':(80|443) ' || echo "80 and 443 are free"
```

Nothing must listen on 80 or 443 yet: Caddy takes them.

**Check** the database is reachable from the VPS (replace the host and port; this tests the network only):

```sh
timeout 5 bash -c '</dev/tcp/<db-host>/<db-port>' && echo reachable
```

## 3. Install

On the VPS:

```sh
curl -fsSL https://raw.githubusercontent.com/ISRAEL-DUFF/taskiem/main/deploy/vm/install.sh | sudo bash
```

Answer the questions:

| Question | Staging answer |
| --- | --- |
| Public host name | `$APP` |
| Owner's email | the first owner (you); gets the set-your-password link |
| Organisation name | e.g. `Taskiem Staging` |
| Postgres URL | `postgres://<owner>:<password>@<host>:<port>/<db>?sslmode=require`, typed at the prompt |
| SMTP URL, sender address | `smtps://user:pass@host:465`; the sender, e.g. `no-reply@$APP` |
| Telegram bot token, username | from @BotFather; the username without `@` |
| Public pages | `yes` |
| Preview domain | `$PREVIEW` |
| Release channel | `staging` |
| Upgrade window | `any` |
| Alert email, webhook | your email; a Slack-compatible webhook or empty |
| Backup destination | the restic repository (`s3:https://host/bucket/taskiem-staging`) and its key, or empty |

It installs Docker and cosign, checks the image's signature, generates every secret, sets up OpenBao, migrates the database, starts everything behind HTTPS, creates the organisation and emails the owner. It takes about five minutes, longer on the first start of the virus scanner (it downloads its signatures).

**Then, at once:** the installer writes a **recovery sheet**, `/root/taskiem-recovery-<date>.txt` (OpenBao's unseal and recovery keys, the anchor key, the backup password). Copy it into your password manager, then:

```sh
sudo shred -u /root/taskiem-recovery-*.txt
```

**Check:**

```sh
sudo taskiem-vps status     # channel staging, a release, every service up, no PROBLEM lines
sudo taskiem-vps check      # "all checks pass"
curl -fsS https://$APP/readyz && echo ready
```

## 4. The owner signs in

Open the email sent to the owner, choose a password, and add a **passkey** when asked (owners are administrators, and administrators are held to passkeys).

**Check:** `https://$APP` shows the dashboard. Under **Account**, link Telegram: open the link it gives, press **Start** in Telegram, and the bot answers that the account is linked.

## 5. Optional settings for the Phase 5 flows

The installer does not ask for these. Add the ones you have to `/opt/taskiem/taskiem.env` (re-running `taskiem-vps setup` keeps them):

```sh
sudo nano /opt/taskiem/taskiem.env
```

| Add | For | Without it |
| --- | --- | --- |
| `TASKIEM_XENIBASE_URL=`, `TASKIEM_XENIBASE_TOKEN=`, `TASKIEM_XENIBASE_ORG=` | Data tables, so storefront catalogues, services and photos | Storefronts work, but without a catalogue or photos |
| `ANTHROPIC_API_KEY=` | The moderation model, and AI building | Moderation uses the word list and price check only |
| `TASKIEM_SMS_TERMII_API_KEY=`, `TASKIEM_SMS_TERMII_SENDER_ID=` | SMS to customers: order texts, form codes | Customers who gave an email still get it |
| `TASKIEM_PUBLIC_ADDRESSES=<the VPS's IPv4>` | The A record shown to a business connecting its own domain | The editor suggests an ALIAS record |
| `TASKIEM_ALLOW_SIGNUP=true` | Self-serve sign-up, to test it | Only invited people join. **Re-running setup sets it back to false** |

Billing stays off on staging unless you are testing plans: every organisation is then on the self-hosted plan, with no storefront limits.

Apply the changes:

```sh
sudo docker compose --project-directory /opt/taskiem up -d --force-recreate taskiem
```

**Check:** `sudo taskiem-vps check` passes, and `sudo taskiem-vps logs taskiem` shows no `ERROR` at start. (With the Xenibase settings, **Tables** in the app lets you create a table.)

## 6. An operator account

Operators review pages and reports, and register field agents, in the operator console at `https://$APP/ops`. Accounts are made from the CLI on the server:

```sh
sudo docker compose --project-directory /opt/taskiem run --rm --no-deps taskiem operators add you@example.com --name "Your Name"
```

It prints a one-time enrolment link, valid 24 hours. Open it, create a passkey, and you are signed in to the console.

**Check:** `https://$APP/ops` shows **Storefront reviews**, **Reports** and **Field agents**.

## 7. Smoke test

Work through these in order. Each is a few minutes.

| # | Do | Expect |
| --- | --- | --- |
| 1 | In the app: **Storefront**, fill in the card, choose an address, **Publish** | "Sent to Taskiem for a quick check" (the business is not verified yet) |
| 2 | In the console: **Storefront reviews**, open it, **Approve** (passkey) | The owner gets "Your page is live" on Telegram; `https://<slug>.$PREVIEW` loads, with a certificate (the first visit takes a few seconds while it is issued) |
| 3 | On the page: **Report this page**, choose a reason, send | The report appears under **Reports** in the console |
| 4 | Send `make my page` to the bot as a second owner (another Telegram account linked to another organisation), answer the questions, publish | The page waits for review, then goes live when approved |
| 5 | In the console: **Field agents**, register yourself (another email) as an agent; set the password from the email; sign in at `https://$APP/agent` | The agent app shows your code |
| 6 | As the agent: **New business**, a test owner's name and **a phone number on a Telegram account you hold** | The consent screen, with the Telegram QR code |
| 7 | On that Telegram account: open the link, **Start**, **Share my number** | The bot says your agent "can now set up the page"; the agent app moves on by itself |
| 8 | As the agent: build the page, **Record visit** (photo and location), **Publish** | It goes live at once (agent onboarded); the owner hears on Telegram |
| 9 | (With Xenibase) add a product with a photo; (with Anthropic) put "Double your money" in the tagline and publish | A product shows; the tagline is refused |

**Check:** `sudo taskiem-vps logs taskiem` shows no `ERROR` from the steps.

## 8. Every day

| Command | Does |
| --- | --- |
| `sudo taskiem-vps status` | Release, last upgrade and backup, services, problems |
| `sudo taskiem-vps update --now` | Upgrade to the newest staging release now |
| `sudo taskiem-vps rollback` | Back to the release before |
| `sudo taskiem-vps logs [taskiem\|taskiem-public\|caddy\|openbao\|clamav]` | Follow logs |
| `sudo taskiem-vps owner-reset` | Email the owner a new set-your-password link |
| `sudo taskiem-vps backup` | Back up now |

A merge to main reaches staging within 15 minutes of CI's `image` job. `taskiem-vps status` shows the commit it runs.

## When something fails

| Symptom | Look at |
| --- | --- |
| The installer stops at the database | The URL (user, password, `sslmode`), the provider's allow-list for the VPS's address, and that the user has `CREATEROLE` |
| No certificate for `$APP` | DNS (step 1) and ports 80 and 443 at the provider's firewall; `sudo taskiem-vps logs caddy` |
| No certificate for a storefront | The wildcard record; the page must be published (certificates are issued only for hosts Taskiem serves); `sudo taskiem-vps logs taskiem-public` |
| The bot does not answer | `sudo taskiem-vps logs taskiem` for the webhook registration at start; the token and username in `/opt/taskiem/taskiem.env` |
| Emails do not arrive | The SMTP URL and sender; the spam folder; `sudo taskiem-vps logs taskiem` |
| A release did not come | CI's `image` job on main; `sudo taskiem-vps status`; a release that failed its health check is rolled back and not retried (the alert says why) |
| Form files wait | The virus scanner's first start downloads its signatures; `sudo taskiem-vps logs clamav` |

## Starting over

To rebuild staging from nothing: drop and recreate the database (empty, same owner), then on the VPS remove the kit and its volumes and run the installer again.

```sh
sudo systemctl disable --now taskiem-vps-update.timer taskiem-vps-check.timer taskiem-vps-backup.timer
sudo docker compose --project-directory /opt/taskiem down -v
sudo rm -rf /opt/taskiem /etc/taskiem /var/lib/taskiem-vps
```

This destroys staging's secrets: never run it on production.
