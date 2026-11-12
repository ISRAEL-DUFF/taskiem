// Large text and a small phone (docs/spec/mobile-app.md §8; T5 of
// docs/spec/mobile-app-m3-fixes.md): every screen the parity test draws,
// at 360 px wide, with the phone's text 1.3 and 2 times its size, light
// and dark: nothing overflows or is cut off where it should wrap.

@Tags(['sizes'])
library;

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'parity_test.dart' show screens;
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  for (final scale in [1.3, 2.0]) {
    for (final mode in TkMode.values) {
      for (final e in screens.entries) {
        testWidgets('${e.key}, text ×$scale, ${mode.name}, 360 px', (t) async {
          t.view.physicalSize = const Size(360, 640);
          t.view.devicePixelRatio = 1;
          t.platformDispatcher.textScaleFactorTestValue = scale;
          addTearDown(t.view.reset);
          addTearDown(t.platformDispatcher.clearTextScaleFactorTestValue);
          final overflows = <String>[];
          final was = FlutterError.onError;
          FlutterError.onError = (d) {
            final s = d.exceptionAsString();
            if (s.contains('overflowed')) {
              // Where: the widget that overflowed, by its place in the code.
              final at = RegExp(r'lib/[a-z_/]+\.dart:\d+').firstMatch(d.toString())?.group(0) ?? '?';
              overflows.add('${s.split('\n').first} at $at');
            } else {
              was?.call(d);
            }
          };
          addTearDown(() => FlutterError.onError = was);
          await t.pumpWidget(TaskiemApp(mode: mode, home: e.value.build()));
          await t.pumpAndSettle();
          await e.value.drive(t);
          await t.pumpAndSettle();
          FlutterError.onError = was;
          expect(overflows, isEmpty);
          // Nothing scrolls sideways: no render box wider than the screen.
          final wide = <String>[];
          void look(RenderObject r) {
            // Inside a clip (a brand pattern drawn larger than the screen)
            // or a scrolling list (photos, chips), wider is meant.
            // A picture drawn at its own size and scaled to fit is no wider.
            if (r is RenderClipRect || r is RenderClipRRect || r is RenderClipPath || r is RenderViewportBase || r is RenderFittedBox) return;
            if (r is RenderBox && r.hasSize && r.size.width > 360.5 && r is! RenderView) wide.add('${r.runtimeType} ${r.size.width}');
            r.visitChildren(look);
          }
          look(t.binding.renderViews.first);
          expect(wide.take(5), isEmpty);
        });
      }
    }
  }
}
