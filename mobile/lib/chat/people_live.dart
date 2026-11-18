// You's live connection (docs/spec/people-chatting.md §13; the server's As
// built, "stage 5a gaps, chat features": `GET /v1/me/live`, server-sent
// events): what changes in the person's own people chats as it happens. A
// message new or changed (arrived, delivered, read, a reaction, a star from
// another of their devices, deleted for everyone), a chat new or changed or
// gone, a message deleted on another device, who is typing, and who is
// online. Followed only while the app is in front (Presence opens and closes
// it); reconnected with Last-Event-ID when it drops or stalls, so what was
// missed comes (an event may come twice: merged by id). While it is open
// the screens poll slowly, only to reconcile; while it is not, as before.
//
// It carries the person's own copy only, as the server reads it in their own
// transaction. People chats are kept by Taskiem, readable by its servers;
// nothing here makes them end-to-end encrypted, and nothing says so.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import 'live.dart' show LiveTiming, SseEvent, readSse;

/// What the connection tells of the person's chats.
sealed class PeopleLiveEvent {
  const PeopleLiveEvent();

  /// The chat it is about.
  String get chatId;
}

/// A message of one of their chats, new or changed; [request]: it waits
/// in Requests.
class PeopleLiveMessage extends PeopleLiveEvent {
  const PeopleLiveMessage(this.chatId, this.message, {this.request = false});
  @override
  final String chatId;
  final PeopleMessage message;
  final bool request;
}

/// A chat new or changed ([chat]), or deleted for the person ([hidden]).
class PeopleLiveChat extends PeopleLiveEvent {
  const PeopleLiveChat(this.chatId, {this.chat, this.hidden = false});
  @override
  final String chatId;
  final PeopleChat? chat;
  final bool hidden;
}

/// A message deleted for the person on another device ([id]), or a request
/// they deleted ([id] null).
class PeopleLiveGone extends PeopleLiveEvent {
  const PeopleLiveGone(this.chatId, this.id);
  @override
  final String chatId;
  final String? id;
}

/// Someone's online or last seen, as both settings let it be seen (both
/// null when hidden).
typedef PeoplePresence = ({bool? online, String? lastSeenAt});

/// A person's card with what the connection said of their presence since.
PersonCard withPresence(PersonCard p, PeoplePresence? now) {
  if (now == null || p.deleted) return p;
  return PersonCard.fromJson({...p.toJson(), 'online': now.online, 'last_seen_at': now.lastSeenAt});
}

/// The connection, followed while the app is in front.
class PeopleLive extends ChangeNotifier {
  PeopleLive(this.transport, {this.timing = const LiveTiming(), DateTime Function()? clock}) : _clock = clock ?? DateTime.now;

  final Transport transport;
  final LiveTiming timing;
  final DateTime Function() _clock;

  static final _of = Expando<PeopleLive>('people live');

  /// The connection You keeps for [auth] while it is open; null in a
  /// business's session (its screens poll as before).
  static PeopleLive? of(Auth auth) => _of[auth];

  /// You holds [live] for [auth] (null: no more).
  static void hold(Auth auth, PeopleLive? live) => _of[auth] = live;

  /// Connected (the ready event came): the screens poll slowly.
  bool connected = false;

  /// This connection's id (the ready event's), for presence.
  String? connection;

  /// The chats whose other person is typing now.
  Set<String> typing = const {};

  /// What the connection said of each chat's other person, by chat id.
  final Map<String, PeoplePresence> presence = {};

  final _events = StreamController<PeopleLiveEvent>.broadcast();
  Stream<PeopleLiveEvent> get events => _events.stream;

  /// Told each time a connection is ready (Presence says it is in front).
  void Function(String connection)? onReady;

  String? _lastId;
  bool _running = false;
  bool get running => _running;
  StreamSubscription<SseEvent>? _sub;
  Timer? _watch;
  Timer? _retry;
  DateTime _heard = DateTime.fromMillisecondsSinceEpoch(0);
  int _generation = 0;

  /// Starts following (signed in on You, the app in front).
  void start() {
    if (_running) return;
    _running = true;
    _retry?.cancel();
    _open();
  }

  /// Stops (the app put away, You closed): the connection closes, which
  /// the server takes as away once no other is in front.
  void stop() {
    _running = false;
    _retry?.cancel();
    _close();
  }

  void _hear() => _heard = _clock();

  Future<void> _open() async {
    final gen = ++_generation;
    _hear();
    _watch?.cancel();
    _watch = Timer.periodic(timing.check, (_) {
      if (_clock().difference(_heard) > timing.stall) {
        _close();
        if (_running) _open();
      }
    });
    try {
      final res = await transport.stream('/v1/me/live', headers: {'Last-Event-ID': ?_lastId});
      if (gen != _generation || !_running) {
        unawaited(res.stream.listen(null).cancel());
        return;
      }
      _sub = readSse(res.stream).listen(_on, onError: (Object _) => _dropped(gen), onDone: () => _dropped(gen), cancelOnError: true);
    } catch (e) {
      // Offline, too many open (429, Retry-After), or the session gone:
      // the screens poll as before, and it is tried again later.
      _dropped(gen, wait: _retryAfter(e));
    }
  }

  static Duration? _retryAfter(Object e) {
    if (e is! ApiError || e.status != 429) return null;
    return const Duration(seconds: 30);
  }

  void _dropped(int gen, {Duration? wait}) {
    if (gen != _generation) return;
    _close();
    if (_running) {
      _retry?.cancel();
      _retry = Timer(wait != null && wait > timing.retry ? wait : timing.retry, () {
        if (_running) _open();
      });
    }
  }

  void _close() {
    _generation++;
    _watch?.cancel();
    _sub?.cancel();
    _sub = null;
    final was = connected || typing.isNotEmpty;
    connected = false;
    connection = null;
    typing = const {};
    if (was) notifyListeners();
  }

  /// One event, as the server sends it (a test may say one).
  @visibleForTesting
  void hearEvent(SseEvent e) => _on(e);

  void _on(SseEvent e) {
    _hear();
    if (e.id != null && e.id!.isNotEmpty) _lastId = e.id;
    Map<String, dynamic> json() => jsonDecode(e.data) as Map<String, dynamic>;
    try {
      switch (e.event) {
        case 'ready':
          connected = true;
          connection = json()['connection'] as String?;
          notifyListeners();
          final c = connection;
          if (c != null) onReady?.call(c);
        case 'ping':
          break;
        case 'message':
          final d = json();
          _events.add(PeopleLiveMessage(d['chat_id'] as String, PeopleMessage.fromJson(d['message'] as Map<String, dynamic>), request: d['request'] == true));
        case 'chat':
          final d = json();
          final hidden = d['hidden'] == true;
          _events.add(PeopleLiveChat(d['id'] as String, hidden: hidden, chat: hidden || d['chat'] == null ? null : PeopleChat.fromJson(d['chat'] as Map<String, dynamic>)));
        case 'gone':
          final d = json();
          _events.add(PeopleLiveGone(d['chat_id'] as String, d['id'] as String?));
        case 'typing':
          final list = (json()['typing'] as List).cast<String>().toSet();
          if (!setEquals(list, typing)) {
            typing = list.isEmpty ? const {} : list;
            notifyListeners();
          }
        case 'presence':
          for (final p in (json()['people'] as List).cast<Map<String, dynamic>>()) {
            final chat = p['chat_id'];
            if (chat is! String) continue;
            presence[chat] = (online: p['online'] as bool?, lastSeenAt: p['last_seen_at'] as String?);
          }
          notifyListeners();
      }
    } on Object {
      // An event this app does not read: passed over.
    }
  }

  @override
  void dispose() {
    stop();
    _events.close();
    super.dispose();
  }
}

/// Reading again while the connection is open: only every so often, to
/// reconcile; at the screen's own pace while it is not.
class SlowWhileLive {
  SlowWhileLive({this.every = const Duration(seconds: 30), DateTime Function()? clock}) : _clock = clock ?? DateTime.now;
  final Duration every;
  final DateTime Function() _clock;
  DateTime? _last;

  /// Whether a poll should read now ([live] connected or not).
  bool due(PeopleLive? live) {
    final now = _clock();
    if (live?.connected == true && _last != null && now.difference(_last!) < every) return false;
    _last = now;
    return true;
  }
}
