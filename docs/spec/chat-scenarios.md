# The chat's scenarios: one list for the web and the app

The web chat and the phone app show the same chat
([mobile-app.md](mobile-app.md) §8, §9). Each scenario a person goes through
in it is here once, with the web's test (`web/e2e/`) and the app's
(`mobile/test/`) that hold it. A new scenario is added here with its tests
on both sides, or says why one side has none.

Where the app's test is a widget test, it drives the app's screens against
recorded answers from the demo world; **server** marks a test of the app's
own code against the real server (`mobile/test/server/`, run by
`.github/workflows/mobile-server.yml` with the server `web/e2e/serve.sh`
starts). The parity test (`mobile/test/parity_test.dart`) holds the look of
each screen against the web's picture of it, and is not listed again here.

Kept up to date by hand, as of 12 November 2026.

## Two businesses talk

| Scenario | Web | App |
| --- | --- | --- |
| Two businesses find each other and link | `links.spec.ts` two businesses find each other and link | **server** two businesses talk (set-up); `chats_test` Accept a request to link; `explore_test` Connect |
| A message reaches the other side; unread; read; the tick turns blue | `link-chat.spec.ts` two linked businesses talk; `link-chat-live.spec.ts` a message reaches the other side in a second or two | **server** a message sent is the other side's unread, read, then blue ticks; `conversation_test` a message is sent and shows its ticks |
| A message shows at once with a clock, then the server's copy | `link-chat-composer.spec.ts` a message shows at once with a clock | `conversation_test` a message is sent and shows its ticks |
| A message that cannot be sent says so, and Retry never sends it twice | `link-chat-composer.spec.ts` a message that cannot be sent says so | **server** a message that did not go is sent again as the same message, once; `conversation_test` tried again with the same client id |
| The live stream: messages, typing, a dropped stream reopened, polling without it | `link-chat-live.spec.ts` (the stream's scenarios) | **server** the live stream tells the other phone at once; `live_test` (all four) |
| A reply quotes; a swipe replies | `link-chat-keys.spec.ts` a reply that quotes a message; `link-chat-thread.spec.ts` a quote goes to its message | **server** a reply quotes, a reaction both see; `menu_test` a reply, a swipe right replies too |
| A reaction both see, changed, taken back; any emoji from + | `link-chat-reactions.spec.ts` a reaction is seen by both businesses; `link-chat-popover.spec.ts` a reaction from the pill, and any emoji from the + | **server** a reply quotes, a reaction both see; `menu_test` a reaction given from the menu, "+": any emoji |
| A star is yours alone; Starred opens at the message | `link-chat-reactions.spec.ts` a star is yours alone | `menu_test` a star; `chats_test` Starred; `contact_test` a starred message |
| Message info: sent, read, who reacted | `link-chat-reactions.spec.ts` Info shows when a message was sent and read | `menu_test` Message info and Forward |
| Pin, typing, forward, delete for me | `link-chat.spec.ts` pin, the typing line, forward and delete for me | `contact_test` Pin, Mute and Archive; `gaps_test` G3 typing; `menu_test` Delete for me, Message info and Forward |
| The menu on a long press, within the screen | `link-chat-popover.spec.ts` on a phone a long press opens it within the screen | `menu_test` (each opens it by a long press) |
| Photos, PDFs and voice notes; a caption | `link-chat-files.spec.ts` a photo with a caption, a PDF and a voice note | `files_test` (all) |
| A photo that did not go is sent again without a second copy | `link-chat-composer.spec.ts` a photo that was not sent can be sent again | `files_test` a photo from the clip waits for its caption, then goes with it and the client id |
| An unsent message is kept per conversation and shown in the list | `link-chat-composer.spec.ts` an unsent message is kept per conversation | `gaps_test` G4 (both) |
| Signing out forgets what was half written | `link-chat-composer.spec.ts` signing out forgets what was half written | `stack_test` signing out closes the screens (the drafts go with the session's chats) |
| @ a linked business, # an item with its price; / a document; its card | `link-chat.spec.ts` mentions; `chat-tools.spec.ts` | `tags_test`; `ref_screens_test` "/invoice", a card read now, "@" finds a business not linked |
| A bill's card, its query | `link-chat.spec.ts` a bill's card, its query | `cards_test`; `conversation_test` cards |
| Offers and counters on an order | `chat-deals.spec.ts` | `deal_test` |
| Older messages as the reader scrolls up; a new one does not move a reader above | `link-chat-thread.spec.ts` older messages load; a new message does not move a reader | `older_test` |
| The thread opens at the unread | `link-chat-thread.spec.ts` the unread messages are marked | `conversation_test` cards: opened at the unread |
| A long message is folded: Read more | `link-chat-mobile.spec.ts` a long message is folded | `fold_test` |
| Search in a conversation; the calendar | `link-chat-search.spec.ts` | `search_test` |
| Contact info: actions, mute, starred, ending the link | `link-chat-info.spec.ts` contact info | `contact_test` |
| The chat list narrows as the person types; search across chats | `chat-search.spec.ts` the chat list narrows | `chats_test` search: a letter narrows the list |
| A link request to a business not found by search (a phone number) | `links.spec.ts` a link code opens a request | `chats_test` a phone number; `explore_test` a phone number |
| A link's preview opens the page | (`Conversation.tsx` PreviewCard; no e2e) | `gaps_test` G11 |
| News chimes; a good moment's ring; the sound switch | `link-chat-live.spec.ts` the sound is off until chosen | `moments_test` |

## Taskiem's conversation

| Scenario | Web | App |
| --- | --- | --- |
| Words sent at once, Taskiem typing, its answer | `link-chat-taskiem.spec.ts` while an answer is being made there is a clock | `taskiem_test` words are shown at once with a clock |
| Reply buttons, the pressed one marked | `link-chat-taskiem.spec.ts` Taskiem's buttons are WhatsApp's reply buttons | `taskiem_test` a reply button sends its id; **server** sets up their page (a reply button pressed) |
| A message that did not go comes back to the box | `link-chat-taskiem.spec.ts` a message that did not go comes back to the box | `taskiem_test` a send that fails; `lifetime_test` B5 |
| What Taskiem said while you were away is counted, from where it began | `link-chat-taskiem.spec.ts` a message that came from WhatsApp is marked | `taskiem_test` (unread); `gaps_test` G13 |
| Its bubbles have Copy | `link-chat-taskiem.spec.ts` Taskiem's bubbles have the menu | `taskiem_test` G9 |
| A voice note and a photo to Taskiem | `link-chat-files.spec.ts` a voice note and a photo to Taskiem | `files_test` (the composer is shared); taskiem's own: not yet a test of its own |
| Older messages; new ones followed only at the bottom | `link-chat-thread.spec.ts` the Taskiem conversation loads older messages | `older_test`; `taskiem_test` an answer comes in view with the cards still below it |
| An owner sets up by talking: the page, a workflow published and run | (WhatsApp's: `api/whatsapp_*_test.go`) | **server** sets up their page; publishes a workflow by name, runs it, and approves its request from the card |
| The briefing, alert cards, Later, Got it, "briefing" | `chat-alerts.spec.ts` Later puts a card off | `taskiem_test` the cards; G10 |
| Approvals from the card; a rule asking for a code | `chat-alerts.spec.ts` an approval whose rules ask for a code | `taskiem_test` approving, a rule asking for a code, declining; **server** approves its request from the card |
| Give this to… | `chat-staff.spec.ts` | `taskiem_test` "Give this to…" a low stock item; `ref_screens_test` |
| Connecting a service from the conversation | (`Connect.tsx`; `api/connect_links_test.go`) | `connect_test`; `TestConnectFromTheWebChat` |

## Customers' conversations

| Scenario | Web | App |
| --- | --- | --- |
| The list, a conversation opened and read | `customer-accounts.spec.ts` | `customer_test` the list |
| A reply at once; one that could not go comes back | `customer-accounts.spec.ts` | `customer_test` a reply; a reply that could not go |
| STOP honoured; after 24 hours, a template | `customer-accounts.spec.ts` | `customer_test` STOP; after the 24 hours |
| Against the real server | `customer-accounts.spec.ts` (with the fake WhatsApp) | not yet: the server's fake WhatsApp is the browser tests' (`whatsapp.spec.ts`); the app's server tests do not start it |

## Only on one side, and why

- **The web only**: the keys of a computer (`link-chat-keys.spec.ts`), a
  computer's two panes and its camera (`link-chat-mobile.spec.ts`, the
  computer cases), tabs and the browser's notifications
  (`link-chat-live.spec.ts`: the tab's count, a tab in the background, the
  question once, several tabs), pasting and dropping files, HEIC made JPEG
  in the browser, the content security policy (`link-chat-csp.spec.ts`).
  A phone has none of these; its own (the app lock, Your phones, the share
  sheet, the browser tab for connecting) are the app's tests.
- **The app only**: the app lock and leaving it (`lock_test`,
  `stack_test`), a session ended or another business (`stack_test`),
  lifetimes on a slow network (`lifetime_test`), the files kept and asked
  again (`cache_test`), Settings (`settings_test`).
- **Notifications to a closed app** come with M4.
