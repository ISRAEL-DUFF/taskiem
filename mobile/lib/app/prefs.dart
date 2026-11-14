// The person's own settings on this phone (docs/spec/mobile-app.md §3.6),
// as the web keeps them in the browser: Appearance (web/src/theme.ts), and
// when Taskiem's conversation was last seen (web/src/lib/taskiemSeen.ts).

import 'dart:async';

import 'package:flutter/foundation.dart';

import 'session.dart';

/// Light, Dark, or Match this device: the web's three, light the default.
enum ThemeChoice { light, dark, system }

class Prefs extends ChangeNotifier {
  Prefs(this.keys);

  final KeyStore keys;
  ThemeChoice theme = ThemeChoice.light;

  /// taskiemSeen.ts: when Taskiem's conversation was last seen at its
  /// newest end; what Taskiem said after it is unread. Kept on the phone,
  /// so it holds when the app is opened again.
  DateTime? taskiemSeen;

  /// moments.ts soundOn: whether new orders and payments make a sound
  /// here (on until turned off).
  bool sound = true;

  /// The app lock (docs/spec/mobile-app.md §5.2), as WhatsApp's: off
  /// until turned on, then how long the app may be away before it locks
  /// (zero: at once). Null: off.
  Duration? lockAfter;

  bool get lock => lockAfter != null;

  /// ChatTools.tsx recentTools: the last two tools opened, newest first.
  List<String> recentTools = const [];

  /// Done once what is kept has been read.
  Future<void> get ready => _ready.future;
  final _ready = Completer<void>();

  Future<void> load() async {
    try {
      final t = await keys.read('theme');
      theme = ThemeChoice.values.firstWhere((c) => c.name == t, orElse: () => ThemeChoice.light);
      taskiemSeen = DateTime.tryParse(await keys.read('taskiem-seen') ?? '');
      sound = await keys.read('chat-sound') != 'off';
      // 'app-lock' was on unless turned off; the lock is now off unless
      // turned on, and that old key is not read.
      final secs = int.tryParse(await keys.read('app-lock-after') ?? '');
      lockAfter = secs == null || secs < 0 ? null : Duration(seconds: secs);
      recentTools = (await keys.read('tools-recent') ?? '').split(',').where((k) => k.isNotEmpty).toList();
    } on Object {
      // The phone's storage not read: the defaults.
    }
    if (!_ready.isCompleted) _ready.complete();
    notifyListeners();
  }

  /// Signing out: what is the person's own (when they last saw Taskiem's
  /// conversation, whether they turned the app lock on) does not pass to
  /// whoever signs in on this phone next. Appearance and sound are the
  /// phone's and stay.
  Future<void> forgetPerson() async {
    taskiemSeen = null;
    lockAfter = null;
    notifyListeners();
    for (final k in const ['taskiem-seen', 'app-lock', 'app-lock-after']) {
      try {
        await keys.write(k, null);
      } on Object {
        // Not removed from storage: already forgotten while the app runs.
      }
    }
  }

  /// The lock on, waiting [after] away (zero: at once), or off (null).
  Future<void> setLockAfter(Duration? after) async {
    lockAfter = after;
    notifyListeners();
    try {
      await keys.write('app-lock-after', after?.inSeconds.toString());
    } on Object {
      // Kept only while the app runs.
    }
  }

  /// ChatTools.tsx remember: a tool opened, first of the two kept.
  Future<void> rememberTool(String key) async {
    recentTools = [key, ...recentTools.where((k) => k != key)].take(2).toList();
    try {
      await keys.write('tools-recent', recentTools.join(','));
    } on Object {
      // Kept only while the app runs.
    }
  }

  Future<void> setSound(bool on) async {
    sound = on;
    notifyListeners();
    try {
      await keys.write('chat-sound', on ? null : 'off');
    } on Object {
      // Kept only while the app runs.
    }
  }

  /// Seen up to [at] (never goes back).
  Future<void> setTaskiemSeen(DateTime at) async {
    final was = taskiemSeen;
    if (was != null && !at.isAfter(was)) return;
    taskiemSeen = at;
    try {
      await keys.write('taskiem-seen', at.toUtc().toIso8601String());
    } on Object {
      // Kept only while the app runs, as the web's when storage is blocked.
    }
  }

  Future<void> setTheme(ThemeChoice t) async {
    theme = t;
    notifyListeners();
    await keys.write('theme', t == ThemeChoice.light ? null : t.name);
  }
}
