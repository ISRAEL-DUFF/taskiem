// Get started (docs/spec/mobile-app-m4.md N7): the number, the code, the
// conversation with Taskiem and its buttons, and signed in at the end.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/app_signup.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/app_sign_up.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'support.dart' show loadFonts;

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> _q(String text, [List<(String, String)> buttons = const []]) => {
  'text': text,
  if (buttons.isNotEmpty) 'buttons': [for (final b in buttons) {'id': b.$1, 'title': b.$2}],
};

(Auth, FakeServer) _world() {
  final server = FakeServer();
  server.answers['GET /v1/me'] = () => _json({'tenant_id': 't1', 'tenant_name': 'Ada Kitchen', 'auth_method': 'chat', 'permissions': <String>[], 'roles': ['owner']});
  server.answers['POST /v1/signup/app/code'] = () => _json({'status': 'We sent a code to +2348031239901 on WhatsApp.'}, 202);
  server.answers['POST /v1/signup/app/verify'] = () => _json({
    'token': 'su-tok',
    'messages': [_q('Before we start: do you agree to the terms?', [('su:yes', 'Yes'), ('su:no', 'No')])],
  });
  final answers = [
    {'messages': [_q('What is your business called?')]},
    {'messages': [_q('Done! *Ada Kitchen* is ready.')], 'done': true, 'token': 'tok-new', 'tenant_id': 't1'},
  ];
  server.answers['POST /v1/signup/app/turn'] = () => _json(answers.removeAt(0));
  final auth = Auth(
    transport: Transport(base: 'http://localhost', client: server.client()),
    store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Ada’s Tecno', platform: 'android')),
  );
  return (auth, server);
}

void main() {
  test('the number, the code, the answers; signed in at the end', () async {
    final (auth, server) = _world();
    final s = AppSignup(auth);
    await s.sendCode('0803 123 9901');
    expect(s.step, AppSignupStep.code);
    await s.verify('123456');
    expect(s.step, AppSignupStep.chat);
    expect(s.buttons.map((b) => b.title), ['Yes', 'No']);
    await s.answer('Yes', reply: 'su:yes');
    final yes = server.calls.lastWhere((c) => c.path == '/v1/signup/app/turn').body!;
    expect(yes['reply'], 'su:yes');
    expect(yes.containsKey('text'), isFalse);
    expect(yes['token'], 'su-tok');
    expect(s.buttons, isEmpty);
    await s.answer('Ada Kitchen');
    final last = server.calls.lastWhere((c) => c.path == '/v1/signup/app/turn').body!;
    expect(last['text'], 'Ada Kitchen');
    expect(last['bearer'], isTrue);
    expect((last['device'] as Map)['name'], 'Ada’s Tecno');
    expect(auth.state, AuthState.signedIn);
    expect(auth.transport.token, 'tok-new');
  });

  test('a turn that fails gives the question’s buttons back', () async {
    final (auth, server) = _world();
    final s = AppSignup(auth);
    await s.sendCode('+2348031239901');
    await s.verify('123456');
    server.offline = true;
    await s.answer('Yes', reply: 'su:yes');
    expect(s.error, contains('offline'));
    expect(s.lines.where((l) => l.mine), isEmpty);
    expect(s.buttons.map((b) => b.title), ['Yes', 'No']);
  });

  test('a number already on an account is sent to sign in', () async {
    final (auth, server) = _world();
    server.answers['POST /v1/signup/app/verify'] = () => _json({'error': 'This number is already on a Taskiem account. Sign in with WhatsApp instead.', 'sign_in': true}, 409);
    final s = AppSignup(auth);
    await s.sendCode('+2348031239901');
    await s.verify('123456');
    expect(s.step, AppSignupStep.signIn);
    expect(s.error, contains('already on a Taskiem account'));
  });

  testWidgets('the screen: the number, then the conversation with its buttons', (t) async {
    await loadFonts();
    t.view.physicalSize = const Size(360, 740);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final (auth, _) = _world();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: AppSignUpScreen(auth: auth)));
    expect(find.text('Get started'), findsOneWidget);
    await t.enterText(find.byType(EditableText).first, '+2348031239901');
    await t.runAsync(() async {
      await t.tap(find.text('Send me a code'));
      await Future<void>.delayed(const Duration(milliseconds: 50));
    });
    await t.pump();
    expect(find.text('We sent a code to +2348031239901 on WhatsApp.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).first, '123456');
    await t.runAsync(() async {
      await t.tap(find.text('Continue'));
      await Future<void>.delayed(const Duration(milliseconds: 50));
    });
    await t.pump();
    expect(find.text('Before we start: do you agree to the terms?'), findsOneWidget);
    expect(find.text('Yes'), findsOneWidget);
    expect(t.takeException(), isNull);
  });
}
