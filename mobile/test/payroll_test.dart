// Payroll in Taskiem, on Payrolla (P4a): the runs with the latest net pay
// on top and Show more, Payrolla not connected, a refusal said; a run's
// lines, those not paid first, the approval link opened outside, a linked
// line opening the staff page; staff in Payrolla confirmed, chosen and
// unlinked; "In Payrolla" on a staff member's page and Payroll beside Pay
// several people, for those with payroll.read. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/links.dart' show openOutside;
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/payments_page.dart';
import 'package:taskiem/chat/payroll_page.dart';
import 'package:taskiem/chat/team_pages.dart';
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

  Auth auth({bool owner = true, bool payroll = true}) {
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
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': [if (owner) 'owner' else 'shift_manager'],
      'permissions': ['money.pay', 'member.manage', if (payroll) 'payroll.read'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ada Obi', 'email': 'ada@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

http.Response _err(int status, String code, String words) => http.Response(jsonEncode({'error': words, 'code': code}), status, headers: _json);

const _r1 = '11111111-1111-4111-8111-111111111111';
const _r2 = '22222222-2222-4222-8222-222222222222';
const _r3 = '33333333-3333-4333-8333-333333333333';
const _r4 = '44444444-4444-4444-8444-444444444444';

/// A run with every field the API marks required.
Map<String, dynamic> run(String id, {String start = '2026-11-01', String end = '2026-11-30', String pay = '2026-11-28', String status = 'COMPUTED', int people = 12, int net = 482500000}) => {
  'run_id': id,
  'period_start': start,
  'period_end': end,
  'pay_date': pay,
  'run_type': 'REGULAR',
  'status': status,
  'employee_count': people,
  'total_gross_kobo': net + net ~/ 4,
  'total_net_kobo': net,
  'awaiting_approval': status == 'COMPUTED',
};

Map<String, dynamic> _payroll({String? next}) => {
  'connected': true,
  'connection': 'payrolla',
  'test': false,
  'company': {'name': 'Mama’s Kitchen Ltd', 'pay_frequency': 'MONTHLY'},
  'runs': [
    run(_r1),
    run(_r2, start: '2026-10-01', end: '2026-10-31', pay: '2026-10-28', status: 'COMPLETED', people: 11, net: 451000000),
    run(_r3, start: '2026-09-01', end: '2026-09-30', pay: '2026-09-28', status: 'PARTIALLY_COMPLETED', people: 11, net: 449000000),
  ],
  'next_cursor': ?next,
};

Map<String, dynamic> _line(String id, String name, int kobo, String state, {String? reason, Map<String, dynamic>? staff}) => {
  'payslip_id': 'ps-$id',
  'employee_id': 'e-$id',
  'name': name,
  'net_kobo': kobo,
  'status': switch (state) {
    'paid' => 'CONFIRMED',
    'failed' => 'FAILED',
    'returned' => 'REVERSED',
    _ => 'PENDING',
  },
  'state': state,
  'reason': ?reason,
  'staff': staff,
};

Map<String, dynamic> _run({bool approval = true, String status = 'COMPUTED'}) => {
  'connection': 'payrolla',
  'run': {
    'summary': run(_r1, status: status, people: 4, net: 112000000),
    'created_at': '2026-11-20T09:00:00Z',
    'updated_at': '2026-11-21T09:00:00Z',
    'approved_at': null,
    'locked_at': null,
  },
  'lines': [
    _line('1', 'Tunde Bakare', 32000000, 'paid', staff: {'user_id': 'U2', 'name': 'Tunde'}),
    _line('2', 'Kemi Ade', 28000000, 'failed', reason: 'The bank refused the account: it is closed.'),
    _line('3', 'Chidi Okeke', 27000000, 'waiting'),
    _line('4', 'Bisi Lawal', 25000000, 'returned', reason: 'Returned by the bank: name does not match.'),
  ],
  'counts': {'paid': 1, 'waiting': 1, 'failed': 1, 'returned': 1},
  'approval_url': ?(approval ? 'https://app.payrolla.ng/runs/$_r1/approve' : null),
};

Map<String, dynamic> _staff() => {
  'connection': 'payrolla',
  'staff': [
    {
      'user_id': 'U1',
      'name': 'Ada Obi',
      'payrolla': {'employee_id': 'e-ada', 'name': 'Ada Obi', 'linked_by_name': 'Ada Obi', 'linked_at': '2026-11-20T09:00:00Z'},
      'suggested': null,
    },
    {
      'user_id': 'U2',
      'name': 'Tunde',
      'payrolla': null,
      'suggested': {'employee_id': 'e-1', 'name': 'Tunde Bakare', 'by': 'email'},
    },
    {'user_id': 'U3', 'name': 'Chidi', 'payrolla': null, 'suggested': null},
  ],
  'employees': [
    {'id': 'e-ada', 'name': 'Ada Obi', 'department': 'Front of house', 'status': 'ACTIVE', 'linked_to': 'U1'},
    {'id': 'e-1', 'name': 'Tunde Bakare', 'department': 'Kitchen', 'status': 'ACTIVE', 'linked_to': null},
    {'id': 'e-3', 'name': 'Chidi Okeke', 'department': 'Kitchen', 'status': 'ACTIVE', 'linked_to': null},
    {'id': 'e-4', 'name': 'Bisi Lawal', 'status': 'TERMINATED', 'linked_to': null},
  ],
};

final _person = {
  'user_id': 'U2',
  'name': 'Tunde',
  'phone': '+2348031110000',
  'roles': ['cashier'],
  'chat': true,
  'profile': {'title': 'Cashier', 'started_on': '2026-01-05', 'note': '', 'rota': <Object>[]},
  'pay_account': null,
  'payrolla': {'employee_id': 'e-1', 'name': 'Tunde Bakare'},
};

final _figures = {
  'period': 'week',
  'minutes': 0,
  'shifts': 0,
  'late_days': <Object>[],
  'missed_days': <Object>[],
  'sales': {'count': 0, 'total_kobo': 0, 'by_how': <String, Object>{}},
  'handovers': <Object>[],
  'orders': 0,
  'tasks': {'done': 0, 'done_late': 0, 'cant': 0, 'open': 0, 'late': 0},
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

  void phone(WidgetTester t, [double tall = 800]) {
    t.view.physicalSize = Size(360, tall);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  testWidgets('Payroll: the latest run’s net pay on top, the runs in plain words, Show more, Staff in Payrolla', (t) async {
    phone(t, 900);
    final api = _Api();
    api.routes['GET /v1/payroll'] = (r) => r.url.queryParameters['cursor'] == 'c2'
        ? {
            'connected': true,
            'connection': 'payrolla',
            'company': {'name': 'Mama’s Kitchen Ltd', 'pay_frequency': 'MONTHLY'},
            'runs': [run(_r4, start: '2026-08-01', end: '2026-08-15', pay: '2026-08-16', status: 'CANCELLED', people: 3, net: 90000000)],
          }
        : _payroll(next: 'c2');
    api.routes['GET /v1/payroll/staff'] = (_) => _staff();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Payroll'), findsWidgets);
    expect(find.text('LATEST RUN'), findsOneWidget);
    expect(find.text('₦4,825,000'), findsWidgets);
    expect(find.text('Nov 2026'), findsOneWidget);
    expect(find.text('Oct 2026'), findsOneWidget);
    expect(find.text('Waiting for approval'), findsWidgets);
    expect(find.text('Paid'), findsOneWidget);
    expect(find.text('Partly paid'), findsOneWidget);
    expect(find.text('Pay day 28 Nov · 12 people'), findsOneWidget);
    await looks(t, 'payroll');
    await snap(t, 'payroll');

    await t.scrollUntilVisible(find.text('Show more'), 200);
    await t.tap(find.text('Show more'));
    await settle(t);
    expect(api.calls.where((c) => c.url.path == '/v1/payroll').last.url.queryParameters['cursor'], 'c2');
    expect(find.text('1 – 15 Aug 2026'), findsOneWidget);
    expect(find.text('Cancelled'), findsOneWidget);
    expect(find.text('Show more'), findsNothing);
    await snap(t, 'payroll-1');

    await t.scrollUntilVisible(find.text('Staff in Payrolla'), 200);
    await t.tap(find.text('Staff in Payrolla'));
    await settle(t);
    expect(find.text('To confirm'), findsOneWidget);
  });

  testWidgets('not connected: payroll runs in Payrolla, and how to connect it; a refusal said in its words', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payroll'] = (_) => {'connected': false, 'runs': <Object>[]};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Payroll runs in Payrolla'), findsOneWidget);
    expect(find.textContaining('connect payrolla'), findsOneWidget);
    expect(find.text('Staff in Payrolla'), findsNothing);
    await snap(t, 'payroll-not-connected');

    const words = 'Your Payrolla key cannot read this: in Payrolla, Settings → Connected apps, make a key with the Read payroll and Read employees permissions and reconnect it in Taskiem.';
    api.routes['GET /v1/payroll'] = (_) => _err(409, 'scope_required', words);
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PayrollPage(key: UniqueKey(), auth: api.auth())));
    await settle(t);
    expect(find.text('Payrolla’s key cannot read payroll'), findsOneWidget);
    expect(find.text(words.replaceAll(' → ', ', ')), findsOneWidget);
    await snap(t, 'payroll-refused');

    api.routes['GET /v1/payroll/runs/$_r1'] = (_) => _err(502, 'payrolla_down', 'Payrolla did not answer just now; try again in a minute.');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollRunPage(key: UniqueKey(), auth: api.auth(), run: _r1)));
    await settle(t);
    expect(find.text('Payrolla did not answer'), findsOneWidget);
    expect(find.text('Payrolla did not answer just now; try again in a minute.'), findsOneWidget);
  });

  testWidgets('a run: net pay, pay day and status on top; approve in Payrolla opened outside; not paid first; a linked line opens the staff page', (t) async {
    phone(t, 900);
    final api = _Api();
    api.routes['GET /v1/payroll/runs/$_r1'] = (_) => _run();
    api.routes['GET /v1/staff/U2'] = (_) => _person;
    api.routes['GET /v1/staff/U2/figures'] = (_) => _figures;
    final opened = <Uri>[];
    final was = openOutside;
    openOutside = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openOutside = was);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollRunPage(auth: api.auth(), run: _r1)));
    await settle(t);
    expect(find.text('November 2026'), findsOneWidget);
    expect(find.text('₦1,120,000'), findsOneWidget);
    expect(find.text('Pay day 28 Nov'), findsOneWidget);
    expect(find.text('Waiting for approval'), findsOneWidget);
    expect(find.text('Not paid'), findsWidgets);
    expect(find.text('The bank refused the account: it is closed.'), findsOneWidget);
    expect(find.text('Returned'), findsOneWidget);
    // Not paid and returned first.
    expect(t.getTopLeft(find.text('Kemi Ade')).dy, lessThan(t.getTopLeft(find.text('Tunde Bakare')).dy));
    expect(t.getTopLeft(find.text('Bisi Lawal')).dy, lessThan(t.getTopLeft(find.text('Chidi Okeke')).dy));
    await looks(t, 'payroll-run');
    await snap(t, 'payroll-run');

    await t.tap(find.text('Review and approve in Payrolla'));
    await settle(t);
    expect(opened.single.toString(), 'https://app.payrolla.ng/runs/$_r1/approve');

    await t.scrollUntilVisible(find.text('Tunde Bakare'), 200);
    await snap(t, 'payroll-run-1');
    await t.tap(find.text('Tunde Bakare'));
    await settle(t);
    expect(api.calls.any((c) => c.url.path == '/v1/staff/U2'), isTrue);
    expect(find.text('In Payrolla: Tunde Bakare'), findsOneWidget);
  });

  testWidgets('a run with no approval link: no button; lines not linked are not opened', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payroll/runs/$_r1'] = (_) => _run(approval: false, status: 'COMPLETED');
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PayrollRunPage(auth: api.auth(), run: _r1)));
    await settle(t);
    expect(find.text('Review and approve in Payrolla'), findsNothing);
    expect(find.text('Paid'), findsWidgets);
    await t.tap(find.text('Kemi Ade'));
    await settle(t);
    expect(find.text('Kemi Ade'), findsOneWidget, reason: 'still on the run');
    await snap(t, 'payroll-run-dark');
  });

  testWidgets('staff in Payrolla: Confirm the one who looks like them, Choose from those not linked, Unlink after asking', (t) async {
    phone(t, 900);
    final api = _Api();
    api.routes['GET /v1/payroll/staff'] = (_) => _staff();
    api.routes['PUT /v1/staff/U2/payrolla'] = (_) => {
      'payrolla': {'employee_id': 'e-1', 'name': 'Tunde Bakare'},
    };
    api.routes['PUT /v1/staff/U3/payrolla'] = (_) => {
      'payrolla': {'employee_id': 'e-3', 'name': 'Chidi Okeke'},
    };
    api.routes['PUT /v1/staff/U1/payrolla'] = (_) => {'payrolla': null};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollStaffPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Looks like Tunde Bakare (same email)'), findsOneWidget);
    expect(find.text('Not linked to anyone in Payrolla'), findsOneWidget);
    expect(find.text('In Payrolla: Ada Obi · linked by Ada Obi'), findsOneWidget);
    expect(find.text('2 employees in Payrolla are not linked'), findsOneWidget);
    await looks(t, 'payroll-staff');
    await snap(t, 'payroll-staff');

    await t.tap(find.text('Confirm'));
    await settle(t);
    expect(api.sent('PUT', '/v1/staff/U2/payrolla').single, {'employee_id': 'e-1'});

    await t.tap(find.text('Choose'));
    await settle(t);
    expect(find.text('Who is Chidi in Payrolla?'), findsOneWidget);
    expect(find.text('Chidi Okeke'), findsOneWidget);
    expect(find.text('Left'), findsOneWidget, reason: 'one who left is said so');
    expect(find.text('Ada Obi'), findsWidgets);
    expect(find.text('Front of house'), findsNothing, reason: 'Ada Obi in Payrolla is linked to someone else');
    await snap(t, 'payroll-staff-choose');
    await t.tap(find.text('Chidi Okeke'));
    await settle(t);
    expect(api.sent('PUT', '/v1/staff/U3/payrolla').single, {'employee_id': 'e-3'});

    await t.tap(find.text('Unlink'));
    await settle(t);
    expect(find.text('Unlink Ada Obi from Payrolla?'), findsOneWidget);
    await t.tap(find.text('Keep it'));
    await settle(t);
    expect(api.sent('PUT', '/v1/staff/U1/payrolla'), isEmpty);
    await t.tap(find.text('Unlink'));
    await settle(t);
    await t.tap(find.text('Unlink').last);
    await settle(t);
    expect(api.sent('PUT', '/v1/staff/U1/payrolla').single, <String, dynamic>{}, reason: 'employee_id left out unlinks');
    await t.pump(const Duration(seconds: 4));
  });

  testWidgets('staff in Payrolla for one who is not an owner: who is who, no buttons', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payroll/staff'] = (_) => _staff();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollStaffPage(auth: api.auth(owner: false))));
    await settle(t);
    expect(find.text('An owner links staff to Payrolla'), findsOneWidget);
    expect(find.text('Confirm'), findsNothing);
    expect(find.text('Choose'), findsNothing);
    expect(find.text('Unlink'), findsNothing);
  });

  testWidgets('a staff member’s page: In Payrolla for those with payroll.read, opening Staff in Payrolla; Salaries for the rest', (t) async {
    phone(t, 1400);
    final api = _Api();
    api.routes['GET /v1/staff/U2'] = (_) => _person;
    api.routes['GET /v1/staff/U2/figures'] = (_) => _figures;
    api.routes['GET /v1/payroll/staff'] = (_) => _staff();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: StaffPersonPage(auth: api.auth(), user: 'U2')));
    await settle(t);
    expect(find.text('In Payrolla: Tunde Bakare'), findsOneWidget);
    expect(find.text('Salaries'), findsNothing);
    await snap(t, 'payroll-staff-person');
    await t.tap(find.text('In Payrolla: Tunde Bakare'));
    await settle(t);
    expect(find.text('Staff in Payrolla'), findsWidgets);
    expect(find.bySemanticsLabel('Back to Tunde'), findsOneWidget);

    await t.pumpWidget(TaskiemApp(key: UniqueKey(), mode: TkMode.light, home: StaffPersonPage(auth: api.auth(payroll: false), user: 'U2')));
    await settle(t);
    expect(find.text('Salaries'), findsOneWidget);
    expect(find.textContaining('In Payrolla'), findsNothing);
  });

  testWidgets('Payments: Payroll beside Pay several people, for those with payroll.read', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payments'] = (_) => {'payments': <Object>[]};
    api.routes['GET /v1/payment-batches'] = (_) => {'batches': <Object>[]};
    api.routes['GET /v1/pay-limits'] = (_) => {'limits': <Object>[]};
    api.routes['GET /v1/roles'] = (_) => {'roles': <Object>[]};
    api.routes['GET /v1/payroll'] = (_) => _payroll();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SentPaymentsPage(auth: api.auth(), follow: false)));
    await settle(t);
    expect(find.text('Payroll'), findsOneWidget);
    await t.tap(find.text('Payroll'));
    await settle(t);
    expect(find.text('LATEST RUN'), findsOneWidget);
    expect(find.bySemanticsLabel('Back to Payments'), findsOneWidget);

    await t.pumpWidget(TaskiemApp(key: UniqueKey(), mode: TkMode.light, home: SentPaymentsPage(auth: api.auth(payroll: false), follow: false)));
    await settle(t);
    expect(find.text('Pay several people'), findsOneWidget);
    expect(find.text('Payroll'), findsNothing);
  });

  testWidgets('large text and what can be pressed: Payroll, a run, staff in Payrolla', (t) async {
    final api = _Api();
    api.routes['GET /v1/payroll'] = (_) => _payroll(next: 'c2');
    api.routes['GET /v1/payroll/runs/$_r1'] = (_) => _run();
    api.routes['GET /v1/payroll/staff'] = (_) => _staff();
    final auth = api.auth();
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollPage(auth: auth)), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: PayrollRunPage(auth: auth, run: _r1)), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollStaffPage(auth: auth)), drive: settle);
  });
}
