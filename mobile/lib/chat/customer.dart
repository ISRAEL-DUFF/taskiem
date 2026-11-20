// A customer's conversation (docs/spec/chat-five-more-flows.md, phase 1;
// web/src/pages/CustomerChat.tsx, lib/customerchat.ts): a customer who
// wrote to the business's own WhatsApp number is a thread in the chat;
// staff who handle orders answer there, and the answer goes from the
// business's number. Within 24 hours of their last message a reply goes as
// words; after, as a WhatsApp message they answer to carry on (on one
// line, up to 900 characters). A customer who sent STOP gets nothing.
//
// A customer who chats in the Taskiem app (taskiem-for-everyone §8.3) is a
// thread of the line `app`: no 24-hour window and no templates, rich
// replies (customer_app.dart), photos and files both ways, typing, and
// Block both ways ("This customer has stopped messages").
//
// One customer on both lines (taskiem-for-everyone §11): a person in the
// app whose verified phone wrote to the business's own WhatsApp number is
// one row and one thread. Each message is marked "on WhatsApp" or "in the
// app"; the head shows each line's state (STOP, Block); the box chooses the
// line a reply goes on (the line they last wrote on, unless switched), a
// line that cannot take a message is not offered and says why; on WhatsApp
// the 24 hours and the template hold; cards, offers and files go in the app.

import 'saved_picks.dart';
import 'dart:async';

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/session.dart' show uuid4;
import '../kit/icon.dart';
import '../kit/keyboard.dart';
import '../kit/kit.dart';
import '../kit/mobile.dart' show TkChips;
import '../kit/modal.dart';
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'chats.dart';
import 'attachments.dart';
import 'composer.dart' show Pickers;
import 'customer_app.dart';
import 'customer_orders.dart' show CustomerOrders;
import 'door.dart';
import 'thread.dart';
import 'words.dart';

// --- words (lib/customerchat.ts) ---

/// customerchat.ts phoneWords: "+234 803 111 2222".
String phoneWords(String phone) {
  final m = RegExp(r'^\+234(\d{3})(\d{3})(\d{4})$').firstMatch(phone);
  return m == null ? phone : '+234 ${m[1]} ${m[2]} ${m[3]}';
}

/// customerchat.ts customerTitle: their name in the book, or their number.
String customerTitle(CustomerChat c) => c.name == c.phone ? phoneWords(c.phone) : c.name;

/// customerchat.ts customerLetter: initials of a known customer, else "Customer".
String customerLetter(CustomerChat c) => c.name == c.phone || RegExp(r'^\+?\d').hasMatch(c.name) ? 'Customer' : c.name;

/// The customer's two lines, once the app and their WhatsApp are one
/// customer (§11): the thread's `lines` has both.
bool bothLines(CustomerChat c) => (c.lines?.length ?? 0) > 1;

/// One of the customer's lines (`whatsapp`, `app`), or null.
CustomerThreadLine? lineOf(CustomerChat c, String line) => c.lines?.where((l) => l.line == line).firstOrNull;

/// Why [l] cannot take a reply now, in a few words (on the head and the
/// chooser), or null when it can.
String? lineShut(CustomerThreadLine l) {
  if (l.phone.isEmpty) return 'Details erased';
  if (l.line == 'app') {
    if (l.blocked) return 'You blocked them';
    if (l.stopped) return l.notice == 'This customer left Taskiem' ? 'Left Taskiem' : 'Stopped messages';
    return null;
  }
  return l.stopped ? 'Sent STOP' : null;
}

/// Why the chooser does not offer [l], in a sentence (customerlines.ts
/// lineClosed, after "Not on WhatsApp:").
String lineWhy(CustomerThreadLine l) {
  final whatsapp = l.line == 'whatsapp';
  if (l.phone.isEmpty) return whatsapp ? 'Not on WhatsApp: their details there were erased.' : 'Not in the app: their details there were erased.';
  if (whatsapp) return 'Not on WhatsApp: they sent STOP. Nothing can go there until they send START.';
  if (l.blocked) return 'Not in the app: you blocked this customer there.';
  final why = (l.notice ?? 'This customer has stopped messages').replaceFirst(RegExp(r'\.$'), '');
  return 'Not in the app: ${why.isEmpty ? why : why[0].toLowerCase() + why.substring(1)}.';
}

/// customerlines.ts lineMoved: said when the line a reply was being written
/// for shut while it was written, and it now goes on [now]; never moved
/// quietly.
String movedWords(String now, String wanted) => 'Now goes ${lineWords(now)}: it can’t go ${lineWords(wanted)} now.';

/// A line's state on the head: why it is shut, or how a reply goes there.
String lineState(CustomerThreadLine l) =>
    lineShut(l) ??
    (l.line == 'whatsapp' && !l.windowOpen ? 'More than a day since they wrote' : 'Open');

/// What the box says about a reply on [l], one of both lines (§11), in the
/// web's words (customerlines.ts lineNote).
({bool stop, String text})? lineNote(CustomerThreadLine l) {
  if (l.line == 'app') {
    if (l.phone.isEmpty) return (stop: true, text: 'This customer’s details were erased.');
    if (l.blocked) return (stop: true, text: 'You blocked this customer. Unblock them to write to them.');
    if (l.stopped) return (stop: true, text: l.notice ?? 'This customer has stopped messages.');
    return null;
  }
  if (l.stopped) return (stop: true, text: 'They asked not to get messages (they sent STOP). Nothing can be sent until they send START.');
  if (l.phone.isEmpty) return (stop: true, text: 'This customer’s details were erased.');
  if (!l.windowOpen) {
    return (stop: false, text: 'More than a day since they last wrote: your reply goes as a WhatsApp message they answer to carry on.');
  }
  // A thread parted from the app when its customer moved their number.
  if ((l.notice ?? '').isNotEmpty) return (stop: false, text: '${l.notice!.replaceFirst(RegExp(r'\.$'), '')}.');
  return null;
}

/// Each bubble says its line once the thread has both lines or holds
/// messages of both (CustomerChat.tsx markLines).
bool marksLines(CustomerChat? c, List<CustomerLine>? messages) =>
    (c != null && bothLines(c)) || {for (final m in messages ?? const <CustomerLine>[]) ?m.m.line}.length > 1;

/// customerchat.ts replyNote: what the box says about how a reply goes.
({bool stop, String text})? replyNote(CustomerChat c) {
  if (c.line == 'app') {
    if (c.phone.isEmpty) return (stop: true, text: 'This customer’s details were erased, so this conversation is closed.');
    if (c.blocked == true) return (stop: true, text: 'You blocked this customer. Unblock them to write to them.');
    if (c.stopped) return (stop: true, text: c.notice ?? 'This customer has stopped messages');
    return null;
  }
  if (c.stopped) return (stop: true, text: 'They asked not to get messages (they sent STOP). Nothing can be sent until they send START.');
  if (c.phone.isEmpty) return (stop: true, text: 'This customer’s details were erased.');
  if (!c.windowOpen) {
    return (stop: false, text: 'More than a day since they last wrote: your reply goes as a WhatsApp message they answer to carry on.');
  }
  // Their app line moved to a new number (§5.5): this thread stays under
  // the old one.
  if ((c.notice ?? '').isNotEmpty) return (stop: false, text: c.notice!);
  return null;
}

/// customerchat.ts owesWords: "Owes ₦12,500 on 2 invoices".
String owesWords(int? kobo, int? invoices) {
  if (kobo == null || kobo <= 0) return '';
  final n = invoices ?? 0;
  return 'Owes ${naira(kobo)}${n > 0 ? ' on $n invoice${n == 1 ? '' : 's'}' : ''}';
}

/// customerchat.ts LATE_REPLY_MAX, lateLength, lateTooLong.
const lateReplyMax = 900;
int lateLength(String text) => text.trim().split(RegExp(r'\s+')).join(' ').runes.length;
bool lateTooLong(String text, bool windowOpen) => !windowOpen && lateLength(text) > lateReplyMax;

/// customerchat.ts mergeCustomer: by id, oldest first.
List<CustomerLine> mergeCustomer(List<CustomerLine> cur, List<CustomerLine> more) {
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

/// customerchat.ts latestPage: the latest page in place of what it covers;
/// older ones kept when there are more, and sends under way.
List<CustomerLine> latestPage(List<CustomerLine> cur, List<CustomerLine> page, bool more) {
  final from = page.isEmpty ? null : page.first.m.createdAt;
  final kept = cur.where((m) => m.local || (more && from != null && m.m.createdAt.isBefore(from))).toList();
  return mergeCustomer(kept, page);
}

/// A message of the thread; a reply of ours on its way is [local], with
/// its file.
class CustomerLine {
  CustomerLine(this.m, {this.local = false, this.file});
  final CustomerMessage m;
  final bool local;
  final PickedFile? file;
  String get id => m.id;
}

/// CustomerChat.tsx POLL_MS.
const _poll = Duration(seconds: 4);

/// What a customer's conversation shows.
class CustomerData extends ChangeNotifier {
  CustomerData(this.chats, this.id, {this.follow = true});
  final ChatsData chats;
  final String id;
  final bool follow;

  TaskiemApi get api => chats.api;
  CustomerChat? chat;
  List<CustomerLine>? messages;
  bool busy = false;
  String? error;
  Timer? _timer;
  bool _disposed = false;
  int _localN = 0;

  /// The words given back to the box when a reply could not go.
  final _back = StreamController<String>.broadcast();
  Stream<String> get givenBack => _back.stream;

  /// Offline: what shows is what the phone kept.
  bool offline = false;

  String get _key => 'customer:$id';

  /// The customer's own id, whichever line's id opened them: the thread's
  /// as the server says it (a pair's is the app thread's). Their draft and
  /// their notifications' tag (`c-{id}`) go by it.
  String get threadId => chat?.id ?? id;

  /// The customer chats in the Taskiem app (not on WhatsApp only).
  bool get app => chat?.line == 'app';

  /// The customer is on both lines (§11).
  bool get both => chat != null && bothLines(chat!);

  /// Whether a message can go on [line] now (`app`, `whatsapp`; null: the
  /// thread's reply line).
  bool reaches(String? line) {
    final c = chat;
    if (c == null) return false;
    final l = line == null || !both ? null : lineOf(c, line);
    if (l != null) return lineShut(l) == null;
    if (line != null && both) return false;
    return !c.stopped && c.blocked != true && c.phone.isNotEmpty;
  }

  /// Whether the app line takes a message: where cards, offers and files go.
  bool get appOpen => app && reaches(both ? 'app' : null);

  /// Why words cannot go as an offer now (an offer goes only in the app, to
  /// a customer who said yes), or null when they can (customerlines.ts
  /// offerRefused). It is never sent as a plain message instead.
  String? offerRefused() {
    const back = 'Your words are still in the box: send them again to send them as a message.';
    if (!appOpen) return 'Not sent: an offer goes in the app, and it can’t take a message now. $back';
    if (chat?.offers != 'yes') return 'Not sent: this customer no longer takes offers. $back';
    return null;
  }

  /// The thread's photos, voice notes and PDFs (app customers'), fetched
  /// as shown.
  late final ChatFiles files = ChatFiles(chats.auth.transport, pathOf: (m) => '/v1/customer-chats/${Uri.encodeComponent(id)}/files/${Uri.encodeComponent(m.id)}');
  DateTime _typedAt = DateTime(2000);

  /// Staff typing, told to an app customer at most every 3 seconds; never
  /// while either side has blocked the other (the server refuses it), nor
  /// while the reply is for WhatsApp ([line]).
  void typing({String? line}) {
    if (!appOpen || (both && line == 'whatsapp')) return;
    final at = chats.now();
    if (at.difference(_typedAt) < const Duration(seconds: 3)) return;
    _typedAt = at;
    api.customerChatTyping(id: id).catchError((Object _) {});
  }

  /// The conversation as the phone kept it: shown at once.
  void _restore() {
    final local = chats.auth.local;
    if (local == null) return;
    try {
      final kept = [for (final j in local.messages(_key)) CustomerLine(CustomerMessage.fromJson(j))];
      if (kept.isNotEmpty && messages == null) {
        messages = kept;
        hasOlder = true;
      }
      final c = local.read('$_key:chat');
      if (c is Map && chat == null) chat = CustomerChat.fromJson(c.cast<String, dynamic>());
      _changed();
    } on Object {
      // Kept by an older app, or damaged: the server's answer will do.
    }
  }

  void _keep() {
    final local = chats.auth.local;
    final list = messages?.where((x) => !x.local).toList();
    if (local == null || list == null) return;
    try {
      local.keepMessages(_key, [for (final x in list.length > 200 ? list.sublist(list.length - 200) : list) x.m.toJson()]);
      if (chat != null) local.write('$_key:chat', chat!.toJson());
    } on Object {
      // Not kept this time.
    }
  }

  Future<void> open() async {
    _restore();
    await Future.wait([_chat(), _pull(first: true)]);
    // Closed before the first page came: nothing to follow.
    if (follow && !_disposed) {
      _timer = Timer.periodic(_poll, (_) {
        _pull(first: false);
        _chat();
      });
    }
  }

  /// The thread's state again (as the poll reads it).
  Future<void> reload() => _chat();

  bool _reading = false;
  bool _pulling = false;

  Future<void> _chat() async {
    if (_reading) return;
    _reading = true;
    try {
      final c = await api.getCustomerChat(id: id);
      if (_disposed) return;
      chat = c;
      _changed();
    } on Object catch (e) {
      _fail(e);
    } finally {
      _reading = false;
    }
  }

  /// There are older messages than those shown (CustomerChat.tsx).
  bool hasOlder = false;
  bool _loadingOlder = false;

  /// The page before the oldest shown.
  Future<void> older() async {
    final oldest = messages?.where((m) => !m.local).firstOrNull;
    if (oldest == null || _loadingOlder) return;
    _loadingOlder = true;
    try {
      final out = await api.listCustomerMessages(id: id, before: oldest.m.id);
      if (_disposed) return;
      messages = mergeCustomer(messages ?? const [], [for (final m in out.messages) CustomerLine(m)]);
      hasOlder = out.more;
      _changed();
    } on Object catch (e) {
      _fail(e);
    } finally {
      _loadingOlder = false;
    }
  }

  /// The latest page; the customer's messages read as they are shown.
  Future<void> _pull({required bool first}) async {
    // One read at a time: a slow answer never lands over a newer one.
    if (_pulling && !first) return;
    _pulling = true;
    try {
      final out = await api.listCustomerMessages(id: id);
      if (_disposed) return;
      final page = [for (final m in out.messages) CustomerLine(m)];
      if (first) hasOlder = out.more;
      messages = first ? page : latestPage(messages ?? const [], page, out.more);
      offline = false;
      error = null;
      _keep();
      _changed();
      if (out.messages.any((m) => !m.mine && m.readAt == null)) {
        await api.readCustomerChat(id: id);
        unawaited(chats.load());
      }
    } on Object catch (e) {
      _fail(e);
    } finally {
      _pulling = false;
    }
  }

  void _fail(Object e) {
    if (_disposed) return;
    if (e is Offline && messages != null) {
      // What the phone kept shows; said quietly.
      offline = true;
    } else {
      error = e is ApiError ? e.message : (e is Offline ? 'You’re offline. The conversation shows when you’re back online.' : 'Something went wrong. Try again.');
    }
    _changed();
  }

  /// A reply: shown at once with a clock, then the server's copy; not sent,
  /// the words come back to the box.
  ///
  /// To a customer on both lines, [line] chooses where it goes (`whatsapp`,
  /// `app`); left out, the thread's reply line. An offer goes in the app.
  Future<void> send(String text, {bool offer = false, String? line}) async {
    final words = text.trim();
    if (words.isEmpty || (offer && offerRefused() != null)) return;
    final on = !both ? null : (offer ? 'app' : line);
    await _reply(
      CustomerMessage(id: '', mine: true, kind: 'text', body: words, offer: offer ? true : null, line: on ?? (both ? chat?.replyLine : null), createdAt: chats.now()),
      (client) => api.replyCustomer(id: id, body: ReplyCustomerRequest(text: words, clientId: client, offer: offer ? true : null, line: offer ? null : on)),
      words: words,
      line: on,
      same: 'words:${offer ? 'offer:' : ''}${on ?? ''}:$words',
    );
  }

  /// A card to an app customer: an order, a pay link, an invoice, a
  /// delivery, a booking offer or a product, made by the server from the
  /// business's own records.
  Future<void> sendCard(ReplyCustomerRequestCard card) => _reply(
    CustomerMessage(id: '', mine: true, kind: 'card', body: '', line: both ? 'app' : null, createdAt: chats.now()),
    (client) => api.replyCustomer(id: id, body: ReplyCustomerRequest(card: card, clientId: client)),
    line: both ? 'app' : null,
    same: 'card:${card.type}:${card.reference ?? ''}:${card.invoiceId ?? ''}:${card.serviceId ?? ''}:${card.itemId ?? ''}',
  );

  /// A photo, voice note or PDF to an app customer.
  Future<void> sendFile(PickedFile f) => _reply(
    CustomerMessage(
      id: '',
      mine: true,
      kind: 'text',
      body: '',
      createdAt: chats.now(),
      line: both ? 'app' : null,
      attachment: ChatAttachment(kind: f.photo ? 'photo' : (f.type.startsWith('audio/') ? 'voice' : 'file'), name: f.name, size: f.bytes.length, type: f.type),
    ),
    line: both ? 'app' : null,
    (client) async {
      final j = await chats.auth.transport.upload('/v1/customer-chats/${Uri.encodeComponent(id)}/files', bytes: f.bytes, filename: f.name, type: f.type, fields: {'client_id': client});
      final m = CustomerMessage.fromJson((j as Map).cast<String, dynamic>());
      await files.keep(m.id, Uint8List.fromList(f.bytes));
      return m;
    },
    file: f,
  );

  /// What went last with no answer (it may have reached the server), and
  /// its client id: the same again goes with the same id, so the server
  /// keeps one.
  ({String what, String client})? _unsure;

  Future<void> _reply(CustomerMessage shown, Future<CustomerMessage> Function(String client) go, {String? words, PickedFile? file, String? same, String? line}) async {
    if (!reaches(line) || busy) return;
    final again = _unsure;
    final client = same != null && again != null && again.what == same ? again.client : '${DateTime.now().millisecondsSinceEpoch}-${++_localN}-${uuid4().substring(0, 8)}';
    _unsure = null;
    final mine = CustomerLine(
      CustomerMessage(id: 'local-$client', mine: true, kind: shown.kind, body: shown.body, offer: shown.offer, attachment: shown.attachment, line: shown.line, createdAt: shown.createdAt),
      local: true,
      file: file,
    );
    messages = mergeCustomer(messages ?? const [], [mine]);
    busy = true;
    error = null;
    _changed();
    try {
      final out = await go(client);
      messages = mergeCustomer((messages ?? const []).where((m) => m != mine).toList(), [CustomerLine(out)]);
      unawaited(chats.load());
    } on Object catch (e) {
      messages = messages?.where((m) => m != mine).toList();
      // No answer (or the server's own failure): it may have arrived, so
      // the same again goes with the same id.
      if (same != null && (e is! ApiError || e.status >= 500)) _unsure = (what: same, client: client);
      // Given back only while the screen is there to take it.
      if (!_back.isClosed && words != null) _back.add(words);
      _fail(e);
      unawaited(_chat());
    } finally {
      busy = false;
      _changed();
    }
  }

  /// Block an app customer (they can no longer write; nothing is deleted),
  /// or unblock.
  Future<bool> setBlocked(bool on) async {
    try {
      on ? await api.blockCustomer(id: id) : await api.unblockCustomer(id: id);
      await _chat();
      unawaited(chats.load());
      return true;
    } on Object catch (e) {
      _fail(e);
      return false;
    }
  }

  /// Mute (its messages tell nobody: no notification, no bell), or not.
  Future<bool> setMuted(bool muted) async {
    try {
      await api.putCustomerChat(id: id, body: PutCustomerChatRequest(muted: muted));
      await _chat();
      return true;
    } on Object catch (e) {
      _fail(e);
      return false;
    }
  }

  /// Archive, or bring back to the chats; false if it could not be.
  Future<bool> setArchived(bool archived) async {
    try {
      await api.putCustomerChat(id: id, body: PutCustomerChatRequest(archived: archived));
      unawaited(chats.load());
      await _chat();
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

/// CustomerChat.tsx CustomerPane.
class CustomerScreen extends StatefulWidget {
  const CustomerScreen({super.key, required this.data, this.onBack, this.pickers = const Pickers()});
  final CustomerData data;
  final VoidCallback? onBack;

  /// The phone's photos and files (a test's own).
  final Pickers pickers;

  @override
  State<CustomerScreen> createState() => _CustomerScreenState();
}

class _CustomerScreenState extends State<CustomerScreen> {
  final _text = TextEditingController();
  // Saved texts by @name (saved objects S1): their words into the box.
  late final _saved = savedTextBox(
    text: _text,
    auth: widget.data.chats.auth,
    onChange: () {
      if (mounted) setState(() {});
    },
  );
  StreamSubscription<String>? _back;

  /// The line staff chose for the reply (both lines); the one held while
  /// the box has words (the line in effect when writing began, so it never
  /// changes quietly under them); and the one it goes on now: the chosen or
  /// held one while it takes messages, else the reply line.
  String? _line;
  String? _held;
  String? _on;

  /// Where the draft is kept: the customer's own id (`threadId`), once the
  /// server has said it.
  late String _draftKey;

  @override
  void initState() {
    super.initState();
    final d = widget.data;
    if (d.messages == null) d.open();
    // The words left unsent here, back in the box, and kept as they change.
    _draftKey = 'customer:${d.threadId}';
    _text.text = d.chats.draft(_draftKey);
    _text.addListener(_saved.typed);
    _text.addListener(() {
      d.chats.setDraft(_draftKey, _text.text);
      final writing = _text.text.trim().isNotEmpty;
      if (writing) d.typing(line: _on);
      // Writing began: the line in effect now is held.
      if (!writing) {
        _held = null;
      } else if (d.both) {
        _held ??= _on;
      }
      setState(() {});
    });
    d.addListener(_rekey);
    _back = d.givenBack.listen((w) {
      if (_text.text.isEmpty) _text.text = w;
    });
  }

  /// Opened by the WhatsApp line's id: once the server says the customer's
  /// own id, the draft moves there (shared with the list and the other id).
  void _rekey() {
    final key = 'customer:${widget.data.threadId}';
    if (key == _draftKey) return;
    final old = _draftKey;
    _draftKey = key;
    final chats = widget.data.chats;
    if (_text.text.trim().isEmpty) {
      _text.text = chats.draft(key);
    } else {
      chats.setDraft(key, _text.text);
    }
    chats.setDraft(old, '');
  }

  @override
  void dispose() {
    widget.data.removeListener(_rekey);
    _back?.cancel();
    _saved.dispose();
    _text.dispose();
    super.dispose();
  }

  void _send() {
    final words = _text.text;
    if (words.trim().isEmpty) return;
    _text.clear();
    widget.data.send(words, line: _on);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    final d = widget.data;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ListenableBuilder(
        listenable: d,
        builder: (context, _) {
          final c = d.chat;
          final app = c?.line == 'app';
          final both = d.both;
          // Both lines (§11): the reply goes on the chosen line while it
          // takes messages, else on the thread's reply line.
          String? on;
          String? moved;
          if (c != null && both) {
            final wanted = _line ?? _held;
            final want = wanted ?? c.replyLine ?? 'app';
            on = d.reaches(want) ? want : (c.replyLine ?? want);
            // The line chosen, or held while writing, shut: said, not
            // changed quietly.
            if (wanted != null && on != wanted && _text.text.trim().isNotEmpty) moved = movedWords(on, wanted);
          }
          _on = on;
          final onLine = c == null || on == null ? null : lineOf(c, on);
          final note = c == null ? null : (onLine != null ? lineNote(onLine) : replyNote(c));
          final canSend = c != null && d.reaches(on);
          final tooLong = c != null && (onLine != null ? onLine.line == 'whatsapp' && lateTooLong(_text.text, onLine.windowOpen) : !app && lateTooLong(_text.text, c.windowOpen));
          final appBlocked = (both ? lineOf(c!, 'app')?.blocked : c?.blocked) == true;
          final owes = c == null ? '' : owesWords(c.owesKobo, c.openInvoices);
          final title = c == null ? 'Customer' : customerTitle(c);
          final list = d.messages;
          final marked = marksLines(c, list);
          // The message after each: the last of a run has the tucked corner.
          final nextOf = <String, CustomerLine>{for (var i = 0; list != null && i + 1 < list.length; i++) list[i].id: list[i + 1]};
          return Semantics(
            label: 'Conversation with ${c == null ? 'a customer' : title}',
            container: true,
            child: WallpaperBox(
              ground: t.chatWallpaper,
              doodle: t.chatDoodle,
              child: Column(
                children: [
                  ColoredBox(
                    color: t.chatWallpaper,
                    child: Padding(
                      padding: EdgeInsets.only(top: pad.top),
                      child: ChatHeadBar(
                        onBack: widget.onBack,
                        // CustomerChat.tsx's menu: Archive, or Unarchive; Mute.
                        onMore: c == null
                            ? null
                            : (at) => showHeadMenu(
                                context,
                                at: at,
                                items: [
                                  (
                                    c.archived ? 'Unarchive' : 'Archive',
                                    () async {
                                      final ok = await d.setArchived(!c.archived);
                                      if (!ok || !context.mounted) return;
                                      showTkToast(context, c.archived ? 'Brought back to the chats' : 'Archived');
                                      // Archived, it leaves the open chat for the list.
                                      if (!c.archived) (widget.onBack ?? () => Navigator.of(context).maybePop())();
                                    },
                                  ),
                                  if (app)
                                    (
                                      // To a customer on both, the app line only
                                      // (CustomerChat.tsx's words).
                                      '${appBlocked ? 'Unblock' : 'Block'} this customer${both ? ' in the app' : ''}',
                                      () async {
                                        final on = !appBlocked;
                                        if (on &&
                                            !await showTkConfirm(
                                              context,
                                              title: both ? 'Block $title in the app?' : 'Block this customer?',
                                              body: both
                                                  ? 'In the app they will not be able to write to you, and you will not be able to write to them. Their WhatsApp stays as it is. Nothing is deleted.'
                                                  : 'They can no longer write to you, and you can’t write to them. Nothing is deleted.',
                                              ok: 'Block',
                                              danger: true,
                                            )) {
                                          return;
                                        }
                                        final ok = await d.setBlocked(on);
                                        if (ok && context.mounted) showTkToast(context, on ? 'Blocked' : 'Unblocked');
                                      },
                                    ),
                                  (
                                    c.muted == true ? 'Unmute notifications' : 'Mute notifications',
                                    () async {
                                      final muted = c.muted == true;
                                      final ok = await d.setMuted(!muted);
                                      if (!ok || !context.mounted) return;
                                      showTkToast(context, muted ? 'Notifications on for this customer' : 'Muted');
                                    },
                                  ),
                                ],
                              ),
                        avatar: ChatAvatar(name: c == null ? 'Customer' : customerLetter(c), size: 40, letterSize: 15),
                        title: title,
                        typing: c?.typing == true,
                        subtitle: d.offline
                            ? 'Waiting for network…'
                            : c == null
                            ? null
                            : [if (c.phone.isNotEmpty && c.phone != c.name) phoneWords(c.phone), owes, both ? 'On WhatsApp and in the app' : (app ? 'Customer in the app' : 'Customer on WhatsApp')].where((s) => s.isNotEmpty).join(' · '),
                      ),
                    ),
                  ),
                  if (c != null && both) _LineStates(lines: c.lines!),
                  // The thread and the order card share what is under the
                  // head (and over the keyboard): the card at most half, a
                  // little more while the keyboard is up (it is typed in).
                  Expanded(
                    child: LayoutBuilder(
                      builder: (context, box) => Column(
                        children: [
                        Expanded(
                          child: ThreadView<CustomerLine>(
                            items: d.messages,
                            of: (id: (m) => m.id, at: (m) => m.m.createdAt, mine: (m) => m.m.mine),
                            now: d.chats.now(),
                            hasOlder: d.hasOlder,
                            onOlder: d.older,
                            errors: [?d.error],
                            tail: app && c?.typing == true ? const TypingBubble() : null,
                            empty: Text(
                              both
                                  ? 'Nothing here yet. What this customer writes on WhatsApp or in the app shows here.'
                                  : app
                                  ? 'Nothing here yet. What this customer writes to you in the Taskiem app shows here.'
                                  : c?.stopped == true
                                  ? 'What they wrote was deleted when they sent STOP.'
                                  : 'Nothing here yet. What this customer writes to your WhatsApp number shows here.',
                              textAlign: TextAlign.center,
                              style: tkHint(t),
                            ),
                            render: (m, prev) {
                              final first = prev == null || prev.m.mine != m.m.mine || prev.m.senderName != m.m.senderName || prev.m.card != null || m.m.card != null;
                              final next = nextOf[m.id];
                              final last = next == null || next.m.mine != m.m.mine || next.m.senderName != m.m.senderName || next.m.card != null;
                              // Each message on its own line: the app's drawn as
                              // the app's, WhatsApp's as WhatsApp's.
                              final line = m.m.line ?? (app ? 'app' : 'whatsapp');
                              if (line == 'app' || m.m.card != null || m.m.attachment != null) {
                                final meta = m.m.mine && m.m.card == null ? t.chatOutMeta : t.muted;
                                return Padding(
                                  padding: EdgeInsets.only(top: first ? 6 : 0),
                                  child: DoorBubble(
                                    id: m.id,
                                    mine: m.m.mine,
                                    first: first,
                                    last: last,
                                    body: m.m.body,
                                    at: m.m.createdAt,
                                    files: d.files,
                                    other: title,
                                    sender: m.m.mine ? m.m.senderName : null,
                                    card: m.m.card,
                                    attachment: m.m.attachment,
                                    offer: m.m.offer == true,
                                    local: m.file,
                                    stamp: Row(
                                      mainAxisSize: MainAxisSize.min,
                                      children: [
                                        if (marked) LineMark(line: line, color: meta),
                                        Text(clock(m.m.createdAt), style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
                                        if (m.m.mine) ...[const SizedBox(width: 4), _Delivery(x: m, meta: meta)],
                                      ],
                                    ),
                                  ),
                                );
                              }
                              return Padding(padding: EdgeInsets.only(top: first ? 6 : 0), child: _CustomerBubble(x: m, first: first, last: last, line: marked ? line : null));
                            },
                          ),
                        ),
                        // CustomerOrders.tsx: the order read from their messages, or
                        // Start an order.
                          if (c != null)
                            CustomerOrders(
                              auth: d.chats.auth,
                              chatId: d.id,
                              canSend: canSend,
                              follow: d.follow,
                              maxHeight: box.maxHeight * (MediaQuery.viewInsetsOf(context).bottom > 0 || KeyboardRoom.up(context) ? 0.75 : 0.5),
                            ),
                        ],
                      ),
                    ),
                  ),
                  // What is said over the box, and the chooser: at most 30%
                  // of the screen, scrolled beyond (large text on a
                  // small phone), so the thread keeps its room.
                  ConstrainedBox(
                    constraints: BoxConstraints(maxHeight: MediaQuery.sizeOf(context).height * 0.3),
                    child: SingleChildScrollView(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          if (moved != null && canSend) _Note(text: moved, stop: false, key: const ValueKey('line-moved')),
                          if (note != null) _Note(text: note.text, stop: note.stop),
                          if (c != null && both && canSend)
                            _LineChooser(
                              lines: c.lines!,
                              on: on!,
                              onPick: (l) => setState(() => _line = l),
                            ),
                          if (canSend && tooLong)
                            _Note(
                              stop: true,
                              text: 'Too long to send now: after a day, a reply can be up to $lateReplyMax characters. This one is ${lateLength(_text.text)}.',
                            ),
                        ],
                      ),
                    ),
                  ),
                  if (canSend) _saved.plain(),
                  if (canSend)
                    _ReplyBox(
                      text: _text,
                      onSend: d.busy || tooLong ? null : _send,
                      bottom: pad.bottom,
                      // The box says the line a reply goes on (CustomerChat.tsx).
                      label: 'Reply to $title${on == null ? '' : ' ${lineWords(on)}'}',
                      hint: on == null ? 'Reply' : 'Reply ${lineWords(on)}',
                      onMore: d.appOpen && !d.busy ? () => showRichReply(context, d, pickers: widget.pickers, box: _text) : null,
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

/// A customer's message, or the business's reply with who sent it, and how
/// far the reply got.
class _CustomerBubble extends StatelessWidget {
  const _CustomerBubble({required this.x, required this.first, required this.last, this.line});
  final CustomerLine x;
  final bool first;
  final bool last;

  /// Its line, marked when the thread has both (§11).
  final String? line;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final m = x.m;
    final meta = m.mine ? t.chatOutMeta : t.muted;
    // Your own words white on pine in the light (ownWords' ink): read here,
    // outside the bubble, the theme's own ink would be dark on pine.
    final light = m.mine && TkTheme.of(context).mode != TkMode.dark;
    final ink = light ? const Color(0xFFFFFFFF) : t.text;
    final stamp = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (line != null) LineMark(line: line, color: meta),
        if (m.mine && m.sentAs == 'template') ...[
          Semantics(
            label: 'Sent as a WhatsApp message they answer to carry on, more than a day after they last wrote',
            excludeSemantics: true,
            child: Text('as a message to answer ·', style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
          ),
          const SizedBox(width: 4),
        ],
        Text(clock(m.createdAt), style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
        if (m.mine) ...[const SizedBox(width: 4), _Delivery(x: x, meta: meta)],
      ],
    );
    return BubbleFrame(
      mine: m.mine,
      first: first,
      last: last,
      stamp: stamp,
      // A stamp with its line or a template's words is too long to sit over
      // the end of the last line: under the words instead.
      inline: line == null && !(m.mine && m.sentAs == 'template'),
      children: [
        if (m.mine && first && (m.senderName ?? '').isNotEmpty) Text(m.senderName!, style: tkText(size: 12, weight: 600, color: light ? const Color(0xFFC8F169) : t.tintText)),
        Text.rich(
          TextSpan(
            children: [
              // CustomerChat.tsx keeps no room for the time (no `.stamp-gap`):
              // it sits over the end of the last line, as there.
              TextSpan(text: m.body, style: m.kind == 'media' ? const TextStyle(fontStyle: FontStyle.italic) : null),
            ],
          ),
          style: tkText(size: 16, lineHeight: 23 / 16, color: m.kind == 'media' ? (light ? const Color(0xB3FFFFFF) : t.muted) : ink),
        ),
        if (m.mine && m.delivery == 'failed')
          Text('Not delivered${(m.failure ?? '').isNotEmpty ? ': ${m.failure}' : '.'}', style: tkText(size: 12, weight: 600, color: t.danger)),
      ],
    );
  }
}

/// DeliveryMark: a clock while it goes, one tick once WhatsApp has it, two
/// once on their phone (blue once read), a mark when it could not be.
class _Delivery extends StatelessWidget {
  const _Delivery({required this.x, required this.meta});
  final CustomerLine x;
  final Color meta;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final d = x.m.delivery;
    if (x.local || d == 'sending') return Semantics(label: 'Sending', child: TkIcon('clock', size: 16, stroke: 2, color: meta));
    return switch (d) {
      'failed' => Semantics(label: 'Not delivered', child: Text('!', style: tkText(size: 11, weight: 700, color: t.danger))),
      'delivered' => Semantics(label: 'Delivered', child: TkIcon('tick-double', size: 16, stroke: 2, color: meta)),
      'read' => Semantics(label: 'Read', child: TkIcon('tick-double', size: 16, stroke: 2, color: t.chatTickRead)),
      _ => Semantics(label: 'Sent', child: TkIcon('tick', size: 16, stroke: 2, color: meta)),
    };
  }
}

/// Both lines' states under the head (§11): on WhatsApp and in the app,
/// each open, past its 24 hours, or why it is shut (STOP, a block).
class _LineStates extends StatelessWidget {
  const _LineStates({required this.lines});
  final List<CustomerThreadLine> lines;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final shown = lines; // The server's order: the app, then WhatsApp.
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 6, 12, 2),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(16)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final l in shown)
            Semantics(
              container: true,
              label: '${l.line == 'whatsapp' ? 'On WhatsApp' : 'In the app'}: ${lineState(l)}',
              excludeSemantics: true,
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: 2),
                child: Row(
                  children: [
                    TkIcon(l.line == 'whatsapp' ? 'b-whatsapp' : 'b-chat', size: 14, stroke: 2, color: t.muted),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text.rich(
                        TextSpan(
                          children: [
                            TextSpan(text: l.line == 'whatsapp' ? 'On WhatsApp' : 'In the app', style: tkText(size: 13, weight: 600, color: t.text)),
                            if (l.phone.isNotEmpty && l.line == 'whatsapp') TextSpan(text: ' · ${phoneWords(l.phone)}', style: tkText(size: 13, color: t.muted)),
                          ],
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Flexible(
                      child: Text(
                        lineState(l),
                        textAlign: TextAlign.end,
                        style: tkText(size: 12.5, weight: lineShut(l) == null ? 400 : 600, color: lineShut(l) == null ? t.muted : t.tkDangerInk),
                      ),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}

/// The box's line chooser (§11): On WhatsApp or In the app, the reply line
/// at first; a line that cannot take a message is not offered, and why.
class _LineChooser extends StatelessWidget {
  const _LineChooser({required this.lines, required this.on, required this.onPick});
  final List<CustomerThreadLine> lines;
  final String on;
  final ValueChanged<String> onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    // The server's order (the app, then WhatsApp), as the web's pills.
    final shown = lines;
    final shut = [for (final l in shown) if (lineShut(l) != null) l];
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // The app's own switch (TkChips), a stopped line quiet with why.
          TkChips<String>(
            label: 'Your reply goes',
            padding: EdgeInsets.zero,
            chips: [for (final l in shown) (l.line, l.line == 'whatsapp' ? 'On WhatsApp' : 'In the app')],
            on: on,
            off: {for (final l in shown) if (lineShut(l) != null) l.line: lineShut(l)!},
            onPick: onPick,
          ),
          for (final l in shut)
            Padding(
              padding: const EdgeInsets.only(top: 4, left: 4),
              child: Text(
                lineWhy(l),
                style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted),
              ),
            ),
        ],
      ),
    );
  }
}

/// `.customer-note`: how a reply goes (warn), or that it cannot (stop).
class _Note extends StatelessWidget {
  const _Note({super.key, required this.text, required this.stop});
  final String text;
  final bool stop;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Semantics(
      liveRegion: true,
      child: Container(
        margin: const EdgeInsets.fromLTRB(12, 0, 12, 8),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        // As the app's chat draws a note: on a card, red-tinted when nothing
        // can be sent, orange while a reply goes another way.
        decoration: BoxDecoration(color: (stop ? look.red : look.orange).withValues(alpha: 0.12), borderRadius: BorderRadius.circular(16)),
        child: Text(text, style: tkText(size: 13.5, lineHeight: 1.4, color: dark ? t.text : (stop ? look.redInk : look.orangeInk))),
      ),
    );
  }
}

/// CustomerPane's box: the round box alone, and Send.
class _ReplyBox extends StatelessWidget {
  const _ReplyBox({required this.text, required this.onSend, required this.bottom, required this.label, this.hint = 'Reply', this.onMore});
  final TextEditingController text;
  final VoidCallback? onSend;
  final double bottom;
  final String label;

  /// The words in the empty box: "Reply", or where it goes ("Reply in the app").
  final String hint;

  /// The + of an app customer's thread: an order, a pay link, a booking
  /// offer, a product, a photo or a file.
  final VoidCallback? onMore;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    const lift = [BoxShadow(color: Color(0x140E1A14), offset: Offset(0, 1), blurRadius: 2)];
    final ready = onSend != null && text.text.trim().isNotEmpty;
    return Container(
      // On the chat's wallpaper, as the composer.
      padding: EdgeInsets.fromLTRB(10, 8, 10, 12 + bottom),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          if (onMore != null) ...[
            TapArea(child: Semantics(
              button: true,
              label: 'Send an order, a pay link, a booking, a product or a file',
              child: TkPress(
                onTap: onMore,
                child: Container(
                  width: 46,
                  height: 46,
                  decoration: BoxDecoration(color: TkLook.of(context).card, shape: BoxShape.circle, boxShadow: lift),
                  alignment: Alignment.center,
                  child: TkIcon('plus', size: 22, stroke: 2, color: dark ? t.text : t.brand),
                ),
              ),
            )),
            const SizedBox(width: 8),
          ],
          Expanded(
            child: Container(
              constraints: const BoxConstraints(minHeight: 46),
              padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
              decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(24), boxShadow: lift),
              child: TapField(child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 10),
                child: Material(
                  type: MaterialType.transparency,
                  child: Semantics(
                    label: label,
                    textField: true,
                    child: TextField(
                      controller: text,
                      minLines: 1,
                      maxLines: 6,
                      maxLength: 2000,
                      buildCounter: (_, {required currentLength, required isFocused, maxLength}) => null,
                      textCapitalization: TextCapitalization.sentences,
                      keyboardType: TextInputType.multiline,
                      cursorColor: t.text,
                      showCursor: !TkField.debugHideCaret,
                      style: tkText(size: 15.5, lineHeight: 1.4, color: t.text),
                      decoration: InputDecoration.collapsed(hintText: hint, hintStyle: tkText(size: 15.5, lineHeight: 1.4, color: t.muted)),
                    ),
                  ),
                ),
              )),
            ),
          ),
          const SizedBox(width: 8),
          // `.send-button:disabled` keeps its look.
          TapArea(child: Semantics(
            button: true,
            enabled: ready,
            label: 'Send',
            child: TkPress(
              onTap: ready ? onSend : null,
              child: Container(
                width: 46,
                height: 46,
                decoration: BoxDecoration(color: dark ? t.brandDark : t.brand, shape: BoxShape.circle),
                alignment: Alignment.center,
                child: TkIcon('arrow-up', size: 22, stroke: 2.4, color: dark ? t.brandDarkText : t.brandDark),
              ),
            ),
          )),
        ],
      ),
    );
  }
}
