# MySQL integration

The connector is `connectors/mysql` (`mysql@1`). It works with MySQL 5.7.8+ and 8.x, and with MariaDB 10.1+. It has the same two actions as the PostgreSQL connector, with the same effect classes. It talks to the server through Taskiem's own implementation of the MySQL client/server protocol (`connectors/mysql/internal/wire`). Why: the common Go driver is MPL-2.0, which the licence policy (decision 0007) does not allow in linked code. That implementation was written clean-room from Oracle's public protocol documentation, cross-checked against MariaDB's public pages. No driver source was read.

## Actions

| Action | Class | What it does |
| --- | --- | --- |
| `query` | read | Runs one statement inside a read-only transaction and returns at most `max_rows` rows (default 1000, maximum 10,000), plus `row_count` and `truncated`. Also the connection test. |
| `execute` | unsafe write | Runs one statement with autocommit. Returns `rows_affected` and `last_insert_id`. |

Both actions take `sql` and an optional `params` array. Placeholders are `?`. Every statement is sent as a **server-side prepared statement**, and the values travel separately as typed binary values. Taskiem never splices a parameter into the SQL text, so a value such as `x'); DROP TABLE t; --` is only ever data. The parameter count must match the placeholders, or the step fails before anything is sent.

| JSON parameter | Sent as |
| --- | --- |
| integer | `BIGINT` |
| number with a fraction | `DOUBLE` |
| string | `VARCHAR` |
| `true` / `false` | `1` / `0` |
| `null` | `NULL` |
| object or array | its JSON text (for `JSON` columns or `CAST(? AS JSON)`) |

Each step opens its own connection, uses it for that one step and closes it.

## Creating the connection

| Field | Required | Notes |
| --- | --- | --- |
| `host` | yes | Host name or IP address. Taskiem connects only to this host, through the egress guard, which refuses private, loopback and metadata addresses. |
| `port` | no | Default `3306`. |
| `database` | yes | The default schema. |
| `user` | yes | See [What to grant](#what-to-grant). |
| `password` | no | Stored encrypted. |
| `sslmode` | no | `require` (default), `verify-ca`, `verify-full` or `disable`. |
| `ssl_ca` | no | PEM CA certificate(s) for `verify-ca` / `verify-full`, for example the Amazon RDS or Azure CA bundle. Without it the system roots are used. |

### TLS

The modes mean what they mean for the PostgreSQL connector (libpq's `sslmode`):

| `sslmode` | Encrypted | Certificate checked | Host name checked |
| --- | --- | --- | --- |
| `disable` | no | | |
| `require` (default) | yes | only when `ssl_ca` is set | no |
| `verify-ca` | yes | yes | no |
| `verify-full` | yes | yes | yes |

Taskiem sends an SSLRequest and finishes the TLS handshake (TLS 1.2 or later) before it sends the user name or any password material. A server that does not offer TLS is refused unless `sslmode` is `disable`.

**Use `verify-full` in production.** Under `require`, the connection is encrypted but not authenticated. An attacker who can intercept traffic could pose as the server. Some logins then send the real password: `caching_sha2_password` on a cold cache, and `mysql_clear_password`. Under `disable`, a `caching_sha2_password` full login fetches the server's RSA key over the plain connection, so an attacker could replace that key too. Use `disable` only on networks you trust.

### Authentication

Supported plugins:

- `caching_sha2_password`, the MySQL 8 default. This covers the fast path (a scramble), and the full path: the cleartext password over TLS, or the RSA-OAEP-encrypted password without TLS.
- `mysql_native_password`.
- `mysql_clear_password`, **over TLS only**. This plugin is used by LDAP/PAM and AWS RDS IAM tokens.

When the server asks for a different plugin (an authentication switch), Taskiem switches. Not supported: `sha256_password`, MariaDB's `client_ed25519` and `auth_gssapi_client`, Kerberos, and the pre-4.1 password scheme. An account using one of these fails with a clear error.

## What to grant

Create one account per purpose. Grant the reading account nothing it could write with. The read-only transaction is a second line of defence, not the first.

```sql
-- For `query` steps only
CREATE USER 'taskiem_ro'@'%' IDENTIFIED BY '…' REQUIRE SSL;
GRANT SELECT ON shop.* TO 'taskiem_ro'@'%';

-- For `execute` steps: only the tables and verbs the workflows need
CREATE USER 'taskiem_rw'@'%' IDENTIFIED BY '…' REQUIRE SSL;
GRANT SELECT, INSERT, UPDATE ON shop.topups TO 'taskiem_rw'@'%';
```

Restrict `'%'` to Taskiem's egress addresses if your network allows. Do not grant `FILE`, `SUPER`, `PROCESS`, `EXECUTE`, `CREATE ROUTINE` or DDL rights unless a workflow needs them.

## Read-only guarantees for `query`

Before your statement runs, Taskiem sends `SET SESSION TRANSACTION READ ONLY` and `START TRANSACTION READ ONLY`. A write is then refused by the server: error 1792, "Cannot execute statement in a READ ONLY transaction". The step fails. Read-only mode also refuses DDL on permanent tables. Taskiem then ends the transaction with `ROLLBACK`. Once more than `max_rows` rows have arrived, Taskiem stops reading and closes the connection, which rolls the transaction back.

Two limits remain:

- **Stored procedures and functions.** A procedure can end the transaction and change the session's mode. It runs with its definer's rights when declared `SQL SECURITY DEFINER`. Do not grant `EXECUTE` to the reading account on routines that write.
- **Temporary tables.** DML on a temporary table is allowed in a read-only transaction. It affects only the step's own session, which is discarded.

The connector also never offers `CLIENT_LOCAL_FILES`. The server therefore cannot make Taskiem read local files through `LOAD DATA LOCAL`. If a server asks anyway, the connection is dropped. The connector never offers `CLIENT_MULTI_STATEMENTS` either, so one `sql` string runs exactly one statement.

## Results

Rows come back as objects keyed by column name. If two columns share a name, the last one wins, so use aliases.

| MySQL type | JSON value |
| --- | --- |
| `TINYINT` … `BIGINT`, `YEAR` | integer. An unsigned `BIGINT` above 2^63−1 becomes a decimal string. |
| `DECIMAL` / `NUMERIC` | **string**, exactly as stored (`"12.50"`). It is never rounded through a float. This differs from the PostgreSQL connector, which turns numerics into numbers. |
| `FLOAT`, `DOUBLE` | number |
| `DATE` | `"2026-10-05"` |
| `DATETIME`, `TIMESTAMP` | RFC 3339 in UTC, for example `"2026-10-05T10:00:00.5Z"`. The session runs with `time_zone = '+00:00'`, so `TIMESTAMP` values come back in UTC. `DATETIME` has no time zone and is shown as stored, labelled `Z`. A zero date comes back as `"0000-00-00 00:00:00"`. |
| `TIME` | `"-26:03:04"` (it can be negative and exceed 24 hours) |
| `CHAR`, `VARCHAR`, `TEXT`, `ENUM`, `SET` | string |
| `BINARY`, `VARBINARY`, `BLOB`, `GEOMETRY` | base64 string, as `bytea` is in the PostgreSQL connector |
| `JSON` | the parsed JSON value. On MariaDB, `JSON` is `LONGTEXT`, so it comes back as a string. |
| `BIT(n)` | integer |
| `NULL` | `null` |

`execute` returns `rows_affected` with the same meaning as PostgreSQL: for `UPDATE`, rows **matched**, even if unchanged (Taskiem sets `CLIENT_FOUND_ROWS`). For `INSERT … ON DUPLICATE KEY UPDATE`, MySQL counts 1 per inserted row and 2 per updated row. `last_insert_id` is the first `AUTO_INCREMENT` value the statement generated, or 0. If the statement returns rows, such as `SELECT … FOR UPDATE`, `rows_affected` is the number of rows.

## Failures and retries

The rules match the PostgreSQL connector's.

| What happened | Outcome |
| --- | --- |
| Egress guard refuses the host | Fails (fatal) |
| DNS failure, connection refused, or an error during login before any statement was sent | Retried (`not_sent`); safe even for `execute` |
| Wrong password (1045), unknown database, TLS refused or certificate rejected, unsupported auth plugin | Fails (fatal) |
| SQL error from the server: syntax (1064), duplicate key (1062), read-only violation (1792), timeout (3024 / 1969), and so on | Fails (fatal), with the server's code, SQLSTATE and message |
| Deadlock (1213), lock wait timeout (1205), too many connections (1040, 1203), server shutting down (1053) | `query`: retried. `execute`: parks for an operator, as an unsafe write does for any retryable error. |
| `execute`: the connection drops or times out after the statement was sent | **Unknown outcome**: the step parks in `needs_reconciliation`. Check the table, then resolve it. |
| `query`: the connection drops or times out | Retried |

`execute` has no idempotency key: MySQL cannot deduplicate a statement on Taskiem's behalf. Make statements idempotent where you can, for example `INSERT IGNORE` or `INSERT … ON DUPLICATE KEY UPDATE` on a unique business key such as a payment reference. Then an operator can safely re-run a parked step.

## Limits

- **Statement timeout: 30 seconds.** On MySQL, the server enforces it with `max_execution_time`, which covers `SELECT` only. On MariaDB, the server enforces `max_statement_time` for every statement. Taskiem gives up on its side 5 seconds later and closes the connection. On MySQL, a slow write can therefore run on after Taskiem has given up. The step is then an unknown outcome and parks.
- **Lock waits: 30 seconds** (`lock_wait_timeout`, `innodb_lock_wait_timeout`).
- **Login: 10 seconds**, including DNS, TCP and TLS.
- **Value size:** one row (or other server message) can be at most 64 MB.
- **Rows:** at most 10,000 rows per `query` step.
- Some statements cannot be prepared (error 1295). Use the equivalent `SHOW`/`SELECT` from `information_schema`, which can.
- The session variables above must be settable. Proxies that reject `SET SESSION` for them are not supported yet. This includes some Vitess/PlanetScale setups.

## To confirm

1. **Run the integration tests against a real MySQL 8 server**, and ideally MariaDB 10.11 too. Set `TASKIEM_TEST_MYSQL_DSN=mysql://user:pw@127.0.0.1:3306/taskiem_test?sslmode=disable` and run `go test ./connectors/mysql/`. The tests so far use a strict in-process fake server, built from the protocol documentation. The integration tests also check that DDL (`CREATE TABLE`) is refused through `query`.
2. Test against the managed services we expect: Amazon RDS / Aurora MySQL (with the RDS CA bundle in `ssl_ca`, and IAM auth through `mysql_clear_password`), Google Cloud SQL, Azure Database for MySQL.
3. Decide whether `require` without `ssl_ca` should keep the libpq meaning (encrypt, do not verify) or verify by default. It sends the password to an unverified server when `caching_sha2_password` needs a full login.
4. Decide whether a client-side timeout on `execute` should also send `KILL QUERY` on a second connection, so a slow write stops instead of running on.
