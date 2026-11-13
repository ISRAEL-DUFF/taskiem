// What every conversation shares, as the web's ChatStream.tsx, ChatHead.tsx,
// ThreadParts.tsx and lib/useThread.ts are shared by a business's
// conversation, a customer's and Taskiem's: the wallpaper, the head, and
// the thread (its days, each chip staying at the top while its day is
// under it; where the unread begin; the round button over the corner; the
// view placed on opening and following what comes only at the bottom).

import 'package:flutter/gestures.dart' show TapGestureRecognizer;
import 'package:flutter/rendering.dart' show RenderDecoratedBox;
import 'package:flutter/services.dart' show HapticFeedback;
import 'package:flutter/widgets.dart';

import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/tap_area.dart';
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

/// The chat's wallpaper under [child], drawn once in a layer of its own:
/// nothing drawn over it (a caret blinking, a list scrolling) draws the
/// dots again.
class WallpaperBox extends StatelessWidget {
  const WallpaperBox({super.key, required this.ground, required this.doodle, required this.child});
  final Color ground;
  final Color doodle;
  final Widget child;

  @override
  Widget build(BuildContext context) => Stack(
    children: [
      Positioned.fill(
        child: RepaintBoundary(
          child: CustomPaint(painter: Wallpaper(ground, doodle), isComplex: true),
        ),
      ),
      child,
    ],
  );
}

/// ChatHead.tsx: Back, the avatar, the name with a line under it (or
/// "typing…"), the head's own buttons, and the menu.
class ChatHeadBar extends StatelessWidget {
  const ChatHeadBar({
    super.key,
    required this.avatar,
    required this.title,
    this.subtitle,
    this.typing = false,
    this.actions = const [],
    this.onBack,
    this.onMore,
    this.onAvatar,
  });

  /// The ⋯ menu's button pressed: where it is on the screen.
  final ValueChanged<Rect>? onMore;

  /// The picture pressed (`.chat-avatar-btn`: About the business).
  final VoidCallback? onAvatar;

  final Widget avatar;
  final String title;
  final String? subtitle;

  /// The other side is typing (or Taskiem preparing an answer).
  final bool typing;

  /// Buttons before the menu (search).
  final List<Widget> actions;
  final VoidCallback? onBack;

  /// A 40 px round button of the head.
  static Widget iconButton(String label, Widget icon, VoidCallback? onTap) => TapArea(
    child: Semantics(
      button: true,
      label: label,
      child: TkPress(
        onTap: onTap ?? () {},
        child: SizedBox(width: 40, height: 40, child: Center(child: icon)),
      ),
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
          if (onAvatar != null)
            TapArea(child: Semantics(
              button: true,
              label: 'About $title',
              child: GestureDetector(onTap: onAvatar, child: avatar),)
            )
          else
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
          // With no menu of its own (Taskiem's), no ⋯ on a phone: the web's
          // holds only Keyboard there, which a phone hides.
          if (onMore != null) ...[
          const SizedBox(width: 6),
          Builder(
            builder: (context) => iconButton(
              'More about this conversation',
              RotatedBox(quarterTurns: 1, child: TkIcon('more', size: 18, stroke: 3, color: t.text)),
              onMore == null
                  ? null
                  : () {
                      final box = context.findRenderObject()! as RenderBox;
                      onMore!(box.localToGlobal(Offset.zero) & box.size);
                    },
            ),
          ),
          ],
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
    return TapArea(child: Semantics(
      button: true,
      label: detached
          ? 'Jump to the latest messages'
          : count > 0
          ? '$count new ${count == 1 ? 'message' : 'messages'}: scroll to the newest'
          : 'Scroll to the newest messages',
      onTap: onTap,
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
    ));
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
  /// Away from the newest (the round button shows) and how many came
  /// since: their own listener, as the day chip's.
  final _jump = ValueNotifier<({bool away, int count})>((away: false, count: 0));
  bool get _away => _jump.value.away;
  int get _newCount => _jump.value.count;
  set _newCount(int n) => _jump.value = (away: _away, count: n);

  /// Kept at the bottom as what is below grows: opened at the end, or
  /// scrolled there by the reader (not opened at the unread or a message).
  bool _keep = false;

  /// At the very end of the thread (not merely with the newest message in
  /// view), as the reader last left it.
  bool _atEnd = false;
  /// The day chip held at the top while scrolling: its own listener, so a
  /// frame of scrolling redraws the chip, not every message.
  final _stuck = ValueNotifier<({String label, double top})?>(null);
  bool _loadingOlder = false;
  final _focusKey = GlobalKey();

  /// The messages drawn so far: one not among them arrives with motion
  /// (those there when the thread opened, and older pages, do not).
  final _known = <String>{};
  bool _seeded = false;

  /// `.bubble.flash`: the ring round the message opened at, 1.8 s.
  late final AnimationController _flash = AnimationController(vsync: this, duration: const Duration(milliseconds: 1800));
  String? _flashId;

  @override
  void initState() {
    super.initState();
    PopIn.openedAt = DateTime.now();
    scroll.addListener(_onScroll);
    _follow(null);
  }

  @override
  void didUpdateWidget(ThreadView<T> oldWidget) {
    super.didUpdateWidget(oldWidget);
    _follow(oldWidget.items);
    _hold();
  }

  @override
  void dispose() {
    _flash.dispose();
    _stuck.dispose();
    _jump.dispose();
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

  /// useThread.ts newestEnd: how far down the content the newest message ends.
  double? _newestEnd() {
    final box = _endKey.currentContext?.findRenderObject();
    final view = _viewport.currentContext?.findRenderObject();
    if (box is! RenderBox || view is! RenderBox || !box.hasSize || !scroll.hasClients) return null;
    return box.localToGlobal(Offset(0, box.size.height), ancestor: view).dy + scroll.offset;
  }

  /// thread.ts atNewest: at the thread's end, or the newest message ending
  /// within the view with only what follows it (Taskiem's cards) below.
  bool get _atNewest {
    if (_atBottom) return true;
    final end = _newestEnd();
    if (end == null) return false;
    final p = scroll.position;
    return end <= p.pixels + p.viewportDimension + _nearBottom && end > p.pixels;
  }

  /// useThread.ts toNewest with thread.ts newestTop: the newest message at
  /// the view's end, a little room under it, when cards follow it; else the
  /// end of the thread.
  void _toNewest() {
    final p = scroll.position;
    final end = _newestEnd();
    final height = p.maxScrollExtent + p.viewportDimension;
    if (end == null || height - end <= _nearBottom) {
      scroll.jumpTo(p.maxScrollExtent);
    } else {
      scroll.jumpTo((end + 16 - p.viewportDimension).clamp(0.0, p.maxScrollExtent));
    }
  }

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

  /// Kept at the bottom after this frame, if the reader is there.
  void _hold() {
    if (!_placed || !_atEnd || !_keep) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !scroll.hasClients || !_atEnd || !_keep) return;
      final end = scroll.position.maxScrollExtent;
      if (scroll.offset != end) scroll.jumpTo(end);
    });
  }

  void _onScroll() {
    _maybeOlder();
    final near = _atNewest;
    // Held at the very end only from there: arriving at it, not with the
    // newest message merely in view (toNewest leaves Taskiem's cards below
    // it, and that place stands).
    final end = _atBottom;
    if (_placed) {
      if (!end) {
        _keep = false;
      } else if (!_atEnd) {
        _keep = true;
      }
    }
    _atEnd = end;
    final away = !near;
    final stuck = _sticky();
    _stuck.value = stuck;
    if (away != _away || (!away && _newCount > 0)) _jump.value = (away: away, count: away ? _newCount : 0);
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
    final stick = _atNewest;
    final prev = _newest;
    _newest = newest;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !scroll.hasClients) return;
      if (!_placed) {
        _placed = true;
        final view = _viewport.currentContext?.findRenderObject();
        final at = _unreadKey.currentContext?.findRenderObject();
        final focus = _focusKey.currentContext?.findRenderObject();
        var atEnd = false;
        if (widget.focus != null && focus is RenderBox && view is RenderBox) {
          // scrollIntoView({ block: "center" }), then the flash.
          final mid = focus.localToGlobal(Offset(0, focus.size.height / 2), ancestor: view).dy;
          scroll.jumpTo((scroll.offset + mid - view.size.height / 2).clamp(0.0, scroll.position.maxScrollExtent));
          _flashAt(widget.focus!);
        } else if (at is RenderBox && view is RenderBox) {
          final top = at.localToGlobal(Offset.zero, ancestor: view).dy;
          scroll.jumpTo((scroll.offset + top - 40).clamp(0.0, scroll.position.maxScrollExtent));
        } else {
          scroll.jumpTo(scroll.position.maxScrollExtent);
          atEnd = true;
        }
        _onScroll();
        // Opened at the end: kept there as what is below arrives.
        _keep = atEnd;
        return;
      }
      if (prev == null) return;
      final arrived = items.where((m) => of.at(m).isAfter(prev)).toList();
      if (arrived.isEmpty) return;
      if (stick || arrived.any(of.mine)) {
        // The newest message in view, not what follows it (Taskiem's
        // cards): an answer is never left out of sight.
        _toNewest();
        _newCount = 0;
      } else {
        _newCount += arrived.where((m) => !of.mine(m)).length;
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
    _newCount = 0;
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
              child: ValueListenableBuilder(
                valueListenable: _stuck,
                builder: (context, stuck, chip) => Opacity(opacity: stuck?.label == label ? 0 : 1, child: chip),
                child: DayChip(label, chipKey: _chipKeys.putIfAbsent(label, GlobalKey.new)),
              ),
            ),
          );
        }
        if (widget.unread?.id == of.id(m)) group.add(UnreadMark(widget.unread!.count, markKey: _unreadKey));
        final w = widget.render(m, prev);
        prev = m;
        final id = of.id(m);
        final fresh = _known.add(id) && _seeded && !of.at(m).isBefore(_newest ?? of.at(m));
        // Each message drawn once and kept: scrolling moves the pictures.
        if (w != null) {
          group.add(
            MessageArrive(
              key: ValueKey('arrive $id'),
              animate: fresh,
              mine: of.mine(m),
              child: RepaintBoundary(child: widget.focus == id ? KeyedSubtree(key: _focusKey, child: w) : w),
            ),
          );
        }
      }
      close();
      _seeded = true;
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
        Positioned.fill(
          child: IgnorePointer(
            child: ValueListenableBuilder(
              valueListenable: _stuck,
              builder: (context, stuck, _) => stuck == null
                  ? const SizedBox.shrink()
                  : Stack(
                      children: [
                        Positioned(top: stuck.top, left: 0, right: 0, child: Center(child: DayChip(stuck.label, margin: false))),
                      ],
                    ),
            ),
          ),
        ),
        Positioned(
          right: 16,
          bottom: 16,
          child: ValueListenableBuilder(
            valueListenable: _jump,
            builder: (context, jump, _) => AnimatedSwitcher(
              duration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 220),
              switchInCurve: Curves.easeOutBack,
              switchOutCurve: Curves.easeIn,
              transitionBuilder: (child, a) => FadeTransition(
                opacity: a,
                child: ScaleTransition(scale: Tween(begin: 0.6, end: 1.0).animate(a), child: child),
              ),
              child: jump.away || widget.detached
                  ? ThreadJump(
                      count: jump.count,
                      detached: widget.detached,
                      onTap: widget.detached && widget.onLatest != null ? widget.onLatest! : _toBottom,
                    )
                  : const SizedBox.shrink(),
            ),
          ),
        ),
      ],
      ),
    );
  }
}

/// A message arriving: it rises from below its place and grows from its
/// own side (the right for one's own), as a messaging app's do. Drawn as it
/// is when it was already there, or when the phone asks for less motion.
class MessageArrive extends StatefulWidget {
  const MessageArrive({super.key, required this.child, required this.animate, required this.mine});
  final Widget child;
  final bool animate;
  final bool mine;

  @override
  State<MessageArrive> createState() => _MessageArriveState();
}

class _MessageArriveState extends State<MessageArrive> with SingleTickerProviderStateMixin {
  AnimationController? _a;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    if (widget.animate && !still && _a == null) {
      _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 340))..forward();
    }
  }

  @override
  void dispose() {
    _a?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final a = _a;
    if (a == null) return widget.child;
    final rise = CurvedAnimation(parent: a, curve: Curves.easeOutCubic);
    final grow = CurvedAnimation(parent: a, curve: Curves.easeOutBack);
    return AnimatedBuilder(
      animation: a,
      builder: (context, child) => Opacity(
              alwaysIncludeSemantics: true,
              opacity: Curves.easeOut.transform(a.value.clamp(0, 1)),
              child: Transform.translate(
                offset: Offset(0, 22 * (1 - rise.value)),
                child: Transform.scale(
                  scale: 0.86 + 0.14 * grow.value,
                  alignment: widget.mine ? Alignment.bottomRight : Alignment.bottomLeft,
                  child: child,
                ),
              ),
      ),
      child: widget.child,
    );
  }
}

/// What appears in a thread already open (a reaction): it pops in, grown
/// past its size and settling. What is there as the thread opens is not.
class PopIn extends StatefulWidget {
  const PopIn({super.key, required this.child});
  final Widget child;

  /// When the open thread opened.
  static DateTime openedAt = DateTime.fromMillisecondsSinceEpoch(0);

  @override
  State<PopIn> createState() => _PopInState();
}

class _PopInState extends State<PopIn> with SingleTickerProviderStateMixin {
  AnimationController? _a;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    if (_a == null && !still && DateTime.now().difference(PopIn.openedAt) > const Duration(milliseconds: 1500)) {
      _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 420))..forward();
    }
  }

  @override
  void dispose() {
    _a?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final a = _a;
    if (a == null) return widget.child;
    final pop = CurvedAnimation(parent: a, curve: Curves.elasticOut);
    return AnimatedBuilder(
      animation: a,
      builder: (context, child) => Transform.scale(scale: pop.value, child: child),
      child: widget.child,
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

/// ChatHead.tsx HeadMenu (`.chat-menu`): the head's ⋯ menu, under its
/// button and against its right edge; its entries, then nothing more on a
/// phone (Keyboard is a computer's). A tap elsewhere or Back closes it.
Future<void> showHeadMenu(BuildContext context, {required Rect at, required List<(String, VoidCallback)> items}) {
  final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
  return Navigator.of(context).push<void>(
    PageRouteBuilder<void>(
      opaque: false,
      // `.chat-menu { animation: chat-menu-in 160ms var(--ease-out) }`:
      // it grows from the button's corner as it fades in.
      transitionDuration: still ? Duration.zero : const Duration(milliseconds: 160),
      reverseTransitionDuration: still ? Duration.zero : const Duration(milliseconds: 110),
      transitionsBuilder: (context, a, _, child) {
        final e = CurvedAnimation(parent: a, curve: const Cubic(0.16, 1, 0.3, 1));
        return FadeTransition(opacity: a, child: ScaleTransition(scale: Tween(begin: 0.92, end: 1.0).animate(e), alignment: Alignment.topRight, child: child));
      },
      pageBuilder: (context, _, _) {
        final t = TkTheme.tokens(context);
        final width = MediaQuery.sizeOf(context).width;
        return Stack(
          children: [
            Positioned.fill(child: Backdrop(onClose: () => Navigator.of(context).pop())),
            Positioned(
              right: width - at.right,
              top: at.bottom + 4,
              child: Semantics(
                container: true,
                explicitChildNodes: true,
                child: Container(
                  // `.chat-menu`: 6 px, 18 px corners, the lift, no line.
                  constraints: BoxConstraints(minWidth: 200, maxWidth: width - 32),
                  padding: const EdgeInsets.all(6),
                  decoration: BoxDecoration(
                    color: t.panel,
                    borderRadius: BorderRadius.circular(18),
                    boxShadow: t.tkLift,
                  ),
                  child: IntrinsicWidth(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        for (final (label, onTap) in items)
                          TapArea(child: Semantics(
                            button: true,
                            label: label,
                            onTap: () {
                                Navigator.of(context).pop();
                                onTap();
                              },
                            excludeSemantics: true,
                            child: _MenuItem(
                              label: label,
                              onTap: () {
                                Navigator.of(context).pop();
                                onTap();
                              },
                            ),
                          )),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ],
        );
      },
    ),
  );
}

/// `.chat-menu button`: 12 px 14 px, 44 px, 12 px corners, no shadow; the
/// selection's tint while pressed.
class _MenuItem extends StatefulWidget {
  const _MenuItem({required this.label, required this.onTap});
  final String label;
  final VoidCallback onTap;

  @override
  State<_MenuItem> createState() => _MenuItemState();
}

class _MenuItemState extends State<_MenuItem> {
  bool _down = false;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTapDown: (_) => setState(() => _down = true),
      onTapCancel: () => setState(() => _down = false),
      onTapUp: (_) => setState(() => _down = false),
      onTap: widget.onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 90),
        constraints: const BoxConstraints(minHeight: 44),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        alignment: Alignment.centerLeft,
        decoration: BoxDecoration(color: _down ? t.chatSelect : null, borderRadius: BorderRadius.circular(12)),
        child: Text(widget.label, style: tkText(size: 15, weight: 500, lineHeight: 1.33, color: t.text)),
      ),
    );
  }
}

/// BubbleShell's touch: a finger held opens the menu, a swipe right replies
/// (60 px, drawn up to 80), as WhatsApp's.
class BubbleHold extends StatefulWidget {
  const BubbleHold({super.key, required this.child, this.onHold, this.onReply, this.label = 'Message'});
  final Widget child;

  /// What a screen reader calls the bubble before its words ("Your
  /// message", "Message from Bayo").
  final String label;
  final ValueChanged<Rect>? onHold;
  final VoidCallback? onReply;

  @override
  State<BubbleHold> createState() => _BubbleHoldState();
}

class _BubbleHoldState extends State<BubbleHold> with SingleTickerProviderStateMixin {
  double _dx = 0;
  bool _armed = false;

  /// The bubble going back to its place, after a swipe.
  late final AnimationController _back = AnimationController(vsync: this, duration: const Duration(milliseconds: 260))
    ..addListener(() => setState(() => _dx = _from * (1 - Curves.easeOutBack.transform(_back.value)).clamp(-0.2, 1.0)));
  double _from = 0;

  @override
  void dispose() {
    _back.dispose();
    super.dispose();
  }

  void _drag(double delta) {
    _back.stop();
    setState(() => _dx = (_dx + delta).clamp(0.0, 80.0));
    final armed = _dx >= 60;
    if (armed && !_armed) HapticFeedback.selectionClick();
    _armed = armed;
  }

  void _release() {
    if (_dx >= 60) widget.onReply!();
    _armed = false;
    _from = _dx;
    if (MediaQuery.maybeDisableAnimationsOf(context) ?? false) {
      setState(() => _dx = 0);
    } else {
      _back.forward(from: 0);
    }
  }

  /// The bubble itself, on the screen (not its row).
  Rect _rect() {
    RenderBox? box;
    void find(Element e) {
      final r = e.renderObject;
      if (r is RenderDecoratedBox && box == null) {
        box = r;
        return;
      }
      e.visitChildElements(find);
    }

    (context as Element).visitChildElements(find);
    final b = box ?? context.findRenderObject()! as RenderBox;
    return b.localToGlobal(Offset.zero) & b.size;
  }

  @override
  Widget build(BuildContext context) {
    final gestures = GestureDetector(
      // Said to a screen reader by the node below, which keeps the
      // bubble's own labels (its ticks, a star) apart.
      excludeFromSemantics: true,
      onLongPress: widget.onHold == null ? null : () => widget.onHold!(_rect()),
      onHorizontalDragUpdate: widget.onReply == null ? null : (d) => _drag(d.delta.dx),
      onHorizontalDragEnd: widget.onReply == null ? null : (_) => _release(),
      onHorizontalDragCancel: widget.onReply == null ? null : () => _release(),
      // Only while swiped: around the bubble, a Stack would keep taps from
      // what hangs outside it (Retry under a message not sent).
      child: _dx <= 0
          ? widget.child
          : Stack(
        clipBehavior: Clip.none,
        children: [
          // The reply arrow, coming in under the bubble as it is swiped.
          if (_dx > 4)
            Positioned(
              left: 4,
              top: 0,
              bottom: 0,
              child: Center(
                child: Opacity(
                  opacity: (_dx / 60).clamp(0.0, 1.0),
                  child: Transform.scale(
                    scale: 0.6 + 0.4 * (_dx / 60).clamp(0.0, 1.0),
                    child: Container(
                      width: 32,
                      height: 32,
                      decoration: BoxDecoration(color: TkTheme.tokens(context).chatIn, shape: BoxShape.circle),
                      alignment: Alignment.center,
                      child: TkIcon('reply', size: 17, color: TkTheme.tokens(context).text),
                    ),
                  ),
                ),
              ),
            ),
          Transform.translate(offset: Offset(_dx, 0), child: widget.child),
        ],
      ),
    );
    if (widget.onHold == null) return gestures;
    return Semantics(
      container: true,
      explicitChildNodes: true,
      label: widget.label,
      onLongPressHint: 'Options',
      onLongPress: () => widget.onHold!(_rect()),
      child: gestures,
    );
  }
}

/// Owns the tap recognisers of a text's links: made once, kept while the
/// text is, and let go with it (a span's recogniser is the caller's to
/// dispose; made afresh on every build, each was left behind).
class TapsOwner extends StatefulWidget {
  const TapsOwner({super.key, required this.builder});
  final Widget Function(BuildContext context, TapGestureRecognizer Function(String key, VoidCallback onTap) tap) builder;

  @override
  State<TapsOwner> createState() => _TapsOwnerState();
}

class _TapsOwnerState extends State<TapsOwner> {
  final _all = <String, TapGestureRecognizer>{};

  @override
  void dispose() {
    for (final r in _all.values) {
      r.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final used = <String>{};
    final w = widget.builder(context, (key, onTap) {
      used.add(key);
      return (_all[key] ??= TapGestureRecognizer())..onTap = onTap;
    });
    // Those no longer in the text: let go once the old text is replaced.
    final gone = [for (final k in _all.keys) if (!used.contains(k)) k].map((k) => _all.remove(k)!).toList();
    if (gone.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        for (final r in gone) {
          r.dispose();
        }
      });
    }
    return w;
  }
}

/// motion.css `tk-cheer`: a good moment's ring, 3 px of lime-on-pine's
/// lime (`--brand-dark`), 22 px corners, growing from 0.4 to 1.6 as it
/// shows (by 40 %) and fades, over 900 ms (`--t-cheer`, `--ease-out`). None
/// with motion turned down.
class CheerRing extends StatefulWidget {
  const CheerRing({super.key});

  @override
  State<CheerRing> createState() => _CheerRingState();
}

class _CheerRingState extends State<CheerRing> with SingleTickerProviderStateMixin {
  late final _go = AnimationController(vsync: this, duration: const Duration(milliseconds: 900))..forward();

  @override
  void dispose() {
    _go.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (MediaQuery.maybeDisableAnimationsOf(context) ?? false) return const SizedBox.shrink();
    final t = TkTheme.tokens(context);
    // `--ease-out: cubic-bezier(0.16, 1, 0.3, 1)`
    const ease = Cubic(0.16, 1, 0.3, 1);
    return AnimatedBuilder(
      animation: _go,
      builder: (context, _) {
        final p = ease.transform(_go.value);
        final opacity = p < 0.4 ? p / 0.4 : 1 - (p - 0.4) / 0.6;
        return Opacity(
          opacity: opacity.clamp(0.0, 1.0),
          child: Transform.scale(
            scale: 0.4 + 1.2 * p,
            child: DecoratedBox(
              decoration: BoxDecoration(border: Border.all(color: t.brandDark, width: 3), borderRadius: BorderRadius.circular(22)),
            ),
          ),
        );
      },
    );
  }
}
