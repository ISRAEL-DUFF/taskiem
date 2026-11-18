# App screens built or touched by "Taskiem for everyone" (stages 1 to 5a), for redesign

Date: 18 November 2026 · Branch `ccr-87497aa3-gf060z` (PR #198) · Base: main at 95c33d23 (after main's app redesign)

The founder will redesign these screens to the app's own look ([mobile-app-look.md](mobile-app-look.md) and the drawings). Many of them were built to match the web and borrow its look. This list names every app screen, sheet and shared widget the branch added or changed, where it lives, how a person reaches it, what it holds, and where it borrows the web's look today (from a read-only audit on 18 November 2026). Behaviour and wording are not in question here, only the look.

Tags: **[web]** clearly the web's look · **[off]** unlike the app's own screens · **[minor]** · **[fine]** already the app's look.

Kit notes that explain most tags:
- `TkButton` (`kit/kit.dart`) is the web's button. The app's buttons are `TkCta`, `TkSmallButton`, `TkButtonRow`, `CardButton`, `AuthButton` and `TkHeadButton`.
- `TkRadio`, `TkChoiceRow`, `TkPill`, `TkBadge`, `showTkModal` and `showTkModalChoices` are web parts.
- A `TkSheet` not wrapped in `TkAppLook` draws the web's checkboxes and links.

## 1. Signing in and getting started (stage 1)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| Welcome: the phone way in (number, code, name, 18+) | `screens/welcome.dart` `WelcomeScreen` | First open, signed out | Phone number, WhatsApp/SMS code, name, adult check | [web] Continue and Create are `TkButton`s; the code is a 6-digit field, not `CodeBoxes`; ground is the web's `t.bg`; "Chat on WhatsApp" is a `CardButton` |
| Sign in (changes) | `screens/sign_in.dart` | Signed out | Phone way in added beside email | [fine] |
| Second proof (step-up into a business) | `screens/second_proof.dart` `SecondProof` | Opening a business that needs passkey, email link, password or PIN | The ways to prove it's you | [web] five stacked `TkButton`s; sheet not in `TkAppLook`, so "Send it again" is the web's underlined link |
| Switcher: You and your businesses | `screens/switcher.dart` `SwitcherSheet` | Head of You, a business's menu | Rows for You and each business; passkey step | [web] "Use your passkey" is a `TkButton`; sheets not in `TkAppLook` [minor] |
| Start something | `screens/start_something.dart` `StartSomethingScreen` | You › Start something | A business or workflows; what the business does | [web] web radio lists; a full-width `TkButton` foot; title in 17/700 instead of the app's display type |
| Change number | `screens/you.dart` `ChangeNumberScreen` | You › Settings | New number, code, confirm | [web] stacked `TkButton`s in a paragraph-led panel; code in a field, not `CodeBoxes` |

## 2. You: home, Chats tab and settings (stages 1 to 3)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| You home (Chats tab head, search, list) | `screens/you.dart` `YouHome` | Signed in on the person's side | Chats with businesses and people, Requests row, Archived row, invitations, Start something, Your businesses, second-proof notice, contacts-on-Taskiem notice | [off] head is "Chats" and "You" side by side, not main's big title with the name under it; + is not primary and there is no ⋯; Switch and Settings get their own head buttons. [off] hand-made search pill, not `_Search`/`TkSearchBox`. [off] no All/Unread/Starred/Archived chips; Requests and Archived are rows. [web] second-proof notice is a panel with a `TkButton`; "Start something" is a full-width `TkButton` with a hint paragraph |
| Invitation row | `screens/you.dart` `_InvitationRow` | You home | Join or decline a business | [web] Join and Decline are `TkButton`s |
| You › Settings | `screens/you.dart` `YouSettingsScreen` | You head | Photo, name, phone, Change number, notifications, privacy, chats with people, contacts, your link, blocked people, bank account for shares, Your standing, Sign out | [off] label-over-value lines instead of `YouCard`; [web] "Change number" and "Sign out" are `TkButton`s |
| Your photo card | `screens/you_account.dart` `MyPhotoCard` | You › Settings | Photo only you see | [fine] |
| Notifications | `screens/you_account.dart` `YouNotificationsPage` | You › Settings | Kinds (businesses, people, requests), preview, sound | [fine] |
| Privacy | `screens/you_account.dart` `YouPrivacyPage` | You › Settings | What businesses see, links to privacy with people and the lawful-requests page | [fine] |
| Delete account | `screens/you_account.dart` `DeleteAccountScreen` | You › Settings › Privacy | Fresh proof, then delete | [web] proof ways are three `TkButton`s; "Delete my account" is a pine button, not red; code not in `CodeBoxes`; `_Fact` is a hand-made label/value table [minor] |
| Saved addresses | `chat/addresses.dart` `SavedAddressesPage`, `AddressSheet` | You › Settings, a shop's check step | Saved delivery addresses | [fine] |
| Links on You | `screens/you_links.dart` | App links | Routing only | n/a |
| Home tabs (changes) | `screens/home_tabs.dart` | Signed in | Orders tab kept in place, re-read when shown | [fine] |
| Settings in a business (changes) | `screens/signed_in.dart` | A business's Settings | Switch row, Customers section, Discover, devices | [fine] |

## 3. Chats with businesses on You (stage 2) and the customer's side

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| A chat with a business | `chat/my_chat.dart` `MyChatScreen` | You › Chats, a chat link, a push | The thread, cards, composer, request bar, offers, verified line | [off] head has no Search button and no tap on the avatar |
| Report a business | `chat/my_chat.dart` `_ReportSheet` | A business chat's ⋯ | Reason, consent | [web] not in `TkAppLook`: browser checkbox and radios |
| Archived chats | `chat/my_chat.dart` `ArchivedChatsScreen` | You home | Archived business and people chats | [fine] |
| Tools in a business chat (the + sheet) | `chat/my_chat_tools.dart` `_ToolsSheet` | A business chat's + | Their shop, Book, Your orders, Pay, Share | [fine] |
| Their shop and the check step | `chat/my_chat_tools.dart` `ShopSheet` | Tools › Their shop | Items, bag, how you get it, zones, address, pay | [off] pick up/deliver, zones and saved addresses are web radio lists (spec: a switch, chips, rows with a tick); "Sold out" is a web `TkPill` [minor] |
| Your orders with this business | `chat/my_chat_tools.dart` `MyOrdersSheet` | Tools › Your orders | Orders and bookings | [fine] |
| Book | `chat/book_sheet.dart` (changes) | Tools › Book | Services, times | [fine] |
| Cards in a chat (order, pay, booking, product, invoice, delivery) | `chat/door.dart` `DoorCard`, `DoorBubble`, `OfferMark`, `TransferWarning`, `DoorTicks`, `BusinessFace`, `LineMark` | Inside every chat on You | Live cards, ticks, offers, the transfer warning | [minor] `OfferMark` is a web `TkPill`; declined and cancelled are not red; no "View order ›" row under a hairline on order cards |
| Your own bubble (shared) | `chat/thread.dart` `BubbleFrame`, `ownWords` | Every chat on You and every people chat | Your own pine bubble | [off, a bug] this copy of `ownWords` lacks main's fix (`tkLookOnPine`, the soft veil), so pills, lime accents and file cards inside your own bubble take dark inks, and a `CardButton` is pine on pine |
| Send sheet and card picker | `chat/customer_app.dart` `_CardPicker`, `_RichMenu` | Business side: sending a card to a customer | Cards to send | [fine] |
| Chat link (opened) | `screens/chat_link.dart` `ChatLinkScreen`, `ChatLinkCardView` | A `/m/{code}` link | The business, Chat on Taskiem or WhatsApp | [minor] "Add your phone" is a `CardButton` on the bare page ground |
| Your chat link (business side) | `chat/chat_link_page.dart` `ChatLinkPage`, `_CustomersSheet` | A business's Settings › Customers | QR, link, new link, shop's chat button, offers | [minor] "Your shop's chat button" is a web radio list (spec: a switch of three); QR ground hard-coded white instead of `qrGround` |
| A customer's thread, both lines (business side) | `chat/customer.dart` (changes), `_LineChooser` | A business's Chats › a customer | App and WhatsApp lines in one thread, which line the reply goes to | [off] `_LineChooser` is a hand-made switch with hard-coded colours and comments saying "as the web's pills"; the shut line uses a web token |
| Chat rows marks (shared) | `chat/chats.dart` `ChatRow` marks | Business Chats list and You's list | "Business", "WhatsApp", "App" marks | [minor] web tokens instead of `CardPill` with the app's accents |
| Customers in the Taskiem chat, Tools › Customers, "Write to them in the app" | `chat/conversation.dart`, `chat/taskiem.dart`, `chat/tools.dart`, `chat/tools_panel.dart`, `chat/storefront_orders_page.dart` (small changes) | A business's chats and orders | Entry points | [fine] |
| Attachments and composer (changes) | `chat/attachments.dart`, `chat/composer.dart` | Every chat | Files from You's chats | [fine] |

## 4. Discover and Orders on You (stage 3)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| Discover | `chat/discover.dart` `DiscoverTab` | You › Discover | Search, area, categories, businesses | [web] "Show more businesses" is a `TkButton` (the app has `TkLoadMore`); [minor] categories are a segmented switch, not the drawn chips |
| A business's profile | `chat/discover.dart` `DiscoverProfileScreen` | Discover | Chat, Their shop, Book, Share, Report | [minor] stacked full-width `CardButton`s on the top card instead of a `TkButtonRow` of `TkSmallButton`s |
| Report a listing | `chat/discover.dart` `DiscoverReportSheet` | A profile's ⋯ | Reason | [fine] |
| Discover settings (business side) | `chat/discover_settings.dart` `DiscoverSettingsPage`, `DiscoverHoursSheet` | A business's Settings › Discover | Listing, category, hours, what you offer | [minor] web `TkPill`s; category picker is a web modal; `_Note` re-makes `TkCallout` |
| Your orders | `chat/your_orders.dart` `YourOrdersTab` | You › Orders | Every order and booking | [web] "Show older orders" is a `TkButton` (use `TkLoadMore`) |
| An order | `chat/your_orders.dart` `YourOrderPage` | Your orders | Lines, state, Pay, Chat, Share | [minor] stacked `CardButton`s instead of a `TkButtonRow` |

## 5. People chats (stage 5a)

| Screen | File | Reached from | Holds | Look today |
| --- | --- | --- | --- | --- |
| A chat with a person | `chat/people_chat.dart` `PeopleChatScreen`, `PeopleBubble` | You › Chats, a push, a link | Messages, ticks, replies, reactions, forward, delete, photos, voice, files, places, cards, typing, last seen | [off] reactions are one flat pill under the bubble, not main's chips hanging over the bubble with counts; Search is only in ⋯, not a round head button; delete for me/everyone is a web modal [minor] |
| Request bar | `chat/people_chat.dart` `_RequestBar` | A request opened | Accept, Delete, Block and report | [fine] |
| Contact info | `chat/people_chat.dart` `PersonInfoScreen` | A people chat's head | Card, mute, archive, block, report, where the chat is kept | [fine] apart from the "where this chat is kept" panel [minor] |
| Report / Block and report | `chat/people_chat.dart` `_ReportSheet` | A people chat or request | Reason, consent to send the last 20 | [web] not in `TkAppLook`: browser checkboxes and radios |
| Requests | `chat/people_find.dart` `PeopleRequestsScreen` | You home | Waiting first messages | [off] a description paragraph under the title |
| New chat | `chat/people_find.dart` `NewPeopleChatScreen` | You home + | By number, by link, scan, from contacts, invite | [web] form cards with full-width `TkButton`s; "Not on Taskiem yet" in 17/700 |
| A found person | `chat/people_find.dart` `FoundPerson` | New chat, a link | Name, photo, Chat, Add to contacts | [web] Chat and Add are `TkButton`s; name in 17/700 |
| A person's link (opened) | `chat/people_find.dart` `PersonLinkScreen` | A `/p/{code}` link | Who it is, Chat, Add | [fine] |
| Your link and QR | `chat/people_find.dart` `MyPersonLinkScreen` | You › Settings, New chat | QR, Share, Copy, Replace | [web] a paragraph, the QR and three `TkButton`s; QR ground hard-coded white (the app's "Linked businesses" page is the pattern) |
| Name a contact | `chat/people.dart` `askContactName` (`_NameForm`) | Add to contacts anywhere | A name | [web] a web modal with a `TkButton` (the app's pattern is a form sheet, as Saved addresses) |
| From your contacts | `chat/people_contacts.dart` `ContactsMatchScreen` | New chat › From your contacts | Explainer, agree, found people | [web] ask step is paragraph cards with stacked `TkButton`s; "Checked N of M" is a plain panel [minor] |
| Your contacts | `chat/people_contacts.dart` `PeopleContactsScreen` | You › Settings | Contacts, rename, remove | [fine] apart from the actions being a web modal [minor] |
| Scan a QR code | `chat/people_scan.dart` `_ScanScreen` | New chat › Scan | Camera, paste a link | [off] a description paragraph; [web] "Type or paste their link instead" is a `TkButton` |
| Send a place | `chat/people_place.dart` `_PlaceSheet` | A people chat's + › A place | Where you are, a name, map link | [off] not in `TkAppLook`; [web] "Open your phone's settings" and "See it on the map" are `TkButton`s |
| Search messages | `chat/people_search.dart` `PeopleSearchScreen`, `FoundRow` | Search bar on Chats, a chat's menu | Results with snippets | [off] a form field, not the app's search on a card; footnote paragraph [minor] |
| Starred messages | `chat/people_search.dart` `PeopleStarredScreen` | Search screen, Settings | Starred messages | [fine] |
| Privacy with people | `chat/people_settings.dart` `PeoplePrivacyPage` | You › Settings | Who can find me, message me, last seen, photo, number, read ticks, contacts; Check my contacts daily (this phone’s switch) | [minor] each choice is a form row whose value opens a web modal (the app's settings open a page or sheet of rows) |
| Blocked people | `chat/people_settings.dart` `BlockedPeopleScreen` | You › Settings | Blocked list, Unblock | [fine] |
| Share and split cards in a chat | `chat/people_cards.dart` `PeopleCardView`, `_LiveCardSheet`, `_CardPicker`, `_SplitForm` | A people chat's + › Share | Business, product, order, delivery, your share | [fine] |
| Forward / share-with picker | `chat/people_cards.dart` `_PeoplePicker` | Forward, Share with someone | Chats to pick | [off] web radio and checkbox rows with a hand-laid face (`TkListRow` has `face:` and a tick box) |
| Bank account for shares | `chat/people_cards.dart` `PayoutAccountPage` | You › Settings | Bank, number, name | [web] one paragraph card and `TkButton`s "Save" and "Remove it" (Remove not red) |
| Your standing | `chat/people_standing.dart` `YourStandingScreen` | You › Settings, a push, a refused send | State, decisions, appeals | [off] description paragraph; state is a plain panel; [web] decisions are stacked panels of long wrapped lines with a `CardButton` each |
| Appeal | `chat/people_standing.dart` `_AppealSheet` | Your standing | Your words | [minor] not in `TkAppLook` |
| Standing notice | `chat/people_standing.dart` `StandingNotice` | Top of You when unseen | A short notice | [off] hand-made box with web danger tokens (the app has `TkCallout`) |
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
| Forward to chats and groups | `chat/groups.dart` `pickChatsAndGroups` | Forward in a group or a people chat | People chats and groups with app ticks | [fine] (5a's `_PeoplePicker` stays for Share with) |
| Join a group | `chat/group_new.dart` `GroupJoinScreen` | A `/g/{code}` link, a scanned QR, New chat's link box | Photo, name, count, Join, waiting, take back | [fine] |
| Invites to groups | `chat/group_new.dart` `GroupInvitesSection` | Requests | Accept, Decline | [fine]; sits in 5a's Requests screen ([off] above) |
| Choices, confirms and actions | `chat/groups.dart` `pickOneOf`, `askSure`, `pickAction` | Settings, delete, leave, member actions | App-look sheets in place of the web's modals | [fine]; 5a screens could move onto them |
| Who can add me to groups | `chat/people_settings.dart` (a row) | Privacy with people | My contacts, Everyone, Nobody | [fine] a `pickOneOf` sheet; the page's other rows keep their [minor] note |

## 6. Shared parts the branch added or changed

| Part | File | Note |
| --- | --- | --- |
| `TkListRow` gained `face:` | `kit/mobile.dart` | [fine] |
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
