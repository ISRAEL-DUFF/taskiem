// Reading again while a screen is open (the web's useLiveRefresh): every so
// often while it is the screen on top and the app is in front; not while
// another screen covers it or the app is put away; once at once when the
// app comes back (the web reads again on becoming visible).

import 'dart:async';

import 'package:flutter/widgets.dart';

mixin Polling<T extends StatefulWidget> on State<T> {
  Timer? _poll;
  AppLifecycleListener? _life;
  bool _awake = true;

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
    _life ??= AppLifecycleListener(
      onStateChange: (s) {
        final was = _awake;
        _awake = s == AppLifecycleState.resumed;
        if (_awake && !was && mounted) read();
      },
    );
  }

  @override
  void dispose() {
    _poll?.cancel();
    _life?.dispose();
    super.dispose();
  }
}
