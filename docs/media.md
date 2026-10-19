# Files in a tenant's Xenibase storage

Storefront photos and the files people send in hosted forms are kept in the organisation's own Xenibase project, the same project as its [data tables](data-tables.md) (Phase 5, S1). The storage API is described in the [Xenibase storage API reference](integrations/xenibase-storage-api.md). This is built and idle until storefronts (S2) and forms (S7) store files.

## Two buckets per project

| Bucket | Holds | Who can read it | Settings |
| --- | --- | --- | --- |
| `taskiem-public` | Storefront photos and logos | Anyone, at the project's host: `<api_url>/storage/v1/public/taskiem-public/…` | Public; JPEG and PNG only; 10 MB; cached for a year |
| `taskiem-private` | Files sent in forms: payslips, IDs, receipts | Only the organisation, through signed URLs that expire (at most 7 days) | Private; JPEG, PNG and PDF only; 25 MB; not cached |

Taskiem reaches them with the project's API token through Xenibase's management file API, where storage policies do not apply. No policy is written on `xb_storage.objects`, so the project's publishable key opens nothing.

The buckets are made the first time a tenant stores a file, and checked again at least hourly per process. A bucket whose settings were changed outside Taskiem is put back. If `taskiem-private` has been made public and cannot be made private again, nothing is written to it.

## Photos

Every image is checked and cleaned before it is stored:

| Step | Why |
| --- | --- |
| The type is read from the file's bytes, not its name: JPEG, PNG or WebP | A script named `photo.jpg` is refused |
| The header must say at most 25 million pixels, checked before decoding | A small file claiming a huge image (a decompression bomb) cannot take the memory |
| It is turned upright from the camera's EXIF orientation | Phones store photos sideways and say so in EXIF |
| It is re-encoded | This drops the metadata: GPS location, camera, the owner's name |
| It is stored at 2000, 800 and 320 pixels wide, never larger than it came | Pages load the size they need on 3G. Taskiem makes the sizes itself rather than with Xenibase's image transforms, which are counted against the project's plan (500 a month on Personal) |

PNGs with transparency (logos) stay PNG; everything else becomes JPEG. Paths are named by the image's content (`images/<hash>/<width>.jpg`), so storing the same photo twice stores nothing new, and the files can be cached for good.

**Downloads count against the project's plan** (5 GB a month on Personal). Past a few thousand storefronts, put a CDN in front of the projects' hosts ([storefronts 8.7](spec/storefronts.md#87-at-scale)).

## Files from forms

- **Photos** are cleaned as above and kept at full size.
- **PDFs** are refused if they carry JavaScript, actions run on opening, embedded files, rich media or XFA forms. This is a quick check of the file's text, not a scanner: form files are also scanned for malware before a run sees them ([forms 6.5](spec/forms.md#65-files)), which comes with S7.
- **Anything else** is refused.

Files go under `files/<random id>`, so a path reveals nothing about its content.

## Checking a tenant's storage

```bash
taskiem media check TENANT_ID
```

It does a round trip through the tenant's project, reporting each step, then deletes what it stored:
1. Makes the buckets.
2. Stores a generated photo and downloads it from its public URL.
3. Stores a PDF and downloads it through a signed URL.
4. Checks the PDF cannot be downloaded publicly.

Run it on each new Xenibase release, and before storefronts go live on an install.
