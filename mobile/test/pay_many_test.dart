// Pay several people (P3; docs/spec/chat-pay-people.md): the list filled
// by pasting (lines that cannot be read kept to fix), Check preparing the
// batch (rows held out in amber, removable), one passkey for the batch
// sending the rows shown, a row per person, the same payment again said
// per row, the owner's OK for all, the failed ones again, the same as last
// time from Payments, and Pay all riders. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/pay_many.dart';
import 'package:taskiem/chat/payments_page.dart';
import 'package:taskiem/chat/riders_page.dart' show RidersPage;
import 'package:taskiem/kit/kit.dart' show TkField;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'pay_test.dart' show payment;
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

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
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': [if (owner) 'owner' else 'cashier'],
      'permissions': ['money.pay'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ada Obi', 'email': 'ada@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

/// A batch with every field the API marks required; the counts from its
/// payments, as the server's are.
Map<String, dynamic> batch(
  String id,
  List<Map<String, dynamic>> payments, {
  String status = 'prepared',
  String title = '',
  String kind = 'list',
  List<Object> held = const [],
  List<Object>? results,
}) {
  final live = payments.where((p) => p['status'] != 'cancelled' && p['status'] != 'rejected');
  int sum(Iterable<Map<String, dynamic>> ps) => ps.fold(0, (n, p) => n + (p['amount_kobo'] as int));
  return {
    'id': id,
    'title': title,
    'kind': kind,
    'status': status,
    'created_by': 'u1',
    'created_at': '2026-11-20T09:00:00Z',
    'confirmed_at': null,
    'payments': payments,
    'total_kobo': sum(live),
    'count': live.length,
    'paid_kobo': sum(live.where((p) => p['status'] == 'paid')),
    'waiting': live.where((p) => p['status'] == 'waiting_ok').length,
    'failed': live.where((p) => p['status'] == 'failed').length,
    'held': held,
    'results': ?results,
  };
}

http.Response _stepUp() => http.Response(
  jsonEncode({
    'error': 'confirm with your passkey',
    'step_up': 'required',
    'methods': ['passkey'],
  }),
  403,
  headers: _json,
);

class _Host extends StatefulWidget {
  const _Host(this.open);
  final Future<Object?> Function(BuildContext context) open;

  @override
  State<_Host> createState() => _HostState();
}

class _HostState extends State<_Host> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => widget.open(context));
  }

  @override
  Widget build(BuildContext context) => const SizedBox.expand();
}

void main() {
  setUpAll(loadFonts);
  setUp(() => TkField.debugHideCaret = true);

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

  FakePasskeys fakeKeys() {
    final was = passkeys;
    final fake = FakePasskeys();
    passkeys = fake;
    addTearDown(() => passkeys = was);
    return fake;
  }

  final bayo = payment('p1', payee: 'Bayo, rider', name: 'Adebayo Musa', kobo: 4500000, purpose: 'wages');
  final chidi = payment('p2', payee: 'Account ••6789', name: 'Chidi Okeke', bank: 'Guaranty Trust Bank', account: '••6789', kobo: 2000000, purpose: 'wages');

  testWidgets('a pasted list read, a line kept to fix; Check holds one out in amber; one passkey sends the rows shown', (t) async {
    phone(t, 1200);
    final keys = fakeKeys();
    final api = _Api();
    api.routes['POST /v1/payment-batches/read'] = (r) {
      final text = (jsonDecode(r.body) as Map)['text'] as String;
      return {
        'lines': [
          if (text.contains('Bayo'))
            {'line': 1, 'text': 'Bayo 0123456789 45k', 'account_number': '0123456789', 'amount_kobo': 4500000, 'name': 'Bayo', 'payee': 'bayo'},
          if (text.contains('Chidi'))
            {
              'line': 2,
              'text': 'Chidi 0001112223 GTBank 20k',
              'account_number': '0001112223',
              'bank_name': 'Guaranty Trust Bank',
              'amount_kobo': 2000000,
              'name': 'Chidi Okeke',
            },
          if (text.contains('Ngozi')) {'line': 3, 'text': 'Ngozi 12k', 'problem': 'No account number of 10 digits on this line.'},
          if (text.contains('Tolu'))
            {'line': 1, 'text': 'Tolu 9998887776 Opay 5k', 'account_number': '9998887776', 'bank_name': 'Opay', 'amount_kobo': 500000, 'name': 'Tolu'},
        ],
      };
    };
    api.routes['POST /v1/payment-batches'] = (_) => http.Response(
      jsonEncode({
        'batch': batch(
          'B1',
          [bayo, chidi],
          held: [
            {'row': 2, 'payee': 'Opay ••7776', 'code': 'name_check', 'problem': 'The bank has no account 9998887776 at Opay.'},
          ],
        ),
      }),
      201,
      headers: _json,
    );
    api.routes['POST /v1/me/step-up/options'] = (_) => {
      'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'},
    };
    api.routes['POST /v1/payment-batches/B1/confirm'] = (r) => (jsonDecode(r.body) as Map).containsKey('passkey')
        ? http.Response(
            jsonEncode({
              'batch': batch('B1', [
                payment('p1', payee: 'Bayo, rider', name: 'Adebayo Musa', kobo: 4500000, purpose: 'wages', status: 'sending'),
                {...chidi, 'status': 'paid'},
              ], status: 'sent'),
            }),
            202,
            headers: _json,
          )
        : _stepUp();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => openPayMany(c, api.auth()))));
    await settle(t);
    expect(find.text('Pay several people'), findsWidgets);
    expect(find.text('Add the people to pay'), findsOneWidget);

    await t.tap(find.bySemanticsLabel('Paste a list'));
    await settle(t);
    final box = find.descendant(of: find.byType(PayManySheet), matching: find.byType(EditableText));
    await t.enterText(box.first, 'Bayo 0123456789 45k\nChidi 0001112223 GTBank 20k\nNgozi 12k');
    await settle(t);
    await t.tap(find.text('Read the list'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/read').single['text'], contains('Ngozi'));
    // Two rows; the line not read is kept in the box, with why.
    expect(find.text('@bayo'), findsOneWidget);
    expect(find.text('Chidi Okeke'), findsOneWidget);
    expect(find.text('Line 3: Ngozi 12k'), findsOneWidget);
    expect((t.widget<EditableText>(box.first)).controller.text, 'Ngozi 12k');
    // Fixed in the box: read again.
    await t.enterText(box.first, 'Tolu 9998887776 Opay 5k');
    await settle(t);
    await t.tap(find.text('Read the list'));
    await settle(t);
    expect(find.text('Tolu'), findsOneWidget);
    expect(find.text('Line 3: Ngozi 12k'), findsNothing);
    // What for, for all of them.
    await t.ensureVisible(find.text('wages').first);
    await t.tap(find.text('wages').first);
    await settle(t);
    expect(find.text('3 people · ₦70,000'), findsOneWidget);
    await t.drag(find.byType(ListView).last, const Offset(0, 2000));
    await settle(t);
    await looks(t, 'pay-many');

    await t.tap(find.text('Check'));
    await settle(t);
    final body = api.sent('POST', '/v1/payment-batches').single;
    expect(body['kind'], 'list');
    expect(body['from'], 'tools');
    expect(body['rows'], [
      {'payee': 'bayo', 'amount_kobo': 4500000, 'purpose': 'wages'},
      {'account_number': '0001112223', 'bank_name': 'Guaranty Trust Bank', 'amount_kobo': 2000000, 'purpose': 'wages'},
      {'account_number': '9998887776', 'bank_name': 'Opay', 'amount_kobo': 500000, 'purpose': 'wages'},
    ]);
    // The batch: two to pay; Tolu's held out in amber with why.
    expect(find.text('Check before you pay'), findsOneWidget);
    expect(find.text('Held out: Tolu'), findsOneWidget);
    expect(find.text('The bank has no account 9998887776 at Opay.'), findsOneWidget);
    expect(find.text('2 people to pay · ₦65,000 · 1 held out'), findsOneWidget);
    expect(find.text('Pay 2 people'), findsOneWidget);
    await looks(t, 'pay-many-check');
    await t.tap(find.bySemanticsLabel('Remove Tolu'));
    await settle(t);
    expect(find.text('Held out: Tolu'), findsNothing);

    await t.tap(find.text('Pay 2 people'));
    await settle(t);
    expect(api.sent('POST', '/v1/me/step-up/options').single, {'operation': 'money.pay.batch', 'target': 'batch:B1'});
    expect(keys.asked, hasLength(1));
    final confirms = api.sent('POST', '/v1/payment-batches/B1/confirm');
    expect(confirms, hasLength(2));
    expect(confirms.first, {
      'payments': ['p1', 'p2'],
    });
    expect(confirms.last['payments'], ['p1', 'p2']);
    expect(confirms.last['passkey'], isA<Map<String, dynamic>>());
    await t.pump(const Duration(seconds: 2));
    await settle(t);
    // A row per person, as each goes.
    expect(find.text('Sending'), findsWidgets);
    expect(find.text('Paid'), findsWidgets);
    expect(find.text('₦20,000 paid'), findsOneWidget);
    expect(find.text('Same as last time'), findsOneWidget);
    // Sent: nothing let go.
    await t.tap(find.text('Done'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/B1/cancel'), isEmpty);
  });

  testWidgets('the same payment in the last day: said on its row, paid again only when said so; the rest let go', (t) async {
    phone(t);
    fakeKeys();
    final api = _Api();
    var sent = 0;
    api.routes['POST /v1/me/step-up/options'] = (_) => {
      'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'},
    };
    api.routes['POST /v1/payment-batches/B2/confirm'] = (r) {
      final b = jsonDecode(r.body) as Map;
      if (!b.containsKey('passkey')) return _stepUp();
      sent++;
      if (sent == 1) {
        return http.Response(
          jsonEncode({
            'batch': batch(
              'B2',
              [
                {...bayo, 'status': 'sending'},
                chidi,
              ],
              results: [
                {'payment': 'p1'},
                {'payment': 'p2', 'code': 'pay_again', 'problem': 'You paid them ₦20,000 in the last day.'},
              ],
            ),
          }),
          202,
          headers: _json,
        );
      }
      return http.Response(
        jsonEncode({
          'batch': batch('B2', [
            {...bayo, 'status': 'sending'},
            {...chidi, 'status': 'sending'},
          ], status: 'sent'),
        }),
        202,
        headers: _json,
      );
    };
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _Host(
          (c) => showTkSheet<PaymentBatch>(c, (_) => BatchSheet(auth: api.auth(), batch: PaymentBatch.fromJson(batch('B2', [bayo, chidi])), follow: false)),
        ),
      ),
    );
    await settle(t);
    await t.tap(find.text('Pay 2 people'));
    await settle(t);
    await t.pump(const Duration(seconds: 2));
    await settle(t);
    expect(find.text('Paid the same in the last day'), findsOneWidget);
    expect(find.text('You paid them ₦20,000 in the last day.'), findsOneWidget);
    expect(find.text('Pay 1 person'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pay Chidi Okeke again'));
    await settle(t);
    expect(find.text('Paying again'), findsOneWidget);
    await t.tap(find.text('Pay 1 person'));
    await settle(t);
    final last = api.sent('POST', '/v1/payment-batches/B2/confirm').last;
    expect(last['payments'], ['p2']);
    expect(last['again'], ['p2']);
    await t.pump(const Duration(seconds: 2));
    await settle(t);
  });

  testWidgets('a batch sent: the owner gives the OK for all with one passkey; their own left; the failed ones again', (t) async {
    phone(t, 1000);
    final keys = fakeKeys();
    final api = _Api();
    final waiting = payment(
      'w1',
      status: 'waiting_ok',
      payee: 'Ngozi, supplier',
      name: 'Ngozi Eze Ventures',
      bank: 'Access Bank',
      account: '••1234',
      kobo: 18000000,
      purpose: 'stock',
    );
    final mine = {
      ...payment('w2', status: 'waiting_ok', payee: 'Musa', name: 'Musa Bello', bank: 'Opay', account: '••4321', kobo: 1250000, purpose: 'wages'),
      'confirmed_by': 'u1',
    };
    final failed = payment('f1', status: 'failed', payee: 'Tunde', name: 'Tunde Ade', bank: 'Kuda', account: '••0001', kobo: 500000, purpose: 'wages');
    final paid = {...bayo, 'status': 'paid'};
    api.routes['POST /v1/me/step-up/options'] = (_) => {
      'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'},
    };
    api.routes['POST /v1/payment-batches/B3/approve'] = (r) => (jsonDecode(r.body) as Map).containsKey('passkey')
        ? {
            'batch': batch(
              'B3',
              [
                paid,
                {...waiting, 'status': 'sending'},
                mine,
                failed,
              ],
              status: 'sent',
              results: [
                {'payment': 'w1'},
                {'payment': 'w2', 'code': 'own', 'problem': 'You confirmed this one: another owner gives its OK.'},
              ],
            ),
          }
        : _stepUp();
    api.routes['POST /v1/payment-batches/B3/again'] = (_) => http.Response(
      jsonEncode({
        'batch': batch('B4', [payment('f9', payee: 'Tunde', name: 'Tunde Ade', bank: 'Kuda', kobo: 500000, purpose: 'wages')], title: 'Week 46 (again)'),
      }),
      201,
      headers: _json,
    );
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _Host(
          (c) => showTkSheet<PaymentBatch>(
            c,
            (_) => BatchSheet(
              auth: api.auth(),
              batch: PaymentBatch.fromJson(batch('B3', [paid, waiting, mine, failed], status: 'sent', title: 'Week 46')),
              follow: false,
            ),
          ),
        ),
      ),
    );
    await settle(t);
    expect(find.text('Week 46'), findsWidgets);
    expect(find.text('Give the OK for all'), findsOneWidget);
    expect(find.text('2 waiting for your OK · ₦192,500'), findsOneWidget);
    expect(find.text('The bank refused the account.'), findsOneWidget);
    await looks(t, 'pay-batch');

    await t.tap(find.text('Give the OK for all'));
    await settle(t);
    expect(api.sent('POST', '/v1/me/step-up/options').single, {'operation': 'money.pay.approve', 'target': 'batch:B3'});
    expect(keys.asked, hasLength(1));
    expect(api.sent('POST', '/v1/payment-batches/B3/approve').last['payments'], ['w1', 'w2']);
    expect(find.text('1 you confirmed yourself: another owner gives its OK.'), findsOneWidget);
    await t.pump(const Duration(seconds: 4));
    await settle(t);
    // Their own is left for another owner: the failed one is next.
    expect(find.text('Pay the failed ones again'), findsOneWidget);
    await t.tap(find.text('Pay the failed ones again'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/B3/again'), hasLength(1));
    expect(find.text('Check before you pay'), findsOneWidget);
    expect(find.text('Pay 1 person'), findsOneWidget);
    // Closed unpaid: the new batch is let go.
    api.routes['POST /v1/payment-batches/B4/cancel'] = (_) => {'batch': batch('B4', const [], status: 'cancelled')};
    await t.tap(find.text('Cancel it'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/B4/cancel'), hasLength(1));
    await t.pump(const Duration(seconds: 4));
  });

  testWidgets('Payments: Pay several people, the batches paid together, the same as last time', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payments'] = (_) => {
      'payments': [
        {...bayo, 'status': 'paid'},
      ],
    };
    api.routes['GET /v1/pay-limits'] = (_) => {'limits': <Object>[]};
    api.routes['GET /v1/roles'] = (_) => {'roles': <Object>[]};
    // The list carries the counts alone.
    final sent = {
      ...batch(
        'B5',
        [
          {...bayo, 'status': 'paid'},
          {...chidi, 'status': 'paid'},
        ],
        status: 'sent',
        title: 'Riders’ pay',
        kind: 'riders',
      ),
      'payments': <Object>[],
    };
    api.routes['GET /v1/payment-batches'] = (_) => {
      'batches': [sent],
    };
    api.routes['POST /v1/payment-batches'] = (_) => http.Response(
      jsonEncode({
        'batch': batch('B6', [bayo, chidi], title: 'Riders’ pay', kind: 'riders'),
      }),
      201,
      headers: _json,
    );
    api.routes['POST /v1/payment-batches/B6/cancel'] = (_) => {'batch': batch('B6', const [], status: 'cancelled')};
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: SentPaymentsPage(auth: api.auth(), follow: false),
      ),
    );
    await settle(t);
    expect(find.text('Pay several people'), findsOneWidget);
    expect(find.text('Paid together'), findsOneWidget);
    expect(find.text('Riders’ pay'), findsOneWidget);
    await looks(t, 'payments-together');

    await t.tap(find.text('Same as last time'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches').single, {'copy_of': 'B5', 'from': 'tools'});
    expect(find.text('Check before you pay'), findsOneWidget);
    expect(find.text('Pay 2 people'), findsOneWidget);
    await t.tap(find.text('Cancel it'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/B6/cancel'), hasLength(1));
    await t.pump(const Duration(seconds: 4));
  });

  testWidgets('Pay all riders: what each is due, rider_id rows, kind riders; one with no bank left out and said', (t) async {
    phone(t, 1200);
    final api = _Api();
    api.routes['GET /v1/riders'] = (_) => {
      'riders': [
        {
          'id': 'R1',
          'name': 'Musa',
          'phone': '0803 123 4567',
          'zones': ['Lekki'],
          'fee_kobo': 100000,
          'active': true,
          'reached': 'whatsapp',
          'presence': {'state': 'free'},
          'bank': 'GTBank ••6789',
          'created_at': '2026-11-01T10:00:00Z',
        },
      ],
      'zones': <Object>[],
    };
    api.routes['GET /v1/riders/pay'] = (_) => {
      'lines': [
        {'rider_id': 'R1', 'rider': 'Musa', 'deliveries': 3, 'fees_kobo': 300000, 'owed_kobo': 0, 'net_kobo': 300000, 'bank': 'GTBank ••6789'},
        {'rider_id': 'R2', 'rider': 'Tolu', 'deliveries': 2, 'fees_kobo': 200000, 'owed_kobo': 0, 'net_kobo': 200000, 'bank': 'Opay ••1111'},
        {'rider_id': 'R3', 'rider': 'Kemi', 'deliveries': 1, 'fees_kobo': 100000, 'owed_kobo': 0, 'net_kobo': 100000, 'bank': ''},
      ],
      'payouts': <Object>[],
    };
    api.routes['GET /v1/riders/owed'] = (_) => {'owed': <Object>[]};
    api.routes['GET /v1/chat/actions'] = (_) => {
      'topics': [
        {
          'id': 'pay',
          'name': 'Pay',
          'icon': 'money',
          'color': 'lime',
          'actions': [
            {
              'id': 'pay.person',
              'title': 'Pay someone',
              'line': '',
              'icon': 'money',
              'does': 'asks',
              'opens': 'form',
              'tool': 'pay',
              'confirm': 'passkey',
              'fields': <Object>[],
            },
          ],
        },
      ],
    };
    api.routes['POST /v1/payment-batches'] = (_) => http.Response(
      jsonEncode({
        'batch': batch(
          'B7',
          [payment('r1', payee: 'Musa', name: 'MUSA BELLO', kobo: 300000), payment('r2', payee: 'Tolu', name: 'TOLU ADE', bank: 'Opay', kobo: 200000)],
          title: 'Riders’ pay',
          kind: 'riders',
        ),
      }),
      201,
      headers: _json,
    );
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: RidersPage(auth: api.auth()),
      ),
    );
    await settle(t);
    expect(find.text('Pay all riders'), findsOneWidget);
    await t.tap(find.text('Pay all riders'));
    await settle(t);
    expect(find.text('Musa'), findsWidgets);
    expect(find.text('Tolu'), findsOneWidget);
    expect(find.text('Kemi'), findsNothing);
    expect(find.text('1 rider is due pay with no bank account: add one in Pay riders.'), findsOneWidget);
    expect(find.text('2 people · ₦5,000'), findsOneWidget);
    await t.tap(find.text('Check'));
    await settle(t);
    final body = api.sent('POST', '/v1/payment-batches').single;
    expect(body['kind'], 'riders');
    expect(body['title'], 'Riders’ pay');
    expect(body['rows'], [
      {'rider_id': 'R1', 'amount_kobo': 300000, 'purpose': 'delivery'},
      {'rider_id': 'R2', 'amount_kobo': 200000, 'purpose': 'delivery'},
    ]);
    expect(find.text('MUSA BELLO'), findsOneWidget);
    // Back to the list: the batch let go.
    api.routes['POST /v1/payment-batches/B7/cancel'] = (_) => {'batch': batch('B7', const [], status: 'cancelled')};
    await t.tap(find.text('Back').last);
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/B7/cancel'), hasLength(1));
    expect(find.text('Check'), findsOneWidget);
    await t.tap(find.text('Cancel').last);
    await settle(t);
    await t.pump(const Duration(seconds: 4));
  });

  testWidgets('Staff: those with an account linked as staff_id rows for wages; those without shown, not payable, with why', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/staff'] = (_) => {
      'people': [
        {
          'user_id': 'U1',
          'name': 'Ada Obi',
          'roles': ['owner'],
          'chat': true,
          'you': true,
        },
        {
          'user_id': 'U2',
          'name': 'Tunde Bakare',
          'roles': ['cashier'],
          'chat': true,
        },
        {
          'user_id': 'U3',
          'name': 'Kemi Ade',
          'roles': ['packer'],
          'chat': true,
        },
      ],
      'invites': <Object>[],
    };
    Map<String, dynamic> person(String id, String name, Map<String, dynamic>? account) => {
      'user_id': id,
      'name': name,
      'roles': ['cashier'],
      'chat': true,
      'profile': {'title': '', 'note': '', 'rota': <Object>[]},
      'pay_account': account,
    };
    api.routes['GET /v1/payments/staff'] = (_) => {
      'staff': [
        person('U1', 'Ada Obi', null),
        person('U2', 'Tunde Bakare', {'saved_id': 'A1', 'name': 'tunde_bank', 'title': 'Tunde (Moniepoint)', 'bank_name': 'Moniepoint', 'last4': '4821'}),
        person('U3', 'Kemi Ade', {'saved_id': 'A2', 'name': 'kemi', 'title': 'Kemi', 'bank_name': 'Opay', 'last4': '7790'}),
      ],
    };
    api.routes['POST /v1/payment-batches'] = (_) => http.Response(
      jsonEncode({
        'batch': batch('B8', [payment('s1', payee: 'Tunde Bakare', name: 'TUNDE BAKARE', bank: 'Moniepoint', kobo: 2500000, purpose: 'wages')]),
      }),
      201,
      headers: _json,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => openPayMany(c, api.auth()))));
    await settle(t);
    await snap(t, 'pay-many-buttons');
    await t.tap(find.bySemanticsLabel('Add staff, to the account linked to each'));
    await settle(t);
    expect(find.text('Tunde Bakare'), findsOneWidget);
    expect(find.text('Staff · Moniepoint ••4821'), findsOneWidget);
    expect(find.text('Not payable yet'), findsOneWidget);
    expect(find.text('No account linked: an owner links one on their page'), findsOneWidget);
    await looks(t, 'pay-many-staff');
    await t.tap(find.text('Tunde Bakare'));
    await t.pump();
    await t.tap(find.text('Ada Obi'));
    await t.pump();
    await t.tap(find.text('Add 1'));
    await settle(t);
    expect(find.text('Tunde Bakare'), findsOneWidget);
    final box = find.descendant(of: find.byType(PayManySheet), matching: find.byType(EditableText));
    await t.enterText(box.first, '25000');
    await settle(t);
    await t.tap(find.text('Check'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches').single['rows'], [
      {'staff_id': 'U2', 'amount_kobo': 2500000, 'purpose': 'wages'},
    ]);
    // Staff already in the list are not offered again; the rest still are.
    api.routes['POST /v1/payment-batches/B8/cancel'] = (_) => {'batch': batch('B8', const [], status: 'cancelled')};
    await t.tap(find.text('Back').last);
    await settle(t);
    await t.tap(find.bySemanticsLabel('Add staff, to the account linked to each'));
    await settle(t);
    expect(find.text('Kemi Ade'), findsOneWidget);
    expect(find.text('Staff · Moniepoint ••4821'), findsOneWidget, reason: 'on its row in the list, not offered again');
    await t.tap(find.text('Cancel').last);
    await settle(t);
    await t.tap(find.text('Cancel').last);
    await settle(t);
    await t.pump(const Duration(seconds: 4));
  });

  testWidgets('large text and what can be pressed: the list, the check, a batch sent', (t) async {
    final api = _Api();
    final auth = api.auth();
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: _Host(
          (c) => openPayMany(
            c,
            auth,
            rows: [
              PayRow(who: '@bayo', sub: 'Bayo, rider · GTBank ••6789', ask: {'payee': 'bayo'}, kobo: 4500000, purpose: 'wages'),
              PayRow(who: 'Chidi Okeke', sub: '••6789', ask: const {}, number: '0001112223', kobo: 2000000),
            ],
          ),
        ),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: _Host(
          (c) => showTkSheet<PaymentBatch>(
            c,
            (_) => BatchSheet(
              auth: auth,
              batch: PaymentBatch.fromJson(batch('B8', [bayo, chidi])),
              held: const [(who: 'Tolu', problem: 'The bank has no account 9998887776 at Opay.')],
              follow: false,
            ),
          ),
        ),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: _Host(
          (c) => showTkSheet<PaymentBatch>(
            c,
            (_) => BatchSheet(
              auth: auth,
              batch: PaymentBatch.fromJson(
                batch(
                  'B9',
                  [
                    {...bayo, 'status': 'paid'},
                    payment('w1', status: 'waiting_ok'),
                    payment('f1', status: 'failed'),
                  ],
                  status: 'sent',
                  title: 'Week 46',
                ),
              ),
              follow: false,
            ),
          ),
        ),
      ),
      drive: settle,
    );
  });
}
