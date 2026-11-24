# Reaching your own network

Status: **built** (decision [0013](../decisions/0013-private-network-access.md), mechanism 1: the SSH tunnel). The outbound relay and operator-allowed ranges, mechanisms 2 and 3, are not built.

Taskiem's workers refuse to connect to private, loopback and link-local addresses (spec 14.2): that rule keeps one business's workflow away from the platform's own network and from every other business. It also means that equipment on a business's own network, such as a router with a REST API, a RADIUS or billing panel, a database on `10.20.0.5`, cannot be called directly. A **tunnel** is how a business reaches its own hosts without opening that door for anyone else.

## How it works

```
Taskiem worker ──(public internet, egress guard)──▶ your bastion ──(your network)──▶ 10.0.0.1:443
```

- You run a **bastion**: a server on your network that runs OpenSSH and has a public address. Anyone may know the address; only Taskiem's key can sign in.
- The worker dials the **bastion**, through the same egress guard as any public host. The bastion must be a public address; a private one is refused before any dial.
- The worker checks the bastion's **host key** against the one you pinned. A connection with no pinned key, or the wrong one, fails *before* any credential is sent, and the error shows the key the bastion presented, so you can check it and pin it.
- It signs in with the connection's private key and asks for **one** `direct-tcpip` channel, to the host and port the connection names. No shell, agent forwarding or other channel is ever requested.
- The destination must still be the host the connection names: a workflow cannot make a connection reach any other address.
- The tunnel lives for **one call**. Closing the connection, or the call ending, times out or being cancelled, closes the SSH session.
- The private key is sealed like any other secret and is decrypted only inside the worker, for the call. It never appears in a run's history.

## Setting up the bastion

On any machine in your network that runs OpenSSH:

1. Make a user for Taskiem, for example `taskiem`, with no password and no shell you care about.
2. Make a key pair (`ssh-keygen -t ed25519 -f taskiem-key`) and put the **public** key in that user's `~/.ssh/authorized_keys`, limited to the one destination it may open:

   ```
   restrict,port-forwarding,permitopen="10.0.0.1:443" ssh-ed25519 AAAA… taskiem
   ```

   `restrict` turns off everything else (shell, agent and X11 forwarding); `permitopen` allows only that `host:port`. Add one line per destination, or one key per destination.
3. Find the bastion's host key fingerprint: `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`. It begins `SHA256:`.
4. Make the bastion reachable on the public internet on its SSH port. If you restrict who may connect by address, ask Taskiem's support which addresses to allow.

## Connections that can use a tunnel

Add the **tunnel fields** to the connection; leave them empty to connect directly, as before.

| Field | |
| --- | --- |
| `tunnel_host` | The bastion's public host. Setting it turns the tunnel on |
| `tunnel_port` | Its SSH port (default 22) |
| `tunnel_user` | The user, for example `taskiem` |
| `tunnel_private_key` | The private key (PEM or OpenSSH format); sealed |
| `tunnel_passphrase` | If the key has one; sealed |
| `tunnel_host_key` | The bastion's host key: a `known_hosts` or `authorized_keys` line, or a `SHA256:` fingerprint. **Required.** Leave it empty once and the error shows the key to pin |

These fields exist on the **MySQL**, **PostgreSQL** and **SFTP** connections, and on the **Your own network (HTTP)** connection below. The destination is the connection's `host` and `port`, as it is for a direct connection.

## Your own network (HTTP): `privatehttp@1`

A connector for web addresses that exist only inside your network. The connection holds the equipment's `host` (and optional `port`, `scheme`, sign-in and certificate settings) and the tunnel; the actions are:

| Action | Class | What it does |
| --- | --- | --- |
| `get` | read | `GET` a path, with optional query and headers. Returns `status`, `ok`, a few headers, `body` (parsed when JSON) and `truncated` |
| `send` | unsafe write | `POST`, `PUT`, `PATCH` or `DELETE`, with a JSON or text body |

- A 4xx answer, such as 404, is **returned**, not failed, so a workflow can branch on it. A 5xx on a read is retried; a 5xx on a write parks the step for a person (the equipment may have applied it). `429` and `408` are safe to send again.
- Redirects are never followed. The path must be a path on the equipment, not another address. `Host`, `Authorization` and `Content-Length` cannot be set by a step.
- Sign-in: a `token` (sent as `Authorization: Bearer …`) or a `username` and `password` (basic). Equipment with a self-signed certificate: set `skip_tls_verify` to `yes` (the way there is the SSH tunnel), or give the `ca_cert` that signed it.
- Answers are cut at `max_bytes` (default 256 KiB, at most 1 MiB).

Use it in place of the `http` step for anything on a private address, for example the suspend and reconnect steps of the [subscription recipes](../guide/bill-subscribers.md).

## What it does not do

- It does not reach a host through a bastion the business does not run: the bastion is the business's.
- Pooling: each call opens its own tunnel and closes it. A busy workflow opens many SSH sessions to the bastion; keep `MaxSessions` and `MaxStartups` on the bastion generous enough.
- The outbound relay (a process in your network that dials out to Taskiem, with no inbound port) is not built. Until it is, the bastion must accept SSH from the internet.
