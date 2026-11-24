import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  for (final mode in TkMode.values) {
    testWidgets('the sign-in screen has the web\'s words and ways in, ${mode.name}', (tester) async {
      tester.view.physicalSize = const Size(390, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await tester.pumpWidget(TaskiemApp(mode: mode));
      await tester.pumpAndSettle();
      for (final words in [
        'Sign in to Taskiem',
        'Email',
        'Password',
        'Sign in',
        'Forgot password?',
        'Sign in with a passkey',
        'A code on WhatsApp or Telegram',
        'Create an account',
      ]) {
        expect(find.text(words), findsOneWidget, reason: words);
      }
      // The two apps' logos, not a long button.
      expect(find.bySemanticsLabel('Sign in with a code on WhatsApp'), findsOneWidget);
      expect(find.bySemanticsLabel('Sign in with a code on Telegram'), findsOneWidget);
    });
  }
}
