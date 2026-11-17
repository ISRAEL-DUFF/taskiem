// A forgotten password in the app (as drawn): the email asked for, the link
// sent; the link opened on this phone opens Choose a new password; saved,
// then signing in with it.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/links.dart' as links;
import 'package:taskiem/app/session.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/forgot_password.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'support.dart' show loadFonts;

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

void main() {
  setUpAll(loadFonts);

  test('a new password: at least 12 characters, the same twice', () {
    expect(newPasswordProblem('short', 'short'), 'At least 12 characters.');
    expect(newPasswordProblem('a long enough one', 'a long enough onE'), 'The two are not the same.');
    expect(newPasswordProblem('a long enough one', 'a long enough one'), isNull);
  });

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  testWidgets('Forgot password?, the link sent; the link opens Choose a new password; saved; signed in with it', (t) async {
    t.view.physicalSize = const Size(390, 1000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final server = FakeServer();
    server.answers['GET /v1/signup'] = () => _json({'enabled': false});
    server.answers['GET /v1/app/config'] = () => _json(<String, Object>{});
    server.answers['POST /v1/auth/password/forgot'] = () => _json({'status': 'If that email belongs to an account…'}, 202);
    server.answers['POST /v1/auth/password/reset'] = () => _json({'status': 'Your password is changed'});
    server.answers['POST /v1/auth/login'] = () => _json({'error': 'invalid email or password'}, 401);
    var mailOpened = 0;
    final before = links.openMailApp;
    links.openMailApp = () async {
      mailOpened++;
      return true;
    };
    addTearDown(() => links.openMailApp = before);
    final auth = Auth(
      transport: Transport(base: 'http://localhost', client: server.client()),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    await t.runAsync(auth.start);
    final arriving = StreamController<Uri>();
    addTearDown(arriving.close);
    final inbox = LinkInbox(links: arriving.stream, first: () async => null);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: auth, links: inbox));
    await settle(t);
    // Signed out, the app opens on the phone way in; email is a link away.
    await t.tap(find.bySemanticsLabel('Sign in with email'));
    await settle(t);

    await t.enterText(find.byType(EditableText).first, 'ada@adaskitchen.ng');
    await t.tap(find.bySemanticsLabel('Forgot password?'));
    await settle(t);
    expect(find.text('Reset your password'), findsOneWidget);
    expect(find.text('ada@adaskitchen.ng'), findsOneWidget, reason: 'what was typed, to start from');
    await t.tap(find.bySemanticsLabel('Email me a link'));
    await settle(t);
    expect(server.calls.last.body, {'email': 'ada@adaskitchen.ng'});
    expect(find.text('Check your email'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Open your email app'));
    await settle(t);
    expect(mailOpened, 1);
    await t.tap(find.bySemanticsLabel('Send it again'));
    await settle(t);
    expect(server.calls.where((c) => c.path == '/v1/auth/password/forgot'), hasLength(2));
    expect(find.text('Sent again. The newest link is the one that works.'), findsOneWidget);

    // The email's link, opened on this phone.
    arriving.add(Uri.parse('$taskiemUrl/reset-password#token=AbC-_1234.xyZ_-9876'));
    await settle(t);
    expect(find.text('Choose a new password'), findsOneWidget);
    await t.enterText(find.byType(EditableText).at(0), 'too short');
    await t.enterText(find.byType(EditableText).at(1), 'too short');
    await t.tap(find.bySemanticsLabel('Save the new password'));
    await settle(t);
    expect(find.text('At least 12 characters.'), findsOneWidget);
    expect(server.calls.where((c) => c.path == '/v1/auth/password/reset'), isEmpty);
    await t.enterText(find.byType(EditableText).at(0), 'jollof on a friday');
    await t.enterText(find.byType(EditableText).at(1), 'jollof on a friday');
    await t.tap(find.bySemanticsLabel('Save the new password'));
    await settle(t);
    expect(server.calls.last.body, {'token': 'AbC-_1234.xyZ_-9876', 'password': 'jollof on a friday'});
    expect(find.text('Password saved'), findsOneWidget);
    await t.enterText(find.byType(EditableText).at(0), 'ada@adaskitchen.ng');
    await t.tap(find.bySemanticsLabel('Sign in'));
    await settle(t);
    final login = server.calls.lastWhere((c) => c.path == '/v1/auth/login').body!;
    expect((login['email'], login['password']), ('ada@adaskitchen.ng', 'jollof on a friday'));
    expect(find.text('invalid email or password'), findsOneWidget, reason: "the server's answer, said");
    inbox.dispose();
  });

  testWidgets('a link that no longer works says so, from the server', (t) async {
    final server = FakeServer();
    server.answers['POST /v1/auth/password/reset'] = () =>
        _json({'error': 'this reset link is not valid: it may have expired or been used. Ask for a new one.'}, 400);
    final auth = Auth(
      transport: Transport(base: 'http://localhost', client: server.client()),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: ResetPasswordScreen(auth: auth, token: 'x.y', onDone: () {})));
    await t.enterText(find.byType(EditableText).at(0), 'jollof on a friday');
    await t.enterText(find.byType(EditableText).at(1), 'jollof on a friday');
    await t.tap(find.bySemanticsLabel('Save the new password'));
    await settle(t);
    expect(find.textContaining('this reset link is not valid'), findsOneWidget);
  });
}
