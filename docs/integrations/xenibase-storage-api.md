# Xenibase storage API

Prepared 19 October 2026 from Xenibase's published docs at `ISRAEL-DUFF/Xenibase` `main` (commit `66ef60e`, which includes Release 4.2.3). The text in the sections "Keys", "Storage" and "Plan limits" is copied from `docs/backend-services.md` without changes, so it matches the source. Section 2 is built from `api/openapi.yaml` and `internal/api/routes.go` on the same commit.

*Kept in Taskiem as the reference for the Xenibase storage integration (P4-T1 in the [Xenibase plan](../xenibase-integration.md#4-phases-and-work-items)), which storefront photos and hosted-form uploads need (Phase 5, S1). When Xenibase's docs change, refresh this copy from the source; do not edit it by hand. The one change from the source: the link to Xenibase's own operations guide points at its repository.*

Xenibase has two storage surfaces. Pick by who is calling:

| | Project storage API (`/storage/v1`) | Management file API (`/api/v1/projects/{id}/files`) |
| --- | --- | --- |
| For | Apps and servers storing their users' files | Tooling that administers a project's files from outside it |
| Host | The project's own: `https://<ref>.<api-domain>` | The Xenibase management API |
| Auth | Project key in `apikey` (publishable `xb_pub_`, or secret `xb_sec_`), plus a user's access token in `Authorization: Bearer` for per-user policies | A Xenibase API token (`xb_...`) in `Authorization: Bearer` |
| Who may do what | Row-level security policies you write on `xb_storage.objects`; the secret key bypasses them | The token's scope and the member's project role; policies do not apply |
| Section | 1 | 2 |

## Keys

| Key | Prefix | Used from | Requests run as |
| --- | --- | --- | --- |
| Publishable | `xb_pub_` | Browsers and mobile apps; meant to be embedded | `<db>_anon`, or `<db>_user` with a signed-in user's token |
| Secret | `xb_sec_` | Servers only | `<db>_service` (bypasses row-level security) |

- Send the key in the `apikey` header. A signed-in user's access token goes
  in `Authorization: Bearer …`; it is an ES256 JWT for this project
  (`aud` is the reference), verified with the project's public keys.
- Keys are stored hashed. The publishable key stays visible in the
  dashboard; a **secret key is shown once**, when it is made.
- A secret key sent from a page (a request with an `Origin` header) is
  refused unless the project allows it.
- Make a second key and move your apps to it before revoking the first:
  revoking takes effect at the edge within seconds.


---

# 1. Project storage API (`/storage/v1`)

## Storage

Files live in **buckets**. A file's metadata is a row in the project's own
database (`xb_storage.objects`: bucket, path, size, MIME type, checksum,
owner, your metadata), and its bytes are in the region's object store (the
same store as the region's backups; for data-residency projects, the
in-country one). Who may read, upload, overwrite and delete files is decided
by **row-level security on `xb_storage.objects`**, the same policies as
your tables. The project's owner role owns the table, so you write the
policies:

```sql
-- Each user reads and writes only under avatars/<their id>/.
CREATE POLICY own_avatar ON xb_storage.objects FOR ALL TO <db>_user
  USING (bucket = 'avatars' AND xb_storage.folder(path, 1) = xb_auth.uid()::text)
  WITH CHECK (bucket = 'avatars' AND xb_storage.folder(path, 1) = xb_auth.uid()::text);
```

Helpers: `xb_storage.foldername(path)` (the folders, as an array),
`xb_storage.folder(path, n)` (the nth), `xb_storage.filename(path)` and
`xb_storage.extension(path)`. The edge checks each request by running the
matching statement on `xb_storage.objects` as the caller (an insert for
an upload, an update for an overwrite, a select for a read, a delete for a
delete) before it touches the bytes. With row-level security off on
`xb_storage.objects`, the publishable key gets `403 rls_required`; the
secret key bypasses policies.

### Buckets

Buckets are made with the secret key (`POST /storage/v1/bucket`), in
Project → Storage, or with `xenibase storage buckets create`. A bucket has a
name (lowercase, digits, `.`, `_`, `-`; it is in file URLs), whether it is
**public**, a **file size limit**, **allowed MIME types** (`image/*`,
`application/pdf`; empty allows any), and how long browsers may cache its
files. A public bucket's files can be downloaded by anyone with the URL;
uploads to it still need a policy. Making a bucket private purges the CDN's
cache of its files when a CDN is configured (below). A bucket with files
can't be deleted until it is emptied (`POST /storage/v1/bucket/<id>/empty`,
or the dashboard's delete, which asks).

### Endpoints

Supabase's storage clients work against these paths; the spellings they
use (`/object/public/…`, `/object/sign/…`, `/render/image/…`) are accepted
too.

| Request | |
| --- | --- |
| `POST /storage/v1/object/<bucket>/<path>` | Upload up to 50 MB (`x-upsert: true` to overwrite; `PUT` always overwrites) |
| `GET /storage/v1/object/<bucket>/<path>` | Download, as the caller's policies allow; `Range` works |
| `GET /storage/v1/object/info/<bucket>/<path>` | The file's metadata |
| `GET /storage/v1/public/<bucket>/<path>` | A public bucket's file, no key |
| `POST /storage/v1/object/sign/<bucket>/<path>` | A signed download URL (`expires_in` seconds, 1 hour by default, 7 days at most; `transform` for an image size) |
| `POST /storage/v1/object/sign/<bucket>` | Signed URLs for several `paths` |
| `POST /storage/v1/object/upload/sign/<bucket>/<path>` | A signed upload URL (2 hours) for a client without a session |
| `POST /storage/v1/upload/<bucket>/<path>` | Start a large upload (`size`, `mime_type`) |
| `GET`, `DELETE /storage/v1/upload/<id>`; `POST …/<id>/complete` | Its status and the parts still to send; abort; finish |
| `GET /storage/v1/list/<bucket>?prefix=&cursor=&limit=` | Files and folders under a prefix (`POST /storage/v1/object/list/<bucket>` takes the same as JSON) |
| `POST /storage/v1/object/move`, `/copy` | `{"bucket", "from", "to"}`, and `to_bucket` for another bucket |
| `DELETE /storage/v1/object/<bucket>/<path>`; `DELETE /storage/v1/object/<bucket>` | Delete one, or `{"paths": […]}` |
| `GET /storage/v1/render/<bucket>/<path>?width=&height=&resize=&format=&quality=` | An image transform |

Paths are UTF-8 with no empty, `.` or `..` segments and no control
characters, at most 1,024 bytes. A file's type is sniffed from its first
bytes: a declared type that contradicts them (HTML posing as a PNG) is
refused with `400 mime_mismatch`, and a type the bucket doesn't allow with
`415 mime_not_allowed`. Over the bucket's limit or the plan's largest
upload is `413 too_large`; over the organisation's file storage quota is
`413 quota_exceeded`.

### Large uploads

Files over 50 MB, up to the plan's largest upload (5 GB on Pro and Team),
go straight to the object store. Starting one checks the policies and the
quota as an upload would, and answers with presigned URLs for its parts
(16 MiB or more each, at most 10,000):

```json
{"id": "…", "size": 2147483648, "part_size": 16777216, "parts": [{"number": 1, "size": 16777216, "url": "https://…"}, …],
 "expires_at": "…"}
```

`PUT` each part's bytes to its URL; a failed part can be sent again.
`GET /storage/v1/upload/<id>` lists the parts not yet received (with fresh
URLs), so an app can resume after a restart. `POST …/complete` checks the
parts and sizes and writes the file's row as the user who started the
upload. An upload not completed in 24 hours is abandoned and its parts
removed.

### Signed URLs, caching and the CDN

A signed URL carries a token over the project, bucket, path, expiry (and
transform), signed with a key derived for the project, so the edge serves
it without a policy query: the policies were checked when it was made.
Files are served with `ETag` and `Cache-Control` (`public` for public
buckets, `private` otherwise, for the bucket's cache time), and
`Content-Disposition: attachment` with `?download` (or `?download=<name>`). Put a CDN in front of
the edge's hostname to cache public files; with Cloudflare, set
`XENIBASE_CDN_CLOUDFLARE_ZONE_ID` and `XENIBASE_CDN_CLOUDFLARE_TOKEN` (a token
with Cache Purge) on xenibase-server so a bucket made private is purged.

### Image transforms

`/render/…` resizes (`width`, `height` up to 2,500 px; `resize` is
`cover`, `contain` or `fill`), converts (`format`: `webp`, `avif`, `jpeg`,
`png`, or `origin`) and sets `quality` (20–100) for PNG, JPEG, GIF, WebP
and AVIF sources up to 25 MB and 25 megapixels. Each result is kept in the
object store next to the file's version, so the second request for the
same transform is served from that cache (`X-Cache: HIT`) without
rendering; replacing or deleting the file removes them. Transforms are
counted per month against the plan (500 on Personal), as are file
downloads' bytes (5 GB on Personal); once either allowance is used up,
renders answer `429 transform_limit` or downloads `429
storage_egress_limit` until the month turns.

Renders run in separate worker processes (`xenibase-edge render-worker`), one
image at a time each, as many as half the edge's CPUs. A worker has its own
memory limit (`XENIBASE_EDGE_RENDER_MEMORY_MB`, 512 by default); an image
that crashes its decoder or runs it out of memory ends only that worker:
the request gets `500 transform_failed` and the next render starts a new
worker.

### Behind the scenes

- **Versions.** Each upload writes a new object under a new version, and
  the row points at it; the old version's bytes are removed a minute
  later, so a download in flight finishes. A trigger on
  `xb_storage.objects` queues the bytes to remove and keeps the project's
  totals.
- **The sweep** (every 5 minutes, xenibase-server) removes queued bytes and
  abandoned uploads, records storage used (`storage_gb_hours`) and the
  month's downloads and transforms, and sends the edges each project's
  remaining quota.
- **The reconciler** (nightly) removes bytes with no row after 7 days and
  counts rows whose bytes are missing; Project → Storage shows them.
- **Deleting a project** removes its files once its final backup's
  retention ends (30 days).
- **Branches and restores** copy the database, so file rows come along but
  not the bytes: a branch's files show as missing until uploaded again.
  Moving a project to another region doesn't move its files yet.

### The dashboard and the CLI

Project → Storage lists buckets with their size, browses folders, uploads
(up to 50 MB from the browser), downloads, makes signed URLs, deletes files
and changes bucket settings, as the platform (policies don't apply).
Project admins and developers can use it; read-only members can't, since
files may hold your users' data. The CLI does the same:

```sh
xenibase storage buckets create <p> avatars --types 'image/*' --size-limit 5MB
xenibase storage cp <p> ./logo.png ss:///avatars/logo.png
xenibase storage ls <p> ss:///avatars/
xenibase storage cp <p> ss:///avatars/logo.png ./logo.png
xenibase storage sign <p> ss:///avatars/logo.png --expires 24h
xenibase storage rm <p> ss:///avatars/logo.png
```


---

# 2. Management file API (`/api/v1/projects/{id}/files`)

These endpoints sit on the management API and act as the platform, so storage policies do not apply (the guide says the dashboard's Storage page works the same way). They need a Xenibase API token (see `docs/cli.md`): `read` scope for the first group, `write` scope for the second. A token restricted to some projects works on those projects. The member behind the token needs the project's developer role or higher (read-only members cannot, since files may hold your users' data). An upload here is limited to 50 MB; larger files use the project storage API's large-upload flow (section 1).

Errors from this API are flat JSON: `{"code": "...", "message": "..."}` (see `docs/errors.md`, "Management API"). A paused Free project answers `503 project_resuming` or `503 project_restoring` with `Retry-After`.

`{id}` is the project's id. `{bucket}` is the bucket name. File paths go in the `path` query parameter, not the URL path.

| Request | Scope | What it does |
| --- | --- | --- |
| `GET /api/v1/projects/{id}/files` | read | The project's file storage overview: bytes and object counts, `quota_bytes` (null is unlimited), `upload_max_bytes`, whether downloads (`egress_blocked`) or image transforms (`transforms_blocked`) are used up for the month, the reconciler's findings (`missing_objects`, `missing_sample`, `reconciled_at`), and the buckets |
| `POST /api/v1/projects/{id}/files/buckets` | write | Create a bucket. Body: `id` (the name), and optionally `public`, `file_size_limit` (bytes), `allowed_mime_types`, `cache_seconds`. Answers `201` with the bucket |
| `PATCH /api/v1/projects/{id}/files/buckets/{bucket}` | write | Change `public`, `file_size_limit` (`null` removes the limit), `allowed_mime_types` or `cache_seconds`. Making a bucket private purges its files from the CDN |
| `DELETE /api/v1/projects/{id}/files/buckets/{bucket}` | write | Delete a bucket. It must be empty, or pass `?empty=true` to delete its files first. Answers `204` |
| `GET /api/v1/projects/{id}/files/buckets/{bucket}/objects` | read | A folder of the bucket: query `prefix`, `cursor`, `limit` (1 to 1000). Answers `items` (each with `name`, `path`, `folder` and, for files, the object) and `next_cursor` |
| `DELETE /api/v1/projects/{id}/files/buckets/{bucket}/objects` | write | Delete files, or folders with everything under them. Body: `{"paths": ["..."]}`. Answers `{"deleted": <count>}` |
| `GET /api/v1/projects/{id}/files/buckets/{bucket}/object?path=` | read | Download a file (`application/octet-stream`) |
| `PUT /api/v1/projects/{id}/files/buckets/{bucket}/object?path=` | write | Upload a file, up to 50 MB; replaces one at the same path. Send the file's type as `Content-Type`. Answers `200` with the stored object |
| `POST /api/v1/projects/{id}/files/buckets/{bucket}/sign` | read | A signed download URL. Body: `{"path": "...", "expires_in": <seconds>}` (1 hour by default, 7 days at most). Answers `signed_url` and `expires_at` |

## Objects returned

| Field | Meaning |
| --- | --- |
| `id`, `bucket`, `path` | Identity and location |
| `size` | Bytes |
| `mime_type`, `etag` | Content type and entity tag |
| `checksum`, `owner` | Nullable |
| `created_at`, `updated_at` | Timestamps |

A bucket is returned as `id`, `public`, `file_size_limit` (nullable bytes), `allowed_mime_types`, `cache_seconds`, `objects` (count), `bytes`, `created_at`, `updated_at`.

---

# 3. Limits and errors that affect storage

## Plan limits

Each quota plan ([quotas](https://github.com/ISRAEL-DUFF/Xenibase/blob/66ef60e/docs/operations.md#quotas-storage-locks-and-usage))
can set monthly hard limits and ceilings on a project's own settings. A key the plan leaves out has no
limit; an organisation's overrides (Admin → Organisations) win over the
plan.

| Limit | Key | Personal | Pro | Team |
| --- | --- | --- | --- | --- |
| Data API requests a month | `api_requests_per_month` | 500,000 | — | — |
| Monthly active users | `auth_mau_per_month` | 10,000 | — | — |
| Statement timeout ceiling (ms) | `api_timeout_ms` | 5,000 | 8,000 | 15,000 |
| Requests per minute per IP, ceiling | `api_rate_per_ip_per_min` | 300 | 600 | 1,200 |
| Requests per minute per key, ceiling | `api_rate_per_key_per_min` | 3,000 | 12,000 | 30,000 |
| SMS codes a day (platform sender) | `sms_codes_per_day` | — | 1,000 | 5,000 |

- **Monthly requests.** Once an organisation's data API requests this
  month reach the limit, the data API, storage and realtime answer
  `429 plan_limit_reached` with a `Retry-After` up to the start of next
  month. Auth and `GET /data/v1/health` keep working, so users can still
  sign in and an app can say why. Paid plans have no request limit: they
  are billed past what the plan includes.
- **Monthly active users.** At the limit, users who already signed in
  this month keep signing in and refreshing their sessions; a new user
  gets `429 mau_limit_reached`. The edge gets a compact filter of the
  users counted this month (about 12 KB for 10,000 users), so it can
  answer without asking xenibase-server; about one new user in a hundred
  can still slip through until the next count.
- **Ceilings.** A project's timeout and rate limits are capped at the
  plan's: asking for 15 s on a 5 s plan gets 5 s. The API page (Access
  and limits) and `xenibase services status` show what is in effect.
- **SMS codes.** Codes sent with Xenibase's own sender are capped per
  project and day at the lower of the project's cap and the plan's; a
  project's own provider has only its own cap. Free plans send codes only
  when the operator allows it (`XENIBASE_PHONE_AUTH_FREE`).

xenibase-server works the limits out every five minutes, so a limit
reached or lifted (an upgrade, an override) reaches the edges within
about five minutes. Owners and admins get an email at 80% and at 100% of
each monthly limit, once a month each.

On an install upgrading from an earlier version, the limits apply from
the first full month after the upgrade (the `plan_limits_from` setting);
notices before then say when they start.


## Error codes named in the storage guide

The project storage API uses the error codes of the storage clients it is compatible with (`docs/errors.md`), so its error bodies do not necessarily match the data API's `{"error": {...}}` shape. The guide above names these:

| Code | Status | When |
| --- | --- | --- |
| `rls_required` | 403 | Row-level security is off on `xb_storage.objects` and the publishable key was used |
| `mime_mismatch` | 400 | The declared type contradicts the file's first bytes |
| `mime_not_allowed` | 415 | The bucket does not allow the type |
| `too_large` | 413 | Over the bucket's size limit or the plan's largest upload |
| `quota_exceeded` | 413 | Over the organisation's file storage quota |
| `transform_limit` | 429 | The month's image transforms are used up |
| `storage_egress_limit` | 429 | The month's download allowance is used up |
| `transform_failed` | 500 | An image crashed its decoder or ran out of memory; the next render starts a new worker |
| `plan_limit_reached` | 429 | The month's requests are used up; `Retry-After` runs to the start of next month |

## Not covered by these docs

- A machine-readable OpenAPI description of `/storage/v1`. The management API's `api/openapi.yaml` describes only section 2; the project storage API is described by the guide in section 1 (the per-project `GET /data/v1/openapi.json` describes the data API).
- Exact JSON bodies of the `/storage/v1` answers beyond the large-upload example. The guide says the paths and spellings follow Supabase's storage clients.
- Moving a project to another region does not move its files yet, and a branch's files show as missing until uploaded again (both stated in the guide above).
