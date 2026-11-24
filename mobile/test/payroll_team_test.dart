// Staff and pay from Taskiem (P4b-2): Team with pay, Add someone from the
// staff with their account checked, Change pay from a date (never the
// phone), a new salary account held for another approver, End employment
// (today, asked first); Account changes approved with a code (a wrong
// code, then the right one) and rejected with a note; My pay with the
// code Payrolla texts the person. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/payroll_team.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

class _Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  List<Map<String, dynamic>> sent(String method, String path) => [
    for (final c in calls.where((c) => c.method == method && c.url.path == path))
      c.body.isEmpty ? <String, dynamic>{} : jsonDecode(c.body) as Map<String, dynamic>,
  ];

  Auth auth() {
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
      'roles': ['owner'],
      'permissions': ['payroll.read', 'payroll.approve', 'payroll.prepare'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ngozi Eze', 'email': 'ngozi@amara.test', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

http.Response _err(int status, String code, String words) => http.Response(jsonEncode({'error': words, 'code': code}), status, headers: _json);
http.Response _status(int status, Object body) => http.Response(jsonEncode(body), status, headers: _json);

const _zainab = 'e0000000-0000-4000-8000-000000000001';
const _may = {'prepare': true, 'approve': true, 'see_pay': true};

Map<String, dynamic> _change(String id, String via, {String? asked, bool mine = false}) => {
  'id': id,
  'employee_id': _zainab,
  'employee': 'Zainab Bello',
  'bank': 'Access Bank',
  'account_name': 'ZAINAB A. BELLO',
  'last4': '2222',
  'via': via,
  'asked_by': ?asked,
  'mine': mine,
  'requested_at': '2026-11-24T09:00:00Z',
};

Map<String, dynamic> _member({Object? waiting}) => {
  'id': _zainab,
  'name': 'Zainab Bello',
  'first_name': 'Zainab',
  'last_name': 'Bello',
  'phone': '+2348011112201',
  'department': 'Operations',
  'status': 'active',
  'bank': {'bank': 'Guaranty Trust Bank', 'bank_code': '058', 'account_name': 'ZAINAB A. BELLO', 'last4': '6789'},
  'pay': {'gross_kobo': 45000000, 'basic_kobo': 0, 'housing_kobo': 0, 'transport_kobo': 0, 'pensionable': true, 'from': '2026-01-01'},
  'waiting_change': waiting,
  'staff': {'user_id': 'u7', 'name': 'Zainab Bello'},
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 600));
  }

  void phone(WidgetTester t, [double tall = 900]) {
    t.view.physicalSize = Size(360, tall);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  _Api teamApi() {
    final api = _Api();
    Object? waiting;
    final added = <Map<String, dynamic>>[];
    api.routes['GET /v1/payroll/banks'] = (_) => {
      'banks': [
        {'code': '044', 'name': 'Access Bank'},
        {'code': '057', 'name': 'Zenith Bank'},
        {'code': '058', 'name': 'Guaranty Trust Bank'},
      ],
    };
    api.routes['GET /v1/payroll/team'] = (_) => {
      'team': [_member(waiting: waiting), ...added],
      'staff': [
        {'user_id': 'u9', 'name': 'Musa Ali', 'phone': '+2348022223333'},
      ],
      'may': _may,
      'connection': 'payrolla',
    };
    api.routes['POST /v1/payroll/name-check'] = (_) => {'checked': true, 'account_name': 'MUSA I. ALI', 'bank': 'Zenith Bank', 'last4': '3333'};
    api.routes['POST /v1/payroll/team'] = (_) {
      final m = {
        ..._member(),
        'id': 'e2',
        'name': 'Musa Ali',
        'first_name': 'Musa',
        'last_name': 'Ali',
        'department': null,
        'bank': {'bank': 'Zenith Bank', 'bank_code': '057', 'account_name': 'MUSA I. ALI', 'last4': '3333'},
      };
      added.add(m);
      return _status(201, {'employee': m});
    };
    api.routes['PATCH /v1/payroll/team/$_zainab'] = (_) => {'employee': _member(), 'words': 'Their new pay starts 1 January 2027.'};
    api.routes['POST /v1/payroll/team/$_zainab/bank'] = (_) {
      waiting = _change('c3', 'in_taskiem', asked: 'Ngozi', mine: true);
      return {'employee': _member(waiting: waiting), 'words': 'Held until another approver approves it. They’re paid into ••6789 until then.'};
    };
    api.routes['POST /v1/payroll/team/$_zainab/end'] = (_) => {'employee': {..._member(), 'status': 'left'}, 'words': 'Zainab Bello’s employment ended today.'};
    return api;
  }

  testWidgets('Team: pay shown; add Musa from the staff with his account checked; a new account held; ending asks first', (t) async {
    phone(t);
    final api = teamApi();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollTeamPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Zainab Bello'), findsOneWidget);
    expect(find.text('Operations · Guaranty Trust Bank ••6789'), findsOneWidget);
    expect(find.text('₦450,000'), findsOneWidget);
    expect(find.text('Musa Ali'), findsOneWidget);
    await looks(t, 'payroll-team');
    await snap(t, 'payroll-team');

    // Add Musa: filled in from the staff; the bank's name checked first.
    await t.tap(find.text('Musa Ali'));
    await settle(t);
    expect(find.text('Add someone to payroll'), findsOneWidget);
    await t.enterText(find.byKey(const ValueKey('payroll-gross')), '300,000');
    await t.ensureVisible(find.text('Choose the bank'));
    await t.pump();
    await t.tap(find.text('Choose the bank'));
    await settle(t);
    await t.tap(find.text('Zenith Bank'));
    await settle(t);
    await t.enterText(find.byKey(const ValueKey('payroll-account')), '3333333333');
    await t.pump();
    await t.ensureVisible(find.text('Check the account'));
    await t.tap(find.text('Check the account'));
    await settle(t);
    expect(find.text('MUSA I. ALI'), findsOneWidget);
    await snap(t, 'payroll-add-sheet');
    await t.tap(find.text('Add to payroll').last);
    await settle(t);
    final body = api.sent('POST', '/v1/payroll/team').single;
    expect(body['first_name'], 'Musa');
    expect(body['last_name'], 'Ali');
    expect(body['staff_user'], 'u9');
    expect(body['pay'], containsPair('gross_kobo', 30000000));
    expect(body['bank'], {'bank_code': '057', 'account_number': '3333333333'});
    expect(find.text('Added to payroll'), findsOneWidget);
    await t.tap(find.text('Done'));
    await settle(t);

    // A new account for Zainab, held for another approver.
    await t.tap(find.text('Zainab Bello'));
    await settle(t);
    await t.tap(find.text('New salary account'));
    await settle(t);
    await t.ensureVisible(find.text('Choose the bank'));
    await t.pump();
    await t.tap(find.text('Choose the bank'));
    await settle(t);
    await t.tap(find.text('Access Bank'));
    await settle(t);
    await t.enterText(find.byKey(const ValueKey('payroll-account')), '2222222222');
    await t.pump();
    await t.tap(find.text('Ask for the change'));
    await settle(t);
    expect(find.text('Held until another approver approves it. They’re paid into ••6789 until then.'), findsOneWidget);
    expect(find.text('Change waiting'), findsOneWidget);

    // Ending asks first, and says it takes effect today.
    await t.tap(find.text('Zainab Bello'));
    await settle(t);
    await t.tap(find.text('End employment'));
    await settle(t);
    expect(find.text('This takes effect today. Payrolla can’t set a last day.'), findsOneWidget);
    await t.tap(find.text('Yes, end it today'));
    await settle(t);
    expect(api.sent('POST', '/v1/payroll/team/$_zainab/end').single['confirm'], 'yes');
    expect(find.text('Zainab Bello’s employment ended today.'), findsOneWidget);
  });

  testWidgets('Team: change pay from a date; the phone is not changeable', (t) async {
    phone(t);
    final api = teamApi();
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PayrollTeamPage(auth: api.auth())));
    await settle(t);
    await t.tap(find.text('Zainab Bello'));
    await settle(t);
    await t.tap(find.text('Change details or pay'));
    await settle(t);
    expect(find.text('Phone: +2348011112201'), findsOneWidget);
    expect(find.text('A phone number is changed in Payrolla. Payrolla keeps no job title.'), findsOneWidget);
    await t.enterText(find.byKey(const ValueKey('payroll-change-gross')), '500000');
    await t.pump();
    await snap(t, 'payroll-change-sheet');
    await t.tap(find.text('Save'));
    await settle(t);
    final sent = api.sent('PATCH', '/v1/payroll/team/$_zainab').single;
    expect((sent['pay'] as Map)['gross_kobo'], 50000000);
    expect(sent.containsKey('phone'), isFalse);
    expect(find.text('Their new pay starts 1 January 2027.'), findsOneWidget);
  });

  testWidgets('Account changes: approve with a code (wrong, then right); reject with a note; one asked by me', (t) async {
    phone(t);
    final api = _Api();
    var changes = [_change('c1', 'in_taskiem', asked: 'Ada'), _change('c2', 'by_employee'), _change('c3', 'in_taskiem', asked: 'Ngozi', mine: true)];
    api.routes['GET /v1/payroll/bank-changes'] = (_) => {'changes': changes, 'may': _may};
    api.routes['POST /v1/payroll/bank-changes/c1/code'] = (_) => _status(202, {
      'challenge': {'challenge_id': 'ch1', 'channel': 'SMS', 'destination_hint': '•••• 4567', 'expires_at': '2026-11-24T10:05:00Z'},
      'sent_to': 'Payrolla sent a code to your phone ending 4567.',
      'safer': true,
    });
    api.routes['POST /v1/payroll/bank-changes/c1/approve'] = (r) {
      if ((jsonDecode(r.body) as Map)['code'] != '615201') return _err(422, 'code_invalid', 'That code isn’t right. Check it and type it again.');
      changes = changes.where((c) => c['id'] != 'c1').toList();
      return {'change': _change('c1', 'in_taskiem', asked: 'Ada'), 'words': 'Approved. Payrolla pays them into the new account from now on.'};
    };
    api.routes['POST /v1/payroll/bank-changes/c2/reject'] = (_) {
      changes = changes.where((c) => c['id'] != 'c2').toList();
      return {'change': _change('c2', 'by_employee'), 'words': 'Rejected. They’re still paid into their current account.'};
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollChangesPage(auth: api.auth())));
    await settle(t);
    expect(find.text('ZAINAB A. BELLO, Access Bank ••2222 · Asked by Ada in Taskiem'), findsOneWidget);
    expect(find.text('ZAINAB A. BELLO, Access Bank ••2222 · Asked by the employee'), findsOneWidget);
    expect(find.text('You asked'), findsOneWidget);
    await looks(t, 'payroll-account-changes');
    await snap(t, 'payroll-account-changes');

    await t.tap(find.text('ZAINAB A. BELLO, Access Bank ••2222 · Asked by Ada in Taskiem'));
    await settle(t);
    await t.tap(find.text('Approve with a code'));
    await settle(t);
    await t.tap(find.text('Send me a code'));
    await settle(t);
    expect(find.text('Payrolla sent a code to your phone ending 4567.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).last, '000000');
    await t.pump();
    await t.tap(find.text('Approve'));
    await settle(t);
    expect(find.text('That code isn’t right. Check it and type it again.'), findsOneWidget);
    await snap(t, 'payroll-approve-sheet');
    await t.enterText(find.byType(EditableText).last, '615201');
    await t.pump();
    await t.tap(find.text('Approve'));
    await settle(t);
    expect(find.text('Approved. Payrolla pays them into the new account from now on.'), findsOneWidget);
    final tries = api.sent('POST', '/v1/payroll/bank-changes/c1/approve');
    expect(tries.map((c) => c['key']).toSet().length, 2, reason: 'each try at a code is its own submission');

    // The one I asked for: only Reject is offered.
    await t.tap(find.text('ZAINAB A. BELLO, Access Bank ••2222 · Asked by Ngozi in Taskiem'));
    await settle(t);
    expect(find.text('You asked for this, so someone else must approve it.'), findsOneWidget);
    expect(find.text('Approve with a code'), findsNothing);
    await t.tapAt(const Offset(10, 10));
    await settle(t);

    await t.tap(find.text('ZAINAB A. BELLO, Access Bank ••2222 · Asked by the employee'));
    await settle(t);
    await t.tap(find.text('Reject'));
    await settle(t);
    await t.enterText(find.byKey(const ValueKey('payroll-reject-note')), 'Call Zainab first');
    await t.pump();
    await t.tap(find.text('Reject').last);
    await settle(t);
    expect(api.sent('POST', '/v1/payroll/bank-changes/c2/reject').single['note'], 'Call Zainab first');
    expect(find.text('Rejected. They’re still paid into their current account.'), findsOneWidget);
  });

  testWidgets('My pay: my account and phone; a new account named before the code; held for my employer', (t) async {
    phone(t);
    final api = _Api();
    Object? waiting;
    api.routes['GET /v1/payroll/banks'] = (_) => {
      'banks': [
        {'code': '044', 'name': 'Access Bank'},
      ],
    };
    api.routes['GET /v1/payroll/me'] = (_) => {
      'linked': true,
      'me': {
        'name': 'Zainab Bello',
        'phone': '+2348011117801',
        'status': 'active',
        'bank': {'bank': 'Guaranty Trust Bank', 'bank_code': '058', 'account_name': 'ZAINAB A. BELLO', 'last4': '6789'},
        'waiting_change': waiting,
      },
    };
    api.routes['POST /v1/payroll/me/bank-change'] = (_) => _status(202, {
      'challenge': {'challenge_id': 'ch9', 'channel': 'SMS', 'destination_hint': '•••• 7801', 'expires_at': '2026-11-24T10:10:00Z'},
      'account_name': 'ZAINAB A. BELLO',
      'last4': '2222',
      'bank': 'Access Bank',
      'sent_to': 'Payrolla sent a code to your phone ending 7801.',
    });
    api.routes['POST /v1/payroll/me/bank-change/confirm'] = (r) {
      if ((jsonDecode(r.body) as Map)['code'] == '000000') return _err(410, 'code_expired', 'That code has expired.');
      waiting = _change('c4', 'by_employee');
      return {'change': waiting, 'words': 'Held for your employer to approve. You’re paid into ••6789 until then.'};
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MyPayPage(auth: api.auth())));
    await settle(t);
    expect(find.text('ZAINAB A. BELLO'), findsOneWidget);
    expect(find.text('+2348011117801 · your employer changes it in Payrolla'), findsOneWidget);
    await looks(t, 'payroll-my-pay');
    await snap(t, 'payroll-my-pay');

    await t.tap(find.text('Change my salary account'));
    await settle(t);
    await t.ensureVisible(find.text('Choose the bank'));
    await t.pump();
    await t.tap(find.text('Choose the bank'));
    await settle(t);
    await t.tap(find.text('Access Bank'));
    await settle(t);
    await t.enterText(find.byKey(const ValueKey('payroll-account')), '2222222222');
    await t.pump();
    await t.tap(find.text('Continue'));
    await settle(t);
    expect(find.text('ZAINAB A. BELLO ••2222'), findsOneWidget);
    expect(find.text('Payrolla sent a code to your phone ending 7801.'), findsOneWidget);
    await snap(t, 'payroll-my-pay-code');
    // An expired code offers a new one.
    await t.enterText(find.byType(EditableText).last, '000000');
    await t.pump();
    await t.tap(find.text('Yes, it’s mine'));
    await settle(t);
    expect(find.text('That code has expired.'), findsOneWidget);
    await t.tap(find.text('Send a new code'));
    await settle(t);
    await t.enterText(find.byType(EditableText).last, '731501');
    await t.pump();
    await t.tap(find.text('Yes, it’s mine'));
    await settle(t);
    expect(find.text('Held for your employer to approve. You’re paid into ••6789 until then.'), findsOneWidget);
    expect(find.text('Waiting for your employer'), findsOneWidget);
    expect(api.sent('POST', '/v1/payroll/me/bank-change').length, 2);
  });

  testWidgets('large text and what can be pressed: Team', (t) async {
    final api = teamApi();
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollTeamPage(auth: api.auth())), drive: settle);
  });
}
