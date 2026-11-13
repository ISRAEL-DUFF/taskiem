// What "@" and "#" offer over the message box (web/src/pages/Mentions.tsx
// Picker and PlainList; chat.css .tk-picker, .tk-pick, .pick-icon;
// styles.css .mention-list): with chat tools "@" opens the picker of the
// designs (things and businesses in groups, Taskiem last, the first ready
// to take), floating over the thread; "#" (and "@" without the tools) the
// plain list above the box.

import 'package:flutter/widgets.dart';

import '../kit/icon.dart';
import '../theme/theme.dart';
import 'tags.dart';
import 'words.dart';

/// The picker of chat tools.
class TagPicker extends StatelessWidget {
  const TagPicker({super.key, required this.query, required this.groups, required this.first, required this.onPick});
  final String query;
  final List<TagGroup> groups;

  /// What Enter would take (drawn on the soft green).
  final TagSuggestion? first;
  final ValueChanged<TagSuggestion> onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    const what = 'Tag a thing or a business';
    final height = MediaQuery.sizeOf(context).height;
    return Semantics(
      container: true,
      label: 'Tag something',
      explicitChildNodes: true,
      child: Container(
        // `.tk-picker { padding: 10px 8px 8px; border-radius: 22px; max-height: min(60vh, 460px) }`
        constraints: BoxConstraints(maxHeight: (height * 0.6).clamp(0.0, 460.0)),
        decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(22), boxShadow: t.tkLift),
        clipBehavior: Clip.antiAlias,
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(8, 10, 8, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // `.tk-picker-head { padding: 2px 10px 8px; font-size: 13px }`
              Padding(
                padding: const EdgeInsets.fromLTRB(10, 2, 10, 8),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.baseline,
                  textBaseline: TextBaseline.alphabetic,
                  children: [
                    Expanded(
                      child: query.isEmpty
                          ? Text(what, style: tkText(size: 13, color: t.muted))
                          : Text.rich(
                              TextSpan(
                                children: [
                                  const TextSpan(text: 'Matches for '),
                                  TextSpan(
                                    text: query,
                                    style: TextStyle(color: t.text, fontWeight: FontWeight.w700, fontVariations: const [FontVariation('wght', 700)]),
                                  ),
                                ],
                              ),
                              style: tkText(size: 13, color: t.muted),
                            ),
                    ),
                    if (query.isNotEmpty) ...[const SizedBox(width: 8), Text(what, style: tkText(size: 12, color: t.muted))],
                  ],
                ),
              ),
              for (final g in groups)
                Semantics(
                  container: true,
                  label: g.label.isEmpty ? 'Taskiem' : g.label,
                  explicitChildNodes: true,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      if (g.label.isNotEmpty)
                        Padding(
                          // `.tk-picker-label { padding: 10px 10px 6px }`
                          padding: const EdgeInsets.fromLTRB(10, 10, 10, 6),
                          child: Text(g.label.toUpperCase(), style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.muted)),
                        ),
                      for (final s in g.items) _Pick(s: s, first: identical(s, first), onTap: () => onPick(s)),
                    ],
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// `.tk-pick`: its picture, its name and line; 58 px.
class _Pick extends StatelessWidget {
  const _Pick({required this.s, required this.first, required this.onTap});
  final TagSuggestion s;
  final bool first;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    // `.pick-icon`: 42 px, 12 px corners (round for a business, Taskiem).
    // `.pick-icon.soft`: the lime tint, pine (lime in dark).
    final softInk = dark ? t.brandDark : t.brand;
    final Widget icon = switch (s.kind) {
      // Not linked (found in Explore): soft, not lime.
      'business' when s.explore != null => _icon(t.tkLimeSoft, round: true, child: Text(initialsOf(s.name), style: tkText(size: 14, weight: 800, lineHeight: 1, color: softInk))),
      'business' => _icon(t.brandDark, round: true, child: Text(initialsOf(s.name), style: tkText(size: 14, weight: 800, lineHeight: 1, color: t.brandDarkText))),
      'customer' => _icon(t.tkLimeSoft, round: true, child: Text(initialsOf(s.name), style: tkText(size: 14, weight: 800, lineHeight: 1, color: softInk))),
      'invoice' => _icon(t.tkLimeSoft, child: TkIcon('invoices', size: 20, color: softInk)),
      'order' => _icon(t.tkLimeSoft, child: TkIcon('orders', size: 20, color: softInk)),
      'bill' => _icon(t.tkBlueBg, child: TkIcon('money', size: 20, color: t.tkBlueInk)),
      'purchase' => _icon(t.tkBlueBg, child: TkIcon('orders', size: 20, color: t.tkBlueInk)),
      'taskiem' => _icon(t.brand, round: true, child: TkIcon('sparkle', size: 20, color: t.brandDark)),
      'service' => _icon(t.tkBlueBg, child: TkIcon('clock', size: 20, color: t.tkBlueInk)),
      _ => _icon(t.tkLimeSoft, child: TkIcon('box', size: 20, color: dark ? t.brandDark : t.brand)),
    };
    return Semantics(
      button: true,
      label: [s.name, ?s.sub, ?s.badge].join(', ') + (s.explore != null ? ': see their card' : ''),
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 58),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          decoration: BoxDecoration(color: first ? t.tkSoft : null, borderRadius: BorderRadius.circular(14)),
          child: Row(
            children: [
              icon,
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(s.name, style: tkText(size: 15, weight: 600, lineHeight: 1.2, color: t.text)),
                    if (s.sub != null) Text(s.sub!, style: tkText(size: 13, lineHeight: 1.2, color: t.muted)),
                  ],
                ),
              ),
              // `.pick-badge`
              if (s.badge != null) ...[
                const SizedBox(width: 8),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
                  decoration: BoxDecoration(color: t.tkWarnBg, borderRadius: BorderRadius.circular(999)),
                  child: Text(s.badge!, style: tkText(size: 12, weight: 700, lineHeight: 1.2, color: t.tkWarnInk)),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  Widget _icon(Color bg, {required Widget child, bool round = false}) => Container(
    width: 42,
    height: 42,
    decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(round ? 21 : 12)),
    alignment: Alignment.center,
    child: child,
  );
}

/// `.mention-list`: the plain list, each a name with its mark and a line.
class TagList extends StatelessWidget {
  const TagList({super.key, required this.mark, required this.items, required this.onPick});
  final String mark;
  final List<TagSuggestion> items;
  final ValueChanged<TagSuggestion> onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: mark == '@' ? 'Businesses to mention' : (mark == '/' ? 'Things to tag' : 'Items to mention'),
      explicitChildNodes: true,
      child: Container(
        // `margin: 0 10px; padding: 4px; border: 1px solid; border-radius: 16px; max-height: 240px`
        margin: const EdgeInsets.symmetric(horizontal: 10),
        constraints: const BoxConstraints(maxHeight: 240),
        decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.border), borderRadius: BorderRadius.circular(16)),
        clipBehavior: Clip.antiAlias,
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final s in items)
                Semantics(
                  button: true,
                  label: '${mark == '/' ? '#' : mark}${s.name}${s.sub != null ? ', ${s.sub}' : ''}',
                  excludeSemantics: true,
                  child: GestureDetector(
                    behavior: HitTestBehavior.opaque,
                    onTap: () => onPick(s),
                    child: Padding(
                      // `.mention-list button { padding: 6px 8px; gap: 2px }`
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text('${mark == '/' ? '#' : mark}${s.name}', style: tkText(size: 13.5, weight: 600, lineHeight: 1.2, color: t.text)),
                          if (s.sub != null) ...[
                            const SizedBox(height: 2),
                            Text(s.sub!, style: tkText(size: 13, lineHeight: 1.2, color: t.muted)),
                          ],
                        ],
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
