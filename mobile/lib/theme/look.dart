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
    required this.glow,
  });

  final Color lime, teal, orange, red, blue, violet;
  final Color limeInk, tealInk, orangeInk, redInk, blueInk;

  /// The top card's light, at its top left (dark only; none in light).
  final Color glow;

  /// The look in force.
  static TkLook of(BuildContext context) => TkTheme.of(context).mode == TkMode.dark ? tkLookDark : tkLookLight;
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
  glow: Color(0x00000000),
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
  glow: Color(0x14B6F23A),
);
