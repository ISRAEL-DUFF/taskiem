// A conversation with a linked business: the web's ConversationPane
// (web/src/pages/Conversation.tsx, ChatHead.tsx, ChatStream.tsx,
// BubbleShell.tsx, ChatComposer.tsx), drawn the same at a phone's width:
// the head, the thread on the chat's wallpaper with its days, where the
// unread begin and the bubbles, and the box to write in.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/session.dart' show uuid4;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'cards.dart';
import 'chats.dart';
import 'composer.dart';
import 'live.dart';
import 'words.dart';

/// Conversation.tsx PAGE: a page of messages.
const _page = 50;

/// Conversation.tsx EVERY_MS: how often what is newer is asked for.
const _every = Duration(seconds: 3);

/// A message of ours on its way (lib/outbox.ts): shown at once with a clock,
/// then the server's copy takes its place; if it cannot be sent it shows as
/// not sent, with Retry and Delete. Its client id makes a retry the same
/// message to the server, never a second one.
class Outgoing {
  Outgoing(this.clientId, this.body, this.at, {this.file});
  final String clientId;
  final String body;
  final DateTime at;

  /// A photo, PDF or voice note going with it (the body its caption).
  final PickedFile? file;
  bool failed = false;
  String? error;
}

/// A message of the thread: the server's, or one of ours not yet sent.
class ThreadMessage {
  ThreadMessage(this.m, {this.out});
  final LinkMessage m;
  final Outgoing? out;
  String get id => m.id;
}

/// What a conversation shows, loaded and followed as the web's pane does.
class ConversationData extends ChangeNotifier {
  ConversationData(this.chats, this.conv, {this.follow = true});

  final ChatsData chats;
  final LinkConversation conv;

  /// Asks for what is newer every few seconds (off in tests).
  final bool follow;

  TaskiemApi get api => chats.api;

  /// Undefined while loading; oldest first.
  List<LinkMessage>? messages;
  final List<Outgoing> outbox = [];
  ({String id, int count})? unread;
  bool typing = false;
  bool hasOlder = false;
  String? error;
  Timer? _timer;
  Timer? _ticks;
  Timer? _readSoon;
  StreamSubscription<LiveEvent>? _liveSub;
  bool _disposed = false;

  ChatLive? get _live => chats.live;
  bool get _connected => _live?.connected == true;

  /// Typing: from the stream while connected, else as the messages said.
  bool get typingNow => _connected ? _live!.typing.contains(conv.link) : typing;

  /// The link's role, as the head says it (links.ts headLine).
  String? get headLine {
    for (final l in chats.links) {
      if (l.id == conv.link) {
        return switch (l.role) {
          'supplier' => 'Your supplier · linked',
          'customer' => 'Buys from you · linked',
          _ => 'Supplies you and buys from you · linked',
        };
      }
    }
    return null;
  }

  List<ThreadMessage> get thread => [
    for (final m in messages ?? const <LinkMessage>[]) ThreadMessage(m),
    for (final o in outbox)
      ThreadMessage(
        LinkMessage(
          id: 'local-${o.clientId}',
          link: conv.link,
          mine: true,
          kind: 'text',
          body: o.body.isEmpty ? null : o.body,
          attachment: o.file?.attachment,
          createdAt: o.at,
        ),
        out: o,
      ),
  ];

  /// Opens at the newest page, the first unread marked (lib/thread.ts firstUnread).
  Future<void> open() async {
    try {
      final out = await api.linkMessages(link: conv.link);
      typing = out.typing ?? false;
      hasOlder = out.messages.length >= _page;
      final unseen = out.messages.where((m) => !m.mine && m.readAt == null).toList();
      unread = unseen.isEmpty ? null : (id: unseen.first.id, count: unseen.length);
      messages = out.messages;
      _changed();
      await _markRead(out.messages);
    } on ApiError catch (e) {
      error = e.message;
      _changed();
    } on Offline {
      error = 'You’re offline. The conversation shows when you’re back online.';
      _changed();
    }
    if (!follow) return;
    _liveSub ??= _live?.events.listen(_onLive);
    _live?.addListener(_reschedule);
    _reschedule();
  }

  bool _wasConnected = false;

  /// Conversation.tsx: what is newer every 3 seconds, the newest page again
  /// every 9 (our ticks, their reactions); while the stream is connected
  /// only every 15 and 30, to reconcile.
  void _reschedule() {
    if (_timer != null && _wasConnected == _connected) {
      _changed();
      return;
    }
    _wasConnected = _connected;
    _timer?.cancel();
    _ticks?.cancel();
    _timer = Timer.periodic(_connected ? _every * 5 : _every, (_) => pull().catchError((Object _) {}));
    _ticks = Timer.periodic(_connected ? _every * 10 : _every * 3, (_) => refresh().catchError((Object _) {}));
    _changed();
  }

  void _onLive(LiveEvent e) {
    if (e is LiveMessage && e.message.link == conv.link) {
      final m = e.message;
      _merge([m]);
      // Ours, sent from here, told by the stream before the send's answer
      // came: the outbox's copy gives way to it.
      if (m.mine) {
        final i = outbox.indexWhere((o) => !o.failed && o.file == null && m.attachment == null && o.body == (m.body ?? '').trim());
        if (i >= 0) outbox.removeAt(i);
      }
      _changed();
      if (!m.mine && m.readAt == null) {
        // Read once for a burst of them.
        _readSoon?.cancel();
        _readSoon = Timer(const Duration(milliseconds: 400), () => _markRead([m]));
      }
    } else if (e is LiveGone && e.link == conv.link) {
      messages = messages?.where((x) => x.id != e.id).toList();
      _changed();
    }
  }

  /// The newest page again: read marks and reactions change in place.
  Future<void> refresh() async {
    final out = await api.linkMessages(link: conv.link);
    if (_disposed) return;
    _merge(out.messages);
    _changed();
  }

  /// What is newer than the newest we have; read while seen.
  Future<void> pull() async {
    final last = messages?.isNotEmpty == true ? messages!.last.createdAt : null;
    final out = await api.linkMessages(link: conv.link, after: last?.toUtc().toIso8601String());
    if (_disposed) return;
    final was = typing;
    typing = out.typing ?? false;
    if (out.messages.isEmpty) {
      if (was != typing) _changed();
      return;
    }
    _merge(out.messages);
    _changed();
    await _markRead(out.messages);
    if (out.messages.length >= _page) await pull();
  }

  void _merge(List<LinkMessage> add) {
    final byId = {for (final m in messages ?? const <LinkMessage>[]) m.id: m};
    for (final m in add) {
      byId[m.id] = m;
    }
    messages = byId.values.toList()..sort((a, b) => a.createdAt.compareTo(b.createdAt));
  }

  Future<void> _markRead(List<LinkMessage> list) async {
    if (!list.any((m) => !m.mine && m.readAt == null)) return;
    try {
      await api.readLinkChat(link: conv.link);
      unawaited(chats.load());
    } catch (_) {
      // Read next time.
    }
  }

  /// Sent at once: it shows with a clock, then the server's copy takes its place.
  /// Sent at once with a clock; a file with its caption.
  Future<void> send(String text, {PickedFile? file}) async {
    final body = text.trim();
    if (body.isEmpty && file == null) return;
    final o = Outgoing(uuid4(), body, chats.now(), file: file);
    outbox.add(o);
    _changed();
    await _send(o);
  }

  Future<void> retry(Outgoing o) async {
    o.failed = false;
    o.error = null;
    _changed();
    await _send(o);
  }

  void discard(Outgoing o) {
    outbox.remove(o);
    _changed();
  }

  Future<void> _send(Outgoing o) async {
    try {
      final f = o.file;
      final LinkMessage sent;
      if (f != null) {
        // POST /v1/link-chats/{link}/files: the file, its caption, the client id.
        final j = await chats.auth.transport.upload(
          '/v1/link-chats/${Uri.encodeComponent(conv.link)}/files',
          bytes: f.bytes,
          filename: f.name,
          type: f.type,
          fields: {'client_id': o.clientId, if (o.body.isNotEmpty) 'text': o.body},
        );
        sent = LinkMessage.fromJson((j as Map<String, dynamic>)['message'] as Map<String, dynamic>);
      } else {
        sent = (await api.sendLinkMessage(
          link: conv.link,
          body: SendLinkMessageRequest(text: o.body, clientId: o.clientId),
        )).message;
      }
      outbox.remove(o);
      _merge([sent]);
      // Our own copy of what we sent: shown without fetching it back.
      if (f != null) chats.files.keep(sent.id, f.bytes);
      unawaited(chats.load());
    } on ApiError catch (e) {
      o.failed = true;
      // lib/outbox.ts sendError.
      o.error = e.status == 429 ? 'You are sending too fast. Wait a moment and tap Retry.' : (e.message.isEmpty ? 'It could not be sent.' : e.message);
    } on Offline {
      o.failed = true;
      o.error = 'There is no connection. Tap Retry when you are back online.';
    }
    _changed();
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _ticks?.cancel();
    _readSoon?.cancel();
    _liveSub?.cancel();
    _live?.removeListener(_reschedule);
    super.dispose();
  }
}

/// The conversation's screen.
class ConversationScreen extends StatefulWidget {
  const ConversationScreen({super.key, required this.data, this.onBack, this.onOpenDoc, this.pickers = const Pickers(), this.recorder});

  final ConversationData data;
  final VoidCallback? onBack;

  /// A card's document opened (View order, View invoice, Pay).
  final void Function(LinkDocCard card)? onOpenDoc;

  /// Where photos and PDFs come from, and the microphone (stand-ins in tests).
  final Pickers pickers;
  final Recorder? recorder;

  @override
  State<ConversationScreen> createState() => _ConversationScreenState();
}

/// lib/thread.ts NEAR_BOTTOM: how close to the bottom still counts as there.
const _nearBottom = 80.0;

/// Where a stuck day chip sits under the thread's top: the stream's 8 px
/// and the chip's `top: 6px`.
const _stickTop = 14.0;

class _ConversationScreenState extends State<ConversationScreen> {
  final _scroll = ScrollController();
  final _text = TextEditingController();
  final _viewport = GlobalKey();

  /// The box to write in (a test hands it a file, as a picker would).
  final composerKey = GlobalKey<ComposerState>();
  final _unreadKey = GlobalKey();
  final _chipKeys = <String, GlobalKey>{};
  final _groupKeys = <String, GlobalKey>{};
  late final CardActs _acts = CardActs(auth: widget.data.chats.auth, onDone: () => widget.data.pull(), onOpen: widget.onOpenDoc);
  bool _placed = false;
  DateTime? _newest;
  bool _away = false;
  int _newCount = 0;
  ({String label, double top})? _stuck;

  @override
  void initState() {
    super.initState();
    widget.data.addListener(_follow);
    _scroll.addListener(_onScroll);
    if (widget.data.messages == null) {
      widget.data.open();
    } else {
      _follow();
    }
    _text.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    widget.data.removeListener(_follow);
    _scroll.dispose();
    _text.dispose();
    super.dispose();
  }

  bool get _atBottom => !_scroll.hasClients || _scroll.position.maxScrollExtent - _scroll.position.pixels <= _nearBottom;

  void _onScroll() {
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
  /// (clear of the day chip), or at the bottom; afterwards the thread
  /// follows new messages only while the reader is at the bottom or when
  /// they sent them, and counts the others on the round button.
  void _follow() {
    final items = widget.data.thread;
    if (widget.data.messages == null) return;
    final newest = items.isEmpty ? null : items.last.m.createdAt;
    final stick = _atBottom;
    final prev = _newest;
    _newest = newest;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_scroll.hasClients) return;
      if (!_placed) {
        _placed = true;
        final at = _unreadKey.currentContext?.findRenderObject();
        final view = _viewport.currentContext?.findRenderObject();
        if (at is RenderBox && view is RenderBox) {
          final top = at.localToGlobal(Offset.zero, ancestor: view).dy;
          _scroll.jumpTo((_scroll.offset + top - 40).clamp(0.0, _scroll.position.maxScrollExtent));
        } else {
          _scroll.jumpTo(_scroll.position.maxScrollExtent);
        }
        _onScroll();
        return;
      }
      if (prev == null) return;
      final arrived = items.where((m) => m.m.createdAt.isAfter(prev)).toList();
      if (arrived.isEmpty) return;
      if (stick || arrived.any((m) => m.m.mine)) {
        _scroll.jumpTo(_scroll.position.maxScrollExtent);
        setState(() => _newCount = 0);
      } else {
        setState(() => _newCount += arrived.where((m) => !m.m.mine).length);
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
    _scroll.jumpTo(_scroll.position.maxScrollExtent);
    setState(() => _newCount = 0);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    final d = widget.data;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: Semantics(
        label: 'Conversation with ${d.conv.name}',
        container: true,
        child: CustomPaint(
          // `.chat-mode .chat-pane { background: var(--chat-pattern), var(--chat-wallpaper) }`
          painter: _Wallpaper(t.chatWallpaper, t.chatDoodle),
          child: Column(
            children: [
              ColoredBox(
                color: t.panel,
                child: Padding(
                  padding: EdgeInsets.only(top: pad.top),
                  child: _ConversationHead(data: d, onBack: widget.onBack),
                ),
              ),
              Expanded(
                child: ListenableBuilder(
                  listenable: d,
                  builder: (context, _) => Stack(
                    children: [
                      Positioned.fill(
                        child: KeyedSubtree(
                          key: _viewport,
                          child: _Stream(
                            data: d,
                            scroll: _scroll,
                            unreadKey: _unreadKey,
                            chipKeys: _chipKeys,
                            groupKeys: _groupKeys,
                            stuck: _stuck?.label,
                            acts: _acts,
                            changed: () => setState(() {}),
                          ),
                        ),
                      ),
                      if (_stuck != null)
                        Positioned(
                          top: _stuck!.top,
                          left: 0,
                          right: 0,
                          child: IgnorePointer(child: Center(child: _DayChip(_stuck!.label, margin: false))),
                        ),
                      if (_away)
                        Positioned(
                          right: 16,
                          bottom: 16,
                          child: _Jump(count: _newCount, onTap: _toBottom),
                        ),
                    ],
                  ),
                ),
              ),
              Composer(
                key: composerKey,
                text: _text,
                onSend: (words, file) => d.send(words, file: file),
                bottom: pad.bottom,
                pickers: widget.pickers,
                recorder: widget.recorder,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// ThreadParts.tsx ThreadJump (`.chat-jump`): the round button over the
/// thread's corner, with the number that came while the reader was up.
class _Jump extends StatelessWidget {
  const _Jump({required this.count, required this.onTap});
  final int count;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: count > 0 ? '$count new ${count == 1 ? 'message' : 'messages'}: scroll to the newest' : 'Scroll to the newest messages',
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Stack(
          clipBehavior: Clip.none,
          children: [
            Container(
              width: 44,
              height: 44,
              decoration: BoxDecoration(
                color: t.chatIn,
                shape: BoxShape.circle,
                border: Border.all(color: t.border),
                boxShadow: t.shadowMd,
              ),
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

/// styles.css `--chat-pattern`: two grids of soft dots, 28 px apart, the
/// second set 14 px in, over the wallpaper.
class _Wallpaper extends CustomPainter {
  _Wallpaper(this.ground, this.doodle);
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
  bool shouldRepaint(_Wallpaper old) => old.ground != ground || old.doodle != doodle;
}

/// ChatHead.tsx: Back, the avatar, the name with who they are under it,
/// search and the menu.
class _ConversationHead extends StatelessWidget {
  const _ConversationHead({required this.data, this.onBack});
  final ConversationData data;
  final VoidCallback? onBack;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final c = data.conv;
    Widget iconButton(String label, Widget icon, VoidCallback? onTap) => Semantics(
      button: true,
      label: label,
      child: TkPress(
        onTap: onTap ?? () {},
        child: SizedBox(width: 40, height: 40, child: Center(child: icon)),
      ),
    );
    return Container(
      // `.chat-head`: 12 px 16 px, a line under it.
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: t.border)),
      ),
      child: Row(
        children: [
          iconButton('Back to the chats', TkIcon('back', size: 20, color: t.text), onBack ?? () => Navigator.of(context).maybePop()),
          const SizedBox(width: 6),
          Semantics(
            button: true,
            label: 'About ${c.name}',
            child: ListenableBuilder(
              listenable: data.chats,
              builder: (context, _) => ChatAvatar(name: c.name, logo: c.logo ? data.chats.logo(c.business) : null, size: 40, letterSize: 15),
            ),
          ),
          const SizedBox(width: 6),
          Expanded(
            child: ListenableBuilder(
              listenable: data,
              builder: (context, _) {
                final sub = data.typingNow ? 'typing…' : data.headLine;
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Semantics(
                      header: true,
                      child: Text(
                        c.name,
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
                        style: data.typingNow
                            ? tkText(size: 13, weight: 600, lineHeight: 1.25, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand)
                            : tkText(size: 13, lineHeight: 1.25, color: t.muted),
                      ),
                  ],
                );
              },
            ),
          ),
          const SizedBox(width: 6),
          iconButton('Search this conversation', TkIcon('search', size: 18, color: t.text), null),
          const SizedBox(width: 6),
          iconButton('More about this conversation', RotatedBox(quarterTurns: 1, child: TkIcon('more', size: 18, stroke: 3, color: t.text)), null),
        ],
      ),
    );
  }
}

/// Conversation.tsx DIARY_RUN_MS: diary lines this close are one line.
const _diaryRun = Duration(minutes: 5);

/// ChatStream.tsx: the days (each chip staying at the top while its day
/// is under it), where the unread begin, and the messages: bubbles, cards,
/// and our own changes as diary lines.
class _Stream extends StatelessWidget {
  const _Stream({
    required this.data,
    required this.scroll,
    required this.unreadKey,
    required this.chipKeys,
    required this.groupKeys,
    required this.acts,
    required this.changed,
    this.stuck,
  });
  final ConversationData data;
  final ScrollController scroll;
  final GlobalKey unreadKey;
  final Map<String, GlobalKey> chipKeys;
  final Map<String, GlobalKey> groupKeys;
  final CardActs acts;
  final VoidCallback changed;

  /// The day whose chip is drawn stuck at the top (its own is hidden).
  final String? stuck;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final now = data.chats.now();
    final chatTools = data.chats.chatTools;
    final items = data.messages == null ? null : data.thread;
    final children = <Widget>[];
    if (items == null) {
      // Loading: the web's skeleton, plainly nothing yet.
    } else if (items.isEmpty) {
      children.add(
        Center(
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 12),
            child: Text('No messages yet. Say hello to ${data.conv.name}.', style: tkHint(t)),
          ),
        ),
      );
    } else {
      // Our own changes made close together on one day are one line.
      bool diary(LinkMessage m) => m.kind == 'diary' || (chatTools && m.card != null && isDiary(m.card!, m.mine));
      final runs = <String, List<LinkMessage>>{};
      final member = <String>{};
      List<LinkMessage>? run;
      for (final x in items) {
        final m = x.m;
        final last = run?.last;
        if (diary(m) &&
            run != null &&
            last != null &&
            dayLabel(last.createdAt, now) == dayLabel(m.createdAt, now) &&
            m.createdAt.difference(last.createdAt) <= _diaryRun) {
          run.add(m);
          member.add(m.id);
        } else if (diary(m)) {
          run = [m];
          runs[m.id] = run;
        } else {
          run = null;
        }
      }
      // A card's actions are on its document's newest card only, and every
      // card of a document shows where it is now.
      final newest = <String>{};
      final current = <String, LinkDocCard>{};
      for (final x in items.reversed) {
        final c = x.m.card;
        if (x.m.kind != 'card' || c == null) continue;
        final key = '${c.doc}:${c.reference}';
        if (current.containsKey(key)) continue;
        current[key] = c;
        newest.add(x.id);
      }

      String? day;
      ThreadMessage? prev;
      final group = <Widget>[];
      void close() {
        if (group.isEmpty) return;
        children.add(
          KeyedSubtree(
            key: groupKeys.putIfAbsent(day!, GlobalKey.new),
            child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: _gapped(List.of(group), 2)),
          ),
        );
        group.clear();
      }

      for (final m in items) {
        final label = dayLabel(m.m.createdAt, now);
        if (label != day) {
          close();
          day = label;
          prev = null;
          group.add(
            Center(
              child: Opacity(
                opacity: stuck == label ? 0 : 1,
                child: _DayChip(label, chipKey: chipKeys.putIfAbsent(label, GlobalKey.new)),
              ),
            ),
          );
        }
        if (data.unread?.id == m.id) group.add(_UnreadMark(data.unread!.count, markKey: unreadKey));
        if (member.contains(m.id)) continue;
        final lead = runs[m.id];
        if (lead != null) {
          group.add(_DiaryRun(run: lead, other: data.conv.name, onOpen: acts.onOpen));
          prev = m;
          continue;
        }
        final first = prev == null || _startsRun(prev.m, m.m);
        final card = m.m.kind == 'card' ? m.m.card : null;
        group.add(
          Padding(
            padding: EdgeInsets.only(top: first ? 6 : 0),
            child: _Bubble(
              m: m,
              other: data.conv.name,
              first: first,
              now: now,
              latest: newest.contains(m.id),
              current: card == null ? null : current['${card.doc}:${card.reference}'],
              acts: acts,
              changed: changed,
              files: data.chats.files,
              onRetry: m.out == null ? null : () => data.retry(m.out!),
              onDiscard: m.out == null ? null : () => data.discard(m.out!),
            ),
          ),
        );
        prev = m;
      }
      close();
    }
    return Semantics(
      label: 'Chat history',
      liveRegion: true,
      child: SingleChildScrollView(
        controller: scroll,
        // `.chat-stream`: 8 px 10 px, 2 px between groups.
        padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [if (data.error != null) TkErrorBox(data.error!), if (acts.error != null) TkErrorBox(acts.error!), ..._gapped(children, 2)],
        ),
      ),
    );
  }
}

List<Widget> _gapped(List<Widget> list, double gap) => [
  for (var i = 0; i < list.length; i++) ...[if (i > 0) SizedBox(height: gap), list[i]],
];

/// How much taller a line of a bubble's text is in the app than in Chrome,
/// half of it for each of two lines.
const _lineDrift = 0.31;

Widget _maybeIntrinsic(bool on, Widget child) => on ? IntrinsicWidth(child: child) : child;

/// lib/thread.ts startsRun: the first of a run has the bubble's tail.
bool _startsRun(LinkMessage prev, LinkMessage m) {
  if (prev.kind == 'card' || m.kind == 'card') return true;
  return prev.mine != m.mine || prev.senderName != m.senderName;
}

/// `.chat-day`: the day, in a soft pill, 6 px above and below.
class _DayChip extends StatelessWidget {
  const _DayChip(this.label, {this.chipKey, this.margin = true});
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
class _UnreadMark extends StatelessWidget {
  const _UnreadMark(this.count, {this.markKey});
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
          count == 1 ? '1 unread message' : '$count unread messages',
          textAlign: TextAlign.center,
          style: tkText(size: 12, weight: 600, color: t.tintText),
        ),
      ),
    );
  }
}

/// Conversation.tsx DiaryRun: the newest change shows; the ones before it
/// fold under it.
class _DiaryRun extends StatefulWidget {
  const _DiaryRun({required this.run, required this.other, this.onOpen});
  final List<LinkMessage> run;
  final String other;
  final void Function(LinkDocCard card)? onOpen;

  @override
  State<_DiaryRun> createState() => _DiaryRunState();
}

class _DiaryRunState extends State<_DiaryRun> {
  bool _open = false;

  Widget _line(LinkMessage m, {Widget? more}) {
    final c = m.card;
    return DiaryLine(
      words: c != null ? cardLine(c, true, widget.other) : (m.body ?? ''),
      sub: c != null ? cardSub(c) : null,
      when: 'You, ${clock(m.createdAt)}',
      more: more,
      onTap: c == null || widget.onOpen == null ? null : () => widget.onOpen!(c),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final lead = widget.run.last;
    final rest = widget.run.sublist(0, widget.run.length - 1);
    final more = rest.isEmpty
        ? null
        : Semantics(
            button: true,
            expanded: _open,
            child: GestureDetector(
              onTap: () => setState(() => _open = !_open),
              child: Text(
                _open ? 'Fold' : 'and ${rest.length} earlier ${rest.length == 1 ? 'change' : 'changes'}',
                style: tkText(size: 12, weight: 600, color: t.tintText, decoration: TextDecoration.underline),
              ),
            ),
          );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (_open)
          for (final m in rest) _line(m),
        _line(lead, more: more),
      ],
    );
  }
}

/// Conversation.tsx Bubble: a message, its time in the corner of its last
/// line, and our ticks.
class _Bubble extends StatelessWidget {
  const _Bubble({
    required this.m,
    required this.other,
    required this.first,
    required this.now,
    required this.acts,
    required this.changed,
    required this.files,
    this.latest = false,
    this.current,
    this.onRetry,
    this.onDiscard,
  });
  final ThreadMessage m;
  final String other;
  final bool first;
  final DateTime now;
  final CardActs acts;
  final VoidCallback changed;
  final ChatFiles files;

  /// The newest card of its document: its actions show.
  final bool latest;

  /// The document's newest card: where it is now.
  final LinkDocCard? current;
  final VoidCallback? onRetry;
  final VoidCallback? onDiscard;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final msg = m.m;
    final mine = msg.mine;
    final out = m.out;
    final failed = out?.failed ?? false;
    final meta = mine ? t.chatOutMeta : t.muted;
    final body = msg.body ?? '';
    final card = msg.kind == 'card' ? msg.card : null;
    final inline = card == null && body.isNotEmpty && msg.preview == null && !failed;
    final stamp = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          clock(msg.createdAt),
          style: tkText(size: 11, lineHeight: 16 / 11, color: meta),
        ),
        if (out != null && !failed) ...[
          const SizedBox(width: 4),
          Semantics(
            label: 'Sending',
            child: TkIcon('clock', size: 16, stroke: 2, color: meta),
          ),
        ],
        if (failed) ...[
          const SizedBox(width: 4),
          Semantics(
            label: 'Not sent',
            child: Text(
              '!',
              style: tkText(size: 11, weight: 700, lineHeight: 16 / 11, color: t.danger),
            ),
          ),
        ],
        if (mine && msg.kind != 'card' && out == null) ...[
          const SizedBox(width: 4),
          Semantics(
            label: msg.readAt != null ? 'Read' : 'Delivered',
            child: TkIcon('tick-double', size: 16, stroke: 2, color: msg.readAt != null ? t.chatTickRead : meta),
          ),
        ],
      ],
    );
    // `.bubble .text` (16 px, 1.4) in a block of the body's type: CSS's
    // line is as tall as both, 22.7 px; Flutter draws whole pixels a line.
    final text = Text.rich(
      TextSpan(
        children: [
          TextSpan(text: body),
          // `.stamp-gap`: room kept at the end of the text for the time.
          if (inline)
            WidgetSpan(
              // 1 px tall: at the line's bottom it leaves the line as it is
              // (on the baseline, Flutter cannot size it in a bubble sized
              // to its content).
              alignment: PlaceholderAlignment.bottom,
              child: SizedBox(width: mine ? 74 : 52, height: 1),
            ),
        ],
      ),
      style: tkText(size: 16, lineHeight: 23 / 16, color: t.text),
    );
    final radius = BorderRadius.only(
      topLeft: Radius.circular(first && !mine ? 4 : 16),
      topRight: Radius.circular(first && mine ? 4 : 16),
      bottomLeft: const Radius.circular(16),
      bottomRight: const Radius.circular(16),
    );
    final bubble = LayoutBuilder(
      builder: (context, box) {
        // `max-width: min(84%, 560px)`; a card's bubble is `min(86%, 420px)` wide.
        final max = card != null ? (box.maxWidth * 0.86).clamp(0.0, 420.0) : (box.maxWidth * 0.84).clamp(0.0, 560.0);
        return Align(
          alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
          child: ConstrainedBox(
            constraints: card != null ? BoxConstraints.tightFor(width: max) : BoxConstraints(maxWidth: max),
            // CSS sizes a bubble to what it holds (shrink to fit): as wide as
            // its widest line or photo, up to the most it may be.
            child: _maybeIntrinsic(
              card == null,
              Semantics(
                label: mine ? null : (first && msg.senderName != null ? '${msg.senderName}: ' : null),
                child: Container(
                  decoration: BoxDecoration(
                    color: mine ? t.chatOut : t.chatIn,
                    borderRadius: radius,
                    boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 1), blurRadius: 1)],
                  ),
                  child: Stack(
                    children: [
                      if (card != null)
                        // `.bubble.card-bubble { padding: 6px }`: the frame, then the time.
                        Padding(
                          padding: const EdgeInsets.all(6),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              CardFrame(
                                card: card,
                                current: current,
                                mine: mine,
                                now: now,
                                title: cardLine(card, mine, other),
                                actions: [viewButton(card, acts), if (latest && (!mine || isDelivery(card))) ...cardActions(card, acts, changed)],
                              ),
                              Padding(
                                padding: const EdgeInsets.only(top: 1),
                                child: Align(alignment: Alignment.centerRight, child: stamp),
                              ),
                            ],
                          ),
                        )
                      else
                        Padding(
                          // `padding: 8px 12px 6px`, with two corrections: on the
                          // right 11.5, as Chrome fits a line its measure says
                          // is a hundredth of a pixel wider; above and below
                          // 0.31 less, as Flutter's lines are a whole 23 px and
                          // Chrome's 22.69 (exact for two lines; a thread
                          // otherwise drifts a pixel every three lines).
                          padding: const EdgeInsets.fromLTRB(12, 8 - _lineDrift, 11.5, 6 - _lineDrift),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              if (msg.forwarded == true)
                                Text(
                                  'Forwarded',
                                  style: tkText(size: 12, color: t.muted).copyWith(fontStyle: FontStyle.italic),
                                ),
                              if (msg.attachment != null) AttachmentView(m: msg, files: files, other: other, room: max - 24, local: out?.file),
                              if (body.isNotEmpty) text,
                              if (!inline)
                                Padding(
                                  // `.stamp { margin-top: 1px }`, which collapses
                                  // into an attachment's larger bottom margin.
                                  padding: EdgeInsets.only(top: msg.attachment != null && body.isEmpty ? 0 : 1),
                                  child: Align(alignment: Alignment.centerRight, child: stamp),
                                ),
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
          ),
        );
      },
    );
    if (!failed) return bubble;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        bubble,
        Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Semantics(
            liveRegion: true,
            label: 'Not sent. ${out?.error ?? ''}',
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Semantics(
                  button: true,
                  label: 'Retry',
                  child: TkPress(
                    onTap: onRetry,
                    child: SizedBox(
                      width: 32,
                      height: 32,
                      child: Center(child: TkIcon('retry', size: 16, color: t.danger)),
                    ),
                  ),
                ),
                Semantics(
                  button: true,
                  label: 'Delete',
                  child: TkPress(
                    onTap: onDiscard,
                    child: SizedBox(
                      width: 32,
                      height: 32,
                      child: Center(child: TkIcon('trash', size: 16, color: t.danger)),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}
