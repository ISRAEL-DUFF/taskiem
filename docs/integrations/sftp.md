# SFTP integration

The connector is `connectors/sftp` (`sftp@1`). It moves files on an SFTP server (SFTP version 3, as OpenSSH serves it). The SSH protocol comes from `golang.org/x/crypto/ssh` and the SFTP protocol from `github.com/pkg/sftp` (BSD-2-Clause). Semantics follow the public specifications listed at the end, read on 6 October 2026. Every connection goes through the engine's egress-guarded dialer (`connector.Request.Dial`); the connector never dials directly.

## Connection

| Field | |
| --- | --- |
| `host` | The server's host name. It is the only host the connector may reach (`${connection.host}`), so it needs no separate allow-list entry |
| `port` | Defaults to 22 |
| `username` | |
| `password` | Password authentication. Also answers keyboard-interactive password prompts |
| `private_key` | Public-key authentication: an OpenSSH (`-----BEGIN OPENSSH PRIVATE KEY-----`) or PEM key. Pasting it with literal `\n` sequences is accepted |
| `private_key_passphrase` | For an encrypted key |
| `host_key` | **Required.** The server's host key (see below) |

At least one of `password` and `private_key` is needed. With both, the key is tried first.

## Host key pinning

SSH proves the server's identity with its host key. Without a check, anyone able to intercept the connection could impersonate the server and receive the files and the password. The connector therefore **always** verifies the key against `host_key` and has no "accept any key" mode.

`host_key` accepts one or more lines, any of which may match (useful while a server rotates keys):

- an `authorized_keys`-style line: `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... comment`
- a `known_hosts` line: `sftp.example.com ssh-ed25519 AAAA...`. The host name in the line is not checked, because the pin belongs to this connection. `@cert-authority` and `@revoked` lines are refused.
- the base64 key alone
- a fingerprint as `ssh-keygen -l` prints it: `SHA256:Jx1...` (trailing `=` padding is ignored)

**Getting the key.** Save the connection once with `host_key` empty and run any step, or the connection test (`stat` of the login directory). The connector completes the key exchange and then stops before sending any credential. The step fails with a message such as:

> the connection has no host_key, so the server was not trusted and no credentials were sent. The server presented ssh-ed25519 SHA256:Jx1…. Confirm this fingerprint with the server's administrator, then set host_key to: ssh-ed25519 AAAA…

Check the fingerprint with the server's administrator before pasting it: `ssh-keygen -l -f /etc/ssh/ssh_host_ed25519_key.pub` on the server, or the provider's documentation. A key that does not match fails every step with "host key mismatch" and the presented fingerprint, again before any credential is sent.

When `host_key` holds full keys, the connector asks the server for those key types, so a server with several host keys (for example RSA and Ed25519) presents the pinned one. A fingerprint pin does not say its type, so the server picks one. If it picks a different type, pin the full key instead.

## Actions

| Action | Class | Notes |
| --- | --- | --- |
| `upload_file` | idempotent write | Text or base64 `content`, at most 10 MiB. `create_dirs` (mkdir -p), `overwrite` (default true) and `mode` (e.g. `0640`). Atomic, see below |
| `download_file` | read | `encoding` `text` (must be UTF-8) or `base64`. `max_bytes` defaults to 1 MiB, at most 10 MiB. A larger file fails the step rather than being cut short. Returns its SHA-256 |
| `list_directory` | read | Sorted by name. `max_entries` defaults to 1000, at most 10000; `truncated` says whether more entries exist |
| `stat` | read | A missing path returns `exists: false`. Also the connection test |
| `rename` | idempotent write | Moves a file or directory. `overwrite` (default false) and `create_dirs` |
| `delete` | idempotent write | A file or an empty directory. With `missing_ok` (default true), a missing path is not an error |
| `mkdir` | idempotent write | `parents` (default true). An existing directory is not an error |

**Atomic upload.** The content is written to a hidden temporary file in the same directory, `.<name>.<engine key>.part`. The connector checks its size and then renames it onto the destination. A reader therefore sees either the old file or the whole new one, never part of one. If a step fails, the connector removes the temporary file, and the destination is left untouched. The temporary name comes from the engine's idempotency key, so a retry reuses its own leftover. Replacing an existing file uses OpenSSH's `posix-rename@openssh.com` extension, an atomic replace. SFTP v3's own rename refuses an existing target. On a server without the extension, the connector deletes the old file and then renames, and reports `atomic: false`.

**Why the writes are idempotent.** The engine may repeat a write after an unknown outcome (the connection dropped mid-operation):

- Repeating an upload of the same content leaves the same file. With `overwrite: false`, finding the destination already holding exactly this content counts as success (`unchanged: true`); other content fails the step.
- A repeated rename that finds the source gone and the destination present reports `already_done`.
- Delete and mkdir end in the same state however often they run.

The manifest declares the engine key as the input field `idempotency_key`, which the contract requires. `upload_file` uses it for the temporary file name; the other actions only log it.

### Errors

| Failure | Step |
| --- | --- |
| Connection refused, DNS failure, server hangs up during the SSH handshake | Not sent: retried, safe for every class |
| Egress refusal, no `host_key`, host key mismatch, authentication failure, no shared algorithm, no SFTP subsystem, encrypted key without passphrase | Fatal |
| The server answers no such file, permission denied, or failure | Fatal |
| Connection lost during an operation | Retryable for reads; unknown outcome for writes, which the engine retries because the writes are idempotent |

## Limits

- 10 MiB per upload and download. Step inputs and outputs live in the run's history. Larger transfers belong in a dedicated job.
- A new SSH connection is made for every step. The handshake times out after 30 s, or at the step's deadline if that is sooner.
- Paths are server paths. Relative paths start at the login directory. `delete` refuses `.` and `/`, and directories are removed only when empty.
- `list_directory` reads the whole directory before trimming it to `max_entries`.
- The server must be reachable on a public address. The egress guard refuses private, loopback and link-local addresses.

## To confirm before go-live

1. **Getting the fingerprint.** To report the fingerprint, the connector performs the SSH key exchange with an unpinned server. It sends no username or credential. Confirm this matches the security review's reading of "refuse to connect without a host key". The alternative is to refuse before dialing and leave fingerprint discovery to the administrator.
2. **Servers without `posix-rename@openssh.com`.** Replacing a file there is delete-then-rename, so a reader can briefly see no file. Decide whether to refuse `overwrite` on such servers instead.
3. **Hosted SFTP services** (AWS Transfer Family, Azure Blob SFTP, and others) may not support `chmod`, `posix-rename` or rename onto an existing name. Test `upload_file` with `overwrite` and `mode` against each provider in use.
4. **Paths with non-UTF-8 bytes** are not supported. SFTP v3 does not specify a filename encoding, and the connector sends UTF-8.
5. **Engine.** `${connection.host}` resolves to the `host` credential only. Port numbers are matched separately, which works. A server that needs a jump host is not supported.

## Sources (read 6 October 2026)

- SFTP v3: https://datatracker.ietf.org/doc/html/draft-ietf-secsh-filexfer-02
- SSH: https://datatracker.ietf.org/doc/html/rfc4251 (architecture, host keys), https://datatracker.ietf.org/doc/html/rfc4252 (authentication), https://datatracker.ietf.org/doc/html/rfc4253 (transport), https://datatracker.ietf.org/doc/html/rfc4256 (keyboard-interactive)
- known_hosts format: https://man.openbsd.org/sshd.8#SSH_KNOWN_HOSTS_FILE_FORMAT
- OpenSSH extensions (`posix-rename@openssh.com`): https://github.com/openssh/openssh-portable/blob/master/PROTOCOL
