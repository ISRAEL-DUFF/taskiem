// The kit's parts for a list in a sheet (web/src/kit.tsx, kit.css, chat.css):
// the pill search box, the chips, a row, the pill, the empty and loading
// states, and the "Open …" line under a list. Every tool's sheet and the
// pages from the chat (Linked businesses, the profile) are made of them.

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart' show TextInputAction;
import 'package:flutter/widgets.dart';

import '../chat/words.dart' show CardTone, toneOf;
import '../theme/theme.dart';
import 'icon.dart';
import 'kit.dart';
import 'tap_area.dart';

/// kit.css `.tk-search`: a pill of the soft ground, 46 px, the magnifier and
/// the words; the ring while typing in it.
class TkSearchBox extends StatefulWidget {
  const TkSearchBox({super.key, required this.controller, required this.hint, this.margin = const EdgeInsets.fromLTRB(20, 0, 20, 12)});
  final TextEditingController controller;
  final String hint;
  final EdgeInsets margin;

  @override
  State<TkSearchBox> createState() => _TkSearchBoxState();
}

class _TkSearchBoxState extends State<TkSearchBox> {
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: widget.margin,
      child: TapField(
        child: Semantics(
          label: widget.hint,
          child: Container(
            constraints: const BoxConstraints(minHeight: 46),
            padding: const EdgeInsets.symmetric(horizontal: 14),
            decoration: BoxDecoration(
              color: t.tkSoft,
              borderRadius: BorderRadius.circular(14),
              boxShadow: _focus.hasFocus ? t.ring : const [],
            ),
            child: Row(
              children: [
                TkIcon('search', size: 18, color: t.muted),
                const SizedBox(width: 10),
                Expanded(
                  child: Material(
                    type: MaterialType.transparency,
                    child: TextField(
                      controller: widget.controller,
                      focusNode: _focus,
                      textInputAction: TextInputAction.search,
                      cursorColor: t.text,
                      showCursor: !TkField.debugHideCaret,
                      style: tkText(size: 15, color: t.text),
                      decoration: InputDecoration.collapsed(hintText: widget.hint, hintStyle: tkText(size: 15, color: t.muted)),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// kit.css `.tk-chips`: All, then each chip ("Low stock · 3"); the one on
/// in pine with white words. Sliding sideways when they are more than the
/// width.
class TkChipRow extends StatelessWidget {
  const TkChipRow({super.key, required this.chips, required this.on, required this.onPick, this.padding = const EdgeInsets.fromLTRB(20, 0, 20, 10)});

  /// The chips after All: their words and tags.
  final List<({String label, String tag})> chips;

  /// The tag on, null for All.
  final String? on;
  final ValueChanged<String?> onPick;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final items = <(String, String?)>[('All', null), for (final c in chips) (c.label, c.tag)];
    return Semantics(
      container: true,
      label: 'Show',
      explicitChildNodes: true,
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        padding: padding,
        child: Row(
          children: [
            for (var i = 0; i < items.length; i++) ...[
              if (i > 0) const SizedBox(width: 8),
              _Chip(label: items[i].$1, on: on == items[i].$2, onTap: () => onPick(items[i].$2 == on ? null : items[i].$2)),
            ],
          ],
        ),
      ),
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({required this.label, required this.on, required this.onTap});
  final String label;
  final bool on;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    // Pine with white words; lime with its own ink in the dark theme.
    final (bg, ink) = on ? (dark ? (t.brandDark, t.brandDarkText) : (t.brand, const Color(0xFFFFFFFF))) : (t.tkSoft, t.text);
    return TapArea(
      child: Semantics(
        button: true,
        toggled: on,
        label: label,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            constraints: const BoxConstraints(minHeight: 36),
            padding: const EdgeInsets.symmetric(horizontal: 14),
            alignment: Alignment.center,
            decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
            child: Text(label, maxLines: 1, style: tkText(size: 14, weight: 600, color: ink)),
          ),
        ),
      ),
    );
  }
}

/// kit.css `.tk-pill`: a state in a word, on its tone's ground.
class TkPill extends StatelessWidget {
  const TkPill(this.text, {super.key, this.tone = CardTone.plain, this.small = false});
  final String text;
  final CardTone tone;

  /// `.tk-tile-badge .tk-pill { font-size: 11px; padding: 2px 7px }`.
  final bool small;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final (bg, ink) = switch (tone) {
      CardTone.lime => (t.brandDark, t.brandDarkText),
      CardTone.warn => (t.tkWarnBg, t.tkWarnInk),
      CardTone.danger => (t.tkDangerBg, t.tkDangerInk),
      CardTone.plain => (t.tkSoft, t.text),
    };
    return Container(
      padding: small ? const EdgeInsets.symmetric(horizontal: 7, vertical: 2) : const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
      child: Text(text, maxLines: 1, style: tkText(size: small ? 11 : 12, weight: 700, color: ink)),
    );
  }
}

/// kit.css `[data-tone]`: a tone's ground and ink.
(Color, Color) tkTone(TkTokens t, String tone) => switch (tone) {
  'pine' => (t.tonePineBg, t.tonePineInk),
  'lime' => (t.toneLimeBg, t.toneLimeInk),
  'peach' => (t.tonePeachBg, t.tonePeachInk),
  'yellow' => (t.toneYellowBg, t.toneYellowInk),
  'green' => (t.toneGreenBg, t.toneGreenInk),
  'lavender' => (t.toneLavenderBg, t.toneLavenderInk),
  'blue' => (t.toneBlueBg, t.toneBlueInk),
  _ => (t.toneSandBg, t.toneSandInk),
};

/// kit.css `.tk-row`: a 52 px tile (letters in their name's tone, or an
/// icon on the lime tint), the name and its line, a pill at the end.
class TkRow extends StatelessWidget {
  const TkRow({super.key, required this.name, required this.sub, this.letters, this.icon, this.pill, this.onTap, this.trailing});
  final String name;
  final String sub;

  /// The tile's letters, in [name]'s tone; or [icon] on the lime tint.
  final String? letters;
  final String? icon;
  final Widget? pill;
  final Widget? trailing;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = icon != null ? (t.tkLimeSoft, dark ? t.brandDark : t.brand) : tkTone(t, toneOf(name));
    return Semantics(
      button: onTap != null,
      child: TkPress(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 8),
          child: Row(
            children: [
              ExcludeSemantics(
                child: Container(
                  width: 52,
                  height: 52,
                  decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(14)),
                  alignment: Alignment.center,
                  child: icon != null ? TkIcon(icon!, size: 24, color: ink) : Text(letters ?? '', style: tkText(size: 16, weight: 800, color: ink)),
                ),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(name, style: tkText(size: 16, weight: 600, color: t.text)),
                    if (sub.isNotEmpty) Text(sub, style: tkText(size: 14, color: t.muted)),
                  ],
                ),
              ),
              if (pill != null) ...[const SizedBox(width: 14), pill!],
              if (trailing != null) ...[const SizedBox(width: 14), trailing!],
            ],
          ),
        ),
      ),
    );
  }
}

/// kit.tsx KitEmpty: the icon on the lime tint, a title, a line, and a
/// button when there is one.
class TkEmpty extends StatelessWidget {
  const TkEmpty({super.key, this.icon = 'info', required this.title, this.line, this.action});
  final String icon;
  final String title;
  final String? line;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 28, horizontal: 16),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          ExcludeSemantics(
            child: Container(
              width: 52,
              height: 52,
              margin: const EdgeInsets.only(bottom: 4),
              decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(16)),
              alignment: Alignment.center,
              child: TkIcon(icon, size: 24, color: dark ? t.brandDark : t.brand),
            ),
          ),
          const SizedBox(height: 6),
          Text(title, textAlign: TextAlign.center, style: tkText(size: 16, weight: 700, color: t.text)),
          if (line != null) ...[
            const SizedBox(height: 6),
            ConstrainedBox(
              // `max-width: 32ch`
              constraints: const BoxConstraints(maxWidth: 32 * 8.0),
              child: Text(line!, textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted)),
            ),
          ],
          if (action != null) ...[const SizedBox(height: 14), action!],
        ],
      ),
    );
  }
}

/// kit.tsx KitLoading: rows shimmering while a list loads.
class TkLoading extends StatefulWidget {
  const TkLoading({super.key, this.rows = 3, this.label = 'Loading…'});
  final int rows;
  final String label;

  @override
  State<TkLoading> createState() => _TkLoadingState();
}

class _TkLoadingState extends State<TkLoading> with SingleTickerProviderStateMixin {
  // `animation: shimmer 1.4s ease-in-out infinite`
  late final _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 1400));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    if (still) {
      _a.stop();
    } else if (!_a.isAnimating) {
      _a.repeat();
    }
  }

  @override
  void dispose() {
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    Widget bar(double radius, {double? width, double height = 12}) => AnimatedBuilder(
      animation: _a,
      builder: (context, _) => _Shimmer(at: Curves.easeInOut.transform(_a.value), radius: radius, width: width, height: height),
    );
    return Semantics(
      label: widget.label,
      liveRegion: true,
      child: ExcludeSemantics(
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 8),
          child: Column(
            children: [
              for (var i = 0; i < widget.rows; i++) ...[
                if (i > 0) const SizedBox(height: 14),
                Row(
                  children: [
                    bar(14, width: 52, height: 52),
                    const SizedBox(width: 14),
                    Expanded(
                      child: LayoutBuilder(
                        builder: (context, c) => Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [bar(999), const SizedBox(height: 8), bar(999, width: c.maxWidth * 0.6)],
                        ),
                      ),
                    ),
                  ],
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// motion.css `.tk-shimmer`: a band of the lighter ground sliding across.
class _Shimmer extends StatelessWidget {
  const _Shimmer({required this.at, required this.radius, this.width, required this.height});
  final double at;
  final double radius;
  final double? width;
  final double height;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    // `linear-gradient(90deg, code-bg 25%, panel-2 50%, code-bg 75%) 200% wide`,
    // its position from 200% to -200%.
    final shift = 2 - 4 * at;
    return Container(
      width: width ?? double.infinity,
      height: height,
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(radius),
        gradient: LinearGradient(
          begin: Alignment(-1 + shift, 0),
          end: Alignment(3 + shift, 0),
          colors: [t.codeBg, t.codeBg, t.panel2, t.codeBg, t.codeBg],
          stops: const [0, 0.25 / 2, 0.5 / 2, 0.75 / 2, 1],
        ),
      ),
    );
  }
}

/// chat.css `.tk-sheet-more`: the line under a list to the rest
/// ("Open stock ›"), muted, centred.
class TkSheetMore extends StatelessWidget {
  const TkSheetMore({super.key, required this.label, required this.onTap});
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.only(top: 14, bottom: 4),
        child: TapArea(
          child: Semantics(
            button: true,
            label: label,
            excludeSemantics: true,
            child: GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: onTap,
              child: ConstrainedBox(
                constraints: const BoxConstraints(minHeight: 40),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(label, style: tkText(size: 14, weight: 600, color: t.muted)),
                    const SizedBox(width: 4),
                    TkIcon('chevron', size: 16, color: t.muted),
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

/// kit.css `.tk-btn.soft`: a 44 px pill of the soft ground, pine words
/// (lime in the dark theme); `.tk-btn.primary` filled in pine.
class TkSoftButton extends StatelessWidget {
  const TkSoftButton({super.key, required this.label, this.onTap, this.primary = false, this.icon});
  final String label;
  final VoidCallback? onTap;
  final bool primary;
  final String? icon;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    // `.tk-btn.soft`: pine words; the text's colour in the dark theme.
    final ink = primary ? const Color(0xFFFFFFFF) : (dark ? t.text : t.brand);
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: label,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            constraints: const BoxConstraints(minHeight: 44),
            padding: const EdgeInsets.symmetric(horizontal: 18),
            decoration: BoxDecoration(color: primary ? t.brand : t.tkSoft, borderRadius: BorderRadius.circular(999)),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (icon != null) ...[TkIcon(icon!, size: 18, color: ink), const SizedBox(width: 8)],
                Flexible(child: Text(label, style: tkText(size: 15, weight: 700, color: ink))),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
