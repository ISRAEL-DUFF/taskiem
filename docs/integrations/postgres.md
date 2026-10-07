# PostgreSQL integration

The connector is `connectors/postgres` (`postgres@1`).

## Actions

| Action | Class | What it does |
| --- | --- | --- |
| `query` | read | Runs one statement in a read-only transaction and returns at most `max_rows` rows (default 1000, maximum 10,000), plus `row_count` and `truncated`. Also the connection test. |
| `execute` | unsafe write | Runs one statement and returns `rows_affected`. |

Both take `sql` and an optional `params` array (`$1`, `$2`, ...). Values travel separately from the SQL text. Every statement has a 30-second `statement_timeout`. Each step opens its own connection and closes it when the step ends.

## Creating the connection

| Field | Required | Notes |
| --- | --- | --- |
| `host` | yes | Taskiem connects only to this host, through the egress guard, which refuses private, loopback and metadata addresses. |
| `port` | no | Default `5432`. |
| `database` | yes | |
| `user` | yes | |
| `password` | no | Stored encrypted. |
| `sslmode` | no | `verify-full` (default), `verify-ca`, `require` or `disable`. |

### TLS

`sslmode` means what it means for libpq:

| `sslmode` | Encrypted | Certificate checked | Host name checked |
| --- | --- | --- | --- |
| `disable` | no | | |
| `require` | yes | no | no |
| `verify-ca` | yes | yes | no |
| `verify-full` (default) | yes | yes | yes |

A connection that sets no `sslmode` uses `verify-full`. The server's certificate must chain to the system roots and name the host you entered. A certificate from a private CA (Amazon RDS's, for example) does not verify against the system roots: for such a server, set `sslmode` explicitly. The connector does not yet take a CA bundle as the MySQL connector does (`ssl_ca`). `require` encrypts but does not authenticate the server: anyone who can intercept the traffic could pose as it and receive the password. Use `require` or `disable` only on networks you trust.

Connections created before `verify-full` became the default, and that set no `sslmode`, now verify the server. If such a connection's test starts to fail with a certificate error, the server's certificate does not verify. Fix the certificate, or set `sslmode` to `require` knowingly.
