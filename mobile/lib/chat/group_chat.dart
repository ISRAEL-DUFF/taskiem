// A group, on **You** (docs/spec/people-chatting.md §9, §13; the 5b
// checklist A and D; "As built: stage 5b, groups (server)" and "group
// safety (server)": `/v1/me/groups/{id}…`), drawn as a chat with a person
// is: words, photos, voice notes, files, places and cards (share cards too),
// sent with a client id so a send tried again is kept once; replies and
// quotes; reactions counted per member; stars (each one's own); forward to
// and from people chats and groups; delete for me, or for everyone within
// 24 hours; search in the group; live updates and typing on You's live
// connection. Each message from another member carries their name (as the
// person sees them: the name they gave them, else their own, "Deleted
// account" once deleted) and their photo as their settings allow. @mentions
// pick a member, and a mention of you stands out. Your own message's info
// says "Read by" (A6). A message the checks marked warns (B1); a closed
// group says so in the composer's place, and its admins can appeal; events
// (and Taskiem's) are lines in the middle.
//
// Groups are kept by Taskiem and readable by its servers: the info says so
// plainly (groups.dart, groupKeptWords) and nothing says they are
// end-to-end encrypted.

import 'dart:async';
import 'dart:convert' show jsonEncode;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/notify.dart' show PhoneNotifications;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/polling.dart';
import '../kit/sheet.dart';
import '../kit/list.dart' show TkLoading;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'composer.dart';
import 'door.dart' show doorFiles;
import 'group_info.dart';
import 'groups.dart';
import 'menu.dart';
import 'my_chat.dart' show OpenMyChat, sayFailure;
import 'people.dart';
import 'basket.dart';
import 'people_cards.dart';
import 'people_chat.dart' show PeopleBubble, PeopleLine, deleteForEveryoneNote, deleteForEveryoneWithin, sendFailure, typingBeat;
import 'people_find.dart' show OpenPeopleChat;
import 'people_live.dart';
import 'people_place.dart';
import 'people_search.dart' show PeopleSearchScreen;
import 'people_standing.dart';
import 'pick_problem.dart';
import 'thread.dart';
import 'words.dart';

/// How often an open group reads again.
const _poll = Duration(seconds: 4);

/// A message of the group; one of yours on its way is [local], with its
/// file.
class GroupLine {
  GroupLine(this.m, {this.local = false, this.file});
  final GroupMessage m;
  final bool local;
  final PickedFile? file;
  String get id => m.id;
}

List<GroupLine> _merge(List<GroupLine> cur, List<GroupLine> more) {
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

/// A group message as a people message, so a bubble is drawn as a people
/// chat's (its reactions counted apart, under it).
PeopleMessage asPeopleMessage(GroupMessage g) => PeopleMessage(
  id: g.id,
  mine: g.mine,
  kind: g.kind,
  body: g.body,
  createdAt: g.createdAt,
  replyTo: g.replyTo,
  attachment: g.attachment,
  card: g.card,
  location: g.location,
  deleted: g.deleted,
  status: g.status,
  forwarded: g.forwarded,
  forwardedMany: g.forwardedMany,
  starred: g.starred,
  warn: g.warn,
  warnTransfer: g.warnTransfer,
);

/// [m] with another status (its ticks moved on the live connection).
GroupMessage withStatus(GroupMessage m, String status) => GroupMessage.fromJson({...m.toJson(), 'status': status});

/// A member picked with @: their name as written, and their id (two
/// members may share a name).
typedef PickedMention = ({String name, String id});

/// The people mentioned in [text]: the members picked with @ whose name is
/// still there as a whole word ("@Ada" is not in "@Adamu"), each once.
List<String> mentionedIn(String text, List<PickedMention> picked) {
  final out = <String>[];
  for (final p in picked) {
    if (out.contains(p.id)) continue;
    final at = RegExp('@${RegExp.escape(p.name)}(?![\\p{L}\\p{N}_])', unicode: true);
    if (at.hasMatch(text)) out.add(p.id);
  }
  return out;
}

/// What a group shows, read every few seconds while it is seen (its
/// screen polls: not while the app is put away or another screen covers
/// it); live while You's connection is open.
class GroupChatData extends ChangeNotifier {
  GroupChatData(this.auth, this.id, {this.follow = true, DateTime Function()? now, Group? started, PeopleLive? live, this.focus, DateTime Function()? clock})
    : now = now ?? DateTime.now,
      _clock = clock ?? DateTime.now,
      group = started,
      live = live ?? PeopleLive.of(auth) {
    _groupEvents = this.live?.groupEvents.listen(_heard);
    _events = this.live?.events.listen(_gone);
    this.live?.addListener(_changed);
  }
  final Auth auth;
  final String id;
  final PeopleLive? live;
  final bool follow;
  final DateTime Function() now;
  final DateTime Function() _clock;
  StreamSubscription<GroupLiveEvent>? _groupEvents;
  StreamSubscription<PeopleLiveEvent>? _events;
  late final _slow = SlowWhileLive(clock: _clock);
  Timer? _readSoon;

  /// The message the group opens at (or jumped to since).
  String? focus;
  int focusRound = 0;
  bool focusing = false;

  /// Whether the group is seen now: messages are marked read only then.
  bool Function() seen = appInFront;

  TaskiemApi get api => auth.api;
  String get base => '/v1/me/groups/${Uri.encodeComponent(id)}';

  /// The tag of this group's notifications: none shows while it is open.
  String get tag => 'group:$id';

  Group? group;

  /// Its members (null until read, or once you left: 403).
  List<GroupMember>? members;

  /// For admins: people invited, and people waiting to join by the link.
  List<GroupPending> invited = const [];
  List<GroupPending> waiting = const [];

  List<GroupLine>? messages;
  bool hasOlder = false;
  String? error;
  bool offline = false;

  /// Gone for you (deleted, or the last one out closed it).
  bool missing = false;

  /// A send refused because your own account is stopped from messaging
  /// people: the screen points to Your standing.
  bool toStanding = false;

  /// What a change could not do, said once by the screen.
  String? actionError;
  bool _disposed = false;
  int _localN = 0;
  bool _pulling = false;
  bool _reading = false;
  bool _loadingOlder = false;
  bool _olderLoaded = false;
  bool _olderMore = false;
  ({String what, String client})? _unsure;
  final List<GroupMessage> _early = [];

  late final ChatFiles files = ChatFiles(auth.transport, pathOf: (m) => '$base/files/${Uri.encodeComponent(m.id)}');

  final _back = StreamController<String>.broadcast();
  Stream<String> get givenBack => _back.stream;

  String get name => group?.name ?? 'Group';
  bool get isAdmin => group?.role == 'admin' && group?.left != true;

  /// You may write here now: in it, not closed, and allowed to post.
  bool get canSend {
    final g = group;
    return g != null && !g.left && g.canPost && g.closed == null && !missing;
  }

  /// A member by their id.
  GroupMember? member(Object? id) => id is String ? members?.where((m) => m.personId == id).firstOrNull : null;

  /// Who is typing here now, by name (never you).
  List<String> get typingNames {
    final ids = live?.groupTyping[id] ?? const [];
    return [for (final p in ids) member(p)?.name ?? 'Someone'];
  }

  Future<void> open() async {
    final at = focus;
    if (at != null) {
      focusing = true;
      _changed();
    }
    await _group();
    unawaited(readMembers());
    await _pull(first: true);
    if (at != null) await _reach(at);
  }

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

  /// To one of this group's messages (a search's hit in it).
  Future<void> jump(String messageId) async {
    focus = messageId;
    focusing = true;
    _changed();
    await _reach(messageId);
  }

  bool get focusFound => focus != null && (messages ?? const []).any((m) => m.id == focus);

  /// Read again (the screen's poll): every 30 seconds while live.
  void poll() {
    if (_disposed || missing) return;
    if (!_slow.due(live)) return;
    _group();
    _pull(first: false);
  }

  Future<void> refresh() => Future.wait([_group(), _pull(first: false), readMembers()]);

  void _heard(GroupLiveEvent e) {
    if (_disposed || e.groupId != id) return;
    switch (e) {
      case GroupLiveMessage(:final message):
        final cur = messages;
        if (cur == null) {
          _early.add(message);
          return;
        }
        if (!_fits(cur, message, hasOlder)) return;
        messages = _merge(cur, [GroupLine(message)]);
        // An event changes who is in it, their roles or the info.
        if (message.kind == 'event') unawaited(readMembers());
        _changed();
        if (!message.mine && seen()) {
          _readSoon?.cancel();
          _readSoon = Timer(const Duration(milliseconds: 300), () {
            if (!_disposed && seen()) unawaited(api.readGroup(id: id).catchError((Object _) {}));
          });
        }
      case GroupLiveGroup(:final hidden, group: final g):
        if (hidden) {
          // Cleared on another device until a new message: still a member,
          // so its messages are emptied (gone only once left or closed).
          final was = group;
          if (was != null && (was.left || was.closed != null)) {
            missing = true;
          } else {
            messages = [...?messages?.where((l) => l.local)];
            hasOlder = false;
            _olderLoaded = false;
          }
        } else if (g != null) {
          group = g;
          if (missing) {
            // Back (a new message after it was cleared): read it afresh.
            missing = false;
            unawaited(_pull(first: true));
          }
        }
        _changed();
      case GroupLiveReceipts(:final statuses):
        final cur = messages;
        if (cur == null) return;
        messages = [for (final l in cur) statuses.containsKey(l.id) && l.m.mine ? GroupLine(withStatus(l.m, statuses[l.id]!), local: l.local, file: l.file) : l];
        _changed();
      case GroupLiveInvite():
        break;
    }
  }

  /// A message deleted for you on another device, or your copy deleted.
  void _gone(PeopleLiveEvent e) {
    if (_disposed || e is! PeopleLiveGone || e.chatId != id) return;
    if (e.id == null) {
      missing = true;
    } else {
      messages = messages?.where((m) => m.id != e.id).toList();
    }
    _changed();
  }

  static bool _fits(List<GroupLine> cur, GroupMessage m, bool more) {
    if (!more) return true;
    if (cur.any((l) => l.id == m.id)) return true;
    final oldest = cur.where((l) => !l.local).firstOrNull;
    if (oldest == null) return false;
    return !m.createdAt.isBefore(oldest.m.createdAt);
  }

  DateTime? _typingAt;

  /// You are typing: said every [typingBeat] at most while the box
  /// changes, and that you stopped when it empties. Only where you may
  /// post.
  void typing(String text) {
    if (!canSend) return;
    if (toStanding) return stopTyping();
    final at = _clock();
    if (text.trim().isEmpty) {
      if (_typingAt != null) {
        _typingAt = null;
        unawaited(api.typingGroup(id: id, body: const TypingGroupRequest(typing: false)).catchError((Object _) {}));
      }
      return;
    }
    if (_typingAt != null && at.difference(_typingAt!) < typingBeat) return;
    _typingAt = at;
    unawaited(api.typingGroup(id: id, body: const TypingGroupRequest(typing: true)).catchError((Object _) {}));
  }

  void stopTyping() {
    if (_typingAt == null || _disposed) return;
    _typingAt = null;
    unawaited(api.typingGroup(id: id, body: const TypingGroupRequest(typing: false)).catchError((Object _) {}));
  }

  Future<void> _group() async {
    if (_reading) return;
    _reading = true;
    try {
      final g = await api.getGroup(id: id);
      if (_disposed) return;
      group = g;
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

  /// The members (and, for admins, who is invited or waiting); none once
  /// you left (403).
  Future<void> readMembers() async {
    try {
      final r = await api.groupMembers(id: id);
      if (_disposed) return;
      members = r.members;
      invited = r.invited ?? const [];
      waiting = r.waiting ?? const [];
      _changed();
    } on Object {
      // Not shown; read again with the group.
    }
  }

  /// A page of the group's media and files, newest first (as you see the
  /// group: your own time in it, none deleted for you or for everyone),
  /// older than [before]; null when it can't be read now.
  Future<GroupMediaResponse?> media({String? before}) async {
    try {
      return await api.groupMedia(id: id, before: before);
    } on Object {
      return null;
    }
  }

  Future<void> _pull({required bool first}) async {
    if (missing) return;
    if (_pulling && !first) return;
    _pulling = true;
    try {
      final out = await api.groupMessages(id: id);
      if (_disposed) return;
      final page = [for (final m in out.messages) GroupLine(m)];
      final from = page.isEmpty ? null : page.first.m.createdAt;
      final kept = (messages ?? const <GroupLine>[]).where((m) => m.local || (out.more && from != null && m.m.createdAt.isBefore(from))).toList();
      if (!out.more) _olderLoaded = false;
      hasOlder = _olderLoaded ? _olderMore : out.more;
      var all = _merge(kept, page);
      if (_early.isNotEmpty) {
        final early = [for (final m in _early) if (_fits(all, m, hasOlder)) GroupLine(m)];
        _early.clear();
        early.removeWhere((l) => page.any((p) => p.id == l.id));
        all = _merge(all, early);
      }
      messages = all;
      offline = false;
      error = null;
      _changed();
      // Marked read only while the group is seen.
      final g = group;
      if (g != null && !g.left && seen() && (g.unread > 0 || first)) await api.readGroup(id: id);
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
      final out = await api.groupMessages(id: id, before: oldest.id);
      if (_disposed) return;
      messages = _merge(messages ?? const [], [for (final m in out.messages) GroupLine(m)]);
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
      error = e is Offline ? 'You’re offline. The group shows when you’re back online.' : sayFailure(e);
    }
    _changed();
  }

  String _clientFor(String what) {
    toStanding = false;
    final again = _unsure;
    _unsure = null;
    return again != null && again.what == what ? again.client : newClientId(now(), ++_localN);
  }

  void _keepUnsure(Object e, String what, String client) {
    if (e is! ApiError || e.status >= 500) _unsure = (what: what, client: client);
  }

  /// Words, or a file with its caption, answering [reply], mentioning
  /// [mentions] (member ids): shown at once with a clock, then the
  /// server's copy; not sent, the words come back to the box.
  Future<bool> send(String text, {PickedFile? file, GroupMessage? reply, List<String> mentions = const []}) async {
    final words = text.trim();
    if (words.isEmpty && file == null) return false;
    _typingAt = null;
    final what = file == null ? 'text:$words' : 'file:${file.name}:${file.type}:${file.bytes.length}:$words';
    final client = _clientFor(what);
    final mine = GroupLine(
      GroupMessage(
        id: 'local-$client',
        groupId: id,
        mine: true,
        sender: 'You',
        kind: file == null ? 'text' : 'media',
        body: words,
        createdAt: now(),
        replyTo: reply?.id,
        mentions: mentions.isEmpty ? null : mentions,
        attachment: file == null ? null : ChatAttachment(kind: file.photo ? 'photo' : (file.voice ? 'voice' : 'file'), name: file.name, size: file.bytes.length, type: file.type),
      ),
      local: true,
      file: file,
    );
    messages = _merge(messages ?? const [], [mine]);
    error = null;
    _changed();
    try {
      final GroupMessage sent;
      if (file != null) {
        final j = await auth.transport.upload('$base/files', bytes: file.bytes, filename: file.name, type: file.type, fields: {
          'client_id': client,
          if (words.isNotEmpty) 'text': words,
          if (reply != null) 'reply_to': reply.id,
          if (mentions.isNotEmpty) 'mentions': mentions.join(','),
        });
        sent = GroupMessage.fromJson((j as Map).cast<String, dynamic>());
        unawaited(files.keep(sent.id, Uint8List.fromList(file.bytes)));
      } else {
        sent = await api.sendGroupMessage(id: id, body: SendGroupMessageRequest(text: words, clientId: client, replyTo: reply?.id, mentions: mentions.isEmpty ? null : mentions));
      }
      if (_disposed) return true;
      messages = _merge((messages ?? const []).where((m) => m != mine).toList(), [GroupLine(sent)]);
      unawaited(_group());
    } on Object catch (e) {
      if (_disposed) return false;
      messages = messages?.where((m) => m != mine).toList();
      _keepUnsure(e, what, client);
      if (!_back.isClosed && words.isNotEmpty) _back.add(words);
      error = sendFailure(e);
      toStanding = pointsToStanding(e);
      unawaited(_group());
      _changed();
      return false;
    }
    _changed();
    return true;
  }

  Future<bool> _sendOne(String what, SendGroupMessageRequest Function(String client) body) async {
    final client = _clientFor(what);
    try {
      final sent = await api.sendGroupMessage(id: id, body: body(client));
      if (_disposed) return true;
      messages = _merge(messages ?? const [], [GroupLine(sent)]);
      error = null;
      _changed();
      unawaited(_group());
      return true;
    } on Object catch (e) {
      _keepUnsure(e, what, client);
      error = sendFailure(e);
      toStanding = pointsToStanding(e);
      _changed();
      return false;
    }
  }

  /// A place (where the phone is, with a name if given).
  Future<bool> sendPlace(PeopleLocation place) =>
      _sendOne('place:${place.lat},${place.lng}:${place.label ?? ''}', (client) => SendGroupMessageRequest(location: place, clientId: client));

  /// A card (a business, a product, an order, a delivery, your share).
  Future<bool> sendCard(SendPeopleMessageRequestCard card) => _sendOne(
    'card:${jsonEncode(card.toJson())}',
    (client) => SendGroupMessageRequest(card: SendGroupMessageRequestCard.fromJson(card.toJson()), clientId: client),
  );

  void _replace(GroupMessage m) {
    if (_disposed) return;
    messages = _merge(messages ?? const [], [GroupLine(m)]);
    _changed();
  }

  /// Your reaction ("" takes it away): one per member, counted.
  Future<void> react(GroupMessage m, String emoji) async {
    try {
      _replace(await api.markGroupMessage(id: id, message: m.id, body: MarkGroupMessageRequest(reaction: m.myReaction == emoji ? '' : emoji)));
    } on Object catch (e) {
      error = sayFailure(e);
      _changed();
    }
  }

  /// Your star (yours alone).
  Future<void> star(GroupMessage m) async {
    try {
      _replace(await api.markGroupMessage(id: id, message: m.id, body: MarkGroupMessageRequest(starred: m.starred != true)));
    } on Object catch (e) {
      error = sayFailure(e);
      _changed();
    }
  }

  /// Yours, within 24 hours, while you are in the group.
  bool canDeleteForEveryone(GroupMessage m) => m.mine && m.deleted != true && m.kind != 'event' && group?.left != true && now().difference(m.createdAt) < deleteForEveryoneWithin;

  Future<bool> delete(GroupMessage m, {required bool everyone}) async {
    try {
      await api.deleteGroupMessage(id: id, message: m.id, for_: everyone ? 'everyone' : 'me');
      if (_disposed) return true;
      if (everyone) {
        _replace(GroupMessage.fromJson({...m.toJson(), 'deleted': true, 'body': 'This message was deleted', 'reactions': null, 'my_reaction': null, 'mentions': null}));
      } else {
        messages = messages?.where((x) => x.id != m.id).toList();
        _changed();
      }
      unawaited(_group());
      return true;
    } on Object catch (e) {
      actionError = sayFailure(e);
      _changed();
      return false;
    }
  }

  /// Forwards [m] to people chats and groups; the words of how it went.
  Future<String> forward(GroupMessage m, ChatsAndGroups to) async {
    try {
      final r = await api.forwardMessage(body: ForwardMessageRequest(groupId: id, messageId: m.id, toChats: to.chats.isEmpty ? null : to.chats, toGroups: to.groups.isEmpty ? null : to.groups));
      return forwardWords(r);
    } on Object catch (e) {
      return sayFailure(e);
    }
  }

  /// A share marked paid, or not yet, by any member.
  Future<void> markPaid(GroupMessage m, bool paid) async {
    try {
      _replace(await api.markGroupSplitPaid(id: id, message: m.id, body: MarkGroupSplitPaidRequest(paid: paid)));
    } on Object catch (e) {
      error = sayFailure(e);
      _changed();
    }
  }

  /// Your settings: mute, archive, mentions while muted (A7).
  Future<bool> put({bool? muted, bool? archived, bool? mentions}) => _act(() async => group = await api.putMyGroup(id: id, body: PutMyGroupRequest(muted: muted, archived: archived, mentions: mentions)));

  /// The group's info and settings (admins, or everyone when A2's switch
  /// lets them edit the info).
  Future<bool> edit(PutGroupRequest body) => _act(() async => group = await api.putGroup(id: id, body: body));

  Future<bool> setPhoto(PickedFile f) => _act(() async {
    final j = await auth.transport.upload('$base/photo', bytes: f.bytes, filename: f.name, type: f.type, method: 'PUT');
    group = Group.fromJson((j as Map).cast<String, dynamic>());
  });

  Future<bool> removePhoto() => _act(() async => group = await api.deleteGroupPhoto(id: id));

  /// Leave: your copy up to now stays (A5); [closed] when you were the
  /// last one out.
  Future<LeaveGroupResponse?> leave() async {
    LeaveGroupResponse? out;
    final ok = await _act(() async {
      out = await api.leaveGroup(id: id);
      final g = out!.group;
      if (g is Map) group = Group.fromJson(g.cast<String, dynamic>());
      if (out!.closed) missing = true;
      members = null;
    });
    return ok ? out : null;
  }

  /// Delete it for you (once left, for good).
  Future<bool> deleteForMe() => _act(() async {
    await api.deleteGroupForMe(id: id);
    missing = true;
  });

  Future<List<GroupAdded>?> add(List<String> people) async {
    List<GroupAdded>? out;
    final ok = await _act(() async {
      out = (await api.addGroupMembers(id: id, body: AddGroupMembersRequest(personIds: people))).added;
      await Future.wait([readMembers(), _group()]);
    });
    return ok ? out : null;
  }

  Future<bool> setAdmin(String person, bool admin) => _act(() async {
    await api.setGroupAdmin(id: id, person: person, body: SetGroupAdminRequest(admin: admin));
    await readMembers();
  });

  Future<bool> remove(String person) => _act(() async {
    await api.removeGroupMember(id: id, person: person);
    await Future.wait([readMembers(), _group()]);
  });

  /// Block a member (A8): their group messages still show; they can't
  /// message you one to one, add you to groups, or see your last seen and
  /// photo.
  Future<bool> block(String person) => _act(() => api.blockGroupMember(id: id, person: person));

  Future<bool> cancelInvite(String person) => _act(() async {
    await api.cancelGroupInvite(id: id, person: person);
    await readMembers();
  });

  Future<bool> approve(String person) => _act(() async {
    await api.approveGroupJoin(id: id, person: person);
    await Future.wait([readMembers(), _group()]);
  });

  Future<bool> declineJoin(String person) => _act(() async {
    await api.declineGroupJoin(id: id, person: person);
    await readMembers();
  });

  /// Report the group, or one member ([person]), with consent: the last 20
  /// messages you can see go to Taskiem's moderators. The server's words.
  Future<String?> report(String reason, String note, {String? person}) async {
    try {
      final r = await api.reportGroup(id: id, body: ReportGroupRequest(reason: reason, note: note.isEmpty ? null : note, consent: true, personId: person));
      return r.said;
    } on Object catch (e) {
      actionError = sayFailure(e);
      _changed();
      return null;
    }
  }

  /// The admins' appeal of a closure: the server's words.
  Future<String?> appeal(String text) async {
    try {
      final r = await api.appealGroup(id: id, body: AppealGroupRequest(text: text));
      await _group();
      return r.said;
    } on Object catch (e) {
      actionError = appealFailure(e);
      _changed();
      return null;
    }
  }

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
    _groupEvents?.cancel();
    _events?.cancel();
    live?.removeListener(_changed);
    _back.close();
    files.dispose();
    super.dispose();
  }
}

/// A member's name's colour over their messages: the same name, the same
/// accent's ink.
Color senderInk(BuildContext context, String name) {
  final look = TkLook.of(context);
  final inks = [look.tealInk, look.orangeInk, look.blueInk, look.violetInk, look.limeInk, look.redInk];
  return inks[name.codeUnits.fold<int>(0, (a, c) => (a * 31 + c) & 0x7fffffff) % inks.length];
}

/// The words with each @name of a member picked out (and yours on lime).
InlineSpan mentionSpans(String words, TextStyle style, {required List<String> names, required List<String> mine, required Color ink, required Color mark}) {
  final all = {...names, ...mine}.where((n) => n.trim().isNotEmpty).toList()..sort((a, b) => b.length.compareTo(a.length));
  if (all.isEmpty || !words.contains('@')) return TextSpan(text: words, style: style);
  final re = RegExp('@(${all.map(RegExp.escape).join('|')})');
  final out = <InlineSpan>[];
  var at = 0;
  for (final m in re.allMatches(words)) {
    if (m.start > at) out.add(TextSpan(text: words.substring(at, m.start)));
    final me = mine.contains(m[1]);
    out.add(TextSpan(text: m[0], style: style.copyWith(fontWeight: FontWeight.w700, color: me ? null : ink, backgroundColor: me ? mark : null)));
    at = m.end;
  }
  if (at < words.length) out.add(TextSpan(text: words.substring(at)));
  return TextSpan(style: style, children: out);
}

/// A group.
class GroupChatScreen extends StatefulWidget {
  const GroupChatScreen({super.key, required this.data, this.onBack, this.pickers = const Pickers(), this.recorder, this.notify, this.openBusiness, this.openOrder, this.places = const DevicePlaceFinder(), this.openPerson});
  final GroupChatData data;
  final PlaceFinder places;
  final VoidCallback? onBack;
  final Pickers pickers;
  final Recorder? recorder;
  final PhoneNotifications? notify;
  final OpenMyChat? openBusiness;
  final void Function(String business, String reference)? openOrder;

  /// Opens a chat with a member, one to one (from their row in the info).
  final OpenPeopleChat? openPerson;

  @override
  State<GroupChatScreen> createState() => _GroupChatScreenState();
}

class _GroupChatScreenState extends State<GroupChatScreen> with Polling {
  final _text = TextEditingController();
  StreamSubscription<String>? _back;
  GroupMessage? _reply;
  String? _paying;

  /// Members picked with @ in the box: their name as written, their id.
  final _mentions = <PickedMention>[];

  /// The @ being typed before the cursor, while it is one.
  String? _at;

  GroupChatData get d => widget.data;

  bool _seen() => mounted && appInFront() && ModalRoute.of(context)?.isCurrent != false;

  AppLifecycleListener? _life;
  String _was = '';

  @override
  void initState() {
    super.initState();
    d.seen = _seen;
    if (d.messages == null) d.open();
    if (d.follow) startPolling(_poll, d.poll);
    widget.notify?.show(d.tag, this);
    _back = d.givenBack.listen((w) {
      if (_text.text.isNotEmpty) return;
      _was = w;
      _text.text = w;
    });
    _text.addListener(_typed);
    _life = AppLifecycleListener(
      onStateChange: (s) {
        if (s == AppLifecycleState.paused || s == AppLifecycleState.hidden) d.stopTyping();
      },
    );
  }

  void _typed() {
    final now = _text.text;
    final sel = _text.selection;
    final upto = sel.isValid && sel.isCollapsed && sel.end <= now.length ? now.substring(0, sel.end) : now;
    final m = RegExp(r'(?:^|\s)@([^@\n]{0,30})$').firstMatch(upto);
    final at = m?[1];
    if (at != _at) setState(() => _at = at);
    if (now == _was) return;
    _was = now;
    d.typing(now);
  }

  /// A member picked for the @ being typed: their name put in.
  void _mention(GroupMember p) {
    final text = _text.text;
    final sel = _text.selection;
    final end = sel.isValid && sel.end <= text.length ? sel.end : text.length;
    final start = text.substring(0, end).lastIndexOf('@');
    if (start < 0) return;
    final put = '@${p.name} ';
    final next = text.replaceRange(start, end, put);
    if (!_mentions.any((m) => m.id == p.personId)) _mentions.add((name: p.name, id: p.personId));
    _text.value = TextEditingValue(text: next, selection: TextSelection.collapsed(offset: start + put.length));
    setState(() => _at = null);
  }

  @override
  void dispose() {
    widget.notify?.unshow(this);
    _back?.cancel();
    _life?.dispose();
    d.stopTyping();
    _text.dispose();
    d.dispose();
    super.dispose();
  }

  void _close() => (widget.onBack ?? () => Navigator.of(context).maybePop())();

  void _closeGroup() {
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

  Future<void> _info() => tkPush<void>(context, GroupInfoScreen(data: d, onClosed: _closeGroup, openPerson: widget.openPerson, onJump: _jumpFromInfo));

  /// To a message from the group's info (its media): back to the chat, at it.
  void _jumpFromInfo(String message) {
    final here = ModalRoute.of(context);
    if (here != null) Navigator.of(context).popUntil((r) => r == here);
    unawaited(d.jump(message));
  }

  Future<void> _search() => tkPush<void>(context, PeopleSearchScreen(auth: d.auth, group: d.id, chatName: d.name, now: d.now, onPick: (m) => unawaited(d.jump(m))));

  Future<void> _menu(Rect at) {
    final g = d.group!;
    return showHeadMenu(
      context,
      at: at,
      items: [
        ('Group info', _info),
        ('Search this group', _search),
        if (!g.left) (g.muted ? 'Unmute notifications' : 'Mute notifications', () async => _say(await d.put(muted: !g.muted), g.muted ? 'Notifications on for ${g.name}' : 'Muted')),
        (
          g.archived ? 'Unarchive' : 'Archive',
          () async {
            final ok = await d.put(archived: !g.archived);
            _say(ok, g.archived ? 'Brought back to the chats' : 'Archived');
            if (ok && !g.archived) _closeGroup();
          },
        ),
        ('Report this group', () => reportGroupFlow(context, d)),
        if (!g.left) ('Leave this group', () => leaveGroupFlow(context, d, onClosed: _closeGroup)),
        ('Delete this group', () => deleteGroupFlow(context, d, onClosed: _closeGroup)),
      ],
    );
  }

  Future<void> _tools() async {
    final kind = await showPeopleShareMenu(context, together: d.group != null && d.group!.canPost && !d.group!.left);
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
        await startBasket(context, d.auth, groupId: d.id);
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

  Future<void> _send(String text, PickedFile? file) async {
    final reply = _reply;
    final picked = List.of(_mentions);
    final mentions = mentionedIn(text, picked);
    setState(() {
      _reply = null;
      _at = null;
    });
    _mentions.clear();
    final sent = await d.send(text, file: file, reply: reply, mentions: mentions);
    // Not sent: the words come back to the box, and their picks with them.
    if (!sent && mounted) {
      for (final p in picked) {
        if (!_mentions.any((m) => m.id == p.id)) _mentions.add(p);
      }
    }
  }

  Future<void> _hold(GroupMessage m, Rect at) async {
    if (m.id.startsWith('local-') || m.kind == 'event') return;
    final write = d.canSend;
    final live = m.deleted != true;
    await showMessageMenu(
      context,
      bubble: at,
      mine: m.mine,
      what: groupLine(m),
      mineEmoji: m.myReaction,
      onReact: write && live ? (e) => d.react(m, e) : null,
      items: [
        if (write && live) MenuItem('reply', 'Reply', () => setState(() => _reply = m)),
        if (live && m.card == null && m.body.isNotEmpty) MenuItem('copy', 'Copy', () => Clipboard.setData(ClipboardData(text: m.body)), done: 'Copied'),
        if (live && m.card?.type != 'split') MenuItem('forward', 'Forward', () => _forward(m)),
        if (live) MenuItem('star', m.starred == true ? 'Unstar' : 'Star', () => d.star(m)),
        if (m.mine && live) MenuItem('info', 'Info', () => showReadBy(context, d, m)),
        if (live && (m.reactions ?? const []).isNotEmpty) MenuItem('emoji', 'Who reacted', () => showGroupReactions(context, d, m)),
        MenuItem('trash', 'Delete', () => _delete(m), danger: true, separated: true),
      ],
    );
  }

  Future<void> _forward(GroupMessage m) async {
    if (m.forwardedMany == true) showTkToast(context, 'This message has been forwarded many times. It can go to one chat at a time.');
    final to = await pickChatsAndGroups(context, d.auth, title: 'Forward to', what: groupLine(m), max: m.forwardedMany == true ? 1 : 5, exceptGroup: d.id);
    if (to == null || (to.chats.isEmpty && to.groups.isEmpty) || !mounted) return;
    final words = await d.forward(m, to);
    if (mounted) showTkToast(context, words, error: !words.startsWith('Forwarded'));
  }

  Future<void> _delete(GroupMessage m) async {
    final everyone = d.canDeleteForEveryone(m);
    final choice = await pickAction(
      context,
      title: 'Delete this message?',
      note: everyone ? deleteForEveryoneNote.replaceFirst('their chat', 'everyone’s copy of the group') : null,
      actions: [('me', 'Delete for me', 'b-person', false), if (everyone) ('everyone', 'Delete for everyone', 'b-people', true)],
    );
    if (choice == null || !mounted) return;
    final ok = await d.delete(m, everyone: choice == 'everyone');
    _say(ok, choice == 'everyone' ? 'Deleted for everyone' : 'Deleted for you');
  }

  Future<void> _paid(GroupMessage m, bool paid) async {
    setState(() => _paying = m.id);
    await d.markPaid(m, paid);
    if (mounted) setState(() => _paying = null);
  }

  /// Your own names (a mention of you is picked out on lime).
  List<String> get _myNames => [
    ...?d.members?.where((m) => m.you).map((m) => m.name),
    if (d.auth.me?.user?.name case final String n when n.isNotEmpty) n,
  ];

  Widget _event(GroupMessage m) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final taskiem = m.senderId == null && (m.event?.type ?? '').startsWith('taskiem_');
    return Semantics(
      container: true,
      label: taskiem ? 'From Taskiem: ${m.body}' : m.body,
      excludeSemantics: true,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 24),
        child: Center(
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            decoration: BoxDecoration(color: taskiem ? look.orange.withValues(alpha: 0.14) : look.card, borderRadius: BorderRadius.circular(14)),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (taskiem) ...[TkIcon('b-shield', size: 14, stroke: 2, color: look.orangeInk), const SizedBox(width: 6)],
                Flexible(child: Text(m.body, textAlign: TextAlign.center, style: tkText(size: 12.5, weight: taskiem ? 600 : 500, lineHeight: 1.35, color: taskiem ? look.orangeInk : t.muted))),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _bubble(GroupLine x, GroupLine? prev, GroupLine? next, Map<String, GroupMessage> byId, DateTime now) {
    final m = x.m;
    if (m.kind == 'event') return _event(m);
    final look = TkLook.of(context);
    bool sameRun(GroupLine? o) => o != null && o.m.kind != 'event' && o.m.mine == m.mine && '${o.m.senderId}' == '${m.senderId}' && o.m.card == null && m.card == null;
    final first = !sameRun(prev);
    final last = !sameRun(next) || dayLabel(next!.m.createdAt, now) != dayLabel(m.createdAt, now);
    final quoted = m.replyTo == null ? null : byId[m.replyTo];
    final who = m.sender;
    final names = [...?d.members?.where((p) => !p.you).map((p) => p.name)];
    final head = !m.mine && first
        ? Padding(
            padding: const EdgeInsets.only(bottom: 2),
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(text: who, style: tkText(size: 13, weight: 700, color: senderInk(context, who))),
                  if (m.mentionsMe == true) TextSpan(text: ' · mentioned you', style: tkText(size: 12, weight: 600, color: look.limeInk)),
                ],
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          )
        : null;
    final bubble = PeopleBubble(
      line: PeopleLine(asPeopleMessage(m), local: x.local, file: x.file),
      first: first,
      last: last,
      other: who,
      quoteName: quoted == null ? null : (quoted.mine ? 'You' : quoted.sender),
      head: head,
      files: d.files,
      quote: quoted == null ? null : asPeopleMessage(quoted),
      quoteMissing: m.replyTo != null && quoted == null,
      busy: _paying == m.id,
      spans: (words, style) => mentionSpans(words, style, names: names, mine: _myNames, ink: look.limeInk, mark: look.lime.withValues(alpha: 0.35)),
      onOpenCard: m.card == null || m.card!.type == 'split'
          ? null
          : m.card!.type == 'basket'
          ? (m.card!.basketId == null ? null : () => showBasket(context, d.auth, m.card!.basketId!))
          : () => showPeopleCard(
              context,
              auth: d.auth,
              chat: d.id,
              message: m.id,
              openBusiness: _openBusiness,
              openOrder: widget.openOrder,
              load: () async {
                final r = await d.api.openGroupCard(id: d.id, message: m.id);
                return OpenPeopleCardResponse.fromJson(r.toJson());
              },
            ),
      onPaid: m.card?.type == 'split' && d.canSend ? (p) => _paid(m, p) : null,
    );
    final reactions = m.deleted == true ? const <GroupMessageReactionsItem>[] : (m.reactions ?? const []);
    Widget shown = reactions.isEmpty
        ? bubble
        : Column(
            crossAxisAlignment: m.mine ? CrossAxisAlignment.end : CrossAxisAlignment.start,
            children: [
              bubble,
              Padding(
                padding: const EdgeInsets.only(top: 2, left: 8, right: 8),
                child: GroupReactionChips(list: reactions, mine: m.myReaction, onReact: d.canSend ? (e) => d.react(m, e) : null, onWho: () => showGroupReactions(context, d, m)),
              ),
            ],
          );
    if (m.mentionsMe == true && !m.mine && m.deleted != true) {
      shown = DecoratedBox(
        decoration: BoxDecoration(border: Border(left: BorderSide(color: look.lime, width: 3))),
        child: Padding(padding: const EdgeInsets.only(left: 4), child: shown),
      );
    }
    if (!m.mine) {
      final p = d.member(m.senderId);
      shown = Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          SizedBox(
            width: 30,
            child: last
                ? ExcludeSemantics(child: PersonFace(name: who, transport: d.auth.transport, photo: p?.person?.photo, size: 30, letterSize: 11))
                : null,
          ),
          const SizedBox(width: 6),
          Expanded(child: shown),
        ],
      );
    }
    return Padding(
      padding: EdgeInsets.only(top: first ? 6 : 0),
      child: BubbleHold(
        label: m.mine ? 'Your message' : 'Message from $who${m.mentionsMe == true ? ', mentioning you' : ''}',
        onHold: x.local ? null : (at) => _hold(m, at),
        onReply: d.canSend && !x.local && m.deleted != true ? () => setState(() => _reply = m) : null,
        child: shown,
      ),
    );
  }

  /// What stands in the composer's place: closed, left, admins only.
  Widget? _notice() {
    final g = d.group;
    if (g == null || d.missing) return null;
    final look = TkLook.of(context);
    final closed = g.closed;
    if (closed != null) {
      final appeal = closed.appeal;
      return _Notice(
        icon: 'b-shield',
        colour: look.red,
        title: closed.words,
        sub: appeal != null ? appealWords(appeal) : (closed.canAppeal && isAdminOf(g) ? 'As an admin, you can ask for this to be looked at again.' : null),
        button: closed.canAppeal && appeal == null && isAdminOf(g) ? 'Appeal' : null,
        onPressed: () => appealGroupFlow(context, d),
      );
    }
    if (g.left) {
      return _Notice(
        icon: 'b-out',
        colour: look.teal,
        title: g.leftHow == 'removed' ? 'You were removed from this group.' : 'You left this group.',
        sub: 'You can read what came before, but nothing new comes and you can’t write.',
      );
    }
    if (!g.canPost) return _Notice(icon: 'b-lock', colour: look.blue, title: 'Only admins can send messages in this group.');
    return null;
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
          final g = d.group;
          final name = d.name;
          final now = d.now();
          final list = d.messages;
          final byId = {for (final m in list ?? const <GroupLine>[]) m.id: m.m};
          final nextOf = <String, GroupLine>{for (var i = 0; list != null && i + 1 < list.length; i++) list[i].id: list[i + 1]};
          final typing = d.typingNames;
          final notice = _notice();
          final at = _at;
          final suggest = at == null || !d.canSend
              ? const <GroupMember>[]
              : [...?d.members?.where((p) => !p.you && p.name.toLowerCase().contains(at.toLowerCase()))].take(5).toList();
          return Semantics(
            label: 'Group $name',
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
                        onMore: g == null ? null : _menu,
                        onAvatar: g == null ? null : _info,
                        avatar: PersonFace(name: name, transport: d.auth.transport, photo: g?.photo, size: 40, letterSize: 15),
                        title: name,
                        subtitle: groupHeadLine(g, typing: d.offline ? const [] : typing, offline: d.offline),
                        actions: [ChatHeadBar.iconButton('Search this group', TkIcon('b-search', size: 18, stroke: 2.2, color: t.text), g == null ? null : _search)],
                      ),
                    ),
                  ),
                  Expanded(
                    child: d.missing
                        ? Center(child: Padding(padding: const EdgeInsets.all(24), child: Text('This group is no longer here.', textAlign: TextAlign.center, style: tkHint(t))))
                        : ThreadView<GroupLine>(
                            key: ValueKey('thread ${d.focusRound}'),
                            items: d.focusing ? null : list,
                            focus: d.focusFound ? d.focus : null,
                            of: (id: (m) => m.id, at: (m) => m.m.createdAt, mine: (m) => m.m.mine),
                            now: now,
                            hasOlder: d.hasOlder,
                            onOlder: d.older,
                            errors: [?d.error],
                            tail: typing.isNotEmpty && !d.offline ? const TypingBubble() : null,
                            empty: Text('No messages yet. Say hello to the group.', textAlign: TextAlign.center, style: tkHint(t)),
                            render: (x, prev) => _bubble(x, prev, nextOf[x.id], byId, now),
                          ),
                  ),
                  if (d.toStanding)
                    _Notice(
                      icon: 'b-shield',
                      colour: TkLook.of(context).red,
                      title: 'Taskiem’s moderators have stopped your account from messaging people for now.',
                      button: 'Your standing',
                      onPressed: () => tkPush<void>(context, YourStandingScreen(auth: d.auth, now: d.now)),
                    ),
                  ?notice,
                  if (suggest.isNotEmpty) _MentionPicker(members: suggest, transport: d.auth.transport, onPick: _mention),
                  if (d.canSend)
                    Composer(
                      text: _text,
                      bottom: pad.bottom,
                      rules: doorFiles,
                      label: 'Message $name',
                      pickers: widget.pickers,
                      recorder: widget.recorder,
                      replying: _reply == null ? null : (_reply!.mine ? 'your message: ' : '${_reply!.sender}: ') + groupLine(_reply!),
                      onCancelReply: () => setState(() => _reply = null),
                      onTools: _tools,
                      onSend: (t, f) => unawaited(_send(t, f)),
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

/// Whether you are one of [g]'s admins now.
bool isAdminOf(Group g) => g.role == 'admin' && !g.left;

/// A line over the box (or in its place): what is so, and what to do.
class _Notice extends StatelessWidget {
  const _Notice({required this.icon, required this.colour, required this.title, this.sub, this.button, this.onPressed});
  final String icon;
  final Color colour;
  final String title;
  final String? sub;
  final String? button;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) => Semantics(
    liveRegion: true,
    child: Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      child: TkAppLook(
        child: TkCallout(leading: TkSquare(icon, color: colour), title: title, sub: sub, color: colour, button: button, onPressed: onPressed),
      ),
    ),
  );
}

/// Members whose names match the @ being typed.
class _MentionPicker extends StatelessWidget {
  const _MentionPicker({required this.members, required this.transport, required this.onPick});
  final List<GroupMember> members;
  final Transport transport;
  final ValueChanged<GroupMember> onPick;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 4, 12, 4),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(18)),
      child: TkAppLook(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final p in members)
              TkListRow(
                title: p.name,
                sub: p.role == 'admin' ? 'Admin' : '',
                face: PersonFace(name: p.name, transport: transport, photo: p.person?.photo, size: 40),
                semanticsLabel: 'Mention ${p.name}',
                onTap: () => onPick(p),
              ),
          ],
        ),
      ),
    );
  }
}

/// The reactions under a message: each emoji with how many, yours tinted;
/// tapped, the same as yours (or yours taken back); held, who reacted.
class GroupReactionChips extends StatelessWidget {
  const GroupReactionChips({super.key, required this.list, this.mine, this.onReact, this.onWho});
  final List<GroupMessageReactionsItem> list;
  final String? mine;
  final ValueChanged<String>? onReact;
  final VoidCallback? onWho;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Semantics(
      container: true,
      label: 'Reactions',
      child: Wrap(
        spacing: 3,
        runSpacing: 3,
        children: [
          for (final r in list)
            PopIn(
              key: ValueKey('react ${r.reaction}'),
              child: Semantics(
                container: true,
                button: onReact != null,
                label: '${reactionNames[r.reaction] ?? r.reaction}, ${r.count}${r.reaction == mine ? ', yours. Tap to take it back.' : '. Tap to react the same.'}',
                excludeSemantics: true,
                child: GestureDetector(
                  onTap: onReact == null ? null : () => onReact!(r.reaction),
                  onLongPress: onWho,
                  // As wide as the emoji and its count (an alignment here
                  // would stretch it across the row).
                  child: Container(
                    constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
                    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                    decoration: BoxDecoration(
                      color: r.reaction == mine ? Color.alphaBlend(look.lime.withValues(alpha: 0.22), look.card) : look.card,
                      borderRadius: BorderRadius.circular(999),
                      boxShadow: const [BoxShadow(color: Color(0x26000000), offset: Offset(0, 1), blurRadius: 3)],
                    ),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(r.reaction, style: tkText(size: 13, lineHeight: 1)),
                        const SizedBox(width: 4),
                        Text('${r.count}', style: tkText(size: 12, weight: 600, lineHeight: 1, color: r.reaction == mine ? t.tintText : t.text)),
                      ],
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

/// Who reacted to [m], and with what.
Future<void> showGroupReactions(BuildContext context, GroupChatData d, GroupMessage m) => showTkSheet<void>(
  context,
  (_) => TkAppLook(
    child: TkSheet(
      title: 'Reactions',
      body: FutureBuilder<GroupMessageReactionsResponse>(
        future: d.api.groupMessageReactions(id: d.id, message: m.id),
        builder: (context, s) {
          if (s.hasError) return Padding(padding: const EdgeInsets.all(14), child: TkErrorBox(sayFailure(s.error!)));
          final r = s.data;
          if (r == null) return const TkLoading(label: 'Loading…');
          return ListView(
            padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
            children: [
              TkPanel(
                children: [
                  for (final x in r.reactions)
                    TkListRow(
                      title: x.name,
                      sub: dayWords(x.at, d.now()),
                      face: PersonFace(name: x.name, transport: d.auth.transport, photo: d.member(x.personId)?.person?.photo, size: 40),
                      trailing: Text(x.reaction, style: tkText(size: 20)),
                    ),
                ],
              ),
            ],
          );
        },
      ),
    ),
  ),
);

/// Your own message's info (A6): who read it (only those whose read ticks
/// go between you), who has it, and how many don't yet.
Future<void> showReadBy(BuildContext context, GroupChatData d, GroupMessage m) => showTkSheet<void>(
  context,
  (_) => TkAppLook(
    child: TkSheet(
      title: 'Message info',
      body: FutureBuilder<GroupMessageInfoResponse>(
        future: d.api.groupMessageInfo(id: d.id, message: m.id),
        builder: (context, s) {
          final t = TkTheme.tokens(context);
          final look = TkLook.of(context);
          if (s.hasError) return Padding(padding: const EdgeInsets.all(14), child: TkErrorBox(sayFailure(s.error!)));
          final r = s.data;
          if (r == null) return const TkLoading(label: 'Loading…');
          String when(Object? at) {
            final w = at is String ? DateTime.tryParse(at) : null;
            return w == null ? '' : '${dayWords(w, d.now())}, ${clock(w)}';
          }

          Widget who(GroupReader x) => TkListRow(
            title: x.name,
            sub: when(x.at),
            face: PersonFace(name: x.name, transport: d.auth.transport, photo: d.member(x.personId)?.person?.photo, size: 40),
          );
          return ListView(
            padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
            children: [
              Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 12), child: Text('“${groupLine(m)}”', maxLines: 2, overflow: TextOverflow.ellipsis, style: tkHint(t))),
              TkSection(
                title: 'Read by',
                children: [
                  if (r.readBy.isEmpty)
                    Padding(padding: const EdgeInsets.all(14), child: Text('No one yet. Only people whose read ticks are on show here.', style: tkHint(t)))
                  else
                    for (final x in r.readBy) who(x),
                ],
              ),
              const SizedBox(height: 14),
              TkSection(
                title: 'Delivered to',
                children: [
                  if (r.deliveredTo.isEmpty) Padding(padding: const EdgeInsets.all(14), child: Text('No one else yet.', style: tkHint(t))) else for (final x in r.deliveredTo) who(x),
                ],
              ),
              if (r.waiting > 0)
                Padding(
                  padding: const EdgeInsets.fromLTRB(4, 12, 4, 0),
                  child: Row(
                    children: [
                      TkIcon('clock', size: 16, stroke: 2, color: look.orangeInk),
                      const SizedBox(width: 6),
                      Expanded(child: Text(r.waiting == 1 ? '1 member doesn’t have it yet' : '${r.waiting} members don’t have it yet', style: tkText(size: 13.5, color: t.text))),
                    ],
                  ),
                ),
            ],
          );
        },
      ),
    ),
  ),
);
