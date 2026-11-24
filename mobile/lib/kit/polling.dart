// Reading again while a screen is open (the web's useLiveRefresh): every so
// often while it is the screen on top and the app is in front; not while
// another screen covers it or the app is put away; once at once when the
// app comes back (the web reads again on becoming visible).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart' show Transport;

/// Whether the app is in front (a test's binding may not say: in front).
bool appInFront() {
  final s = WidgetsBinding.instance.lifecycleState;
  return s == null || s == AppLifecycleState.resumed;
}

mixin Polling<T extends StatefulWidget> on State<T> {
  Timer? _poll;
  AppLifecycleListener? _life;
  StreamSubscription<String>? _fresh;
  Timer? _soon;
  bool _awake = true;

  /// Whether the screen was on top when last looked at.
  bool _wasCurrent = true;

  /// Something it shows changed while it was not seen: read once it is.
  VoidCallback? _staleRead;

  /// Whether the screen is seen now: the app in front and the screen on
  /// top.
  bool get seenNow => mounted && _awake && appInFront() && ModalRoute.of(context)?.isCurrent != false;

  /// Something the screen shows changed (the live connection said so):
  /// [read] now while it is seen; else nothing is read until it is on top
  /// again (or the app back in front), and then once.
  void readWhenSeen(VoidCallback read) {
    if (seenNow) {
      _staleRead = null;
      read();
      return;
    }
    _staleRead = read;
  }

  /// Reads what changed while it was not seen; true when there was any.
  bool _readStale() {
    final r = _staleRead;
    if (r == null || !mounted || !_awake) return false;
    _staleRead = null;
    r();
    return true;
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    // Depends on whether the route is on top: told when the screen over
    // it closes.
    final current = ModalRoute.isCurrentOf(context) ?? true;
    if (current && !_wasCurrent) _readStale();
    _wasCurrent = current;
  }

  /// Calls [read] every [every] while the screen is seen; and once when the
  /// app comes back to the front.
  void startPolling(Duration every, VoidCallback read) {
    _poll?.cancel();
    _poll = Timer.periodic(every, (_) {
      if (!mounted || !_awake) return;
      // Another screen over it: it reads again once it is on top.
      if (ModalRoute.of(context)?.isCurrent == false) return;
      read();
    });
    // A kept page drawn at once and since brought up to date: read again,
    // which now answers the fresh copy from the phone.
    // Several pages brought up to date together are read once; a screen
    // covered by another reads again when it is on top, as the timer does.
    _fresh ??= Transport.pageChanged.stream.listen((_) {
      _soon?.cancel();
      _soon = Timer(const Duration(milliseconds: 300), () {
        if (!mounted || !_awake || ModalRoute.of(context)?.isCurrent == false) return;
        read();
      });
    });
    _life ??= AppLifecycleListener(
      onStateChange: (s) {
        final was = _awake;
        _awake = s == AppLifecycleState.resumed;
        if (_awake && !was && mounted && !_readStale()) read();
      },
    );
  }

  @override
  void dispose() {
    _poll?.cancel();
    _fresh?.cancel();
    _soon?.cancel();
    _life?.dispose();
    super.dispose();
  }
}
