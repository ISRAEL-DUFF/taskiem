// A way to sign in on a new phone (docs/spec/taskiem-for-everyone.md §5.4;
// screens/sign_in_way.dart): a person in a business with no passkey,
// email or password (the PIN is none) is asked to add one, as a step that
// cannot be skipped before the business opens for an account new to the
// apps, else as a reminder on Today and Settings until they do.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/kit/mobile.dart' show TkRoute;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/sign_in_way.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart' show loadFonts, snap;

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> businessMe({bool needs = true, bool step = false}) => {
  'tenant_id': 'b1',
  'tenant_name': 'Mama’s Kitchen',
  'personal': false,
  'roles': ['owner'],
  'permissions': <String>[],
  'auth_method': 'chat',
  'factors': {'passkey': false, 'totp': false, 'password': false},
  'user': {
    'id': 'u1',
    'name': 'Tolu Ade',
    'email': 'person+x@phone.taskiem.invalid',
    'email_placeholder': true,
    'phone': '+2348031230000',
    'needs_sign_in_way': needs,
    'sign_in_way_step': step,
  },
};

Auth _auth(FakeServer server) => Auth(
  transport: Transport(base: 'http://localhost', client: server.client()),
  store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Tolu’s Tecno', platform: 'android')),
);

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 500));
}

Future<void> press(WidgetTester t, String words) async {
  final f = find.text(words).last;
  await t.ensureVisible(f);
  await t.pump();
  await t.tap(f);
  await settle(t);
}

Future<void> typeIn(WidgetTester t, Key key, String text) async {
  final box = find.descendant(of: find.byKey(key), matching: find.byType(EditableText));
  await t.ensureVisible(box.first);
  await t.enterText(box.first, text);
  await t.pump();
}

void main() {
  setUpAll(loadFonts);

  void phone(WidgetTester t) {
    t.view.physicalSize = const Size(360, 900);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  for (final mode in TkMode.values) {
    testWidgets('new to the app with no way: a step before the business, with no way past it but adding one, ${mode.name}', (t) async {
      phone(t);
      final was = passkeys;
      passkeys = FakePasskeys();
      addTearDown(() => passkeys = was);
      final server = FakeServer();
      var added = false;
      server.answers['GET /v1/me'] = () => _json(businessMe(needs: !added, step: !added));
      server.answers['POST /v1/me/password'] = () {
        added = true;
        return http.Response('', 204);
      };
      final auth = _auth(server);
      auth.transport.token = 'tok-b';
      auth.me = Me.fromJson(businessMe(step: true));
      auth.state = AuthState.signedIn;
      await t.pumpWidget(TaskiemApp(mode: mode, auth: auth));
      await settle(t);

      expect(find.byType(SignInWayScreen), findsOneWidget);
      expect(find.text(signInWayTitle), findsOneWidget);
      expect(find.text('Before Mama’s Kitchen opens'), findsOneWidget);
      expect(find.text(signInWayWhy), findsOneWidget);
      for (final way in ['Add an email', 'Set a password', 'Add a passkey']) {
        expect(find.text(way), findsOneWidget, reason: way);
      }
      expect(find.textContaining('PIN'), findsNothing, reason: 'the PIN is no way to sign in');
      expect(find.text('Not now'), findsNothing, reason: 'it cannot be skipped');
      expect(find.text('Sign out'), findsOneWidget);
      await snap(t, 'sign-in-way-step-${mode.name}');

      await press(t, 'Set a password');
      await typeIn(t, const ValueKey('way-password'), 'short');
      await typeIn(t, const ValueKey('way-again'), 'short');
      await press(t, 'Set password');
      expect(find.text('At least 12 characters.'), findsOneWidget);
      expect(server.calls.where((c) => c.path == '/v1/me/password'), isEmpty);
      await typeIn(t, const ValueKey('way-password'), 'a long new password');
      await typeIn(t, const ValueKey('way-again'), 'a long new password');
      await snap(t, 'sign-in-way-password-${mode.name}');
      await press(t, 'Set password');
      expect(server.calls.lastWhere((c) => c.path == '/v1/me/password').body, {'new_password': 'a long new password'});
      expect(auth.me?.user?.signInWayStep, isFalse);
      expect(find.byType(SignInWayScreen), findsNothing, reason: 'the business opens');
      await t.pumpWidget(const SizedBox());
      await t.pump(const Duration(seconds: 5));
    });
  }

  for (final mode in TkMode.values) {
    testWidgets('in use before, with no way: the page to add one; an email, added by its link, ${mode.name}', (t) async {
      phone(t);
      final server = FakeServer();
      var confirmed = false;
      server.answers['GET /v1/me'] = () => _json(businessMe(needs: !confirmed));
      server.answers['POST /v1/me/email'] = () => _json({'status': 'A link is on its way.'}, 202);
      final auth = _auth(server);
      auth.transport.token = 'tok-b';
      auth.me = Me.fromJson(businessMe());
      auth.state = AuthState.signedIn;
      await t.pumpWidget(TaskiemApp(mode: mode, home: Builder(builder: (c) => GestureDetector(
        onTap: () => Navigator.of(c).push(TkRoute<void>(pageBuilder: (_, _, _) => SignInWayScreen(auth: auth, backLabel: 'Back to Today'))),
        child: const Text('open'),
      ))));
      await t.tap(find.text('open'));
      await settle(t);
      expect(find.byType(SignInWayScreen), findsOneWidget);
      expect(find.text('Sign out'), findsNothing, reason: 'a page with Back, not a step');

      await press(t, 'Add an email');
      await typeIn(t, const ValueKey('way-email'), 'tolu@example.com');
      await press(t, 'Send the link');
      expect(server.calls.lastWhere((c) => c.path == '/v1/me/email').body, {'email': 'tolu@example.com'});
      expect(find.text('We sent a link to tolu@example.com'), findsOneWidget);
      await snap(t, 'sign-in-way-sent-${mode.name}');

      // Not opened yet: said so.
      await press(t, 'I’ve opened the link');
      expect(find.text('The link has not been opened yet. Open it from the email, then try again.'), findsOneWidget);
      confirmed = true;
      await press(t, 'I’ve opened the link');
      expect(find.byType(SignInWayScreen), findsNothing, reason: 'done: back where it was');
      expect(needsSignInWay(auth), isFalse);
      await t.pump(const Duration(seconds: 5));
    });
  }
}
