// A chat with a person, on **You** (docs/spec/people-chatting.md §7, §8,
// §10, §11; `/v1/me/people/{id}…`), drawn as a chat with a business is:
// words, photos, voice notes and files, sent with a client id so a send
// tried again is kept once; ticks (read only while both have read ticks
// on); replies and quotes; reactions and stars; forward (to at most five
// chats, marked Forwarded, or Forwarded many times); delete for me, or for
// everyone within 24 hours; shared cards and "Your share". Its head is the
// person's card: the name you gave them, their photo and last seen as
// their settings let you see them, and their number only if they show it.
//
// A request (a stranger's first message) opens here too, without telling
// the sender: Accept, Delete, or Block and report. The warning shows under
// a stranger's message that the checks marked. A message held by the
// checks is not sent, and the server's words say why.
//
// People chats are kept by Taskiem and readable by its servers; the info
// says so plainly (people.dart, peopleKeptWords) and nothing says they are
// end-to-end encrypted.

import 'saved_picks.dart';
import 'dart:async';
import 'dart:convert' show jsonEncode;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/notify.dart' show PhoneNotifications;
import '../app/phone.dart' show showPhone;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart';
import 'cards.dart' show CardButton, cardDecoration;
import 'composer.dart';
import 'door.dart' show doorFileMessage, doorFiles;
import 'menu.dart';
import 'my_chat.dart' show OpenMyChat, sayFailure;
import 'group_chat.dart' show GroupReactionChips;
import 'groups.dart' show ChatsAndGroups, askSure, forwardWords, pickAction, pickChatsAndGroups;
import 'people.dart';
import 'basket.dart';
import 'people_cards.dart';
import 'people_live.dart';
import 'people_place.dart';
import 'people_search.dart';
import 'people_standing.dart';
import 'saved_card.dart';
import 'thread.dart';
import 'words.dart';
import 'pick_problem.dart';
import 'save_as.dart' show photoAccountItems, saveFromWords, saveNamed;
import 'saved_files.dart' show attachSavedFile;
import 'pay_collect.dart' show PayListBar;
import 'link_preview.dart' show ComposerPreview, LinkPreviewCard, LinkedText;

/// How often an open chat reads again.
const _poll = Duration(seconds: 4);

/// How often "typing" is said while the box changes: a little under the
/// 3 seconds the server passes it on at most every, so each of its turns
/// has a fresh one and the other side's "typing…" (kept 6 seconds) does
/// not lapse while the person types.
const typingBeat = Duration(milliseconds: 2500);

/// Said over Delete for everyone (§7): what it can't undo.
const deleteForEveryoneNote = 'Delete for everyone takes it from their chat too. Copies, forwards or screenshots they made before stay.';

/// How long a message can be deleted for everyone (§7).
const deleteForEveryoneWithin = Duration(hours: 24);

/// A message of the chat; one of yours on its way is [local], with its
/// file.
class PeopleLine {
  PeopleLine(this.m, {this.local = false, this.file});
  final PeopleMessage m;
  final bool local;
  final PickedFile? file;
  String get id => m.id;
}

List<PeopleLine> _merge(List<PeopleLine> cur, List<PeopleLine> more) {
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

/// The words for a send that did not go: the server's own (a message held
/// by the checks, too many requests waiting), or the rate's.
String sendFailure(Object e) {
  if (e is ApiError && e.status == 429 && !e.body.containsKey('error')) return 'You are sending too fast. Wait a moment and try again.';
  return sayFailure(e);
}

/// What a chat with a person shows, read every few seconds while it is
/// seen (its screen polls: not while the app is put away or another screen
/// covers it).
class PeopleChatData extends ChangeNotifier {
  /// [started] is the chat as `POST /v1/me/people` answered it: a chat just
  /// opened is in no list until its first message, so it opens from that.
  ///
  /// [live] is You's live connection (by default the one You holds for
  /// [auth]): while it is open, what it tells shows at once and the chat is
  /// read again only to reconcile. [focus] opens the chat at that message
  /// (a search's hit, a starred message): older pages are read until it is
  /// there.
  PeopleChatData(this.auth, this.id, {this.follow = true, DateTime Function()? now, PeopleChat? started, PeopleLive? live, this.focus, DateTime Function()? clock})
    : now = now ?? DateTime.now,
      _clock = clock ?? DateTime.now,
      chat = started,
      live = live ?? PeopleLive.of(auth) {
    _events = this.live?.events.listen(_heard);
    this.live?.addListener(_liveChanged);
  }
  final Auth auth;
  final String id;

  /// You's live connection, when open.
  final PeopleLive? live;

  /// The message the chat opens at (or jumped to since: [jump]).
  String? focus;

  /// Counts the jumps: the thread is placed again at each.
  int focusRound = 0;

  /// Older pages are being read to reach [focus]: the thread waits.
  bool focusing = false;

  /// Real time, for the typing beat and the slow poll ([now] may be a
  /// test's fixed time).
  final DateTime Function() _clock;
  StreamSubscription<PeopleLiveEvent>? _events;
  late final _slow = SlowWhileLive(clock: _clock);
  Timer? _readSoon;

  /// Read again every few seconds while seen (the screen polls).
  final bool follow;
  final DateTime Function() now;

  /// Whether the chat is seen now: the app in front and the chat on top
  /// (its screen says). Messages are marked read only then.
  bool Function() seen = appInFront;

  TaskiemApi get api => auth.api;
  String get base => '/v1/me/people/${Uri.encodeComponent(id)}';

  /// The tag of this chat's notifications: none shows while it is open.
  String get tag => 'person:$id';

  PeopleChat? chat;
  List<PeopleLine>? messages;
  bool hasOlder = false;
  String? error;
  bool offline = false;

  /// The chat is not there (deleted, or a request deleted elsewhere).
  bool missing = false;

  /// A send was refused because the person's own account is stopped from
  /// messaging people (403): the screen points to Your standing.
  bool toStanding = false;

  /// What a change from the menu could not do, said once by the screen.
  String? actionError;
  bool _disposed = false;
  int _localN = 0;
  bool _pulling = false;
  bool _reading = false;
  bool _loadingOlder = false;

  /// Older pages were read (Load older): kept across reads of the latest
  /// page while the server has more before it; [_olderMore] is whether
  /// there are more before the oldest read.
  bool _olderLoaded = false;
  bool _olderMore = false;

  /// What was sent last without an answer (words, a file, a card), with
  /// its client id: the same sent again goes with the same id, so the
  /// server keeps one.
  ({String what, String client})? _unsure;

  late final ChatFiles files = ChatFiles(auth.transport, pathOf: (m) => '$base/files/${Uri.encodeComponent(m.id)}');

  final _back = StreamController<String>.broadcast();
  Stream<String> get givenBack => _back.stream;

  String get name => chat?.name ?? 'Chat';

  /// A request waiting for you: reading it tells the sender nothing.
  bool get isRequest => chat?.request == true;

  /// Read ticks go between you two (both have them on, §5.4).
  bool get readTicks => chat?.person?.readTicks ?? true;

  Future<void> open() async {
    final at = focus;
    if (at != null) {
      focusing = true;
      _changed();
    }
    await _chat();
    await _pull(first: true);
    if (at != null) await _reach(at);
  }

  /// Older pages until [at] is there (or none are left), then the thread
  /// placed at it.
  Future<void> _reach(String at) async {
    for (var n = 0; n < 40 && !_disposed && hasOlder && !(messages ?? const []).any((m) => m.id == at); n++) {
      final had = messages?.length ?? 0;
      await older();
      if ((messages?.length ?? 0) == had) break;
    }
    focusing = false;
    focusRound++;
    _changed();
  }

  /// To one of this chat's messages (a search's hit in it).
  Future<void> jump(String messageId) async {
    focus = messageId;
    focusing = true;
    _changed();
    await _reach(messageId);
  }

  /// Whether the message [focus] names is in the chat as read.
  bool get focusFound => focus != null && (messages ?? const []).any((m) => m.id == focus);

  /// Read again (the screen's poll, while the chat is seen): while the
  /// live connection is open, only every 30 seconds, to reconcile.
  void poll() {
    if (_disposed || missing) return;
    if (!_slow.due(live)) return;
    _chat();
    _pull(first: false);
  }

  /// The other person is typing now (the live connection says so).
  bool get typingNow => live?.typing.contains(id) == true && chat?.request != true;

  /// Their card, with what the live connection said of them since.
  PersonCard? get person {
    final p = chat?.person;
    return p == null ? null : withPresence(p, live?.presenceOf(id));
  }

  void _liveChanged() => _changed();

  /// What the live connection tells of this chat.
  void _heard(PeopleLiveEvent e) {
    if (_disposed || e.chatId != id) return;
    switch (e) {
      case PeopleLiveMessage(:final message):
        // Ours sent from here is already shown with its clock: the
        // server's copy takes its place when the send answers. A change to
        // an old message not loaded (a reaction, a read, a star, a delete,
        // or a reconnect's replay) is not merged: Load older pages from
        // the oldest loaded, which would skip what lies between.
        final cur = messages;
        if (cur == null) {
          _early.add(message);
          return;
        }
        if (!_fits(cur, message, hasOlder)) return;
        messages = _merge(cur, [PeopleLine(message)]);
        _changed();
        if (!message.mine && message.readAt == null && chat != null && seen()) {
          _readSoon?.cancel();
          _readSoon = Timer(const Duration(milliseconds: 300), () {
            if (!_disposed && seen()) unawaited(api.readPeopleChat(id: id).catchError((Object _) {}));
          });
        }
      case PeopleLiveChat(:final hidden, chat: final c):
        if (hidden) {
          missing = true;
        } else if (c != null) {
          chat = c;
        }
        _changed();
      case PeopleLiveGone(id: final gone):
        if (gone == null) {
          missing = true;
        } else {
          messages = messages?.where((m) => m.id != gone).toList();
        }
        _changed();
    }
  }

  /// You are typing: said every [typingBeat] at most while the box changes
  /// (the server passes it on no more often), and that you stopped when it
  /// empties. Never in a request, a blocked or a deleted chat, nor while
  /// your own account is stopped from messaging people ([toStanding]).
  void typing(String text) {
    final c = chat;
    if (c == null || c.request || c.waiting || c.blocked || c.gone || c.suspended == true) return;
    if (toStanding) return stopTyping();
    final now = _clock();
    if (text.trim().isEmpty) {
      if (_typingAt != null) {
        _typingAt = null;
        unawaited(api.typingPeople(id: id, body: const TypingPeopleRequest(typing: false)).catchError((Object _) {}));
      }
      return;
    }
    if (_typingAt != null && now.difference(_typingAt!) < typingBeat) return;
    _typingAt = now;
    unawaited(api.typingPeople(id: id, body: const TypingPeopleRequest(typing: true)).catchError((Object _) {}));
  }

  DateTime? _typingAt;

  /// Says you stopped typing, if you were said to be (the chat closed or
  /// the app put away with words in the box).
  void stopTyping() {
    if (_typingAt == null || _disposed) return;
    _typingAt = null;
    unawaited(api.typingPeople(id: id, body: const TypingPeopleRequest(typing: false)).catchError((Object _) {}));
  }

  /// Live messages heard before the first page was read: merged with it
  /// when they fit.
  final List<PeopleMessage> _early = [];

  /// Whether a live [m] belongs in [cur]: already loaded, or not older
  /// than the oldest loaded message from the server, or [more] says there
  /// are no older pages (the loaded ones are the whole chat).
  static bool _fits(List<PeopleLine> cur, PeopleMessage m, bool more) {
    if (!more) return true;
    if (cur.any((l) => l.id == m.id)) return true;
    final oldest = cur.where((l) => !l.local).firstOrNull;
    if (oldest == null) return false;
    return !m.createdAt.isBefore(oldest.m.createdAt);
  }

  Future<void> refresh() => Future.wait([_chat(), _pull(first: false)]);

  Future<void> _chat() async {
    if (_reading) return;
    _reading = true;
    try {
      final c = await api.getPeopleChat(id: id);
      if (_disposed) return;
      chat = c;
      _changed();
    } on ApiError catch (e) {
      if (e.status == 404) {
        missing = true;
        _changed();
      } else {
        _fail(e);
      }
    } on Object catch (e) {
      _fail(e);
    } finally {
      _reading = false;
    }
  }

  Future<void> _pull({required bool first}) async {
    if (missing) return;
    if (_pulling && !first) return;
    _pulling = true;
    try {
      final out = await api.peopleMessages(id: id);
      if (_disposed) return;
      final page = [for (final m in out.messages) PeopleLine(m)];
      // Older messages read before stay while the server has more before
      // the latest page; when it has none, the page is the whole chat.
      final from = page.isEmpty ? null : page.first.m.createdAt;
      final kept = (messages ?? const <PeopleLine>[]).where((m) => m.local || (out.more && from != null && m.m.createdAt.isBefore(from))).toList();
      if (!out.more) _olderLoaded = false;
      hasOlder = _olderLoaded ? _olderMore : out.more;
      var all = _merge(kept, page);
      if (_early.isNotEmpty) {
        final early = [for (final m in _early) if (_fits(all, m, hasOlder)) PeopleLine(m)];
        _early.clear();
        // The page read is the server's copy: what came live before it
        // and is in it already is not put back.
        early.removeWhere((l) => page.any((p) => p.id == l.id));
        all = _merge(all, early);
      }
      messages = all;
      offline = false;
      error = null;
      _changed();
      // Marked read only while the chat is seen. A request too: the server
      // tells its sender nothing until it is accepted (§7).
      if (chat != null && seen() && out.messages.any((m) => !m.mine && m.readAt == null)) await api.readPeopleChat(id: id);
    } on ApiError catch (e) {
      if (e.status == 404) {
        missing = true;
        _changed();
      } else {
        _fail(e);
      }
    } on Object catch (e) {
      _fail(e);
    } finally {
      _pulling = false;
    }
  }

  Future<void> older() async {
    final oldest = messages?.where((m) => !m.local).firstOrNull;
    if (oldest == null || _loadingOlder) return;
    _loadingOlder = true;
    try {
      final out = await api.peopleMessages(id: id, before: oldest.id);
      if (_disposed) return;
      messages = _merge(messages ?? const [], [for (final m in out.messages) PeopleLine(m)]);
      _olderLoaded = true;
      _olderMore = out.more;
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
      error = e is Offline ? 'You’re offline. The chat shows when you’re back online.' : sayFailure(e);
    }
    _changed();
  }

  /// Words, or a file with its caption, answering [reply] when given:
  /// shown at once with a clock, then the server's copy; not sent, the
  /// words come back to the box and the reason is said.
  Future<void> send(String text, {PickedFile? file, PeopleMessage? reply, Object? preview}) async {
    final words = text.trim();
    if (words.isEmpty && file == null) return;
    // A message ends typing on the server: the next key says it again.
    _typingAt = null;
    final what = file == null ? 'text:$words' : 'file:${file.name}:${file.type}:${file.bytes.length}:${_sum(file.bytes)}:$words';
    final client = _clientFor(what);
    final mine = PeopleLine(
      PeopleMessage(
        id: 'local-$client',
        mine: true,
        kind: file == null ? 'text' : 'media',
        body: words,
        createdAt: now(),
        replyTo: reply?.id,
        attachment: file == null ? null : ChatAttachment(kind: file.photo ? 'photo' : (file.type.startsWith('audio/') ? 'voice' : 'file'), name: file.name, size: file.bytes.length, type: file.type),
      ),
      local: true,
      file: file,
    );
    messages = _merge(messages ?? const [], [mine]);
    error = null;
    _changed();
    try {
      final PeopleMessage sent;
      if (file != null) {
        final j = await auth.transport.upload('$base/files', bytes: file.bytes, filename: file.name, type: file.type, fields: {
          'client_id': client,
          if (words.isNotEmpty) 'text': words,
          if (reply != null) 'reply_to': reply.id,
        });
        sent = PeopleMessage.fromJson((j as Map).cast<String, dynamic>());
        unawaited(files.keep(sent.id, Uint8List.fromList(file.bytes)));
      } else {
        sent = await api.sendPeopleMessage(id: id, body: SendPeopleMessageRequest(text: words, clientId: client, replyTo: reply?.id, preview: preview));
      }
      if (_disposed) return;
      messages = _merge((messages ?? const []).where((m) => m != mine).toList(), [PeopleLine(sent)]);
      unawaited(_chat());
    } on Object catch (e) {
      if (_disposed) return;
      messages = messages?.where((m) => m != mine).toList();
      _keepUnsure(e, what, client);
      if (!_back.isClosed && words.isNotEmpty) _back.add(words);
      error = sendFailure(e);
      toStanding = pointsToStanding(e);
      unawaited(_chat());
    }
    _changed();
  }

  /// The client id for [what]: the one it went with when it was sent last
  /// with no answer, else a new one.
  String _clientFor(String what) {
    toStanding = false;
    final again = _unsure;
    _unsure = null;
    return again != null && again.what == what ? again.client : newClientId(now(), ++_localN);
  }

  /// A send with no answer (offline, cut off, the server failing) may
  /// have reached the server: the same sent again goes with [client]. One
  /// refused for sure takes a new id next time.
  void _keepUnsure(Object e, String what, String client) {
    if (e is! ApiError || e.status >= 500) _unsure = (what: what, client: client);
  }

  static int _sum(List<int> bytes) {
    var h = 17;
    for (var i = 0; i < bytes.length; i += 1 + bytes.length ~/ 4096) {
      h = (h * 31 + bytes[i]) & 0x3fffffff;
    }
    return h;
  }

  /// A place (where the phone is, with a name if given).
  Future<bool> sendPlace(PeopleLocation place) async {
    final what = 'place:${place.lat},${place.lng}:${place.label ?? ''}';
    final client = _clientFor(what);
    try {
      final sent = await api.sendPeopleMessage(id: id, body: SendPeopleMessageRequest(location: place, clientId: client));
      if (_disposed) return true;
      messages = _merge(messages ?? const [], [PeopleLine(sent)]);
      error = null;
      _changed();
      unawaited(_chat());
      return true;
    } on Object catch (e) {
      _keepUnsure(e, what, client);
      error = sendFailure(e);
      toStanding = pointsToStanding(e);
      _changed();
      return false;
    }
  }

  /// One of the person's own saved things, shared as a card (saved objects
  /// S1), with [ask]'s yes when the server wants one first. False when it
  /// failed (error says why); true when sent or the sender said no.
  Future<bool> shareSaved(String saved, Future<bool> Function(String question) ask) async {
    final what = 'saved:$saved';
    final client = _clientFor(what);
    try {
      final sent = await sendSaved(
        (confirm) => api.sendPeopleMessage(id: id, body: SendPeopleMessageRequest(card: SendPeopleMessageRequestCard(type: 'saved', saved: SavedShare(id: saved, owner: 'mine', confirm: confirm)), clientId: client)),
        ask,
      );
      if (sent == null || _disposed) return true;
      messages = _merge(messages ?? const [], [PeopleLine(sent)]);
      error = null;
      _changed();
      unawaited(_chat());
      return true;
    } on Object catch (e) {
      _keepUnsure(e, what, client);
      error = sendFailure(e);
      toStanding = pointsToStanding(e);
      _changed();
      return false;
    }
  }

  /// A card (a business, a product, an order, a delivery, your share).
  Future<bool> sendCard(SendPeopleMessageRequestCard card) async {
    final what = 'card:${jsonEncode(card.toJson())}';
    final client = _clientFor(what);
    try {
      final sent = await api.sendPeopleMessage(id: id, body: SendPeopleMessageRequest(card: card, clientId: client));
      if (_disposed) return true;
      messages = _merge(messages ?? const [], [PeopleLine(sent)]);
      error = null;
      _changed();
      unawaited(_chat());
      return true;
    } on Object catch (e) {
      _keepUnsure(e, what, client);
      error = sendFailure(e);
      toStanding = pointsToStanding(e);
      _changed();
      return false;
    }
  }

  void _replace(PeopleMessage m) {
    if (_disposed) return;
    messages = _merge(messages ?? const [], [PeopleLine(m)]);
    _changed();
  }

  /// Your reaction ("" takes it away).
  Future<void> react(PeopleMessage m, String emoji) async {
    try {
      _replace(await api.markPeopleMessage(id: id, message: m.id, body: MarkPeopleMessageRequest(reaction: m.myReaction == emoji ? '' : emoji)));
    } on Object catch (e) {
      error = sayFailure(e);
      _changed();
    }
  }

  Future<void> star(PeopleMessage m) async {
    try {
      _replace(await api.markPeopleMessage(id: id, message: m.id, body: MarkPeopleMessageRequest(starred: m.starred != true)));
    } on Object catch (e) {
      error = sayFailure(e);
      _changed();
    }
  }

  /// Whether [m] can still be deleted for everyone (yours, within 24 h).
  bool canDeleteForEveryone(PeopleMessage m) => m.mine && m.deleted != true && now().difference(m.createdAt) < deleteForEveryoneWithin;

  /// Deletes [m] for you, or for everyone.
  Future<bool> delete(PeopleMessage m, {required bool everyone}) async {
    try {
      await api.deletePeopleMessage(id: id, message: m.id, for_: everyone ? 'everyone' : 'me');
      if (_disposed) return true;
      if (everyone) {
        _replace(PeopleMessage(id: m.id, mine: m.mine, kind: m.kind, body: '', createdAt: m.createdAt, deleted: true, status: m.status, readAt: m.readAt));
      } else {
        messages = messages?.where((x) => x.id != m.id).toList();
        _changed();
      }
      unawaited(_chat());
      return true;
    } on Object catch (e) {
      actionError = sayFailure(e);
      _changed();
      return false;
    }
  }

  /// Forwards [m] to [to] (at most five chats); the words of how it went.
  Future<String> forward(PeopleMessage m, List<String> to) async {
    try {
      final r = await api.forwardPeopleMessage(body: ForwardPeopleMessageRequest(chatId: id, messageId: m.id, to: to));
      if (r.failed.isEmpty) return r.sent.length == 1 ? 'Forwarded' : 'Forwarded to ${r.sent.length} chats';
      final why = r.failed.first.error;
      return r.sent.isEmpty ? why : 'Forwarded to ${r.sent.length}. $why';
    } on Object catch (e) {
      return sayFailure(e);
    }
  }

  /// Forwards [m] to people chats and groups together (`POST
  /// /v1/me/forward`); the words of how it went.
  Future<String> forwardAll(PeopleMessage m, ChatsAndGroups to) async {
    try {
      final r = await api.forwardMessage(body: ForwardMessageRequest(chatId: id, messageId: m.id, toChats: to.chats.isEmpty ? null : to.chats, toGroups: to.groups.isEmpty ? null : to.groups));
      return forwardWords(r);
    } on Object catch (e) {
      return sayFailure(e);
    }
  }

  /// A share marked paid, or not yet: both sides see it.
  Future<void> markPaid(PeopleMessage m, bool paid) async {
    try {
      _replace(await api.markSplitPaid(id: id, message: m.id, body: MarkSplitPaidRequest(paid: paid)));
    } on Object catch (e) {
      error = sayFailure(e);
      _changed();
    }
  }

  /// Your settings with them: mute, archive.
  Future<bool> put({bool? muted, bool? archived}) => _act(() async => chat = await api.putPeopleChat(id: id, body: PutPeopleChatRequest(muted: muted, archived: archived)));

  /// Accept a request: it moves to your chats, and they see your read marks.
  Future<bool> accept() => _act(() async => chat = await api.acceptPeopleRequest(id: id));

  /// Delete a request: the sender is not told.
  Future<bool> decline() => _act(() => api.declinePeopleRequest(id: id));

  /// Block them (and report, with consent: the last 20 messages).
  Future<BlockPersonResponse?> block({PeopleReport? report}) async {
    BlockPersonResponse? out;
    final ok = await _act(() async {
      out = await api.blockPerson(id: id, body: BlockPersonRequest(report: report));
      if (out!.removed) {
        missing = true;
      } else {
        await _chat();
      }
    });
    return ok ? out : null;
  }

  Future<bool> unblock() => _act(() async {
    await api.unblockPerson(id: id);
    await _chat();
  });

  /// Report them: the last 20 messages of your copy to Taskiem's moderators.
  Future<String?> report(String reason, String note) async {
    try {
      final r = await api.reportPerson(id: id, body: PeopleReport(reason: reason, note: note.isEmpty ? null : note, consent: true));
      return r.said;
    } on Object catch (e) {
      actionError = sayFailure(e);
      _changed();
      return null;
    }
  }

  /// Delete this chat for you (they keep theirs).
  Future<bool> deleteChat() => _act(() => api.deletePeopleChat(id: id));

  Future<bool> _act(Future<void> Function() f) async {
    try {
      await f();
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
    _readSoon?.cancel();
    _events?.cancel();
    live?.removeListener(_liveChanged);
    _back.close();
    files.dispose();
    super.dispose();
  }
}

/// The line under the person's name: online or last seen as their
/// settings and yours allow, "Message request", or the account deleted.
String peopleHeadLine(PeopleChat? c, DateTime now, {bool offline = false, bool typing = false, PersonCard? person}) {
  if (offline) return 'Waiting for network…';
  if (c == null) return '';
  if (c.gone) return 'This account was deleted';
  if (c.suspended == true) return suspendedWords;
  if (c.request) return 'Message request';
  if (c.blocked) return 'Blocked';
  if (typing) return 'typing…';
  return lastSeenWords(person ?? c.person, now) ?? 'Person';
}

/// The chat with a person.
class PeopleChatScreen extends StatefulWidget {
  const PeopleChatScreen({super.key, required this.data, this.onBack, this.pickers = const Pickers(), this.recorder, this.notify, this.openBusiness, this.openOrder, this.places = const DevicePlaceFinder()});
  final PeopleChatData data;

  /// Where the phone is, asked only when A place is tapped (a test's own).
  final PlaceFinder places;
  final VoidCallback? onBack;
  final Pickers pickers;
  final Recorder? recorder;
  final PhoneNotifications? notify;

  /// Opens a chat with a business (a shared card's Chat), by its id.
  final OpenMyChat? openBusiness;

  /// Opens one of your orders (a shared order's See your order).
  final void Function(String business, String reference)? openOrder;

  @override
  State<PeopleChatScreen> createState() => _PeopleChatScreenState();
}

class _PeopleChatScreenState extends State<PeopleChatScreen> with Polling {
  final _text = TextEditingController();

  /// A link typed: its card above the box (chat-previews-and-editing.md §2.1).
  late final _linkPreview = ComposerPreview(api: widget.data.auth.api, transport: widget.data.auth.transport, chat: 'people:${widget.data.id}');
  // Saved texts by @name (saved objects S1): their words into the box.
  late final _saved = savedTextBox(
    text: _text,
    auth: widget.data.auth,
    onChange: () {
      if (mounted) setState(() {});
    },
    // Anything else of one's own: shared as a card (S1 part 3).
    onShare: (s) => unawaited(d.shareSaved(s.id, askShare(context))),
    mineOnly: true,
    onSaveNamed: (name) => unawaited(saveNamed(context, d.auth, name)),
    // A saved file: attached as itself, to send (S1).
    onAttach: (s) => unawaited(attachSavedFile(context, d.auth, s, (f) => _composer.currentState?.choose(f))),
  );
  final _composer = GlobalKey<ComposerState>();
  StreamSubscription<String>? _back;
  PeopleMessage? _reply;
  String? _paying;

  PeopleChatData get d => widget.data;

  /// The app in front and this chat on top: only then is it read again,
  /// and only then are messages marked read.
  bool _seen() => mounted && appInFront() && ModalRoute.of(context)?.isCurrent != false;

  @override
  void initState() {
    super.initState();
    d.seen = _seen;
    if (d.messages == null) d.open();
    if (d.follow) startPolling(_poll, d.poll);
    widget.notify?.show(d.tag, this);
    _back = d.givenBack.listen((w) {
      if (_text.text.isNotEmpty) return;
      // Words given back after a failed send are not typing.
      _was = w;
      _text.text = w;
    });
    _text.addListener(_typed);
    _text.addListener(_saved.typed);
    // Put away with words in the box: they no longer see "typing…".
    _life = AppLifecycleListener(
      onStateChange: (s) {
        if (s == AppLifecycleState.paused || s == AppLifecycleState.hidden) d.stopTyping();
      },
    );
  }

  AppLifecycleListener? _life;

  /// Typing, said to them while the box changes (throttled by the data).
  String _was = '';
  void _typed() {
    final now = _text.text;
    if (now == _was) return;
    _was = now;
    d.typing(now);
  }

  @override
  void dispose() {
    widget.notify?.unshow(this);
    _back?.cancel();
    _life?.dispose();
    _saved.dispose();
    // Closed with words in the box: they no longer see "typing…".
    d.stopTyping();
    _text.dispose();
    _linkPreview.dispose();
    d.dispose();
    super.dispose();
  }

  void _close() => (widget.onBack ?? () => Navigator.of(context).maybePop())();

  /// Closes the chat, and any screen over it (the person's info) first.
  void _closeChat() {
    if (!mounted) return;
    final me = ModalRoute.of(context);
    if (me != null && !me.isCurrent && me.isActive) Navigator.of(context).popUntil((r) => r == me);
    _close();
  }

  void _say(bool ok, String done) {
    if (!mounted) return;
    showTkToast(context, ok ? done : (d.actionError ?? 'Something went wrong. Try again.'), error: !ok);
  }

  Future<void> _openBusiness(String id, {String? notice, String draft = '', String? tool}) async {
    final open = widget.openBusiness;
    if (open != null) return open(id, notice: notice, draft: draft, tool: tool);
  }

  Future<void> _menu(Rect at) {
    final c = d.chat!;
    return showHeadMenu(
      context,
      at: at,
      items: [
        ('About ${c.name}', _info),
        if (!c.request) (c.muted ? 'Unmute notifications' : 'Mute notifications', () async => _say(await d.put(muted: !c.muted), c.muted ? 'Notifications on for ${c.name}' : 'Muted')),
        if (!c.request)
          (
            c.archived ? 'Unarchive' : 'Archive',
            () async {
              final ok = await d.put(archived: !c.archived);
              _say(ok, c.archived ? 'Brought back to the chats' : 'Archived');
              if (ok && !c.archived) _closeChat();
            },
          ),
        if (!c.gone) (c.blocked ? 'Unblock ${c.name}' : 'Block ${c.name}', c.blocked ? () async => _say(await d.unblock(), 'Unblocked ${c.name}') : () => _block(report: false)),
        if (!c.gone) ('Report ${c.name}', () => _block(report: true, blockToo: false)),
        ('Delete this chat', _deleteChat),
      ],
    );
  }

  /// Search this chat: a message found is jumped to here.
  Future<void> _search() => tkPush<void>(
    context,
    PeopleSearchScreen(auth: d.auth, chat: d.id, chatName: d.name, now: d.now, onPick: (m) => unawaited(d.jump(m))),
  );

  Future<void> _info() => tkPush<void>(
    context,
    PersonInfoScreen(data: d, onBlock: () => _block(report: false), onUnblock: _unblock, onReport: () => _block(report: true, blockToo: false)),
  );

  Future<void> _unblock() async => _say(await d.unblock(), 'Unblocked ${d.name}');

  /// Block (and report): with [report], the reasons and consent first.
  Future<void> _block({required bool report, bool blockToo = true}) async {
    final name = d.name;
    if (report) {
      final r = await showPeopleReport(context, name: name, block: blockToo);
      if (r == null || !mounted) return;
      if (r.block) {
        final out = await d.block(report: r.report);
        if (out == null) return _say(false, '');
        _say(true, out.reported ? 'Blocked and reported' : 'Blocked $name');
        if (out.removed) _closeChat();
      } else {
        final said = await d.report(r.report.reason, r.report.note ?? '');
        if (!mounted) return;
        if (said != null) showTkToast(context, said);
        if (said == null) _say(false, '');
      }
      return;
    }
    final yes = await askSure(
      context,
      title: 'Block $name?',
      body: 'They can no longer message you or see your last seen and photo. They are not told. You can unblock them at any time.',
      ok: 'Block',
      danger: true,
    );
    if (!yes) return;
    final out = await d.block();
    if (out == null) return _say(false, '');
    _say(true, 'Blocked $name');
    if (out.removed) _closeChat();
  }

  Future<void> _deleteChat() async {
    final yes = await askSure(
      context,
      title: 'Delete this chat?',
      body: 'It goes from your chats, on every phone you use. ${d.name} keeps their copy.',
      ok: 'Delete',
      danger: true,
    );
    if (!yes) return;
    final ok = await d.deleteChat();
    _say(ok, 'Chat deleted');
    if (ok) _closeChat();
  }

  Future<void> _accept() async {
    final ok = await d.accept();
    _say(ok, 'Accepted. ${d.name} is in your chats now.');
  }

  Future<void> _decline() async {
    final yes = await askSure(
      context,
      title: 'Delete this request?',
      body: '${d.name} is not told. If they write again, it comes as a new request.',
      ok: 'Delete',
      danger: true,
    );
    if (!yes) return;
    final ok = await d.decline();
    _say(ok, 'Request deleted');
    if (ok) _closeChat();
  }

  Future<void> _tools() async {
    final kind = await showPeopleShareMenu(context, together: d.chat != null && !d.chat!.request && !d.chat!.waiting && !d.chat!.gone && !d.chat!.blocked);
    if (kind == null || !mounted) return;
    switch (kind) {
      case PeopleShare.photo:
        final PickedFile? f;
        try {
          f = await widget.pickers.photo();
        } on Object catch (e) {
          if (mounted) sayPickProblem(context, e, camera: false);
          return;
        }
        if (f != null && mounted) await _file(f);
      case PeopleShare.file:
        final PickedFile? f;
        try {
          f = await widget.pickers.document();
        } on Object {
          if (mounted) showTkToast(context, 'That could not be opened. Try again.', error: true);
          return;
        }
        if (f != null && mounted) await _file(f);
      case PeopleShare.place:
        final place = await pickPlace(context, widget.places);
        if (place != null && mounted) await d.sendPlace(place);
      case PeopleShare.split:
        final card = await showSplitForm(context, d.auth);
        if (card != null) await d.sendCard(card);
      case PeopleShare.business || PeopleShare.product || PeopleShare.order || PeopleShare.delivery:
        final card = await pickShareCard(context, d.auth, kind);
        if (card != null) await d.sendCard(card);
      case PeopleShare.together:
        await startBasket(context, d.auth, chatId: d.id);
    }
  }

  Future<void> _file(PickedFile f) async {
    final problem = fileProblem(f, doorFiles);
    if (problem != null) {
      showTkToast(context, problem, error: true);
      return;
    }
    final reply = _reply;
    setState(() => _reply = null);
    await d.send('', file: f, reply: reply);
  }

  Future<void> _hold(PeopleMessage m, Rect at) async {
    if (m.id.startsWith('local-')) return;
    final c = d.chat;
    final write = c != null && !c.gone && !c.blocked && !c.request && c.suspended != true;
    await showMessageMenu(
      context,
      bubble: at,
      mine: m.mine,
      what: peopleLine(m),
      mineEmoji: m.myReaction,
      onReact: write && m.deleted != true ? (e) => d.react(m, e) : null,
      items: [
        if (write && m.deleted != true) MenuItem('reply', 'Reply', () => setState(() => _reply = m)),
        if (m.deleted != true && m.card == null && m.body.isNotEmpty) MenuItem('copy', 'Copy', () => Clipboard.setData(ClipboardData(text: m.body)), done: 'Copied'),
        if (m.deleted != true && m.card == null && m.body.isNotEmpty) MenuItem('note', 'Save as…', () => saveFromWords(context, d.auth, m.body)),
        // A photo (reading account details from photos): read on the phone.
        if (m.deleted != true && m.card == null && m.attachment?.kind == 'photo' && (m.attachment?.type ?? '').startsWith('image/'))
          ...photoAccountItems(
            context,
            d.auth,
            '${d.base}/files/${Uri.encodeComponent(m.id)}',
            from: (kind: 'person', chat: d.id, message: m.id, from: m.mine ? '' : d.name),
          ),
        if (m.deleted != true && m.card?.type != 'split') MenuItem('forward', 'Forward', () => _forward(m)),
        if (m.deleted != true) MenuItem('star', m.starred == true ? 'Unstar' : 'Star', () => d.star(m)),
        MenuItem('trash', 'Delete', () => _delete(m), danger: true, separated: true),
      ],
    );
  }

  Future<void> _forward(PeopleMessage m) async {
    if (m.forwardedMany == true) {
      showTkToast(context, 'This message has been forwarded many times. It can go to one chat at a time.');
    }
    final to = await pickChatsAndGroups(context, d.auth, title: 'Forward to', what: peopleLine(m), max: m.forwardedMany == true ? 1 : 5, exceptChat: d.id);
    if (to == null || (to.chats.isEmpty && to.groups.isEmpty) || !mounted) return;
    // To people chats alone as before; with a group, the one forward.
    final words = to.groups.isEmpty ? await d.forward(m, to.chats) : await d.forwardAll(m, to);
    if (mounted) showTkToast(context, words, error: !words.startsWith('Forwarded'));
  }

  Future<void> _delete(PeopleMessage m) async {
    final everyone = d.canDeleteForEveryone(m);
    // The app's sheet of actions (groups.dart pickAction), red as deleting.
    final choice = await pickAction(
      context,
      title: 'Delete this message?',
      // §7: delete for everyone removes their copy, not what they did with
      // it before.
      note: everyone ? deleteForEveryoneNote : null,
      actions: [('me', 'Delete for me', 'b-x', true), if (everyone) ('everyone', 'Delete for everyone', 'b-x-ring', true)],
    );
    if (choice == null || !mounted) return;
    final ok = await d.delete(m, everyone: choice == 'everyone');
    _say(ok, choice == 'everyone' ? 'Deleted for everyone' : 'Deleted for you');
  }

  Future<void> _paid(PeopleMessage m, bool paid) async {
    setState(() => _paying = m.id);
    await d.markPaid(m, paid);
    if (mounted) setState(() => _paying = null);
  }

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
          final now = d.now();
          final list = d.messages;
          final canSend = c != null && !c.gone && !c.blocked && !c.request && c.suspended != true && !d.missing;
          final byId = {for (final m in list ?? const <PeopleLine>[]) m.id: m.m};
          final nextOf = <String, PeopleLine>{for (var i = 0; list != null && i + 1 < list.length; i++) list[i].id: list[i + 1]};
          return Semantics(
            label: 'Chat with $name',
            container: true,
            child: WallpaperBox(
              ground: t.chatWallpaper,
              doodle: t.chatDoodle,
              child: Column(
                children: [
                  ColoredBox(
                    // The head on a bar of its own, as WhatsApp's (under the phone's
                    // status bar too), apart from the wallpaper.
                    color: t.panel,
                    child: Padding(
                      padding: EdgeInsets.only(top: pad.top),
                      child: ChatHeadBar(
                        onBack: widget.onBack,
                        onMore: c == null ? null : _menu,
                        onAvatar: c == null ? null : _info,
                        // As a group's head: Search as its own round button.
                        actions: [ChatHeadBar.headButton('Search this chat', TkIcon('b-search', size: 22, stroke: 2.2, color: t.text), c == null ? null : _search)],
                        avatar: PersonFace(name: name, transport: d.auth.transport, photo: c?.person?.photo, size: 40, letterSize: 15),
                        title: name,
                        typing: d.typingNow && !d.offline,
                        subtitle: peopleHeadLine(c, now, offline: d.offline, person: d.person),
                      ),
                    ),
                  ),
                  if (c != null && c.request) _RequestNote(name: name),
                  Expanded(
                    child: d.missing
                        ? Center(child: Padding(padding: const EdgeInsets.all(24), child: Text('This chat is no longer here.', textAlign: TextAlign.center, style: tkHint(t))))
                        : ThreadView<PeopleLine>(
                            // A jump places the thread again.
                            key: ValueKey('thread ${d.focusRound}'),
                            // Opened at a message: shown once it is read.
                            items: d.focusing ? null : list,
                            focus: d.focusFound ? d.focus : null,
                            of: (id: (m) => m.id, at: (m) => m.m.createdAt, mine: (m) => m.m.mine),
                            now: now,
                            hasOlder: d.hasOlder,
                            onOlder: d.older,
                            errors: [?d.error],
                            tail: d.typingNow ? const TypingBubble() : null,
                            empty: Text(
                              c?.waiting == true ? 'Your message waits for $name to accept it.' : 'Say hello to $name.',
                              textAlign: TextAlign.center,
                              style: tkHint(t),
                            ),
                            render: (x, prev) {
                              final m = x.m;
                              final first = prev == null || prev.m.mine != m.mine || prev.m.card != null || m.card != null;
                              final next = nextOf[x.id];
                              final last = next == null || next.m.mine != m.mine || next.m.card != null || dayLabel(next.m.createdAt, now) != dayLabel(m.createdAt, now);
                              final quoted = m.replyTo == null ? null : byId[m.replyTo];
                              return Padding(
                                padding: EdgeInsets.only(top: first ? 6 : 0),
                                child: BubbleHold(
                                  label: m.mine ? 'Your message' : 'Message from $name',
                                  onHold: x.local ? null : (at) => _hold(m, at),
                                  onReply: canSend && !x.local && m.deleted != true ? () => setState(() => _reply = m) : null,
                                  child: PeopleBubble(
                                    line: x,
                                    first: first,
                                    last: last,
                                    other: name,
                                    files: d.files,
                                    previewPath: '${d.base}/previews/${Uri.encodeComponent(m.id)}',
                                    quote: quoted,
                                    quoteMissing: m.replyTo != null && quoted == null,
                                    readTicks: d.readTicks,
                                    busy: _paying == m.id,
                                    onOpenCard: m.card == null || m.card!.type == 'split'
                                        ? null
                                        : m.card!.type == 'saved'
                                        ? (m.card!.saved == null ? null : () => saveSavedCard(context, d.auth, m.card!.saved!))
                                        : m.card!.type == 'basket'
                                        ? (m.card!.basketId == null ? null : () => showBasket(context, d.auth, m.card!.basketId!))
                                        : () => showPeopleCard(context, auth: d.auth, chat: d.id, message: m.id, openBusiness: _openBusiness, openOrder: widget.openOrder),
                                    onPaid: m.card?.type == 'split' && canSend ? (p) => _paid(m, p) : null,
                                    onReact: canSend && m.deleted != true ? (e) => d.react(m, e) : null,
                                  ),
                                ),
                              );
                            },
                          ),
                  ),
                  if (d.toStanding)
                    _Note(
                      text: 'Taskiem’s moderators have stopped your account from messaging people for now.',
                      action: 'Your standing',
                      onAction: () => tkPush<void>(context, YourStandingScreen(auth: d.auth, now: d.now)),
                    ),
                  if (c != null && c.request && !d.missing) _RequestBar(name: name, onAccept: _accept, onDelete: _decline, onBlock: () => _block(report: true)),
                  if (c != null && c.waiting && !c.request) _Note(text: 'Your messages wait in $name’s requests until they accept.'),
                  if (c != null && c.gone) const _Note(text: 'This account was deleted. You can read the chat, but not write to it.'),
                  if (c != null && !c.gone && c.suspended == true) const _Note(text: '$suspendedWords You can read the chat, but not write to it now.'),
                  if (c != null && c.blocked && !c.gone) _Note(text: 'You blocked $name. They can’t message you.', action: 'Unblock', onAction: _unblock),
                  // The people to pay collected from messages (pay_collect.dart).
                  PayListBar(auth: d.auth),
                  if (canSend) _saved.plain(),
                  if (canSend)
                    Composer(
                      key: _composer,
                      text: _text,
                      bottom: pad.bottom,
                      rules: doorFiles,
                      label: 'Message $name',
                      pickers: widget.pickers,
                      recorder: widget.recorder,
                      replying: _reply == null ? null : (_reply!.mine ? 'your message: ' : '$name: ') + peopleLine(_reply!),
                      onCancelReply: () => setState(() => _reply = null),
                      onTools: _tools,
                      linkPreview: _linkPreview,
                      onSend: (text, file) {
                        final reply = _reply;
                        setState(() => _reply = null);
                        final preview = file == null ? _linkPreview.choice(text) : null;
                        _linkPreview.reset();
                        d.send(text, file: file, reply: reply, preview: preview);
                      },
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

/// A message's ticks: a clock while it goes, one when sent, two when
/// delivered, two lime once read (only while both have read ticks on).
class PeopleTicks extends StatelessWidget {
  const PeopleTicks({super.key, required this.sending, required this.status, required this.readTicks, required this.meta});
  final bool sending;
  final String? status;
  final bool readTicks;
  final Color meta;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (sending) return Semantics(label: 'Sending', child: TkIcon('clock', size: 16, stroke: 2, color: meta));
    if (status == 'read' && readTicks) return Semantics(label: 'Read', child: TkIcon('tick-double', size: 16, stroke: 2, color: t.chatTickRead));
    if (status == 'read' || status == 'delivered') return Semantics(label: 'Delivered', child: TkIcon('tick-double', size: 16, stroke: 2, color: meta));
    return Semantics(label: 'Sent', child: TkIcon('tick', size: 16, stroke: 2, color: meta));
  }
}

/// One message of a people chat: Forwarded, the message it answers, its
/// file, its card, its words (or that it was deleted), the reactions, and
/// the warnings under a stranger's message.
class PeopleBubble extends StatelessWidget {
  const PeopleBubble({
    super.key,
    required this.line,
    required this.first,
    required this.last,
    required this.other,
    required this.files,
    this.quote,
    this.quoteMissing = false,
    this.readTicks = true,
    this.onOpenCard,
    this.onPaid,
    this.onReact,
    this.busy = false,
    this.head,
    this.quoteName,
    this.spans,
    this.previewPath,
  });
  final PeopleLine line;

  /// Where its link card's picture is (the chat's previews route).
  final String? previewPath;

  /// Over the rest inside the bubble: who wrote it (a group's sender).
  final Widget? head;

  /// Who wrote the message answered, when not [other] (a group's member).
  final String? quoteName;

  /// The words drawn as spans (a group's @mentions picked out).
  final InlineSpan Function(String words, TextStyle style)? spans;
  final bool first;
  final bool last;
  final String other;
  final ChatFiles files;
  final PeopleMessage? quote;

  /// It answers a message no longer shown (older, or deleted for you).
  final bool quoteMissing;
  final bool readTicks;
  /// The card opened live; a saved thing's card: Save this.
  final VoidCallback? onOpenCard;
  final ValueChanged<bool>? onPaid;

  /// A reaction chip tapped: the same as theirs, or yours taken back.
  final ValueChanged<String>? onReact;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final m = line.m;
    final mine = m.mine;
    final deleted = m.deleted == true;
    final c = deleted ? null : m.card;
    final meta = mine && c == null ? t.chatOutMeta : t.muted;
    final ink = t.text;
    final stamp = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (m.starred == true) ...[Semantics(label: 'Starred', child: TkIcon('star', size: 12, stroke: 2, color: meta)), const SizedBox(width: 3)],
        Text(clock(m.createdAt), style: tkText(size: 11, lineHeight: 16 / 11, color: meta)),
        if (mine) ...[const SizedBox(width: 4), PeopleTicks(sending: line.local, status: m.status, readTicks: readTicks, meta: meta)],
      ],
    );
    final forwarded = m.forwarded == true && !deleted
        ? Text(m.forwardedMany == true ? 'Forwarded many times' : 'Forwarded', style: tkText(size: 12, color: meta).copyWith(fontStyle: FontStyle.italic))
        : null;
    final q = quote == null && !quoteMissing ? null : _Quote(m: quote, mine: mine, other: quoteName ?? other);
    final a = deleted ? null : m.attachment;
    final words = deleted ? '' : (a != null && const {'Photo', 'Voice note', 'File'}.contains(m.body) ? '' : (c != null && m.body == c.title ? '' : m.body));
    final place = deleted ? null : m.location;
    final Widget shown;
    if (c != null) {
      shown = LayoutBuilder(
        builder: (context, box) => Align(
          alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
          child: Container(
            width: (box.maxWidth * 0.86).clamp(0.0, 360.0),
            padding: const EdgeInsets.fromLTRB(14, 12, 14, 8),
            decoration: cardDecoration(context, mine: mine, first: first),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              mainAxisSize: MainAxisSize.min,
              children: [
                ?head,
                ?forwarded,
                ?q,
                if (c.type == 'saved' && c.saved != null)
                  SavedCardView(card: c.saved!, mine: mine, onSave: onOpenCard)
                else
                  PeopleCardView(card: c, mine: mine, onOpen: onOpenCard, onPaid: onPaid, busy: busy),
                if (words.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(words, style: tkText(size: 15, lineHeight: 1.4, color: t.text))),
                Padding(padding: const EdgeInsets.only(top: 4), child: Align(alignment: Alignment.centerRight, child: stamp)),
              ],
            ),
          ),
        ),
      );
    } else {
      shown = LayoutBuilder(
        builder: (context, box) {
          final room = (box.maxWidth * 0.78 - 24).clamp(120.0, 360.0);
          return BubbleFrame(
            mine: mine,
            first: first,
            last: last,
            stamp: stamp,
            inline: false,
            children: [
              ?head,
              ?forwarded,
              ?q,
              // The first link's card, at the top as on WhatsApp.
              if (m.preview != null && !deleted && !line.local) LinkPreviewCard(preview: m.preview!, transport: files.transport, path: previewPath),
              if (a != null) AttachmentView(m: doorFileMessage(id: m.id, mine: mine, a: a, at: m.createdAt), files: files, other: other, room: room, local: line.file),
              if (place != null)
                // Opens in the phone's own map app.
                Semantics(
                  button: true,
                  label: '${(place.label ?? '').isEmpty ? 'A place' : place.label!}, open it in your map app',
                  excludeSemantics: true,
                  child: GestureDetector(
                    behavior: HitTestBehavior.opaque,
                    onTap: () => openPlace(place),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            TkIcon('pin', size: 16, stroke: 2, color: meta),
                            const SizedBox(width: 6),
                            Flexible(child: Text((place.label ?? '').isEmpty ? 'A place' : place.label!, style: tkText(size: 15, weight: 600, color: ink))),
                          ],
                        ),
                        Text('Open in maps', style: tkText(size: 13, weight: 600, color: mine ? t.chatOutMeta : TkLook.of(context).limeInk)),
                      ],
                    ),
                  ),
                ),
              if (deleted)
                Text('This message was deleted', style: tkText(size: 15, lineHeight: 23 / 16, color: meta).copyWith(fontStyle: FontStyle.italic))
              else if (words.isNotEmpty)
                // Web addresses open in the browser tab; @mentions keep their mark.
                LinkedText(words, style: tkText(size: 16, lineHeight: 23 / 16, color: ink), plain: spans),
            ],
          );
        },
      );
    }
    final reactions = [if ((m.theirReaction ?? '').isNotEmpty) m.theirReaction!, if ((m.myReaction ?? '').isNotEmpty) m.myReaction!];
    // A share card from someone not in your contacts names an account: the
    // transfer warning, as any message marked warn_transfer.
    final transfer = !mine && !deleted && (m.warnTransfer == true || (m.card?.type == 'split' && m.warn == true));
    final warn = !mine && !deleted && m.warn == true && !transfer;
    if (reactions.isEmpty && !warn && !transfer) return shown;
    return Column(
      crossAxisAlignment: mine ? CrossAxisAlignment.end : CrossAxisAlignment.start,
      children: [
        shown,
        // As main's chats (and a group's): each emoji with how many, yours
        // tinted, the chips just under the bubble.
        if (reactions.isNotEmpty)
          Transform.translate(
            offset: const Offset(0, -6),
            child: Padding(
              padding: const EdgeInsets.only(left: 8, right: 8),
              child: GroupReactionChips(
                list: [for (final e in {...reactions}) GroupMessageReactionsItem(reaction: e, count: reactions.where((x) => x == e).length)],
                mine: m.myReaction,
                onReact: onReact,
              ),
            ),
          ),
        if (warn) const PeopleWarning(strangerWarning),
        if (transfer) const PeopleWarning(peopleTransferWarning),
      ],
    );
  }
}

/// The message answered: who wrote it and its first line.
class _Quote extends StatelessWidget {
  const _Quote({required this.m, required this.mine, required this.other});
  final PeopleMessage? m;
  final bool mine;
  final String other;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final q = m;
    return Container(
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.fromLTRB(9, 5, 9, 5),
      decoration: BoxDecoration(
        color: mine ? const Color(0x1AFFFFFF) : t.tkSoft,
        border: Border(left: BorderSide(color: look.lime, width: 3)),
        borderRadius: BorderRadius.circular(12),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (q != null) Text(q.mine ? 'You' : other, style: tkText(size: 12.5, weight: 700, color: mine || dark ? look.lime : look.limeInk)),
          Text(
            q == null ? 'An earlier message' : peopleLine(q),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: tkText(size: 13, color: mine ? t.chatOutMeta : t.muted),
          ),
        ],
      ),
    );
  }
}

/// Over a request: who it is from, and that reading it tells them nothing.
class _RequestNote extends StatelessWidget {
  const _RequestNote({required this.name});
  final String name;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Text(
        '$name wants to chat with you. They are not told you read this unless you accept.',
        textAlign: TextAlign.center,
        style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted),
      ),
    );
  }
}

/// A request's answers: Accept, Delete, Block and report.
class _RequestBar extends StatelessWidget {
  const _RequestBar({required this.name, required this.onAccept, required this.onDelete, required this.onBlock});
  final String name;
  final VoidCallback onAccept;
  final VoidCallback onDelete;
  final VoidCallback onBlock;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final pad = MediaQuery.paddingOf(context);
    return Container(
      margin: EdgeInsets.fromLTRB(12, 4, 12, 8 + pad.bottom),
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 14),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text('Accept a chat with $name?', style: tkDisplay(size: 16, lineHeight: 1.3, color: t.text)),
          const SizedBox(height: 2),
          Text('Once you accept, they can see you read their messages.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(child: CardButton(label: 'Accept', primary: true, onTap: onAccept)),
              const SizedBox(width: 8),
              Expanded(child: CardButton(label: 'Delete', onTap: onDelete)),
            ],
          ),
          const SizedBox(height: 8),
          CardButton(label: 'Block and report', onTap: onBlock),
        ],
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
    final look = TkLook.of(context);
    return Semantics(
      liveRegion: true,
      child: Container(
        margin: const EdgeInsets.fromLTRB(12, 8, 12, 8),
        padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
        decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(16)),
        child: Row(
          children: [
            Expanded(child: Text(text, style: tkText(size: 13.5, lineHeight: 1.4, color: t.text))),
            if (action != null && onAction != null) ...[const SizedBox(width: 8), CardButton(label: action!, primary: true, onTap: onAction)],
          ],
        ),
      ),
    );
  }
}

// --- About the person ---

/// The person's card (§5.4): the name you gave them (and theirs), their
/// photo by their setting, last seen, their number only if they show it;
/// add them to your contacts or rename them; where the chat is kept; Block
/// and Report.
class PersonInfoScreen extends StatefulWidget {
  const PersonInfoScreen({super.key, required this.data, required this.onBlock, required this.onReport, this.onUnblock});
  final PeopleChatData data;
  final VoidCallback onBlock;
  final VoidCallback? onUnblock;
  final VoidCallback onReport;

  @override
  State<PersonInfoScreen> createState() => _PersonInfoScreenState();
}

class _PersonInfoScreenState extends State<PersonInfoScreen> {
  PeopleChatData get d => widget.data;

  Future<void> _rename(PersonCard p) async {
    final name = await askContactName(context, initial: p.contactName is String ? p.contactName as String : '', theirs: p.name);
    if (name == null || !mounted) return;
    try {
      await d.api.renameContact(person: p.id, body: RenameContactRequest(name: name.isEmpty ? null : name));
      await d.refresh();
      if (mounted) showTkToast(context, 'Saved');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return ListenableBuilder(
      listenable: d,
      builder: (context, _) {
        final c = d.chat;
        final p = c?.person;
        final seen = lastSeenWords(p, d.now());
        final phone = c?.phone is String && (c!.phone as String).isNotEmpty ? c.phone as String : null;
        final own = p?.contactName is String && (p!.contactName as String).isNotEmpty;
        return TkScreen(
          title: d.name,
          children: [
            TkPanel(
              padding: const EdgeInsets.all(20),
              children: [
                Column(
                  children: [
                    PersonFace(name: d.name, transport: d.auth.transport, photo: p?.photo, size: 96, letterSize: 32),
                    const SizedBox(height: 12),
                    Semantics(header: true, child: Text(d.name, textAlign: TextAlign.center, style: tkDisplay(size: 22, color: t.text))),
                    if (own && p.name != d.name) Text('Their name: ${p.name}', style: tkHint(t)),
                    if (c?.gone == true)
                      Text(deletedAccount, style: tkHint(t))
                    else if (c?.suspended == true)
                      Text(suspendedWords, textAlign: TextAlign.center, style: tkHint(t))
                    else if (seen != null)
                      Text(seen, style: tkHint(t)),
                    if (phone != null) ...[const SizedBox(height: 4), Text(showPhone(phone), style: tkText(size: 15, weight: 500, color: t.text))],
                  ],
                ),
              ],
            ),
            if (p != null && c?.gone != true)
              TkSection(
                inset: 56,
                children: [
                  if (p.inContacts)
                    TkListRow(title: own ? 'Rename' : 'Give them a name', sub: 'Only you see the name you give them', icon: 'b-people', square: look.teal, onTap: () => _rename(p))
                  else
                    TkListRow(title: 'Not in your contacts', sub: p.theyAddedYou ? 'They have you in theirs' : 'Accepting a chat adds them', icon: 'b-people', square: look.teal),
                ],
              ),
            // As Group info says it: a quiet note under the sections.
            Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text(peopleKeptWords, style: tkText(size: 13, lineHeight: 1.4, color: t.muted))),
            if (c != null && !c.gone)
              TkSection(
                inset: 56,
                children: [
                  if (!c.blocked)
                    TkListRow(title: 'Block ${d.name}', icon: 'b-x-ring', square: look.red, onTap: widget.onBlock)
                  else if (widget.onUnblock != null)
                    TkListRow(title: 'Unblock ${d.name}', sub: 'They can message you again', icon: 'b-x-ring', square: look.teal, onTap: widget.onUnblock),
                  TkListRow(title: 'Report ${d.name}', sub: 'The last 20 messages go to Taskiem’s moderators, if you agree', icon: 'b-alert', square: look.red, onTap: widget.onReport),
                ],
              ),
          ],
        );
      },
    );
  }
}

// --- Report, and Block and report ---

/// What a report sheet answers: the report, and whether to block too.
typedef PeopleReportAnswer = ({PeopleReport report, bool block});

/// Report a person (§8.3): why, a note, and consent to send the last 20
/// messages of this chat to Taskiem's moderators; with [block], they are
/// blocked too (Block and report).
Future<PeopleReportAnswer?> showPeopleReport(BuildContext context, {required String name, bool block = true}) =>
    showTkSheet<PeopleReportAnswer>(context, (_) => TkAppLook(child: _ReportSheet(name: name, block: block)));

class _ReportSheet extends StatefulWidget {
  const _ReportSheet({required this.name, required this.block});
  final String name;
  final bool block;

  @override
  State<_ReportSheet> createState() => _ReportSheetState();
}

class _ReportSheetState extends State<_ReportSheet> {
  String? _reason;
  bool _consent = false;
  late bool _block = widget.block;
  String? _error;
  final _note = TextEditingController();

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  void _send() {
    final r = _reason;
    if (r == null) return setState(() => _error = 'Choose why you are reporting them.');
    if (!_consent) return setState(() => _error = 'Agree to send the last 20 messages, so the moderators can see what happened.');
    final note = _note.text.trim();
    Navigator.of(context).pop((report: PeopleReport(reason: r, note: note.isEmpty ? null : note, consent: true), block: _block));
  }

  @override
  Widget build(BuildContext context) {
    // As the group's report (group_info.dart): a form sheet, each choice a
    // row with the app's tick.
    Widget tick(String words, bool on, VoidCallback onTap, {bool checkbox = true}) => Semantics(
          checked: checkbox ? on : null,
          selected: checkbox ? null : on,
          child: TkListRow(title: words, titleLines: 3, leading: TkCheckbox(checked: on), onTap: onTap),
        );
    return TkFormSheet(
      title: widget.block ? 'Block and report ${widget.name}' : 'Report ${widget.name}',
      crumb: 'Taskiem’s moderators read it',
      action: _block ? 'Block and report' : 'Send report',
      onAction: _send,
      children: [
        TkFormGroup(
          title: 'Why',
          children: [for (final (k, words) in peopleReportReasons) tick(words, _reason == k, () => setState(() => _reason = k), checkbox: false)],
        ),
        TkFormGroup(children: [TkKitField(label: 'What happened', hint: 'Optional', child: TkTextBox(controller: _note, lines: 3, max: 1000))]),
        TkFormGroup(
          children: [
            tick('Send the last 20 messages of this chat, with their photos and files, to Taskiem’s moderators', _consent, () => setState(() => _consent = !_consent)),
            tick('Block ${widget.name} too', _block, () => setState(() => _block = !_block)),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}
