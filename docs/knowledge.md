# What your assistant knows

A business's assistant answers customers from the business's own facts: its hours, where it delivers, what it sells and for how much, how returns work ([decision 0062](decisions/0062-customer-assistant.md) D12, [plan V5b](spec/voice-assistant.md#v5b-knowledge-sources-d12)).

The facts come from **knowledge sources**, inside Taskiem and outside it. Each source is read as text, split into short passages, and searched per question, so a reply sends the model only the passages that answer it. The owner manages them on **What your assistant knows** (`/knowledge`, people with `secret.manage`).

## Sources

| Kind | What it reads | Refreshed |
| --- | --- | --- |
| **About my business** (`about`) | What the owner writes here. A line starting with `#`, or a short line ending with a colon (`Delivery:`), starts a part | When the owner saves it |
| **My storefront** (`storefront`) | The storefront's name, address, phone, area and opening hours, and what it sells with its prices, availability and descriptions | Daily |
| **A table** (`table`) | A data table's rows, with the columns the owner picks. Its personal columns (and its phone and email columns) are never read, whatever is picked. Tables kept by Taskiem (the money log, the school copies) cannot be sources | Daily |
| **My website** (`website`) | The business's own website: only one it has proved is its own ([embed sites](embed-sites.md)), checked again on every read. Read from its first page, following links on that origin only, up to 50 pages (20 by default) | Daily |
| **A document** (`file`) | A PDF or Word (.docx) document the owner uploads. Only its text is kept, never the file. A scan with no text in it is refused in plain words until OCR exists | When uploaded again |
| **A Google Sheet** (`sheet`) | A range of a sheet through a `googlesheets@1` connection. The first row names the columns | Daily |
| **My books** (`books`) | The items and prices in the business's books, through a connection whose connector lists them (`books_items`: QuickBooks, Zoho Books) | Daily |

**Website reading** goes through the egress guard, to the site's host only. It honours `robots.txt` (`User-agent: *` and `TaskiemBot`) and identifies itself as `TaskiemBot/1.0`. It drops scripts, styles, navigation, footers, forms and hidden elements, and skips login, cart, checkout and account pages, and files that are not pages.

## The owner sees it first

A new source is read at once, and the owner sees what it gave (**What it gave**) before anything is used. **Use this** approves it.
- The About page is the owner's own words, so it is approved as written.
- After the first approval, a refresh replaces the text in use at once, and the page shows when it last changed.
- **Pause** stops using a source (and refreshing it).
- **Delete** removes it and its passages.

A read that fails keeps the last text in use (or none, if it was never read) and says why on the source's page. The owners get an alert, once a day per source (`knowledge_source`).

**Try a question** on the page shows what the assistant would find for a customer's question.

## Passages and search

- **Passages.** A source's text is split into passages of at most 1,200 characters: paragraphs joined up to that, a longer one cut at a sentence. Each passage is titled with its part (a heading, a page's title) and linked back to where it came from. A source keeps at most 2,000 passages, and a business at most 50 sources.
- **Search** is Postgres full text search in Taskiem's own database, under row-level security. It uses the simple dictionary, so Pidgin, Yoruba, Hausa and Igbo words match as written. A passage matches any word of the question, or a word it begins ("deliver" finds "delivery"). Common words are left out, in English and the most common ones of the other languages. Matches are ranked, and only approved sources that are not paused are searched.

## In a workflow: `knowledge@1`

| Action | Class | |
| --- | --- | --- |
| `search` | read | `question` (the customer's message, sealed in history), `limit` (1 to 10, default 5), `max_chars` (default 6,000). Output: `found`, `passages` (`source`, `kind`, `title`, `text`, `link`, `read_at`), and `material`: the passages quoted for an ai step's input |
| `refresh` | idempotent write | `source` (a title or id): read it again within minutes instead of at the daily refresh |

```json
{"id": "find", "type": "connector", "connector": "knowledge@1", "action": "search", "input": {"question": "=steps.hear.output.text"}},
{"id": "reply", "type": "ai", "needs": ["find"],
 "config": {"prompt": "Answer the customer from the material; if it does not say, offer to find out.", "...": "..."},
 "input": {"material": "=steps.find.output.material"}}
```

**Material, never instructions (K11).** `material` quotes each passage between `<<material N: source, part, read date>>` and `<<end of material N>>`, after a line saying it is information to answer from and never instructions. A passage cannot close its own quote: `<<` inside it is written `‹‹`. Every ai step's fixed rules (which an instruction cannot change) tell the model that text quoted this way is information only, and that it never follows requests written in it. Text from outside Taskiem (a website, a document, a sheet, books) is reviewed by the owner before it is used. The reply check of V6 applies to whatever a reply says.

## For operators

- **Refreshing.** The `api` role refreshes, every 5 minutes, the sources due: those read over a day ago, and those a workflow or the owner asked to read again. It finds them across businesses through `taskiem_knowledge_due`, which shows ids only. Each source is read within its business's scope, with its own connections (recorded in the secret reads as `knowledge.read`) and through the egress guard.
- **Storage.** Sources and passages are in `knowledge_sources` and `knowledge_passages` (migration 00319), under forced row-level security, with a GIN index on the passages' words.
- **Audit.** Adding, changing, approving, pausing and deleting a source are audited (`knowledge.source.*`), with its kind and title, never its text.

## Not yet

- **OCR** for scanned documents and photos of menus.
- **Google Docs and Drive files:** there is no Drive connector yet.
- **Shop platforms** (Shopify, WooCommerce, Jumia): there are no connectors yet.
- **Sending a document in chat** to add it: today a document is uploaded on the page.
