// A person's chats with businesses, on **You** (docs/spec/taskiem-for-everyone.md
// §7, §8.3, §14, §15): the list (`GET /v1/me/chats`, unread, archived) and
// one conversation through the customer door (`/v1/me/chats/{id}…`): words,
// photos, voice notes and files, read ticks and typing; the business's
// hours under its name; the notice, said once on starting, of what the
// business sees; "Get offers from {business}?" asked once; offers marked
// Offer with **Stop offers from {business}** under them; the tools (their
// shop, Book, your orders with them, Pay); and the menu: Mute, Archive,
// offers, Block and Report.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart';
import 'book_sheet.dart' show showBookSheet;
import 'chats.dart' show ChatAvatar, ChatRow;
import 'composer.dart';
import 'door.dart';
import 'my_chat_tools.dart';
import 'thread.dart';
import 'words.dart';

/// The words for a failure, as the chats say them.
String sayFailure(Object e) => switch (e) {
  ApiError() => e.message,
  Offline() => 'You’re offline. Try again when you’re back online.',
  _ => 'Something went wrong. Try again.',
};

/// How often an open conversation reads again (CustomerChat.tsx's 4 s).
const _poll = Duration(seconds: 4);

// --- the list ---

/// Your chats with businesses: latest first, with how many are unread, and
/// the archived ones apart.
class MyChats extends ChangeNotifier {
  MyChats(this.auth);
  final Auth auth;

  List<PersonChat>? chats;
  List<PersonChat> archived = const [];
  int unread = 0;
  String? error;
  bool _disposed = false;

  Future<void> load() async {
    try {
      final r = await auth.api.listMyChats();
      chats = r.chats.where((c) => !c.archived).toList();
      unread = r.unread;
      error = null;
      // The archived ones, for their row; one failing leaves the rest.
      try {
        archived = (await auth.api.listMyChats(archived: '1')).chats.where((c) => c.archived).toList();
      } on Object {
        // Read again next time.
      }
    } on ApiError catch (e) {
      // A server without chats yet answers 404: none to show.
      if (e.status == 404) {
        chats = const [];
      } else {
        error = e.message;
      }
    } on Object catch (e) {
      error = sayFailure(e);
    }
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}

/// A chat's line in the list: theirs, or yours with "You:".
String myChatPreview(PersonChat c) {
  if (c.blocked) return 'You blocked them';
  if (c.last.isEmpty) return 'No messages yet';
  return '${c.lastMine ? 'You: ' : ''}${c.last}';
}

/// The rows of your chats (You's Chats, and Archived).
class MyChatRows extends StatelessWidget {
  const MyChatRows({super.key, required this.chats, required this.now, required this.onOpen});
  final List<PersonChat> chats;
  final DateTime now;
  final ValueChanged<PersonChat> onOpen;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final c in chats)
          ChatRow(
            key: ValueKey('row my ${c.id}'),
            title: c.name,
            letter: c.name,
            time: listTime(c.lastAt, now),
            preview: Text(myChatPreview(c), maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14, color: t.muted)),
            unread: c.unread,
            muted: c.muted,
            onTap: () => onOpen(c),
          ),
      ],
    );
  }
}

/// You > Archived: the chats put away, each opened as any other.
class ArchivedChatsScreen extends StatefulWidget {
  const ArchivedChatsScreen({super.key, required this.data, required this.onOpen, this.now = DateTime.now});
  final MyChats data;
  final Future<void> Function(PersonChat c) onOpen;
  final DateTime Function() now;

  @override
  State<ArchivedChatsScreen> createState() => _ArchivedChatsScreenState();
}

class _ArchivedChatsScreenState extends State<ArchivedChatsScreen> {
  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: widget.data,
      builder: (context, _) => TkScreen(
        title: 'Archived',
        onRefresh: widget.data.load,
        children: [
          if (widget.data.archived.isEmpty)
            const TkPanel(children: [TkEmpty(icon: 'chat', title: 'No archived chats', line: 'Chats you archive show here.')])
          else
            TkPanel(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              children: [MyChatRows(chats: widget.data.archived, now: widget.now(), onOpen: widget.onOpen)],
            ),
        ],
      ),
    );
  }
}

// --- one conversation ---

/// A message of the conversation; one of yours on its way is [local], with
/// its file, and [failed] with why when it could not go.
class MyLine {
  MyLine(this.m, {this.local = false, this.file});
  final PersonMessage m;
  final bool local;
  final PickedFile? file;
  bool failed = false;
  String? error;
  String get id => m.id;
}

List<MyLine> _merge(List<MyLine> cur, List<MyLine> more) {
  final by = {for (final m in cur) m.id: m};
  for (final m in more) {
    by[m.id] = m;
  }
  return by.values.toList()
    ..sort((a, b) {
      final t = a.m.createdAt.compareTo(b.m.createdAt);
      return t != 0 ? t : a.id.compareTo(b.id);
    });
}

/// What a person's conversation with a business shows, read every few
/// seconds while it is open.
class MyChatData extends ChangeNotifier {
  MyChatData(this.auth, this.id, {this.follow = true, this.notice, DateTime Function()? now}) : now = now ?? DateTime.now;
  final Auth auth;
  final String id;
  final bool follow;
  final DateTime Function() now;

  /// Said once, on starting: what the business sees of you, and that
  /// Taskiem may read business conversations (§14). Put away with OK.
  String? notice;

  TaskiemApi get api => auth.api;
  String get base => '/v1/me/chats/${Uri.encodeComponent(id)}';

  PersonChat? chat;
  List<MyLine>? messages;
  bool hasOlder = false;
  String? error;
  bool offline = false;
  Timer? _timer;
  bool _disposed = false;
  int _localN = 0;
  bool _loadingOlder = false;
  DateTime _typedAt = DateTime(2000);

  /// Your copy's files: photos, voice notes and PDFs, fetched as shown.
  late final ChatFiles files = ChatFiles(auth.transport, pathOf: (m) => '$base/files/${Uri.encodeComponent(m.id)}');

  /// The words given back to the box when a message could not go.
  final _back = StreamController<String>.broadcast();
  Stream<String> get givenBack => _back.stream;

  String get name => chat?.name ?? 'Business';

  Future<void> open() async {
    await Future.wait([_chat(), _pull(first: true)]);
    if (follow && !_disposed) {
      _timer = Timer.periodic(_poll, (_) {
        _pull(first: false);
        _chat();
      });
    }
  }

  Future<void> refresh() => Future.wait([_chat(), _pull(first: false)]);

  Future<void> _chat() async {
    try {
      chat = await api.getMyChat(id: id);
      _changed();
    } on Object catch (e) {
      _fail(e);
    }
  }

  Future<void> _pull({required bool first}) async {
    try {
      final out = await api.listMyChatMessages(id: id);
      if (_disposed) return;
      final page = [for (final m in out.messages) MyLine(m)];
      if (first) hasOlder = out.more;
      final from = page.isEmpty ? null : page.first.m.createdAt;
      final kept = (messages ?? const <MyLine>[]).where((m) => m.local || (hasOlder && from != null && m.m.createdAt.isBefore(from))).toList();
      messages = _merge(kept, page);
      offline = false;
      error = null;
      _changed();
      if (out.messages.any((m) => !m.mine && m.readAt == null)) await api.readMyChat(id: id);
    } on Object catch (e) {
      _fail(e);
    }
  }

  /// The page before the oldest shown.
  Future<void> older() async {
    final oldest = messages?.where((m) => !m.local).firstOrNull;
    if (oldest == null || _loadingOlder) return;
    _loadingOlder = true;
    try {
      final out = await api.listMyChatMessages(id: id, before: oldest.id);
      if (_disposed) return;
      messages = _merge(messages ?? const [], [for (final m in out.messages) MyLine(m)]);
      hasOlder = out.more;
      _changed();
    } on Object catch (e) {
      _fail(e);
    } finally {
      _loadingOlder = false;
    }
  }

  void _fail(Object e) {
    if (_disposed) return;
    if (e is Offline && messages != null) {
      offline = true;
    } else {
      error = e is Offline ? 'You’re offline. The conversation shows when you’re back online.' : sayFailure(e);
    }
    _changed();
  }

  /// You are typing: said to the business at most every 3 seconds.
  void typing() {
    final at = now();
    if (at.difference(_typedAt) < const Duration(seconds: 3)) return;
    _typedAt = at;
    unawaited(api.typingInMyChat(id: id).catchError((Object _) {}));
  }

  /// Words, or a file with its caption: shown at once with a clock, then
  /// the server's copy; not sent, the words come back to the box.
  Future<void> send(String text, {PickedFile? file}) async {
    final words = text.trim();
    if (words.isEmpty && file == null) return;
    final client = '${now().millisecondsSinceEpoch}-${++_localN}';
    final mine = MyLine(
      PersonMessage(
        id: 'local-$client',
        mine: true,
        kind: 'text',
        body: words,
        createdAt: now(),
        attachment: file == null ? null : ChatAttachment(kind: file.photo ? 'photo' : (file.type.startsWith('audio/') ? 'voice' : 'file'), name: file.name, size: file.bytes.length, type: file.type),
      ),
      local: true,
      file: file,
    );
    messages = _merge(messages ?? const [], [mine]);
    error = null;
    _changed();
    try {
      final PersonMessage sent;
      if (file != null) {
        final j = await auth.transport.upload('$base/files', bytes: file.bytes, filename: file.name, type: file.type, fields: {'client_id': client, if (words.isNotEmpty) 'text': words});
        sent = PersonMessage.fromJson((j as Map).cast<String, dynamic>());
        unawaited(files.keep(sent.id, Uint8List.fromList(file.bytes)));
      } else {
        sent = await api.sendMyChatMessage(id: id, body: SendMyChatMessageRequest(text: words, clientId: client));
      }
      messages = _merge((messages ?? const []).where((m) => m != mine).toList(), [MyLine(sent)]);
    } on Object catch (e) {
      messages = messages?.where((m) => m != mine).toList();
      if (!_back.isClosed && words.isNotEmpty) _back.add(words);
      error = e is ApiError && e.status == 429 ? 'You are sending too fast. Wait a moment and try again.' : sayFailure(e);
      unawaited(_chat());
    }
    _changed();
  }

  /// Your settings with them: mute, archive, offers. False if refused.
  Future<bool> put({bool? muted, bool? archived, bool? offers}) async {
    try {
      chat = await api.putMyChat(id: id, body: PutMyChatRequest(muted: muted, archived: archived, offers: offers));
      _changed();
      return true;
    } on Object catch (e) {
      _fail(e);
      return false;
    }
  }

  /// Block them (they can no longer write to you; nothing is deleted), or
  /// unblock.
  Future<bool> block(bool on) async {
    try {
      chat = on ? await api.blockBusiness(id: id) : await api.unblockBusiness(id: id);
      _changed();
      return true;
    } on Object catch (e) {
      _fail(e);
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
    _back.close();
    files.dispose();
    super.dispose();
  }
}

/// The conversation with a business, on You.
class MyChatScreen extends StatefulWidget {
  const MyChatScreen({super.key, required this.data, this.onBack, this.draft = '', this.pickers = const Pickers(), this.recorder});
  final MyChatData data;
  final VoidCallback? onBack;

  /// The phone's photos and files, and the voice recorder (a test's own).
  final Pickers pickers;
  final Recorder? recorder;

  /// Words to start the box with ("About my order …").
  final String draft;

  @override
  State<MyChatScreen> createState() => _MyChatScreenState();
}

class _MyChatScreenState extends State<MyChatScreen> {
  late final _text = TextEditingController(text: widget.draft);
  StreamSubscription<String>? _back;

  MyChatData get d => widget.data;

  @override
  void initState() {
    super.initState();
    if (d.messages == null) d.open();
    _text.addListener(() {
      if (_text.text.trim().isNotEmpty) d.typing();
    });
    _back = d.givenBack.listen((w) {
      if (_text.text.isEmpty) _text.text = w;
    });
  }

  @override
  void dispose() {
    _back?.cancel();
    _text.dispose();
    // The screen owns its conversation: closed, it stops reading.
    d.dispose();
    super.dispose();
  }

  void _close() => (widget.onBack ?? () => Navigator.of(context).maybePop())();

  Future<void> _say(Future<bool> f, String done) async {
    final ok = await f;
    if (!mounted) return;
    showTkToast(context, ok ? done : (d.error ?? 'Something went wrong. Try again.'), error: !ok);
  }

  Future<void> _menu(Rect at) {
    final c = d.chat!;
    return showHeadMenu(
      context,
      at: at,
      items: [
        (c.muted ? 'Unmute notifications' : 'Mute notifications', () => _say(d.put(muted: !c.muted), c.muted ? 'Notifications on for ${c.name}' : 'Muted')),
        (
          c.archived ? 'Unarchive' : 'Archive',
          () async {
            final ok = await d.put(archived: !c.archived);
            if (!mounted) return;
            showTkToast(context, ok ? (c.archived ? 'Brought back to the chats' : 'Archived') : (d.error ?? 'Something went wrong. Try again.'), error: !ok);
            if (ok && !c.archived) _close();
          },
        ),
        if (c.offers == 'yes') ('Stop offers from ${c.name}', _stopOffers) else ('Get offers from ${c.name}', () => _say(d.put(offers: true), 'You’ll get offers from ${c.name}')),
        if (c.blocked) ('Unblock ${c.name}', () => _say(d.block(false), 'Unblocked ${c.name}')) else ('Block ${c.name}', _block),
        ('Report ${c.name}', () => showReportSheet(context, d)),
      ],
    );
  }

  Future<void> _stopOffers() => _say(d.put(offers: false), 'No more offers from ${d.name}. Messages about your orders still come.');

  Future<void> _block() async {
    final yes = await showTkConfirm(
      context,
      title: 'Block ${d.name}?',
      body: 'They can no longer write to you. Nothing is deleted: they keep their copy of this chat, for your orders and payments.',
      ok: 'Block',
      danger: true,
    );
    if (yes) await _say(d.block(true), 'Blocked ${d.name}');
  }

  /// A card's button: Pay opens its pay link in the app; Book, their
  /// services; a product, their shop; an order, your orders with them.
  (String, VoidCallback)? _cardAction(CustomerCard c) => switch (c.type) {
    'pay' when (c.url ?? '').isNotEmpty => ('Pay', () => openOrSay(context, () => openInBrowser(Uri.parse(c.url!)), 'The pay page didn’t open. Try again.')),
    'booking' => ('Book', () => showBookSheet(context, auth: d.auth, link: '', business: d.name, customer: d.base, onBooked: (_) => d.refresh())),
    'product' => ('See in their shop', () => showShopSheet(context, d)),
    'order' => ('Your orders with them', () => showMyOrdersSheet(context, d)),
    _ => null,
  };

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ListenableBuilder(
        listenable: d,
        builder: (context, _) {
          final c = d.chat;
          final name = d.name;
          final canSend = c != null && !c.blocked && !c.blockedYou;
          return Semantics(
            label: 'Conversation with $name',
            container: true,
            child: WallpaperBox(
              ground: t.chatWallpaper,
              doodle: t.chatDoodle,
              child: Column(
                children: [
                  ColoredBox(
                    color: t.panel,
                    child: Padding(
                      padding: EdgeInsets.only(top: pad.top),
                      child: ChatHeadBar(
                        onBack: widget.onBack,
                        onMore: c == null ? null : _menu,
                        avatar: ChatAvatar(name: name, size: 42, letterSize: 15),
                        title: name,
                        typing: c?.typing == true,
                        subtitle: d.offline ? 'Waiting for network…' : (c?.hours?.line ?? 'Business'),
                      ),
                    ),
                  ),
                  if (d.notice != null)
                    _Note(
                      text: d.notice!,
                      action: 'OK',
                      onAction: () => setState(() => d.notice = null),
                    ),
                  Expanded(
                    child: ThreadView<MyLine>(
                      items: d.messages,
                      of: (id: (m) => m.id, at: (m) => m.m.createdAt, mine: (m) => m.m.mine),
                      now: d.now(),
                      hasOlder: d.hasOlder,
                      onOlder: d.older,
                      errors: [?d.error],
                      empty: Text('Say hello to $name. Ask about what they sell, or order and book with the button by the box.', textAlign: TextAlign.center, style: tkHint(t)),
                      render: (x, prev) {
                        final m = x.m;
                        final first = prev == null || prev.m.mine != m.mine;
                        final meta = m.mine ? t.chatOutMeta : t.muted;
                        final act = m.card == null ? null : _cardAction(m.card!);
                        return Padding(
                          padding: EdgeInsets.only(top: first ? 6 : 0),
                          child: DoorBubble(
                            id: m.id,
                            mine: m.mine,
                            first: first,
                            body: m.body,
                            at: m.createdAt,
                            files: d.files,
                            other: name,
                            card: m.card,
                            attachment: m.attachment,
                            local: x.file,
                            offer: m.offer == true,
                            cardAction: act?.$1,
                            onCard: act?.$2,
                            under: m.offer == true && c?.offers == 'yes'
                                ? Padding(padding: const EdgeInsets.only(top: 2), child: TkLinkButton(label: 'Stop offers from $name', onPressed: _stopOffers))
                                : null,
                            stamp: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Text(clock(m.createdAt), style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
                                if (m.mine) ...[const SizedBox(width: 4), DoorTicks(sending: x.local, read: m.readAt != null, meta: meta)],
                              ],
                            ),
                          ),
                        );
                      },
                    ),
                  ),
                  if (c != null && c.askOffers && c.offers == 'unasked') _OffersQuestion(name: name, onAnswer: (yes) => d.put(offers: yes)),
                  if (c != null && c.blocked)
                    _Note(text: 'You blocked $name. They can’t write to you, and you can’t write to them.', action: 'Unblock', onAction: () => _say(d.block(false), 'Unblocked $name')),
                  if (c != null && !c.blocked && c.blockedYou) _Note(text: '$name is not taking messages from you.'),
                  if (canSend)
                    Composer(
                      text: _text,
                      bottom: pad.bottom,
                      rules: doorFiles,
                      label: 'Message $name',
                      pickers: widget.pickers,
                      recorder: widget.recorder,
                      onTools: () => showMyChatTools(context, d),
                      onSend: (text, file) => d.send(text, file: file),
                    ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

/// "Get offers from {business}?", asked once (§15): only Yes gets offers.
class _OffersQuestion extends StatefulWidget {
  const _OffersQuestion({required this.name, required this.onAnswer});
  final String name;
  final Future<bool> Function(bool yes) onAnswer;

  @override
  State<_OffersQuestion> createState() => _OffersQuestionState();
}

class _OffersQuestionState extends State<_OffersQuestion> {
  bool _busy = false;

  Future<void> _answer(bool yes) async {
    setState(() => _busy = true);
    await widget.onAnswer(yes);
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      child: Container(
        margin: const EdgeInsets.fromLTRB(12, 0, 12, 8),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(12), border: Border.all(color: t.border)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('Get offers from ${widget.name}?', style: tkText(size: 15, weight: 600, color: t.text)),
            const SizedBox(height: 2),
            Text('At most 2 a week. Messages about your orders come either way. You can change this in the menu.', style: tkHint(t)),
            const SizedBox(height: 10),
            Row(
              children: [
                Expanded(child: TkButton(label: 'Yes', primary: true, enabled: !_busy, onPressed: () => _answer(true))),
                const SizedBox(width: 8),
                Expanded(child: TkButton(label: 'No', enabled: !_busy, onPressed: () => _answer(false))),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// A line over the box: what is so, and what to do about it.
class _Note extends StatelessWidget {
  const _Note({required this.text, this.action, this.onAction});
  final String text;
  final String? action;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      liveRegion: true,
      child: Container(
        margin: const EdgeInsets.fromLTRB(12, 8, 12, 8),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(color: t.tkWarnBg, borderRadius: BorderRadius.circular(12)),
        child: Row(
          children: [
            Expanded(child: Text(text, style: tkText(size: 13, lineHeight: 1.4, color: t.tkWarnInk))),
            if (action != null && onAction != null) ...[const SizedBox(width: 8), TkSmallButton(label: action!, onPressed: onAction)],
          ],
        ),
      ),
    );
  }
}

// --- Report ---

/// Report (§15): why, a note, and consent to send the last 20 messages to
/// Taskiem's moderators.
Future<void> showReportSheet(BuildContext context, MyChatData d) => showTkSheet<void>(context, (_) => _ReportSheet(d: d));

class _ReportSheet extends StatefulWidget {
  const _ReportSheet({required this.d});
  final MyChatData d;

  @override
  State<_ReportSheet> createState() => _ReportSheetState();
}

class _ReportSheetState extends State<_ReportSheet> {
  static const _reasons = [('spam', 'Spam'), ('scam', 'A scam or fraud'), ('abuse', 'Abuse or threats'), ('other', 'Something else')];
  String? _reason;
  bool _consent = false;
  bool _busy = false;
  String? _error;
  String? _done;
  final _note = TextEditingController();

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final r = _reason;
    if (r == null) return setState(() => _error = 'Choose why you are reporting them.');
    if (!_consent) return setState(() => _error = 'Agree to send the last 20 messages, so the moderators can see what happened.');
    setState(() => (_busy = true, _error = null));
    try {
      final out = await widget.d.api.reportBusiness(id: widget.d.id, body: ReportBusinessRequest(reason: r, note: _note.text.trim().isEmpty ? null : _note.text.trim(), consent: true));
      if (mounted) setState(() => _done = out.said);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final name = widget.d.name;
    if (_done != null) {
      return TkSheet(
        title: 'Report sent',
        body: TkFormBody(children: [Semantics(liveRegion: true, child: Text(_done!, style: tkText(size: 15, color: t.text)))]),
        footer: TkCta(label: 'Done', onTap: () => Navigator.of(context).maybePop()),
      );
    }
    return TkSheet(
      title: 'Report $name',
      footer: TkCta(label: _busy ? 'Sending…' : 'Send report', danger: true, onTap: _busy ? null : _send),
      body: TkFormBody(
        children: [
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final (k, words) in _reasons)
                TkChoiceRow(control: TkRadio(checked: _reason == k), label: Text(words, style: tkText(size: 15, color: t.text)), onTap: () => setState(() => _reason = k)),
            ],
          ),
          TkKitField(label: 'What happened', hint: 'Optional', child: TkTextBox(controller: _note, lines: 3, max: 1000)),
          TkChoiceRow(
            radio: false,
            control: TkCheckbox(checked: _consent),
            label: Text('Send the last 20 messages of this chat to Taskiem’s moderators', style: tkText(size: 14, color: t.text)),
            onTap: () => setState(() => _consent = !_consent),
          ),
          if (_error != null) TkErrorBox(_error!),
        ],
      ),
    );
  }
}
