# Reach your own network

Some of your equipment lives only inside your own network: a router, a billing or RADIUS panel, a database on a private address. Taskiem cannot call those directly, and that is deliberate: it keeps every business's workflows away from everyone else's networks. A **tunnel** lets Taskiem reach yours, and only yours, through a small server you run.

You need someone who looks after your network for the first part.

## What you set up once

1. **A bastion.** Any machine on your network that runs OpenSSH and can be reached from the internet on its SSH port. It is the one door Taskiem knocks on.
2. **A user and a key for Taskiem** on that machine. The key is allowed to open a connection to **one** address and nothing else (your network person knows this as `permitopen`). It cannot run commands.
3. **The bastion's fingerprint**, so Taskiem knows it is really your machine and never sends the key anywhere else.

The full steps, with the exact lines to paste, are in [Reaching your own network](../integrations/private-network.md).

## Connecting the equipment

Go to **Connections**, choose **Your own network (HTTP)** (or MySQL, PostgreSQL or SFTP for a database or file server), and fill in:

- the equipment's address inside your network, for example `10.0.0.1`;
- how it signs people in (a token, or a username and password);
- the bastion's address, user, key and fingerprint.

Leave the fingerprint empty the first time. Taskiem shows the one it saw; if it matches the one you wrote down, paste it in and save. If it ever changes without you changing the bastion, the connection stops and tells you, instead of sending your key to something else.

Then use the connection in a workflow in place of **Call a web address**: for example, in **Switch off the expired** the step that disables a customer can call your own panel.

## What to know

- A connection reaches the one address you named, and only through your bastion.
- A step that changes something on your equipment (turning a customer off) cannot be asked "did you already do this?". If Taskiem does not hear the answer, it stops and asks a person rather than doing it twice.
- Equipment with a certificate it signed itself: tick **yes** for the self-signed setting. The way there is already protected by the tunnel.
