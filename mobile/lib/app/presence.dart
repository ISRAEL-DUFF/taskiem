// Being here (docs/spec/people-chatting.md §13; the server's As built,
// "Presence" and "Presence on the connection"). While the app is open and
// in front, You's live connection (`GET /v1/me/live`, lib/chat/people_live.dart)
// is open, and the server counts the person online while it is: when it is
// ready, the app says that connection is in front (`POST /v1/me/presence
// {online: true, connection}`); when the app goes to the background it says
// it is away (`{online: false, connection}`) and closes it. While the
// connection is not open (refused, offline, an older server), the heartbeat
// stands in as before: `{online: true}` at once and every 30 seconds, and
// `{online: false}` on going away. Who sees it is the server's: last seen and
// online go only where both people's settings allow (§5.4).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../chat/people_live.dart';

/// The heartbeat's beat (the server counts a person online for 75 s).
const presenceBeat = Duration(seconds: 30);

class Presence with WidgetsBindingObserver {
  Presence(this.api, {this.beat = presenceBeat, this.live});
  final TaskiemApi api;
  final Duration beat;

  /// You's live connection: opened in front, closed away; while it is
  /// open, no heartbeat is needed.
  final PeopleLive? live;
  Timer? _timer;
  bool _started = false;
  bool _front = false;

  /// Starts: here now, and kept so while in front.
  void start() {
    if (_started) return;
    _started = true;
    WidgetsBinding.instance.addObserver(this);
    live?.onReady = _ready;
    _here();
  }

  void _here() {
    _front = true;
    _timer?.cancel();
    live?.start();
    // At once, whatever the connection does: the heartbeat until it is
    // ready (said again then, with the connection), and in its place while
    // it is not open.
    if (live?.connected != true) _say(true);
    _timer = Timer.periodic(beat, (_) {
      if (live?.connected != true) _say(true);
    });
  }

  /// The connection is ready: it is the one in front.
  void _ready(String connection) {
    if (_started && _front) _say(true, connection: connection);
  }

  void _away() {
    _front = false;
    _timer?.cancel();
    _timer = null;
    // Said for the connection while it is open, then closed: the server
    // takes the person as away, last seen now.
    _say(false, connection: live?.connection);
    live?.stop();
  }

  void _say(bool online, {String? connection}) {
    // Nothing to do when it cannot be said: the next beat says it.
    unawaited(api.myPresence(body: MyPresenceRequest(online: online, connection: connection)).catchError((Object _) {}));
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (!_started) return;
    switch (state) {
      case AppLifecycleState.resumed:
        if (!_front) _here();
      case AppLifecycleState.paused || AppLifecycleState.hidden || AppLifecycleState.detached:
        if (_front) _away();
      case AppLifecycleState.inactive:
        break;
    }
  }

  /// Stops (the session ends or the screen goes): away, and no more beats.
  void stop({bool away = true}) {
    if (!_started) return;
    _started = false;
    WidgetsBinding.instance.removeObserver(this);
    _timer?.cancel();
    _timer = null;
    if (live?.onReady == _ready) live?.onReady = null;
    final connection = live?.connection;
    if (away) _say(false, connection: connection);
    live?.stop();
    _front = false;
  }
}
