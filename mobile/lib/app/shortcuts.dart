// The app on the phone's home screen (docs/spec/device-fields.md §7, D2,
// settled by the founder): long-press Taskiem's icon for Scan a code (a
// code scanned with no workflow open offers the workflows that take it)
// and for the person's tools, the ones they opened last on this phone
// first. Each is a tap by the person; neither senses anything on its own.
// Each shortcut names the business it was put there for: signed out, in a
// personal account or another business, they are taken off, and one
// tapped for another business is not opened in this one.
//
// The phone's quick actions are behind [ShortcutPlatform], so a test gives
// its own.

import 'package:flutter/foundation.dart';
import 'package:quick_actions/quick_actions.dart';

/// The shortcut that scans a code, for a business: `scan:<business>`.
String scanShortcut(String business) => 'scan:$business';

/// A tool's shortcut, for a business: `tool:<business>:expense`.
String toolShortcut(String business, String command) => 'tool:$business:$command';

/// A shortcut tapped, read: what it opens (scan, or a tool's command) and
/// the business it was put there for; null for one this app does not
/// know (one kept from before, with no business).
({String business, String? tool})? readShortcut(String type) {
  final scan = RegExp(r'^scan:([^:]+)$').firstMatch(type);
  if (scan != null) return (business: scan[1]!, tool: null);
  final tool = RegExp(r'^tool:([^:]+):([a-z][a-z0-9-]*)$').firstMatch(type);
  if (tool != null) return (business: tool[1]!, tool: tool[2]!);
  return null;
}

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

  /// The business the icon's shortcuts are for now; null when none are.
  String? business;

  /// What the icon offers for this person in [forBusiness]: Scan a code,
  /// then up to three of their tools, those opened last here first
  /// ([recent] holds the panel's keys, "tool:command"), then by name.
  Future<void> show(String forBusiness, List<({String command, String name})> tools, {List<String> recent = const []}) async {
    business = forBusiness;
    final order = [
      for (final k in recent)
        ...tools.where((t) => 'tool:${t.command}' == k),
      ...([...tools]..sort((a, b) => a.name.compareTo(b.name))),
    ];
    final seen = <String>{};
    final pick = [for (final t in order) if (seen.add(t.command)) t].take(maxToolShortcuts);
    try {
      await platform.set([
        (type: scanShortcut(forBusiness), title: 'Scan a code'),
        for (final t in pick) (type: toolShortcut(forBusiness, t.command), title: t.name),
      ]);
    } on Object {
      // Not offered on this phone.
    }
  }

  /// Signed out, in a personal account, or moved to another business:
  /// none. One tapped before is still read, and opened only in the
  /// business it names.
  Future<void> clear() async {
    business = null;
    try {
      await platform.set(const []);
    } on Object {
      // Not offered on this phone.
    }
  }
}
