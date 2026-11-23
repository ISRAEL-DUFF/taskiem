# Your tools

A workflow you start by hand can be a **tool**: something your people call from the chat by a word, like `/expense` or "spent 4,500 on diesel", and that answers there. It is the workflow's own block, `tool` ([the contract](contracts/wd-v1.md#tools), [decision 0073](decisions/0073-made-tools.md)); nothing about how the workflow runs changes.

## What a tool says about itself

| | |
| --- | --- |
| **Name** and **about** | What the tile and the list show: "Log an expense", "Record money spent, with what it was for" |
| **Command** | The word after `/`: `expense`. One workflow's per business and environment, and never one of Taskiem's own words in any language (`invoice`, `run`, `yes`…) |
| **Says** | Up to eight phrases that call it when a message starts with one: "spent", "i bought" |
| **Who it is for** | Your members, or your customers (`for: "customers"`, [below](#customers-tools)). Who among your members may use it is the workflow's **Who can start it** (`roles`): the same rule holds on every way to start it |
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

On the web and in the app, `/expense 4500 diesel` sent in the Taskiem chat opens the tool's form **filled in** from the words (`POST /v1/tools/{command}/read`) instead of going as a message; on the web and in the app a sentence that starts with a phrase exactly one of the person's tools says offers **Open X, filled in** as a chip above the box (the longest phrase wins; a phrase two tools say offers none); pressed, the sheet opens with the words after the phrase read into it, and sent instead, the sentence goes to the chat, which reads it as WhatsApp does. A tool whose input is a scan (a code) has a **Scan** button beside it in the app; on the web the code is typed ([device fields D2](spec/device-fields.md#16-as-built-d2)). The form has a **Fill from words** box at its top that reads more words the same way, and says what it could not ("Nothing the business saved is called @x."). Nothing starts from words: only the form's own **Go**, **Run it** or **Next**.

## Saved things in a tool's form

An input a saved kind fits has the **Saved** button beside it, offering your saved things and the business's of that kind; a pick fills the input. Which kind is read from the input: its `x-saved` hint first (`place`, `person`, `phone`, `email`, `bank_account`, `date`), else its `format` (`phone`, `phone-ng`, `email`, `date`), else the words of its name and title (phone, email, account number, address or deliver to, date or due, customer or contact). Only a text input has one, never a choice, a number or a yes/no. A place fills its address (fitted to the input's length), a person their name, a phone or an email itself, a date as 2027-03-03, and a bank account its number, shown only with your passkey. This is a pick in the form, so your own saved things are offered too, unlike `@name` in words.

## While a run waits

A tool's run that stops for a person says who for, in names and roles only: **Waiting for Ada to approve** (the one person who holds the approval's role, or has it delegated; "you" when that is you; "someone with the role manager" when several do; "an owner" when nobody does), **Waiting for your answer: open To fill in** (a form step put to you, with a link to it on the web), or **Waiting for the cashiers / Bayo Stores / someone outside the business to answer** (a form to a role, a linked business, or a phone's or an email's link, never the number or the address). The answer's `waiting_for` carries it.

## The API

| Call | What |
| --- | --- |
| `GET /v1/tools` | Your tools: command, name, about, inputs, care |
| `POST /v1/tools/{command}/read` | Words in, proposed values out, for a form filled in (`@name` looked up); nothing started |
| `POST /v1/tools/{command}/start` | Values in: the run at once (201, with the echo), or a confirm and what will run (200) |
| `POST /v1/tools/{command}/confirm` | The confirm and the same values (and a passkey for a tool that moves money): the run, once |
| `POST /v1/tools/buttons/{token}` | A button on an answer or a message pressed: the tool it opens and the values it fills in, for whoever pressed it; a once button someone used is 409 with who took it and when, one gone is 404. Its token goes with the start (`button`), and the run it starts spends a once button |
| `GET /v1/runs/{id}/answer` | The run's answer as data, masked as messages are: text, card or list; how it ended while it runs or when it failed; while it runs, `waiting_for` (each open approval's or form's `kind`, `who` in words, `you`, and for a form put to you its `request`) |

An API key may start a tool that only reads; one that asks waits for a person. An `Idempotency-Key` is the caller's own. What the run sealed as personal data is never opened for an answer: it shows masked (•••), and in full only on the run, to whoever may reveal it. What a tool shows is read with the business's connections, not with the rights of whoever calls it: a tool that reads salaries shows them to everyone who may use it. Choose its **Who can start it** with that in mind.

## Buttons

A tool's card and list rows, and a Tell me message (`notify.tell` `buttons`, up to three), carry buttons that open another tool filled in. Each is a row the server mints the first time it is drawn: a random token, kept seven days, read only inside its business, held by the tool's workflow (renaming a command keeps it working). Pressing one (`POST /v1/tools/buttons/{token}`) gives the tool and its values to whoever pressed it, held to its roles; a value that was shown masked is asked rather than filled in, and a button never carries personal data opened. A `once` button is spent by the first run it starts; the same person pressing again gets that run, a run that fails to start gives the button back, and anyone else is told who took it and when. Two presses of one button by one person within ten seconds are one run.

**On a page.** A [page's](pages.md#buttons) buttons, on the page and on each of its rows, open a tool the same way: a row of `tool_buttons` minted for the page, part, row and button when it is pressed (`POST /v1/pages/{name}/act`), its values worked out from the row as the presser sees it (a value they see masked is asked), held to the tool's roles, care and `once`.

**In a chat.** When a tool's run started from WhatsApp, Telegram or Slack ends, its answer comes back there. A card's buttons (three at most) are the chat's reply buttons. A list shows its first ten rows; past ten it says *…and 4 more: see them all in the app* with the run's link, and only those ten rows' buttons are minted. On WhatsApp the list is a list message (**Choose**): a row's title, its sub and state as the description; choosing a row answers with that row's buttons as reply buttons, so even a row with one action is pressed in a second tap, after its words are shown again. Only the person the answer went to can choose a row, in the business it came from, while its buttons last (seven days); anyone else is told the list was sent to someone else. On Telegram and Slack the list is one message, the rows as lines and a button for each row's action, named by the row and its label cut short (*Printer jam · Assign to me*). Pressing any of them is a press as above (the business's members, the tool's roles, `once`). Outside WhatsApp's 24-hour window an answer goes as the run-outcome template with **Open**, without its buttons.

## Customers' tools

A tool with `"for": "customers"` is for the people who buy from you (made tools T5, [decision 0073](decisions/0073-made-tools.md) point 6). Your **public menu**'s entries are customers' tools too: migration 00569 carried each one over, named by its label, with a command made from the label (`Pay a supplier` is `pay-a-supplier`; one that clashes with another entry's, a tool's, or one of Taskiem's own words in any language, or `buttons`, gets `-2`: `Order` is `order-2`), and a new entry gets one the same way. A workflow that declares a tool is that tool instead of its menu entry: a customers' tool for customers, a members' tool for your members alone (its entry is no longer offered). No other tool's command may be a menu entry's (publishing says which), and no tool may be called `buttons`.

| | |
| --- | --- |
| **Where** | Your own WhatsApp number (**menu**, then a number, with the menu switched on), and a person's chat with you in the app and on the web, under the tools button as **Their tools**. Not Telegram or Slack (one bot for everyone), never the shared number |
| **Which** | Your customers' tools deployed in prod, and the menu's entries whose workflow is deployed there and declares no tool. Never one whose steps move money (refused when published, and again when listed and at every start, whatever was deployed), one whose workflow names roles (**Who can start it**: a customer holds none, so a menu entry of such a workflow is not offered, and is refused at every start), or one whose inputs no form carries (nested, or more than ten). What each version allows is worked out once and kept |
| **Asking** | A form only: WhatsApp's form, the app's sheet; never questions in the chat, never words read by the model |
| **Yes** | Always: what will run, then **yes**, whatever the tool's care. Nothing starts before it |
| **Whose run** | The customer's, never a member's: `whatsapp_public:<number>` on WhatsApp, `customer:<person>` in the app, with no roles. In your audit as `run.start` (`for: customers`, the tool, the channel), the number masked; in the app each step through the customer door is audited too (`customer.tools`, `customer.tool`) |
| **Limits** | Your plan's run limits, as any run, and the menu's: three starts, then one every 20 minutes, a customer; 30 a minute for the business. On WhatsApp the menu's message limits come before any tool is looked at; in the app a start, a button and reading an answer are limited for each person and chat |
| **The answer** | Drawn from the outputs your workflow marks `public` and nothing else: the rest are dropped before the answer is worked out, so an answer that names one (at any depth, in a row, in a button's values) shows nothing of it. Masked as messages are. How a run that failed failed, and who it waits for, are never said to a customer: they get "didn't go through" and the reference |
| **Buttons** | Only to your other customers' tools (one to a members' tool is left out), and only for the customer the answer went to, as long as it lasts (seven days). A button fills the tool's form; it never starts it |

**On WhatsApp.** **menu** lists the customers' tools by name; a number sends the form (`taskiem_inputs`); then the summary and **yes**. A tool with an answer says *{tool} has started. Your reference is …; the answer comes here when it is ready*, and the answer comes from your number when the run ends (the number waits beside the run only until then, a day at most, and goes with an erasure of the number): a card's buttons as reply buttons; a list whose rows have buttons as a list message (**Choose**, its first ten rows, *…and 4 more.* over it), a row chosen answering with that row's buttons, for the customer it went to only (anyone else is told the list was sent to someone else); a list without buttons as words. Outside WhatsApp's 24-hour window it is not sent. A menu entry with no answer says *has started* and its reference, as before. A button pressed opens that tool: straight to **yes** when it carries every value, else the form.

**In the app.** A person's chat with you, the tools button: **Their tools** under Their shop, Book and Pay. A tool's sheet: its form, **Next**, what will run and **Yes, go ahead**; then the sheet closes and the answer goes into the conversation, read until the run ends and kept on the phone with it (working on it, the card or list, its words, or that it didn't go through with its reference).

**On the web.** A person's chat with you (You > chats), the tools button: **Their tools** after Their shop, Book, Your orders and Pay. A tool's sheet: its form, **Next**, **Yes, go ahead**, then the answer in the sheet with its reference; a button on it opens the other tool in the same sheet, filled in.

| Call (the person's own, through the customer door) | What |
| --- | --- |
| `GET /v1/me/chats/{id}/tools` | Their tools: command, name, about, icon, inputs, the answer's shape; never care, roles or steps |
| `POST /v1/me/chats/{id}/tools/{command}/start` | Values in: what will run and a confirm (200), never a run; 409 `not_offered`; 404 for a members' tool |
| `POST /v1/me/chats/{id}/tools/{command}/confirm` | The confirm and the same values: the run, once (201; 200 with the same run after) |
| `GET /v1/me/chats/{id}/runs/{run}/answer` | The answer of a run the person started, public outputs only, without the business's command for it. Checked by the door (`customer.answer`), not written to the chain each time it is read |
| `POST /v1/me/chats/{id}/tools/buttons/{token}` | A button on their answer: the tool it opens and its values |
