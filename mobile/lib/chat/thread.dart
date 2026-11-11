// What every conversation shares, as the web's ChatStream.tsx, ChatHead.tsx,
// ThreadParts.tsx and lib/useThread.ts are shared by a business's
// conversation, a customer's and Taskiem's: the wallpaper, the head, and
// the thread (its days, each chip staying at the top while its day is
// under it; where the unread begin; the round button over the corner; the
// view placed on opening and following what comes only at the bottom).

import 'package:flutter/widgets.dart';

import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'chats.dart' show UnreadCount;
import 'words.dart';

/// styles.css `--chat-pattern`: two grids of soft dots, 28 px apart, the
/// second set 14 px in, over the wallpaper.
class Wallpaper extends CustomPainter {
  Wallpaper(this.ground, this.doodle);
  final Color ground;
  final Color doodle;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawRect(Offset.zero & size, Paint()..color = ground);
    // `radial-gradient(var(--chat-doodle) 1.2px, transparent 1.7px)`
    // centred in each 28 px tile.
    final clear = doodle.withValues(alpha: 0);
    void dot(double x, double y) {
      final c = Offset(x, y);
      canvas.drawCircle(
        c,
        1.7,
        Paint()..shader = RadialGradient(colors: [doodle, doodle, clear], stops: const [0, 1.2 / 1.7, 1]).createShader(Rect.fromCircle(center: c, radius: 1.7)),
      );
    }

    for (var y = 0.0; y < size.height + 28; y += 28) {
      for (var x = 0.0; x < size.width + 28; x += 28) {
        dot(x + 14, y + 14);
        dot(x, y);
      }
    }
  }

  @override
  bool shouldRepaint(Wallpaper old) => old.ground != ground || old.doodle != doodle;
}

/// ChatHead.tsx: Back, the avatar, the name with a line under it (or
/// "typing…"), the head's own buttons, and the menu.
class ChatHeadBar extends StatelessWidget {
  const ChatHeadBar({super.key, required this.avatar, required this.title, this.subtitle, this.typing = false, this.actions = const [], this.onBack});

  final Widget avatar;
  final String title;
  final String? subtitle;

  /// The other side is typing (or Taskiem preparing an answer).
  final bool typing;

  /// Buttons before the menu (search).
  final List<Widget> actions;
  final VoidCallback? onBack;

  /// A 40 px round button of the head.
  static Widget iconButton(String label, Widget icon, VoidCallback? onTap) => Semantics(
    button: true,
    label: label,
    child: TkPress(
      onTap: onTap ?? () {},
      child: SizedBox(width: 40, height: 40, child: Center(child: icon)),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final sub = typing ? 'typing…' : subtitle;
    return Container(
      // `.chat-head`: 12 px 16 px, a line under it.
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
      decoration: BoxDecoration(border: Border(bottom: BorderSide(color: t.border))),
      child: Row(
        children: [
          iconButton('Back to the chats', TkIcon('back', size: 20, color: t.text), onBack ?? () => Navigator.of(context).maybePop()),
          const SizedBox(width: 6),
          avatar,
          const SizedBox(width: 6),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Semantics(
                  header: true,
                  child: Text(
                    title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: tkText(size: 16, weight: 600, lineHeight: 1.35, letterSpacing: -0.16, color: t.text),
                  ),
                ),
                if (sub != null)
                  Text(
                    sub,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: typing
                        ? tkText(size: 13, weight: 600, lineHeight: 1.25, color: dark ? t.brandDark : t.brand)
                        : tkText(size: 13, lineHeight: 1.25, color: t.muted),
                  ),
              ],
            ),
          ),
          for (final a in actions) ...[const SizedBox(width: 6), a],
          const SizedBox(width: 6),
          iconButton('More about this conversation', RotatedBox(quarterTurns: 1, child: TkIcon('more', size: 18, stroke: 3, color: t.text)), null),
        ],
      ),
    );
  }
}

/// `.chat-day`: the day, in a soft pill, 6 px above and below.
class DayChip extends StatelessWidget {
  const DayChip(this.label, {super.key, this.chipKey, this.margin = true});
  final String label;
  final Key? chipKey;
  final bool margin;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final chip = Container(
      key: chipKey,
      padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 3),
      decoration: BoxDecoration(
        color: cssMix(t.panel, const Color(0x00000000), 0.92),
        borderRadius: BorderRadius.circular(999),
        boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 1), blurRadius: 0.5)],
      ),
      child: Text(label, style: tkText(size: 12, color: t.muted)),
    );
    return margin ? Padding(padding: const EdgeInsets.symmetric(vertical: 6), child: chip) : chip;
  }
}

/// `.chat-unread`: where the unread messages begin.
class UnreadMark extends StatelessWidget {
  const UnreadMark(this.count, {super.key, this.markKey});
  final int count;
  final Key? markKey;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Container(
        key: markKey,
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        decoration: BoxDecoration(color: t.tint, borderRadius: BorderRadius.circular(8)),
        child: Text(
          // ChatStream.tsx unreadWords.
          count == 1 ? '1 unread message' : '$count unread messages',
          textAlign: TextAlign.center,
          style: tkText(size: 12, weight: 600, color: t.tintText),
        ),
      ),
    );
  }
}

/// ThreadParts.tsx ThreadJump (`.chat-jump`): the round button over the
/// thread's corner, with the number that came while the reader was up.
class ThreadJump extends StatelessWidget {
  const ThreadJump({super.key, required this.count, required this.onTap, this.detached = false});
  final int count;
  final VoidCallback onTap;

  /// Away from the newest page (opened at a message): back to it.
  final bool detached;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: detached
          ? 'Jump to the latest messages'
          : count > 0
          ? '$count new ${count == 1 ? 'message' : 'messages'}: scroll to the newest'
          : 'Scroll to the newest messages',
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Stack(
          clipBehavior: Clip.none,
          children: [
            Container(
              width: 44,
              height: 44,
              decoration: BoxDecoration(color: t.chatIn, shape: BoxShape.circle, border: Border.all(color: t.border), boxShadow: t.shadowMd),
              alignment: Alignment.center,
              child: TkIcon('down', size: 20, color: t.text),
            ),
            if (count > 0) Positioned(top: -6, right: -4, child: UnreadCount(count)),
          ],
        ),
      ),
    );
  }
}

/// lib/thread.ts NEAR_BOTTOM: how close to the bottom still counts as there.
const _nearBottom = 80.0;

/// Where a stuck day chip sits under the thread's top: the stream's 8 px
/// and the chip's `top: 6px`.
const _stickTop = 14.0;

/// What the thread needs to know of a message.
typedef ThreadOf<T> = ({String Function(T) id, DateTime Function(T) at, bool Function(T) mine});

/// ChatStream.tsx with lib/useThread.ts: the messages by day, where the
/// unread begin, what follows them (Taskiem's cards), and the scrolling.
class ThreadView<T> extends StatefulWidget {
  const ThreadView({
    super.key,
    required this.items,
    required this.of,
    required this.render,
    required this.now,
    this.unread,
    this.empty,
    this.errors = const [],
    this.tail,
    this.placeAtMessagesEnd = false,
    this.hasOlder = false,
    this.onOlder,
    this.focus,
    this.detached = false,
    this.onLatest,
  });

  /// Opened at this message (a search's hit, a starred message): placed in
  /// the middle and flashed, as useThread's jump does.
  final String? focus;

  /// Away from the newest page: the round button shows, and goes back to it
  /// ([onLatest]).
  final bool detached;
  final VoidCallback? onLatest;

  /// There are older messages than these: asked for as the reader nears
  /// the top ([onOlder]), the reader's place kept as they land above.
  final bool hasOlder;
  final Future<void> Function()? onOlder;

  /// Null while loading; oldest first.
  final List<T>? items;
  final ThreadOf<T> of;

  /// A message, given the one before it on its day (null for the first);
  /// null to draw nothing (a diary line folded into another).
  final Widget? Function(T m, T? prev) render;
  final DateTime now;
  final ({String id, int count})? unread;
  final Widget? empty;
  final List<String> errors;

  /// After the messages: Taskiem's briefing and alert cards.
  final Widget? tail;

  /// Opened at the end of the messages rather than of everything: the web's
  /// view is placed before the cards after them have loaded.
  final bool placeAtMessagesEnd;

  @override
  State<ThreadView<T>> createState() => ThreadViewState<T>();
}

class ThreadViewState<T> extends State<ThreadView<T>> with SingleTickerProviderStateMixin {
  /// The thread's scrolling (a test scrolls it as the web half does).
  final scroll = ScrollController();
  final _viewport = GlobalKey();
  final _unreadKey = GlobalKey();
  final _endKey = GlobalKey();
  final _chipKeys = <String, GlobalKey>{};
  final _groupKeys = <String, GlobalKey>{};
  bool _placed = false;
  DateTime? _newest;
  bool _away = false;
  int _newCount = 0;
  ({String label, double top})? _stuck;
  bool _loadingOlder = false;
  final _focusKey = GlobalKey();

  /// `.bubble.flash`: the ring round the message opened at, 1.8 s.
  late final AnimationController _flash = AnimationController(vsync: this, duration: const Duration(milliseconds: 1800));
  String? _flashId;

  @override
  void initState() {
    super.initState();
    scroll.addListener(_onScroll);
    _follow(null);
  }

  @override
  void didUpdateWidget(ThreadView<T> oldWidget) {
    super.didUpdateWidget(oldWidget);
    _follow(oldWidget.items);
  }

  @override
  void dispose() {
    _flash.dispose();
    scroll.dispose();
    super.dispose();
  }

  void _flashAt(String id) {
    setState(() => _flashId = id);
    _flash.forward(from: 0).whenCompleteOrCancel(() {
      if (mounted) setState(() => _flashId = null);
    });
  }

  bool get _atBottom => !scroll.hasClients || scroll.position.maxScrollExtent - scroll.position.pixels <= _nearBottom;

  /// useThread.ts: a mark at the top of the thread, watched 200 px beyond
  /// the top; in sight, the older page is asked for.
  void _maybeOlder() {
    final items = widget.items;
    if (!widget.hasOlder || widget.onOlder == null || _loadingOlder || items == null || items.isEmpty || !scroll.hasClients) return;
    // The mark sits under the stream's 8 px padding.
    if (scroll.position.pixels > 8 + 200) return;
    _older();
  }

  Future<void> _older() async {
    _loadingOlder = true;
    final was = scroll.position.maxScrollExtent;
    try {
      await widget.onOlder!();
    } finally {
      _loadingOlder = false;
    }
    if (!mounted) return;
    // The page drawn above: the reader stays where they were.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !scroll.hasClients) return;
      final grew = scroll.position.maxScrollExtent - was;
      if (grew > 0) scroll.jumpTo((scroll.position.pixels + grew).clamp(0.0, scroll.position.maxScrollExtent));
      _maybeOlder();
    });
  }

  void _onScroll() {
    _maybeOlder();
    final away = !_atBottom;
    final stuck = _sticky();
    if (away != _away || stuck != _stuck || (!away && _newCount > 0)) {
      setState(() {
        _away = away;
        if (!away) _newCount = 0;
        _stuck = stuck;
      });
    }
  }

  /// lib/useThread.ts: opened at the first unread, 40 px under the top
  /// (clear of the day chip), or at the end; afterwards the thread follows
  /// new messages only while the reader is at the bottom or when they sent
  /// them, and counts the others on the round button.
  void _follow(List<T>? before) {
    final items = widget.items;
    if (items == null) return;
    final of = widget.of;
    final newest = items.isEmpty ? null : of.at(items.last);
    final stick = _atBottom;
    final prev = _newest;
    _newest = newest;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !scroll.hasClients) return;
      if (!_placed) {
        _placed = true;
        final view = _viewport.currentContext?.findRenderObject();
        final at = _unreadKey.currentContext?.findRenderObject();
        final end = _endKey.currentContext?.findRenderObject();
        final focus = _focusKey.currentContext?.findRenderObject();
        if (widget.focus != null && focus is RenderBox && view is RenderBox) {
          // scrollIntoView({ block: "center" }), then the flash.
          final mid = focus.localToGlobal(Offset(0, focus.size.height / 2), ancestor: view).dy;
          scroll.jumpTo((scroll.offset + mid - view.size.height / 2).clamp(0.0, scroll.position.maxScrollExtent));
          _flashAt(widget.focus!);
        } else if (at is RenderBox && view is RenderBox) {
          final top = at.localToGlobal(Offset.zero, ancestor: view).dy;
          scroll.jumpTo((scroll.offset + top - 40).clamp(0.0, scroll.position.maxScrollExtent));
        } else if (widget.placeAtMessagesEnd && end is RenderBox && view is RenderBox) {
          final bottom = end.localToGlobal(Offset(0, end.size.height + 8), ancestor: view).dy;
          scroll.jumpTo((scroll.offset + bottom - view.size.height).clamp(0.0, scroll.position.maxScrollExtent));
        } else {
          scroll.jumpTo(scroll.position.maxScrollExtent);
        }
        _onScroll();
        return;
      }
      if (prev == null) return;
      final arrived = items.where((m) => of.at(m).isAfter(prev)).toList();
      if (arrived.isEmpty) return;
      if (stick || arrived.any(of.mine)) {
        scroll.jumpTo(scroll.position.maxScrollExtent);
        setState(() => _newCount = 0);
      } else {
        setState(() => _newCount += arrived.where((m) => !of.mine(m)).length);
      }
    });
  }

  /// CSS `position: sticky` on `.chat-day` (top 6 px in its day's group):
  /// the chip of the day under the top stays there until its day's end
  /// pushes it up.
  ({String label, double top})? _sticky() {
    final view = _viewport.currentContext?.findRenderObject();
    if (view is! RenderBox) return null;
    ({String label, double top})? out;
    for (final e in _chipKeys.entries) {
      final chip = e.value.currentContext?.findRenderObject();
      final group = _groupKeys[e.key]?.currentContext?.findRenderObject();
      if (chip is! RenderBox || group is! RenderBox) continue;
      final natural = chip.localToGlobal(Offset.zero, ancestor: view).dy;
      final end = group.localToGlobal(Offset(0, group.size.height), ancestor: view).dy;
      if (end <= 0 || natural >= _stickTop) continue;
      final top = _stickTop < end - 6 - chip.size.height ? _stickTop : end - 6 - chip.size.height;
      if (top > natural + 0.01) out = (label: e.key, top: top);
    }
    return out;
  }

  void _toBottom() {
    scroll.jumpTo(scroll.position.maxScrollExtent);
    setState(() => _newCount = 0);
  }

  @override
  Widget build(BuildContext context) {
    final items = widget.items;
    final of = widget.of;
    final children = <Widget>[];
    if (items != null && items.isEmpty && widget.empty != null) {
      children.add(Center(child: Padding(padding: const EdgeInsets.symmetric(vertical: 12), child: widget.empty)));
    } else if (items != null) {
      String? day;
      T? prev;
      final group = <Widget>[];
      void close() {
        if (group.isEmpty) return;
        children.add(
          KeyedSubtree(
            key: _groupKeys.putIfAbsent(day!, GlobalKey.new),
            child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: gapped(List.of(group), 2)),
          ),
        );
        group.clear();
      }

      for (final m in items) {
        final label = dayLabel(of.at(m), widget.now);
        if (label != day) {
          close();
          day = label;
          prev = null;
          group.add(
            Center(
              child: Opacity(
                opacity: _stuck?.label == label ? 0 : 1,
                child: DayChip(label, chipKey: _chipKeys.putIfAbsent(label, GlobalKey.new)),
              ),
            ),
          );
        }
        if (widget.unread?.id == of.id(m)) group.add(UnreadMark(widget.unread!.count, markKey: _unreadKey));
        final w = widget.render(m, prev);
        prev = m;
        if (w != null) group.add(widget.focus == of.id(m) ? KeyedSubtree(key: _focusKey, child: w) : w);
      }
      close();
    }
    return ThreadFlash(
      id: _flashId,
      ring: _flash,
      child: Stack(
      children: [
        Positioned.fill(
          child: KeyedSubtree(
            key: _viewport,
            child: Semantics(
              label: 'Chat history',
              liveRegion: true,
              child: SingleChildScrollView(
                controller: scroll,
                // `.chat-stream`: 8 px 10 px, 2 px between groups.
                padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 10),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    for (final e in widget.errors) TkErrorBox(e),
                    // `.chat-top { height: 1px }` while there are older ones,
                    // in the stream's 2 px gaps.
                    if (widget.hasOlder && children.isNotEmpty) const SizedBox(height: 3),
                    KeyedSubtree(
                      key: _endKey,
                      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: gapped(children, 2)),
                    ),
                    if (items != null && widget.tail != null) ...[if (children.isNotEmpty) const SizedBox(height: 2), widget.tail!],
                  ],
                ),
              ),
            ),
          ),
        ),
        if (_stuck != null)
          Positioned(
            top: _stuck!.top,
            left: 0,
            right: 0,
            child: IgnorePointer(child: Center(child: DayChip(_stuck!.label, margin: false))),
          ),
        if (_away || widget.detached)
          Positioned(
            right: 16,
            bottom: 16,
            child: ThreadJump(
              count: _newCount,
              detached: widget.detached,
              onTap: widget.detached && widget.onLatest != null ? widget.onLatest! : _toBottom,
            ),
          ),
      ],
      ),
    );
  }
}

/// The message being flashed in a thread, and how far its ring has faded.
class ThreadFlash extends InheritedWidget {
  const ThreadFlash({super.key, required this.id, required this.ring, required super.child});
  final String? id;
  final Animation<double> ring;

  static ThreadFlash? of(BuildContext context) => context.dependOnInheritedWidgetOfExactType<ThreadFlash>();

  @override
  bool updateShouldNotify(ThreadFlash old) => old.id != id;
}

/// `.bubble.flash`: `0 0 0 3px var(--accent)`, held to 60% of 1.8 s, then
/// faded out (held throughout with motion turned down), round a bubble.
Widget flashRing(BuildContext context, String id, BorderRadius radius, Widget child) {
  final f = ThreadFlash.of(context);
  if (f == null || f.id != id) return child;
  final accent = TkTheme.tokens(context).accent;
  final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
  return AnimatedBuilder(
    animation: f.ring,
    builder: (context, child) {
      final v = f.ring.value;
      final a = still || v <= 0.6 ? 1.0 : 1 - Curves.easeOut.transform((v - 0.6) / 0.4);
      return DecoratedBox(
        decoration: BoxDecoration(
          borderRadius: radius,
          boxShadow: [BoxShadow(color: accent.withValues(alpha: accent.a * a), spreadRadius: 3)],
        ),
        child: child,
      );
    },
    child: child,
  );
}

/// A flex column's `gap`.
List<Widget> gapped(List<Widget> list, double gap) => [
  for (var i = 0; i < list.length; i++) ...[if (i > 0) SizedBox(height: gap), list[i]],
];

/// How much taller a line of a bubble's text is in the app than in Chrome
/// (Flutter's whole 23 px against Chrome's 22.69), half of it for each of
/// two lines: taken off a bubble's padding above and below.
const bubbleLineDrift = 0.31;

/// `.bubble`: a message's bubble, as wide as what it holds up to 84% of the
/// thread (560 px at most), the tail's corner on the first of a run, its
/// time in the corner of the last line ([inline]) or on a row of its own.
class BubbleFrame extends StatelessWidget {
  const BubbleFrame({super.key, required this.mine, required this.first, required this.stamp, required this.children, this.inline = true});

  final bool mine;
  final bool first;
  final Widget stamp;
  final List<Widget> children;
  final bool inline;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final radius = BorderRadius.only(
      topLeft: Radius.circular(first && !mine ? 4 : 16),
      topRight: Radius.circular(first && mine ? 4 : 16),
      bottomLeft: const Radius.circular(16),
      bottomRight: const Radius.circular(16),
    );
    return LayoutBuilder(
      builder: (context, box) {
        final max = (box.maxWidth * 0.84).clamp(0.0, 560.0);
        return Align(
          alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
          child: ConstrainedBox(
            constraints: BoxConstraints(maxWidth: max),
            child: IntrinsicWidth(
              child: Container(
                decoration: BoxDecoration(
                  color: mine ? t.chatOut : t.chatIn,
                  borderRadius: radius,
                  boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 1), blurRadius: 1)],
                ),
                child: Stack(
                  children: [
                    Padding(
                      // `padding: 8px 12px 6px`; on the right 11.5, as Chrome
                      // fits a line its measure says is a hundredth wider.
                      padding: const EdgeInsets.fromLTRB(12, 8 - bubbleLineDrift, 11.5, 6 - bubbleLineDrift),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          ...children,
                          if (!inline) Padding(padding: const EdgeInsets.only(top: 1), child: Align(alignment: Alignment.centerRight, child: stamp)),
                        ],
                      ),
                    ),
                    // `.stamp.inline { position: absolute; right: 9px; bottom: 5px }`
                    if (inline) Positioned(right: 9, bottom: 5, child: stamp),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}
