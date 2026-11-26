// What every conversation shares, as the web's ChatStream.tsx, ChatHead.tsx,
// ThreadParts.tsx and lib/useThread.ts are shared by a business's
// conversation, a customer's and Taskiem's: the wallpaper, the head, and
// the thread (its days, each chip staying at the top while its day is
// under it; where the unread begin; the round button over the corner; the
// view placed on opening and following what comes only at the bottom).

import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/gestures.dart' show TapGestureRecognizer;
import 'package:flutter/rendering.dart' show RenderDecoratedBox, RenderProxyBox, RenderSliver, RenderSliverMultiBoxAdaptor, ScrollCacheExtent;
import 'package:flutter/services.dart' show HapticFeedback;
import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../kit/motion.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'chats.dart' show UnreadCount;
import 'menu.dart' show MessageSelection;
import 'words.dart';

/// The chat's wallpaper under [child] (styles.css `--chat-pattern`): the
/// ground, and over it the doodles of where business meets chat
/// (assets/chat/wallpaper.svg, drawn by tools/chatwallpaper), a 400 px tile
/// repeated, its lines in [doodle]. Drawn in a layer of its own: nothing
/// drawn over it (a caret blinking, a list scrolling) draws it again.
class WallpaperBox extends StatelessWidget {
  const WallpaperBox({super.key, required this.ground, required this.doodle, required this.child});
  final Color ground;
  final Color doodle;
  final Widget child;

  /// The tile's side, as the web's `400px 400px`.
  static const tile = 400.0;

  @override
  Widget build(BuildContext context) => Stack(
    children: [
      Positioned.fill(
        child: RepaintBoundary(
          child: ColoredBox(
            color: ground,
            child: ClipRect(
              child: LayoutBuilder(
                builder: (context, box) {
                  final across = (box.maxWidth / tile).ceil();
                  final down = (box.maxHeight / tile).ceil();
                  return Stack(
                    children: [
                      for (var r = 0; r < down; r++)
                        for (var c = 0; c < across; c++)
                          Positioned(
                            left: c * tile,
                            top: r * tile,
                            width: tile,
                            height: tile,
                            child: SvgPicture.asset(
                              'assets/chat/wallpaper.svg',
                              width: tile,
                              height: tile,
                              colorFilter: ColorFilter.mode(doodle, BlendMode.srcIn),
                              excludeFromSemantics: true,
                            ),
                          ),
                    ],
                  );
                },
              ),
            ),
          ),
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

  /// A 38 px round button of the head, on the card's colour
  /// (docs/spec/mobile-app-look.md, "The chat").
  static Widget iconButton(String label, Widget icon, VoidCallback? onTap) => TapArea(
    child: Semantics(
      button: true,
      label: label,
      child: TkPress(
        onTap: onTap ?? () {},
        child: Builder(
          builder: (context) => Container(
            width: 38,
            height: 38,
            decoration: BoxDecoration(color: TkLook.of(context).card, shape: BoxShape.circle),
            child: Center(child: icon),
          ),
        ),
      ),
    ),
  );

  /// A button of the chat's own head: the icon alone, no circle, as
  /// WhatsApp's (docs/spec/mobile-app-look.md, "The chat"), 40 px to press.
  static Widget headButton(String label, Widget icon, VoidCallback? onTap) => TapArea(
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
    final look = TkLook.of(context);
    final sub = typing ? 'typing…' : subtitle;
    return Container(
      // The head on its own bar (the screen colours it), a hairline under it.
      padding: const EdgeInsets.fromLTRB(8, 6, 6, 8),
      decoration: BoxDecoration(border: Border(bottom: BorderSide(color: look.line))),
      child: Row(
        children: [
          TapArea(
            child: Semantics(
              button: true,
              label: 'Back to the chats',
              child: TkPress(
                onTap: onBack ?? () => Navigator.of(context).maybePop(),
                child: SizedBox(width: 34, height: 40, child: Center(child: TkIcon('back', size: 24, stroke: 2, color: t.text))),
              ),
            ),
          ),
          // The face and the name together, as WhatsApp's: pressed, who
          // this is (their profile, the group's info), where there is one.
          Expanded(
            child: _HeadWho(
              onTap: onAvatar,
              label: 'About $title',
              value: sub,
              child: Row(
                children: [
                  avatar,
                  const SizedBox(width: 10),
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
                            style: tkDisplay(size: 17, lineHeight: 1.25, letterSpacing: -0.3, color: t.text),
                          ),
                        ),
                        if (sub != null)
                          Text(
                            sub,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: typing
                                ? tkText(size: 12.5, weight: 600, lineHeight: 1.3, color: look.limeInk)
                                : tkText(size: 12.5, lineHeight: 1.3, color: t.muted),
                          ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
          for (final a in actions) ...[const SizedBox(width: 2), a],
          // With no menu of its own (Taskiem's), no ⋯ on a phone: the web's
          // holds only Keyboard there, which a phone hides.
          if (onMore != null) ...[
          const SizedBox(width: 2),
          Builder(
            builder: (context) => headButton(
              'More about this conversation',
              TkIcon('b-more', size: 22, stroke: 2.4, color: t.text),
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

/// The head's face and name, pressed as one: [onTap] where there is a
/// profile to open; else only shown.
class _HeadWho extends StatelessWidget {
  const _HeadWho({required this.child, required this.label, this.value, this.onTap});
  final Widget child;
  final String label;
  final String? value;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    if (onTap == null) return child;
    return TapArea(
      // One button to a screen reader: "About …", what they are doing now
      // (typing…, last seen) as its value.
      child: Semantics(
        container: true,
        button: true,
        label: label,
        value: value,
        excludeSemantics: true,
        child: GestureDetector(behavior: HitTestBehavior.opaque, onTap: onTap, child: child),
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
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(999)),
      child: Text(label, style: tkText(size: 12, weight: 600, color: t.muted)),
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
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Center(
        child: Container(
          key: markKey,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          decoration: BoxDecoration(color: TkLook.of(context).lime.withValues(alpha: 0.18), borderRadius: BorderRadius.circular(999)),
          child: Text(
            // ChatStream.tsx unreadWords.
            count == 1 ? '1 unread message' : '$count unread messages',
            textAlign: TextAlign.center,
            style: tkText(size: 12, weight: 700, color: TkLook.of(context).limeInk),
          ),
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

/// One row of the thread's list: the top (the errors, the stream's 8 px),
/// a message (with its day's chip over it when it is the day's first, and
/// the unread line over the first unread), or the end (Taskiem's cards,
/// the stream's 8 px under them).
class _Entry<T> {
  const _Entry.head() : key = 'head', m = null, prev = null, day = null, chip = null, unread = false;
  const _Entry.foot() : key = 'foot', m = null, prev = null, day = null, chip = null, unread = false;
  const _Entry.message(this.key, T this.m, this.prev, String this.day, {this.chip, this.unread = false});

  final String key;
  final T? m;

  /// The one before it on its day (null for the day's first).
  final T? prev;
  final String? day;

  /// The day's chip over it: the day's first message.
  final String? chip;

  /// The unread line over it.
  final bool unread;
}

/// ChatStream.tsx with lib/useThread.ts: the messages by day, where the
/// unread begin, what follows them (Taskiem's cards), and the scrolling.
///
/// Built as WhatsApp's is (the phone feedback of 25 November 2026: the
/// thread flickered as it opened): a list built lazily, only what is in
/// view and a little round it, anchored where it opens (the newest message
/// at the bottom, the first unread, or a message looked for) so the first
/// frame shows that place with no jump. What comes later never moves the
/// view: older pages grow above the anchor, newer messages below it, and
/// the view follows them only while the reader is at the bottom.
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
    this.arrival,
    this.selection,
  });

  /// How a message arriving while the thread is open comes in: a bubble
  /// by default.
  final TkArrival Function(T m)? arrival;

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
  /// null to draw nothing (a diary line folded into another). Asked for
  /// only as the message comes near the view.
  final Widget? Function(T m, T? prev) render;
  final DateTime now;
  final ({String id, int count})? unread;
  final Widget? empty;
  final List<String> errors;

  /// After the messages: Taskiem's briefing and alert cards.
  final Widget? tail;

  /// The messages held (WhatsApp's selection): each row tinted across the
  /// thread while held, and a tap adds or takes one away.
  final MessageSelection? selection;

  @override
  State<ThreadView<T>> createState() => ThreadViewState<T>();
}

/// How the thread opened: at the newest, the first unread, or a message.
enum _Open { end, unread, focus }

class ThreadViewState<T> extends State<ThreadView<T>> with SingleTickerProviderStateMixin {
  /// The thread's scrolling (a test scrolls it as the web half does). Its
  /// offsets run from the top of what is loaded ([ScrollPosition.minScrollExtent],
  /// below zero once there is anything above where it opened) to the
  /// bottom ([ScrollPosition.maxScrollExtent]).
  late final ScrollController scroll = _ThreadScroll(this);
  final _viewport = GlobalKey();
  final _before = GlobalKey();
  final _center = GlobalKey();
  final _foot = GlobalKey();
  final _focusKey = GlobalKey();
  final _unreadKey = GlobalKey();

  /// The list's rows, made afresh on each build (only words, no widgets).
  List<_Entry<T>> _entries = const [];
  Map<String, int> _index = const {};

  /// Where the list is anchored: the key of the first row below the anchor
  /// (the rows before it grow upwards from it), or opened at the newest the
  /// last row above it; kept as rows come and go.
  String _split = 'foot';
  int _splitAt = 0;

  /// Where the anchor sits in the view: always its top (the viewport's
  /// anchor 0: Flutter reveals a widget, ensureVisible, only rightly with
  /// it), the place where it opens given by the scroll's offset instead.
  static const double _anchor = 0;
  _Open _open = _Open.end;

  /// Placed where it opens (in the first layout with messages).
  bool _placed = false;

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

  /// The held day shows while the thread moves and fades a moment after it
  /// stops: at rest the day is in its place, as drawn
  /// (docs/spec/mobile-app-look.md, "The chat").
  final _moving = ValueNotifier<bool>(false);
  Timer? _rest;
  bool _loadingOlder = false;

  /// Messages arrived with this build (until its frame is drawn), and
  /// whether the view follows them to the bottom as it is laid out.
  bool _arrived = false;
  bool _follows = false;

  /// How tall a day chip is, as last measured.
  double _chipHeight = 26;

  /// The messages seen so far, and those that arrived with this build: one
  /// arriving while the thread is open comes in with motion (those there
  /// when the thread opened, and older pages, do not).
  final _seen = <String>{};
  final _fresh = <String>{};
  bool _seeded = false;
  DateTime? _newest;

  /// `.bubble.flash`: the ring round the message opened at, 1.8 s.
  late final AnimationController _flash = AnimationController(vsync: this, duration: const Duration(milliseconds: 1800));
  String? _flashId;

  @override
  void initState() {
    super.initState();
    PopIn.openedAt = DateTime.now();
    scroll.addListener(_onScroll);
    widget.selection?.locate = _bubbleOf;
    _newest = _newestOf(widget.items);
  }

  @override
  void didUpdateWidget(ThreadView<T> oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.selection != widget.selection) {
      if (oldWidget.selection?.locate == _bubbleOf) oldWidget.selection?.locate = null;
      widget.selection?.locate = _bubbleOf;
    }
    _follow(oldWidget.items);
  }

  @override
  void dispose() {
    if (widget.selection?.locate == _bubbleOf) widget.selection?.locate = null;
    _flash.dispose();
    _stuck.dispose();
    _rest?.cancel();
    _moving.dispose();
    _jump.dispose();
    scroll.dispose();
    super.dispose();
  }

  DateTime? _newestOf(List<T>? items) => items == null || items.isEmpty ? null : widget.of.at(items.last);

  void _flashAt(String id) {
    setState(() => _flashId = id);
    _flash.forward(from: 0).whenCompleteOrCancel(() {
      if (mounted) setState(() => _flashId = null);
    });
  }

  bool get _atBottom => !scroll.hasClients || scroll.position.maxScrollExtent - scroll.position.pixels <= _nearBottom;

  RenderBox? get _view {
    final v = _viewport.currentContext?.findRenderObject();
    return v is RenderBox && v.hasSize ? v : null;
  }

  /// useThread.ts newestEnd: how far down the content the newest message
  /// ends (the top of what follows it), in the scroll's offsets; null while
  /// that is not near the view.
  double? _newestEnd() {
    final box = _foot.currentContext?.findRenderObject();
    final view = _view;
    if (box is! RenderBox || view == null || !box.attached || !box.hasSize || !scroll.hasClients) return null;
    return box.localToGlobal(Offset.zero, ancestor: view).dy + scroll.offset;
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
    if (!scroll.hasClients) return;
    final p = scroll.position;
    final end = _newestEnd();
    final height = p.maxScrollExtent + p.viewportDimension;
    if (end == null || height - end <= _nearBottom) {
      scroll.jumpTo(p.maxScrollExtent);
    } else {
      scroll.jumpTo((end + 16 - p.viewportDimension).clamp(p.minScrollExtent, p.maxScrollExtent));
    }
  }

  /// useThread.ts: a mark at the top of the thread, watched 200 px beyond
  /// the top; in sight, the older page is asked for.
  void _maybeOlder() {
    final items = widget.items;
    if (!widget.hasOlder || widget.onOlder == null || _loadingOlder || items == null || items.isEmpty || !scroll.hasClients) return;
    // The mark sits under the stream's 8 px padding.
    if (scroll.position.pixels - scroll.position.minScrollExtent > 8 + 200) return;
    _older();
  }

  Future<void> _older() async {
    _loadingOlder = true;
    try {
      await widget.onOlder!();
    } finally {
      _loadingOlder = false;
    }
    if (!mounted) return;
    // The page lands above the anchor and grows upwards: the reader stays
    // where they were with nothing moved. Still near the top: the next.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _maybeOlder();
    });
  }

  void _onScroll() {
    if (!scroll.hasClients) return;
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
    if (stuck != null && (stuck.label != _stuck.value?.label || stuck.top != _stuck.value?.top)) {
      _moving.value = true;
      _rest?.cancel();
      _rest = Timer(const Duration(milliseconds: 1200), () => _moving.value = false);
    }
    _stuck.value = stuck;
    if (away != _away || (!away && _newCount > 0)) _jump.value = (away: away, count: away ? _newCount : 0);
  }

  /// lib/useThread.ts: afterwards the thread follows new messages only
  /// while the reader is at the bottom (in the layout itself, so nothing
  /// jumps: [_ThreadPosition]) or when they sent them, and counts the
  /// others on the round button.
  void _follow(List<T>? before) {
    final items = widget.items;
    final prev = _newest;
    _newest = _newestOf(items);
    if (items == null || !_placed || prev == null) return;
    final of = widget.of;
    final arrived = items.where((m) => of.at(m).isAfter(prev)).toList();
    if (arrived.isEmpty) return;
    final stick = _atNewest;
    final mine = arrived.any(of.mine);
    // With nothing after the messages, one arriving while the reader is at
    // the newest (or one they sent) is followed in the layout itself, in
    // the frame it is drawn ([_wanted]); with Taskiem's cards after them,
    // the newest is brought into sight here instead, the cards below it.
    _follows = (stick || mine) && widget.tail == null;
    _arrived = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final followed = _follows;
      _arrived = false;
      _follows = false;
      if (!mounted || !scroll.hasClients) return;
      if (stick || mine) {
        // The newest message in view, not what follows it (Taskiem's
        // cards): an answer is never left out of sight.
        if (!followed) _toNewest();
        _newCount = 0;
      } else {
        _newCount += arrived.where((m) => !of.mine(m)).length;
      }
      _onScroll();
    });
  }

  /// The rows of the list built now, each with the index of its row.
  Iterable<(int, RenderBox)> _shown() sync* {
    for (final (key, below) in [(_before, false), (_center, true)]) {
      final s = key.currentContext?.findRenderObject();
      if (s is! RenderSliverMultiBoxAdaptor) continue;
      for (var c = s.firstChild; c != null; c = s.childAfter(c)) {
        final i = s.indexOf(c);
        final at = below ? _splitAt + i : _splitAt - 1 - i;
        if (at >= 0 && at < _entries.length) yield (at, c);
      }
    }
  }

  /// CSS `position: sticky` on `.chat-day` (top 6 px in its day's group):
  /// the chip of the day under the top stays there until its day's end
  /// pushes it up.
  ({String label, double top})? _sticky() {
    final view = _view;
    if (view == null) return null;
    // The days' chips built now, where they are, and the row at the top.
    final chips = <({String label, double top, double row})>[];
    String? day;
    for (final (i, box) in _shown()) {
      final e = _entries[i];
      if (!box.hasSize || !box.attached) continue;
      final top = box.localToGlobal(Offset.zero, ancestor: view).dy;
      if (e.day != null && top <= _stickTop && top + box.size.height > _stickTop) day = e.day;
      if (e.chip != null) {
        final chip = _chipIn(box);
        if (chip != null) {
          _chipHeight = chip.size.height;
          chips.add((label: e.chip!, top: chip.localToGlobal(Offset.zero, ancestor: view).dy, row: top));
        }
      }
    }
    if (day == null) return null;
    final own = chips.where((c) => c.label == day).firstOrNull;
    if (own != null && own.top >= _stickTop) return null;
    // Pushed up by the next day's row.
    final next = chips.where((c) => c.label != day && c.row > _stickTop).fold<double?>(null, (a, c) => a == null || c.row < a ? c.row : a);
    final top = next == null || _stickTop < next - 6 - _chipHeight ? _stickTop : next - 6 - _chipHeight;
    if (own != null && top <= own.top + 0.01) return null;
    return (label: day, top: top);
  }

  /// The day chip drawn in a row.
  RenderBox? _chipIn(RenderObject row) {
    RenderBox? found;
    void look(RenderObject o) {
      if (found != null) return;
      if (o is _RenderDayChipMark) {
        found = o;
        return;
      }
      o.visitChildren(look);
    }

    look(row);
    return found;
  }

  /// A held message's bubble on the screen, while its row is built.
  Rect? _bubbleOf(String id, RenderObject? ancestor) {
    final at = _index['m:$id'];
    if (at == null) return null;
    for (final (i, box) in _shown()) {
      if (i != at) continue;
      if (!box.attached || !box.hasSize) return null;
      return heldBubble(box, ancestor: ancestor);
    }
    return null;
  }

  void _toBottom() {
    scroll.jumpTo(scroll.position.maxScrollExtent);
    _newCount = 0;
  }

  /// The rows, by day, as ChatStream.tsx groups them.
  void _list() {
    final items = widget.items;
    final of = widget.of;
    final out = <_Entry<T>>[const _Entry.head()];
    if (items != null) {
      String? day;
      T? prev;
      for (final m in items) {
        final label = dayLabel(of.at(m), widget.now);
        final first = label != day;
        if (first) {
          day = label;
          prev = null;
        }
        final id = of.id(m);
        out.add(_Entry.message('m:$id', m, prev, label, chip: first ? label : null, unread: widget.unread?.id == id));
        prev = m;
      }
    }
    out.add(const _Entry.foot());
    _entries = out;
    _index = {for (var i = 0; i < out.length; i++) out[i].key: i};
  }

  /// Where the anchor is now: where it was, or (that row gone) the first
  /// message after where it was.
  void _anchorAt() {
    final items = widget.items;
    if (!_placed && items != null) {
      // Placed now, in the first layout with messages: at the message
      // looked for, the first unread, or the newest.
      final focus = widget.focus;
      final unread = widget.unread?.id;
      if (focus != null && _index.containsKey('m:$focus')) {
        _open = _Open.focus;
        _split = 'm:$focus';
      } else if (unread != null && _index.containsKey('m:$unread')) {
        _open = _Open.unread;
        _split = 'm:$unread';
      } else {
        _open = _Open.end;
        // Under the newest there is now: what comes later lands below it.
        _split = _entries[_entries.length - 2].key;
      }
    }
    final at = _index[_split];
    final above = _open == _Open.end;
    if (at != null) {
      _splitAt = above ? at + 1 : at;
      return;
    }
    // The anchor's message gone (sent under its own id, deleted): the
    // row now where it was.
    final i = _splitAt.clamp(1, _entries.length - 1);
    _splitAt = i;
    _split = _entries[above ? i - 1 : i].key;
  }

  /// [_ThreadPosition]: what the scroll may reach, from the two halves'
  /// lengths: from the top of the first row to the end of the last, never
  /// past them; a thread shorter than the view stands at its top, as the
  /// web's does.
  (double, double) _extents(double view, double min, double max) {
    final b = _before.currentContext?.findRenderObject();
    final a = _center.currentContext?.findRenderObject();
    if (b is! RenderSliver || a is! RenderSliver || b.geometry == null || a.geometry == null) return (min, max);
    final lo = _anchor * view - b.geometry!.scrollExtent;
    final hi = a.geometry!.scrollExtent + _anchor * view - view;
    return (lo, hi < lo ? lo : hi);
  }

  /// [_ThreadPosition]: where the view must be now, in the layout, given
  /// the ends it is about to take: where it opens, the first time; the
  /// bottom while the reader is held there.
  double? _wanted(double min, double max, {required double view, required bool dragging, required bool grew}) {
    if (!_placed) {
      if (widget.items == null) return null;
      _placed = true;
      double want = 0;
      final row = _center.currentContext?.findRenderObject();
      final first = row is RenderSliverMultiBoxAdaptor ? row.firstChild : null;
      // Where [key]'s box is in the anchor's row (the first below it).
      double? within(GlobalKey key, double Function(RenderBox) at) {
        final box = key.currentContext?.findRenderObject();
        if (box is! RenderBox || !box.hasSize || first == null) return null;
        return box.localToGlobal(Offset(0, at(box)), ancestor: first).dy;
      }

      switch (_open) {
        case _Open.end:
          // The end of the thread at the bottom of the view (the newest
          // message, or Taskiem's cards after it); kept there as what is
          // below arrives.
          want = max;
          _keep = true;
          _atEnd = true;
        case _Open.unread:
          // The unread line 40 px under the top (clear of the day chip).
          want = (within(_unreadKey, (_) => 0) ?? 8) - 40;
        case _Open.focus:
          // scrollIntoView({ block: "center" }): the message's middle at
          // the view's.
          want = (within(_focusKey, (b) => ((b as _RenderMeasured).measured?.height ?? 0) / 2) ?? 0) - view / 2;
      }
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        if (_open == _Open.focus) _flashAt(widget.focus!);
        _onScroll();
        // Opened at the end: kept there as what is below arrives.
        _keep = _open == _Open.end;
      });
      return want.clamp(min, max);
    }
    // What is below grew (a message, a picture loaded, the typing dots);
    // a message with Taskiem's cards after it is brought into sight by
    // [_follow] instead, the cards left below it.
    if (grew && !dragging && (_follows || (_keep && _atEnd && !(_arrived && widget.tail != null)))) return max;
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final items = widget.items;
    final of = widget.of;
    if (items != null) {
      // Arrived since the last build, newer than what was there.
      final newest = _lastNewest;
      for (final m in items) {
        if (_seen.add(of.id(m)) && _seeded && newest != null && !of.at(m).isBefore(newest)) _fresh.add(of.id(m));
      }
      _seeded = true;
      // Only as it arrives: one built later (scrolled to) is simply there.
      if (_fresh.isNotEmpty) WidgetsBinding.instance.addPostFrameCallback((_) => _fresh.clear());
    }
    _lastNewest = _newestOf(items);
    _list();
    _anchorAt();
    final sel = widget.selection;
    sel?.order = [for (final e in _entries) if (e.m != null) of.id(e.m as T)];
    final picked = TkLook.of(context).chatPicked;

    Widget row(int i) {
      final e = _entries[i];
      final key = ValueKey(e.key);
      if (e.key == 'head') {
        return KeyedSubtree(
          key: key,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(10, 8, 10, 0),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (final e in widget.errors) TkErrorBox(e),
                if (items != null && items.isEmpty && widget.empty != null)
                  Center(child: Padding(padding: const EdgeInsets.symmetric(vertical: 12), child: widget.empty)),
                // `.chat-top { height: 1px }` while there are older ones,
                // in the stream's 2 px gaps.
                if (widget.hasOlder && items != null && items.isNotEmpty) const SizedBox(height: 3),
              ],
            ),
          ),
        );
      }
      if (e.key == 'foot') {
        return KeyedSubtree(
          key: key,
          child: KeyedSubtree(
          key: _foot,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (items != null && widget.tail != null) ...[if (items.isNotEmpty) const SizedBox(height: 2), widget.tail!],
              ],
            ),
          ),
          ),
        );
      }
      final m = e.m as T;
      final id = of.id(m);
      final w = widget.render(m, e.prev);
      final label = e.chip;
      final first = i == 1;
      Widget? body;
      if (w != null) {
        body = MessageArrive(
          key: ValueKey('arrive $id'),
          animate: _fresh.remove(id),
          mine: of.mine(m),
          kind: widget.arrival?.call(m) ?? TkArrival.bubble,
          // Each message drawn once and kept: scrolling moves the pictures.
          child: RepaintBoundary(child: widget.focus == id ? _Measured(key: _focusKey, child: w) : w),
        );
        body = Padding(padding: const EdgeInsets.fromLTRB(10, 2, 10, 0), child: body);
        if (sel != null) body = _PickRow(selection: sel, id: id, tint: picked, child: body);
      }
      return KeyedSubtree(
        key: key,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (label != null)
              Padding(
                padding: EdgeInsets.fromLTRB(10, first ? 0 : 2, 10, 0),
                child: Center(
                  child: ListenableBuilder(
                    listenable: Listenable.merge([_stuck, _moving]),
                    builder: (context, chip) => Opacity(opacity: _stuck.value?.label == label && _moving.value ? 0 : 1, child: chip),
                    child: _DayChipMark(child: DayChip(label)),
                  ),
                ),
              ),
            if (e.unread) Padding(padding: const EdgeInsets.fromLTRB(10, 2, 10, 0), child: UnreadMark(widget.unread!.count, markKey: _unreadKey)),
            ?body,
          ],
        ),
      );
    }

    int? find(Key key, bool below) {
      if (key is! ValueKey<String>) return null;
      final at = _index[key.value];
      if (at == null) return null;
      if (below) return at >= _splitAt ? at - _splitAt : null;
      return at < _splitAt ? _splitAt - 1 - at : null;
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
              // What is in it changed size (a message, a card, a picture
              // loaded): the round button and the day chip told again,
              // once it is laid out.
              child: NotificationListener<ScrollMetricsNotification>(
                onNotification: (_) {
                  _onScroll();
                  return false;
                },
                child: CustomScrollView(
                controller: scroll,
                center: _center,
                // What is in view and a screen's height round it.
                scrollCacheExtent: const ScrollCacheExtent.viewport(1),
                slivers: [
                  SliverList(
                    key: _before,
                    delegate: SliverChildBuilderDelegate(
                      (context, i) => row(_splitAt - 1 - i),
                      childCount: _splitAt,
                      findChildIndexCallback: (k) => find(k, false),
                      addRepaintBoundaries: false,
                      addSemanticIndexes: false,
                    ),
                  ),
                  SliverList(
                    key: _center,
                    delegate: SliverChildBuilderDelegate(
                      (context, i) => row(_splitAt + i),
                      childCount: _entries.length - _splitAt,
                      findChildIndexCallback: (k) => find(k, true),
                      addRepaintBoundaries: false,
                      addSemanticIndexes: false,
                    ),
                  ),
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
                        Positioned(
                          top: stuck.top,
                          left: 0,
                          right: 0,
                          child: Center(
                            child: ValueListenableBuilder(
                              valueListenable: _moving,
                              builder: (context, moving, chip) => AnimatedOpacity(opacity: moving ? 1 : 0, duration: const Duration(milliseconds: 200), child: chip),
                              child: DayChip(stuck.label, margin: false),
                            ),
                          ),
                        ),
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

  DateTime? _lastNewest;
}

/// The thread's scrolling, with [_ThreadPosition].
class _ThreadScroll extends ScrollController {
  _ThreadScroll(this._thread);
  final ThreadViewState<dynamic> _thread;

  @override
  ScrollPosition createScrollPosition(ScrollPhysics physics, ScrollContext context, ScrollPosition? oldPosition) =>
      _ThreadPosition(_thread, physics: physics, context: context, oldPosition: oldPosition);
}

/// The thread's place, settled in the layout itself (so a frame never
/// shows it elsewhere first): where it opens, held at the bottom while the
/// reader is there, and its ends exactly those of what is loaded.
class _ThreadPosition extends ScrollPositionWithSingleContext {
  _ThreadPosition(this._thread, {required super.physics, required super.context, super.oldPosition});
  final ThreadViewState<dynamic> _thread;

  /// The bottom as last laid out.
  double? _lastEnd;

  /// A finger on it: not fought.
  bool get _dragging => activity is DragScrollActivity;

  @override
  bool applyContentDimensions(double minScrollExtent, double maxScrollExtent) {
    final (lo, hi) = _thread._extents(viewportDimension, minScrollExtent, maxScrollExtent);
    final last = _lastEnd;
    _lastEnd = hi;
    var want = _thread._wanted(lo, hi, view: viewportDimension, dragging: _dragging, grew: last != null && (hi - last).abs() > 0.5);
    // Its ends moved past it (a page gone): back within them, at once
    // rather than sprung back.
    if (want == null && !_dragging && (pixels < lo - 0.5 || pixels > hi + 0.5)) want = pixels.clamp(lo, hi);
    var moved = false;
    if (want != null && (want - pixels).abs() > 0.5) {
      correctPixels(want);
      moved = true;
    }
    final ok = super.applyContentDimensions(lo, hi);
    return ok && !moved;
  }
}

/// A box that keeps its size as laid out, to be read in the same layout
/// (the message a thread opens at, placed before the first frame).
class _Measured extends SingleChildRenderObjectWidget {
  const _Measured({super.key, required super.child});

  @override
  RenderObject createRenderObject(BuildContext context) => _RenderMeasured();
}

class _RenderMeasured extends RenderProxyBox {
  Size? measured;

  @override
  void performLayout() {
    super.performLayout();
    measured = size;
  }
}

/// Marks where a row's day chip is drawn (for the chip held at the top).
class _DayChipMark extends SingleChildRenderObjectWidget {
  const _DayChipMark({required super.child});

  @override
  RenderObject createRenderObject(BuildContext context) => _RenderDayChipMark();
}

class _RenderDayChipMark extends RenderProxyBox {}

/// A message's row while messages may be held: tinted across the thread
/// when held; while any are, a tap on it adds or takes it away (what is in
/// the bubble is not pressed meanwhile).
class _PickRow extends StatelessWidget {
  const _PickRow({required this.selection, required this.id, required this.tint, required this.child});
  final MessageSelection selection;
  final String id;
  final Color tint;
  final Widget child;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: selection,
    builder: (context, child) {
      final picking = selection.on;
      final on = picking && selection.has(id);
      void flip() => selection.toggle(id);
      return Semantics(
        selected: picking ? on : null,
        // While selecting, the row is pressed to add it or take it away.
        label: picking ? (on ? 'Selected. Tap to take it away' : 'Tap to add it') : null,
        onTap: picking ? flip : null,
        child: GestureDetector(
          behavior: picking ? HitTestBehavior.opaque : HitTestBehavior.deferToChild,
          excludeFromSemantics: true,
          onTap: picking ? flip : null,
          onLongPress: picking ? flip : null,
          child: ColoredBox(
            color: on ? tint : const Color(0x00000000),
            child: AbsorbPointer(absorbing: picking, child: child),
          ),
        ),
      );
    },
    child: child,
  );
}

/// A message arriving (chat.css): a bubble slides in from its own side
/// (`tkd-in-l` 0.4 s for theirs, `tkd-in-r` 0.45 s with a little spring for
/// one's own), a card arrives with a bounce (`tkd-bounce`), a diary line
/// lands itself ([TkLand], told it is fresh). Drawn as it is when it was
/// already there, or when the phone asks for less motion.
class MessageArrive extends StatelessWidget {
  const MessageArrive({super.key, required this.child, required this.animate, required this.mine, this.kind = TkArrival.bubble});
  final Widget child;
  final bool animate;
  final bool mine;
  final TkArrival kind;

  @override
  Widget build(BuildContext context) {
    final w = TkFresh(fresh: animate, child: child);
    if (!animate || kind == TkArrival.diary) return w;
    if (kind == TkArrival.card) return TkIn.card(child: w);
    return TkIn(
      duration: Duration(milliseconds: mine ? 450 : 400),
      curve: mine ? const Cubic(0.2, 0.9, 0.25, 1.12) : Curves.ease,
      dx: mine ? 14 : -14,
      scale: 0.98,
      alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
      child: w,
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
      // styles.css `.reaction-chip { animation: chip-pop 0.22s ease-out }`.
      _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 220))..forward();
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
    // chip-pop: 0.6, 1.15 at 60%, then 1, along ease-out.
    return AnimatedBuilder(
      animation: a,
      builder: (context, child) {
        final v = Curves.easeOut.transform(a.value);
        final scale = v < 0.6 ? 0.6 + (1.15 - 0.6) * v / 0.6 : 1.15 - 0.15 * (v - 0.6) / 0.4;
        return Transform.scale(scale: scale, child: child);
      },
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

/// "Edited" before a message's time, once its sender changed its words
/// (chat-previews-and-editing.md §3): in the stamp's own colour.
List<Widget> editedMark(DateTime? at, Color meta) => at == null
    ? const []
    : [
        Text('Edited', style: tkText(size: 11, lineHeight: 16 / 11, color: meta).copyWith(fontStyle: FontStyle.italic)),
        const SizedBox(width: 4),
      ];

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
  const BubbleFrame({super.key, required this.mine, required this.first, required this.stamp, required this.children, this.inline = true, this.last});

  final bool mine;
  final bool first;

  /// In the app's own chat look (docs/spec/mobile-app-look.md, "The chat"):
  /// 20 px round, the run's last bubble with its corner on the sender's
  /// side tucked in, in the web chat's (WhatsApp's) colours. Null: as the
  /// web's.
  final bool? last;
  final Widget stamp;
  final List<Widget> children;
  final bool inline;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final own = last != null;
    final radius = own
        ? BorderRadius.only(
            topLeft: const Radius.circular(20),
            topRight: const Radius.circular(20),
            bottomLeft: Radius.circular(last! && !mine ? 6 : 20),
            bottomRight: Radius.circular(last! && mine ? 6 : 20),
          )
        : BorderRadius.only(
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
        // It hangs from the button's right edge, so it is never wider than
        // the room left of that edge (16 px kept clear of the screen's own
        // edge); a long item wraps rather than running off the screen.
        final room = (at.right - 16).clamp(120.0, width - 32);
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
                  constraints: BoxConstraints(minWidth: math.min(200.0, room), maxWidth: room),
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
    final r = context.findRenderObject()!;
    return heldBubble(r) ?? ((r as RenderBox).localToGlobal(Offset.zero) & r.size);
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
    return _HoldMark(
      child: Semantics(
        container: true,
        explicitChildNodes: true,
        label: widget.label,
        onLongPressHint: 'Select',
        onLongPress: () => widget.onHold!(_rect()),
        child: gestures,
      ),
    );
  }
}

/// Marks a bubble that may be held, for [heldBubble].
class _HoldMark extends SingleChildRenderObjectWidget {
  const _HoldMark({required super.child});

  @override
  RenderObject createRenderObject(BuildContext context) => _RenderHoldMark();
}

class _RenderHoldMark extends RenderProxyBox {}

/// The bubble that may be held within [row] (a message's row, or the
/// [BubbleHold] itself), on the screen: the largest box drawn with a
/// decoration under the mark.
Rect? heldBubble(RenderObject row, {RenderObject? ancestor}) {
  RenderObject? mark;
  void findMark(RenderObject o) {
    if (mark != null) return;
    if (o is _RenderHoldMark) {
      mark = o;
      return;
    }
    o.visitChildren(findMark);
  }

  findMark(row);
  // The outermost decorated boxes; the largest is the bubble (not a
  // sender's face beside it).
  RenderBox? box;
  void findBox(RenderObject o) {
    if (o is RenderDecoratedBox && o.hasSize) {
      final b = box;
      if (b == null || o.size.width * o.size.height > b.size.width * b.size.height) box = o;
      return;
    }
    o.visitChildren(findBox);
  }

  if (mark == null) return null;
  findBox(mark!);
  final b = box ?? mark as RenderBox;
  if (!b.attached || !b.hasSize) return null;
  return b.localToGlobal(Offset.zero, ancestor: ancestor) & b.size;
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

/// The other side typing: three dots in their bubble, at the thread's end
/// (docs/spec/mobile-app-look.md, "The chat"); still when the phone asks
/// for less motion.
class TypingBubble extends StatefulWidget {
  const TypingBubble({super.key});

  @override
  State<TypingBubble> createState() => _TypingBubbleState();
}

class _TypingBubbleState extends State<TypingBubble> with SingleTickerProviderStateMixin {
  late final _beat = AnimationController(vsync: this, duration: const Duration(milliseconds: 1200));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    if (still) {
      _beat.stop();
    } else if (!_beat.isAnimating) {
      _beat.repeat();
    }
  }

  @override
  void dispose() {
    _beat.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      liveRegion: true,
      label: 'Typing',
      excludeSemantics: true,
      child: Align(
        alignment: Alignment.centerLeft,
        child: Container(
          margin: const EdgeInsets.only(top: 6),
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 13),
          decoration: BoxDecoration(
            color: t.chatIn,
            borderRadius: const BorderRadius.only(
              topLeft: Radius.circular(20),
              topRight: Radius.circular(20),
              bottomRight: Radius.circular(20),
              bottomLeft: Radius.circular(6),
            ),
          ),
          child: AnimatedBuilder(
            animation: _beat,
            builder: (context, _) => Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                for (var i = 0; i < 3; i++) ...[
                  if (i > 0) const SizedBox(width: 4),
                  Opacity(
                    opacity: 0.35 + 0.5 * (1 - ((_beat.value * 3 - i) % 3).clamp(0.0, 1.0)),
                    child: Container(width: 7, height: 7, decoration: BoxDecoration(color: t.muted, shape: BoxShape.circle)),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
