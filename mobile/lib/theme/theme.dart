// The app's theme: the web chat's tokens (tokens.g.dart, generated from the
// web's CSS) and its type, so every screen is drawn as the web draws it
// (docs/spec/mobile-app.md §4.1).

import 'package:flutter/widgets.dart';

import 'look.dart' show TkLook;
import 'tokens.g.dart';

export 'tokens.g.dart';

/// Light or dark: light is the default, as on the web; dark is the person's
/// choice (or follows the phone, when they choose that).
enum TkMode { light, dark }

/// The theme a screen is drawn with.
class TkTheme extends InheritedWidget {
  const TkTheme({super.key, required this.mode, required super.child, this.own, this.look});

  final TkMode mode;

  /// Tokens of its own, for a part of a screen (the words on your own
  /// bubble); else the app's.
  final TkTokens? own;

  TkTokens get t => own ?? (mode == TkMode.dark ? tkAppDark : tkAppLight);

  /// The app look's accents of its own, for a part of a screen on a dark
  /// ground in the light (your own pine bubble); else the mode's.
  final TkLook? look;

  /// [child] with [change] made to the tokens in force (a part of a screen
  /// with a few colours of its own).
  static Widget recolour(BuildContext context, TkTokens Function(TkTokens t) change, Widget child, {TkLook? look}) {
    final theme = of(context);
    return TkTheme(mode: theme.mode, own: change(theme.t), look: look ?? theme.look, child: child);
  }

  static TkTheme of(BuildContext context) {
    final theme = context.dependOnInheritedWidgetOfExactType<TkTheme>();
    assert(theme != null, 'No TkTheme above this widget');
    return theme!;
  }

  /// The tokens of the theme in force.
  static TkTokens tokens(BuildContext context) => of(context).t;

  @override
  bool updateShouldNotify(TkTheme old) => old.mode != mode || old.own != own || old.look != look;
}

/// The chat in the app's own look (docs/spec/mobile-app-look.md, "The
/// chat"): no wallpaper, the page's ground under it; their bubbles white
/// (#1A1B1A in the dark), yours pine (a deep green in the dark); read ticks
/// lime. The rest of the tokens are the web's.
final tkAppLight = tkLight.copyWith(
  chatWallpaper: const Color(0xFFF2F3F0),
  chatIn: const Color(0xFFFFFFFF),
  chatOut: const Color(0xFF0D3B2E),
  chatOutMeta: const Color(0xB3FFFFFF),
  chatTickRead: const Color(0xFFC8F169),
  chatShadow: const Color(0x0D000000),
  chatDoodle: const Color(0x00000000),
);

final tkAppDark = tkDark.copyWith(
  chatWallpaper: const Color(0xFF000000),
  chatIn: const Color(0xFF1A1B1A),
  chatOut: const Color(0xFF1D3A2D),
  chatOutMeta: const Color(0xA6F2F3F0),
  chatTickRead: const Color(0xFFC8F169),
  chatShadow: const Color(0x00000000),
  chatDoodle: const Color(0x00000000),
);

/// The families after Inter's latin file, one per alphabet: the browser picks
/// among the web's files by unicode-range; Flutter falls back letter by letter.
const tkFallback = <String>[
  'InterLatinExt',
  'InterVietnamese',
  'InterGreek',
  'InterGreekExt',
  'InterCyrillic',
  'InterCyrillicExt',
  // Emoji: the phone's own (this name is the tests', where Chromium's Noto
  // Color Emoji is loaded to draw what the web's pictures show).
  'NotoColorEmoji',
];

/// Text as the web sets it: Inter at a size, a weight, a line height as a
/// multiple, and the web's letter features (`font-feature-settings: "cv11",
/// "ss01"` on the body). The weight is set on the variable font's axis too,
/// so every platform draws the weight the web does.
TextStyle tkText({
  required double size,
  double weight = 400,
  double lineHeight = 1.55,
  double letterSpacing = 0,
  Color? color,
  TextDecoration? decoration,
}) {
  return TextStyle(
    fontFamily: 'Inter',
    fontFamilyFallback: tkFallback,
    fontSize: size,
    fontWeight: _weight(weight),
    fontVariations: <FontVariation>[FontVariation('wght', weight)],
    fontFeatures: const <FontFeature>[FontFeature('cv11'), FontFeature('ss01')],
    height: lineHeight,
    leadingDistribution: TextLeadingDistribution.even,
    letterSpacing: letterSpacing,
    color: color,
    decoration: decoration ?? TextDecoration.none,
    decorationColor: color,
  );
}

/// Headings and figures on the pages away from the chat: Inter Tight, a
/// narrower cut of Inter (docs/spec/mobile-app-look.md, Direction B), set
/// tight; letters it lacks fall back to Inter.
TextStyle tkDisplay({required double size, double weight = 700, double lineHeight = 1.12, double? letterSpacing, Color? color}) => TextStyle(
  fontFamily: 'InterTight',
  fontFamilyFallback: const ['Inter', ...tkFallback],
  fontSize: size,
  fontWeight: _weight(weight),
  height: lineHeight,
  leadingDistribution: TextLeadingDistribution.even,
  letterSpacing: letterSpacing ?? -size * 0.035,
  color: color,
  decoration: TextDecoration.none,
);

FontWeight _weight(double w) {
  final i = ((w / 100).round().clamp(1, 9)) - 1;
  return FontWeight.values[i];
}

/// CSS `color-mix(in srgb, a p%, b)`, as the browser works it out (and as
/// tools/mobilegen does for the tokens): for the web's colours mixed in a
/// rule rather than kept as a token.
Color cssMix(Color a, Color b, double p) {
  final alpha = a.a * p + b.a * (1 - p);
  if (alpha == 0) return const Color(0x00000000);
  double ch(double x, double y) => (x * a.a * p + y * b.a * (1 - p)) / alpha;
  return Color.from(alpha: alpha, red: ch(a.r, b.r), green: ch(a.g, b.g), blue: ch(a.b, b.b));
}

/// A shadow as CSS draws one on a transparent thing: only outside it.
/// Flutter's BoxShadow also fills underneath, which shows through a box
/// with no colour of its own (a chip, a link-like button).
List<BoxShadow> outsideOnly(List<BoxShadow> shadows) =>
    [for (final s in shadows) BoxShadow(color: s.color, offset: s.offset, blurRadius: s.blurRadius, spreadRadius: s.spreadRadius, blurStyle: BlurStyle.outer)];
