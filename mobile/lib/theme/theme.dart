// The app's theme: the web chat's tokens (tokens.g.dart, generated from the
// web's CSS) and its type, so every screen is drawn as the web draws it
// (docs/spec/mobile-app.md §4.1).

import 'package:flutter/widgets.dart';

import 'tokens.g.dart';

export 'tokens.g.dart';

/// Light or dark: light is the default, as on the web; dark is the person's
/// choice (or follows the phone, when they choose that).
enum TkMode { light, dark }

/// The theme a screen is drawn with.
class TkTheme extends InheritedWidget {
  const TkTheme({super.key, required this.mode, required super.child});

  final TkMode mode;

  TkTokens get t => mode == TkMode.dark ? tkDark : tkLight;

  static TkTheme of(BuildContext context) {
    final theme = context.dependOnInheritedWidgetOfExactType<TkTheme>();
    assert(theme != null, 'No TkTheme above this widget');
    return theme!;
  }

  /// The tokens of the theme in force.
  static TkTokens tokens(BuildContext context) => of(context).t;

  @override
  bool updateShouldNotify(TkTheme old) => old.mode != mode;
}

/// The families after Inter's latin file, one per alphabet: the browser picks
/// among the web's files by unicode-range; Flutter falls back letter by letter.
const tkFallback = <String>[
  'InterLatinExt',
  'InterVietnamese',
  'InterGreek',
  'InterGreekExt',
  'InterCyrillic',
  'InterCyrillicExt',
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

FontWeight _weight(double w) {
  final i = ((w / 100).round().clamp(1, 9)) - 1;
  return FontWeight.values[i];
}
