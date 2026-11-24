// A message's menu (web/src/pages/BubbleShell.tsx; styles.css .bubble-pop,
// .pop-pill, .pop-card, .pop-item): held on a phone, it opens over the
// thread: the reactions in a pill above the bubble and what can be done
// with it in a card below, each on the bubble's own side and kept on the
// screen; with no room above, the pill goes under the bubble and the card
// under it. A tap elsewhere, Back or a scroll closes it.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'emoji_panel.dart';

/// linkchat.ts REACTIONS, and Conversation.tsx's names for them.
const reactions = ['👍', '❤️', '😂', '😮', '😢', '🙏'];
const reactionNames = {'👍': 'thumbs up', '❤️': 'heart', '😂': 'laughing', '😮': 'surprise', '😢': 'sad', '🙏': 'thanks'};

/// An entry of the card: its icon and words (red when it cannot be undone).
class MenuItem {
  const MenuItem(this.icon, this.label, this.onTap, {this.danger = false, this.separated = false, this.done});
  final String icon;
  final String label;
  final VoidCallback onTap;
  final bool danger;

  /// Done in place, the menu kept open, its words then these (Copy's
  /// "Copied", as CopyItem).
  final String? done;

  /// A line above it (`.pop-sep`).
  final bool separated;
}

/// Opens the menu for the bubble at [bubble] (on the screen).
Future<void> showMessageMenu(
  BuildContext context, {
  required Rect bubble,
  required bool mine,
  required String what,
  required List<MenuItem> items,
  ValueChanged<String>? onReact,
  String? mineEmoji,
  VoidCallback? onPicking,
}) {
  HapticFeedback.selectionClick();
  return Navigator.of(context).push<void>(
    PageRouteBuilder<void>(
      opaque: false,
      // `animation: pop-in 0.12s ease-out`; none with motion turned down.
      transitionDuration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 120),
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (context, _, _) => _Menu(bubble: bubble, mine: mine, what: what, items: items, onReact: onReact, mineEmoji: mineEmoji, onPicking: onPicking),
      transitionsBuilder: (context, a, _, child) => FadeTransition(opacity: a, child: child),
    ),
  );
}

class _Menu extends StatefulWidget {
  const _Menu({required this.bubble, required this.mine, required this.what, required this.items, this.onReact, this.mineEmoji, this.onPicking});

  /// Told when the "+" is chosen.
  final VoidCallback? onPicking;
  final Rect bubble;
  final bool mine;
  final String what;
  final List<MenuItem> items;
  final ValueChanged<String>? onReact;
  final String? mineEmoji;

  @override
  State<_Menu> createState() => _MenuState();
}

class _MenuState extends State<_Menu> {
  final _pill = GlobalKey();
  final _card = GlobalKey();
  Offset? _pillAt;
  Offset? _cardAt;

  /// The "+" chosen: every emoji in the reactions' place, the card gone.
  bool _picking = false;

  void _pickAny() {
    widget.onPicking?.call();
    setState(() {
      _picking = true;
      _pillAt = null;
      _cardAt = null;
    });
    WidgetsBinding.instance.addPostFrameCallback((_) => _place());
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _place());
  }

  /// BubbleShell's placing: the pill 6 px above the bubble (or under it, with
  /// no room above), the card 6 px under what is under the bubble; each
  /// against the bubble's own side, kept 8 px inside the screen.
  void _place() {
    if (!mounted) return;
    final size = MediaQuery.sizeOf(context);
    final r = widget.bubble;
    const gap = 6.0, edge = 8.0;
    double place(double w) {
      final x = widget.mine ? r.right - w : r.left;
      final most = size.width - w - edge;
      return (x < most ? x : most) < edge ? edge : (x < most ? x : most);
    }

    Offset? pill;
    var below = r.bottom + gap;
    final p = _pill.currentContext?.size;
    if (p != null) {
      var top = r.top - p.height - gap;
      if (top < edge) {
        top = r.bottom + gap;
        below = top + p.height + gap;
      }
      pill = Offset(place(p.width), top);
    }
    Offset? card;
    final c = _card.currentContext?.size;
    if (c != null) {
      var top = below;
      if (top + c.height > size.height - edge) top = (size.height - c.height - edge).clamp(edge, double.infinity);
      card = Offset(place(c.width), top);
    }
    setState(() {
      _pillAt = pill;
      _cardAt = card;
    });
  }

  void _close() => Navigator.of(context).pop();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    Widget at(Offset? o, Widget child) =>
        o == null ? Positioned(left: 0, top: 0, child: Opacity(opacity: 0, child: child)) : Positioned(left: o.dx, top: o.dy, child: child);
    return Stack(
      children: [
        Positioned.fill(
          child: Backdrop(onClose: _close, onVerticalDragStart: (_) => _close()),
        ),
        if (widget.onReact != null && _picking)
          at(
            _pillAt,
            // `.pop-picker { width: min(360px, calc(100vw - 16px)); border-radius: 14px }`
            Container(
              key: _pill,
              width: (MediaQuery.sizeOf(context).width - 16).clamp(0.0, 360.0),
              clipBehavior: Clip.antiAlias,
              decoration: BoxDecoration(
                color: t.panel,
                borderRadius: BorderRadius.circular(14),
                boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 4), blurRadius: 18)],
              ),
              child: EmojiPanel(
                maxHeight: 330,
                onPick: (e) {
                  _close();
                  widget.onReact!(e);
                },
              ),
            ),
          ),
        if (widget.onReact != null && !_picking)
          at(
            _pillAt,
            Semantics(
              container: true,
              label: 'React',
              child: Container(
                key: _pill,
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
                decoration: BoxDecoration(
                  color: t.panel,
                  borderRadius: BorderRadius.circular(999),
                  boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 2), blurRadius: 12)],
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    for (final e in reactions) ...[
                      _Pick(
                        emoji: e,
                        label: 'React with ${reactionNames[e]}',
                        pressed: widget.mineEmoji == e,
                        onTap: () {
                          _close();
                          widget.onReact!(e);
                        },
                      ),
                      const SizedBox(width: 2),
                    ],
                    // "+": every emoji.
                    TapArea(child: Semantics(
                      container: true,
                      button: true,
                      label: 'More reactions',
                      onTap: _pickAny,
                      excludeSemantics: true,
                      child: TkPress(
                        onTap: _pickAny,
                        child: Container(
                          width: 38,
                          height: 38,
                          decoration: BoxDecoration(color: t.chatSelect, shape: BoxShape.circle),
                          alignment: Alignment.center,
                          child: TkIcon('plus', size: 20, color: t.muted),
                        ),
                      ),
                    )),
                  ],
                ),
              ),
            ),
          ),
        if (!_picking)
          at(
            _cardAt,
            Semantics(
              container: true,
              // Each item its own button, even when there is only one.
              explicitChildNodes: true,
              label: 'Options for ${widget.what}',
              child: Container(
                key: _card,
                constraints: const BoxConstraints(minWidth: 220),
                padding: const EdgeInsets.symmetric(vertical: 6),
                decoration: BoxDecoration(
                  color: t.panel,
                  borderRadius: BorderRadius.circular(14),
                  boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 4), blurRadius: 18)],
                ),
                child: IntrinsicWidth(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (final i in widget.items) ...[
                        if (i.separated) Container(height: 1, margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 6), color: t.border),
                        _Item(
                          item: i,
                          onTap: () {
                            if (i.done == null) _close();
                            i.onTap();
                          },
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ),
          ),
      ],
    );
  }
}

/// `.pop-pill .react-pick`: an emoji, 38 px round, ringed when it is ours.
class _Pick extends StatelessWidget {
  const _Pick({required this.emoji, required this.label, required this.pressed, required this.onTap});
  final String emoji;
  final String label;
  final bool pressed;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(child: Semantics(
      button: true,
      selected: pressed,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          width: 38,
          height: 38,
          decoration: BoxDecoration(
            color: pressed ? t.chatSelect : null,
            shape: BoxShape.circle,
            border: pressed ? Border.all(color: t.accent, width: 2) : null,
          ),
          alignment: Alignment.center,
          child: Text(emoji, style: tkText(size: 24, lineHeight: 1)),
        ),
      ),
    ));
  }
}

/// `.pop-item`: 44 px, the icon muted, 16 px before the words.
class _Item extends StatefulWidget {
  const _Item({required this.item, required this.onTap});
  final MenuItem item;
  final VoidCallback onTap;

  @override
  State<_Item> createState() => _ItemState();
}

class _ItemState extends State<_Item> {
  bool _done = false;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final item = widget.item;
    final ink = item.danger ? t.danger : t.text;
    final label = _done ? item.done! : item.label;
    void onTap() {
      widget.onTap();
      if (item.done != null) setState(() => _done = true);
    }

    return TapArea(child: Semantics(
      button: true,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        scale: 1,
        child: Container(
          constraints: const BoxConstraints(minHeight: 44),
          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 8),
          color: const Color(0x00000000),
          child: Row(
            children: [
              TkIcon(item.icon, size: 20, color: item.danger ? t.danger : t.muted),
              const SizedBox(width: 16),
              Text(label, style: tkText(size: 15, weight: 500, lineHeight: 1.2, color: ink)),
            ],
          ),
        ),
      ),
    ));
  }
}
