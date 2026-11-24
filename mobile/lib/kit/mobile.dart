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
import '../chat/words.dart' show CardTone, naira;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'icon.dart';
import 'kit.dart';
import 'look.dart' show tkTabular;
import 'list.dart' show TkLoading;
import 'sheet.dart' show TkCta, TkGhostButton, TkSheet;
import 'tap_area.dart';
import 'forms.dart' show TkSwitch;

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

/// A part of a page. It used to rise in (styles.css rise-in), but the page
/// already slides in from the right as a phone's do, and the parts rising
/// after it read as a flicker on a real phone (14 November 2026): a page's
/// parts are simply there when it arrives.
class TkEnter extends StatelessWidget {
  const TkEnter({super.key, required this.child, this.index = 0});
  final Widget child;
  final int index;

  @override
  Widget build(BuildContext context) => child;
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
    this.head,
    this.compact = false,
  });
  final String title;
  final bool back;

  /// The head's own words in the title's place (Today's greeting); the
  /// title is still what a screen reader hears as the screen's name.
  final Widget? head;

  /// A tab whose first part is its figure (Money): the title small.
  final bool compact;
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
          for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 14), TkEnter(index: i, child: children[i])],
        ],
      ),
    );
    if (onRefresh != null) list = RefreshIndicator(onRefresh: onRefresh!, color: t.brand, child: list);
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: TkAppLook(
        child: ColoredBox(
        color: TkLook.of(context).ground,
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
                    child: head != null
                        ? Semantics(header: true, label: title, child: head)
                        : Semantics(
                            header: true,
                            child: Text(
                              title,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: tkDisplay(
                                size: back || compact ? 20 : 30,
                                letterSpacing: back || compact ? -0.4 : -1.1,
                                color: t.text,
                              ),
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
  const TkHeadButton({super.key, required this.icon, required this.label, required this.onTap, this.primary = false, this.onCard = false});

  /// On a card rather than the page: the ground's colour, so it shows.
  final bool onCard;
  final String icon;
  final String label;
  final VoidCallback onTap;

  /// The page's main button (New): pine with a lime icon, lime with pine
  /// in the dark.
  final bool primary;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return ChatHeadBar.iconButton(
      label,
      Container(
        width: 38,
        height: 38,
        decoration: BoxDecoration(color: primary ? TkLook.of(context).on : (onCard ? TkLook.of(context).ground : TkLook.of(context).card), shape: BoxShape.circle),
        alignment: Alignment.center,
        child: TkIcon(icon, size: 19, stroke: primary ? 2.4 : 2, color: primary ? (dark ? t.brandDarkText : t.brandDark) : _ink(context)),
      ),
      onTap,
    );
  }
}

/// Chips across, scrolling sideways: one on (a period, a filter).
class TkChips<T> extends StatelessWidget {
  const TkChips({super.key, required this.chips, required this.on, required this.onPick, this.label = 'Show', this.padding = const EdgeInsets.fromLTRB(16, 4, 16, 10), this.off = const {}});

  /// Choices that cannot be picked now, with why (a line a customer
  /// stopped): drawn quiet, said as not enabled with the reason as a hint.
  final Map<T, String> off;

  /// Round the switch: the page's edge; none inside a card.
  final EdgeInsets padding;
  final List<(T, String)> chips;
  final T on;
  final ValueChanged<T> onPick;
  final String label;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    // As a phone's segmented switch: a soft track, the one chosen on a card
    // of its own, each as wide as its words from the left (`.seg.sc`, as
    // drawn); wider than the screen, it scrolls sideways.
    return Semantics(
      container: true,
      label: label,
      explicitChildNodes: true,
      child: Padding(
        padding: padding,
        child: LayoutBuilder(
          builder: (context, box) => Container(
            padding: const EdgeInsets.all(2),
            decoration: BoxDecoration(
              color: dark ? const Color(0x1AFFFFFF) : const Color(0x0F000000),
              borderRadius: BorderRadius.circular(12),
            ),
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              physics: const BouncingScrollPhysics(),
              child: ConstrainedBox(
                constraints: BoxConstraints(minWidth: box.maxWidth - 4),
                child: Row(
                  children: [
                    for (final (v, words) in chips)
                      TapArea(
                        child: Semantics(
                          button: true,
                          selected: v == on,
                          enabled: !off.containsKey(v),
                          label: words,
                          hint: off[v],
                          excludeSemantics: true,
                          onTap: off.containsKey(v) ? null : () => onPick(v),
                          child: TkPress(
                            onTap: off.containsKey(v) ? null : () => onPick(v),
                            scale: 0.95,
                            child: AnimatedContainer(
                              duration: _still(context) ? Duration.zero : const Duration(milliseconds: 200),
                              curve: Curves.easeOut,
                              constraints: const BoxConstraints(minHeight: 34),
                              padding: const EdgeInsets.symmetric(horizontal: 12),
                              alignment: Alignment.center,
                              decoration: BoxDecoration(
                                color: v == on ? (dark ? const Color(0xFF3A3B3A) : look.card) : const Color(0x00000000),
                                borderRadius: BorderRadius.circular(10),
                                boxShadow: v == on && !dark ? const [BoxShadow(color: Color(0x1F000000), blurRadius: 3, offset: Offset(0, 1))] : null,
                              ),
                              child: Text(words, style: tkText(size: 13, weight: 600, color: off.containsKey(v) ? t.muted : t.text)),
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ),
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
  const TkSection({super.key, this.title, this.count, this.onSeeAll, this.seeAllLabel, this.children = const [], this.empty, this.trailing, this.inset = 66});
  final String? title;

  /// Where the hairline between rows starts: after a 40 px face (66), or a
  /// 30 px square icon (56).
  final double inset;

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
              padding: const EdgeInsets.only(left: 4, bottom: 8),
              child: Row(
                children: [
                  Expanded(
                    child: Row(
                      children: [
                        Flexible(
                          child: Semantics(
                            header: true,
                            child: Text(title!, style: tkDisplay(size: 20, lineHeight: 1.25, letterSpacing: -0.4, color: t.text)),
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
                                Text('See all', style: tkText(size: 14, weight: 600, color: t.muted)),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                ],
              ),
            ),
          if (rows.isNotEmpty) TkPanel(inset: inset, children: rows),
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
  const TkPanel({super.key, required this.children, this.padding = EdgeInsets.zero, this.inset = 66});
  final List<Widget> children;
  final EdgeInsets padding;

  /// Where the hairline between what it holds starts.
  final double inset;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return Container(
      padding: padding,
      clipBehavior: Clip.antiAlias,
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < children.length; i++) ...[if (i > 0) Container(height: 1, margin: EdgeInsets.only(left: inset), color: look.line), children[i]],
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
    this.square,
    this.statusAccent,
    this.level,
    this.levelColor,
    this.trailing,
    this.face,
    this.chevron = true,
    this.leading,
    this.subLines = 2,
    this.titleLines,
  });

  /// A tile of its own, 40 px round (a business's photo), in place of the
  /// letters or the icon.
  final Widget? face;

  /// How many lines the title may take (one; two beside a switch).
  final int? titleLines;

  /// Whether a row that opens has its chevron: not a choice, which is
  /// ticked instead.
  final bool chevron;

  /// In place of the tile: a tick box when rows are chosen (as drawn), no
  /// chevron after.
  final Widget? leading;

  /// How many lines the sub may take.
  final int subLines;

  /// At the row's end in place of an amount: a switch, or quiet words
  /// ("Linked") before the chevron.
  final Widget? trailing;

  /// How full it is (an item's stock against its level), as a thin bar
  /// under the line; null for none.
  final double? level;
  final Color? levelColor;
  final String title;
  final String sub;
  final String? letters;
  final String? icon;

  /// The icon white on a rounded square of this colour (a list of places
  /// to go, as a phone's settings), in place of the round tile.
  final Color? square;

  /// The status in an accent of its own (an order's stage), in place of
  /// [statusTone]'s.
  final ({Color fill, Color ink})? statusAccent;

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
    final look = TkLook.of(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final small = square != null && icon != null;
    final size = small ? 30.0 : 40.0;
    // The tile: an icon white on a small coloured square (a place to go);
    // an icon on a soft round tile; or the letters on a face's gradient
    // (warm or red when the row warns).
    final Widget face;
    if (this.face != null) {
      face = SizedBox(width: size, height: size, child: this.face);
    } else if (small) {
      face = Container(
        width: size,
        height: size,
        decoration: BoxDecoration(color: square, borderRadius: BorderRadius.circular(9)),
        alignment: Alignment.center,
        child: TkIcon(icon!, size: 17, stroke: 2.2, color: const Color(0xFFFFFFFF)),
      );
    } else if (icon != null || tone == CardTone.warn || tone == CardTone.danger) {
      final (bg, ink) = switch (tone) {
        CardTone.warn => (look.orange.withValues(alpha: 0.16), look.orangeInk),
        CardTone.danger => (look.red.withValues(alpha: 0.16), look.redInk),
        _ => (t.tkLimeSoft, dark ? t.brandDark : t.brand),
      };
      face = Container(
        width: size,
        height: size,
        decoration: BoxDecoration(color: bg, shape: BoxShape.circle),
        alignment: Alignment.center,
        child: icon != null
            ? TkIcon(icon!, size: 20, stroke: 2, color: ink)
            : Text(tkInitials(letters ?? title), maxLines: 1, textScaler: TextScaler.noScaling, style: tkText(size: 13.5, weight: 700, color: ink)),
      );
    } else {
      final g = tkFaceFor(letters ?? title);
      face = Container(
        width: size,
        height: size,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          gradient: LinearGradient(begin: Alignment.topLeft, end: Alignment.bottomRight, colors: g),
        ),
        alignment: Alignment.center,
        child: Text(tkInitials(letters ?? title), maxLines: 1, textScaler: TextScaler.noScaling, style: tkText(size: 13.5, weight: 700, color: const Color(0xFFFFFFFF))),
      );
    }
    final (pillBg, pillInk) = statusAccent != null ? (statusAccent!.fill.withValues(alpha: 0.16), statusAccent!.ink) : switch (statusTone) {
      CardTone.lime => (look.lime.withValues(alpha: 0.18), look.limeInk),
      CardTone.warn => (look.orange.withValues(alpha: 0.16), look.orangeInk),
      CardTone.danger => (look.red.withValues(alpha: 0.16), look.redInk),
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
            padding: EdgeInsets.symmetric(horizontal: 14, vertical: small ? 11 : 10),
            child: Row(
              children: [
                if (leading != null)
                  leading!
                else
                ExcludeSemantics(
                  child: SizedBox(
                    width: size,
                    height: size,
                    child: Stack(
                      clipBehavior: Clip.none,
                      children: [
                        face,
                        if (waiting)
                          Positioned(
                            right: -2,
                            top: -2,
                            child: Container(
                              width: 12,
                              height: 12,
                              decoration: BoxDecoration(
                                color: look.orange,
                                shape: BoxShape.circle,
                                border: Border.all(color: look.card, width: 2),
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
                        // A setting's words wrap beside its switch, as drawn.
                        maxLines: titleLines ?? (trailing is TkSwitch ? 2 : 1),
                        overflow: TextOverflow.ellipsis,
                        style: tkText(size: 15.5, weight: 500, lineHeight: 1.3, color: t.text),
                      ),
                      if (sub.isNotEmpty)
                        // Up to two lines, as drawn.
                        Text(
                          sub,
                          maxLines: subLines,
                          overflow: TextOverflow.ellipsis,
                          style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted),
                        ),
                      if (level != null)
                        Padding(
                          padding: const EdgeInsets.only(top: 6, right: 12),
                          child: ExcludeSemantics(
                            child: ClipRRect(
                              borderRadius: BorderRadius.circular(2),
                              child: SizedBox(
                                height: 4,
                                child: Stack(
                                  fit: StackFit.expand,
                                  children: [
                                    ColoredBox(color: t.tkSoft),
                                    FractionallySizedBox(
                                      alignment: Alignment.centerLeft,
                                      widthFactor: level!.clamp(0.02, 1.0),
                                      child: ColoredBox(color: levelColor ?? look.lime),
                                    ),
                                  ],
                                ),
                              ),
                            ),
                          ),
                        ),
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
                            style: tkText(size: 15, weight: 650, color: t.text).copyWith(fontFeatures: tkTabular),
                          ),
                        if (status != null) ...[
                          if (amount != null) const SizedBox(height: 4),
                          Container(
                            padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 2),
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
                if (trailing != null) ...[const SizedBox(width: 10), trailing!],
                // A row that opens has its chevron, after its amount too (as drawn).
                if (chevron && onTap != null && status == null && leading == null && trailing is! TkSwitch) ...[
                  const SizedBox(width: 6),
                  ExcludeSemantics(child: TkIcon('chevron', size: 16, color: t.muted.withValues(alpha: 0.6))),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// A choice's tick, at the end of a [TkListRow] (`chevron: false`) chosen
/// from a few: the tick when [on], else the same room left blank so the
/// words do not move.
class TkTick extends StatelessWidget {
  const TkTick(this.on, {super.key});
  final bool on;

  @override
  Widget build(BuildContext context) =>
      ExcludeSemantics(child: on ? TkIcon('b-check', size: 20, stroke: 2.4, color: TkLook.of(context).limeInk) : const SizedBox(width: 20, height: 20));
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
            decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(20)),
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
                            border: Border.all(color: TkLook.of(context).card, width: 2),
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

/// A form over a page (docs/spec/mobile-app-look.md, "The remaining
/// screens"): the sheet's title large, what it is about over it, the form
/// on the ground, and its buttons at the foot: the main one last, wide.
class TkFormSheet extends StatelessWidget {
  const TkFormSheet({super.key, required this.title, this.crumb, required this.children, required this.action, this.onAction, this.second, this.onSecond, this.secondDanger = false, this.overlay, this.actionIcon, this.foot});
  final String title;

  /// A line over the buttons, kept in view (how many, and the total).
  final String? foot;
  final String? crumb;
  final List<Widget> children;

  /// The main button.
  final String action;
  final VoidCallback? onAction;

  /// A quieter one beside it (Cancel booking).
  final String? second;
  final VoidCallback? onSecond;
  final bool secondDanger;

  /// The saved tick, over everything.
  final Widget? overlay;

  /// The main button's icon (Send's paper plane), as drawn.
  final String? actionIcon;

  @override
  Widget build(BuildContext context) => TkAppLook(
    child: TkSheet(
      title: title,
      crumb: crumb,
      overlay: overlay,
      body: ListView(
        padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
        children: [
          for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 12), children[i]],
        ],
      ),
      // TkSheet sets it in its foot: not a second foot inside it.
      bareFooter: true,
      footer: _withFoot(
        context,
        Row(
        children: [
          // A long action ("Save JSS 1A's fees") takes more of the row, so it
          // stays on one line, as drawn.
          if (second != null) ...[Expanded(flex: 4, child: TkGhostButton(label: second!, danger: secondDanger, onTap: onSecond)), const SizedBox(width: 10)],
          Expanded(flex: action.length + (actionIcon == null ? 0 : 3) > 15 ? 6 : 4, child: TkCta(label: action, icon: actionIcon, onTap: onAction)),
        ],
      ),
      ),
    ),
  );

  Widget _withFoot(BuildContext context, Widget buttons) {
    final words = foot;
    if (words == null || words.isEmpty) return buttons;
    final t = TkTheme.tokens(context);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
          child: Semantics(liveRegion: true, child: Text(words, textAlign: TextAlign.center, style: tkText(size: 14, weight: 600, lineHeight: 1.3, color: t.text))),
        ),
        buttons,
      ],
    );
  }
}

/// A group of a form's fields (docs/spec/mobile-app-look.md, "The remaining
/// screens"): its heading on the ground, the fields on a card, their boxes
/// a shade of the ground so they show on it.
class TkFormGroup extends StatelessWidget {
  const TkFormGroup({super.key, this.title, required this.children});
  final String? title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (title != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 10),
            child: Semantics(header: true, child: Text(title!, style: tkDisplay(size: 20, letterSpacing: -0.4, color: t.text))),
          ),
        Container(
          padding: const EdgeInsets.fromLTRB(14, 12, 14, 14),
          decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
          child: TkTheme.recolour(
            context,
            (x) => x.copyWith(field: look.ground, tkSoft: look.ground),
            Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 12), children[i]],
              ],
            ),
          ),
        ),
      ],
    );
  }
}
