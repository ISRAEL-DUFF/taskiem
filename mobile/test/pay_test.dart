// Pay one person (P1; docs/spec/chat-pay-people.md): the Pay sheet a
// catalogue action with confirm "passkey" opens (prepared with POST
// /v1/payments, the bank's name large, the warnings, Pay with the passkey
// for this payment, Pay again), paying an account number with its bank
// picked, the Payments page (statuses, Cancel, the owner's limits per
// role), and the link to one payment. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/route.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/pay_sheet.dart';
import 'package:taskiem/chat/payments_page.dart';
import 'package:taskiem/kit/kit.dart' show TkField;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

/// A stand-in server: answers by "METHOD /path", every request kept.
class _Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  List<Map<String, dynamic>> sent(String method, String path) =>
      [for (final c in calls.where((c) => c.method == method && c.url.path == path)) c.body.isEmpty ? <String, dynamic>{} : jsonDecode(c.body) as Map<String, dynamic>];

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

http.Response _err(int status, String code, String words, [Map<String, dynamic> more = const {}]) =>
    http.Response(jsonEncode({'error': words, 'code': code, ...more}), status, headers: _json);

/// A payment with every field the API marks required.
Map<String, dynamic> payment(
  String id, {
  String status = 'prepared',
  String payee = 'Bayo, rider',
  String? name = 'Adebayo Musa',
  String bank = 'Guaranty Trust Bank',
  String account = '••6789',
  int kobo = 4500000,
  String purpose = 'delivery',
  bool first = false,
  bool changed = false,
  bool again = false,
  String at = '2026-11-21T09:30:00Z',
}) => {
  'id': id,
  'saved_id': 's1',
  'personal': false,
  'payee': payee,
  'bank_name': bank,
  'account': account,
  'account_name': name,
  'amount_kobo': kobo,
  'purpose': purpose,
  'purpose_note': '',
  'note': '',
  'first_payment': first,
  'changed_account': changed,
  'needs_ok': status == 'waiting_ok' ? 'limit' : '',
  'status': status,
  'reason': status == 'failed' ? 'The bank refused the account.' : null,
  'fee_kobo': status == 'paid' ? 2500 : null,
  'run_id': null,
  'started_from': 'chat',
  'prepared_by': 'u1',
  'confirmed_by': null,
  'created_at': at,
  'confirmed_at': null,
  'finished_at': null,
  'again': again,
};

final _payPerson = ChatAction.fromJson({
  'id': 'pay.person',
  'title': 'Pay someone',
  'line': 'From your balance, checked with the bank',
  'icon': 'money',
  'does': 'asks',
  'opens': 'form',
  'tool': 'pay',
  'confirm': 'passkey',
  'fields': [
    {'key': 'who', 'label': 'Who are you paying?', 'hint': 'One of your saved accounts, like @chidi.', 'kind': 'payee'},
    {'key': 'amount', 'label': 'How much?', 'kind': 'money'},
    {'key': 'for', 'label': 'What is it for?', 'kind': 'choice', 'choices': ['a bill', 'wages', 'a delivery', 'a refund', 'stock bought', 'other']},
    {'key': 'note', 'label': 'A few words for their bank statement?', 'kind': 'text', 'optional': true},
  ],
});

final _payAccount = ChatAction.fromJson({
  'id': 'pay.account',
  'title': 'Pay an account number',
  'line': 'Any bank: its name is checked first',
  'icon': 'money',
  'does': 'asks',
  'opens': 'form',
  'tool': 'pay',
  'add': true,
  'confirm': 'passkey',
  'fields': [
    {'key': 'number', 'label': 'What is the account number?', 'hint': '10 digits.', 'kind': 'account'},
    {'key': 'bank', 'label': 'Which bank is it with?', 'kind': 'bank'},
    {'key': 'amount', 'label': 'How much?', 'kind': 'money'},
    {'key': 'for', 'label': 'What is it for?', 'kind': 'choice', 'choices': ['a bill', 'wages', 'a delivery', 'a refund', 'stock bought', 'other']},
  ],
});

/// A screen that opens [open]'s sheet over it, and keeps what it closed with.
class _Host extends StatefulWidget {
  const _Host(this.open, this.closed);
  final Future<Object?> Function(BuildContext context) open;
  final List<Object?> closed;

  @override
  State<_Host> createState() => _HostState();
}

class _HostState extends State<_Host> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) async => widget.closed.add(await widget.open(context)));
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

  void phone(WidgetTester t) {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  testWidgets('Pay someone: @name chips, prepared, the bank’s name large, a first payment, paid with the passkey', (t) async {
    phone(t);
    final was = passkeys;
    final fake = FakePasskeys();
    passkeys = fake;
    addTearDown(() => passkeys = was);
    final api = _Api();
    api.routes['GET /v1/chat/actions/pay.person/choices'] = (_) => {
      'fields': {
        'who': ['@bayo', '@chidi'],
      },
    };
    api.routes['POST /v1/payments'] = (_) => {'payment': payment('p1', first: true)};
    api.routes['POST /v1/me/step-up/options'] = (_) => {'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'}};
    api.routes['POST /v1/payments/p1/confirm'] = (_) => http.Response(jsonEncode({'payment': payment('p1', status: 'sending', first: true)}), 202, headers: _json);
    final closed = <Object?>[];
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: _payPerson)), closed)));
    await settle(t);

    expect(find.text('Pay someone'), findsWidgets);
    expect(find.text('@bayo'), findsOneWidget);
    await t.tap(find.text('@bayo'));
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    await t.enterText(boxes.at(1), '45k');
    await t.ensureVisible(find.text('a delivery'));
    await t.tap(find.text('a delivery'));
    await settle(t);
    await snap(t, 'pay-form');
    await t.tap(find.text('Next'));
    await settle(t);

    final body = api.sent('POST', '/v1/payments').single;
    expect(body, {'payee': 'bayo', 'amount_kobo': 4500000, 'purpose': 'delivery', 'from': 'chat'});
    // The bank's name large, the payee, bank and masked account under it.
    expect(find.text('Adebayo Musa'), findsOneWidget);
    expect(find.text('Bayo, rider · Guaranty Trust Bank · ••6789'), findsOneWidget);
    expect(find.text('First payment to this account'), findsOneWidget);
    expect(find.text('Pay ₦45,000'), findsOneWidget);
    await looks(t, 'pay-check');

    await t.tap(find.text('Pay ₦45,000'));
    await settle(t);
    expect(api.sent('POST', '/v1/me/step-up/options').single, {'operation': 'money.pay', 'target': 'payment:p1'});
    expect(fake.asked, hasLength(1));
    final confirm = api.sent('POST', '/v1/payments/p1/confirm').single;
    expect(confirm['passkey'], isA<Map<String, dynamic>>());
    expect(confirm.containsKey('again'), isFalse);
    expect(find.text('Sent to the bank'), findsWidgets);
    await t.pump(const Duration(seconds: 2));
    await settle(t);
    expect(find.byType(PaySheet), findsNothing);
    expect((closed.single! as Payment).status, 'sending');
    // Paid: nothing let go.
    expect(api.sent('POST', '/v1/payments/p1/cancel'), isEmpty);
  });

  testWidgets('the same payment in the last day: said, and Pay again sends again', (t) async {
    phone(t);
    final was = passkeys;
    passkeys = FakePasskeys();
    addTearDown(() => passkeys = was);
    final api = _Api();
    var tries = 0;
    api.routes['POST /v1/me/step-up/options'] = (_) => {'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'}};
    api.routes['POST /v1/payments/p2/confirm'] = (r) {
      tries++;
      final b = jsonDecode(r.body) as Map<String, dynamic>;
      if (b['again'] != true) return _err(409, 'pay_again', 'You paid Bayo, rider ₦45,000 in the last day. Pay again?');
      return http.Response(jsonEncode({'payment': payment('p2', status: 'waiting_ok')}), 202, headers: _json);
    };
    final closed = <Object?>[];
    final auth = api.auth(owner: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => showTkSheet<Payment>(c, (_) => PaymentSheet(auth: auth, payment: Payment.fromJson(payment('p2', changed: true)))), closed)));
    await settle(t);
    expect(find.text('The account changed since they were last paid'), findsOneWidget);
    expect(find.text('Cancel it'), findsOneWidget);
    await t.tap(find.text('Pay ₦45,000'));
    await settle(t);
    expect(find.text('You paid Bayo, rider ₦45,000 in the last day. Pay again?'), findsOneWidget);
    expect(find.text('Pay again ₦45,000'), findsOneWidget);
    await snap(t, 'pay-again');
    await t.tap(find.text('Pay again ₦45,000'));
    await settle(t);
    expect(tries, 2);
    expect(api.sent('POST', '/v1/payments/p2/confirm').last['again'], true);
    expect(find.text('Sent for the owner’s OK'), findsWidgets);
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('no passkey on this phone: a code from the authenticator', (t) async {
    phone(t);
    final was = passkeys;
    passkeys = _NoPasskeys();
    addTearDown(() => passkeys = was);
    final api = _Api();
    api.routes['POST /v1/payments/p3/confirm'] = (_) => http.Response(jsonEncode({'payment': payment('p3', status: 'sending')}), 202, headers: _json);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => showTkSheet<Payment>(c, (_) => PaymentSheet(auth: api.auth(), payment: Payment.fromJson(payment('p3')))), [])));
    await settle(t);
    await t.tap(find.text('Pay ₦45,000'));
    await settle(t);
    expect(find.text('A code from your authenticator app'), findsOneWidget);
    expect(api.sent('POST', '/v1/payments/p3/confirm'), isEmpty);
    await t.enterText(find.descendant(of: find.byType(PaymentSheet), matching: find.byType(EditableText)), '123456');
    await t.pump();
    await t.tap(find.text('Pay ₦45,000'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments/p3/confirm').single, {'totp': '123456'});
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('Pay an account number: 10 digits, the bank from the list, its name checked as it is prepared', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/banks'] = (_) => {
      'banks': [
        {'code': '044', 'name': 'Access Bank'},
        {'code': '058', 'name': 'Guaranty Trust Bank'},
      ],
      'from': 'iswallet/taskiem',
    };
    api.routes['POST /v1/payments'] = (_) => {'payment': payment('p4', payee: 'Account ••6789', name: 'Chidi Okeke', purpose: 'stock')};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: _payAccount)), [])));
    await settle(t);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), '01234');
    await t.enterText(boxes.at(1), '20000');
    await t.ensureVisible(find.text('stock bought'));
    await t.tap(find.text('stock bought'));
    await t.tap(find.text('Choose the bank'));
    await settle(t);
    await t.tap(find.text('Guaranty Trust Bank'));
    await settle(t);
    expect(find.text('An account number is 10 digits.'), findsOneWidget);
    await t.enterText(boxes.at(0), '0123456789');
    await settle(t);
    await snap(t, 'pay-account');
    await t.tap(find.text('Next'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments').single, {
      'account_number': '0123456789',
      'amount_kobo': 2000000,
      'bank_name': 'Guaranty Trust Bank',
      'purpose': 'stock',
      'from': 'chat',
    });
    expect(find.text('Chidi Okeke'), findsOneWidget);
    expect(find.text('Account ••6789 · Guaranty Trust Bank · ••6789'), findsOneWidget);

    // Back to the form: the prepared one let go.
    await t.tap(find.text('Back'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments/p4/cancel'), hasLength(1));
    expect(find.text('Next'), findsOneWidget);
  });

  testWidgets('a refusal is said on the sheet', (t) async {
    phone(t);
    final api = _Api();
    api.routes['POST /v1/payments'] = (_) => _err(409, 'no_provider', 'Connect a payments account first: people are paid from it.');
    api.routes['GET /v1/chat/actions/pay.person/choices'] = (_) => {'fields': {}};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: _payPerson, initial: const {'who': '@bayo', 'amount': '45000', 'for': 'wages'})), [])));
    await settle(t);
    await t.tap(find.text('Next'));
    await settle(t);
    expect(find.text('Connect a payments account first: people are paid from it.'), findsOneWidget);
    expect(api.sent('POST', '/v1/payments').single['purpose'], 'wages');
  });

  testWidgets('Payments: statuses in words, one opened and cancelled; the owner’s limits per role', (t) async {
    phone(t);
    final api = _Api();
    final list = [
      payment('a1', at: '2026-11-21T10:00:00Z'),
      payment('a2', status: 'waiting_ok', payee: 'Ngozi, supplier', name: 'Ngozi Eze Ventures', bank: 'Access Bank', account: '••1234', kobo: 18000000, purpose: 'stock', at: '2026-11-20T15:00:00Z'),
      payment('a3', status: 'paid', payee: 'Musa', name: 'Musa Bello', bank: 'Opay', account: '••4321', kobo: 1250000, purpose: 'wages', at: '2026-11-19T08:00:00Z'),
      payment('a4', status: 'failed', payee: 'Account ••0001', name: 'Tunde Ade', bank: 'Kuda', account: '••0001', kobo: 500000, purpose: 'refund', at: '2026-11-18T12:00:00Z'),
    ];
    api.routes['GET /v1/payments'] = (r) {
      final s = r.url.queryParameters['status'];
      return {'payments': [for (final p in list) if (s == null || p['status'] == s) p]};
    };
    api.routes['GET /v1/pay-limits'] = (_) => {
      'limits': [
        {'role': 'shift_manager', 'one_kobo': 5000000, 'day_kobo': 20000000},
      ],
    };
    api.routes['GET /v1/roles'] = (_) => {
      'roles': [
        {'name': 'owner'},
        {'name': 'cashier'},
        {'name': 'shift_manager'},
      ],
    };
    api.routes['POST /v1/payments/a1/cancel'] = (_) => {'payment': payment('a1', status: 'cancelled')};
    api.routes['PUT /v1/pay-limits/cashier'] = (r) => {'role': 'cashier', ...jsonDecode(r.body) as Map<String, dynamic>};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SentPaymentsPage(auth: api.auth(), follow: false)));
    await settle(t);
    expect(find.text('Payments'), findsWidgets);
    expect(find.text('Not sent yet'), findsWidgets);
    expect(find.text('Waiting for an OK'), findsWidgets);
    expect(find.text('Not paid'), findsWidgets);
    expect(find.text('₦180,000'), findsOneWidget);
    expect(find.text('What others may pay'), findsOneWidget);
    expect(find.text('₦50,000 a payment · ₦200,000 a day'), findsOneWidget);
    expect(find.text('Nothing without your OK'), findsOneWidget);
    await looks(t, 'payments');

    // A filter: asked of the server.
    await t.tap(find.text('Paid').first);
    await settle(t);
    expect(api.calls.last.url.queryParameters['status'], 'paid');
    expect(find.text('Musa Bello'), findsOneWidget);
    expect(find.text('Adebayo Musa'), findsNothing);
    await t.tap(find.text('All').first);
    await settle(t);

    // One not sent yet: opened, and cancelled.
    await t.tap(find.text('Adebayo Musa'));
    await settle(t);
    expect(find.byType(PaymentSheet), findsOneWidget);
    expect(find.text('Check before you pay'), findsOneWidget);
    await t.tap(find.text('Cancel it'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments/a1/cancel'), hasLength(1));
    expect(find.byType(PaymentSheet), findsNothing);

    // A role's limit.
    await t.ensureVisible(find.text('Cashier'));
    await t.tap(find.text('Cashier'));
    await settle(t);
    expect(find.byType(PayLimitSheet), findsOneWidget);
    final boxes = find.descendant(of: find.byType(PayLimitSheet), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), '300k');
    await t.enterText(boxes.at(1), '200k');
    await t.pump();
    expect(find.text('One payment is no more than a day’s total.'), findsOneWidget);
    await t.enterText(boxes.at(0), '50k');
    await t.pump();
    await snap(t, 'pay-limit');
    await t.tap(find.text('Save'));
    await settle(t);
    expect(api.sent('PUT', '/v1/pay-limits/cashier').single, {'one_kobo': 5000000, 'day_kobo': 20000000});
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a payment sent shows what became of it; staff see no limits', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payments'] = (_) => {
      'payments': [payment('b1', status: 'paid')],
    };
    api.routes['GET /v1/payments/b1'] = (_) => {'payment': payment('b1', status: 'paid')};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SentPaymentsPage(auth: api.auth(owner: false), follow: false, open: 'b1')));
    await settle(t);
    expect(find.byType(PaymentSheet), findsOneWidget);
    expect(find.text('Fee'), findsOneWidget);
    expect(find.text('₦25'), findsOneWidget);
    expect(find.text('Done'), findsOneWidget);
    expect(find.text('Pay ₦45,000'), findsNothing);
    await snap(t, 'payment-paid');
    await t.tap(find.text('Done'));
    await settle(t);
    expect(find.text('What others may pay'), findsNothing);
    expect(api.calls.where((c) => c.url.path == '/v1/pay-limits'), isEmpty);
  });

  testWidgets('large text and what can be pressed: the Pay form, the check, Payments', (t) async {
    final api = _Api();
    api.routes['GET /v1/chat/actions/pay.person/choices'] = (_) => {
      'fields': {
        'who': ['@bayo', '@chidi'],
      },
    };
    api.routes['GET /v1/payments'] = (_) => {
      'payments': [payment('c1', first: true), payment('c2', status: 'waiting_ok', kobo: 18000000)],
    };
    api.routes['GET /v1/pay-limits'] = (_) => {'limits': <Object>[]};
    api.routes['GET /v1/roles'] = (_) => {
      'roles': [
        {'name': 'cashier'},
      ],
    };
    final auth = api.auth();
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: auth, action: _payPerson)), [])), drive: settle);
    await expectLargeText(
      t,
      TaskiemApp(mode: TkMode.dark, home: _Host((c) => showTkSheet<Payment>(c, (_) => PaymentSheet(auth: auth, payment: Payment.fromJson(payment('c1', first: true, changed: true, again: true)))), [])),
      drive: settle,
    );
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: SentPaymentsPage(auth: auth, follow: false)), drive: settle);
  });

  test('a link to a payment opens it over Payments', () {
    expect(appPageOf(Uri.parse('$taskiemUrl/payments/0193-abc')), '/payments/0193-abc');
    expect(appPageOf(Uri.parse('$taskiemUrl/payments')), '/payments');
    final one = routeOf('/payments/0193-abc');
    expect((one.tab, one.screen, one.id), (tabMoney, RouteScreen.payments, '0193-abc'));
    final all = routeOf('/payments');
    expect((all.screen, all.id), (RouteScreen.payments, ''));
    expect(purposeOf('stock bought'), 'stock');
    expect(purposeOf('a delivery'), 'delivery');
    expect(purposeOf('for the generator'), 'other');
  });
}

class _NoPasskeys extends FakePasskeys {
  @override
  Future<bool> available() async => false;
}
