// The app on the phone's home screen (docs/spec/device-fields.md §7, D2,
// settled by the founder): long-press Taskiem's icon for Scan a code (a
// code scanned with no workflow open offers the workflows that take it)
// and for the person's tools, the ones they opened last on this phone
// first. Each is a tap by the person; neither senses anything on its own.
//
// The phone's quick actions are behind [ShortcutPlatform], so a test gives
// its own.

import 'package:flutter/foundation.dart';
import 'package:quick_actions/quick_actions.dart';

/// The shortcut that scans a code.
const scanShortcut = 'scan';

/// A tool's shortcut: "tool:expense".
String toolShortcut(String command) => 'tool:$command';

/// The command of a tool's shortcut, or null.
String? shortcutTool(String type) => type.startsWith('tool:') ? type.substring(5) : null;

/// How many tools are put on the icon beside Scan (phones show four).
const maxToolShortcuts = 3;

/// The phone's quick actions, behind one face.
abstract class ShortcutPlatform {
  /// [onAction] is called with a shortcut's type when it is tapped.
  void initialize(void Function(String type) onAction);
  Future<void> set(List<({String type, String title})> items);
}

/// The phone's own, through quick_actions.
class PhoneShortcuts implements ShortcutPlatform {
  final _q = const QuickActions();

  @override
  void initialize(void Function(String type) onAction) => _q.initialize(onAction);

  @override
  Future<void> set(List<({String type, String title})> items) =>
      _q.setShortcutItems([for (final i in items) ShortcutItem(type: i.type, localizedTitle: i.title)]);
}

/// The shortcuts on the icon, and the one tapped, waiting to be opened.
class HomeShortcuts extends ChangeNotifier {
  HomeShortcuts(this.platform) {
    try {
      platform.initialize((type) {
        _pending = type;
        notifyListeners();
      });
    } on Object {
      // A phone with no quick actions: none.
    }
  }

  final ShortcutPlatform platform;
  String? _pending;

  /// The shortcut tapped, once.
  String? take() {
    final p = _pending;
    _pending = null;
    return p;
  }

  /// What the icon offers for this person: Scan a code, then up to three of
  /// their tools, those opened last here first ([recent] holds the panel's
  /// keys, "tool:command"), then by name.
  Future<void> show(List<({String command, String name})> tools, {List<String> recent = const []}) async {
    final order = [
      for (final k in recent)
        ...tools.where((t) => toolShortcut(t.command) == k),
      ...([...tools]..sort((a, b) => a.name.compareTo(b.name))),
    ];
    final seen = <String>{};
    final pick = [for (final t in order) if (seen.add(t.command)) t].take(maxToolShortcuts);
    try {
      await platform.set([
        (type: scanShortcut, title: 'Scan a code'),
        for (final t in pick) (type: toolShortcut(t.command), title: t.name),
      ]);
    } on Object {
      // Not offered on this phone.
    }
  }

  /// Signed out: none but Scan, which asks to sign in.
  Future<void> clear() async {
    try {
      await platform.set(const []);
    } on Object {
      // Not offered on this phone.
    }
  }
}
