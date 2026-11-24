// Paying one person, finished (docs/spec/build-plan-gaps.md H7): the Pay
// sheet's balance and Add money, the fee and the balance after before
// paying, Send them a receipt and Save as a beneficiary, the receipt and
// Try again on a payment's sheet, a payment's diary line in the chat, a
// batch's fees and by how much the balance is short, and Pay it back on a
// refund. Pictured light and dark.

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
import 'package:taskiem/chat/pay_diary.dart';
import 'package:taskiem/chat/pay_many.dart';
import 'package:taskiem/chat/pay_sheet.dart';
import 'package:taskiem/kit/forms.dart' show TkSwitch, TkSwitchRow;
import 'package:taskiem/kit/kit.dart' show TkField;
import 'package:taskiem/kit/page.dart' show TkPageScreen;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'pay_many_test.dart' show batch;
import 'pay_test.dart' show payment;
import 'support.dart' show loadFonts;

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
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': ['owner'],
      'permissions': ['money.pay', 'saved.manage'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ada Obi', 'email': 'ada@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

/// A payment to an account typed in, with the fee quoted and the balance.
Map<String, dynamic> _typed(String id, {String status = 'prepared'}) => {
  ...payment(id, status: status, payee: 'Account ••6789', name: 'CHIDI OKEKE', first: true),
  'saved_id': null,
  'fee_quote_kobo': 2500,
  'balance_kobo': 10000000,
  'route': 'bank',
  'tell_payee': true,
  'send_receipt': false,
  'may_tell': false,
};

final _payAccount = ChatAction.fromJson({
  'id': 'pay.account',
  'title': 'Pay an account number',
  'line': 'Any bank: its name is checked first',
  'icon': 'money',
  'does': 'asks',
  'opens': 'form',
  'tool': 'pay',
  'confirm': 'passkey',
  'fields': [
    {'key': 'number', 'label': 'What is the account number?', 'hint': '10 digits.', 'kind': 'account'},
    {'key': 'bank', 'label': 'Which bank is it with?', 'kind': 'bank'},
    {'key': 'amount', 'label': 'How much?', 'kind': 'money'},
    {
      'key': 'for',
      'label': 'What is it for?',
      'kind': 'choice',
      'choices': ['a bill', 'wages', 'a delivery', 'a refund', 'stock bought', 'other'],
    },
  ],
});

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

  void phone(WidgetTester t, {double height = 800}) {
    t.view.physicalSize = Size(360, height);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  testWidgets('an account typed: the balance and Add money, the fee before paying, Send them a receipt, saved as a beneficiary', (t) async {
    phone(t, height: 1100);
    final was = passkeys;
    passkeys = FakePasskeys();
    addTearDown(() => passkeys = was);
    final api = _Api();
    api.routes['GET /v1/payments/balance'] = (_) => {
      'from': 'your Taskiem wallet',
      'available_kobo': 10000000,
      'wallet': true,
      'quotes_fee': true,
      'add_money': {'bank_name': 'Wema Bank', 'account_number': '1100110011', 'account_name': 'Taskiem / Mama’s Kitchen'},
    };
    api.routes['GET /v1/payments/banks'] = (_) => {
      'from': 'iswallet/taskiem',
      'banks': [
        {'name': 'Guaranty Trust Bank', 'code': '058', 'likely': true},
      ],
    };
    api.routes['POST /v1/payments/name-check'] = (_) => {
      'banks': [
        {'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'account_name': 'CHIDI OKEKE'},
      ],
    };
    api.routes['POST /v1/payments'] = (_) => {'payment': _typed('p1')};
    api.routes['POST /v1/me/step-up/options'] = (_) => {
      'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'},
    };
    api.routes['POST /v1/payments/p1/confirm'] = (r) => (jsonDecode(r.body) as Map).containsKey('passkey')
        ? http.Response(jsonEncode({'payment': _typed('p1', status: 'sending')}), 202, headers: _json)
        : http.Response(
            jsonEncode({
              'error': 'confirm with your passkey',
              'step_up': 'required',
              'methods': ['passkey'],
            }),
            403,
            headers: _json,
          );
    api.routes['POST /v1/saved'] = (_) => http.Response(jsonEncode({'saved': <String, dynamic>{}}), 201, headers: _json);
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _Host(
          (c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: _payAccount, initial: const {'number': '0123456789', 'bank': 'Guaranty Trust Bank'}, from: 'tools')),
        ),
      ),
    );
    await settle(t);
    // What people are paid from, and Add money: the wallet's own number.
    expect(find.text('Balance ₦100,000'), findsOneWidget);
    await t.tap(find.text('Add money'));
    await settle(t);
    expect(find.text('1100110011'), findsOneWidget);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    await t.enterText(boxes.at(1), '45k');
    await t.ensureVisible(find.text('a delivery'));
    await t.tap(find.text('a delivery'));
    await settle(t);
    await looks(t, 'pay-balance');
    await t.tap(find.text('Next'));
    await settle(t);

    // Before paying: the fee, and the balance after with it.
    expect(find.text('₦45,000 + ₦25 fee'), findsOneWidget);
    expect(find.text('₦54,975'), findsOneWidget);
    expect(find.text('Send them a receipt'), findsOneWidget);
    expect(find.text('Tell them in the chat'), findsNothing);
    expect(find.text('Save as a beneficiary'), findsOneWidget);
    await looks(t, 'pay-fee');
    final receipt = find.descendant(of: find.widgetWithText(TkSwitchRow, 'Send them a receipt'), matching: find.byType(TkSwitch));
    await t.ensureVisible(receipt);
    await t.tap(receipt);
    await settle(t);
    await t.tap(find.text('Pay ₦45,000'));
    await settle(t);
    final confirm = api.sent('POST', '/v1/payments/p1/confirm').last;
    expect(confirm['send_receipt'], true);
    expect(confirm.containsKey('tell_payee'), isFalse);
    // Paid: saved as a beneficiary, by the name it offered.
    expect(api.sent('POST', '/v1/saved').single, {
      'account_number': '0123456789',
      'bank_name': 'Guaranty Trust Bank',
      'kind': 'bank_account',
      'name': 'chidi',
      'title': 'CHIDI OKEKE',
    });
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('paid: the receipt to send them and into the chat; failed: Try again prepares it again', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payments/p2/receipt'] = (_) => {
      'url': 'https://taskiem.test/v1/pay-receipts/abc/pdf',
      'receipt': {
        'business': 'Mama’s Kitchen',
        'payee': 'CHIDI OKEKE',
        'bank_name': 'Guaranty Trust Bank',
        'account': '••6789',
        'amount_kobo': 4500000,
        'for': 'a delivery',
        'paid_at': '2026-11-21T09:30:00Z',
        'reference': 'ABC',
        'route': 'Bank transfer',
      },
    };
    api.routes['POST /v1/payments/p2/share'] = (_) => http.Response(jsonEncode({'shared': 'taskiem'}), 201, headers: _json);
    api.routes['POST /v1/payments/p3/again'] = (_) => http.Response(jsonEncode({'payment': _typed('p4')}), 201, headers: _json);
    final auth = api.auth();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _Host((c) => showTkSheet<Payment>(c, (_) => PaymentSheet(auth: auth, payment: Payment.fromJson(_typed('p2', status: 'paid'))))),
      ),
    );
    await settle(t);
    expect(find.text('Send them the receipt'), findsOneWidget);
    await looks(t, 'pay-receipt');
    await t.tap(find.text('Put it in the chat'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments/p2/share').single, {'where': 'taskiem'});

    await t.pumpWidget(
      TaskiemApp(
        key: const ValueKey('failed'),
        mode: TkMode.light,
        home: _Host((c) => showTkSheet<Payment>(c, (_) => PaymentSheet(auth: auth, payment: Payment.fromJson(_typed('p3', status: 'failed'))))),
      ),
    );
    await settle(t);
    await t.tap(find.text('Try again'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments/p3/again'), hasLength(1));
    // Prepared again: to confirm here, with its fee.
    expect(find.text('Pay ₦45,000'), findsOneWidget);
    expect(find.text('₦45,000 + ₦25 fee'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a payment’s diary line: received by the bank, or failed with Try again', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/payments/p5'] = (_) => {'payment': _typed('p5', status: 'paid')};
    api.routes['GET /v1/payments/p6'] = (_) => {'payment': _typed('p6', status: 'failed')};
    final auth = api.auth();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: TkPageScreen(
          title: 'Your Taskiem chat',
          children: [
            PayDiaryLine(auth: auth, words: 'Paid CHIDI OKEKE ₦45,000 (delivery) · GTBank ••6789', when: 'You, 10:42', payment: 'p5'),
            PayDiaryLine(auth: auth, words: 'Paid MUSA BELLO ₦3,000 (delivery) · Access Bank ••2108', when: 'You, 10:43', payment: 'p6'),
            PayChatCard(auth: auth, kind: 'receipt', text: 'Paid CHIDI OKEKE ₦45,000 for a delivery on 21 Nov 2026', payment: 'p5'),
          ],
        ),
      ),
    );
    await settle(t);
    expect(find.textContaining('Received by the bank'), findsOneWidget);
    expect(find.textContaining('Failed: refunded to your balance'), findsOneWidget);
    expect(find.text('Try again'), findsOneWidget);
    await looks(t, 'pay-diary');
  });

  testWidgets('a batch’s foot: the fees, and by how much the balance is short', (t) async {
    phone(t);
    final api = _Api();
    final rows = [payment('b1', kobo: 1000000), payment('b2', kobo: 2000000)];
    final b = {
      ...batch('B9', rows),
      'fees_kobo': 5000,
      'fees_unknown': 0,
      'balance_kobo': 2500000,
      'short_kobo': 505000,
      'fit': ['b1'],
    };
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _Host((c) => showTkSheet<PaymentBatch>(c, (_) => BatchSheet(auth: api.auth(), batch: PaymentBatch.fromJson(b), follow: false))),
      ),
    );
    await settle(t);
    expect(find.text('₦50'), findsOneWidget);
    expect(find.text('Your balance is short'), findsOneWidget);
    expect(find.text('₦25,000: ₦5,050 short for all 2 people. The first one fits: take some out, or add money first.'), findsOneWidget);
    await looks(t, 'pay-batch-short');
  });

  testWidgets('Pay it back on a refund: the sheet filled in with the amount and a refund', (t) async {
    phone(t);
    final api = _Api();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: refundPayAction('TK-ABC123'), initial: const {'amount': '5000', 'for': 'a refund'}, from: 'tools'))),
      ),
    );
    await settle(t);
    expect(find.text('Pay back TK-ABC123'), findsWidgets);
    expect(find.text('5000'), findsOneWidget);
    expect(find.text('Their account number'), findsOneWidget);
  });
}
