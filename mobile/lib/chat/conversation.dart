// A conversation with a linked business: the web's ConversationPane
// (web/src/pages/Conversation.tsx, ChatHead.tsx, ChatStream.tsx,
// BubbleShell.tsx, ChatComposer.tsx), drawn the same at a phone's width:
// the head, the thread on the chat's wallpaper with its days, where the
// unread begin and the bubbles, and the box to write in.

import 'dart:async';

import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/session.dart' show uuid4;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'cards.dart';
import 'chats.dart';
import 'composer.dart';
import 'thread.dart';
import 'live.dart';
import 'menu.dart';
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
  Outgoing(this.clientId, this.body, this.at, {this.file, this.reply});
  final String clientId;
  final String body;
  final DateTime at;

  /// The message it answers (quoted).
  final LinkMessage? reply;

  /// A photo, PDF or voice note going with it (the body its caption).
  final PickedFile? file;
  bool failed = false;
  String? error;
}

/// linkchat.ts Quote: a message as the one answering it quotes it.
LinkMessageReply quoteOf(LinkMessage m) => LinkMessageReply.fromJson({
  'pair_id': m.id,
  'mine': m.mine,
  'kind': m.kind,
  'body': ?m.body,
  if (m.card != null) 'card': m.card!.toJson(),
  if (m.attachment != null) 'attachment': m.attachment!.toJson(),
});

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
          reply: o.reply == null ? null : quoteOf(o.reply!),
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
  Future<void> send(String text, {PickedFile? file, LinkMessage? reply}) async {
    final body = text.trim();
    if (body.isEmpty && file == null) return;
    final o = Outgoing(uuid4(), body, chats.now(), file: file, reply: reply);
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
          fields: {'client_id': o.clientId, if (o.body.isNotEmpty) 'text': o.body, if (o.reply != null) 'reply_to': o.reply!.id},
        );
        sent = LinkMessage.fromJson((j as Map<String, dynamic>)['message'] as Map<String, dynamic>);
      } else {
        sent = (await api.sendLinkMessage(
          link: conv.link,
          body: SendLinkMessageRequest(text: o.body, clientId: o.clientId, replyTo: o.reply?.id),
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

  /// What went wrong with a reaction, a star or a deletion, said once.
  String? actionError;

  void _swap(LinkMessage m) {
    messages = messages?.map((x) => x.id == m.id ? m : x).toList();
    _changed();
  }

  LinkMessage? _now(String id) => messages?.where((x) => x.id == id).firstOrNull;

  String _say(Object e) => e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.';

  /// Our reaction given, or taken back when it is the same one; shown at
  /// once, and put back if the server says no.
  Future<void> react(LinkMessage m, String emoji) async {
    final was = _now(m.id) ?? m;
    final taking = was.reactions?.any((r) => r.mine == true && r.emoji == emoji) ?? false;
    _swap(changedMessage(was, reactions: toggleReaction(was.reactions, emoji, !taking)));
    try {
      final out = taking
          ? (await api.unreactLinkMessage(link: conv.link, id: m.id)).reactions
          : (await api.reactLinkMessage(
              link: conv.link,
              id: m.id,
              body: ReactLinkMessageRequest(emoji: emoji),
            )).reactions;
      _swap(changedMessage(_now(m.id) ?? was, reactions: out));
    } on Object catch (e) {
      _swap(changedMessage(_now(m.id) ?? was, reactions: was.reactions ?? const []));
      actionError = _say(e);
      _changed();
    }
  }

  /// Starred, or the star taken off: for this person alone.
  Future<void> star(LinkMessage m) async {
    final on = !(m.starred ?? false);
    _swap(changedMessage(_now(m.id) ?? m, starred: on));
    try {
      if (on) {
        await api.starLinkMessage(link: conv.link, id: m.id);
      } else {
        await api.unstarLinkMessage(link: conv.link, id: m.id);
      }
    } on Object catch (e) {
      _swap(changedMessage(_now(m.id) ?? m, starred: !on));
      actionError = _say(e);
      _changed();
    }
  }

  /// Deleted for us: our copy goes; they keep theirs. True once done.
  Future<bool> hide(LinkMessage m) async {
    try {
      await api.hideLinkMessage(link: conv.link, id: m.id);
      messages = messages?.where((x) => x.id != m.id).toList();
      _changed();
      unawaited(chats.load());
      return true;
    } on Object catch (e) {
      actionError = _say(e);
      _changed();
      return false;
    }
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

class _ConversationScreenState extends State<ConversationScreen> {
  final _text = TextEditingController();

  /// The box to write in (a test hands it a file, as a picker would).
  final composerKey = GlobalKey<ComposerState>();
  late final CardActs _acts = CardActs(
    auth: widget.data.chats.auth,
    onDone: () => widget.data.pull(),
    onOpen: widget.onOpenDoc,
    ask: (title, ok) => showTkConfirm(context, title: title, ok: ok, danger: true),
    said: (words) {
      if (mounted) showTkToast(context, words);
    },
  );

  @override
  void initState() {
    super.initState();
    if (widget.data.messages == null) widget.data.open();
  }

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  /// The message whose menu is open, and the one being answered.
  String? _menuFor;

  /// The menu's "+" chosen: the chevron goes with the card (the web shows
  /// it only with the card).
  bool _picking = false;
  LinkMessage? _reply;

  ConversationData get _d => widget.data;
  bool get _trades => _d.chats.auth.can('chat.trade');

  /// Conversation.tsx write: may send (chat.trade, the link not ended).
  bool get _write => _trades && _d.conv.state == 'active';

  /// The other conversations a message may go to.
  List<LinkConversation> get _others => _d.chats.conversations.where((c) => c.state == 'active' && c.link != _d.conv.link).toList();

  /// Conversation.tsx Bubble's menu: what can be done with a message.
  Future<void> _menu(ThreadMessage x, Rect at) async {
    final m = x.m;
    final card = m.kind == 'card' ? m.card : null;
    final body = m.body ?? '';
    final other = _d.conv.name;
    final mineEmoji = m.reactions?.where((r) => r.mine == true).firstOrNull?.emoji;
    setState(() {
      _menuFor = m.id;
      _picking = false;
    });
    await showMessageMenu(
      context,
      bubble: at,
      mine: m.mine,
      what: card != null ? cardLine(card, m.mine, other) : previewLine(m, other),
      mineEmoji: mineEmoji,
      onReact: _write ? (e) => _d.react(m, e) : null,
      onPicking: () => setState(() => _picking = true),
      items: [
        MenuItem('info', 'Message info', () => _info(m)),
        if (_write) MenuItem('reply', 'Reply', () => setState(() => _reply = m)),
        if (card == null && body.isNotEmpty) MenuItem('copy', 'Copy', () => Clipboard.setData(ClipboardData(text: body)), done: 'Copied'),
        if (card == null && _write && _others.isNotEmpty) MenuItem('forward', 'Forward', () => _forward(m)),
        MenuItem('star', m.starred == true ? 'Unstar' : 'Star', () => _d.star(m)),
        if (card == null && _trades) MenuItem('trash', 'Delete for me', () => _hide(m), danger: true, separated: true),
      ],
    );
    if (mounted) setState(() => _menuFor = null);
  }

  Future<void> _hide(LinkMessage m) async {
    final yes = await showTkConfirm(
      context,
      title: 'Delete this message?',
      body: 'It goes from your business\'s side. They keep theirs.',
      ok: 'Delete for me',
      danger: true,
    );
    if (!yes || !mounted) return;
    if (await _d.hide(m) && mounted) showTkToast(context, 'Deleted for you');
  }

  /// Conversation.tsx InfoSheet: when it was sent and read, who reacted.
  Future<void> _info(LinkMessage m) {
    final other = _d.conv.name;
    final now = _d.chats.now();
    return showTkModal<void>(
      context,
      title: 'Message info',
      builder: (context) {
        final t = TkTheme.tokens(context);
        String when(DateTime? d) => d == null ? 'Not yet' : whenWords(d, now);
        Widget row(String k, String v) => Padding(
          padding: const EdgeInsets.only(bottom: 6),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                width: 120,
                child: Text(k, style: tkText(size: 14, color: t.muted)),
              ),
              const SizedBox(width: 16),
              Expanded(
                child: Text(v, style: tkText(size: 14, weight: 600, color: t.text)),
              ),
            ],
          ),
        );
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('“${m.kind == 'card' && m.card != null ? cardLine(m.card!, m.mine, other) : previewLine(m, other)}”', style: tkHint(t)),
            const SizedBox(height: 8),
            row(m.mine ? 'Sent' : 'Received', whenWords(m.createdAt, now)),
            row(m.mine ? 'Read by $other' : 'Read by you', when(m.readAt)),
            const SizedBox(height: 8),
            if (m.reactions?.isNotEmpty ?? false) ...[
              Text('Reactions', style: tkText(size: 15, weight: 600, color: t.text)),
              const SizedBox(height: 4),
              for (final r in m.reactions!)
                Padding(
                  padding: const EdgeInsets.only(bottom: 4),
                  child: Text(reactionWords(r), style: tkText(size: 14, color: t.text)),
                ),
              const SizedBox(height: 8),
            ],
            TkPlainButton(label: 'Close', onTap: () => Navigator.of(context).pop()),
          ],
        );
      },
    );
  }

  /// Conversation.tsx ForwardSheet: to up to five linked businesses.
  Future<void> _forward(LinkMessage m) async {
    final from = _d.conv;
    final to = _others;
    final picked = <String>[];
    var busy = false;
    String? error;
    await showTkModal<void>(
      context,
      title: 'Forward to',
      builder: (context) => StatefulBuilder(
        builder: (context, set) {
          final t = TkTheme.tokens(context);
          Future<void> send() async {
            set(() {
              busy = true;
              error = null;
            });
            try {
              await _d.chats.auth.transport.send('POST', '/v1/link-chats/${from.link}/forward', body: {'message': m.id, 'to': picked});
              final words = picked.length == 1 ? (to.where((c) => c.link == picked.first).firstOrNull?.name ?? '1 chat') : '${picked.length} chats';
              if (context.mounted) {
                Navigator.of(context).pop();
                showTkToast(context, 'Forwarded to $words');
              }
              unawaited(_d.chats.load());
            } on ApiError catch (e) {
              set(() => error = e.message);
            } on Offline {
              set(() => error = 'You’re offline. Try again when you’re back online.');
            } finally {
              if (context.mounted) set(() => busy = false);
            }
          }

          return Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text('“${previewLine(m, from.name)}”', style: tkHint(t)),
              const SizedBox(height: 12),
              Text('Linked businesses (up to five)', style: tkText(size: 14, color: t.text)),
              const SizedBox(height: 6),
              for (final c in to)
                Semantics(
                  checked: picked.contains(c.link),
                  label: c.name,
                  excludeSemantics: true,
                  child: GestureDetector(
                    behavior: HitTestBehavior.opaque,
                    onTap: () => set(() {
                      if (picked.contains(c.link)) {
                        picked.remove(c.link);
                      } else if (picked.length < 5) {
                        picked.add(c.link);
                      }
                    }),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(vertical: 6),
                      child: Row(
                        children: [
                          TkCheckbox(checked: picked.contains(c.link)),
                          const SizedBox(width: 8),
                          Expanded(
                            child: Text(c.name, style: tkText(size: 14, color: t.text)),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              if (error != null) TkErrorBox(error!),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  Opacity(
                    opacity: busy || picked.isEmpty ? 0.5 : 1,
                    child: TkPlainButton(label: 'Forward', primary: true, onTap: busy || picked.isEmpty ? null : send),
                  ),
                  TkPlainButton(label: 'Cancel', onTap: () => Navigator.of(context).pop()),
                ],
              ),
            ],
          );
        },
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    final d = widget.data;
    final c = d.conv;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: Semantics(
        label: 'Conversation with ${c.name}',
        container: true,
        child: CustomPaint(
          // `.chat-mode .chat-pane { background: var(--chat-pattern), var(--chat-wallpaper) }`
          painter: Wallpaper(t.chatWallpaper, t.chatDoodle),
          child: Column(
            children: [
              ColoredBox(
                color: t.panel,
                child: Padding(
                  padding: EdgeInsets.only(top: pad.top),
                  child: ListenableBuilder(
                    listenable: Listenable.merge([d, d.chats]),
                    builder: (context, _) => ChatHeadBar(
                      onBack: widget.onBack,
                      avatar: Semantics(
                        button: true,
                        label: 'About ${c.name}',
                        child: ChatAvatar(name: c.name, logo: c.logo ? d.chats.logo(c.business) : null, size: 40, letterSize: 15),
                      ),
                      title: c.name,
                      subtitle: d.headLine,
                      typing: d.typingNow,
                      actions: [ChatHeadBar.iconButton('Search this conversation', TkIcon('search', size: 18, color: t.text), null)],
                    ),
                  ),
                ),
              ),
              Expanded(
                child: ListenableBuilder(listenable: d, builder: (context, _) => _thread(context, d)),
              ),
              Composer(
                key: composerKey,
                text: _text,
                replying: _reply == null ? null : previewLine(_reply!, c.name),
                onCancelReply: () => setState(() => _reply = null),
                onSend: (words, file) {
                  final r = _reply;
                  setState(() => _reply = null);
                  d.send(words, file: file, reply: r);
                },
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

  /// The thread: bubbles, cards, and our own changes as diary lines.
  Widget _thread(BuildContext context, ConversationData d) {
    final t = TkTheme.tokens(context);
    final now = d.chats.now();
    final chatTools = d.chats.chatTools;
    final items = d.messages == null ? null : d.thread;
    // Our own changes made close together on one day are one line.
    bool diary(LinkMessage m) => m.kind == 'diary' || (chatTools && m.card != null && isDiary(m.card!, m.mine));
    final runs = <String, List<LinkMessage>>{};
    final member = <String>{};
    List<LinkMessage>? run;
    for (final x in items ?? const <ThreadMessage>[]) {
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
    for (final x in (items ?? const <ThreadMessage>[]).reversed) {
      final card = x.m.card;
      if (x.m.kind != 'card' || card == null) continue;
      final key = '${card.doc}:${card.reference}';
      if (current.containsKey(key)) continue;
      current[key] = card;
      newest.add(x.id);
    }
    return ThreadView<ThreadMessage>(
      items: items,
      of: (id: (m) => m.id, at: (m) => m.m.createdAt, mine: (m) => m.m.mine),
      now: now,
      unread: d.unread,
      empty: Text('No messages yet. Say hello to ${d.conv.name}.', style: tkHint(t)),
      errors: [?d.error, ?_acts.error, ?d.actionError],
      render: (m, prev) {
        if (member.contains(m.id)) return null;
        final lead = runs[m.id];
        if (lead != null) return _DiaryRun(run: lead, other: d.conv.name, onOpen: _acts.onOpen);
        final first = prev == null || _startsRun(prev.m, m.m);
        final card = m.m.kind == 'card' ? m.m.card : null;
        return Padding(
          padding: EdgeInsets.only(top: first ? 6 : 0),
          child: _Bubble(
            m: m,
            other: d.conv.name,
            first: first,
            now: now,
            latest: newest.contains(m.id),
            current: card == null ? null : current['${card.doc}:${card.reference}'],
            acts: _acts,
            changed: () => setState(() {}),
            files: d.chats.files,
            onRetry: m.out == null ? null : () => d.retry(m.out!),
            onDiscard: m.out == null ? null : () => d.discard(m.out!),
            menuOpen: _menuFor == m.id && !_picking,
            onHold: (at) => _menu(m, at),
            onReply: _write ? () => setState(() => _reply = m.m) : null,
            onReact: _write ? (e) => d.react(m.m, e) : null,
          ),
        );
      },
    );
  }
}

/// Conversation.tsx DIARY_RUN_MS: diary lines this close are one line.
const _diaryRun = Duration(minutes: 5);

/// How much taller a line of a bubble's text is in the app than in Chrome,
/// half of it for each of two lines.
const _lineDrift = 0.31;

Widget _maybeIntrinsic(bool on, Widget child) => on ? IntrinsicWidth(child: child) : child;

/// lib/thread.ts startsRun: the first of a run has the bubble's tail.
bool _startsRun(LinkMessage prev, LinkMessage m) {
  if (prev.kind == 'card' || m.kind == 'card') return true;
  return prev.mine != m.mine || prev.senderName != m.senderName;
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
    this.menuOpen = false,
    this.onHold,
    this.onReply,
    this.onReact,
  });
  final ThreadMessage m;

  /// Its menu is open: the chevron shows, as the web's does then.
  final bool menuOpen;

  /// Held: its menu opens by the bubble (on the screen).
  final ValueChanged<Rect>? onHold;

  /// Swiped right: it is replied to.
  final VoidCallback? onReply;

  /// A reaction chip pressed: the same reaction, or ours taken back.
  final ValueChanged<String>? onReact;
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
    final reacted = (msg.reactions?.isNotEmpty ?? false) && out == null;
    final stamp = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (msg.starred == true) ...[
          Semantics(
            label: 'Starred',
            child: TkIcon('star', size: 12, color: meta, fill: true),
          ),
          const SizedBox(width: 4),
        ],
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
              _Overhung(
                // `.reactions`: the chips over the bubble's foot, 12 px below it.
                below: reacted ? _Reactions(list: msg.reactions!, onReact: onReact) : null,
                mine: mine,
                child: Semantics(
                  label: mine ? null : (first && msg.senderName != null ? '${msg.senderName}: ' : null),
                  child: Container(
                    decoration: BoxDecoration(
                      color: mine ? t.chatOut : t.chatIn,
                      borderRadius: radius,
                      boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 1), blurRadius: 1)],
                    ),
                    child: Stack(
                      clipBehavior: Clip.none,
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
                            // `.bubble.has-reactions { padding-bottom: 13px }` more.
                            padding: EdgeInsets.fromLTRB(12, 8 - _lineDrift, 11.5, 6 - _lineDrift + (reacted ? 13 : 0)),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.stretch,
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                if (msg.forwarded == true)
                                  Text(
                                    'Forwarded',
                                    style: tkText(size: 12, color: t.muted).copyWith(fontStyle: FontStyle.italic),
                                  ),
                                if (msg.reply != null) _Quote(reply: msg.reply!, mine: mine, other: other),
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
                        // (14 px with reactions under it).
                        if (inline) Positioned(right: 9, bottom: reacted ? 14 : 5, child: stamp),
                        // `.bubble-chevron`, shown while its menu is open.
                        if (menuOpen)
                          Positioned(
                            top: 1,
                            right: 2,
                            child: Container(
                              width: 22,
                              height: 22,
                              decoration: BoxDecoration(
                                color: cssMix(mine ? t.chatOut : t.chatIn, const Color(0x00000000), 0.85),
                                shape: BoxShape.circle,
                                boxShadow: const [BoxShadow(color: Color(0x0A0E0F0C), offset: Offset(0, 1), blurRadius: 2)],
                              ),
                              alignment: Alignment.center,
                              child: TkIcon('down', size: 16, color: meta),
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
      },
    );
    final held = _Hold(onHold: out == null ? onHold : null, onReply: out == null ? onReply : null, child: bubble);
    final placed = held;
    if (!failed) return placed;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        placed,
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

/// BubbleShell's touch: a finger held opens the menu, a swipe right replies
/// (60 px, drawn up to 80), as WhatsApp's.
class _Hold extends StatefulWidget {
  const _Hold({required this.child, this.onHold, this.onReply});
  final Widget child;
  final ValueChanged<Rect>? onHold;
  final VoidCallback? onReply;

  @override
  State<_Hold> createState() => _HoldState();
}

class _HoldState extends State<_Hold> {
  double _dx = 0;

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
      onHorizontalDragUpdate: widget.onReply == null ? null : (d) => setState(() => _dx = (_dx + d.delta.dx).clamp(0.0, 80.0)),
      onHorizontalDragEnd: widget.onReply == null
          ? null
          : (_) {
              if (_dx >= 60) widget.onReply!();
              setState(() => _dx = 0);
            },
      onHorizontalDragCancel: widget.onReply == null ? null : () => setState(() => _dx = 0),
      child: Transform.translate(offset: Offset(_dx, 0), child: widget.child),
    );
    if (widget.onHold == null) return gestures;
    return Semantics(
      container: true,
      explicitChildNodes: true,
      onLongPressHint: 'Options',
      onLongPress: () => widget.onHold!(_rect()),
      child: gestures,
    );
  }
}

/// `.bubble .quote`: the message answered, who wrote it, three lines at most.
class _Quote extends StatelessWidget {
  const _Quote({required this.reply, required this.mine, required this.other});
  final LinkMessageReply reply;
  final bool mine;
  final String other;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final line = previewLine(
      LinkMessage(id: reply.pairId, link: '', mine: reply.mine, kind: reply.kind, body: reply.body, card: reply.card, createdAt: DateTime(2000)),
      other,
    );
    return Container(
      margin: const EdgeInsets.only(bottom: 4),
      padding: const EdgeInsets.fromLTRB(8, 5, 8, 5),
      decoration: BoxDecoration(
        color: t.accentSoft,
        border: Border(left: BorderSide(color: t.accent, width: 4)),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(reply.mine ? 'You' : other, style: tkText(size: 12.5, weight: 600, color: mine ? t.chatOutMeta : t.tintText)),
          const SizedBox(height: 1),
          Text(
            line,
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
            style: tkText(size: 13.5, color: mine ? t.chatOutMeta : cssMix(t.text, t.muted, 0.75)),
          ),
        ],
      ),
    );
  }
}

/// A bubble with its reaction chips hanging 12 px under it, and
/// `.bubble.has-reactions { margin-bottom: 11px }` kept under it: the chips
/// inside what is laid out (all but a pixel), so a tap on them is theirs.
class _Overhung extends StatelessWidget {
  const _Overhung({required this.child, required this.below, required this.mine});
  final Widget child;
  final Widget? below;
  final bool mine;

  @override
  Widget build(BuildContext context) {
    if (below == null) return child;
    return Stack(
      clipBehavior: Clip.none,
      children: [
        Padding(padding: const EdgeInsets.only(bottom: 11), child: child),
        Positioned(bottom: -1, left: mine ? null : 6, right: mine ? 6 : null, child: below!),
      ],
    );
  }
}

/// `.reaction-chip`s: each emoji, how many when more than one, ours tinted.
class _Reactions extends StatelessWidget {
  const _Reactions({required this.list, this.onReact});
  final List<LinkReaction> list;
  final ValueChanged<String>? onReact;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: 'Reactions',
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final r in list) ...[
            if (r != list.first) const SizedBox(width: 3),
            Semantics(
              container: true,
              button: onReact != null,
              label: '${reactionWords(r)}. ${r.mine == true ? 'Tap to take yours back.' : 'Tap to react the same.'}',
              excludeSemantics: true,
              child: GestureDetector(
                onTap: onReact == null ? null : () => onReact!(r.emoji),
                child: Container(
                  constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
                  padding: const EdgeInsets.symmetric(horizontal: 5),
                  decoration: BoxDecoration(
                    color: r.mine == true ? t.tint : t.panel,
                    borderRadius: BorderRadius.circular(999),
                    boxShadow: [
                      BoxShadow(color: t.chatWallpaper, spreadRadius: 2),
                      BoxShadow(color: t.chatShadow, offset: const Offset(0, 1), blurRadius: 2),
                    ],
                  ),
                  alignment: Alignment.center,
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(r.emoji, style: tkText(size: 14, lineHeight: 1)),
                      if (r.count > 1) ...[
                        const SizedBox(width: 3),
                        Text('${r.count}', style: tkText(size: 11.5, weight: 600, lineHeight: 1, color: r.mine == true ? t.tintText : t.muted)),
                      ],
                    ],
                  ),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
