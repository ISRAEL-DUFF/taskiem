import 'dart:io';

import 'package:flutter/services.dart';
import 'package:taskiem/theme/theme.dart';

/// Loads the app's own fonts, so a test draws the web's Inter, not the test
/// font.
Future<void> loadFonts() async {
  const files = {
    'Inter': 'Inter-latin.ttf',
    'InterLatinExt': 'Inter-latin-ext.ttf',
    'InterVietnamese': 'Inter-vietnamese.ttf',
    'InterGreek': 'Inter-greek.ttf',
    'InterGreekExt': 'Inter-greek-ext.ttf',
    'InterCyrillic': 'Inter-cyrillic.ttf',
    'InterCyrillicExt': 'Inter-cyrillic-ext.ttf',
  };
  assert(files.keys.skip(1).toList().toString() == tkFallback.toString());
  for (final e in files.entries) {
    final loader = FontLoader(e.key)
      ..addFont(Future.value(ByteData.sublistView(File('assets/fonts/${e.value}').readAsBytesSync())));
    await loader.load();
  }
}
