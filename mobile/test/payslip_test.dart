// After locking (P4b-4): a locked payroll's Payment (waiting for money,
// paid with one not paid), Pay the failed ones again and Pay now with
// Payrolla's code; a payslip opened from the run, saved as PDF; an
// employee's own payslips on My pay; the payslip's card in their chat.
// Pictured light and dark.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/attachments.dart' as attachments;
import 'package:taskiem/chat/payroll_run.dart';
import 'package:taskiem/chat/payroll_team.dart' show MyPayPage;
import 'package:taskiem/chat/payslip.dart';
import 'package:taskiem/kit/page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

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

  Auth auth({List<String> permissions = const ['payroll.read', 'payroll.prepare', 'payroll.approve']}) {
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
      'permissions': permissions,
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ngozi Eze', 'email': 'ngozi@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

http.Response _err(int status, String code, String words) => http.Response(jsonEncode({'error': words, 'code': code}), status, headers: _json);

const _run = '0192e2e0-0000-7000-8000-00000000d401';
const _slipZ = '0192e2e0-0000-7000-8000-00000000f401';
const _slipM = '0192e2e0-0000-7000-8000-00000000f402';

final _zItems = [
  {'kind': 'earning', 'code': 'BASIC', 'label': 'Basic', 'amount_kobo': 27000000},
  {'kind': 'earning', 'code': 'HOUSING', 'label': 'Housing', 'amount_kobo': 11250000},
  {'kind': 'earning', 'code': 'TRANSPORT', 'label': 'Transport', 'amount_kobo': 6750000},
  {'kind': 'deduction', 'code': 'PAYE', 'label': 'Tax (PAYE)', 'amount_kobo': 3150000},
  {'kind': 'deduction', 'code': 'PENSION_EE', 'label': 'Pension (yours)', 'amount_kobo': 3600000},
  {'kind': 'employer', 'code': 'PENSION_ER', 'label': 'Pension (employer’s)', 'amount_kobo': 4500000},
];

final _payslip = {
  'payslip_id': _slipZ,
  'run_id': _run,
  'period': 'November 2026',
  'month': 'November',
  'period_start': '2026-11-01',
  'pay_date': '2026-11-28',
  'employee': {'id': 'e1', 'name': 'Zainab Bello', 'department': 'Sales'},
  'employer': {'name': 'Amara Foods Ltd', 'rc_number': 'RC1234567'},
  'gross_kobo': 45000000,
  'total_earnings_kobo': 45000000,
  'total_deductions_kobo': 6750000,
  'net_kobo': 38250000,
  'items': _zItems,
  'payment': {'status': 'CONFIRMED', 'state': 'paid', 'words': 'Paid'},
};

class _State {
  String stage = 'some'; // scheduled, short, some, paid
}

Map<String, dynamic> _summary(_State s) => {
  'run_id': _run,
  'period_start': '2026-11-01',
  'period_end': '2026-11-30',
  'pay_date': '2026-11-28',
  'run_type': 'REGULAR',
  'status': s.stage == 'some' ? 'PARTIALLY_COMPLETED' : s.stage == 'paid' ? 'COMPLETED' : 'LOCKED',
  'employee_count': 2,
  'total_gross_kobo': 75000000,
  'total_net_kobo': 64000000,
  'awaiting_approval': false,
};

Map<String, dynamic> _payment(_State s) {
  final base = {'total_kobo': 64000000, 'returned': 0, 'short_by_kobo': 0, 'may_payslips': true, 'cannot_pay': false, 'scheduled_for': '2026-11-28'};
  return switch (s.stage) {
    'scheduled' => {...base, 'status': 'SCHEDULED', 'words': 'Pays on 28 Nov', 'tone': 'info', 'paid': 0, 'failed': 0, 'pending': 2, 'may_pay_now': true, 'may_retry': false},
    'short' => {...base, 'status': 'AWAITING_FUNDS', 'words': 'Waiting for money: short by ₦120,000', 'tone': 'warn', 'paid': 0, 'failed': 0, 'pending': 2, 'may_pay_now': false, 'may_retry': false},
    'some' => {...base, 'status': 'PARTIALLY_COMPLETED', 'words': 'Paid, 1 not paid', 'tone': 'warn', 'paid': 1, 'failed': 1, 'pending': 0, 'may_pay_now': false, 'may_retry': true},
    _ => {...base, 'status': 'COMPLETED', 'words': 'Paid', 'tone': 'ok', 'paid': 2, 'failed': 0, 'pending': 0, 'may_pay_now': false, 'may_retry': false},
  };
}

Map<String, dynamic> _view(_State s) {
  final paid = s.stage == 'some' || s.stage == 'paid';
  return {
    'connection': 'payrolla',
    'run': {'summary': _summary(s), 'created_at': '2026-11-20T09:00:00Z', 'updated_at': '2026-11-20T09:00:00Z', 'approved_at': null, 'locked_at': null},
    'lines': [
      {
        'payslip_id': _slipZ,
        'employee_id': 'e1',
        'name': 'Zainab Bello',
        'net_kobo': 38250000,
        'status': paid ? 'CONFIRMED' : 'PENDING',
        'state': paid ? 'paid' : 'waiting',
        'staff': null,
        'gross_kobo': 45000000,
        'total_earnings_kobo': 45000000,
        'total_deductions_kobo': 6750000,
        'items': _zItems,
      },
      {
        'payslip_id': _slipM,
        'employee_id': 'e2',
        'name': 'Musa Ali',
        'net_kobo': 25750000,
        'status': s.stage == 'some' ? 'FAILED' : 'PENDING',
        'state': s.stage == 'some' ? 'failed' : (paid ? 'paid' : 'waiting'),
        if (s.stage == 'some') 'reason': 'Account closed',
        'staff': null,
      },
    ],
    'counts': {'paid': 0, 'waiting': 2, 'failed': 0, 'returned': 0},
    'adjustments': <Object>[],
    'exclusions': <Object>[],
    'changes': {'new': <String>[], 'left': <String>[], 'pay_changed': <Object>[]},
    'may': {'prepare': false, 'approve': true, 'linked': true, 'reconnect': false},
    if (s.stage == 'scheduled' || s.stage == 'short') 'pays_on': 'Pays on 28 Nov',
    'payment': _payment(s),
  };
}

void _stand(_Api api, _State s) {
  api.routes['GET /v1/payroll/runs/$_run'] = (_) => _view(s);
  api.routes['POST /v1/payroll/runs/$_run/confirmations'] = (_) => http.Response(
    jsonEncode({
      'challenge': {'challenge_id': 'ch1', 'channel': 'TOTP', 'expires_at': '2026-11-24T10:05:00Z'},
      'sent_to': 'Type the code from your authenticator app.',
      'safer': false,
    }),
    202,
    headers: _json,
  );
  Object? pay(http.Request r, String words) {
    if ((jsonDecode(r.body) as Map)['code'] != '482910') return _err(422, 'code_invalid', 'That code isn’t right. Check it and type it again.');
    s.stage = 'paid';
    return {
      'run': _summary(s),
      'payment': {'status': 'COMPLETED', 'total_kobo': 64000000, 'paid': 2, 'failed': 0, 'returned': 0, 'pending': 0},
      'words': words,
    };
  }

  api.routes['POST /v1/payroll/runs/$_run/retry-failed'] = (r) => pay(r, 'Sent again. Payrolla pays each of them into their current account.');
  api.routes['POST /v1/payroll/runs/$_run/pay-now'] = (r) => pay(r, 'Paying now. Payrolla pays 2 people ₦640,000.');
  api.routes['GET /v1/payroll/payslips/$_slipZ'] = (_) => {'payslip': _payslip};
  api.routes['GET /v1/payroll/me/payslips/$_slipZ'] = (_) => {'payslip': _payslip};
  api.routes['GET /v1/payroll/payslips/$_slipZ/pdf'] = (_) => http.Response('%PDF-1.4 stand-in', 200, headers: {'content-type': 'application/pdf'});
  api.routes['POST /v1/payroll/payslips/$_slipZ/link'] = (_) => http.Response(jsonEncode({'url': 'https://payroller.test/p/abc', 'expires_at': '2026-12-01T10:00:00Z'}), 201, headers: _json);
  api.routes['GET /v1/payroll/me'] = (_) => {
    'linked': true,
    'me': {
      'name': 'Zainab Bello',
      'phone': '+234 803 ••• 4567',
      'status': 'active',
      'bank': {'bank_code': '058', 'bank': 'GTBank', 'account_name': 'ZAINAB A. BELLO', 'last4': '6789'},
      'waiting_change': null,
    },
  };
  api.routes['GET /v1/payroll/me/payslips'] = (_) => {
    'linked': true,
    'payslips': [
      {'payslip_id': _slipZ, 'run_id': _run, 'period': 'November 2026', 'month': 'November', 'pay_date': '2026-11-28', 'gross_kobo': 45000000, 'net_kobo': 38250000, 'state': 'paid', 'words': 'Paid'},
      {'payslip_id': _slipM, 'run_id': 'r-oct', 'period': 'October 2026', 'month': 'October', 'pay_date': '2026-10-31', 'gross_kobo': 45000000, 'net_kobo': 38250000, 'state': 'paid', 'words': 'Paid'},
    ],
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

  testWidgets('a payroll paid but one: who and why, and the failed one paid again with the code', (t) async {
    phone(t, 1200);
    final api = _Api();
    final s = _State();
    _stand(api, s);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollRunPage(auth: api.auth(), run: _run)));
    await settle(t);
    expect(find.text('Paid, 1 not paid'), findsOneWidget);
    expect(find.text('Paid 1 of 2 people, 1 not paid'), findsOneWidget);
    expect(find.text('Account closed'), findsOneWidget);
    await looks(t, 'payroll-run-paid-some');
    await snap(t, 'payroll-run-paid-some');

    await t.tap(find.text('Pay the failed ones again'));
    await settle(t);
    expect(find.byKey(const ValueKey('payroll-retry-note')), findsOneWidget);
    await t.tap(find.text('Send me a code'));
    await settle(t);
    expect(api.sent('POST', '/v1/payroll/runs/$_run/confirmations').single['action'], 'retry_failed');
    await snap(t, 'payroll-retry-sheet');
    await t.enterText(find.byType(EditableText).last, '000000');
    await t.pump();
    await t.tap(find.text('Pay the failed ones again').last);
    await settle(t);
    expect(find.text('That code isn’t right. Check it and type it again.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).last, '482910');
    await t.pump();
    await t.tap(find.text('Pay the failed ones again').last);
    await settle(t);
    expect(find.text('Sent again. Payrolla pays each of them into their current account.'), findsOneWidget);
    final keys = api.sent('POST', '/v1/payroll/runs/$_run/retry-failed').map((b) => b['key']).toList();
    expect(keys.toSet().length, keys.length);
  });

  testWidgets('waiting for money, then Pay now with the code', (t) async {
    phone(t, 1100);
    final api = _Api();
    final s = _State()..stage = 'short';
    _stand(api, s);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollRunPage(auth: api.auth(), run: _run)));
    await settle(t);
    expect(find.text('Waiting for money: short by ₦120,000'), findsOneWidget);
    expect(find.byKey(const ValueKey('payroll-add-money')), findsOneWidget);
    expect(find.text('Pay now'), findsNothing);
    await snap(t, 'payroll-run-short');

    s.stage = 'scheduled';
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollRunPage(key: const ValueKey('again'), auth: api.auth(), run: _run)));
    await settle(t);
    await t.tap(find.text('Pay now'));
    await settle(t);
    expect(find.byKey(const ValueKey('payroll-pay-now-warning')), findsOneWidget);
    await t.tap(find.text('Send me a code'));
    await settle(t);
    await t.enterText(find.byType(EditableText).last, '482910');
    await t.pump();
    await t.tap(find.text('Pay now').last);
    await settle(t);
    expect(find.text('Paying now. Payrolla pays 2 people ₦640,000.'), findsOneWidget);
    expect(api.sent('POST', '/v1/payroll/runs/$_run/confirmations').single['action'], 'pay_now');
  });

  testWidgets('a payslip from the run: the breakdown, the employer, Save as PDF, Send a link', (t) async {
    phone(t, 1100);
    final api = _Api();
    _stand(api, _State());
    final saved = <String>[];
    final was = attachments.handOffFile;
    attachments.handOffFile = (Uint8List bytes, String name, String type, Rect? from) async => saved.add('$name $type ${bytes.length}');
    addTearDown(() => attachments.handOffFile = was);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayslipPage(auth: api.auth(), payslip: _slipZ, own: false)));
    await settle(t);
    expect(find.text('Payslip for November 2026'), findsOneWidget);
    expect(find.text('Tax (PAYE)'), findsOneWidget);
    expect(find.text('Amara Foods Ltd · RC1234567'), findsOneWidget);
    expect(find.text('₦382,500'), findsWidgets);
    await looks(t, 'payslip');
    await snap(t, 'payslip');
    await t.tap(find.text('Save as PDF'));
    await settle(t);
    expect(saved.single, startsWith('payslip-november-2026.pdf application/pdf'));
    await t.tap(find.text('Send a link'));
    await settle(t);
    expect(find.byKey(const ValueKey('payslip-link')), findsOneWidget);
  });

  testWidgets('My pay lists one’s own payslips, and opens one', (t) async {
    phone(t, 1100);
    final api = _Api();
    _stand(api, _State());
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MyPayPage(auth: api.auth(permissions: const []))));
    await settle(t);
    expect(find.text('Payslips'), findsOneWidget);
    expect(find.text('November 2026'), findsOneWidget);
    expect(find.text('October 2026'), findsOneWidget);
    await looks(t, 'my-pay-payslips');
    await snap(t, 'my-pay-payslips');
    await t.tap(find.text('November 2026'));
    await settle(t);
    expect(find.text('Payslip for November 2026'), findsOneWidget);
    expect(find.text('Send a link'), findsNothing);
    expect(api.calls.where((c) => c.url.path == '/v1/payroll/me/payslips/$_slipZ'), isNotEmpty);
  });

  testWidgets('the payslip’s card in one’s own chat', (t) async {
    phone(t, 600);
    final api = _Api();
    _stand(api, _State());
    final auth = api.auth(permissions: const []);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TkPageScreen(title: 'Chat', backLabel: 'Back', children: [PayslipChatCard(auth: auth, payslip: _slipZ, period: 'November 2026')])));
    await settle(t);
    expect(find.text('Your payslip for November is ready'), findsOneWidget);
    expect(find.text('₦382,500'), findsOneWidget);
    await snap(t, 'payslip-chat-card');
    expect(payslipCardOf('{"payslip_id":"p1","period":"November 2026"}')?.payslip, 'p1');
    expect(payslipCardOf('{"run_id":"r1"}'), isNull);
    expect(payslipFile('November 2026'), 'payslip-november-2026.pdf');
  });
}
