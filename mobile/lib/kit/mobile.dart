// The app's own look for the pages away from the chat: a phone's, not the
// web's. A head with a large title and icon buttons (no paragraph under it),
// a pine summary whose figure counts up, chips that scroll sideways, sections
// with "See all ›", compact rows (one line each, the amount and its status
// at the end), a grid of tiles for the pages behind it; and the motion: a
// screen slides in from the right, a page's parts rise in one after another,
// a figure counts up. All of it stands still when the phone asks for less
// motion.

import 'dart:math' as math;

import 'package:flutter/material.dart' show RefreshIndicator;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../chat/thread.dart' show ChatHeadBar;
import '../chat/words.dart' show CardTone, naira, toneOf;
import 'motion.dart';
import '../theme/theme.dart';
import 'icon.dart';
import 'kit.dart';
import 'list.dart' show TkLoading, tkTone;
import 'tap_area.dart';

bool _still(BuildContext context) => MediaQuery.maybeDisableAnimationsOf(context) ?? false;

// --- motion ---

/// A screen pushed over another: it slides in from the right as the one
/// under it drifts left, as a phone's screens do; back, the other way.
class TkRoute<T> extends PageRouteBuilder<T> {
  TkRoute({required super.pageBuilder, super.settings, Duration? transitionDuration, Duration? reverseTransitionDuration})
    : super(
        transitionDuration: transitionDuration ?? const Duration(milliseconds: 320),
        reverseTransitionDuration: reverseTransitionDuration ?? const Duration(milliseconds: 260),
        transitionsBuilder: tkSlide,
      );

  /// Only a screen over it moves it aside, not a modal or a sheet.
  @override
  bool canTransitionTo(TransitionRoute<dynamic> nextRoute) => nextRoute is TkRoute;
}

/// [TkRoute]'s motion.
Widget tkSlide(BuildContext context, Animation<double> a, Animation<double> under, Widget child) {
  if (_still(context)) return child;
  final inn = CurvedAnimation(parent: a, curve: Curves.easeOutCubic, reverseCurve: Curves.easeInCubic);
  final out = CurvedAnimation(parent: under, curve: Curves.easeOutCubic, reverseCurve: Curves.easeInCubic);
  return SlideTransition(
    position: Tween(begin: const Offset(1, 0), end: Offset.zero).animate(inn),
    child: SlideTransition(
      position: Tween(begin: Offset.zero, end: const Offset(-0.28, 0)).animate(out),
      child: DecoratedBox(
        decoration: const BoxDecoration(boxShadow: [BoxShadow(color: Color(0x22000000), blurRadius: 16)]),
        child: child,
      ),
    ),
  );
}

/// Pushes [page] with [TkRoute].
Future<T?> tkPush<T>(BuildContext context, Widget page) => Navigator.of(context).push<T>(TkRoute<T>(pageBuilder: (_, _, _) => page));

/// A part of a page rising into place (styles.css `.stats .stat,
/// .home-col > .panel, .list-row { animation: rise-in 0.36s }`): faded in
/// and lifted 4 px, the [index]th 30 ms after the one before it.
class TkEnter extends StatelessWidget {
  const TkEnter({super.key, required this.child, this.index = 0});
  final Widget child;
  final int index;

  @override
  Widget build(BuildContext context) => TkIn.rise(index: index, child: child);
}

/// An amount in naira counting up to [kobo] (from where it was, when it
/// changes). A screen reader hears the amount itself.
class TkCountUp extends StatefulWidget {
  const TkCountUp(this.kobo, {super.key, required this.style, this.format = naira, this.textAlign});
  final int kobo;
  final TextStyle style;
  final String Function(int) format;
  final TextAlign? textAlign;

  @override
  State<TkCountUp> createState() => _TkCountUpState();
}

class _TkCountUpState extends State<TkCountUp> with SingleTickerProviderStateMixin {
  late final _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 650));
  late final _curve = CurvedAnimation(parent: _a, curve: Curves.easeOutCubic);
  int _from = 0;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_still(context)) {
      _a.value = 1;
    } else if (_a.value == 0 && !_a.isAnimating) {
      _a.forward();
    }
  }

  @override
  void didUpdateWidget(TkCountUp old) {
    super.didUpdateWidget(old);
    if (old.kobo == widget.kobo) return;
    _from = _at(old.kobo);
    if (_still(context)) {
      _a.value = 1;
    } else {
      _a.forward(from: 0);
    }
  }

  int _at(int to) => (_from + (to - _from) * _curve.value).round();

  @override
  void dispose() {
    _curve.dispose();
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Semantics(
    label: widget.format(widget.kobo),
    excludeSemantics: true,
    child: AnimatedBuilder(
      animation: _curve,
      builder: (context, _) {
        // Whole naira while counting; the amount itself at the end.
        final v = _a.isCompleted ? widget.kobo : (_at(widget.kobo) ~/ 100) * 100;
        return Text(widget.format(v), maxLines: 1, overflow: TextOverflow.ellipsis, textAlign: widget.textAlign, style: widget.style);
      },
    ),
  );
}

// --- the screen ---

/// A screen of its own: the head (Back or none, the title large on a tab,
/// its icon buttons), what stays under the head (chips), and its parts,
/// rising in one after another. Pulled down, read again.
class TkScreen extends StatelessWidget {
  const TkScreen({
    super.key,
    required this.title,
    required this.children,
    this.back = true,
    this.backLabel = 'Back',
    this.actions = const [],
    this.under,
    this.onRefresh,
  });
  final String title;
  final bool back;
  final String backLabel;

  /// The head's icon buttons ([TkHeadButton]).
  final List<Widget> actions;

  /// Stays under the head as the page scrolls (the period's chips).
  final Widget? under;
  final List<Widget> children;
  final Future<void> Function()? onRefresh;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    Widget list = SingleChildScrollView(
      physics: onRefresh == null ? const BouncingScrollPhysics() : const AlwaysScrollableScrollPhysics(),
      padding: EdgeInsets.fromLTRB(16, 8, 16, 28 + pad.bottom),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 22), TkEnter(index: i, child: children[i])],
        ],
      ),
    );
    if (onRefresh != null) list = RefreshIndicator(onRefresh: onRefresh!, color: t.brand, child: list);
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: TkAppLook(
        child: ColoredBox(
        color: t.bg,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: EdgeInsets.fromLTRB(back ? 6 : 20, 8 + pad.top, 10, back ? 6 : 4),
              child: Row(
                children: [
                  if (back) ...[
                    ChatHeadBar.iconButton(backLabel, TkIcon('back', size: 22, color: t.text), () => Navigator.of(context).maybePop()),
                    const SizedBox(width: 4),
                  ],
                  Expanded(
                    child: Semantics(
                      header: true,
                      child: Text(
                        title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: tkText(size: back ? 19 : 28, weight: 700, letterSpacing: back ? -0.2 : -0.6, color: t.text),
                      ),
                    ),
                  ),
                  ...actions,
                ],
              ),
            ),
            ?under,
            Expanded(child: list),
          ],
        ),
      ),
      )
    );
  }
}

/// An icon button in a screen's head.
class TkHeadButton extends StatelessWidget {
  const TkHeadButton({super.key, required this.icon, required this.label, required this.onTap});
  final String icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ChatHeadBar.iconButton(
      label,
      Container(
        width: 38,
        height: 38,
        decoration: BoxDecoration(color: t.tkSoft, shape: BoxShape.circle),
        alignment: Alignment.center,
        child: TkIcon(icon, size: 19, color: t.text),
      ),
      onTap,
    );
  }
}

/// Chips across, scrolling sideways: one on (a period, a filter).
class TkChips<T> extends StatelessWidget {
  const TkChips({super.key, required this.chips, required this.on, required this.onPick, this.label = 'Show'});
  final List<(T, String)> chips;
  final T on;
  final ValueChanged<T> onPick;
  final String label;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Semantics(
      container: true,
      label: label,
      explicitChildNodes: true,
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        physics: const BouncingScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 10),
        child: Row(
          children: [
            for (final (v, words) in chips) ...[
              if (v != chips.first.$1) const SizedBox(width: 8),
              TapArea(
                child: Semantics(
                  button: true,
                  selected: v == on,
                  label: words,
                  excludeSemantics: true,
                  onTap: () => onPick(v),
                  child: TkPress(
                    onTap: () => onPick(v),
                    scale: 0.95,
                    child: AnimatedContainer(
                      duration: _still(context) ? Duration.zero : const Duration(milliseconds: 200),
                      curve: Curves.easeOut,
                      constraints: const BoxConstraints(minHeight: 38),
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      alignment: Alignment.center,
                      decoration: BoxDecoration(color: v == on ? (dark ? t.brandDark : t.brand) : t.tkSoft, borderRadius: BorderRadius.circular(999)),
                      child: Text(words, style: tkText(size: 14, weight: 600, color: v == on ? (dark ? t.brandDarkText : const Color(0xFFFFFFFF)) : t.text)),
                    ),
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

// --- the parts ---

/// The pine summary: a label, the amount large and counting up, and the
/// figures beside it.
class TkSummary extends StatelessWidget {
  const TkSummary({super.key, required this.label, this.kobo, this.big, this.tag, this.figures = const [], this.foot, this.semanticsLabel});
  final String label;

  /// The amount, counting up; or [big], words as they are.
  final int? kobo;
  final String? big;
  final String? tag;

  /// Side by side under it: a word, an amount, its dot's colour.
  final List<({String label, String value, Color? dot})> figures;

  /// Under the figures (what is pending).
  final String? foot;
  final String? semanticsLabel;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    const white = Color(0xFFFFFFFF);
    final soft = white.withValues(alpha: 0.78);
    final bigStyle = tkText(size: 38, weight: 700, lineHeight: 1.15, letterSpacing: -1, color: white);
    return Semantics(
      container: true,
      label: semanticsLabel,
      explicitChildNodes: true,
      child: Container(
        padding: const EdgeInsets.fromLTRB(20, 18, 20, 18),
        decoration: BoxDecoration(
          color: t.brand,
          borderRadius: BorderRadius.circular(26),
          boxShadow: [BoxShadow(color: t.brand.withValues(alpha: 0.16), blurRadius: 18, offset: const Offset(0, 6))],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(label, style: tkText(size: 14, weight: 600, color: soft)),
                ),
                if (tag != null)
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
                    decoration: BoxDecoration(color: t.brandDark, borderRadius: BorderRadius.circular(999)),
                    child: Text(tag!, style: tkText(size: 12, weight: 700, color: t.brandDarkText)),
                  ),
              ],
            ),
            const SizedBox(height: 4),
            FittedBox(
              fit: BoxFit.scaleDown,
              alignment: Alignment.centerLeft,
              child: kobo != null ? TkCountUp(kobo!, style: bigStyle) : Text(big ?? '', style: bigStyle),
            ),
            if (figures.isNotEmpty) ...[
              const SizedBox(height: 14),
              Wrap(
                spacing: 10,
                runSpacing: 10,
                children: [
                  for (final f in figures)
                    MergeSemantics(
                      child: Container(
                        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                        decoration: BoxDecoration(color: white.withValues(alpha: 0.1), borderRadius: BorderRadius.circular(14)),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                if (f.dot != null) ...[
                                  Container(
                                    width: 8,
                                    height: 8,
                                    decoration: BoxDecoration(color: f.dot, shape: BoxShape.circle),
                                  ),
                                  const SizedBox(width: 6),
                                ],
                                Text(f.label, style: tkText(size: 12.5, weight: 600, color: soft)),
                              ],
                            ),
                            Text(f.value, style: tkText(size: 16, weight: 700, color: white)),
                          ],
                        ),
                      ),
                    ),
                ],
              ),
            ],
            if (foot != null) ...[const SizedBox(height: 10), Text(foot!, style: tkText(size: 13, color: soft))],
          ],
        ),
      ),
    );
  }
}

/// A section: its title (and a count), "See all ›" at its end, and its
/// rows on a rounded panel, a hairline between them.
class TkSection extends StatelessWidget {
  const TkSection({super.key, this.title, this.count, this.onSeeAll, this.seeAllLabel, this.children = const [], this.empty, this.trailing});
  final String? title;

  /// Beside the title, amber (how many wait).
  final int? count;
  final VoidCallback? onSeeAll;

  /// What a screen reader hears for "See all".
  final String? seeAllLabel;
  final List<Widget> children;

  /// Said on the panel when there are no rows.
  final String? empty;

  /// At the head's end, in "See all"'s place.
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final rows = children.isEmpty && empty != null
        ? [
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 16),
              child: Text(empty!, style: tkText(size: 14, color: t.muted)),
            ),
          ]
        : children;
    return Semantics(
      container: true,
      explicitChildNodes: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (title != null)
            Padding(
              padding: const EdgeInsets.only(left: 4, bottom: 6),
              child: Row(
                children: [
                  Expanded(
                    child: Row(
                      children: [
                        Flexible(
                          child: Semantics(
                            header: true,
                            child: Text(title!, style: tkText(size: 17, weight: 700, letterSpacing: -0.2, color: t.text)),
                          ),
                        ),
                        if (count != null && count! > 0) ...[
                          const SizedBox(width: 8),
                          Semantics(
                            label: '$count waiting',
                            excludeSemantics: true,
                            child: Container(
                              constraints: const BoxConstraints(minWidth: 22),
                              padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
                              decoration: BoxDecoration(color: t.tkWarnBg, borderRadius: BorderRadius.circular(999)),
                              alignment: Alignment.center,
                              child: Text('$count', style: tkText(size: 12, weight: 700, color: t.tkWarnInk)),
                            ),
                          ),
                        ],
                      ],
                    ),
                  ),
                  ?trailing,
                  if (onSeeAll != null && trailing == null)
                    TapArea(
                      child: Semantics(
                        button: true,
                        label: seeAllLabel ?? 'See all',
                        excludeSemantics: true,
                        onTap: onSeeAll,
                        child: TkPress(
                          onTap: onSeeAll,
                          child: Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 8),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Text('See all', style: tkText(size: 14, weight: 600, color: _ink(context))),
                                TkIcon('chevron', size: 16, color: _ink(context)),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                ],
              ),
            ),
          if (rows.isNotEmpty) TkPanel(children: rows),
        ],
      ),
    );
  }
}

Color _ink(BuildContext context) {
  final t = TkTheme.tokens(context);
  return TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand;
}

/// A rounded panel, a hairline between what it holds.
class TkPanel extends StatelessWidget {
  const TkPanel({super.key, required this.children, this.padding = EdgeInsets.zero});
  final List<Widget> children;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Container(
      padding: padding,
      clipBehavior: Clip.antiAlias,
      decoration: BoxDecoration(
        color: t.panel,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: t.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < children.length; i++) ...[if (i > 0) Container(height: 1, margin: const EdgeInsets.only(left: 70), color: t.border), children[i]],
        ],
      ),
    );
  }
}

/// A compact row: its round tile (letters or an icon), the title and one
/// line under it, each on one line; the amount and the status at its end.
class TkListRow extends StatelessWidget {
  const TkListRow({
    super.key,
    required this.title,
    this.sub = '',
    this.letters,
    this.icon,
    this.tone,
    this.amount,
    this.status,
    this.statusTone = CardTone.plain,
    this.waiting = false,
    this.onTap,
    this.semanticsLabel,
  });
  final String title;
  final String sub;
  final String? letters;
  final String? icon;

  /// The tile's tone; else the letters' own, or lime for an icon.
  final CardTone? tone;
  final String? amount;
  final String? status;
  final CardTone statusTone;

  /// Waits for the business: an amber dot on the tile.
  final bool waiting;
  final VoidCallback? onTap;
  final String? semanticsLabel;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = switch (tone) {
      CardTone.warn => (t.tintWarn, t.tintWarnText),
      CardTone.danger => (t.tkDangerBg, t.tkDangerInk),
      _ => icon != null ? (t.tkLimeSoft, dark ? t.brandDark : t.brand) : tkTone(t, toneOf(letters ?? title)),
    };
    final initials = tkInitials(letters ?? title);
    final (pillBg, pillInk) = switch (statusTone) {
      CardTone.lime => (t.brandDark, t.brandDarkText),
      CardTone.warn => (t.tkWarnBg, t.tkWarnInk),
      CardTone.danger => (t.tkDangerBg, t.tkDangerInk),
      CardTone.plain => (t.tkSoft, t.muted),
    };
    return MergeSemantics(
      child: Semantics(
        button: onTap != null,
        label: semanticsLabel,
        child: TkPress(
          onTap: onTap,
          scale: 0.985,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
            child: Row(
              children: [
                ExcludeSemantics(
                  child: SizedBox(
                    width: 44,
                    height: 44,
                    child: Stack(
                      clipBehavior: Clip.none,
                      children: [
                        Container(
                          width: 44,
                          height: 44,
                          decoration: BoxDecoration(color: bg, shape: BoxShape.circle),
                          alignment: Alignment.center,
                          child: icon != null
                              ? TkIcon(icon!, size: 21, color: ink)
                              : Text(
                                  initials,
                                  maxLines: 1,
                                  textScaler: TextScaler.noScaling,
                                  style: tkText(size: 15, weight: 800, color: ink),
                                ),
                        ),
                        if (waiting)
                          Positioned(
                            right: -1,
                            top: -1,
                            child: Container(
                              width: 13,
                              height: 13,
                              decoration: BoxDecoration(
                                color: t.warn,
                                shape: BoxShape.circle,
                                border: Border.all(color: t.panel, width: 2),
                              ),
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: tkText(size: 15.5, weight: 600, color: t.text),
                      ),
                      if (sub.isNotEmpty) ...[
                        const SizedBox(height: 1),
                        Text(
                          sub,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: tkText(size: 13.5, color: t.muted),
                        ),
                      ],
                    ],
                  ),
                ),
                if (amount != null || status != null) ...[
                  const SizedBox(width: 10),
                  ConstrainedBox(
                    constraints: BoxConstraints(maxWidth: MediaQuery.sizeOf(context).width * 0.38),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        if (amount != null)
                          Text(
                            amount!,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: tkText(size: 15, weight: 700, color: t.text),
                          ),
                        if (status != null) ...[
                          if (amount != null) const SizedBox(height: 3),
                          Container(
                            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                            decoration: BoxDecoration(color: pillBg, borderRadius: BorderRadius.circular(999)),
                            child: Text(
                              status!,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: tkText(size: 11.5, weight: 700, color: pillInk),
                            ),
                          ),
                        ],
                      ],
                    ),
                  ),
                ],
                if (onTap != null && amount == null && status == null) ...[
                  const SizedBox(width: 6),
                  ExcludeSemantics(child: TkIcon('chevron', size: 18, color: t.muted)),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

String tkInitials(String name) {
  final words = name.split(RegExp(r'[\s·]+')).where((w) => w.isNotEmpty && RegExp(r'[A-Za-z0-9]').hasMatch(w[0])).toList();
  if (words.isEmpty) return '?';
  if (words.length == 1) return words.first.substring(0, math.min(2, words.first.length)).toUpperCase();
  return (words[0][0] + words[1][0]).toUpperCase();
}

/// A tile in a [TkTiles] grid: an icon, its word, a count when something
/// waits there.
typedef TkTile = ({String icon, String label, int badge, VoidCallback onTap, String? semantics});

/// Tiles, three across, for the pages behind a screen.
class TkTiles extends StatelessWidget {
  const TkTiles({super.key, required this.tiles, this.across = 3});
  final List<TkTile> tiles;
  final int across;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    Widget tile(TkTile x) => TapArea(
      child: Semantics(
        button: true,
        label: x.semantics ?? (x.badge > 0 ? '${x.label}, ${x.badge} waiting' : x.label),
        excludeSemantics: true,
        onTap: x.onTap,
        child: TkPress(
          onTap: x.onTap,
          scale: 0.95,
          child: Container(
            padding: const EdgeInsets.fromLTRB(8, 14, 8, 12),
            decoration: BoxDecoration(
              color: t.panel,
              borderRadius: BorderRadius.circular(20),
              border: Border.all(color: t.border),
            ),
            child: Column(
              children: [
                Stack(
                  clipBehavior: Clip.none,
                  children: [
                    Container(
                      width: 42,
                      height: 42,
                      decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
                      alignment: Alignment.center,
                      child: TkIcon(x.icon, size: 21, color: dark ? t.brandDark : t.brand),
                    ),
                    if (x.badge > 0)
                      Positioned(
                        right: -8,
                        top: -6,
                        child: Container(
                          constraints: const BoxConstraints(minWidth: 20),
                          padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                          decoration: BoxDecoration(
                            color: t.warn,
                            borderRadius: BorderRadius.circular(999),
                            border: Border.all(color: t.panel, width: 2),
                          ),
                          alignment: Alignment.center,
                          child: Text(
                            '${x.badge}',
                            textScaler: TextScaler.noScaling,
                            style: tkText(size: 11, weight: 800, color: const Color(0xFFFFFFFF)),
                          ),
                        ),
                      ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(
                  x.label,
                  textAlign: TextAlign.center,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: tkText(size: 13, weight: 600, lineHeight: 1.2, color: t.text),
                ),
              ],
            ),
          ),
        ),
      ),
    );
    final rows = <List<TkTile>>[for (var i = 0; i < tiles.length; i += across) tiles.sublist(i, math.min(i + across, tiles.length))];
    return Column(
      children: [
        for (var r = 0; r < rows.length; r++) ...[
          if (r > 0) const SizedBox(height: 10),
          IntrinsicHeight(
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (var i = 0; i < across; i++) ...[
                  if (i > 0) const SizedBox(width: 10),
                  Expanded(child: i < rows[r].length ? tile(rows[r][i]) : const SizedBox()),
                ],
              ],
            ),
          ),
        ],
      ],
    );
  }
}

/// The kit's shimmering rows on a panel, while a section loads.
class TkSkeleton extends StatelessWidget {
  const TkSkeleton({super.key, this.rows = 3, this.label = 'Loading…'});
  final int rows;
  final String label;

  @override
  Widget build(BuildContext context) => TkPanel(
    padding: const EdgeInsets.symmetric(horizontal: 6),
    children: [TkLoading(rows: rows, label: label)],
  );
}
