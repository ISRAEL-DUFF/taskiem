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
| Voice notes and audio | Ogg (Opus, Vorbis), WebM, MP3, MP4 audio, AAC (ADTS) and AMR (narrow and wide band, as older phones record); tags stripped; a container with a video track is refused | 16 MB, and an Ogg recording at most 5 minutes long |
| Documents | PDF, refused if it carries scripts or actions | 25 MB |
| Photos | JPEG, PNG, WebP, re-encoded without metadata | 25 MB |

Anything else is refused (`runfiles.ErrRefused`), and so is an empty file.

**What a person sent is scanned** for malware before it is kept, by the clamd the operator runs (`TASKIEM_CLAMD_ADDR`). Without one, such files are refused, as hosted forms' files are. Files a step made itself (a reply a speech step spoke) are not scanned. A person's file is marked personal (`run_files.personal`) and kept under a neutral name (`voice-note.ogg`, `photo.jpg`, `document.pdf`), never the name it was sent with, which can carry a person's name or number. The time each scan takes is measured (`taskiem_file_scan_seconds`), and so is every file kept or refused (`taskiem_run_files_total{origin="person"|"made", outcome="kept"|"refused"|"over_allowance"}`), for [operations](operations.md).

## How much can be kept

Each organisation may keep at most **1 GB of new files in any 24 hours** (`runfiles.Service.DailyBytes`, `DefaultDailyBytes`). Past that a file is refused (`ErrOverAllowance`, fatal for the step), so a flood of large voice notes cannot fill the organisation's storage or run up the scanner.

## How long files are kept

30 days from when the file was kept, or less when the plan keeps run history for less (`max_retention_days`: 7 days on Starter). An expired file cannot be read or linked at once; a sweep in the api role deletes the stored object and then the row every 10 minutes. A tenant whose storage cannot be reached keeps its expired files until a later sweep succeeds. A per-assistant retention (30 days by default, changeable by the owner) comes with [plan V5](spec/voice-assistant.md#v5-memory-and-the-conversations-table).

A run's files go no later than the run itself: when a run ends, its files' time is cut to the run's retention if that is shorter. For a run kept `"conversations"` long ([data tables](data-tables.md#how-long-rows-are-kept)), its files are kept exactly as long as the run, so a conversation's voice notes go with its rows and runs.

## For connectors

A connector call in a run gets `connector.Request.Files` (nil outside a run, or where the deployment keeps no files):

- `Put(ctx, name, data, personal, about...)` keeps bytes and answers the reference. A connector that downloads a customer's media puts it with `personal: true` and says whom it is about (`connector.FileSubject{Category: "phone", Value: from}`): the file is tied to that person's key (`run_files.subject`, migration 00314), and erasing the person deletes it.
- `Read(ctx, ref)` answers the bytes and the reference, given the reference or its id. Only the run's own organisation's files are found.

The worker binds the store to the run (organisation, environment, workflow and run), so a connector never names an organisation.

## The `files@1` connector

A platform connector that needs no connection. It acts for the organisation of the run calling it.

| Action | Class | |
| --- | --- | --- |
| `link` | read | A download link for a file (`file`: the reference or its id; `ttl_minutes` 1 to 60, default 15). Output: `url`, `expires_at`, `file` (the reference). The link opens the file for anyone who has it, so it is sealed in the run's history (`pii`) like a customer's number; the run's own later steps read it as usual |
| `delete` | idempotent write | Deletes a file now. Deleting one that is already gone does nothing |

An id the organisation does not have, or a file that has expired, fails the step (fatal: retrying cannot help).

## Storage and records

- `run_files` (migrations 00313, 00314): id, tenant, environment, workflow, run, name, type, size, sha256, storage key, personal, subject (the person's key, when a connector named them), expires_at; forced row-level security. Indexes by subject (for erasure) and by organisation and time (for the daily allowance).
- The sweep reads expired files through `taskiem_run_files_expired(limit)`, which shows only ids, tenants and storage keys, never names or content.
- Objects are stored under `runs/` in the private bucket (`files/` for PDFs and photos, as forms keep them).

## Erasure

Erasing a person (`POST /v1/pii/erase`, [privacy](privacy.md)) deletes their key and then every run file tied to them, at once rather than at expiry. The answer says how many were deleted (`files_deleted`); `files_pending: true` means storage could not be reached and the files are deleted by a later sweep (their key is already gone either way).

## Not yet

- Where Xenibase stores the objects (region) is a people item (VP8).
- Converting audio between formats (plan F7, decided by a spike before speech replies).
