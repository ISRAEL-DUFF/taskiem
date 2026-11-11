// The web's icons (web/src/icons.tsx), drawn from the same paths
// (theme/icons.g.dart): a 24-unit grid, a 1.7 stroke, round caps and joins,
// in the colour around them.

import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../theme/icons.g.dart';

class TkIcon extends StatelessWidget {
  const TkIcon(this.name, {super.key, this.size = 18, this.color, this.stroke = 1.7});

  final String name;
  final double size;
  final Color? color;

  /// The stroke on the 24-unit grid (the web sets 2 for ticks).
  final double stroke;

  @override
  Widget build(BuildContext context) {
    final d = tkIconPaths[name];
    if (d == null) return SizedBox(width: size, height: size);
    final c = color ?? DefaultTextStyle.of(context).style.color ?? const Color(0xFF000000);
    return SvgPicture.string(
      '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="none" stroke="#000" stroke-width="$stroke" '
      'stroke-linecap="round" stroke-linejoin="round"><path d="$d"/></svg>',
      width: size,
      height: size,
      colorFilter: ColorFilter.mode(c, BlendMode.srcIn),
    );
  }
}

/// Taskiem's mark in the chat: filled sparks, in the colour around them.
class TkTaskiemMark extends StatelessWidget {
  const TkTaskiemMark({super.key, this.size = 20, required this.color});
  final double size;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final paths = tkTaskiemMarkPaths.map((d) => '<path d="$d"/>').join();
    return SvgPicture.string(
      '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="#000">$paths</svg>',
      width: size,
      height: size,
      colorFilter: ColorFilter.mode(color, BlendMode.srcIn),
    );
  }
}
