// The chat as it happens: the server's stream of changes
// (GET /v1/chat/stream, server-sent events), followed as the web's
// ChatLive.tsx follows it. A message (new, or changed: its read mark, its
// reactions), a message gone, a conversation's settings, who is typing, and
// Taskiem's conversation. It reconnects when it stalls or drops, sending
// Last-Event-ID so nothing is missed or told twice; while it is connected
// the screens poll slowly, only to reconcile.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import '../api/api.g.dart';
import '../api/transport.dart';

/// What the stream tells.
sealed class LiveEvent {
  const LiveEvent();
}

class LiveMessage extends LiveEvent {
  const LiveMessage(this.message);
  final LinkMessage message;
}

class LiveGone extends LiveEvent {
  const LiveGone(this.id, this.link);
  final String id;
  final String link;
}

class LiveConversation extends LiveEvent {
  const LiveConversation(this.link, {required this.muted, required this.archived, required this.pinned});
  final String link;
  final bool muted;
  final bool archived;
  final bool pinned;
}

/// Taskiem's conversation changed (its messages, as GET /v1/taskiem-chat).
class LiveTaskiem extends LiveEvent {
  const LiveTaskiem(this.messages);
  final List<Map<String, dynamic>> messages;
}

/// One server-sent event.
typedef SseEvent = ({String event, String data, String? id});

/// Reads server-sent events from a body as it comes (the EventSource
/// format: `event:`, `data:` (lines joined), `id:`, a blank line ending
/// each; comments start with a colon).
Stream<SseEvent> readSse(Stream<List<int>> body) async* {
  var event = 'message';
  final data = <String>[];
  String? id;
  await for (final line in const LineSplitter().bind(utf8.decoder.bind(body))) {
    if (line.isEmpty) {
      if (data.isNotEmpty) yield (event: event, data: data.join('\n'), id: id);
      event = 'message';
      data.clear();
      continue;
    }
    if (line.startsWith(':')) continue;
    final colon = line.indexOf(':');
    final field = colon < 0 ? line : line.substring(0, colon);
    var value = colon < 0 ? '' : line.substring(colon + 1);
    if (value.startsWith(' ')) value = value.substring(1);
    switch (field) {
      case 'event':
        event = value;
      case 'data':
        data.add(value);
      case 'id':
        id = value;
    }
  }
}

/// ChatLive.tsx timing(): when a quiet stream counts as stalled, how often
/// that is checked, and how long after a drop it is tried again.
class LiveTiming {
  const LiveTiming({this.stall = const Duration(seconds: 30), this.check = const Duration(seconds: 5), this.retry = const Duration(seconds: 30)});
  final Duration stall;
  final Duration check;
  final Duration retry;
}

/// The stream, followed while the app is in front.
class ChatLive extends ChangeNotifier {
  ChatLive(this.transport, {this.timing = const LiveTiming(), DateTime Function()? clock}) : _clock = clock ?? DateTime.now;

  final Transport transport;
  final LiveTiming timing;
  final DateTime Function() _clock;

  /// Connected: the screens poll slowly.
  bool connected = false;

  /// The conversations whose other side is typing now.
  Set<String> typing = const {};

  final _events = StreamController<LiveEvent>.broadcast();
  Stream<LiveEvent> get events => _events.stream;

  String? _lastId;
  bool _running = false;
  StreamSubscription<SseEvent>? _sub;
  Timer? _watch;
  Timer? _retry;
  DateTime _heard = DateTime.fromMillisecondsSinceEpoch(0);
  int _generation = 0;

  /// Starts following (signed in, the app in front).
  void start() {
    if (_running) return;
    _running = true;
    _open();
  }

  /// Stops (the app behind another, or signed out).
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
      final res = await transport.stream('/v1/chat/stream', headers: {'Last-Event-ID': ?_lastId});
      if (gen != _generation || !_running) {
        unawaited(res.stream.listen(null).cancel());
        return;
      }
      _sub = readSse(res.stream).listen(_on, onError: (Object _) => _dropped(gen), onDone: () => _dropped(gen), cancelOnError: true);
    } catch (_) {
      // Offline, refused (429: too many open), or the session gone: the
      // screens poll as before, and it is tried again later.
      _dropped(gen);
    }
  }

  void _dropped(int gen) {
    if (gen != _generation) return;
    _close();
    if (_running) {
      _retry?.cancel();
      _retry = Timer(timing.retry, () {
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
    typing = const {};
    if (was) notifyListeners();
  }

  void _on(SseEvent e) {
    _hear();
    if (e.id != null) _lastId = e.id;
    Map<String, dynamic> json() => jsonDecode(e.data) as Map<String, dynamic>;
    try {
      switch (e.event) {
        case 'ready':
          connected = true;
          notifyListeners();
        case 'ping':
          break;
        case 'message':
          _events.add(LiveMessage(LinkMessage.fromJson(json()['message'] as Map<String, dynamic>)));
        case 'gone':
          final d = json();
          _events.add(LiveGone(d['id'] as String, d['link'] as String));
        case 'conversation':
          final d = json();
          _events.add(LiveConversation(d['link'] as String,
              muted: d['muted'] == true, archived: d['archived'] == true, pinned: d['pinned'] == true));
        case 'typing':
          final list = (json()['typing'] as List).cast<String>();
          typing = list.isEmpty ? const {} : list.toSet();
          notifyListeners();
        case 'taskiem':
          _events.add(LiveTaskiem((json()['messages'] as List).cast<Map<String, dynamic>>()));
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
