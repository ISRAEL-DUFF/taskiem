# Files in runs

A workflow run sometimes holds a file: a voice note a customer sent, a reply a speech step spoke, a document a step downloaded to pass on. A run never carries the file's bytes. It carries a **reference**, and the file itself is kept in the organisation's own storage ([media](media.md): its Xenibase project's private bucket) for a limited time ([decision 0062](decisions/0062-customer-assistant.md), [plan V1](spec/voice-assistant.md#v1-files-in-runs)).

## A file reference

```json
{ "file": "0193f0a2-…", "name": "voice.ogg", "type": "audio/ogg", "size": 48213, "sha256": "9f2c…" }
```

- `file` is the file's id. Steps pass the reference on as it is (`"=steps.fetch.output.file"`); an action that takes a file accepts the reference or its id.
- `type` and `size` describe what is kept, which may differ from what was sent: a voice note's tags are stripped and a photo is cleaned before it is stored.
- `sha256` is checked every time the file is read back.

The reference is ordinary JSON: nothing in wd/v1 changed for it, and a run's history and its 256 KB step outputs hold only the reference. Forms' file answers and email attachments keep their own shape (`{id, …}`) and their own link actions (`form@1 file_url`, `email@1 file_url`).

## What can be kept

| Kind | Types | Largest |
| --- | --- | --- |
| Voice notes and audio | Ogg (Opus, Vorbis), WebM, MP3, MP4 audio; tags stripped; a container with a video track is refused | 16 MB, and an Ogg recording at most 5 minutes long |
| Documents | PDF, refused if it carries scripts or actions | 25 MB |
| Photos | JPEG, PNG, WebP, re-encoded without metadata | 25 MB |

Anything else is refused (`runfiles.ErrRefused`), and so is an empty file.

**What a person sent is scanned** for malware before it is kept, by the clamd the operator runs (`TASKIEM_CLAMD_ADDR`). Without one, such files are refused, as hosted forms' files are. Files a step made itself (a reply a speech step spoke) are not scanned. A person's file is marked personal (`run_files.personal`).

## How long files are kept

30 days from when the file was kept, or less when the plan keeps run history for less (`max_retention_days`: 7 days on Starter). An expired file cannot be read or linked at once; a sweep in the api role deletes the stored object and then the row every 10 minutes. A tenant whose storage cannot be reached keeps its expired files until a later sweep succeeds. A per-assistant retention (30 days by default, changeable by the owner) comes with [plan V5](spec/voice-assistant.md#v5-memory-and-the-conversations-table).

## For connectors

A connector call in a run gets `connector.Request.Files` (nil outside a run, or where the deployment keeps no files):

- `Put(ctx, name, data, personal)` keeps bytes and answers the reference. A connector that downloads a customer's media puts it with `personal: true`.
- `Read(ctx, ref)` answers the bytes and the reference, given the reference or its id. Only the run's own organisation's files are found.

The worker binds the store to the run (organisation, environment, workflow and run), so a connector never names an organisation.

## The `files@1` connector

A platform connector that needs no connection. It acts for the organisation of the run calling it.

| Action | Class | |
| --- | --- | --- |
| `link` | read | A download link for a file (`file`: the reference or its id; `ttl_minutes` 1 to 60, default 15). Output: `url`, `expires_at`, `file` (the reference) |
| `delete` | idempotent write | Deletes a file now. Deleting one that is already gone does nothing |

An id the organisation does not have, or a file that has expired, fails the step (fatal: retrying cannot help).

## Storage and records

- `run_files` (migration 00313): id, tenant, environment, workflow, run, name, type, size, sha256, storage key, personal, expires_at; forced row-level security.
- The sweep reads expired files through `taskiem_run_files_expired(limit)`, which shows only ids, tenants and storage keys, never names or content.
- Objects are stored under `runs/` in the private bucket (`files/` for PDFs and photos, as forms keep them).

## Not yet

- Erasing every file of one customer with their other data ([plan V6](spec/voice-assistant.md#v6-guard-rails-for-customers)).
- Where Xenibase stores the objects (region) is a people item (VP8).
- Converting audio between formats (plan F7, decided by a spike before speech replies).
