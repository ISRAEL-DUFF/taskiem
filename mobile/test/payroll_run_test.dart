// This month's payroll (P4b-3): Prepare November payroll from Payroll; a
// payroll's page with the wallet short, what changed since last month,
// each person's breakdown, a bonus added before tax, someone left out and
// put back; Approve with Payrolla's code (a wrong one, then the right
// one), Lock and pay with its warning; Reject with a note; refusals said
// in words; the payroll's card in the Taskiem chat. Pictured light and dark.

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
import 'package:taskiem/chat/payroll_run.dart';
import 'package:taskiem/kit/page.dart';
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
      'permissions': ['payroll.read', 'payroll.prepare', 'payroll.approve'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ngozi Eze', 'email': 'ngozi@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

http.Response _err(int status, String code, String words) => http.Response(jsonEncode({'error': words, 'code': code}), status, headers: _json);

const _run = '0192e2e0-0000-7000-8000-00000000d301';
const _z = 'e1', _m = 'e2', _a = 'e3';

class _State {
  String status = 'COMPUTED';
  int bonus = 0;
  bool leftOut = false;
}

List<Map<String, dynamic>> _items(int gross, int bonus) => [
  {'kind': 'earning', 'code': 'BASIC', 'label': 'Basic', 'amount_kobo': gross * 6 ~/ 10},
  {'kind': 'earning', 'code': 'HOUSING', 'label': 'Housing', 'amount_kobo': gross * 25 ~/ 100},
  {'kind': 'earning', 'code': 'TRANSPORT', 'label': 'Transport', 'amount_kobo': gross * 15 ~/ 100},
  if (bonus > 0) {'kind': 'earning', 'code': 'BONUS', 'label': 'Bonus: Q4 target', 'amount_kobo': bonus},
  {'kind': 'deduction', 'code': 'PAYE', 'label': 'Tax (PAYE)', 'amount_kobo': (gross + bonus) * 7 ~/ 100},
  {'kind': 'deduction', 'code': 'PENSION_EE', 'label': 'Pension (yours)', 'amount_kobo': gross * 8 ~/ 100},
  {'kind': 'employer', 'code': 'PENSION_ER', 'label': 'Pension (employer’s)', 'amount_kobo': gross ~/ 10},
];

List<Map<String, dynamic>> _lines(_State s) => [
  for (final p in [(_z, 'Zainab Bello', 45000000, s.bonus), (_m, 'Musa Ali', 30000000, 0), if (!s.leftOut) (_a, 'Ada Obi', 25000000, 0)])
    () {
      final items = _items(p.$3, p.$4);
      final earn = p.$3 + p.$4;
      final ded = items.where((i) => i['kind'] == 'deduction').fold<int>(0, (a, i) => a + (i['amount_kobo'] as int));
      return {
        'payslip_id': 'ps-${p.$1}',
        'employee_id': p.$1,
        'name': p.$2,
        'net_kobo': earn - ded,
        'status': 'PENDING',
        'state': 'waiting',
        'staff': null,
        'gross_kobo': p.$3,
        'total_earnings_kobo': earn,
        'total_deductions_kobo': ded,
        'items': items,
      };
    }(),
];

Map<String, dynamic> _summary(_State s) {
  final lines = _lines(s);
  final net = lines.fold<int>(0, (a, l) => a + (l['net_kobo'] as int));
  return {
    'run_id': _run,
    'period_start': '2026-11-01',
    'period_end': '2026-11-30',
    'pay_date': '2026-11-28',
    'run_type': 'REGULAR',
    'status': s.status,
    'employee_count': lines.length,
    'total_gross_kobo': lines.fold<int>(0, (a, l) => a + (l['total_earnings_kobo'] as int)),
    'total_net_kobo': net,
    'awaiting_approval': s.status == 'COMPUTED',
  };
}

Map<String, dynamic> _view(_State s) {
  final sum = _summary(s);
  final net = sum['total_net_kobo'] as int;
  return {
    'connection': 'payrolla',
    'run': {'summary': sum, 'created_at': '2026-11-20T09:00:00Z', 'updated_at': '2026-11-20T09:00:00Z', 'approved_at': null, 'locked_at': null},
    'lines': _lines(s),
    'counts': {'paid': 0, 'waiting': _lines(s).length, 'failed': 0, 'returned': 0},
    'adjustments': [
      if (s.bonus > 0) {'id': 'adj1', 'employee_id': _z, 'name': 'Zainab Bello', 'type': 'bonus', 'amount_kobo': s.bonus, 'reason': 'Q4 target'},
    ],
    'exclusions': [
      if (s.leftOut) {'id': 'ex1', 'employee_id': _a, 'name': 'Ada Obi', 'reason': 'unpaid leave'},
    ],
    'changes': {
      'since': 'October 2026',
      'new': ['Ada Obi'],
      'left': <String>[],
      'pay_changed': [
        {'name': 'Zainab Bello', 'from_kobo': 40000000, 'to_kobo': 45000000},
      ],
    },
    'funding': {
      'has_wallet': true,
      'available_kobo': 80000000,
      'needed_kobo': net,
      'short_by_kobo': net - 80000000,
      'words': 'Your Payrolla wallet has ₦800,000. This payroll needs more: short by ₦${(net - 80000000) ~/ 100}. Add money before 28 Nov.',
    },
    'may': {'prepare': s.status == 'COMPUTED', 'approve': true, 'linked': true, 'reconnect': false},
    if (s.status == 'LOCKED') 'pays_on': 'Pays on 28 Nov',
  };
}

void _stand(_Api api, _State s) {
  api.routes['GET /v1/payroll/runs/$_run'] = (_) => _view(s);
  api.routes['POST /v1/payroll/runs/$_run/adjustments'] = (r) {
    s.bonus = (jsonDecode(r.body) as Map)['amount_kobo'] as int;
    return http.Response(jsonEncode({'run': _summary(s)}), 201, headers: _json);
  };
  api.routes['DELETE /v1/payroll/runs/$_run/adjustments/adj1'] = (_) {
    s.bonus = 0;
    return {'run': _summary(s)};
  };
  api.routes['POST /v1/payroll/runs/$_run/exclusions'] = (_) {
    s.leftOut = true;
    return http.Response(jsonEncode({'run': _summary(s)}), 201, headers: _json);
  };
  api.routes['DELETE /v1/payroll/runs/$_run/exclusions/ex1'] = (_) {
    s.leftOut = false;
    return {'run': _summary(s)};
  };
  api.routes['POST /v1/payroll/runs/$_run/confirmations'] = (_) => http.Response(
    jsonEncode({
      'challenge': {'challenge_id': 'ch1', 'channel': 'SMS', 'destination_hint': '•••• 4567', 'expires_at': '2026-11-24T10:05:00Z'},
      'sent_to': 'Payrolla sent a code to your phone ending 4567.',
      'safer': true,
    }),
    202,
    headers: _json,
  );
  Object? decide(http.Request r, bool lock) {
    if ((jsonDecode(r.body) as Map)['code'] != '482910') return _err(422, 'code_invalid', 'That code isn’t right. Check it and type it again.');
    s.status = lock ? 'LOCKED' : 'APPROVED';
    return {
      'run': _summary(s),
      'payment': {'status': lock ? 'SCHEDULED' : 'NOT_SCHEDULED', 'total_kobo': 1, 'paid': 0, 'failed': 0, 'returned': 0, 'pending': 3},
      'words': lock ? 'Locked. Payrolla pays 3 people ₦870,250 on 28 Nov.' : 'Approved. Lock and pay?',
    };
  }

  api.routes['POST /v1/payroll/runs/$_run/approve'] = (r) => decide(r, false);
  api.routes['POST /v1/payroll/runs/$_run/lock'] = (r) => decide(r, true);
  api.routes['POST /v1/payroll/runs/$_run/reject'] = (_) {
    s.status = 'DRAFT';
    return {'run': _summary(s), 'words': 'Sent back. Whoever prepared it is told, with your note.'};
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

  testWidgets('Prepare November payroll from Payroll opens it', (t) async {
    phone(t, 900);
    final api = _Api();
    final s = _State();
    _stand(api, s);
    var prepared = false;
    api.routes['GET /v1/payroll'] = (_) => {
      'connected': true,
      'connection': 'payrolla',
      'auth': 'oauth',
      'is_owner': true,
      'company': {'name': 'Amara Foods Ltd', 'pay_frequency': 'MONTHLY'},
      'runs': [if (prepared) _summary(s)],
      'may_prepare': true,
      'this_month': {'period': '2026-11', 'label': 'November 2026', 'month': 'November', if (prepared) 'run': _summary(s)},
    };
    api.routes['POST /v1/payroll/runs'] = (_) {
      prepared = true;
      return http.Response(jsonEncode({'run': _summary(s)}), 201, headers: _json);
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Prepare November payroll'), findsOneWidget);
    await snap(t, 'payroll-this-month');
    await t.tap(find.text('Prepare November payroll'));
    await settle(t);
    expect(api.sent('POST', '/v1/payroll/runs').single['period'], '2026-11');
    expect(find.textContaining('Short by ₦'), findsOneWidget);
  });

  testWidgets('a payroll: the wallet short, the changes, a breakdown, a bonus, someone left out and back; approve and lock with the code', (t) async {
    phone(t, 1400);
    final api = _Api();
    final s = _State();
    _stand(api, s);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollRunPage(auth: api.auth(), run: _run)));
    await settle(t);
    expect(find.text('November 2026'), findsOneWidget);
    expect(find.textContaining('Short by ₦'), findsOneWidget);
    expect(find.text('New this month: Ada Obi'), findsOneWidget);
    expect(find.text('Zainab Bello: pay ₦400,000 to ₦450,000'), findsOneWidget);
    expect(find.text('Approve'), findsOneWidget);
    await looks(t, 'payroll-run-prepare');
    await snap(t, 'payroll-run-prepare');

    // A person's breakdown.
    await t.tap(find.text('Zainab Bello').last);
    await settle(t);
    expect(find.text('Tax (PAYE)'), findsOneWidget);
    expect(find.text('Pension (employer’s)'), findsOneWidget);
    expect(find.text('Net pay'), findsOneWidget);
    await snap(t, 'payroll-line-sheet');
    await t.tap(find.text('Done'));
    await settle(t);

    // A bonus, before tax.
    await t.tap(find.text('Add a bonus or deduction'));
    await settle(t);
    expect(find.text('Before tax: tax and pension apply as usual.'), findsOneWidget);
    await t.enterText(find.byKey(const ValueKey('payroll-adjust-amount')), '100000');
    await t.enterText(find.byKey(const ValueKey('payroll-adjust-reason')), 'Q4 target');
    await t.pump();
    await snap(t, 'payroll-adjust-sheet');
    await t.tap(find.text('Add the bonus'));
    await settle(t);
    expect(api.sent('POST', '/v1/payroll/runs/$_run/adjustments').single, containsPair('amount_kobo', 10000000));
    expect(find.text('₦100,000 bonus, Q4 target'), findsOneWidget);

    // Ada left out, then put back.
    await t.tap(find.text('Ada Obi').last);
    await settle(t);
    await t.tap(find.text('Leave out this month'));
    await settle(t);
    await t.enterText(find.byKey(const ValueKey('payroll-leave-reason')), 'unpaid leave');
    await t.tap(find.text('Leave out'));
    await settle(t);
    expect(find.text('Left out this month'), findsOneWidget);
    expect(find.text('unpaid leave'), findsOneWidget);
    await t.tap(find.text('Put back'));
    await settle(t);
    expect(find.text('Left out this month'), findsNothing);

    // Approve with the code: a wrong one, then the right one.
    await t.tap(find.text('Approve'));
    await settle(t);
    await t.tap(find.text('Send me a code'));
    await settle(t);
    expect(find.text('Payrolla sent a code to your phone ending 4567.'), findsOneWidget);
    await snap(t, 'payroll-code-sheet');
    await t.enterText(find.byType(EditableText).last, '000000');
    await t.pump();
    await t.tap(find.text('Approve').last);
    await settle(t);
    expect(find.text('That code isn’t right. Check it and type it again.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).last, '482910');
    await t.pump();
    await t.tap(find.text('Approve').last);
    await settle(t);
    expect(find.text('Approved. Lock and pay?'), findsOneWidget);

    // Lock and pay, with its warning.
    await t.tap(find.text('Lock and pay'));
    await settle(t);
    expect(find.text('Locking pays everyone on 28 Nov. A locked payroll can’t be unlocked.'), findsWidgets);
    await t.tap(find.text('Send me a code'));
    await settle(t);
    await t.enterText(find.byType(EditableText).last, '482910');
    await t.pump();
    await t.tap(find.text('Lock and pay').last);
    await settle(t);
    expect(find.text('Locked. Payrolla pays 3 people ₦870,250 on 28 Nov.'), findsOneWidget);
    expect(find.text('Pays on 28 Nov'), findsOneWidget);
    final keys = [for (final p in ['approve', 'lock']) ...api.sent('POST', '/v1/payroll/runs/$_run/$p').map((b) => b['key'])];
    expect(keys.toSet().length, keys.length, reason: 'each try at a code has its own key');
  });

  testWidgets('reject with a note; the preparer refused in words; the card in the chat', (t) async {
    phone(t, 900);
    final api = _Api();
    final s = _State();
    _stand(api, s);
    api.routes['POST /v1/payroll/runs/$_run/confirmations'] = (_) => _err(403, 'sod', 'You prepared this payroll, so someone else must approve it. Ask Musa Bello.');
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PayrollRunPage(auth: api.auth(), run: _run)));
    await settle(t);
    await snap(t, 'payroll-run-prepare-dark');
    await t.tap(find.text('Approve'));
    await settle(t);
    await t.tap(find.text('Send me a code'));
    await settle(t);
    expect(find.text('You prepared this payroll, so someone else must approve it. Ask Musa Bello.'), findsOneWidget);
    await snap(t, 'payroll-code-refused');
    await t.tapAt(const Offset(10, 10));
    await settle(t);
    Navigator.of(t.element(find.byType(PayrollRunPage))).popUntil((r) => r.isFirst);
    await settle(t);
    await t.tap(find.text('Reject'));
    await settle(t);
    await t.enterText(find.byKey(const ValueKey('payroll-reject-note')), 'Musa’s bonus is wrong');
    await t.pump();
    await t.tap(find.text('Send it back').last);
    await settle(t);
    expect(api.sent('POST', '/v1/payroll/runs/$_run/reject').single['note'], 'Musa’s bonus is wrong');
    expect(find.text('Sent back. Whoever prepared it is told, with your note.'), findsOneWidget);

    // The card in the Taskiem chat.
    s.status = 'COMPUTED';
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: TkPageScreen(title: 'Chat', backLabel: 'Back', children: [PayrollChatCard(auth: api.auth(), run: _run, period: 'November 2026')]),
    ));
    await settle(t);
    expect(find.text('November 2026 payroll'), findsOneWidget);
    expect(find.textContaining('Your Payrolla wallet is short by'), findsOneWidget);
    expect(find.text('Approve'), findsOneWidget);
    expect(find.text('Reject'), findsOneWidget);
    await snap(t, 'payroll-chat-card');
    expect(payrollCardOf('{"run_id":"r1","period":"November 2026"}')?.period, 'November 2026');
    expect(payrollCardOf('not json'), isNull);
  });

  testWidgets('large text and what can be pressed: a payroll being prepared, its card', (t) async {
    final api = _Api();
    _stand(api, _State());
    final auth = api.auth();
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollRunPage(auth: auth, run: _run)), drive: settle);
    await expectLargeText(
      t,
      TaskiemApp(mode: TkMode.dark, home: TkPageScreen(title: 'Chat', backLabel: 'Back', children: [PayrollChatCard(auth: auth, run: _run, period: 'November 2026')])),
      drive: settle,
    );
  });
}
