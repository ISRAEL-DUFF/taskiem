// The accents of the pages away from the chat (docs/spec/mobile-app-look.md,
// "Direction B"): written here by hand, not generated from the web's CSS,
// as those pages are the app's own (tokens.g.dart stays the web chat's).
//
// Each accent has two colours: the graphic one for lines, bars, dots and
// filled squares, and the ink one for words, which keeps 4.5:1 on a card
// (light) or 7:1 (dark). The same accent means the same thing on every
// page: lime is the page's main figure, teal is what moves along, orange
// is what goes out or waits, red is a leak, blue is on its way.

import 'package:flutter/widgets.dart';

import 'theme.dart';

class TkLook {
  const TkLook({
    required this.lime,
    required this.teal,
    required this.orange,
    required this.red,
    required this.blue,
    required this.violet,
    required this.limeInk,
    required this.tealInk,
    required this.orangeInk,
    required this.redInk,
    required this.blueInk,
    required this.violetInk,
    required this.glow,
    required this.ground,
    required this.card,
    required this.line,
    required this.bar,
    required this.on,
  });

  /// The page's ground, the cards on it (no border: the ground sets them
  /// apart), the hairline between rows, the bottom bar's frosted colour,
  /// and the open tab's colour.
  final Color ground, card, line, bar, on;

  final Color lime, teal, orange, red, blue, violet;
  final Color limeInk, tealInk, orangeInk, redInk, blueInk, violetInk;

  /// The top card's light, at its top left (dark only; none in light).
  final Color glow;

  /// Whether this is the look on your own pine bubble in the light.
  bool get onPine => identical(this, tkLookOnPine);

  /// The look in force.
  static TkLook of(BuildContext context) {
    final theme = TkTheme.of(context);
    return theme.look ?? (theme.mode == TkMode.dark ? tkLookDark : tkLookLight);
  }
}

const tkLookLight = TkLook(
  lime: Color(0xFF7ED321),
  teal: Color(0xFF13C2A3),
  orange: Color(0xFFFF8A3D),
  red: Color(0xFFFF3B5C),
  blue: Color(0xFF4C8DFF),
  violet: Color(0xFF8E7CF0),
  limeInk: Color(0xFF3F7A0E),
  tealInk: Color(0xFF087A66),
  orangeInk: Color(0xFFB4510E),
  redInk: Color(0xFFC81E3C),
  blueInk: Color(0xFF2559C9),
  violetInk: Color(0xFF5B45C9),
  glow: Color(0x00000000),
  ground: Color(0xFFF2F3F0),
  card: Color(0xFFFFFFFF),
  line: Color(0x12000000),
  bar: Color(0xDBF8F9F7),
  on: Color(0xFF0D3B2E),
);

/// The light look on pine (your own bubble in the light): the dark look's
/// bright accents and inks, so a pill or a tag's letters show on the deep
/// green as they do in the dark; the page's own colours stay the light's.
const tkLookOnPine = TkLook(
  lime: Color(0xFFB6F23A),
  teal: Color(0xFF2FE0C0),
  orange: Color(0xFFFF9A52),
  red: Color(0xFFFF5A74),
  blue: Color(0xFF7AA9FF),
  violet: Color(0xFFA99BFF),
  limeInk: Color(0xFFC8F169),
  tealInk: Color(0xFF2FE0C0),
  orangeInk: Color(0xFFFFB27A),
  redInk: Color(0xFFFF8FA0),
  blueInk: Color(0xFFA6C6FF),
  violetInk: Color(0xFFC9C0FF),
  glow: Color(0x00000000),
  // A card's plain buttons: a light veil on the green; its main one lime.
  ground: Color(0x29FFFFFF),
  card: Color(0xFFFFFFFF),
  line: Color(0x12000000),
  bar: Color(0xDBF8F9F7),
  on: Color(0xFFC8F169),
);

const tkLookDark = TkLook(
  lime: Color(0xFFB6F23A),
  teal: Color(0xFF2FE0C0),
  orange: Color(0xFFFF9A52),
  red: Color(0xFFFF5A74),
  blue: Color(0xFF7AA9FF),
  violet: Color(0xFFA99BFF),
  limeInk: Color(0xFFB6F23A),
  tealInk: Color(0xFF2FE0C0),
  orangeInk: Color(0xFFFF9A52),
  redInk: Color(0xFFFF7A8E),
  blueInk: Color(0xFF8DB6FF),
  violetInk: Color(0xFFBDB2FF),
  glow: Color(0x14B6F23A),
  ground: Color(0xFF000000),
  card: Color(0xFF1A1B1A),
  line: Color(0x14FFFFFF),
  bar: Color(0xDB121212),
  on: Color(0xFFC8F169),
);

/// A person's or a business's letters on one of these, chosen by the name,
/// so the same name always has the same colour.
const tkFaces = <List<Color>>[
  [Color(0xFFFFB199), Color(0xFFE8634A)],
  [Color(0xFF9BE7C4), Color(0xFF2EA37A)],
  [Color(0xFFB9B4FF), Color(0xFF6B5BD6)],
  [Color(0xFFFFD98A), Color(0xFFE3A21A)],
  [Color(0xFFA8C8FF), Color(0xFF4C7FE0)],
];

List<Color> tkFaceFor(String name) => tkFaces[name.codeUnits.fold<int>(0, (a, c) => (a * 31 + c) & 0x7fffffff) % tkFaces.length];
