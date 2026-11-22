# Your tools

A workflow you start by hand can be a **tool**: something your people call from the chat by a word, like `/expense` or "spent 4,500 on diesel", and that answers there. It is the workflow's own block, `tool` ([the contract](contracts/wd-v1.md#tools), [decision 0070](decisions/0070-made-tools.md)); nothing about how the workflow runs changes.

## What a tool says about itself

| | |
| --- | --- |
| **Name** and **about** | What the tile and the list show: "Log an expense", "Record money spent, with what it was for" |
| **Command** | The word after `/`: `expense`. One workflow's per business and environment, and never one of Taskiem's own words in any language (`invoice`, `run`, `yes`…) |
| **Says** | Up to eight phrases that call it when a message starts with one: "spent", "i bought" |
| **Who it is for** | Your members (customers' tools come later). Who among them may use it is the workflow's **Who can start it** (`roles`): the same rule holds on every way to start it |
| **What it says back** | A sentence, a card or a list, drawn from what the run gives back (its outputs) |

## How careful it is

Worked out from its steps when it is published, never chosen by whoever made it:

| | When | What the person sees |
| --- | --- | --- |
| **Answers at once** | Every step that reaches outside only reads | An echo of every value read, then the answer |
| **Asks yes** | Anything else: it writes, sends, asks a person, waits, runs code | What will run, and **Yes** |
| **Asks yes and a passkey** | A step moves money out of the business | **Yes**, then a passkey (on WhatsApp, Telegram and Slack, the link to confirm) |

An owner may make a tool **always ask** (`PUT /v1/tools/{command}/settings`), never less careful than its steps. A tool's run counts against the plan's run limits as any run.

## The API

| Call | What |
| --- | --- |
| `GET /v1/tools` | Your tools: command, name, about, inputs, care |
| `POST /v1/tools/{command}/start` | Values in: the run at once (201, with the echo), or a confirm and what will run (200) |
| `POST /v1/tools/{command}/confirm` | The confirm and the same values (and a passkey for a tool that moves money): the run, once |
| `GET /v1/runs/{id}/answer` | The run's answer as data, masked as messages are: text, card or list; how it ended while it runs or when it failed |

What a tool shows is read with the business's connections, not with the rights of whoever calls it: a tool that reads salaries shows them to everyone who may use it. Choose its **Who can start it** with that in mind.
