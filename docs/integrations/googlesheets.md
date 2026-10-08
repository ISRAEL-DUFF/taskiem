# Google Sheets integration

The connector is `connectors/googlesheets` (`googlesheets@1`), on the Sheets API v4. Built from Google's public documentation (developers.google.com/sheets/api, now under developers.google.com/workspace/sheets/api, and the OAuth 2.0 guides), read 6 October 2026. Authentication is shared with Gmail (`connectors/internal/google`; see [gmail.md](gmail.md) for how tokens are minted and cached).

## Connections

### A service account (simplest)

1. In the Google Cloud console, enable the **Google Sheets API** in a project.
2. **IAM & Admin → Service accounts → Create service account**, then **Keys → Add key → JSON**.
3. **Share each spreadsheet** the workflows use with the service account's `client_email` (Editor to write, Viewer to read).
4. Connection: `service_account_json` = the key file. Optional `scopes` (default `https://www.googleapis.com/auth/spreadsheets`); optional `subject` to act as a Workspace user instead, which needs domain-wide delegation of the same scope (Admin console → Security → Access and data control → API controls → Manage Domain Wide Delegation).

A spreadsheet created by a service account without a subject is owned by the service account (in its own Drive); share it, or use a subject or a refresh-token connection, if people need it.

### An OAuth client and refresh token

Enable the Sheets API, configure the consent screen, create an OAuth client ID (*Web application*), and obtain a refresh token with offline access for `https://www.googleapis.com/auth/spreadsheets`. Connection: `client_id`, `client_secret`, `refresh_token`.

There is no connection test action: Sheets has no "who am I" call. `get_spreadsheet` on a known spreadsheet checks access.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_values` | `GET spreadsheets/{id}/values/{range}` | read | `value_render_option`, `date_time_render_option`, `major_dimension`; `header_row` |
| `batch_get_values` | `GET spreadsheets/{id}/values:batchGet` | read | Several `ranges`, in order |
| `append_rows` | `POST .../values/{range}:append` | unsafe write | `value_input_option` (default `RAW`), `insert_data_option` (default `INSERT_ROWS`) |
| `update_values` | `PUT .../values/{range}` | idempotent write | A set operation; `request_key` is not sent |
| `clear_values` | `POST .../values/{range}:clear` | idempotent write | Values only; formatting stays |
| `create_spreadsheet` | `POST spreadsheets` | unsafe write | `title`, `sheet_titles`, `locale`, `time_zone` |
| `add_sheet` | `POST spreadsheets/{id}:batchUpdate` (`addSheet`) | unsafe write | `title`, `index`, `row_count`, `column_count` |
| `get_spreadsheet` | `GET spreadsheets/{id}?fields=...` | read | Title, URL and sheets |

**Rows and objects.** Reads return `values` as arrays of rows. With `header_row: true` (rows only) they also return `headers` and `objects`: one object per row after the first, keyed by the first row. Blank headers take their column letter (`C`), repeated headers are numbered (`Gender_2`), cells Google omitted at the end of a row are `null`, and cells beyond the header take their column letter.

**Never twice.** Google has no idempotency key for appends, and a repeated append adds the rows again, so an append whose outcome is unknown (connection dropped after sending, 500, 502, 504) parks for a person. Quota refusals (429, `RESOURCE_EXHAUSTED`) are retried even for appends: Google refused before writing. Sheets returns 503 when a request is too complex; it is retried for reads and set operations, and parks an append. `create_spreadsheet` and `add_sheet` are not idempotent either (a repeated `add_sheet` is refused because the title exists, which cannot be told apart from an older sheet with that title).

**Formula injection.** `value_input_option` defaults to `RAW`, so a value from a webhook such as `=HYPERLINK(...)` is stored as text. Use `USER_ENTERED` only for values the workflow controls.

**Limits.** Google documents 300 read and 300 write requests per minute per project and 60 per user per project, a recommended 2 MB payload, and a 180-second processing limit.

## Needs a person

- A Google Cloud project with the Sheets API enabled, a service account key, and a spreadsheet shared with it, for a live check.

## To confirm with Google

1. Whether a 503 ("complexity of the request is high") can follow a partly applied write; the connector treats 503 as retryable, which parks unsafe writes and retries the rest.
2. Whether `values.clear` accepts an empty body on every front end (the reference says the body must be empty; the connector sends none).
