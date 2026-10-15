# Running Taskiem on one VM

[Decision 0032](decisions/0032-deployment-target.md) allows staging and the first production environment to run on one VM until a trigger moves them to [Kubernetes](kubernetes.md). This guide sets that up with `deploy/vm`: every role in one process, OpenBao with persistent storage, and Caddy for HTTPS. Postgres runs elsewhere, on a managed service or another host, with backups and point-in-time recovery.

What this deployment does not do: container steps (their only safe runner is Kubernetes), more than one replica of anything, or upgrades without a short stop. If you need any of those, you have met a trigger in 0032.

## Before you start

| You need | Notes |
| --- | --- |
| A Linux VM, hosted in Nigeria (P4-C1) | At least 2 vCPU and 4 GiB; 4 vCPU and 8 GiB is comfortable (each running Python step may use 256 MiB). Docker Engine with the Compose plugin |
| A DNS name | An A (and AAAA) record pointing at the VM before the first start, so Caddy can get a certificate |
| A firewall | Inbound 80 and 443 from anywhere; 22 from your own addresses only. Nothing else, including 8200 (OpenBao) and 9090 (metrics) |
| Postgres 16 | Managed, or on another host; backups with point-in-time recovery turned on. `max_connections` of at least 30 for this deployment |
| SMTP | For password resets, invitations and alerts (P4-O1 for the sending domain) |
| The image | Published by CI to `ghcr.io/israel-duff/taskiem` on every merge to main, tagged `sha-<12 characters of the commit>` and, for releases, the version. The package is private until its owner makes it public; otherwise run `docker login ghcr.io` on the VM with a token that has only `read:packages` |

## 1. Database

Create the database and two roles. The owner runs migrations, which create the `taskiem_app` and `taskiem_dispatch` roles, so it needs `CREATEROLE` (on a managed service, use its administrator to create it). Taskiem itself signs in as a login role that is only a member of `taskiem_app` ([operations](operations.md#database-roles)).

```sql
CREATE ROLE taskiem_owner LOGIN CREATEROLE PASSWORD '…';
CREATE DATABASE taskiem OWNER taskiem_owner;
```

After the first migration (step 4), as `taskiem_owner`:

```sql
CREATE ROLE taskiem LOGIN PASSWORD '…';
GRANT taskiem_app TO taskiem;
```

Use `sslmode=verify-full` in both connection strings wherever the provider supports it.

## 2. Files

Copy `deploy/vm` to the VM (for example to `/opt/taskiem`) and, in it:

```sh
for f in .env taskiem.env migrate.env openbao.env; do cp "$f.example" "$f"; done
chmod 600 *.env
```

| File | Holds |
| --- | --- |
| `.env` | `TASKIEM_DOMAIN` and `TASKIEM_IMAGE` (an immutable tag, never `main`). No secrets |
| `migrate.env` | The owner's connection string |
| `taskiem.env` | Taskiem's settings: the `taskiem` role's connection string, the public URL, the anchor key (`openssl rand -base64 32`, kept offline too), SMTP |
| `openbao.env` | Taskiem's OpenBao token, filled in during step 3 |

The `.env` files are ignored by Git; only the `.example` files are in the repository.

Check the image before trusting it. CI signs every image keylessly:

```sh
cosign verify "$TASKIEM_IMAGE" \
  --certificate-identity-regexp '^https://github.com/ISRAEL-DUFF/taskiem/\.github/workflows/ci\.yml@refs/(heads/main|tags/v.+)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## 3. OpenBao

OpenBao holds the key that wraps every secret Taskiem stores. It starts sealed, and stays sealed after every restart until people with unseal keys open it.

1. Start it: `docker compose up -d openbao`.
2. Initialise it once. Five key shares, any three of which unseal:

   ```sh
   docker compose exec openbao bao operator init -key-shares=5 -key-threshold=3
   ```

   Give each unseal key to a different person, stored offline. Keep the printed root token only until step 5.
3. Unseal it: run `docker compose exec openbao bao operator unseal` three times, with three different keys (it prompts; nothing goes into shell history).
4. Create Taskiem's key, policy and token. In a shell inside the container (`docker compose exec openbao sh`):

   ```sh
   bao login                                    # paste the root token at the prompt
   bao secrets enable transit
   bao write -f transit/keys/taskiem            # aes256-gcm96, not exportable, not deletable
   bao policy write taskiem /openbao/policies/taskiem.hcl
   bao token create -policy=taskiem -no-default-policy -orphan -period=768h -display-name=taskiem -field=token
   ```

   Put the token in `openbao.env` as `TASKIEM_OPENBAO_TOKEN`. It can only encrypt and decrypt with that key and renew itself (`openbao-taskiem-policy.hcl`). It is periodic: it lasts 32 days from its last renewal, and the `openbao-renew` service renews it daily. Taskiem does not renew it itself, so never stop `openbao-renew` for long.
5. Revoke the root token: `bao token revoke -self`, then `exit`. A new one can be made later from the unseal keys (`bao operator generate-root`).

## 4. Start

```sh
docker compose up -d
docker compose ps        # migrate exited 0; openbao, taskiem and caddy healthy
```

The order is enforced: migrations first, Taskiem once they finish and OpenBao is unsealed, Caddy once Taskiem is healthy. Then create the `taskiem` login role (step 1) if you have not, and restart: `docker compose up -d --force-recreate taskiem`.

Create the first tenant once. Read the password without echoing it:

```sh
read -rs P && docker compose run --rm --no-deps -e TASKIEM_BOOTSTRAP_PASSWORD="$P" taskiem \
  bootstrap --tenant "Payrolla" --email you@example.com; unset P
```

## 5. Check

- `https://<domain>/readyz` answers `ok`; `https://<domain>/status` shows the status page.
- Sign in; the owner is asked to add a passkey (administrators are held to passkeys with an https public URL).
- Alerts → add an email channel, confirm it, and send a test message.
- Audit log → Verify chain.
- `docker compose logs openbao-renew` shows `renewed`.

## 6. Partners

Webhook URLs are on each workflow's Endpoints tab (or `GET /v1/workflows/{id}/triggers`):

- **Payrolla** sends payrolls to `https://<domain>/hooks/<tenant>/payrolla/payroll-approved?env=prod`, signed with the environment secret `webhook_wf_payrollaSalaryDisbursement`. Set `payrolla_api_url` to Payrolla's report base URL and `payrolla_api_token` to the token the company issues ([Payrolla](integrations/payrolla.md)).
- **iswallet** delivers outflow events to `https://<domain>/hooks/<tenant>/connectors/iswallet@1/outflow_event?env=prod&connection=<name>`. The connection holds the API key and the subscription's signing secret ([iswallet](integrations/iswallet.md)).

The VM's public address is the platform's egress address; give it to partners who want it for incident forensics.

## Upgrades

1. Set `TASKIEM_IMAGE` in `.env` to the new tag, and verify its signature.
2. `docker compose pull && docker compose up -d`.

Taskiem drains its workers (up to 30 s), the migration runs, and the new version starts. Webhooks sent during the gap get connection errors and are retried by the sender; nothing already accepted is lost. Upgrade outside payroll runs. Every release works with the schema of the release before it, so going back means setting the previous tag again; the schema never moves backwards.

## After a reboot

Docker restarts every service, but OpenBao comes back **sealed**. Until it is unsealed (step 3.3), Taskiem cannot decrypt secrets, and anything that needs one fails. Unseal it at once, and alert on it (`/v1/sys/health` answers 503 while sealed).

## Backups

| What | How | Without it |
| --- | --- | --- |
| Postgres | The provider's backups with point-in-time recovery; a restore drill each quarter ([cloud](cloud.md#restore-drills)) | Every workflow, run and audit entry |
| OpenBao's storage (`openbao_data`) | A daily copy, encrypted, kept off the VM: `docker run --rm -v taskiem_openbao_data:/d:ro -v "$PWD":/b openbao/openbao:2.4.1 tar czf /b/openbao-$(date +%F).tgz -C /d .` | **Every secret Taskiem holds, permanently**: the storage without unseal keys, or the keys without the storage, is useless |
| Unseal keys | Offline, with five different people | As above |
| `TASKIEM_ANCHOR_KEY` | Offline | Anchors already signed still verify with the published public key; new ones need a new key |
| `taskiem_data` (archive, anchors) | Copy off the VM; anchors ideally to write-once storage | Archived runs past retention; the anchors' independent copy |

## Monitoring

Metrics are on `127.0.0.1:9090` for a Prometheus on the VM or one reaching it through a tunnel; the recording rules and alerts are in `deploy/prometheus` ([reliability](reliability.md)). Watch at least: Taskiem's health, OpenBao sealed, `openbao-renew` failures, disk space for Docker volumes, and certificate expiry (Caddy renews on its own).
