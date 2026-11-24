// What can be pressed (docs/spec/mobile-app.md §8; T6 of
// docs/spec/mobile-app-m3-fixes.md): on every screen the parity test
// draws, each thing to press has words for a screen reader, and is at
// least 48 by 48 to a finger (Android's guideline; an iPhone's 44 is less).

@Tags(['targets'])
library;

import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'parity_test.dart' show screens;
import 'guidelines.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  for (final e in screens.entries) {
    testWidgets('${e.key}: labelled, and big enough to press', (t) async {
      final handle = t.ensureSemantics();
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: e.value.build()));
      await t.pumpAndSettle();
      await e.value.drive(t);
      await t.pumpAndSettle();
      handle.dispose();
      await expectPressable(t);
    });
  }
}

