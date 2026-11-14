// M4's screens as people use them (docs/spec/mobile-app-m4.md N4, N6, N7):
// the Passkeys card, the approve sheet with the phone's passkey, and each
// new screen at twice the text on a small phone, nothing cut off.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/notify.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/approve.dart';
import 'package:taskiem/kit/modal.dart' show showTkConfirm;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/app_sign_up.dart';
import 'package:taskiem/screens/signed_in.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'guidelines.dart';
import 'notify_test.dart' show PushServer, signedIn;
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart' show loadFonts;

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

/// Signed in with factors (what proves it is them), every request kept.
Auth _auth(FakeServer server, Map<String, bool> factors) {
  final me = {'tenant_id': 't1', 'tenant_name': 'Ada Foods', 'auth_method': 'password', 'permissions': <String>[], 'factors': factors};
  server.answers['GET /v1/me'] = () => _json(me);
  final a = Auth(
    transport: Transport(base: 'http://localhost', token: 'tok', client: server.client()),
    store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Ada’s Tecno', platform: 'android')),
  );
  a.state = AuthState.signedIn;
  a.me = Me.fromJson(me);
  return a;
}

class _NoPasskeys extends FakePasskeys {
  @override
  Future<bool> available() async => false;
}

ChatAlert _approval({String? stepUp}) => ChatAlert.fromJson({
  'key': 'approval:r1/pay', 'kind': 'approval', 'at': '2026-11-14T10:00:00Z', 'title': 'Pay Bayo Rice Mills ₦450,000', 'line': 'Asked by Tunde', 'id': 'r1',
  'who': 'Tunde (cashier)',
  'approval': {
    'run_id': 'r1', 'step_id': 'pay', 'step_name': 'Pay', 'workflow': 'Pay a supplier', 'environment': 'prod', 'required': 1, 'approvals': 0,
    'level': 1, 'levels': 1, 'sole_approver': true, 'requested_at': '2026-11-14T10:00:00Z', 'step_up': ?stepUp,
    'subject': {'account_name': 'Bayo Rice Mills', 'account_number': '0123456789', 'amount_kobo': 45000000, 'bank_code': '058'},
  },
});

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
  }

  testWidgets('Passkeys: listed, and one added on this phone after the code', (t) async {
    t.view.physicalSize = const Size(390, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final phone = FakePasskeys();
    passkeys = phone;
    final server = FakeServer();
    final list = <Map<String, Object?>>[{'id': 'k1', 'name': 'MacBook', 'synced': true, 'created_at': '2026-11-01T10:00:00Z', 'last_used_at': null}];
    server.answers['GET /v1/me/passkeys'] = () => _json({'passkeys': list});
    server.answers['POST /v1/me/passkeys/options'] = () => _json({
      'publicKey': {'challenge': 'c', 'rp': {'id': 'taskiem.example', 'name': 'Taskiem'}, 'user': {'id': 'dQ', 'name': 'ada', 'displayName': 'Ada'}},
    });
    server.answers['POST /v1/me/passkeys'] = () {
      list.add({'id': 'new', 'name': 'Ada’s Tecno', 'synced': true, 'created_at': '2026-11-14T10:00:00Z', 'last_used_at': null});
      return _json(list.last, 201);
    };
    final auth = _auth(server, {'totp': true, 'password': true});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ListView(children: [PasskeysCard(auth: auth)])));
    await settle(t);
    expect(find.text('MacBook · synced'), findsOneWidget);
    await t.enterText(find.byType(EditableText), '123456');
    await t.tap(find.text('Add a passkey on this phone'));
    await settle(t);
    expect(server.calls.firstWhere((c) => c.path == '/v1/me/passkeys/options').body, {'totp': '123456'});
    expect(find.text('Ada’s Tecno · synced'), findsOneWidget);
    expect(phone.asked, hasLength(1));
    await t.pump(const Duration(seconds: 5)); // the toast goes
  });

  testWidgets('Passkeys on a phone that cannot use them: said ahead, nothing to press', (t) async {
    passkeys = _NoPasskeys();
    final server = FakeServer();
    server.answers['GET /v1/me/passkeys'] = () => _json({'passkeys': <Object>[]});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ListView(children: [PasskeysCard(auth: _auth(server, {'password': true}))])));
    await settle(t);
    expect(find.textContaining('This phone cannot use passkeys'), findsOneWidget);
    expect(find.text('Add a passkey on this phone'), findsNothing);
  });

  testWidgets('an approval that asks for confirmation: the phone’s passkey, or the code where it has none', (t) async {
    t.view.physicalSize = const Size(390, 900);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final phone = FakePasskeys();
    passkeys = phone;
    final server = FakeServer();
    server.answers['POST /v1/me/step-up/options'] = () => _json({'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.example', 'userVerification': 'required', 'timeout': 60000}});
    server.answers['POST /v1/approvals/r1/pay'] = () => _json({'status': 'approved'});
    final auth = _auth(server, {'passkey': true});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ApproveSheet(a: _approval(stepUp: 'passkey_or_totp'), decision: 'approved', auth: auth)));
    await settle(t);
    expect(find.textContaining('Your phone asks for your passkey'), findsOneWidget);
    await t.tap(find.text('Approve').last);
    await settle(t);
    final decided = server.calls.firstWhere((c) => c.path == '/v1/approvals/r1/pay');
    expect((decided.body!['passkey'] as Map)['id'], 'k1');
    await t.pump(const Duration(seconds: 2));

    // No passkey on this phone: the code is what it asks for.
    await t.pumpWidget(const SizedBox());
    await t.pump();
    passkeys = _NoPasskeys();
    server.calls.clear();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ApproveSheet(key: const ValueKey('again'), a: _approval(stepUp: 'passkey_or_totp'), decision: 'approved', auth: auth)));
    await settle(t);
    await t.tap(find.text('Approve').last);
    await settle(t);
    expect(find.textContaining('Type a code from your authenticator app'), findsOneWidget);
    expect(server.calls.where((c) => c.path == '/v1/approvals/r1/pay'), isEmpty);
  });

  group('at twice the text on a small phone', () {
    testWidgets('Notifications on this phone, on, with its kinds', (t) async {
      late PhoneNotifications n;
      await t.runAsync(() async {
        final (_, nn, _, _) = await signedIn(PushServer());
        n = nn;
        await n.start();
        await n.turnOn();
      });
      await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: ListView(children: [NotificationsCard(notify: n)])));
    });

    testWidgets('Passkeys', (t) async {
      passkeys = FakePasskeys();
      final server = FakeServer();
      server.answers['GET /v1/me/passkeys'] = () => _json({'passkeys': [{'id': 'k1', 'name': 'MacBook', 'synced': true, 'created_at': '2026-11-01T10:00:00Z', 'last_used_at': null}]});
      await expectLargeText(
        t,
        TaskiemApp(mode: TkMode.dark, home: ListView(children: [PasskeysCard(auth: _auth(server, {'totp': true}))])),
        drive: settle,
      );
    });

    testWidgets('the approve sheet with a passkey', (t) async {
      passkeys = FakePasskeys();
      await expectLargeText(
        t,
        TaskiemApp(mode: TkMode.light, home: ApproveSheet(a: _approval(stepUp: 'passkey_or_totp'), decision: 'approved', auth: _auth(FakeServer(), {'passkey': true}))),
      );
    });

    testWidgets('the question asked once', (t) async {
      await expectLargeText(
        t,
        TaskiemApp(
          mode: TkMode.light,
          home: Builder(
            builder: (context) => TkAskOnce(onBuilt: () => showTkConfirm(
              context,
              title: 'Get a notification when a business or customer writes?',
              body: 'Taskiem tells you on this phone, even when the app is closed. You can change this in Settings.',
              ok: 'Yes',
              cancel: 'Not now',
            )),
          ),
        ),
        drive: (t) async => t.pump(const Duration(milliseconds: 400)),
      );
      expect(find.text('Not now'), findsOneWidget);
    });

    testWidgets('Get started, the number', (t) async {
      await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: AppSignUpScreen(auth: _auth(FakeServer(), {}))));
    });
  });
}

/// Shows what [onBuilt] opens once it is on the screen.
class TkAskOnce extends StatefulWidget {
  const TkAskOnce({super.key, required this.onBuilt});
  final void Function() onBuilt;
  @override
  State<TkAskOnce> createState() => _TkAskOnceState();
}

class _TkAskOnceState extends State<TkAskOnce> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => widget.onBuilt());
  }

  @override
  Widget build(BuildContext context) => const SizedBox.expand();
}
