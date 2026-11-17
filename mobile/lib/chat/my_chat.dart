// A person's chats with businesses, on **You** (docs/spec/taskiem-for-everyone.md
// §7, §8.3, §14, §15): the list (`GET /v1/me/chats`, unread, archived) and
// one conversation through the customer door (`/v1/me/chats/{id}…`): words,
// photos, voice notes and files, read ticks and typing; the business's
// hours under its name; the notice, said once on starting, of what the
// business sees; "Get offers from {business}?" asked once; offers marked
// Offer with **Stop offers from {business}** under them; the tools (their
// shop, Book, your orders with them, Pay); and the menu: Mute, Archive,
// offers, Block and Report.
//
// A business with its own WhatsApp number (§11): once the person's chat
// there, from the phone on their account, is joined to this one, its
// messages show here too, marked "on WhatsApp", and the head says "Your
// WhatsApp chat with them is here too". What the person writes goes in the
// app.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../app/notify.dart' show PhoneNotifications;
import '../app/session.dart' show uuid4;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart';
import 'book_sheet.dart' show showBookSheet;
import 'cards.dart' show CardButton;
import 'chats.dart' show ChatRow;
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

/// Opens a person's chat by its id over You (its notice said once, the box
/// started with [draft], and a tool opened over it: `shop` or `book`).
typedef OpenMyChat = Future<void> Function(String id, {String? notice, String draft, String? tool});

/// The person's chat with [business]: [chat] when they have one, else
/// started (`POST /v1/me/chats {business_id, from}`), then opened with
/// [open]. A refusal is said as a toast.
///
/// One at a time for each [open] (each You): pressed again while a chat is
/// being started or is open (a second tap on Chat, Chat about this), nothing
/// more happens, so no second chat is started and none opens twice.
Future<void> chatWithBusiness(
  BuildContext context,
  Auth auth,
  OpenMyChat open, {
  required String business,
  String? chat,
  required String from,
  String draft = '',
  String? tool,
}) async {
  if (!_opening.add(open)) return;
  try {
    if (chat != null && chat.isNotEmpty) return await open(chat, draft: draft, tool: tool);
    final c = await auth.api.startMyChat(body: StartMyChatRequest(businessId: business, from: from));
    await open(c.id, notice: c.notice, draft: draft, tool: tool);
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  } finally {
    _opening.remove(open);
  }
}

/// The [OpenMyChat]s opening a chat now (a tear-off of the same home is
/// equal to itself, so one home opens one chat at a time).
final _opening = <OpenMyChat>{};

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
            avatar: c.photo?.urls == null ? null : BusinessFace(name: c.name, urls: c.photo!.urls, size: 48),
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

  /// The tag of this conversation's notifications (`you:{id}`): none shows
  /// while it is on the screen.
  String get tag => 'you:$id';

  PersonChat? chat;
  List<MyLine>? messages;
  bool hasOlder = false;
  String? error;
  bool offline = false;

  /// The business erased the person's details: the conversation is closed
  /// (the server's words).
  String? closed;

  /// What a change from the menu could not do, said once by the screen.
  String? actionError;
  Timer? _timer;
  bool _disposed = false;
  int _localN = 0;
  bool _loadingOlder = false;
  bool _pulling = false;
  bool _reading = false;
  DateTime _typedAt = DateTime(2000);

  /// The words given back after a send that may have reached the server,
  /// with the client id they went with: sent again, they go with it, so
  /// the server keeps one.
  ({String words, String client})? _unsure;

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
    if (_reading) return;
    _reading = true;
    try {
      final c = await api.getMyChat(id: id);
      if (_disposed) return;
      chat = c;
      _changed();
    } on Object catch (e) {
      _fail(e);
    } finally {
      _reading = false;
    }
  }

  Future<void> _pull({required bool first}) async {
    // One read at a time: a slow answer never lands over a newer one.
    if (_pulling && !first) return;
    _pulling = true;
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
    } finally {
      _pulling = false;
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

  /// You are typing: said to the business at most every 3 seconds; never
  /// while either side has blocked the other (the server refuses it).
  void typing() {
    final c = chat;
    if (c == null || c.blocked || c.blockedYou || closed != null) return;
    final at = now();
    if (at.difference(_typedAt).abs() < const Duration(seconds: 3)) return;
    _typedAt = at;
    unawaited(api.typingInMyChat(id: id).catchError((Object _) {}));
  }

  /// Words, or a file with its caption: shown at once with a clock, then
  /// the server's copy; not sent, the words come back to the box.
  Future<void> send(String text, {PickedFile? file}) async {
    final words = text.trim();
    if (words.isEmpty && file == null) return;
    // The same words again after an answer that never came: the same id.
    final again = _unsure;
    final client = file == null && again != null && again.words == words ? again.client : '${now().millisecondsSinceEpoch}-${++_localN}-${uuid4().substring(0, 8)}';
    _unsure = null;
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
      if (_disposed) return;
      messages = _merge((messages ?? const []).where((m) => m != mine).toList(), [MyLine(sent)]);
    } on Object catch (e) {
      if (_disposed) return;
      messages = messages?.where((m) => m != mine).toList();
      // Refused for sure (an answer came): a fresh id next time; else the
      // server may have it, and the same words go with the same id.
      if (e is! ApiError || e.status >= 500) _unsure = (words: words, client: client);
      if (!_back.isClosed && words.isNotEmpty) _back.add(words);
      if (e is ApiError && e.status == 409 && e.message.contains('erased')) {
        closed = e.message;
      } else {
        error = e is ApiError && e.status == 429 ? 'You are sending too fast. Wait a moment and try again.' : sayFailure(e);
      }
      unawaited(_chat());
    }
    _changed();
  }

  /// Your settings with them: mute, archive, offers, the offers question
  /// passed over. False if refused ([actionError] says why).
  Future<bool> put({bool? muted, bool? archived, bool? offers, bool? offersAsked}) async {
    try {
      final c = await api.putMyChat(id: id, body: PutMyChatRequest(muted: muted, archived: archived, offers: offers, offersAsked: offersAsked));
      if (_disposed) return true;
      chat = c;
      actionError = offers != null && c.offersRefused == true ? '$name is not taking messages from you, so your answer about offers was not saved.' : null;
      _changed();
      return actionError == null;
    } on Object catch (e) {
      actionError = sayFailure(e);
      _changed();
      return false;
    }
  }

  /// Block them (they can no longer write to you; nothing is deleted), or
  /// unblock.
  Future<bool> block(bool on) async {
    try {
      final c = on ? await api.blockBusiness(id: id) : await api.unblockBusiness(id: id);
      if (_disposed) return true;
      chat = c;
      actionError = null;
      _changed();
      return true;
    } on Object catch (e) {
      actionError = sayFailure(e);
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
    _back.close();
    files.dispose();
    super.dispose();
  }
}

/// The conversation with a business, on You.
class MyChatScreen extends StatefulWidget {
  const MyChatScreen({super.key, required this.data, this.onBack, this.draft = '', this.pickers = const Pickers(), this.recorder, this.notify, this.tool});
  final MyChatData data;

  /// A tool opened over the conversation once it is read: `shop` (Their
  /// shop) or `book` (Book), from Discover's profile.
  final String? tool;
  final VoidCallback? onBack;

  /// The phone's photos and files, and the voice recorder (a test's own).
  final Pickers pickers;
  final Recorder? recorder;

  /// Words to start the box with ("About my order …").
  final String draft;

  /// Notifications on this phone: this conversation's show nothing while
  /// it is on the screen.
  final PhoneNotifications? notify;

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
    final opening = d.messages == null ? d.open() : Future<void>.value();
    final tool = widget.tool;
    if (tool != null) {
      opening.then((_) {
        if (!mounted || d.chat == null) return;
        if (tool == 'shop') {
          showShopSheet(context, d);
        } else if (tool == 'book') {
          showBookSheet(context, auth: d.auth, link: '', business: d.name, customer: d.base, onBooked: (_) => d.refresh());
        }
      });
    }
    widget.notify?.show(d.tag, this);
    _text.addListener(() {
      if (_text.text.trim().isNotEmpty) d.typing();
    });
    _back = d.givenBack.listen((w) {
      if (_text.text.isEmpty) _text.text = w;
    });
  }

  @override
  void dispose() {
    widget.notify?.unshow(this);
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
    showTkToast(context, ok ? done : (d.actionError ?? 'Something went wrong. Try again.'), error: !ok);
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
            showTkToast(context, ok ? (c.archived ? 'Brought back to the chats' : 'Archived') : (d.actionError ?? 'Something went wrong. Try again.'), error: !ok);
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

  void _openUrl(String url, String failed) => openOrSay(context, () => openInBrowser(Uri.parse(url)), failed);

  /// A card's button: Pay opens its pay link (an invoice's page) in the
  /// app; Book, that service; a product, their shop; an order or a
  /// delivery, your orders with them.
  (String, VoidCallback)? _cardAction(CustomerCard c) => switch (c.type) {
    'pay' when (c.url ?? '').isNotEmpty => ('Pay', () => _openUrl(c.url!, 'The pay page didn’t open. Try again.')),
    'invoice' when (c.url ?? '').isNotEmpty => ((c.totalKobo ?? 0) > 0 && c.status != 'paid' ? 'Pay' : 'See the invoice', () => _openUrl(c.url!, 'The invoice didn’t open. Try again.')),
    'booking' => ('Book', () => showBookSheet(context, auth: d.auth, link: '', business: d.name, customer: d.base, service: c.serviceId, onBooked: (_) => d.refresh())),
    'product' => ('See in their shop', () => showShopSheet(context, d)),
    'order' || 'delivery' => ('Your orders with them', () => showMyOrdersSheet(context, d)),
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
          final canSend = c != null && !c.blocked && !c.blockedYou && d.closed == null;
          final proved = verifiedWords(c?.verified);
          final list = d.messages;
          final now = d.now();
          // The message after each: the last of a run has the tucked corner.
          final nextOf = <String, MyLine>{for (var i = 0; list != null && i + 1 < list.length; i++) list[i].id: list[i + 1]};
          return Semantics(
            label: 'Conversation with $name',
            container: true,
            child: WallpaperBox(
              ground: t.chatWallpaper,
              doodle: t.chatDoodle,
              child: Column(
                children: [
                  ColoredBox(
                    // The head on the conversation's own ground.
                    color: t.chatWallpaper,
                    child: Padding(
                      padding: EdgeInsets.only(top: pad.top),
                      child: ChatHeadBar(
                        onBack: widget.onBack,
                        onMore: c == null ? null : _menu,
                        avatar: BusinessFace(name: name, urls: c?.photo?.urls, size: 40, letterSize: 15),
                        title: name,
                        typing: c?.typing == true,
                        subtitle: d.offline ? 'Waiting for network…' : (c?.hours?.line ?? 'Business'),
                      ),
                    ),
                  ),
                  if (c?.lines?.contains('whatsapp') == true) const _OnWhatsApp(),
                  if (proved.isNotEmpty) _Proved(words: proved),
                  if (d.notice != null)
                    _Note(
                      text: d.notice!,
                      action: 'OK',
                      onAction: () => setState(() => d.notice = null),
                    ),
                  Expanded(
                    child: ThreadView<MyLine>(
                      items: list,
                      of: (id: (m) => m.id, at: (m) => m.m.createdAt, mine: (m) => m.m.mine),
                      now: now,
                      hasOlder: d.hasOlder,
                      onOlder: d.older,
                      errors: [?d.error],
                      tail: c?.typing == true ? const TypingBubble() : null,
                      empty: Text('Say hello to $name. Ask about what they sell, or order and book with the button by the box.', textAlign: TextAlign.center, style: tkHint(t)),
                      render: (x, prev) {
                        final m = x.m;
                        final first = prev == null || prev.m.mine != m.mine || prev.m.card != null || m.card != null;
                        final next = nextOf[x.id];
                        final last = next == null || next.m.mine != m.mine || next.m.card != null || dayLabel(next.m.createdAt, now) != dayLabel(m.createdAt, now);
                        final meta = m.mine && m.card == null ? t.chatOutMeta : t.muted;
                        final act = m.card == null ? null : _cardAction(m.card!);
                        return Padding(
                          padding: EdgeInsets.only(top: first ? 6 : 0),
                          child: DoorBubble(
                            id: m.id,
                            mine: m.mine,
                            first: first,
                            last: last,
                            body: m.body,
                            at: m.createdAt,
                            files: d.files,
                            other: name,
                            card: m.card,
                            attachment: m.attachment,
                            local: x.file,
                            offer: m.offer == true,
                            warn: !m.mine && m.warnTransfer == true,
                            cardAction: act?.$1,
                            onCard: act?.$2,
                            under: m.offer == true && c?.offers == 'yes'
                                ? Padding(padding: const EdgeInsets.only(top: 2), child: TkLink(label: 'Stop offers from $name', bold: true, onTap: _stopOffers))
                                : null,
                            stamp: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                if (m.line == 'whatsapp') LineMark(line: m.line, color: meta),
                                Text(clock(m.createdAt), style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
                                if (m.mine) ...[const SizedBox(width: 4), DoorTicks(sending: x.local, read: m.readAt != null, meta: meta)],
                              ],
                            ),
                          ),
                        );
                      },
                    ),
                  ),
                  if (c != null && c.askOffers && c.offers == 'unasked' && canSend)
                    _OffersQuestion(name: name, onAnswer: (yes) => _say(d.put(offers: yes), yes ? 'You’ll get offers from $name' : 'No offers from $name'), onDismiss: () => d.put(offersAsked: true)),
                  if (d.closed != null) _Note(text: d.closed!, stop: true),
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
                    )
                  else
                    SizedBox(height: pad.bottom),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

/// Under the business's name, once their WhatsApp chat is joined to this
/// one (§11).
class _OnWhatsApp extends StatelessWidget {
  const _OnWhatsApp();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          TkIcon('b-whatsapp', size: 14, stroke: 2, color: t.muted),
          const SizedBox(width: 6),
          Flexible(child: Text('Your WhatsApp chat with them is here too', textAlign: TextAlign.center, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted))),
        ],
      ),
    );
  }
}

/// What the business has proved, under the head, in plain words (§15).
class _Proved extends StatelessWidget {
  const _Proved({required this.words});
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Semantics(
      container: true,
      label: words,
      excludeSemantics: true,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 2),
        child: Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            TkIcon('b-shield', size: 14, stroke: 2, color: look.limeInk),
            const SizedBox(width: 6),
            Flexible(child: Text(words, textAlign: TextAlign.center, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted))),
          ],
        ),
      ),
    );
  }
}

/// "Get offers from {business}?", asked once (§15): only Yes gets offers;
/// put away with ✕, it is not asked again.
class _OffersQuestion extends StatefulWidget {
  const _OffersQuestion({required this.name, required this.onAnswer, required this.onDismiss});
  final String name;
  final Future<void> Function(bool yes) onAnswer;
  final Future<void> Function() onDismiss;

  @override
  State<_OffersQuestion> createState() => _OffersQuestionState();
}

class _OffersQuestionState extends State<_OffersQuestion> {
  bool _busy = false;

  Future<void> _run(Future<void> Function() f) async {
    if (_busy) return;
    setState(() => _busy = true);
    await f();
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Semantics(
      container: true,
      explicitChildNodes: true,
      child: Container(
        margin: const EdgeInsets.fromLTRB(12, 4, 12, 8),
        padding: const EdgeInsets.fromLTRB(16, 12, 8, 14),
        decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text('Get offers from ${widget.name}?', style: tkDisplay(size: 16, lineHeight: 1.3, color: t.text)),
                  ),
                ),
                ChatHeadBar.iconButton('Not now', TkIcon('close', size: 16, stroke: 2.2, color: t.muted), _busy ? null : () => _run(widget.onDismiss)),
              ],
            ),
            Padding(
              padding: const EdgeInsets.only(right: 8, top: 2),
              child: Text('At most 2 a week. Messages about your orders come either way. You can change this in the menu.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
            ),
            const SizedBox(height: 12),
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: Row(
                children: [
                  Expanded(child: CardButton(label: 'Yes', primary: true, busy: _busy, onTap: () => _run(() => widget.onAnswer(true)))),
                  const SizedBox(width: 8),
                  Expanded(child: CardButton(label: 'No', busy: _busy, onTap: () => _run(() => widget.onAnswer(false)))),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A line over the box: what is so, and what to do about it, on a card.
class _Note extends StatelessWidget {
  const _Note({required this.text, this.action, this.onAction, this.stop = false});
  final String text;
  final String? action;
  final VoidCallback? onAction;

  /// The conversation is closed: said in red.
  final bool stop;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Semantics(
      liveRegion: true,
      child: Container(
        margin: const EdgeInsets.fromLTRB(12, 8, 12, 8),
        padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
        decoration: BoxDecoration(color: stop ? look.red.withValues(alpha: 0.12) : look.card, borderRadius: BorderRadius.circular(16)),
        child: Row(
          children: [
            Expanded(child: Text(text, style: tkText(size: 13.5, lineHeight: 1.4, color: stop && !(TkTheme.of(context).mode == TkMode.dark) ? look.redInk : t.text))),
            if (action != null && onAction != null) ...[const SizedBox(width: 8), CardButton(label: action!, primary: true, onTap: onAction)],
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
