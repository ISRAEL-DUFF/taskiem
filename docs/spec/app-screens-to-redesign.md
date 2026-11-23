# App screens built or touched by "Taskiem for everyone" (stages 1 to 5a), for redesign

Date: 18 November 2026 · Branch `ccr-87497aa3-gf060z` (PR #198) · Base: main at 95c33d23 (after main's app redesign)

The founder will redesign these screens to the app's own look ([mobile-app-look.md](mobile-app-look.md) and the drawings). Many of them were built to match the web and borrow its look. This list names every app screen, sheet and shared widget the branch added or changed, where it lives, how a person reaches it, what it holds, and where it borrows the web's look today (from a read-only audit on 18 November 2026). Behaviour and wording are not in question here, only the look.

**18 November:** every screen below tagged [web], [off] or [minor] has been redesigned to the app's own look, in the patterns of main's redesign (TkPageScreen, TkSection and TkListRow, TkFormSheet and TkFormGroup, TkTopCard, TkCallout, TkCta, TkSmallButton and TkButtonRow, TkChips, CodeBoxes, and the group screens' pickOneOf, pickAction and askSure). Each is now tagged [done 18 Nov] with what changed. One thing is left out on purpose: a business chat on You has no Search button, because business chats have no search on the server yet.

Tags: **[web]** clearly the web's look · **[off]** unlike the app's own screens · **[minor]** · **[fine]** already the app's look · **[done 18 Nov]** redesigned to the app's own look on 18 November (what changed is said beside it).

Kit notes that explain most tags:
- `TkButton` (`kit/kit.dart`) is the web's button. The app's buttons are `TkCta`, `TkSmallButton`, `TkButtonRow`, `CardButton`, `AuthButton` and `TkHeadButton`.
- `TkRadio`, `TkChoiceRow`, `TkPill`, `TkBadge`, `showTkModal` and `showTkModalChoices` are web parts.
- A `TkSheet` not wrapped in `TkAppLook` draws the web's checkboxes and links.

## 1. Signing in and getting started (stage 1)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| Welcome: the phone way in (number, code, name, 18+) | `screens/welcome.dart` `WelcomeScreen` | First open, signed out | Phone number, WhatsApp/SMS code, name, adult check | [done 18 Nov] page ground; AuthButton for Continue, Create and Chat on WhatsApp; the code as CodeBoxes |
| Sign in (changes) | `screens/sign_in.dart` | Signed out | Phone way in added beside email | [fine] |
| Second proof (step-up into a business) | `screens/second_proof.dart` `SecondProof` | Opening a business that needs passkey, email link, password or PIN | The ways to prove it's you | [done 18 Nov] ways in as rows, password and PIN in form groups with TkCta, a sent link as a callout; sheet in TkAppLook |
| Switcher: You and your businesses | `screens/switcher.dart` `SwitcherSheet` | Head of You, a business's menu | Rows for You and each business; passkey step | [done 18 Nov] sheets in TkAppLook; Use your passkey a TkCta; an organisation's sign-in a callout |
| Start something | `screens/start_something.dart` `StartSomethingScreen` | You › Start something | A business or workflows; what the business does | [done 18 Nov] large-title page; choices as ticked rows; the kind as sign-up's KindTile; name in a form group; TkCta |
| Change number | `screens/you.dart` `ChangeNumberScreen` | You › Settings | New number, code, confirm | [done 18 Nov] large-title page; number in a form group; the old-number question as rows; codes as CodeBoxes on a card |

## 2. You: home, Chats tab and settings (stages 1 to 3)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| You home (Chats tab head, search, list) | `screens/you.dart` `YouHome` | Signed in on the person's side | Chats with businesses and people, Requests row, Archived row, invitations, Start something, Your businesses, second-proof notice, contacts-on-Taskiem notice | [done 18 Nov] main's head (Chats large, the name under it, ⋯ with Settings, Switch and Start something, + primary); the app's search box; All, Unread, Archived chips; the locked notice a callout; Start something a row |
| Invitation row | `screens/you.dart` `_InvitationRow` | You home | Join or decline a business | [done 18 Nov] Join and Decline as TkSmallButtons in a TkButtonRow |
| You › Settings | `screens/you.dart` `YouSettingsScreen` | You head | Photo, name, phone, Change number, notifications, privacy, chats with people, contacts, your link, blocked people, bank account for shares, Your standing, Sign out | [done 18 Nov] you at the top (photo, name, email); Change number a row; Sign out a red row |
| Your photo card | `screens/you_account.dart` `MyPhotoCard` | You › Settings | Photo only you see | [fine] |
| Notifications | `screens/you_account.dart` `YouNotificationsPage` | You › Settings | Kinds (businesses, people, requests), preview, sound | [fine] |
| Privacy | `screens/you_account.dart` `YouPrivacyPage` | You › Settings | What businesses see, links to privacy with people and the lawful-requests page | [fine] |
| Delete account | `screens/you_account.dart` `DeleteAccountScreen` | You › Settings › Privacy | Fresh proof, then delete | [done 18 Nov] ways to confirm as rows; the code as CodeBoxes; Delete my account red; a refusal a callout; Privacy's facts as TkLines |
| Saved addresses | `chat/addresses.dart` `SavedAddressesPage`, `AddressSheet` | You › Settings, a shop's check step | Saved delivery addresses | [fine] |
| Links on You | `screens/you_links.dart` | App links | Routing only | n/a |
| Home tabs (changes) | `screens/home_tabs.dart` | Signed in | Orders tab kept in place, re-read when shown | [fine] |
| Saved things (a business's) | `chat/saved_accounts.dart` `SavedAccountsPage(mine: false)`, `OpenWalletSheet` | A business's Settings › Saved | The business's saved things, a filter by kind; its bank accounts the bank's name checked through its payments account; for an owner with none, the step to connect one (Open a Taskiem wallet here, or an account it has on the web) | [done 20 Nov] built in the new look: the step a callout over its rows; staff see the list without the step or Save |
| Settings in a business (changes) | `screens/signed_in.dart` | A business's Settings | Switch row, Customers section, Discover, devices | [fine] |

## 3. Chats with businesses on You (stage 2) and the customer's side

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| A chat with a business | `chat/my_chat.dart` `MyChatScreen` | You › Chats, a chat link, a push | The thread, cards, composer, request bar, offers, verified line | [done 18 Nov] the business's photo opens its profile. Search is not added: business chats have no search on the server yet |
| Report a business | `chat/my_chat.dart` `_ReportSheet` | A business chat's ⋯ | Reason, consent | [done 18 Nov] TkFormSheet in TkAppLook, reasons and consent as rows with the app's tick |
| Archived chats | `chat/my_chat.dart` `ArchivedChatsScreen` | You home | Archived business and people chats | [fine] |
| Tools in a business chat (the + sheet) | `chat/my_chat_tools.dart` `_ToolsSheet` | A business chat's + | Their shop, Book, Your orders, Pay, Share | [fine] |
| Their shop and the check step | `chat/my_chat_tools.dart` `ShopSheet` | Tools › Their shop | Items, bag, how you get it, zones, address, pay | [done 18 Nov] pick up or delivery as TkChips, areas as chips, saved addresses as ticked rows; Sold out a CardPill |
| Your orders with this business | `chat/my_chat_tools.dart` `MyOrdersSheet` | Tools › Your orders | Orders and bookings | [fine] |
| Book | `chat/book_sheet.dart` (changes) | Tools › Book | Services, times | [fine] |
| Cards in a chat (order, pay, booking, product, invoice, delivery) | `chat/door.dart` `DoorCard`, `DoorBubble`, `OfferMark`, `TransferWarning`, `DoorTicks`, `BusinessFace`, `LineMark` | Inside every chat on You | Live cards, ticks, offers, the transfer warning | [done 18 Nov] View order › / View delivery › as the card's foot under a hairline; declined, cancelled, no-show red; Offer a CardPill |
| Your own bubble (shared) | `chat/thread.dart` `BubbleFrame`, `ownWords` | Every chat on You and every people chat | Your own pine bubble | [done 18 Nov] thread.dart's ownWords has main's pine fix (tkSoft, tkLookOnPine) |
| Send sheet and card picker | `chat/customer_app.dart` `_CardPicker`, `_RichMenu` | Business side: sending a card to a customer | Cards to send | [fine] |
| Chat link (opened) | `screens/chat_link.dart` `ChatLinkScreen`, `ChatLinkCardView` | A `/m/{code}` link | The business, Chat on Taskiem or WhatsApp | [done 18 Nov] Add your phone a row |
| Your chat link (business side) | `chat/chat_link_page.dart` `ChatLinkPage`, `_CustomersSheet` | A business's Settings › Customers | QR, link, new link, shop's chat button, offers | [done 18 Nov] the shop's chat button as TkChips of three; the QR on qrGround |
| A customer's thread, both lines (business side) | `chat/customer.dart` (changes), `_LineChooser` | A business's Chats › a customer | App and WhatsApp lines in one thread, which line the reply goes to | [done 18 Nov] Your reply goes is TkChips (gains off: a shut line quiet, with why) |
| Chat rows marks (shared) | `chat/chats.dart` `ChatRow` marks | Business Chats list and You's list | "Business", "WhatsApp", "App" marks | [done 18 Nov] CardPill: a stopped line red, WhatsApp lime, the rest plain |
| Customers in the Taskiem chat, Tools › Customers, "Write to them in the app" | `chat/conversation.dart`, `chat/taskiem.dart`, `chat/tools.dart`, `chat/tools_panel.dart`, `chat/storefront_orders_page.dart` (small changes) | A business's chats and orders | Entry points | [fine] |
| Attachments and composer (changes) | `chat/attachments.dart`, `chat/composer.dart` | Every chat | Files from You's chats | [fine] |

## 4. Discover and Orders on You (stage 3)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| Discover | `chat/discover.dart` `DiscoverTab` | You › Discover | Search, area, categories, businesses | [done 18 Nov] categories as Explore's chips; Show more businesses the app's small button (pages still on a tap: its races are tested) |
| A business's profile | `chat/discover.dart` `DiscoverProfileScreen` | Discover | Chat, Their shop, Book, Share, Report | [done 18 Nov] Chat, Their shop and Book as a TkButtonRow of TkSmallButtons; sharing a link under them |
| Report a listing | `chat/discover.dart` `DiscoverReportSheet` | A profile's ⋯ | Reason | [fine] |
| Discover settings (business side) | `chat/discover_settings.dart` `DiscoverSettingsPage`, `DiscoverHoursSheet` | A business's Settings › Discover | Listing, category, hours, what you offer | [done 18 Nov] CardPills; the category from pickOneOf; notes tinted as callouts (whole words) |
| Your orders | `chat/your_orders.dart` `YourOrdersTab` | You › Orders | Every order and booking | [done 18 Nov] Show older orders the app's small button |
| An order | `chat/your_orders.dart` `YourOrderPage` | Your orders | Lines, state, Pay, Chat, Share | [done 18 Nov] Pay and Chat as a TkButtonRow; sharing a link under them |

## 5. People chats (stage 5a)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| A chat with a person | `chat/people_chat.dart` `PeopleChatScreen`, `PeopleBubble` | You › Chats, a push, a link | Messages, ticks, replies, reactions, forward, delete, photos, voice, files, places, cards, typing, last seen | [done 18 Nov] reactions as GroupReactionChips with counts; Search a round head button; delete and confirmations as pickAction and askSure |
| Request bar | `chat/people_chat.dart` `_RequestBar` | A request opened | Accept, Delete, Block and report | [fine] |
| Contact info | `chat/people_chat.dart` `PersonInfoScreen` | A people chat's head | Card, mute, archive, block, report, where the chat is kept | [done 18 Nov] where the chat is kept a quiet note (as Group info) |
| Report / Block and report | `chat/people_chat.dart` `_ReportSheet` | A people chat or request | Reason, consent to send the last 20 | [done 18 Nov] TkFormSheet in TkAppLook, each choice a row with the app's tick |
| Requests | `chat/people_find.dart` `PeopleRequestsScreen` | You home | Waiting first messages | [done 18 Nov] what they are, a quiet note under the list |
| New chat | `chat/people_find.dart` `NewPeopleChatScreen` | You home + | By number, by link, scan, from contacts, invite | [done 18 Nov] large-title page; the ways in as rows (Scan among them); number and link in form groups with the app's buttons; Not on Taskiem yet a callout with Invite |
| A found person | `chat/people_find.dart` `FoundPerson` | New chat, a link | Name, photo, Chat, Add to contacts | [done 18 Nov] a top card, the name in the display type, Chat and Add as a TkButtonRow |
| A person's link (opened) | `chat/people_find.dart` `PersonLinkScreen` | A `/p/{code}` link | Who it is, Chat, Add | [fine] |
| Your link and QR | `chat/people_find.dart` `MyPersonLinkScreen` | You › Settings, New chat | QR, Share, Copy, Replace | [done 18 Nov] as GroupLinkScreen: a top card, the QR on qrGround, Share and Copy, Replace a row |
| Name a contact | `chat/people.dart` `askContactName` (`_NameForm`) | Add to contacts anywhere | A name | [done 18 Nov] TkFormSheet |
| From your contacts | `chat/people_contacts.dart` `ContactsMatchScreen` | New chat › From your contacts | Explainer, agree, found people | [done 18 Nov] the ask on a top card, TkCta and a Not now link; the day's limit a callout |
| Your contacts | `chat/people_contacts.dart` `PeopleContactsScreen` | You › Settings | Contacts, rename, remove | [done 18 Nov] a contact's actions from pickAction; removing asks with askSure |
| Scan a QR code | `chat/people_scan.dart` `_ScanScreen` | New chat › Scan | Camera, paste a link | [done 18 Nov] what to do under the camera; the other way a link |
| Send a place | `chat/people_place.dart` `_PlaceSheet` | A people chat's + › A place | Where you are, a name, map link | [done 18 Nov] in TkAppLook; a problem a callout with Settings; the map a link; the name in a form group |
| Search messages | `chat/people_search.dart` `PeopleSearchScreen`, `FoundRow` | Search bar on Chats, a chat's menu | Results with snippets | [done 18 Nov] the app's search box (TkSearchBox); the note quiet |
| Starred messages | `chat/people_search.dart` `PeopleStarredScreen` | Search screen, Settings | Starred messages | [fine] |
| Privacy with people | `chat/people_settings.dart` `PeoplePrivacyPage` | You › Settings | Who can find me, message me, last seen, photo, number, read ticks, contacts; Check my contacts daily (this phone’s switch) | [done 18 Nov] each choice from pickOneOf |
| Blocked people | `chat/people_settings.dart` `BlockedPeopleScreen` | You › Settings | Blocked list, Unblock | [fine] |
| Share and split cards in a chat | `chat/people_cards.dart` `PeopleCardView`, `_LiveCardSheet`, `_CardPicker`, `_SplitForm` | A people chat's + › Share | Business, product, order, delivery, your share | [fine] |
| Forward / share-with picker | `chat/people_cards.dart` `_PeoplePicker` | Forward, Share with someone | Chats to pick | [off] web radio and checkbox rows with a hand-laid face (`TkListRow` has `face:` and a tick box) |
| Saved things (yours) | `chat/saved_accounts.dart` `SavedAccountsPage(mine: true)`, `SavedAccountSheet`, `SavedThingSheet` | You › Settings | Your own saved accounts, people, places, texts and accounts with a provider (saved objects S0, S1): a filter by kind; a bank account masked, Change and Show the number with a passkey; Remove | [done 20 Nov] built in the new look: rows with a Checked or Not checked pill, the bank and its masked number kept on one line; actions from pickAction; the sheet's bank a TkPickBox |
| Bank account for shares | `chat/people_cards.dart` `PayoutAccountPage` | You › Settings | Bank, number, name | [done 18 Nov] large-title page; the account in a form group; Save a TkCta; Remove it a red row |
| Your standing | `chat/people_standing.dart` `YourStandingScreen` | You › Settings, a push, a refused send | State, decisions, appeals | [done 18 Nov] where you stand on a top card; each decision a row with its state, its story and Appeal in a sheet |
| Appeal | `chat/people_standing.dart` `_AppealSheet` | Your standing | Your words | [done 18 Nov] TkFormSheet |
| Standing notice | `chat/people_standing.dart` `StandingNotice` | Top of You when unseen | A short notice | [done 18 Nov] a red TkCallout with Read |
| Live updates and presence | `chat/people_live.dart`, `app/presence.dart` | Behind You | No screen | n/a |

## 5b. Groups (stage 5b, branch `stage5b-groups-app`)

Built in the app's own look from the start (no `TkButton`, web modal, `TkRadio` or `TkChoiceRow`; every sheet in `TkAppLook`; guarded by `test/groups_test.dart`'s look test), so listed for the founder's review rather than as web borrowings. Where a group screen reuses a 5a part, the 5a part's tag above still applies.

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| A group | `chat/group_chat.dart` `GroupChatScreen` | You › Chats, a push, a link | Messages with each sender's name and face, events and Taskiem's as centred pills, reaction chips with counts, @mention picker, Read by, closed/left/admins-only callouts | [fine] head with the round Search button and ⋯; the bubble is 5a's `PeopleBubble` (its [off] notes above hold), the reactions are chips with counts as main's |
| Message info (Read by) | `chat/group_chat.dart` `showReadBy` | Hold your message › Info | Read by, delivered to, how many don't have it | [fine] |
| Who reacted | `chat/group_chat.dart` `showGroupReactions` | Hold a message, or hold a reaction chip | Each member's reaction | [fine] |
| Group info | `chat/group_info.dart` `GroupInfoScreen` | A group's head or ⋯ | Top card (photo, name, description, Add people, Invite link, Edit), waiting to join, notifications switches, settings (who posts, who edits, approve new members), members, invited, archive, report, leave, delete, where groups are kept | [fine] |
| Edit the group | `chat/group_info.dart` `_InfoForm` | Group info › Edit | Name, description | [fine] `TkFormSheet` |
| Invite link | `chat/group_info.dart` `GroupLinkScreen` | Group info › Invite link | Top card with QR on `qrGround`, Share and Copy, replace, turn off, approve new members | [fine] |
| Report a group or a member | `chat/group_info.dart` `_ReportSheet` | Group info, a member, ⋯ | Reason, note, consent, then leave or block | [fine] `TkFormSheet`, app ticks |
| Appeal a closed group | `chat/group_info.dart` `_AppealSheet` | The closed callout, Group info | Why it should open | [fine] |
| New group | `chat/group_new.dart` `NewGroupScreen` | New chat › New group | Photo, name, description, people, Make the group | [fine] |
| People to add | `chat/groups.dart` `pickGroupPeople` | New group, Group info › Add people | Contacts and chats with a search and app ticks | [fine] |
| Add answers | `chat/groups.dart` `showGroupAdded` | After adding | Each person's answer in the server's words | [fine] |
| Forward to chats and groups | `chat/groups.dart` `pickChatsAndGroups` | Forward in a group or a people chat; Share with someone (Discover, a business's chat and shop, an order) | People chats and groups with app ticks | [fine] (5a's `_PeoplePicker` is no longer reached from the app) |
| Media and files (on Group info) | `chat/group_media.dart` `GroupMediaSection` | Group info | The latest four as square tiles, See all | [fine] (stage 5b leftovers) |
| Media and files | `chat/group_media.dart` `GroupMediaScreen` | Group info › Media and files › See all | Photos three to a row, videos, voice notes and files as rows, Show older; each opens in the chat | [fine] (stage 5b leftovers) |
| Join a group | `chat/group_new.dart` `GroupJoinScreen` | A `/g/{code}` link, a scanned QR, New chat's link box | Photo, name, count, Join, waiting, take back | [fine] |
| Invites to groups | `chat/group_new.dart` `GroupInvitesSection` | Requests | Accept, Decline | [fine]; sits in 5a's Requests screen ([off] above) |
| Choices, confirms and actions | `chat/groups.dart` `pickOneOf`, `askSure`, `pickAction` | Settings, delete, leave, member actions | App-look sheets in place of the web's modals | [fine]; 5a screens could move onto them |
| Who can add me to groups | `chat/people_settings.dart` (a row) | Privacy with people | My contacts, Everyone, Nobody | [fine] a `pickOneOf` sheet; the page's other rows keep their [minor] note |

## 5c. Ordering together (stage 5b C, branch `stage5b-together`)

Built in the app's own look (`TkAppLook` sheets, `TkSummary`, `TkSection`, `TkListRow`, `TkCallout`, `TkSmallButton`, `TkCta`; no `TkButton`, web modal, `TkRadio` or `TkChoiceRow`), guarded by `test/basket_test.dart`.

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| Order together from… | `chat/basket.dart` `pickTogetherBusiness` | The + of a people chat or a group › Order together | The businesses you chat with | [fine] |
| The basket | `chat/basket.dart` `BasketSheet` | A basket card's Open basket | Pine summary (total, state, items, delivery, paid), what stands in the way (`TkCallout`), the order as it moves, who wants what with each share, the actions, the holder's pick-up or delivery and area, hand on, cancel, where it is kept | [fine]; the card in the chat is 5a's `PeopleCardView` (its notes hold) |
| My items | `chat/basket.dart` `_MineStep` | The basket › Add my items | The shop's items, each size and colour a row, with a stepper | [fine]; a product's sizes are rows rather than a picker |
| Place the order | `chat/basket.dart` `_PlaceStep` | The basket › Place the order | Who pays (rows marked Chosen), address, landmark, note | [fine] |
| Where to be paid back | `chat/basket.dart` `_AccountStep` | Send each their share, with no saved account | Bank, number, name | [fine] |

## 5d. What a business buys (stages S2 to U3, `what-a-business-buys.md`)

Built in the app's own look from the start (`mobile-app-look.md`, Stock and supplies: `TkPageScreen`, `TkSection` and `TkListRow`, `TkFormSheet` in `TkAppLook`, `TkTopCard`, `TkCallout`, `TkChips`, `pickOneOf`, `askSure`), so listed for the founder's review rather than as web borrowings. **23 November (PR review):** the choice rows that used `TkRadio` (Do you sell it?, the three answers of the question, a thing's status, Paid in cash or by transfer) are now `TkListRow(chevron: false)` with an icon square and the kit's `TkTick`, as `mobile/CLAUDE.md` asks.

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| Stock (changes) | `chat/stock_page.dart` `StockPage` | A business's Go to › Stock | What you count (All, Sold, Not sold), Low and At cost tiles, Running low; a failed load of the stock items a `TkCallout`; the head's + only for `table.edit` | [fine] |
| Add something to count | `chat/stock_items.dart` `AddToCountSheet` | Stock's + | Do you sell it? (ticked rows), name, unit, count, cost, price, levels, supplier | [fine] ticked rows since 23 Nov |
| Counted by | `chat/stock_items.dart` `CountedByRow`, `CountedBySheet` | A product's form | Its stock item, how many a sale uses; Change, Count by none | [fine] |
| The question on a line | `chat/treat_line.dart` `TreatQuestion`, `OpenLineQuestion`, `TreatSection` | An order's review, Confirm it, How each is treated | Just record it, Add to stock, Keep track of it (ticked rows), stock item, label, Remember for next time; shut to a few words once answered | [fine] ticked rows since 23 Nov |
| Pick a label | `chat/treat_line.dart` `LabelPickSheet` | The question's Label, a thing's Label | Your labels by use, Suggested, a new one typed, No label | [fine] |
| A new stock item | `chat/treat_line.dart` `NewStockItemSheet` | The question › Make a new one | Do you sell it? (ticked rows), name, unit | [fine] ticked rows since 23 Nov |
| Put many on Things we have? | `chat/treat_line.dart` `askTrackMany` | A `track_ask` answer | One row each, or Just record them | [fine] |
| Line marks | `chat/treat_line.dart` `LineMark` | Lines of an order to a supplier and of a bill | To stock or Things we have pill, the label; none on a bill line not matched to its order line | [fine] |
| Their price list and their shop (changes) | `chat/order_sheet.dart` `OrderSheet` | Order from a supplier's chat, a link | Each bag line asked unless remembered | [fine] |
| An order to a supplier (changes) | `chat/purchases_page.dart` `PurchasePage` | Supplies › an order | Confirm it (a supplier's draft), How each is treated, On Things we have | [fine] |
| Their quote in a chat (changes) | `chat/purchase_view.dart` `PurchaseView` | A supplier's quote in Taskiem's chat | Confirm their quote with the question on each line | [fine] |
| What you buy from them | `chat/supplier_items.dart` `SupplierItemsPage`, `_ItemSheet` | Supplies › Suppliers › a supplier | Each item remembered, changed or forgotten | [fine] |
| Labels | `chat/labels_page.dart` `LabelsPage`, `_RenameSheet` | Money › Settings › Labels | Labels by use, Rename, Merge into another | [fine] |
| Things we have | `chat/things_page.dart` `ThingsPage` | Money › Go to; an order's or a bill's On Things we have (that order's rows, asked by `order=`) | Top card (total, count, split by label), Newest first or By label, those no longer had | [fine] |
| One of Things we have | `chat/things_page.dart` `ThingPage` | Things we have › a row | Cost and status card, status as five ticked rows, details, where it came from (the order, or Added by hand with its day) | [fine] ticked rows since 23 Nov |
| Sold | `chat/things_page.dart` `SoldSheet` | A thing › Sold, Add what it sold for | The price, Paid in cash or by transfer (ticked rows) | [fine] ticked rows since 23 Nov |
| Add something we have | `chat/things_page.dart` `AddThingSheet` | Things we have's + | Name, cost, when, from whom, label, serial, warranty, note | [fine] |
| Spending by label | `chat/spending.dart` `SpendingCard`, `SpendingPage` | Money (the card), its See all | The split, the labels spent on most; by month, profit without Bought to keep | [fine] |
| A bill (changes) | `chat/bills_page.dart` `BillPage` | Money › Bills › a bill | Line marks, On Things we have for `table.read` | [fine] |

## 6. Shared parts the branch added or changed

| Part | File | Note |
| --- | --- | --- |
| `TkListRow` gained `face:` | `kit/mobile.dart` | [fine] |
| `TkTick`, a choice row's tick (`TkListRow(chevron: false, trailing: TkTick(on))`) | `kit/mobile.dart` | [fine] 23 Nov, in place of `TkRadio` on Stock and supplies' choices |
| `TkLoadMore` (load more as the list nears its end) | `kit/load_more.dart` | [fine] |
| Modal changes | `kit/modal.dart` | Web modals are still used by the screens above |
| Polling that pauses in the background | `kit/polling.dart` | n/a |
| Theme tokens (generated) | `theme/tokens.g.dart` | n/a |
| Routing, pushes, links | `main.dart`, `app/applinks.dart`, `app/notify.dart`, `app/openers.dart`, `app/route.dart` | n/a |

## Order of work, if the redesign goes screen by screen

1. Your own bubble's colours (`thread.dart`): a bug visible in every chat on You and every people chat.
2. You home and the people chat: the screens people see most.
3. Sheets drawing the browser's checkboxes and radios (the two report sheets, place, appeal, second proof, switcher).
4. The rest of the [web] screens, then the [off] and [minor] ones.

The web↔app parity test (`mobile/test/parity_test.dart`) does not cover any screen in sections 1 to 5 except sign-in. Per the founder's direction, the app does not have to match the web, so these screens should not be added to it.
