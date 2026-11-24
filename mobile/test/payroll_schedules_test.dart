// Payroll settings and the tax and pension schedules (P4b-5): the setup
// read-only with Change in Payrolla; Tax and pension with November's PAYE,
// pension and NHF; the pension schedule with its PINs masked, shown with a
// passkey; Download handed to the share sheet as a spreadsheet; and an
// owner whose Payrolla doesn't allow reports told so. Pictured light and
// dark.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/payroll_schedules.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

class _Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

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
      'permissions': ['payroll.read', 'payroll.approve'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ngozi Eze', 'email': 'ngozi@amara.test', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

const _paye = '0192e2e0-0000-7000-8000-00000000f501';
const _pen = '0192e2e0-0000-7000-8000-00000000f502';

Map<String, dynamic> _sched(String id, String kind, String title, String to, int total, String when) => {
  'id': id,
  'run_id': 'r1',
  'kind': kind,
  'title': title,
  'to': to,
  'period': '2026-11',
  'period_words': 'November 2026',
  'total_kobo': total,
  'people': 3,
  'state': 'to_pay',
  'state_words': 'To pay',
  'when': when,
};

final _schedules = [
  _sched(_paye, 'paye', 'Tax (PAYE)', 'Lagos State Internal Revenue Service', 7000000, 'Due 10 Dec'),
  _sched(_pen, 'pension', 'Pension', 'Pension fund administrator •••• IBTC', 18000000, 'Due 7 Dec'),
  _sched('0192e2e0-0000-7000-8000-00000000f503', 'nhf', 'Housing fund (NHF)', 'Federal Mortgage Bank of Nigeria', 1800000, 'Due 10 Dec'),
];

const _people = [
  {'employee_id': 'e1', 'name': 'Zainab Bello', 'amount_kobo': 8100000, 'pfa': 'STANBIC_IBTC', 'pin': 'PEN100012345678'},
  {'employee_id': 'e2', 'name': 'Musa Ali', 'amount_kobo': 5400000, 'pfa': 'STANBIC_IBTC', 'pin': 'PEN100087654321'},
  {'employee_id': 'e3', 'name': 'Ada Obi', 'amount_kobo': 4500000, 'pfa': 'STANBIC_IBTC', 'pin': 'PEN200011112222'},
];

final Map<String, dynamic> _settings = {
      'is_owner': true,
      'settings': {
        'company': {'name': 'Amara Foods Ltd'},
        'rows': [
          {'key': 'pay_frequency', 'label': 'How often people are paid', 'value': 'Every month'},
          {'key': 'tax_state', 'label': 'State you pay tax to', 'value': 'Lagos State'},
          {'key': 'separation_of_duties', 'label': 'Someone else approves', 'value': 'On: whoever prepares a payroll can’t approve it', 'on': true},
          {'key': 'funding', 'label': 'How payroll is paid for', 'value': 'You add money to your Payrolla wallet before pay day'},
          {'key': 'paye_tin', 'label': 'PAYE tax number', 'value': 'Not on file yet. Payrolla needs it to file your tax: add it in Payrolla', 'warn': true, 'on': false},
        ],
        'deductions': [
          {'key': 'paye', 'label': 'Tax (PAYE)', 'value': 'In force, by the tax tables', 'on': true},
          {'key': 'pension', 'label': 'Pension', 'value': '8% from each person, 10% from you', 'on': true},
          {'key': 'nhf', 'label': 'Housing fund (NHF)', 'value': '2.5% of basic pay', 'on': true},
          {'key': 'nhis', 'label': 'Health insurance (NHIS)', 'value': 'Not in force', 'on': false},
        ],
        'change_url': 'https://payroller.example/settings',
        'separation_of_duties': true,
      },
    };

String _mask(Object? v) => '•••• ${(v as String).substring(v.length - 4)}';

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

  testWidgets('the setup, read-only, with Change in Payrolla', (t) async {
    phone(t, 1000);
    final api = _Api();
    api.routes['GET /v1/payroll/settings'] = (_) => _settings;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollSetupPage(auth: api.auth())));
    await settle(t);
    expect(find.text('How payroll is set up'), findsWidgets);
    expect(find.text('Every month'), findsOneWidget);
    expect(find.text('8% from each person, 10% from you'), findsOneWidget);
    expect(find.textContaining('Not on file yet'), findsOneWidget);
    expect(find.text('Change in Payrolla'), findsOneWidget);
    expect(find.byType(EditableText), findsNothing);
    await looks(t, 'payroll-setup');
    await snap(t, 'payroll-setup');
  });

  testWidgets('Tax and pension: November’s schedules; the pension one masked, PINs shown with a passkey, downloaded', (t) async {
    phone(t, 900);
    final api = _Api();
    final fake = FakePasskeys();
    final was = passkeys;
    passkeys = fake;
    addTearDown(() => passkeys = was);
    final handed = <String>[];
    final hand = handOffFile;
    handOffFile = (Uint8List bytes, String name, String type, Rect? from) async => handed.add('$name $type ${utf8.decode(bytes)}');
    addTearDown(() => handOffFile = hand);
    api.routes['GET /v1/payroll/schedules'] = (_) => {'schedules': _schedules};
    api.routes['GET /v1/payroll/schedules/$_pen'] = (_) => {
      'schedule': _schedules[1],
      'lines': [for (final p in _people) {...p, 'pfa': _mask(p['pfa']), 'pin': _mask(p['pin'])}],
      'masked': true,
    };
    api.routes['POST /v1/me/step-up/options'] = (_) => {'publicKey': {'challenge': 'AAAA', 'allowCredentials': <Object>[]}};
    api.routes['POST /v1/payroll/schedules/$_pen/reveal'] = (r) {
      if ((jsonDecode(r.body) as Map)['passkey'] == null) return http.Response(jsonEncode({'error': 'Confirm with your passkey.', 'step_up': 'passkey'}), 403, headers: _json);
      return {'schedule': _schedules[1], 'lines': _people, 'masked': false};
    };
    api.routes['POST /v1/payroll/schedules/$_pen/download'] = (_) => {'filename': 'pension-november-2026.csv', 'csv': 'Name,Amount (NGN),PFA,Pension PIN\n'};
    final auth = api.auth();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayrollSchedulesPage(auth: auth)));
    await settle(t);
    expect(find.text('November 2026'), findsOneWidget);
    expect(find.text('Tax (PAYE)'), findsOneWidget);
    expect(find.text('Due 10 Dec · 3 people'), findsNWidgets(2));
    expect(find.text('₦180,000'), findsOneWidget);
    await looks(t, 'payroll-schedules');
    await snap(t, 'payroll-schedules');

    await t.tap(find.text('Pension'));
    await settle(t);
    expect(find.text('Pension, November 2026'), findsWidgets);
    expect(find.text('PIN •••• 5678 · PFA •••• IBTC'), findsOneWidget);
    expect(find.textContaining('PEN1000'), findsNothing);
    await snap(t, 'payroll-schedule-masked');

    await t.tap(find.text('Show PINs'));
    await settle(t);
    expect(find.text('PIN PEN100012345678 · PFA STANBIC_IBTC'), findsOneWidget);
    expect(find.text('Show PINs'), findsNothing);
    final reveal = api.calls.where((c) => c.url.path == '/v1/payroll/schedules/$_pen/reveal').single;
    expect((jsonDecode(reveal.body) as Map)['passkey'], isNotNull);
    expect(api.calls.where((c) => c.url.path == '/v1/me/step-up/options').map((c) => jsonDecode(c.body)), [
      {'operation': 'payroll.reveal', 'target': _pen},
    ]);
    await snap(t, 'payroll-schedule-shown');

    await t.tap(find.text('Download'));
    await settle(t);
    expect(handed, ['pension-november-2026.csv text/csv Name,Amount (NGN),PFA,Pension PIN\n']);
    expect(fake.asked, hasLength(2));
  });

  testWidgets('Payrolla doesn’t allow reports: said, to reconnect', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payroll/schedules'] = (_) => http.Response(
      jsonEncode({'code': 'scope_required', 'error': 'Payrolla hasn’t allowed Taskiem to read your tax and pension schedules. Reconnect Payrolla and allow Read statutory reports.'}),
      409,
      headers: _json,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PayrollSchedulesPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Payrolla hasn’t allowed this'), findsOneWidget);
    expect(find.textContaining('Reconnect Payrolla and allow Read statutory reports.'), findsOneWidget);
    await snap(t, 'payroll-schedules-scope');
  });

  testWidgets('large text and what can be pressed: the setup, Tax and pension, a pension schedule', (t) async {
    final api = _Api();
    api.routes['GET /v1/payroll/settings'] = (_) => _settings;
    api.routes['GET /v1/payroll/schedules'] = (_) => {'schedules': _schedules};
    api.routes['GET /v1/payroll/schedules/$_pen'] = (_) => {
      'schedule': _schedules[1],
      'lines': [for (final p in _people) {...p, 'pfa': _mask(p['pfa']), 'pin': _mask(p['pin'])}],
      'masked': true,
    };
    final auth = api.auth();
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollSetupPage(auth: auth)), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollSchedulesPage(auth: auth)), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PayrollSchedulePage(auth: auth, id: _pen)), drive: settle);
  });
}
