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
        'Sign in with WhatsApp or Telegram',
        'Create an account',
      ]) {
        expect(find.text(words), findsOneWidget, reason: words);
      }
    });
  }
}
