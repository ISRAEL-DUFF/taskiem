// Being here (docs/spec/people-chatting.md §13; the server's As built,
// "Presence"): while the app is open and in front, it says so every 30
// seconds (`POST /v1/me/presence {online: true}`), at once when it comes
// back; and `{online: false}` when it goes to the background. Who sees it
// is the server's: last seen and online go only where both people's
// settings allow (§5.4).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';

/// The heartbeat's beat (the server counts a person online for 75 s).
const presenceBeat = Duration(seconds: 30);

class Presence with WidgetsBindingObserver {
  Presence(this.api, {this.beat = presenceBeat});
  final TaskiemApi api;
  final Duration beat;
  Timer? _timer;
  bool _started = false;

  /// Starts: here now, and every [beat] while in front.
  void start() {
    if (_started) return;
    _started = true;
    WidgetsBinding.instance.addObserver(this);
    _here();
  }

  void _here() {
    _timer?.cancel();
    _say(true);
    _timer = Timer.periodic(beat, (_) => _say(true));
  }

  void _away() {
    _timer?.cancel();
    _timer = null;
    _say(false);
  }

  void _say(bool online) {
    // Nothing to do when it cannot be said: the next beat says it.
    unawaited(api.myPresence(body: MyPresenceRequest(online: online)).catchError((Object _) {}));
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (!_started) return;
    switch (state) {
      case AppLifecycleState.resumed:
        _here();
      case AppLifecycleState.paused || AppLifecycleState.hidden || AppLifecycleState.detached:
        if (_timer != null) _away();
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
    if (away) _say(false);
  }
}
