// The person's own settings on this phone (docs/spec/mobile-app.md §3.6),
// as the web keeps them in the browser: Appearance (web/src/theme.ts).

import 'package:flutter/foundation.dart';

import 'session.dart';

/// Light, Dark, or Match this device: the web's three, light the default.
enum ThemeChoice { light, dark, system }

class Prefs extends ChangeNotifier {
  Prefs(this.keys);

  final KeyStore keys;
  ThemeChoice theme = ThemeChoice.light;

  Future<void> load() async {
    final t = await keys.read('theme');
    theme = ThemeChoice.values.firstWhere((c) => c.name == t, orElse: () => ThemeChoice.light);
    notifyListeners();
  }

  Future<void> setTheme(ThemeChoice t) async {
    theme = t;
    notifyListeners();
    await keys.write('theme', t == ThemeChoice.light ? null : t.name);
  }
}
