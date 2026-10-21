# Runbook: deploy staging on one VM

Staging runs on one VM with the [VM kit](../vm.md) (`taskiem-vps`), on the **staging** release channel: every merge to main that passes CI publishes a signed image, and the server upgrades itself within 15 minutes, rolling back a release that is not healthy. This runbook takes a fresh VPS to a staging environment where storefronts, forms, field agents and moderation can be tried end to end, over Telegram.

Each step ends with a **check**. Stop at the first check that fails and see [When something fails](#when-something-fails).

`$APP` below is the app's host name (`staging.example.com`), `$PREVIEW` the storefronts' preview domain (`shops-staging.example.net`) and `$MAIL` the mail domain for email triggers (`in.staging.example.com`).

## 0. What you need

| | Notes |
| --- | --- |
| A VPS | Ubuntu 22.04 or 24.04, or Debian 12, x86_64. 4 vCPU and 8 GB is comfortable; 2 vCPU and 4 GB is the minimum with public pages on (the virus scanner for form files takes about 1.5 GB). 40 GB of disk. A user with `sudo` and SSH key login |
| The provider's firewall | Inbound 22, 80 and 443 open, and 25 for [email triggers](#9-email-triggers). The kit closes everything else on the server itself |
| Postgres 16 or newer | A **dedicated** server (managed, with point-in-time recovery), not a shared platform's project database: Taskiem's migrations create roles, which belong to the whole server, so the owner needs `CREATEROLE`, and two environments cannot share one server. An **empty** database, **not named `taskiem`** (`taskiem_staging`), and its owner. The VPS must be allowed to connect. For staging only, Postgres can run on the VPS itself: see [A database on the VPS](#a-database-on-the-vps-staging-only). Tested on 16 and 18 |
| A mail domain (optional) | `$MAIL`, for [email triggers](#9-email-triggers): a subdomain of one you own, whose MX record you can set. Can be added later |
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
| `MX $MAIL`, priority 10 (optional) | `$APP`: mail for email triggers comes to the same server |

**Check** (from your computer; allow a few minutes after creating them):

```sh
dig +short $APP
dig +short anything.$PREVIEW
```

Both print the VPS's address. With a mail domain, `dig +short MX $MAIL` prints `10 $APP.`

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

## A database on the VPS (staging only)

Instead of a managed database, staging can run Postgres beside Taskiem, from [`deploy/vm/staging-postgres.yaml`](../../deploy/vm/staging-postgres.yaml). It is not backed up (the kit's backups assume the provider's), so keep only test data in it.

```sh
sudo mkdir -p /opt/taskiem-postgres
echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)" | sudo tee /opt/taskiem-postgres/.env >/dev/null && sudo chmod 600 /opt/taskiem-postgres/.env
sudo curl -fsSL -o /opt/taskiem-postgres/compose.yaml https://raw.githubusercontent.com/ISRAEL-DUFF/taskiem/main/deploy/vm/staging-postgres.yaml
sudo docker compose --project-directory /opt/taskiem-postgres up -d
```

It runs on the host's network. Once the installer has turned on the firewall (step 3), let Docker's private networks reach it, and nothing else:

```sh
sudo ufw allow from 172.16.0.0/12 to any port 5432 proto tcp
sudo ufw allow from 192.168.0.0/16 to any port 5432 proto tcp
```

**Check:** `sudo docker compose --project-directory /opt/taskiem-postgres ps` shows it `healthy`.

In step 3, give the installer the URL from the `.env` instead of typing one:

```sh
sudo sh -c '. /opt/taskiem-postgres/.env && TASKIEM_VPS_DATABASE_URL="postgres://taskiem_owner:$POSTGRES_PASSWORD@172.17.0.1:5432/taskiem_staging?sslmode=disable" taskiem-vps setup'
```

(On a fresh server, run the installer first, which installs Docker, the firewall and `taskiem-vps`, and let it stop at the database question with Ctrl-C; then start Postgres as above, and run `taskiem-vps setup` like this.)

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
| Mail domain for email triggers | `$MAIL`, or empty to leave email triggers off (see [step 9](#9-email-triggers)) |
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
| `TASKIEM_OAUTH_GOOGLESHEETS_CLIENT_ID=`, `TASKIEM_OAUTH_GOOGLESHEETS_CLIENT_SECRET=` (and `TASKIEM_OAUTH_GMAIL_*`) | **Connect Google Sheets** (and Gmail) in one click. Register the app as [Google Sheets](../integrations/googlesheets.md#one-click-taskiems-google-app) says, with the redirect URI `https://staging.<domain>/v1/connections/oauth/callback`, and add each tester's Google account under the consent screen's **Test users** | Google connections are made by pasting a service account key |
| `TASKIEM_AI_MODERATION_MODEL=` | A cheaper model for storefront moderation than for building | Moderation uses the building model |

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
| `sudo taskiem-vps logs [taskiem\|taskiem-public\|taskiem-mail\|caddy\|openbao\|clamav]` | Follow logs |
| `sudo taskiem-vps owner-reset` | Email the owner a new set-your-password link |
| `sudo taskiem-vps backup` | Back up now |

A merge to main reaches staging within 15 minutes of CI's `image` job. `taskiem-vps status` shows the commit it runs.

## 9. Email triggers

[Email triggers](../email.md) give a workflow its own address, `<id>@$MAIL`; mail sent or forwarded there starts it. The server receives that mail itself on port 25. Releases bring the feature without turning it on: until a mail domain is set, email triggers cannot be published.

### On a new server

Answer the installer's "Mail domain" question with `$MAIL` (step 3), after creating the MX record (step 1) and opening port 25 (step 0). Then go to the checks below.

### On a server installed before email triggers

1. **Upgrade.** Staging upgrades itself within 15 minutes of the merge; `sudo taskiem-vps update --now` does it at once. The upgrade migrates the database and brings the `taskiem-mail` service and the setup question, but turns nothing on. **Check:** `sudo taskiem-vps status` shows the new commit, with no PROBLEM lines.
2. **DNS.** Add `MX $MAIL` → `$APP`, priority 10, at `$MAIL`'s DNS provider. **Check** from your computer: `dig +short MX $MAIL` prints `10 $APP.`
3. **Port 25.** Allow inbound TCP 25 in the hosting provider's firewall or security group. Some providers block port 25 for new accounts: they usually mean outbound, which is not needed, but ask if the check in step 5 fails. Nothing is needed on the server itself.
4. **Setup.** Run `sudo taskiem-vps setup`. Press Enter at every question to keep its answer, and give `$MAIL` at "Mail domain for email triggers". It writes `/opt/taskiem/mail.env`, sets the domain for the app, starts `taskiem-mail` (and `clamav`, if public pages are off) and restarts what changed. Every secret is kept.
5. **Check** on the server:

   ```sh
   sudo taskiem-vps status     # taskiem-mail running and healthy
   sudo taskiem-vps check      # "all checks pass"
   sudo grep MAIL /opt/taskiem/mail.env /opt/taskiem/taskiem.env   # TASKIEM_MAIL_DOMAIN in both
   ```

   And from your computer (not the server), that the internet reaches it:

   ```sh
   nc -vz $APP 25                        # succeeded / open
   printf 'QUIT\r\n' | nc $APP 25         # 220 $APP ESMTP …
   ```

### Try it

| # | Do | Expect |
| --- | --- | --- |
| 1 | In the app: **Workflows**, a workflow (or a new one), **When it runs** → **When an email comes in**, **Publish** | **Its email address** shows `<id>@$MAIL`, **Waiting for your first email…** |
| 2 | **Try it with a sample email** | A run opens, completed |
| 3 | From your own mailbox, send an email to the address | Within a minute it is listed under **What arrived**, **Started a run** |
| 4 | In Gmail: **Settings → Forwarding and POP/IMAP → Add a forwarding address**, the address | The confirmation code appears on the workflow's page, and the owner gets it on Telegram; enter it in Gmail |
| 5 | Send yourself mail from someone not on the workflow's **Only take mail from** list (if it has one) | Listed as **Not started**, with the reason |

**Check:** `sudo taskiem-vps logs taskiem-mail` shows `mail: received`, and `sudo taskiem-vps logs taskiem` shows no `ERROR` for email.

To turn it off again: re-run setup and clear the mail domain. Published email triggers then refuse mail; senders retry for a few days.

## 10. Updates for people outside a business

[Updates](../updates.md) let a business's staff and customers, who never sign in, get its workflows' messages on Telegram or WhatsApp with one tap, instead of by text. The feature needs no setup of its own. It uses the Telegram bot and WhatsApp number the server already has, `TASKIEM_MESSAGE_HASH_KEY` (the installer always sets it) and the public key (set when public pages are on).

### On a new or upgraded server

1. **Upgrade** as for any release (`sudo taskiem-vps update --now`, or wait for the automatic one). The migration adds the contacts tables. **Check:** `sudo taskiem-vps status` shows the new commit.
2. **Telegram:** nothing to do when the bot is set (step 3). **Check:** in the app, **Settings > People you message** shows a link, and the text says people can use Telegram.
3. **WhatsApp**, once the Business account is approved: set the platform number's settings ([whatsapp](../whatsapp.md#for-operators-setting-up-the-platform-number)), including `TASKIEM_WHATSAPP_DISPLAY_NUMBER=+234…`. Update links point at that number. Submit the `taskiem_update` template with the others ([templates](../whatsapp.md#templates)). Until Meta approves it, WhatsApp updates go only to people who wrote in the last 24 hours, and the rest by text. **Check:** **Settings > People you message** mentions WhatsApp.

### Try it

| # | Do | Expect |
| --- | --- | --- |
| 1 | **Templates** → **Leave requests from your staff**, accept it, make the leave table if asked, publish | The workflow is live; its form has a link |
| 2 | On your phone, open the form's link, fill it in with your own number, enter the code | The thank-you page shows **Get updates on Telegram** (and WhatsApp, when on) |
| 3 | Tap **Get updates on Telegram**, press **Start** | The bot: "You'll get updates from <your business> here" |
| 4 | In the app, approve the request | Within a minute, the decision arrives in Telegram, not by text |
| 5 | Send `STOP` to the bot, then submit and approve another request | The bot confirms the stop; the next decision comes by text |
| 6 | **Settings > People you message** | Your number, masked, with Telegram (stopped); **Remove** removes it |

**Check:** `sudo taskiem-vps logs taskiem` shows no `ERROR` mentioning `reach` or `whatsapp: send failed` for these messages.

## When something fails

| Symptom | Look at |
| --- | --- |
| The installer stops at the database | The URL (user, password, `sslmode`), the provider's allow-list for the VPS's address |
| `permission denied to create role` | The user lacks `CREATEROLE`: a shared platform's project database cannot host Taskiem; use a dedicated server, or Postgres on the VPS for staging |
| `role "taskiem_app" is a member of role "taskiem_app"` | The database was named `taskiem` (kits before the guard did not refuse it). Its roles are now wrong: start the database over with another name |
| No certificate for `$APP` | DNS (step 1) and ports 80 and 443 at the provider's firewall; `sudo taskiem-vps logs caddy` |
| No certificate for a storefront | The wildcard record; the page must be published (certificates are issued only for hosts Taskiem serves); `sudo taskiem-vps logs taskiem-public` |
| The bot does not answer | `sudo taskiem-vps logs taskiem` for the webhook registration at start; the token and username in `/opt/taskiem/taskiem.env` |
| Emails do not arrive | The SMTP URL and sender; the spam folder; `sudo taskiem-vps logs taskiem` |
| A release did not come | CI's `image` job on main; `sudo taskiem-vps status`; a release that failed its health check is rolled back and not retried (the alert says why) |
| Form files wait | The virus scanner's first start downloads its signatures; `sudo taskiem-vps logs clamav` |
| Mail to a workflow never arrives | `dig MX $MAIL` (it must name `$APP`); `nc -vz $APP 25` from outside (the provider's firewall); `sudo taskiem-vps logs taskiem-mail`. A sender told `550 No such address here` used an address that is not a published workflow's, or one replaced by **Get a new address** |
| Publishing an email workflow says this Taskiem cannot receive email | No mail domain: [step 9](#9-email-triggers). "Keeping attachments needs a malware scanner": `clamav` is not running (`sudo taskiem-vps logs clamav`) |
| Mail arrives but says "not one this workflow takes mail from" | The sender is not on the workflow's list, or could not be proven (no DKIM signature for its domain): widen the list, or leave it empty |
| A form's thank-you page shows no **Get updates** buttons | The workflow must message a phone from the form with **Message a person**; the bot or `TASKIEM_WHATSAPP_DISPLAY_NUMBER` must be set; `TASKIEM_MESSAGE_HASH_KEY` and the public key must be set (`sudo grep -c HASH_KEY /opt/taskiem/taskiem.env`) |
| Updates go by text although the person tapped the link | They stopped (STOP) or blocked the bot; or, on WhatsApp outside 24 hours, the `taskiem_update` template is not approved yet. **Settings > People you message** shows a stop |

## Starting over

To rebuild staging from nothing: drop and recreate the database (empty, same owner), then on the VPS remove the kit and its volumes and run the installer again.

```sh
sudo systemctl disable --now taskiem-vps-update.timer taskiem-vps-check.timer taskiem-vps-backup.timer
sudo docker compose --project-directory /opt/taskiem down -v
sudo rm -rf /opt/taskiem /etc/taskiem /var/lib/taskiem-vps
```

This destroys staging's secrets: never run it on production.
