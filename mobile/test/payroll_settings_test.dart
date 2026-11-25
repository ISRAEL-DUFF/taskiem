// Payroll settings (P4b-1): Payrolla connected in one step with who can
// approve and Link my Payrolla account by one code (a wrong code, then the
// right one; an expired code asking for a new one); a key connection's
// owner told to reconnect; an ended connection said, to the owner and to
// others; Payroll's own callouts for both. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/payroll_page.dart';
import 'package:taskiem/chat/payroll_settings.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

/// A stand-in server: answers by "METHOD /path", every request kept.
class _Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  List<Map<String, dynamic>> sent(String method, String path) => [
    for (final c in calls.where((c) => c.method == method && c.url.path == path))
      c.body.isEmpty ? <String, dynamic>{} : jsonDecode(c.body) as Map<String, dynamic>,
  ];

  Auth auth({bool owner = true}) {
    final a = Auth(
      transport: Transport(
        base: 'http://localhost',
        token: 'tok',
        client: MockClient((req) async {
          calls.add(req);
          final f = routes['${req.method} ${req.url.path}'];
          if (f == null) return http.Response(jsonEncode({'error': 'not here'}), 404, headers: _json);
          final out = f(req);
          if (out is http.Response) return out;
          return http.Response(jsonEncode(out ?? {}), 200, headers: _json);
        }),
      ),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': 't1',
      'tenant_name': 'Amara Foods',
      'personal': false,
      'roles': [if (owner) 'owner' else 'payroll'],
      'permissions': ['payroll.read', 'payroll.approve'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ngozi Eze', 'email': 'ngozi@amara.test', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

http.Response _err(int status, String code, String words) => http.Response(jsonEncode({'error': words, 'code': code}), status, headers: _json);

const _ngozi = 'a0000000-0000-4000-8000-000000000001';
const _musa = 'a0000000-0000-4000-8000-000000000002';

Map<String, dynamic> _conn(String auth, {String status = 'active', bool owner = true}) => {
  'connected': status == 'active',
  'ended': ?(status == 'expired' ? true : null),
  'is_owner': owner,
  'may_link': true,
  'modes': ['test'],
  'connection': {
    'id': '0192e2e0-0000-7000-8000-0000000c0001',
    'name': 'payrolla',
    'status': status,
    'auth': auth,
    'test': true,
    'company': {'id': 'c1', 'name': 'Amara Foods Ltd'},
    'scopes': auth == 'oauth' ? ['payroll:read', 'payroll:approve'] : ['payroll:read'],
  },
};

Map<String, dynamic> _approvers({required bool linked}) {
  final ngozi = {
    'user_id': _ngozi,
    'name': 'Ngozi Eze',
    'email': 'ngozi@amara.test',
    'role': 'OWNER',
    'can_approve': true,
    'can_pay': true,
    'can_confirm': true,
    'linked': linked ? {'user_id': 'u1', 'name': 'Ngozi Eze'} : null,
    'suggested': !linked,
    'me': linked,
  };
  return {
    'approvers': [
      {'user_id': _musa, 'name': 'Musa Bello', 'email': 'musa@amara.test', 'role': 'APPROVER', 'can_approve': true, 'can_pay': false, 'can_confirm': false, 'linked': null, 'suggested': false, 'me': false},
      ngozi,
    ],
    'me': linked ? ngozi : null,
    'may_link': true,
    'is_owner': true,
  };
}

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 600));
  }

  void phone(WidgetTester t, [double tall = 800]) {
    t.view.physicalSize = Size(360, tall);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  testWidgets('connected in one step: who can approve, the same email first; Link my Payrolla account with one code', (t) async {
    phone(t, 900);
    final api = _Api();
    var linked = false;
    var expireNext = false;
    api.routes['GET /v1/payroll/connection'] = (_) => _conn('oauth');
    api.routes['GET /v1/payroll/approvers'] = (_) => _approvers(linked: linked);
    api.routes['POST /v1/payroll/approvers/$_ngozi/link'] = (_) => http.Response(
      jsonEncode({
        'challenge': {'challenge_id': 'ch1', 'channel': 'SMS', 'destination_hint': '•••• 4567', 'expires_at': '2026-11-24T10:05:00Z'},
        'sent_to': 'Payrolla sent a code to your phone ending 4567.',
        'safer': true,
      }),
      202,
      headers: _json,
    );
    api.routes['POST /v1/payroll/approvers/$_ngozi/link/confirm'] = (r) {
      final code = (jsonDecode(r.body) as Map)['code'];
      if (expireNext) {
        expireNext = false;
        return _err(410, 'code_expired', 'That code has expired.');
      }
      if (code != '482910') return _err(422, 'code_invalid', 'That code isn’t right. Check it and type it again.');
      linked = true;
      return {
        'approver': {'user_id': _ngozi, 'name': 'Ngozi Eze', 'role': 'OWNER', 'can_approve': true, 'can_pay': true},
      };
    };
    api.routes['DELETE /v1/payroll/approvers/$_ngozi/link'] = (_) {
      linked = false;
      return {'unlinked': true};
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollSettingsPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Payroll settings'), findsWidgets);
    expect(find.text('Amara Foods Ltd'), findsOneWidget);
    expect(find.text('Connected · Test, no real money'), findsOneWidget);
    expect(find.text('Owner · Approves and pays · Looks like you (same email)'), findsOneWidget);
    expect(find.text('Approver · Approves · No phone for codes yet · Not linked'), findsOneWidget);
    expect(find.text('Link'), findsOneWidget); // Musa has no phone for codes
    await looks(t, 'payroll-settings');
    await snap(t, 'payroll-settings');

    await t.tap(find.text('Link'));
    await settle(t);
    expect(find.text('Link your Payrolla account'), findsOneWidget);
    await t.tap(find.text('Send me a code'));
    await settle(t);
    expect(find.text('Payrolla sent a code to your phone ending 4567.'), findsOneWidget);
    expect(find.text('A code from an authenticator app is safer: set one up in Payrolla.'), findsOneWidget);
    await snap(t, 'payroll-link-sheet');
    await t.enterText(find.byType(EditableText).last, '000000');
    await t.pump();
    await t.tap(find.text('Link my account'));
    await settle(t);
    expect(find.text('That code isn’t right. Check it and type it again.'), findsOneWidget);
    expireNext = true;
    await t.enterText(find.byType(EditableText).last, '482910');
    await t.pump();
    await t.tap(find.text('Link my account'));
    await settle(t);
    expect(find.text('That code has expired.'), findsOneWidget);
    await t.tap(find.text('Send a new code'));
    await settle(t);
    await t.enterText(find.byType(EditableText).last, '482910');
    await t.pump();
    await t.tap(find.text('Link my account'));
    await settle(t);
    expect(find.text('Link your Payrolla account'), findsNothing);
    expect(find.text('Owner · Approves and pays · Linked to you'), findsOneWidget);
    final confirms = api.sent('POST', '/v1/payroll/approvers/$_ngozi/link/confirm');
    expect(confirms.length, 3);
    expect(confirms.map((c) => c['key']).toSet().length, 3, reason: 'each try at a code is its own submission');
    expect(api.sent('POST', '/v1/payroll/approvers/$_ngozi/link').map((c) => c['key']).toSet().length, 2, reason: 'a new code is a new submission');

    await t.tap(find.text('Unlink'));
    await settle(t);
    await t.tap(find.text('Unlink').last);
    await settle(t);
    expect(api.calls.where((c) => c.method == 'DELETE').single.url.queryParameters['key'], isNotEmpty);
    await t.pump(const Duration(seconds: 6)); // the toast goes
  });

  testWidgets('a key connection: Reconnect Payrolla for the owner; an ended one said to the owner and to others', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payroll/connection'] = (_) => _conn('key');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollSettingsPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Connected with a key'), findsOneWidget);
    expect(find.text('Reconnect Payrolla'), findsWidgets);
    expect(find.text('Who can approve in Payrolla'), findsNothing);
    await snap(t, 'payroll-settings-key');

    api.routes['GET /v1/payroll/connection'] = (_) => _conn('oauth', status: 'expired');
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PayrollSettingsPage(key: UniqueKey(), auth: api.auth())));
    await settle(t);
    expect(find.text('Payrolla is no longer connected'), findsOneWidget);
    expect(find.text('Reconnect Payrolla'), findsOneWidget);
    await snap(t, 'payroll-settings-ended');

    api.routes['GET /v1/payroll/connection'] = (_) => _conn('oauth', status: 'expired', owner: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollSettingsPage(key: UniqueKey(), auth: api.auth(owner: false))));
    await settle(t);
    expect(find.text('Payroll isn’t connected right now'), findsOneWidget);
    expect(find.text('The owner has been told.'), findsOneWidget);
    expect(find.text('Reconnect Payrolla'), findsNothing);
  });

  testWidgets('Payroll: an ended connection and a key connection tell the owner to reconnect; Payroll settings as a row', (t) async {
    phone(t, 900);
    final api = _Api();
    api.routes['GET /v1/payroll/connection'] = (_) => _conn('key');
    api.routes['GET /v1/payroll'] = (_) => {'connected': false, 'ended': true, 'is_owner': true, 'runs': <Object>[]};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Payrolla is no longer connected'), findsOneWidget);
    expect(find.text('Reconnect Payrolla'), findsOneWidget);

    api.routes['GET /v1/payroll'] = (_) => {
      'connected': true,
      'connection': 'payrolla',
      'auth': 'key',
      'is_owner': true,
      'company': {'name': 'Amara Foods Ltd', 'pay_frequency': 'MONTHLY'},
      'runs': <Object>[],
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollPage(key: UniqueKey(), auth: api.auth())));
    await settle(t);
    expect(find.text('Connect again in one step so you can run payroll here.'), findsOneWidget);
    await t.scrollUntilVisible(find.text('Payroll settings'), 200);
    await t.tap(find.text('Payroll settings'));
    await settle(t);
    expect(find.text('Connected with a key'), findsOneWidget);
  });

  testWidgets('large text and what can be pressed: Payroll settings', (t) async {
    final api = _Api();
    api.routes['GET /v1/payroll/connection'] = (_) => _conn('oauth');
    api.routes['GET /v1/payroll/approvers'] = (_) => _approvers(linked: false);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollSettingsPage(auth: api.auth())), drive: settle);
  });
}
