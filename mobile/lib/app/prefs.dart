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

  /// Done once what is kept has been read.
  Future<void> get ready => _ready.future;
  final _ready = Completer<void>();

  Future<void> load() async {
    try {
      final t = await keys.read('theme');
      theme = ThemeChoice.values.firstWhere((c) => c.name == t, orElse: () => ThemeChoice.light);
      taskiemSeen = DateTime.tryParse(await keys.read('taskiem-seen') ?? '');
      sound = await keys.read('chat-sound') != 'off';
    } on Object {
      // The phone's storage not read: the defaults.
    }
    if (!_ready.isCompleted) _ready.complete();
    notifyListeners();
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
