# Chats: file previews, link previews and editing a sent message

Date: 26 November 2026 · Branch `claude/taskiem-chat-business-4gjlfs` · Status: **§2 (links) and §1 (files) built; §3 planned**

Three things people expect from WhatsApp that Taskiem's chats do not do yet:

1. **A file shows what it is before it is opened**, and opens inside Taskiem: a PDF's first page in the bubble, a viewer to read it, and Word, Excel and other documents accepted and shown as WhatsApp shows them.
2. **A link in a message can be tapped**, and shows a card with the page's picture, title and site, also while it is being typed.
3. **A sent message can be edited** for a short while, and everyone in the chat sees the new words, marked "Edited".

Each is built as its own pull request, in this order: links (§2), files (§1), editing (§3). Each updates its "As built" section here, the guide pages named in §5, and the testing manual.

## 0. Where this applies, and what is there today

### 0.1 The chats

| Chat | Tables | Files | Live updates | Web | App |
| --- | --- | --- | --- | --- | --- |
| **Business to business** (linked businesses) | `link_messages` (one copy per business, joined by `pair_id`) | `POST/GET /v1/link-chats/{link}/files[/{id}]` | `GET /v1/chat/stream` (`message`, `gone`); app sync `GET /v1/link-chats/changes?since=` | `pages/Conversation.tsx` | `chat/conversation.dart` |
| **Person to person** | `people_messages` (sealed), `people_files` | `/v1/me/people/{id}/files[/{message}]` | `GET /v1/me/live` | `pages/PeopleChat.tsx` | `chat/people_chat.dart` |
| **Groups** | `group_messages` (sealed), `group_files` | `/v1/me/groups/{id}/files[/{message}]` | `GET /v1/me/live` | `pages/Groups.tsx` | `chat/group_chat.dart` |
| **A person and a business** (customer chats, "the door") | customer chat tables | `/v1/me/chats/{id}/files/{message}` (person), `/v1/customer-chats/{id}/files/{message}` (business) | as each side's stream | `pages/PeopleChat.tsx` (`AppAttachmentView`), the business's customer chat page | `screens/door.dart` |

Everything below applies to **all four** unless it says otherwise. The Taskiem assistant's own chat (`chat_history`) is not changed, except that links in its answers can already be tapped in the app and become tappable on the web too (§2.2).

### 0.2 Today

- **Files.**
  - **What is accepted:** photos (JPEG, PNG, WebP, up to 10 MB), voice notes (up to 5 minutes), and PDFs (up to 25 MB). Anything else is refused: "send a photo, a voice note or a PDF" (`engine/media/chatfile.go` `CheckChatFile`).
  - **Checks:** each file is scanned for malware (`ScanChatFile`, ClamAV). A PDF with scripts, launch actions or embedded files is refused (`checkPDF`). Photos are turned upright and their metadata removed.
  - **What is kept about a file:** `attachment {kind: photo|file|voice, type, size, name}`. Nothing tells a page count, a picture of the file, or a photo's width and height.
  - **How it is shown:**
    - Web: a file is a download link with an icon, its name and size; a photo is an image; a voice note is a player.
    - App: a file is a row; tapping it hands the file to the phone's share sheet (`chat/attachments.dart` `_open`).
    - A business-to-business file of kind `file` is always sent as a download (`Content-Disposition: attachment`).
  - **What does not exist:** nothing draws a picture of a PDF; the app has no PDF viewer.
- **Links.**
  - **Business to business:** previews exist. The sender's side fetches the first link once as the message is sent, through the egress guard (`api/link_preview.go`). It allows only http and https, refuses private addresses, and stops at 4 seconds and 512 KB; it reads HTML only and keeps no image. It fetches at most ten pages a minute per person, and remembers what was fetched per business. The title and description are stored on both copies (`link_messages.preview`). See business-links.md, "Link previews".
  - **Person to person and groups:** no previews.
  - **Links in the text** cannot be tapped in any chat, on web or app, except in the app's Taskiem assistant chat.
- **Editing and deleting.**
  - **Editing:** does not exist anywhere.
  - **Person to person and groups** have delete for me, and delete for everyone for your own messages within 24 hours. Delete for everyone clears the words and file on both copies and removes them from search.
  - **Business to business** has delete for me only (`hidden_at`); a conversation is never deleted by one side, as it is the record of the trade.
  - **WhatsApp, Telegram and Slack:** a message sent there by Taskiem is never changed or removed.

## 1. Files: previews, a viewer, and documents

### 1.1 What people see

- **A PDF in a chat** shows, in the bubble:
  - a picture of its first page, cut to the bubble's width;
  - under it, the file's name, then "PDF · 3 pages · 1.2 MB";
  - the time and ticks as on any bubble.
  - With a caption, the caption sits under the file, as for a photo.
  - **While the picture is being made, or when it cannot be made** (a damaged or password-protected PDF), the bubble shows the PDF icon instead of the picture. Everything else works as before.
  - **A password-protected PDF** says "Protected PDF" in place of the page count.
- **Tapping a PDF:**
  - **App:** it opens in Taskiem's PDF viewer. The viewer:
    - has a full-screen page with the file's name and the sender and time in its head;
    - lets the person swipe or scroll the pages, pinch and double-tap to zoom, and shows "Page 2 of 3";
    - has **Share** (the phone's share sheet, as today) and **Save** (to the phone's Downloads or Files) buttons.
    - Back returns to the chat at the same place.
    - A file once opened is kept on the phone with the chat's other kept media (K1), so it opens again without the internet.
  - **Web:** it opens in a viewer over the chat. The viewer:
    - uses the browser's own PDF reader;
    - has the file's name, the sender and the time in its head, and **Download** and **Close** buttons.
    - Escape closes it.
    - On a phone-width browser it fills the screen.
- **A photo** shows at its real proportions before it has loaded (a soft colour, then the photo), so the chat does not jump as photos arrive. Tapping opens it full screen with zoom, as today.
- **Other documents are now accepted** (founder's decision, 26 November):
  - **Which ones:** Word (`.doc`, `.docx`), Excel (`.xls`, `.xlsx`), PowerPoint (`.ppt`, `.pptx`), OpenDocument (`.odt`, `.ods`, `.odp`), CSV (`.csv`) and plain text (`.txt`).
  - **How they look:** a bubble with the type's icon and colour (Word blue, Excel green, PowerPoint orange, others grey), the name, then for example "Excel · 84 KB".
  - **Tapping one:**
    - App: downloads it, keeps it with the chat's media, and opens it in another app on the phone (the phone's "Open with"). If the phone has no app for it, it offers Share.
    - Web: downloads it.
  - **No picture is drawn** for these, and none is planned.
- **"Media, links and docs"** (a chat's info page) lists:
  - documents with the same icon and line;
  - PDFs with their small picture.
- **Sending:**
  - **App:** the file picker (`file_selector`) allows the new types. Before sending, the person sees the same card the chat will show (a PDF's first page once drawn on the phone, or the type's icon), the name and size, a caption box, and Send.
  - **Web:** the attach button's picker allows the new types and shows the same card in the composer before sending.
  - **Refused files:** a file too large or of a type not allowed is refused before upload, in plain words: "This file is larger than 25 MB" or "Taskiem can send photos, voice notes, PDFs and Word, Excel, PowerPoint and text documents".

### 1.2 Rules for the new documents

- **Size:** up to 25 MB, as a PDF.
- **Type:** read from the file's own bytes, never trusted from its name or the browser's type.
  - The Office Open XML and OpenDocument files are ZIP files: their type is read from the ZIP's `[Content_Types].xml` or `mimetype` entry.
  - The old Office files (`.doc`, `.xls`, `.ppt`) are read by their compound-file header.
  - CSV and text must be valid UTF-8 with no NUL bytes.
- **Refused:**
  - files with macros: `.docm`, `.xlsm`, `.pptm`, a `vbaProject.bin` inside a ZIP, or the old formats' macro storage;
  - password-protected Office files, which cannot be scanned;
  - ZIPs whose unpacked size exceeds 100 MB or holds more than 2,000 entries (a "zip bomb");
  - executables and anything not on the list, whatever their name.
- **Malware scan:** every document is scanned as PDFs are (`ScanChatFile`) before it is kept. If the scanner is down, the file is refused with "Taskiem could not check this file. Try again in a minute", as today.
- **The name:** kept as sent, with path parts, control characters and right-to-left marks removed and the length capped at 120 characters. The extension is corrected to the real type, so `invoice.pdf.exe` cannot be sent and a renamed file shows its real extension.
- **Downloading a document:** always as a download (`Content-Disposition: attachment`), with `X-Content-Type-Options: nosniff`, never shown inside a Taskiem page.
- **Coming in from WhatsApp, Telegram and Slack:** documents that come into a Taskiem chat from those channels go through the same check, so a Word file sent from WhatsApp now arrives instead of being turned away.

### 1.3 How it works

- **The file's details** gain, all optional so old messages read as before (`links.Attachment`, `chatAttachment`, OpenAPI `ChatAttachment`):
  - `pages` (a PDF's page count);
  - `protected` (a password-protected PDF);
  - `preview` (`ready`, `none` or `failed`);
  - `w` and `h` (a photo's, or the PDF picture's, width and height);
  - `doc` (`word`, `excel`, `powerpoint`, `text`, `csv`, `opendocument`).
- **The picture of a PDF's first page:**
  - **Size:** drawn 640 pixels wide (enough for a phone bubble at twice the pixels), stored as WebP, at most 200 KB.
  - **Drawn in pure Go with no system program**, because the server image is distroless:
    - PDFium compiled to WebAssembly and run inside the process (`go-pdfium` in WebAssembly mode, run by `wazero`);
    - in its own instance with a memory cap (256 MB) and a time limit (5 seconds);
    - at most two at once per server, with others waiting in a queue.
    - A PDF that crashes, times out or is too large to draw gets `preview: failed`, and its bubble shows the icon.
  - **When it is drawn:** after the malware scan and `checkPDF`, never before.
    - **New files:** in the background straight after the upload is answered, so sending is not slowed. The message is marked changed when the picture is ready, so the live stream and the app's sync carry `preview: ready`.
    - **Older PDFs:** drawn the first time someone asks for one (§1.3, "Where it is fetched"), then kept. No bulk job.
  - **Where it is kept:** next to the file, under the same rules:
    - business to business: in each business's own storage, as each keeps its own copy of the file;
    - person to person and groups: sealed like the file, in `people_files` and `group_files` with a `kind` of `preview`;
    - deleted whenever the file is deleted (delete for everyone, account deletion, a business leaving).
- **Where it is fetched:** `GET …/files/{id}?preview=1` on each of the file routes in §0.1. It answers the WebP, or 404 when there is none. It uses the same permission check as the file itself.
- **Photos:** their width and height are kept when they are cleaned (they are known then). Older photos have none and show as today.
- **The page count** is read while the picture is drawn. A protected PDF's count is unknown.
- **The web viewer:**
  - **A new route for viewing inside Taskiem:** `GET …/files/{id}?view=1`. It answers a PDF with `Content-Disposition: inline`, `Content-Security-Policy: sandbox; default-src 'none'`, `X-Content-Type-Options: nosniff` and `Cross-Origin-Resource-Policy: same-origin`. Only PDFs are answered this way; every other type stays a download.
  - **How it is shown:** in an `<iframe sandbox>` inside the viewer.
  - **Browsers that will not show a PDF in a frame** (some phone browsers) get "Open in a new tab" and Download instead.
- **The app viewer:**
  - **Package:** `pdfrx`, which uses PDFium. Its licence and size are checked before adding it, and the release build's shrinking rules are updated if it needs them.
  - **The file it reads:** the kept file in the encrypted media store (K1), opened to a temporary file the viewer reads, deleted when the viewer closes.
- **Other documents in the app:** opened with `open_filex` (or the platform's open-with intent through `url_launcher` with a `content://` file provider) from the kept copy. If no app can open it, Share is offered.

### 1.4 Not in this part

- **No pictures of Word, Excel or PowerPoint files**: that would need a document converter on the server.
- **No reading or searching inside documents.**
- **No editing of files.**

### 1.5 Tests

- **Go:**
  - Each new type is accepted and recognised from its bytes.
  - These are refused: macros, password-protected Office files, a ZIP bomb, a renamed executable, and wrong UTF-8 in a CSV.
  - The name is cleaned and the extension corrected.
  - The PDF picture: drawn with the right page count; a protected PDF; a damaged PDF giving `failed`; the time limit; the concurrency limit.
  - `?preview=1` answers on each route and is refused to someone outside the chat.
  - `?view=1` only for PDFs, with its headers.
  - The picture is deleted with delete for everyone.
  - Old messages read without the new fields.
- **Web:**
  - vitest for the file card and the type icons.
  - Playwright: a PDF bubble with its picture; the viewer opening and closing with Escape; a Word file downloading; the picker refusing a `.exe`; axe in both themes; 390 and 360 px.
- **App:**
  - Widget tests: the PDF bubble with and without a picture; the document bubble per type; the viewer's page count and buttons; open-with falling back to Share; the send card.
  - Pictures of each, in light and dark, looked at (`mobile-screen` skill).

## 2. Links: tappable, with a preview card

### 2.1 What people see

- **Every web address in a message's words can be tapped**, in every chat, on web and app.
  - Underlined in the bubble's link colour.
  - **What counts as a link:** `http://` and `https://` addresses, and bare ones like `jumia.com.ng/phones` or `www.example.com`, which are taken as https.
  - **Not tappable:** any other scheme (`javascript:`, `file:`, `intent:`, `tel:` and so on). Email addresses and phone numbers stay plain text in this part.
  - **App:** opens Taskiem's in-app browser (`openInBrowser`, `app/links.dart`). Holding a link offers **Open**, **Copy link** and **Share**.
  - **Web:** opens a new tab (`target="_blank" rel="noopener noreferrer nofollow"`).
- **The preview card,** for the first link in a message, sits at the top of the bubble as on WhatsApp. It shows:
  - the page's picture: full width when the page's picture is wide, or a square on the left when it is small or square;
  - the title (two lines at most);
  - the description (two lines at most);
  - the site's name or host (e.g. "jumia.com.ng").
  - **Tapping anywhere on the card** opens the link as above.
  - **If the page gave no title**, there is no card, only the tappable link.
- **While typing:**
  - Once a link is typed or pasted and the person pauses (half a second), the card appears above the message box, with a ✕ to send without it.
  - It shows "Loading preview…" while it is fetched, and nothing if none can be made.
  - Removing the link from the words removes the card.
  - **Sending** carries the card if it was shown and not closed. If the person sends before the card has loaded, the server makes it as today.
- **Taskiem's own links** (a store, order, invoice, payment, form or chat link on a Taskiem address) are not fetched as web pages. They show the reference card Taskiem already shows for such things (G6), or, where the person may not see that thing, a plain card "Taskiem · Order link".
- **A link to a page that changes** keeps the card made when it was sent, as WhatsApp does.

### 2.2 How it works

- **Finding links in the words:** one rule shared by web and app, with the same test cases in both. Web: `web/src/lib/linkify.ts`. App: `mobile/lib/chat/linkify.dart`.
  - Trailing `.,;:!?)"'` are not part of a link, unless they balance a `(` inside it.
  - Mentions (`@`, `#`) keep their chips; a link inside a mention is not linkified.
  - Long links are shortened in the middle when shown (`jumia.com.ng/…/phones`) but open in full.
  - The same rule makes links tappable in the web's Taskiem assistant chat and in customer chats.
- **Fetching the preview** (extending `api/link_preview.go`):
  - **New things read:** `og:image` (with `og:image:width`/`height`), `og:site_name`, `twitter:image` and `twitter:title` as fallbacks, and `<link rel="icon">` is not used.
  - **The picture is fetched by the server** through the same egress guard: its own host allowed, private addresses refused, 2 MB at most, JPEG, PNG or WebP only by its bytes.
    - It is cleaned and resized like a chat photo (largest side 800, plus 320).
    - It is stored as Taskiem's own file: per business for business-to-business chats, sealed for person-to-person chats and groups.
    - **Phones and browsers never load the picture from the other site**, so that site never learns who read the chat or from where. The console's "images from its own origin only" rule stays.
  - **Redirects:** at most three, each to an http or https address checked again by the guard.
  - **The fetch's other limits are as today:** 4 seconds and 512 KB of HTML; ten pages a minute per person (the rest go without a preview); remembered per business, and per person for person-to-person chats, never shared between them.
  - **The request:** `User-Agent: TaskiemBot/1.0 (+https://taskiem.com/bot)`, no cookies, and nothing about the sender.
- **The new "preview this link" request** for the composer: `POST /v1/link-preview {url, chat}`.
  - **What it answers:** `{url, host, site, title, description, image: {id, w, h}}`, or `204` when there is none.
  - **Rules:**
    - The person must be able to send in that chat.
    - It uses the same rate limit and remembered pages as sending.
    - The picture is held for 30 minutes and kept only if a message is sent with it.
  - **Sending carries** `preview: {id}` (the card shown) or `preview: false` (closed with ✕). With neither, the server makes it as today.
- **Where previews are kept:**
  - **Business to business:** `link_messages.preview` gains `site` and `image {path, w, h}`. The picture's path is never shown to clients; it is answered by `GET /v1/link-chats/{link}/previews/{message}`.
  - **Person to person and groups:** sealed on the message, like its words. The picture is in `people_files` and `group_files` with `kind = 'link_image'`.
  - **A person and a business:** as each side's messages.
  - **Delete for everyone** removes the preview and its picture with the words.
- **Sealed chats:** the server already sees a person-to-person message's words when it is sent (it seals them). Fetching the preview happens then and nowhere else. Nothing about the link is kept unsealed.
- **WhatsApp, Telegram and Slack:**
  - **Messages sent there by Taskiem** carry the link in their words, and those apps draw their own previews. Taskiem sends `preview_url: true` on WhatsApp text messages that contain a link.
  - **Messages coming in from them with a link** get a preview made on arrival, as if sent in Taskiem.

### 2.3 Tests

- **Go:**
  - The picture is fetched through the guard.
  - These are refused: a private-address picture, a redirect to a private address, a picture over 2 MB, a non-image picture.
  - A page with no picture still gives a card without one.
  - `site` is read.
  - The composer's request: refused outside the chat, rate-limited, and the picture dropped after 30 minutes if unused.
  - `preview: false` sends none.
  - The preview is sealed in person-to-person chats.
  - Delete for everyone removes it.
  - A Taskiem link is not fetched.
- **Shared cases for finding links:** one JSON file of words and the links found in them, read by both vitest and the Flutter tests. It covers punctuation, brackets, bare addresses, `javascript:`, mentions, and the dot at the end of a sentence.
- **Web:**
  - Playwright: a link in a message opens a new tab; the card's picture comes from Taskiem's address; the composer card appears and ✕ sends without it; axe in both themes; 390 and 360 px.
- **App:**
  - Widget tests: tapping a link calls the in-app browser; holding a link shows Open, Copy link and Share; the card's two layouts; the composer card and ✕.
  - Pictures of each in light and dark, looked at.

## 3. Editing a sent message

### 3.1 The rules (founder's decisions, 26 November)

- **Who:** only the person who sent it. In a business chat, the member who sent it, not other members of the same business.
- **When:** **within 30 minutes** of sending, by the server's clock. After that the edit is refused and the menu no longer offers Edit.
- **What can be edited:** the words of a text message, and the caption of a photo, file or document. The file itself cannot be swapped.
- **What can never be edited:**
  - **Cards:** order, bill, invoice, purchase, product, business, delivery, split, payment, payout list, and every other card or `kind` other than text.
  - **Money:** a message with an amount the sender asked for (`asked`), or one tied to a payment or payout list (`payment_id`, `batch_id`); a message quoting account details to pay (`chat_pay_quotes`); a message tagging a price, invoice or order with `#`.
  - **Taskiem's own messages:** notes, diary lines and answers.
  - **Others:** voice notes (no words to edit); forwarded messages; a deleted message; a message still waiting to send (that is changed in the outbox instead, §3.3).
  - **A message whose words made Taskiem act** (an order or invoice drafted from it, a menu answered, an `@Taskiem` question answered). The action does not follow the new words, so the words are kept as they were.
- **How many times:** any number of times within the 30 minutes.
- **The new words follow the same rules as a new message:** not empty (unless it is a caption), the same length limit, and the same checks on mentions. A new `#` tag of a price, invoice or order cannot be added by editing.
- **What everyone sees:** the new words, and **"Edited"** next to the time, in the chat, in replies quoting it, in search, in the chat list's last line, and in "Media, links and docs" for a caption. No new notification is sent, and the chat does not move up the list.
- **Earlier wording:**
  - **Business to business:** every earlier wording is **kept for the business's audit log only**. It is not shown to either business in the chat; the owner and those allowed to read the audit log see each version with who changed it and when.
  - **Person to person, groups, and a person and a business:** earlier wording is **not kept** (as delete for everyone keeps nothing). For a person-and-business chat, the business's side follows the business rule: its audit log keeps the earlier wording of messages its own members edited.
- **Someone reading on WhatsApp:** a message already sent to them on WhatsApp cannot be changed there. Taskiem sends a short follow-up with the new wording: "✏️ Edited: *new words*", quoting the original where WhatsApp allows. On **Telegram and Slack** the message is changed in place where Taskiem sent it (Telegram `editMessageText`, Slack `chat.update`), so no follow-up is needed. If changing it in place fails (too old, deleted), the follow-up is sent instead.

### 3.2 What people do

- **App:** hold the message, then choose **Edit** in the menu, between Copy and Forward, shown only where the rules allow.
  - The message box fills with the words and shows a strip "Editing message" with the old words and a ✕.
  - Send becomes a tick. Tapping the tick saves; ✕ or Back cancels.
  - **If the 30 minutes run out while editing,** saving is refused with "This message can no longer be edited" and the new words stay in the box, so the person can send them as a new message.
- **Web:** **More**, then **Edit** on a message (also the Up arrow in an empty box edits your last message, as on WhatsApp Web and Slack).
  - The same strip appears; Enter saves and Escape cancels.
- **Both, after saving:** the bubble shows the new words and "Edited" at once, before the server answers, and goes back if the server refuses, with the reason in a toast.

### 3.3 How it works

- **Columns** (one migration, in the range given when it is built):
  - **`link_messages`:** `edited_at timestamptz`, `edits int not null default 0`.
  - **`people_messages` and `group_messages`:** `edited_at timestamptz`.
  - **Customer chat messages:** `edited_at timestamptz`.
  - **`link_message_edits`:** `(id, tenant_id, message_id, pair_id, edited_by, edited_at, words_before text, caption_before text)`, with RLS like `link_messages`. Only the sending business's side keeps it: the earlier wording belongs to the business whose member wrote it, and the other business never had it in its audit log. Kept as long as the message is kept.
- **The edit request**, one per chat:
  - `PATCH /v1/link-chats/{link}/messages/{id}`
  - `PATCH /v1/me/people/{id}/messages/{message}`
  - `PATCH /v1/me/groups/{id}/messages/{message}`
  - `PATCH /v1/me/chats/{id}/messages/{message}`
  - `PATCH /v1/customer-chats/{id}/messages/{message}`

  The body is `{text, edits}`, where `edits` is the count the client last saw.
  - **Answers:**
    - `200` with the message;
    - `409 {stale: true}` when `edits` is behind (someone's other device edited it; the client reloads the message and shows the newer words);
    - `403 {not_editable: reason}` with reason `not_yours`, `too_old`, `card`, `money`, `taskiem_acted`, `deleted`, `forwarded` or `voice`;
    - `422` for words the rules refuse.
- **In one transaction:**
  1. Lock the message.
  2. Check the rules.
  3. Write the edit-history row (business to business).
  4. Change the words on **every copy**: for business-to-business chats, both businesses' rows by `pair_id`; for sealed chats, re-seal on each copy.
  5. Set `edited_at`, increment `edits`, and set `updated_at` / `changed_at`.
  6. Rebuild what depends on the words:
     - **mentions:** parsed again, and only `@` mentions may be added or removed;
     - **search:** `people_search` tokens replaced, and the business chat's search where it exists;
     - **the link preview:** made again if the first link changed, otherwise kept;
     - **the chat list's last line:** if it was the last message.
  7. Write the audit entry: `link.message.edit` (business to business, with the edit-history id), or `customer_chat.message.edit` for the business's side. Person-to-person and group edits write no audit entry, as their deletes do not.
- **Live and offline:**
  - `updated_at` / `changed_at` moving is enough. The live streams already send changed messages as `message`, and the app's sync (`/v1/link-chats/changes`, and the people chats' changes) already upserts them into the encrypted store, so an edit reaches offline phones when they return. **Nothing new is needed in the streams.**
  - Clients show "Edited" from `edited_at`.
- **The outbox (offline):**
  - **A message still waiting to send:** Edit changes the waiting copy, so one message is sent, with the final words.
  - **An edit made while offline:** waits in the outbox like a message, as `{kind: edit, message, text, edits}`, and is sent in order after anything waiting before it.
    - Refused when it reaches the server (too old by then, or stale): the bubble goes back to the server's words, and a note in the chat says "Your edit was not saved: the message can only be edited for 30 minutes after it was sent", with **Send as a new message**.
- **Replies:** a reply quoting an edited message shows its current words, as replies already read the quoted message live.
- **Notifications and pushes:** none for an edit. A push already shown on a phone is not changed.
- **Taskiem acting:** a new column `acted_at` (in the same migration, on `link_messages` and the customer chat messages) is set where Taskiem makes an order, invoice or menu answer from a message, and where `@Taskiem` answers it. The edit rule reads it. Older messages have none: for those, a message older than the deploy is judged by whether a draft, invoice or note points at it (`source_message_id` and the like, found when this is built).
- **The bridge to WhatsApp, Telegram and Slack** (`tellLinkMessage`, `link_chat_sent`):
  - After the edit is saved, a job looks up where the message was sent (`link_chat_sent`), and for each channel:
    - **Telegram:** `editMessageText` / `editMessageCaption`.
    - **Slack:** `chat.update`.
    - **WhatsApp:** sends the "✏️ Edited" follow-up as a reply to the original where the context id is known (`context.message_id`), so it quotes it.
  - Failures are retried like other channel messages and never undo the edit in Taskiem.
  - The follow-up's words are in `engine/lang/messages/en.json`, with drafts in ha, ig, pcm and yo.
- **Edits made inside WhatsApp, Telegram or Slack** to a message that came into Taskiem from there: **not in this part.** They are kept as they arrived. A later part may take Telegram's `edited_message` and Slack's `message_changed` events, and WhatsApp's edit webhooks, through the same edit path.

### 3.4 Not in this part

- **Showing earlier versions in the chat.** Per the founder's decision, the audit log only.
- **Editing a file, a voice note or a card.**
- **Editing by an owner or admin of someone else's message.**
- **Edits coming in from WhatsApp, Telegram and Slack** (§3.3, last point).

### 3.5 Tests

- **Go**, for each chat:
  - Edit within 30 minutes; refused at 30 minutes and one second (with a set clock).
  - Refused for someone else's message, including another member of the same business.
  - Refused for each `not_editable` reason.
  - Both copies changed.
  - Sealed words re-sealed and readable by both.
  - `409` when stale.
  - Mentions: `#` cannot be added; `@` can.
  - Search finds the new words and not the old.
  - The preview is made again when the link changes.
  - `link_message_edits` holds the old words, on the sending business's side only, and the other business cannot read it (RLS).
  - The audit entry is written.
  - No push is sent.
  - The live stream and `/changes` carry the edit.
  - Telegram and Slack are changed in place (fake clients); WhatsApp gets the follow-up, with the follow-up sent when in-place editing fails.
- **Web:**
  - vitest: the rules shown in the menu.
  - Playwright: Edit from More, Up arrow, Enter saves, Escape cancels; "Edited" shown on the other side live; refused after time with the words kept; axe; 390 and 360 px.
- **App:**
  - Widget tests: Edit in the menu only where allowed; the editing strip; tick and ✕; refused after time; the outbox edit offline then sent; a refused offline edit's note with Send as a new message; "Edited" on the bubble.
  - Pictures in light and dark.

## 4. Order of work and sizes

| Part | Server | Web | App | Size |
| --- | --- | --- | --- | --- |
| §2 Links | preview picture, site, composer request, sealed previews for people chats, Taskiem links, WhatsApp `preview_url` | linkify, card, composer card | linkify, hold menu, card, composer card | medium |
| §1 Files | new types and their checks, PDF picture in WebAssembly, `?preview` and `?view`, new attachment fields | PDF bubble, viewer, document bubbles, picker | PDF bubble, `pdfrx` viewer, document bubbles, open-with, send card | large |
| §3 Editing | migration, five edit routes, rules, edit history, audit, bridge edits | Edit, strip, Up arrow, "Edited" | Edit, strip, outbox edits, "Edited" | large |

Each part also adds:
- its OpenAPI changes and the generated clients (`web/src/api/gen`, `mobile/lib/api/api.g.dart`, `mobile/api.txt`);
- its words for chat and WhatsApp in `engine/lang/messages/` (drafts not reviewed);
- its rows in `docs/spec/chat-scenarios.md`;
- its checks in the testing manual.

## 5. Docs to update when built

- `docs/guide/` chat pages: sending files, links, editing.
- `docs/spec/business-links.md` "Link previews": point here.
- `docs/spec/people-chatting.md`: files, previews and editing in people chats and groups.
- `docs/spec/mobile-app-explore-and-offline.md` K3 / K-S3: "edited since" is now real.
- This file's "As built" sections.

## 6. As built

### 6.1 Files (§1), built 26 November 2026

**What people see now.**
- **A PDF** shows the picture of its first page across its card, its name, and "PDF · 3 pages · 1.2 MB" (a caption under it). While the picture is drawn, or when there is none (a damaged PDF, one that took too long), the card shows the PDF icon; a password-protected one says "Protected PDF".
  - **Web:** tapping it opens Taskiem's viewer over the chat: the name, who sent it and when in its head; **Open in a new tab**, **Download** and **Close**; the browser's own reader in an `<iframe sandbox>` reading `?view=1`. Escape closes it and gives the focus back to the card. On a phone-width screen it fills the screen. A browser that says it has no PDF reader (`navigator.pdfViewerEnabled === false`) gets the words "This browser cannot show the PDF here" with the tab and the download.
  - **App:** tapping it opens `PdfViewerScreen` (`lib/chat/file_card.dart`): the name, the sender and the time in its head; the pages drawn by `pdfrx` (swipe, scroll, pinch and double-tap); "Page 2 of 3"; **Share** and **Save**. Back returns to the chat where it was.
- **Documents** (Word, Excel, PowerPoint, OpenDocument, CSV, text) show their type's icon in its colour (Word blue, Excel green, PowerPoint orange, PDF red, the others grey; the `--doc-*` tokens, `TkTokens.doc*` in the app) and "Excel · 84 KB". The web downloads them; the app opens them in another app on the phone (`open_filex`), or offers the share sheet when none can.
- **Photos** keep their proportions before they have loaded, a soft colour in their place, when their size is known (photos sent from now on).
- **Sending:** the web's attach button and the app's Document both take the new types; the file waits for its caption as its card (the type's icon and line). A program, a document with macros by its name (`.docm`, `.xlsm`…), or anything else is refused before upload: "Taskiem can send photos, voice notes, PDFs and Word, Excel, PowerPoint and text documents"; one too large: "This file is larger than 25 MB."
- **"Media, links and docs"** shows the same cards (a PDF with its picture).

**How it is built.**
- **The checks** (`engine/media/documents.go`, `chatfile.go` `CheckChatUpload`): the type from the bytes; Office Open XML by the main part in `[Content_Types].xml` (macro-enabled and template types refused, as is any `vbaProject.bin`); OpenDocument by its `mimetype` (a `Basic/` or `Scripts/` part refused); the old Office files by their compound-file streams (`WordDocument`, `Workbook`, `PowerPoint Document`), read with `github.com/richardlehane/mscfb`: macro storages (`Macros`, `_VBA_PROJECT_CUR`, `VBA`, a PowerPoint `VBAInfoContainer`) refused, and password protection found by Word's `fEncrypted`, Excel's `FILEPASS`, an `EncryptedSummary`, or an encrypted new Office file (`EncryptionInfo`/`EncryptedPackage`); a ZIP whose entries add up to more than 100 MB or number more than 2,000 refused, and only small parts ever unpacked (1 MB at most); text and CSV valid UTF-8 with no NUL, and not a script (`#!`). CSV and text are the same bytes: a text file named `.csv` is kept as CSV.
- **The name** (`ChatFileName`): no path parts, control characters or bidirectional marks, at most 120 characters, and a PDF's or a document's real extension (`invoice.pdf.exe` that is a PDF is "invoice.pdf"; a renamed spreadsheet shows `.xlsx`). A photo's or voice note's name is cleaned but keeps its extension.
- **The picture** (`engine/media/pdfpreview.go`): `github.com/klippa-app/go-pdfium` in WebAssembly mode, run by `wazero` inside the process; each instance has 256 MB of memory (`WithMemoryLimitPages`), no directory of the server mounted, and 5 seconds (the instance is killed past them, `WithCloseOnContextDone`); two at once, others waiting. The first page is drawn 640 pixels wide (at most 1,280 tall), on white, and encoded as WebP with `github.com/gen2brain/webp` (libwebp translated to Go, no system library), its quality lowered until it is at most 200 KB.
- **When it is drawn** (`api/file_previews.go`): once, in the background, after a message with a PDF is sent (`drawLater`), for every copy; or the first time `?preview=1` is asked for one that has none (an older PDF), then kept (one at a time per copy). A protected PDF gets `preview: none`; a damaged or too slow one `failed`.
- **Where it is kept**, beside the file: business to business and a business's copy of a customer chat, in each business's own storage (`chat/preview-….webp`, its place in the attachment as `preview_path`, never shown); people chats and groups, sealed in `people_files` and `group_files` with `kind = 'preview'`; the person's side of a chat with a business, `person_files` with `kind = 'preview'`. Delete for everyone removes it with the file; erasing a customer removes the business's copy with their files.
- **The facts** `pages`, `protected`, `preview`, `w`, `h` and `doc` are in each copy's attachment (`media.Facts`, embedded in `links.Attachment` and `chatAttachment`; OpenAPI `ChatAttachment` and the link message's attachment). Setting them changes the message (`updated_at` / `changed_at`), so the live streams and the app's sync carry `preview: ready`.
- **The routes:** `?preview=1` and `?view=1` on `GET /v1/link-chats/{link}/files/{id}`, `/v1/me/people/{id}/files/{message}`, `/v1/me/groups/{id}/files/{message}`, `/v1/me/chats/{id}/files/{message}` and `/v1/customer-chats/{id}/files/{message}`, each with its file's own permission check. `?view=1` answers a PDF only, inline, with `Content-Security-Policy: sandbox; default-src 'none'; frame-ancestors 'self'`, `X-Content-Type-Options: nosniff`, `Cross-Origin-Resource-Policy: same-origin` and `X-Frame-Options: SAMEORIGIN`; anything else stays a download.
- **Storage:** the private bucket's allowed types gain `image/webp` and the document types.
- **WhatsApp, Telegram and Slack:** a document a member sends to a linked business from there (`api/chat_direct.go`) goes through the same check. A customer's document from WhatsApp is still shown as the word "File", as before: no file from a customer's WhatsApp is kept today.
- **The app:** `pdfrx` 3.0.0 (MIT; PDFium through FFI, so no R8 rules) and `open_filex` 5.0.0 (BSD-3-Clause), whose storage permissions are removed from the app's manifest (it opens only the app's own copy). The picture is fetched with `?preview=1` and kept with the chat's media (K1). The viewer reads the kept copy from memory (`PdfViewer.data`), so no copy is written; a document is written to the app's own temporary folder for the other app.
- **Migration** `00780_file_previews.sql`.

**Where this differs from §1, and why.**
- **The picture's height** is capped at twice its width, so a very tall page does not make a huge picture; the bubble shows its top.
- **A protected PDF** has `preview: none` (no picture can be made), not `failed`.
- **People chats draw the picture for each person's copy separately** (each sealed under its owner's key), from the sender's bytes in the background; an older one is drawn by whoever asks first, for their own copy only. A group's one copy may be drawn by any member who sees the message (a new policy, `member_preview_insert`).
- **App Save:** the phone's "save where you choose" (`file_selector` `getSaveLocation`) where the phone offers it; on phones that do not (Android, iPhone), Save opens the share sheet, whose "Save to Files" or "Save to device" keeps it.
- **The web viewer:** "Open in a new tab" is always in its head, not only where the frame fails, as a page cannot reliably tell that a frame's PDF did not draw.
- **The dependency upgrade:** go-pdfium needs `wazero` 1.12 (was 1.9). wazero 1.10 and later refuse a module compiled by another runtime, which the vendored QuickJS fork did, so `third_party/qjs` now compiles the module in each runtime from the shared cache (FORK.md, item 5).
- **No testing manual was found in the repository** (as for §2); its checks are not added.

**Tests.** Go: `engine/media/documents_test.go` (each type by its bytes; macros, protected `.docx`, `.doc` and `.xls`, zip bombs by size and by count, a renamed executable, Latin-1 CSV, a NUL, HTML; the name), `engine/media/pdfpreview_test.go` (pages and size, protected, damaged, the time limit, two at once), `api/file_previews_test.go` (business to business, people chats and groups: the picture on each side, refused outside the chat, `?view=1` and its headers, a Word file always downloaded, an older PDF drawn when asked, a protected one, delete for everyone), the PDF in `TestCustomerDoor`, `engine/db` `TestFilePreviewsMigrationUpDownUp`. Web: `src/lib/chatfiles.test.ts`, `e2e/chat-files.spec.ts`. App: `test/file_card_test.dart`, with pictures in light and dark, and `test/files_test.dart`.

### 6.2 Links (§2), built 26 November 2026

**What people see now.**
- **Links can be tapped** in every chat: business to business, people, groups, a person's chat with a business (both sides), and the Taskiem assistant's chat on the web.
  - Web: a new tab, `target="_blank" rel="noopener noreferrer nofollow"`, in the bubble's link colour (the read ticks' blue), long ones shortened in the middle.
  - App: Taskiem's browser tab (`openInBrowser`). Holding a link opens a menu with **Open**, **Copy link** and **Share**, before the message's own menu.
  - Tags (`@`, `#`) keep their chips; only the words between them are searched for links.
- **The card** sits at the top of the bubble: the picture across it when it is wide (at least 400 pixels and 1.3 times wider than tall), else a 76-pixel square on its left; the title and the words, two lines each; the site's name, or its host. Tapping it opens the link. A page with no title gives no card.
- **While typing**, the card shows above the box half a second after the person stops, with ✕ ("Send without the preview"). "Loading preview…" shows while it is made.
- **Taskiem's own links** show a plain card: a link mark, "Taskiem · Order link" (or Invoice, Payment, Form, Chat, Store link), and "Taskiem".

**How it is built.**
- **One rule for finding links**: `engine/linkify` (Go), `web/src/lib/linkify.ts`, `mobile/lib/chat/linkify.dart`. All three read the same cases, `web/src/lib/linkify.cases.json`. A bare address must start with `www.` or end in a common ending (`.com`, `.ng`, `.org` and others); this keeps "file.txt" and "index.html" as words.
- **The server** (`api/link_preview.go`, `api/link_preview_chats.go`):
  - fetches the page and its picture through the egress guard, following at most three redirects itself, each checked again (a redirect to another site is allowed, as for short links, but a private address is not);
  - reads `og:title`, `og:description`, `og:site_name`, `og:image`, then the `twitter:` ones, then `<title>` and the description;
  - takes the picture only if it is at most 2 MB and a JPEG, PNG or WebP by its bytes, and cleans it as a chat photo (`media.CleanLinkImage`: upright, no metadata, 800 and 320 pixels on its longest side);
  - asks as `TaskiemBot/1.0 (+https://taskiem.com/bot)`, with no cookies;
  - remembers what it fetched for an hour, per business or per person, at most 256 pages.
- **Where it is kept.**
  - Business to business: `link_messages.preview` gains `site`, `taskiem` and `image {w, h}`. Each business keeps its own copy of the picture, at 800 and 320 pixels, in its own storage, served by `GET /v1/link-chats/{link}/previews/{id}` (`?size=small`). The paths are never sent to clients.
  - People chats and groups: the card is sealed with the words. Its picture is in `people_files` and `group_files` with `kind = 'link_image'`. It is served by `GET /v1/me/people/{id}/previews/{message}` and `GET /v1/me/groups/{id}/previews/{message}`.
  - A person's chat with a business: on the business's side, `customer_messages.preview` holds the card sealed under the customer's subject, as the words are, and the picture's place in its storage. On the person's side, `person_messages.preview` holds the card, and `person_files` (`kind = 'link_image'`) the picture. They are served by `GET /v1/customer-chats/{id}/previews/{message}` and `GET /v1/me/chats/{id}/previews/{message}`. When a customer is erased, the pictures go with their files.
  - Delete for everyone (people chats and groups) removes the card and its picture with the words.
- **The composer**: `POST /v1/link-preview {url, chat}`. `chat` is `link:…`, `customer:…`, `people:…`, `group:…` or `door:…`. It answers `{id, url, host, site, title, description, taskiem, image: {id, w, h}}`, or 204, and 403 for a chat the person cannot write in. The picture is at `GET /v1/link-preview/{id}/image`. A send carries `preview: {id}` or `preview: false`. Without it, the server makes the card as before.
- **WhatsApp**: a text sent by Taskiem with a link asks for WhatsApp's own preview (`preview_url: true`). A customer's message from WhatsApp, on a business's own number, gets its card on arrival, in the background; it shows the next time the chat is read.
- **Migration** `00770_link_previews.sql`.

**Where this differs from §2, and why.**
- **The composer's card is held in the server's memory**, not in a table, for 30 minutes. Its picture is never stored unless a message is sent with it. With more than one server, a send that reaches another one makes the card again: the same result, one more fetch.
- **People chats, groups and the person's side of a chat with a business keep the picture once, at 800 pixels.** Each copy has a single file of `kind = 'link_image'`. Business copies keep 800 and 320.
- **Taskiem's own links always show the plain card.** They do not show the reference card (G6) yet: the link would have to be matched to a document the reader may see.
- **Cards on a customer's chat are made for messages in the app**, and for the customer's WhatsApp messages on arrival. A reply a business sends on WhatsApp has no card in Taskiem; WhatsApp draws its own.
- **Telegram and Slack**: messages from members there reach business chats through the usual send, so they get cards as if sent in Taskiem. No other path from Telegram or Slack into a chat exists today.
- **The web has no generated API client** (`web/src/api/gen` does not exist). Its types (`LinkPreview` in `web/src/lib/linkchat.ts`, and the message types) were changed by hand.
- **No testing manual was found in the repository.** Its checks are not added.

**Tests.** Go: `api/link_preview_test.go` (pictures, refusals, redirects, site, Taskiem links, `preview: false`, the composer with its limit and its 30 minutes, sealed people previews, delete for everyone), `TestLinkPreviewGuarded`, `engine/linkify`, `engine/whatsapp` `TestSendTextPreviewURL`. Web: `src/lib/linkify.test.ts`, `e2e/link-chat-links.spec.ts`. App: `test/linkify_test.dart`, `test/link_preview_test.dart`, with pictures in light and dark.
