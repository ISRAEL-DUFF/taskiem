// The person's own settings on this phone (docs/spec/mobile-app.md §3.6),
// as the web keeps them in the browser: Appearance (web/src/theme.ts), and
// when Taskiem's conversation was last seen (web/src/lib/taskiemSeen.ts).

import 'dart:async';
import 'dart:convert';

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

  /// The day the phone's contacts were last checked for people on Taskiem
  /// (docs/spec/people-chatting.md §5.3), set only once the person agreed:
  /// You checks again by itself at most once a day while the phone still
  /// allows it. Nothing else of the contacts or the answer is kept.
  String? contactsChecked;

  /// The way a bill was last paid to each seller (the payments flow,
  /// settled 2): transfer, taskiem or checkout, by the seller's id. It
  /// comes first on Pay next time. The person's own: forgotten on
  /// signing out.
  Map<String, String> payWays = const {};

  /// The shop (docs/spec/mobile-app-look.md, "The shop"): the items the
  /// person saved with the heart, and the ones they looked at lately
  /// (newest first, [shopRecentKept] at most), by business. Kept on this
  /// phone only (the server keeps neither yet); the person's own:
  /// forgotten on signing out.
  Map<String, List<String>> shopSaved = const {};
  Map<String, List<String>> shopRecent = const {};
  static const shopRecentKept = 12;

  /// Done once what is kept has been read.
  Future<void> get ready => _ready.future;

  /// Whether what is kept has been read.
  bool get loaded => _ready.isCompleted;
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
      contactsChecked = await keys.read('contacts-checked');
      final ways = jsonDecode(await keys.read('pay-ways') ?? '{}');
      if (ways is Map) payWays = {for (final e in ways.entries) if (e.value is String) '${e.key}': e.value as String};
      shopSaved = _lists(await keys.read('shop-saved'));
      shopRecent = _lists(await keys.read('shop-recent'));
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
    contactsChecked = null;
    payWays = const {};
    shopSaved = const {};
    shopRecent = const {};
    notifyListeners();
    for (final k in const ['taskiem-seen', 'app-lock', 'app-lock-after', 'contacts-checked', 'pay-ways', 'shop-saved', 'shop-recent']) {
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

  /// The heart on [item] of [business]'s shop: saved, or no longer.
  Future<void> toggleShopSaved(String business, String item) async {
    final now = shopSaved[business] ?? const <String>[];
    shopSaved = {...shopSaved, business: now.contains(item) ? [...now.where((x) => x != item)] : [item, ...now]};
    notifyListeners();
    await _keep('shop-saved', shopSaved);
  }

  /// [item] of [business]'s shop looked at: first of those seen lately.
  Future<void> viewedShopItem(String business, String item) async {
    final now = shopRecent[business] ?? const <String>[];
    if (now.isNotEmpty && now.first == item) return;
    shopRecent = {...shopRecent, business: [item, ...now.where((x) => x != item)].take(shopRecentKept).toList()};
    notifyListeners();
    await _keep('shop-recent', shopRecent);
  }

  Future<void> _keep(String key, Map<String, List<String>> lists) async {
    try {
      await keys.write(key, jsonEncode(lists));
    } on Object {
      // Kept only while the app runs.
    }
  }

  static Map<String, List<String>> _lists(String? raw) {
    final j = jsonDecode(raw ?? '{}');
    if (j is! Map) return const {};
    return {
      for (final e in j.entries)
        if (e.value is List) '${e.key}': [for (final x in e.value as List) if (x is String) x],
    };
  }

  /// A bill paid to [seller] by [way]: that way first next time.
  Future<void> rememberPayWay(String seller, String way) async {
    if (seller.isEmpty || payWays[seller] == way) return;
    payWays = {...payWays, seller: way};
    try {
      await keys.write('pay-ways', jsonEncode(payWays));
    } on Object {
      // Kept only while the app runs.
    }
  }

  /// The contacts checked today ([day]: `2026-11-18`); null: not checked
  /// again by themselves (Check my contacts daily turned off).
  Future<void> setContactsChecked(String? day) async {
    contactsChecked = day;
    notifyListeners();
    try {
      await keys.write('contacts-checked', day);
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
