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

Some actions outside payments spend the business's money all the same (airtime from its balance): they ask for a passkey too. An owner may make a tool **always ask** (`PUT /v1/tools/{command}/settings`), never less careful than its steps. A tool's run counts against the plan's run limits as any run.

## In words

A tool is called by `/command`, by its plain command, by a phrase it says, or by a sentence: when nothing of Taskiem's own reads a message, the model is told the business's tools you may use (their command, name, about and inputs; never what a saved thing or a run holds) after Taskiem's commands, and may take the message to one of them with the values it heard (made tools M3). It never starts anything: what it read is shown first, as the tool's care says (the echo, **Yes**, or the app), and inputs it did not hear are asked. Two tools that fit are a numbered pick. Taskiem's own words always come first: "send 20k to @bayo" is a payment, never a tool, and a tool cannot take a word Taskiem uses.

`@name` as a value is one of the business's **Saved** things, looked up by Taskiem, not the model: a place gives its address (or its phone, for an input about a phone), a person their name (or phone, or email), a text its words. A bank account, a file, and your own saved things never fill a tool's input; you are told so and asked.

## The API

| Call | What |
| --- | --- |
| `GET /v1/tools` | Your tools: command, name, about, inputs, care |
| `POST /v1/tools/{command}/read` | Words in, proposed values out, for a form filled in (`@name` looked up); nothing started |
| `POST /v1/tools/{command}/start` | Values in: the run at once (201, with the echo), or a confirm and what will run (200) |
| `POST /v1/tools/{command}/confirm` | The confirm and the same values (and a passkey for a tool that moves money): the run, once |
| `POST /v1/tools/buttons/{token}` | A button on an answer or a message pressed: the tool it opens and the values it fills in, for whoever pressed it; a once button someone used is 409 with who took it and when, one gone is 404. Its token goes with the start (`button`), and the run it starts spends a once button |
| `GET /v1/runs/{id}/answer` | The run's answer as data, masked as messages are: text, card or list; how it ended while it runs or when it failed |

An API key may start a tool that only reads; one that asks waits for a person. An `Idempotency-Key` is the caller's own. What the run sealed as personal data is never opened for an answer: it shows masked (•••), and in full only on the run, to whoever may reveal it. What a tool shows is read with the business's connections, not with the rights of whoever calls it: a tool that reads salaries shows them to everyone who may use it. Choose its **Who can start it** with that in mind.
