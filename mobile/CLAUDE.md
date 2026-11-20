# The phone app: how its screens are built

The app has its own look ("Direction B"), settled with the founder in
November 2026 and written up in `docs/spec/mobile-app-look.md`. Every screen
away from the web-matched chat is built in it. A screen that works but looks
like the old app (a paragraph under the title, full-width grey buttons,
cards of long wrapped lines, underlined links) is **not done**.

To build or change a screen, use the `mobile-screen` skill
(`.claude/skills/mobile-screen/`): it has the steps and the scripts to
render and compare.

## Before you write a screen

1. Read the part of `docs/spec/mobile-app-look.md` for that area (Settings,
   signing in, sheets and chats, the school…), and the screen's row in
   `docs/spec/app-screens-to-redesign.md`.
2. Find the closest screen that is already in the new look and copy its
   structure. Good models:
   - a page of rows: `lib/screens/you.dart` (`YouSettingsScreen`), `lib/screens/signed_in.dart`;
   - a form page: `lib/screens/you.dart` (`ChangeNumberScreen`), `lib/screens/start_something.dart`;
   - a sheet with a form: the report sheet in `lib/chat/my_chat.dart`;
   - a list with filters: `lib/chat/discover.dart`, `lib/chat/your_orders.dart`;
   - signing in: `lib/screens/welcome.dart`, `lib/screens/second_proof.dart`.
3. Build from kit parts; never from raw `Container`s, `Column`s of `Text`s
   and colours typed in by hand.

## The parts to build with

| For | Use |
| --- | --- |
| A page | `TkPageScreen(title:, backLabel: 'Back to X', titleSize: 30, children:)` (22 for one document). A tab: `TkScreen`. |
| A group of rows | `TkSection(title:, children: [TkListRow(...)])` |
| One row | `TkListRow(title:, sub:, icon: 'b-…', square: look.x, onTap:)`; `trailing:` for a switch or button, `chevron: false` for a choice with a tick |
| The card at the top | `TkTopCard`; `MyPhotoCard` for the person |
| Fields | `TkFormGroup(title:, children: [TkKitField(label:, child:)])` |
| A code | `CodeBoxes` (from `screens/sign_in.dart`), inside a `TkFormGroup` |
| A field chosen, not typed (a bank, a class, a date) | `TkKitField(field: false, child: TkPickBox(label:, shown:, onTap:))`, opening `pickOneOf` or a date |
| The page's main button | `TkCta(label:)`; `danger: true` to delete |
| Two buttons side by side | `TkButtonRow([...])` |
| A small button in a row or under a list | `TkSmallButton` (`primary:` / `danger:`) |
| A link | `TkLink(label:, bold: true)` |
| A note, a warning, a locked thing | `TkCallout(leading:, title:, sub:, color:)` |
| Filters | `TkChips` (`off:` for ones that cannot be chosen) |
| Search | `TkSearchBox` |
| A status mark | `CardPill(words, CardTone.x)` |
| A sheet with a form | `TkFormSheet(title:, action:, onAction:, children:)` |
| Pick one of a few | `pickOneOf` (`chat/groups.dart`); of many (banks), with `searchHint:` for a search box over them |
| A menu of actions | `pickAction` |
| "Are you sure?" | `askSure(danger: true)` |
| Text that is several lines of facts | `TkLines` |

Icons are the mockup's own: `'b-…'` names in `lib/theme/look_icons.dart`.
Colours come from the theme: `TkTheme.tokens(context)` (`t.text`, `t.muted`,
`t.danger`, …) and the look: `TkLook.of(context)` (`look.lime`,
`look.teal`, `look.violet`, …). Never type a `Color(0x…)` for something the
theme has.

## Do not use (on a new or redesigned screen)

- `TkButton` as a page's button: `TkCta` or `TkSmallButton`.
- `TkErrorBox` or a raw red `Container` for a note: `TkCallout` (a failed
  save is a toast, as the screens beside it do).
- A paragraph under the title: the description goes behind the head's (i)
  (`TkPageScreen(description:)`).
- Underlined links: `TkLink(bold: true)`.
- `TkLoadMore`'s auto-loading where the tests tap "Show more" themselves:
  a `TkSmallButton` the person taps.
- Material widgets (`ElevatedButton`, `ListTile`, `AlertDialog`…): the app
  does not use Material's look.

## Mistakes that have happened before

- **Your own words on the pine bubble.** In the light theme your bubble is
  dark pine and its words white. `ownWords` recolours the theme *inside*
  the bubble, so a colour read *outside* it (a `t` taken at the top of
  `build`) is still the dark one. Work the ink out yourself:
  `final ink = mine && TkTheme.of(context).mode != TkMode.dark ? white : t.text;`
  (see `chat/door.dart`, `chat/customer.dart`).
- **Code boxes vanish** on the page's ground: put them in a `TkFormGroup`.
- **A stamp over the words.** A bubble's time sits over the end of the last
  line; a longer stamp (with "on WhatsApp", a template's note) goes under
  the words (`BubbleFrame(inline: false)`).
- **A chip stretched to the full width**: no `alignment:` on a chip's
  `Container`.
- **A sheet in the wrong look**: open sheets inside the app's look
  (`TkAppLook`); a screen pushed above it keeps it.
- **A stand-in server that does not answer** shows "Something went wrong"
  in a preview: that is the test's server, not the screen. Fields the API
  marks `required` (see `api/openapi/openapi.yaml`) must be in the stand-in.

## When a screen is done

- It has been **rendered and looked at**, in light **and** dark, at phone
  size (340–390 wide), scrolled to the bottom as well as the top. Tests
  passing is not enough: look at the pictures.
- It matches its drawing in `docs/spec/mobile-app-look.md` (or the
  drawing the founder gave), and the screens around it.
- Large text (`test/sizes_test.dart`) cuts nothing off, and what can be
  pressed is labelled and 48 px (`test/targets_test.dart`).
- `flutter analyze` is clean and `flutter test` passes, including the
  screenshot tests (`test/looks_test.dart`). If you changed how a screen
  looks **on purpose**, update its pictures
  (`flutter test test/looks_test.dart --update-goldens`, on Linux), look at
  the new ones, and say so in the PR.
- Its row in `docs/spec/app-screens-to-redesign.md` is marked done.

## Running things

```sh
export PATH=/root/sdk/flutter/bin:$PATH   # where Flutter is in a cloud session
cd mobile
flutter analyze
flutter test                              # everything (about 12 minutes)
flutter test test/looks_test.dart         # the screenshot tests
SNAP_DIR=/some/dir flutter test test/you_settings_test.dart   # pictures from snap() calls
```
