# Menus in the chat: showing people what they can do

Status: **plan, settled** (21 November 2026), written with the founder, who took every recommendation in §2. Nothing here is built. It follows [the model's help with a message nothing recognised](../languages.md) (PR #217, built 20 November 2026), which it extends.

Builds on: [chat tools](chat-tools.md) (the tools panel, the tool sheets, the forms), [business chat tools](business-chat-tools.md), [the phone app's look](mobile-app-look.md) (Direction B), [saved objects](saved-objects.md) (`@name`), [Schools in the chat](schools-in-the-chat.md), [languages](../languages.md) (the word lists, the model's routing).

## 1. Why

A business runs Taskiem from a chat by typing commands: *sold 3 bags of rice 45,000 cash*, *add rider Musa 0803 123 4567 Ikeja*, *adjust rice -2 spoilt*. There are well over a hundred, and more come with every feature. Until 20 November a message Taskiem did not recognise was answered with all of them, up to 29 dense lines. Since PR #217, the model reads that list for the person and works out the one command they meant, asks for what is missing, or says Taskiem cannot do it.

That helps when the model can name one command. When it cannot, the person is still left with words to guess. Someone who types "my rider issue" or "stock thing" does not want to be told what to type; they want to be **shown what they can do about riders, or stock, and pick**.

So the chat answers with **menus**: the actions of a topic, as rows to tap. Each row shows something at once, opens the form for it already filled in with what the person said, or asks for what it needs one question at a time. The typed command is shown small under each row, for those who want to learn it, and never needed.

## 2. Decisions

The founder took each recommendation (21 November 2026):

1. **When the model is sure of one action and has all it needs, it acts as now**: shows the result, or asks Yes first where the action would change something without asking by itself (PR #217). The menu is for when it is not sure.
2. **Help becomes the menu of topics, on every channel.** *help* and a topic's word alone (*riders*, *stock*) open menus; *help all* still sends the whole list in words.
3. **An action with no form yet asks step by step**, in the chat, on every channel. Forms for those actions come in the last phase (§12, M4) and replace the questions in the app and on the web.
4. **This spec is settled before any building.**

## 3. The catalogue of actions

Today the commands are written down three times: the help's lines (`waHelp`, `wa.help.*`), the code that reads typed words, and the tools panel's tiles (`chatTools`, `schoolTools`; `mobile/lib/chat/tools.dart`, `web/src/lib/tools.ts`). They drift.

There is to be **one catalogue** (`api/chat_actions.go`), and everything comes from it: help in words and as a menu, the model's list, the menus, the tools panel's tiles, and WhatsApp's and Telegram's menus. A new feature adds one entry and appears everywhere.

An **action** has:

| Field | What it is |
| --- | --- |
| `id` | Stable, short: `riders.add`, `stock.adjust` |
| `topic` | The topic it is listed under (§4); an action may be listed under a second topic (selling is in Sales and in Stock) |
| `title` | What the row says, in the person's language: "Add a rider" (24 characters at most, WhatsApp's limit) |
| `line` | One line under the title: "Name, phone and the areas they cover" (72 at most) |
| `icon` | One of the look's icons (`mobile/lib/theme/look_icons.dart`, its web twin) |
| `perm`, `area` | Who may use it, and the part of Taskiem it belongs to: a row the person may not use, or of a part the business turned off, is not shown (as `waHelp` and `chatAreas` do now) |
| `does` | `show` (only shows something), `asks` (asks before it changes anything: a money entry, an invoice, a run) or `changes` (changes something when done) |
| `shortcut` | The typed command, as the help gives it: "add rider Musa 0803 123 4567 Ikeja" |
| `opens` | How it is done: `show` (the answer, at once), `form` (a form in the app and on the web, by key), `questions` (asked step by step, §7), or `page` (a page on the web, by path) |
| `fields` | For `questions` and to fill a form: each value it needs, its kind (text, money, number, phone, a choice from a list, a product, a customer, a rider, a date), whether it is needed, and its question |

A shortcut starting with `/` opens a tool as *tools* does today (`waToolsCommand`); an action with no typed command yet ("none yet" in §4) has no shortcut line on its row. A test holds the catalogue to the code: every action's shortcut is read by the chat as that action, and every command the chat reads has an entry. A command with no entry fails the build.

## 4. The topics and their actions

The first catalogue, from what Taskiem does today. *Form* names the form the app and web already have; *questions* are asked step by step until M4.

**Sales**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Record a sale | changes | form: Sell | *sold 3 bags of rice 45,000 cash* |
| Today's sales | show | show | *how did we do today* |
| How we did this week | show | show | *how did we do this week* |
| Hand over a shift | changes | form: Handover | */handover* |

**Stock**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| See what's low | show | show | *stock* |
| How many of an item | show | show | *stock rice* |
| Add stock that came in | changes | form: Stock in | *add 50 cartons of Indomie at 8,500* |
| Correct a count | changes | questions | *adjust rice -2 spoilt* |
| Count the shelf | changes | questions (item by item, as now) | *count rice 12, indomie 40* |
| Record a sale | changes | form: Sell | (as in Sales) |

**Products and your page**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Add a product | changes | form: Product | *add product Jollof rice 2000* |
| Change a price | changes | form: Product | *price jollof rice 2500* |
| Hide or show a product | changes | questions | *hide jollof rice* |
| Add a service | changes | form: Service | *add service Haircut 30 2000* |
| Price lists | show | form: Price list | */prices* |
| See your page, and its QR code | show | show | *my page*, *qr* |
| Opening hours | changes | questions | *hours mon-sat 8-6* |
| Close or open the shop | changes | questions | *close shop* |
| Other pages (menus) | show | page | *fronts* |

**Orders**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Open orders | show | show | *orders* |
| One order | show | questions (which) | *order TK-0042* |
| Mark ready or delivered | changes | questions (which, then which) | *order TK-0042 ready* |
| Refund an order | changes | questions | *refund TK-0042* |

**Deliveries and riders**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Your riders, and who's free | show | show | *riders* |
| Add a rider | changes | questions (form in M4) | *add rider Musa 0803 123 4567 Ikeja* |
| Give an order to a rider | changes | questions (which order, which rider) | *order TK-0042 to Musa* |
| Take an order back | changes | questions | *release TK-0042* |
| Today's deliveries | show | show | *deliveries* |
| Offer every order to riders | changes | questions (Yes) | *turn on deliveries* |

**Bookings**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Today's bookings | show | show | *bookings*, *today* |
| Move a booking | changes | questions | *move TK-0042 to Sat 11:00* |
| Cancel a booking | changes | questions | *booking TK-0042 cancel* |
| Take a day off | changes | questions | *off 25 Dec* |

**Buying from suppliers**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Your suppliers | show | show | *suppliers* |
| Add a supplier | changes | questions (form in M4) | *add supplier Dangote Depot 0803 123 4567 for rice* |
| Order from a supplier | changes | questions | *order from Dangote* |
| Goods received | changes | questions | *received 50 cartons of indomie* |
| Pay a supplier | changes | questions | *paid Dangote 425,000 transfer* |
| Your bills | show | page: Bills | */bills* |

**Money**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Record money spent | asks | questions | *spent 12,000 on diesel* |
| Cash in or out | asks | questions | *cash in 20,000 till* |
| Money this week | show | page: Money | */money* |
| Send an invoice | asks | questions | *invoice Ada 25,000 for 5 bags of rice* |
| Your invoices | show | page: Invoices | */invoices* |

**Customers who owe you**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Who owes you | show | show | *who owes me* |
| Someone owes you | changes | questions | *Ada owes 5,000* |
| Someone paid | changes | questions | *Ada paid 2,000 cash* |
| Send a customer their balance | changes | questions | *send Ada her balance* |

**Staff and tasks**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Your tasks | show | show | *my tasks* |
| Give someone a task | changes | form: Give a task | *@Tunde count the float by 7am* |
| Waiting for your approval | show | show | *approvals* |

**Your lists (tables)**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Your lists | show | show | *tables* |
| Look something up | show | questions | *find customers Ada* |
| Add to a list | asks | form: Row | *add to customers* |
| Ask a question about them | show | questions | *how many customers in Ikeja* |
| Make a new list | changes | questions | *new table* |

**Automations**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| What ran today | show | show | *status* |
| Run an automation | asks | questions (which) | *run Salary reminders* |
| Automate something new | asks | questions | *build every Friday text my customers who owe me* |
| Connect a service | changes | questions | *connect Paystack* |

**Saved things**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Your saved things | show | page | none yet |
| Save something | asks | questions | *save mum: GTBank 0123456789* |
| Share a saved thing | asks | questions | *share @our-account with Ada's Kitchen* |

**School** (for those who read the school, as the tools panel's school tiles)
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Students | show | page | */students* |
| Record a payment | asks | form (the app's) | *cash 50k from Mrs Bello for Tunde* (when built: [Schools in the chat](schools-in-the-chat.md) §1) |
| Who owes, by how late | show | page | none yet |
| Clearance | show | page | none yet |

**Your account**
| Action | Does | Opens | Shortcut |
| --- | --- | --- | --- |
| Switch business | changes | questions (a choice) | *switch Ada's Kitchen* |
| Language | changes | questions (a choice) | *language Yoruba* |
| Sign in on the web | show | show | *sign in* |
| Your plan | show | show | *plan* |

Message a linked business (*... @Dangote how much is a bag of cement?*) and voice notes are not actions: they stay as the help says them, in a line under the topics menu.

Topics are shown in this order, with those the person cannot use at all left out. Topics a person uses often may later come first (§13).

## 5. When a menu is shown

1. **help**: the menu of topics (§6). *help all*: the whole list in words, as today. *help stock* or *help riders*: that topic's menu.
2. **A topic's word alone**: *riders*, *stock*, *money*, *suppliers*. Where the word is already a command that shows something (*riders* lists them), the answer comes first and the topic's menu under it.
3. **A message nothing recognises** goes to the model (§8). If it is sure of one action and has its values, it acts as now (decision 1). If it knows the topic but not the action, or the action but not its values, the answer is that topic's menu, with what it understood filled in (§6, §7). If nothing fits, one line saying what Taskiem cannot do, then the topics menu.
4. **Without the model** (not on the plan, over the budget or the limit): the topic is found by words (§8.3) and its menu shown; with no topic found, "Sorry, I did not understand that." and the topics menu. Nobody is left with a line of text and nothing to press.

A menu never does anything by itself: it is only choices.

## 6. The menu in the Taskiem chat (app and web)

A **menu card** in Taskiem's conversation, drawn in the app's own look (Direction B) on the phone and as the web chat's cards are:

- **A head:** the topic's icon on its colour, its name ("Riders"), and when the model sent it, one plain line of what it understood ("About riders. What would you like to do?").
- **Rows,** as the tools panel's rows (`TkListRow`; the web's `.tk-row`): the action's icon on its colour, its title, its line, and under it, small and muted, the shortcut ("or type *add rider Musa 0803… Ikeja*"). A row with values from the message says so: "Add a rider: *Musa*".
- **A foot row,** "All topics", which turns the card into the topics menu in place.
- **The topics menu** is the same card with one row a topic: its icon, its name, and its first three actions in its line ("Add stock, correct a count, count the shelf").

**Pressing a row:**

| The row | What happens |
| --- | --- |
| `show` | The answer appears in the chat as if the shortcut were typed (cards where the answer has them: a rider list, low stock, an order) |
| `form` | The form opens as a sheet over the chat (the same sheet the tools panel opens), its fields filled with what the message gave. Its own Save does it, and the result is said in the chat as now |
| `questions` | The first question appears in the chat (§7) |
| `page` | The page opens (in the app where it has the screen; else the web, as `openToolPage` does) |

Once a row is pressed, the card **folds** to one line ("Riders: Add a rider") with "Show again" to unfold it, as tagged cards fold (17 November 2026). A newer answer makes the card's rows unpressable, as Taskiem's buttons are now (`clearLiveButtons`). In the history the card is kept by its topic and rows' titles only, as buttons are (`keepHistory`).

The card passes the app's rules (`mobile/CLAUDE.md`): light and dark, 340 to 390 wide, large text with nothing cut off, every row 48 px and labelled for screen readers ("Add a rider. Name, phone and the areas they cover. Button."). On the web it passes axe at 390 and 360 in both themes.

## 7. Step by step: questions in the chat

An action that opens `questions` asks for its fields one at a time, in the chat, on every channel. Nothing new is written: the answers **make the action's own command**, and the chat reads it as typed, with its permissions and confirmations as now.

1. Fields the message already gave are taken, said back ("Adding a rider: Musa."), and not asked.
2. Each missing field is asked with its question: "What's Musa's phone number?" A field that is a choice (a rider, an order, a product, an area, a business) comes as a menu of the choices (the five most likely first, then "Something else" to type it).
3. A field the person may leave out says so ("Which areas does Musa cover? Send *skip* if any").
4. An answer is checked as the command would check it (a phone that is not a phone, an amount that is not money), and the question is asked again with why, at most twice, then "Let's leave it for now" and the topic's menu.
5. With everything, the command is made and read as typed. An action that `changes` shows it first and waits for **Yes** (decision 1; PR #217's confirmation, the same card): "Add rider *Musa*, *0803 123 4567*, covering *Ikeja*? Yes / No". One that `asks` already asks by itself.
6. *cancel* at any time drops it ("Left it."). Any message that does not answer the question lets it go and is read as a new one, as a found row is now.

The questions are kept as the session's state (`chatData`, as a debt or a money entry is now), with the usual time limit (`waPendingTTL`).

## 8. The model, and finding a topic without it

### 8.1 What the model reads

Instead of the help's lines, the model is given the catalogue, the person's own (their permissions and areas): each topic, and each action's id, title, line and shortcut. It answers one of:

| Kind | With | Then |
| --- | --- | --- |
| `action` | an action's id and the values it understood (`{"name": "Musa", "phone": "0803 123 4567"}`) | All needed values: done as now (decision 1). Some missing: the action's form opens filled in (app and web), or its questions start with those taken (§7) |
| `topic` | a topic's id, and any values | The topic's menu, with what it understood on the rows it fits |
| `ask` | one short question | As now: the answer is read with the message |
| `cannot` | one or two sentences | As now, then the topics menu |

Ids and values are checked: an id not in the person's own catalogue, or a value not of its field's kind, is refused, as today's answers are. The model still never confirms: an `action` that `changes` shows its Yes first, whatever the model says.

The other rules stay as built: the prompt redacted, the AI budget, 30 an hour a chat, the call recorded as `understand` without the words, a command from the model read once and never sent back to it.

### 8.2 Values into a form

An action's `fields` name the form's fields. The app and the web open the form with them (`initial` values, as `TaskForm` takes a person and a due time now). A value the form cannot take (a product that is not in the list) is left empty for the person, never guessed.

### 8.3 A topic by its words, without the model

Each topic has **words** in each language's word list (`engine/lang/intents`, drafts for the four languages, like the rest): *rider, dispatch, delivery, okada, bike* for riders; *stock, carton, bag, count, shelf, finished, remain* for stock; *spent, money, cash, transfer, expense* for money; and so on. A message nothing recognises, with no model to ask, that has one topic's words gets that topic's menu. With words of several, the topics menu with those topics first. The words are matched as the chat's other words are (without accents or tone marks, the person's language first, English always).

## 9. On WhatsApp, Telegram and Slack

The same menus, in each app's own way. A row's id is the action's (`act:riders.add`), and a press is read as the row would be in the Taskiem chat: shown, asked step by step, or a link to the form on the web, filled in (as the tool requests' links are now, `chat_tool_requests.go`).

| | Topics menu | A topic's menu | A form |
| --- | --- | --- | --- |
| **WhatsApp** | A **list message**: the text, and one button ("Choose") opening the rows; up to 10 rows in all, in sections. More than 10 topics: the 9 most used, then "More…" | A list message: the actions, each with its title (24 characters) and line (72) | Questions in the chat; a link to the web's form for one that has a form and many fields (products with photos) |
| **Telegram** | A button for each row under the message (inline keyboard, one row each) | The same | As WhatsApp |
| **Slack** | Buttons in the message (Block Kit), five at most, then "More…" | The same | As WhatsApp |

Taskiem's WhatsApp client sends only reply buttons today (`SendButtons`, three at most): **list messages are new** (`SendList` in `engine/whatsapp/graph.go`, the reply read as a button's is now). Lists, like buttons, can be sent only inside WhatsApp's 24-hour window; outside it Taskiem's answers are templates anyway.

On every channel a row can also be chosen by its **number** ("2"), so a menu still works where an app shows it as text.

## 10. What changes for the API

- `GET /v1/chat/actions`: the person's own catalogue (topics, actions, fields), for the tools panel and the composer's `/` list; cached with an ETag, as the kept pages are (K4).
- A Taskiem message gains `menu`: `{topic, title, line, rows: [{id, title, line, icon, shortcut, opens, form, page, values}], folded}`. The web and the app draw it (§6); other channels get it as §9 says.
- Pressing a row: `POST /v1/chat` with the row's id as a button's is now (`act:<id>`, with `values` for a form row opened and closed without saving: nothing is sent).
- `chat_understand.go`: the model's answer kinds of §8.1; the catalogue as its list.
- `waHelp` is made from the catalogue; *help* answers with the topics menu (§5).
- OpenAPI, the web's and the app's generated clients (`mobile/api.txt`, `tools/mobilegen`), and the docs (`docs/languages.md`, `docs/spec/chat-tools.md`) follow.

## 11. What the person never meets

No command they must learn; no list of 29 lines unless they ask for it (*help all*); no technical words in a title, a line or a question (Appendix C of [no technical steps](no-technical-steps.md), checked by the same test as the help's lines); no row they cannot use; no change without their Yes or the form's Save.

## 12. Phases

| Phase | What | Done when |
| --- | --- | --- |
| **M1** | The catalogue (§3, §4), with the test holding it to the code; `waHelp` made from it; `GET /v1/chat/actions`; the menu card in the app and on the web (§6); *help*, *help all*, *help riders*, a topic's word alone (§5); rows that show, open the existing forms, or open pages; step by step questions (§7) for the rest | Every topic's menu, in the app and on the web, light and dark, both widths, with every row doing what §6 says; questions for every `questions` action; tests for each (server, web e2e, the app's screen tests and pictures) |
| **M2** | The model answers with `action` and `topic` and values (§8.1); forms filled in (§8.2); topics by words without the model (§8.3) | A message about riders the model is unsure of shows the riders menu; "add rider Musa" opens the questions with Musa taken; with the model off, "my rider issue" shows the riders menu |
| **M3** | WhatsApp list messages, Telegram's buttons, Slack's blocks, numbers on every channel (§9) | Each channel's tests: the topics menu, a topic's menu, a row pressed, questions to the end, a link to a form |
| **M4** | Forms for the actions that ask step by step today and are better as forms: Add a rider, Add a supplier, Correct a count, Order from a supplier, Opening hours, Someone owes you, Record money spent, Send an invoice. In the app and on the web they replace the questions; on WhatsApp and Telegram the questions stay | Each form built in the look ([mobile-screen](../../.claude/skills/mobile-screen/SKILL.md)), filled from the model's values, with the web's twin |

Each phase is its own pull request and leaves the chat working: M1 alone already ends the long list and the dead end.

## 13. Later, not now

- **Topics the person uses most first**, from what they pressed and typed in the last month.
- **A row's last values** ("Add stock: 50 cartons of Indomie, again?").
- **A menu in a customer's own chat with a business** (order, book, pay, my orders): customers have the door's buttons today ([Taskiem for everyone](taskiem-for-everyone.md) §8.4); a menu there is its own plan.
- **Voice**: a voice note's transcript is read as typed, so it gets menus already; a spoken menu is not planned.
