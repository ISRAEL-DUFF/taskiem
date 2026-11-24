// A film: pay a supplier (video/recording-app.md). From Today, Ada taps Money,
// then Pay someone, picks @bayo, types the amount on the number keyboard, picks
// what it is for, checks the bank's name, and pays with a fingerprint. Set-up
// copied from today_tab_test.dart and pay_test.dart; the filming is film.dart's.
// Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_pay_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/pay-a-supplier --out pay.mp4 --captions

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/pay_sheet.dart' show PaySheet;
import 'package:taskiem/chat/today_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart';

// Thursday 12 November 2026, 13:00 in Lagos.
final _now = DateTime.utc(2026, 11, 12, 12);

final _approval = {
  'run_id': 'R1',
  'step_id': 'pay',
  'step_name': 'Pay Bayo Rice Mills',
  'workflow': 'Pay suppliers',
  'environment': 'prod',
  'requested_at': '2026-11-12T10:30:00Z',
  'approvals': 0,
  'required': 1,
  'level': 1,
  'levels': 1,
  'sole_approver': false,
  'subject': {'amount_kobo': 15600000},
};

Map<String, dynamic> _topic(String id, String name, String icon, String color, List<(String, String, String)> rows) => {
  'id': id, 'name': name, 'icon': icon, 'color': color,
  'actions': [for (final (aid, title, line) in rows) {'id': aid, 'title': title, 'line': line, 'icon': icon, 'does': 'show', 'opens': 'show'}],
};


final _payPerson = {
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
    {
      'key': 'for',
      'label': 'What is it for?',
      'kind': 'choice',
      'choices': ['a bill', 'wages', 'a delivery', 'a refund', 'stock bought', 'other'],
    },
    {'key': 'note', 'label': 'A few words for their bank statement?', 'kind': 'text', 'optional': true},
  ],
};

Map<String, dynamic> _payment(String status) => {
  'id': 'p1',
  'saved_id': 's1',
  'personal': false,
  'payee': 'Bayo Rice Mills',
  'bank_name': 'Guaranty Trust Bank',
  'account': '••6789',
  'account_name': 'BAYO RICE MILLS LTD',
  'amount_kobo': 15600000,
  'purpose': 'stock bought',
  'purpose_note': '',
  'note': '',
  'first_payment': true,
  'changed_account': false,
  'needs_ok': '',
  'status': status,
  'reason': null,
  'fee_kobo': null,
  'run_id': null,
  'started_from': 'chat',
  'prepared_by': 'u1',
  'confirmed_by': null,
  'created_at': '2026-11-12T12:00:00Z',
  'confirmed_at': null,
  'finished_at': null,
  'again': false,
};

/// The fingerprint check takes as long as the film says: it waits on [gate].
class _FilmPasskeys extends FakePasskeys {
  Completer<void>? gate;
  @override
  Future<Map<String, dynamic>> get(Map<String, dynamic> publicKey) async {
    await gate?.future;
    return super.get(publicKey);
  }
}

final _catalogue = {
  'topics': [
    _topic('sales', 'Sales', 'sell', 'lime', [('sales.today', "Today's sales", 'What came in today'), ('sales.week', 'How we did this week', 'Sales, money in and out')]),
    _topic('stock', 'Stock', 'stock', 'orange', [('stock.low', "See what's low", 'Items about to run out')]),
    _topic('products', 'Products', 'products', 'teal', [('products.list', 'Your products', 'What you sell')]),
    _topic('orders', 'Orders', 'orders', 'blue', [('orders.open', 'Open orders', 'Waiting for you')]),
    {
      ..._topic('money', 'Money', 'money', 'lime', [('money.today', 'Money today', 'In and out')]),
      'actions': [
        {'id': 'money.today', 'title': 'Money today', 'line': 'In and out', 'icon': 'money', 'does': 'show', 'opens': 'show'},
        _payPerson,
      ],
    },
    _topic('owe', 'Who owes you', 'customers', 'red', [('owe.list', 'Who owes you', 'Everyone, biggest first')]),
    _topic('staff', 'Staff and tasks', 'tasks', 'blue', [('staff.mine', 'Your tasks', 'What is on you today')]),
    _topic('auto', 'Automations', 'workflows', 'teal', [('auto.list', 'Your automations', 'What runs for you')]),
    _topic('account', 'Your account', 'you', 'violet', [('account.me', 'Your account', 'You and your business')]),
  ],
};

void main() {
  setUpAll(loadFonts);

  Future<List<String>> open(WidgetTester t, {bool settled = true}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final asked = <String>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          asked.add('${req.method} ${req.url.path}${req.url.query.isEmpty ? '' : '?${req.url.query}'}');
          if (req.method == 'POST' && req.url.path == '/v1/chat/actions/sales.today/run') {
            return http.Response.bytes(
              utf8.encode(jsonEncode({
                'messages': [
                  {'id': 'h1', 'channel': 'web', 'inbound': true, 'kind': 'text', 'text': "Today's sales", 'created_at': '2026-11-12T13:00:00Z'},
                  {'id': 'h2', 'channel': 'web', 'inbound': false, 'kind': 'text', 'text': 'Sales today: ₦45,000 from 3 sales.', 'created_at': '2026-11-12T13:00:00Z'},
                ],
              })),
              200,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          if (req.url.path == '/v1/chat/actions/pay.person/choices') {
            return ok({
              'fields': {
                'who': ['@bayo', '@chidi'],
              },
            });
          }
          if (req.method == 'POST' && req.url.path == '/v1/payments') return ok({'payment': _payment('prepared')});
          if (req.method == 'POST' && req.url.path == '/v1/me/step-up/options') {
            return ok({
              'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'},
            });
          }
          if (req.method == 'POST' && req.url.path == '/v1/payments/p1/confirm') {
            return (jsonDecode(req.body) as Map).containsKey('passkey')
                ? http.Response(jsonEncode({'payment': _payment('sending')}), 202, headers: {'content-type': 'application/json'})
                : http.Response(jsonEncode({'error': 'confirm with your passkey', 'step_up': 'required', 'methods': ['passkey']}), 403, headers: {'content-type': 'application/json'});
          }
          if (req.method != 'GET') return ok({'ok': true});
          return switch (req.url.path) {
            // Today's menu (menus-everywhere.md §5): the person's topics.
            '/v1/chat/actions' => ok(_catalogue),
            // The same weekday in the weeks before: ₦20,000 by 9 am.
            '/v1/money/summary' when req.url.queryParameters['from'] != '2026-11-12' => ok({
              'in_settled_kobo': 2000000,
              'out_settled_kobo': 0,
              'buckets': [
                {'from': '${req.url.queryParameters['from']}T08:00:00Z', 'in_settled_kobo': 2000000, 'out_settled_kobo': 0, 'payments': 1},
              ],
            }),
            '/v1/money/summary' => ok({
              'in_settled_kobo': 4500000,
              'out_settled_kobo': 1000000,
              'buckets': [
                {'from': '2026-11-12T08:00:00Z', 'in_settled_kobo': 4500000, 'out_settled_kobo': 0, 'payments': 3},
              ],
            }),
            '/v1/money/unmatched' => ok({
              'payments': [
                {'entry_id': 'E1', 'amount_kobo': 700000, 'channel': 'paystack/main', 'received_at': '2026-11-12T09:00:00Z'},
              ],
            }),
            '/v1/storefront/orders' => ok({
              'orders': [
                {
                  'reference': 'AK-2',
                  'status': 'received',
                  'customer': {'name': 'Tunde'},
                  'total_kobo': 300000,
                  'created_at': '2026-11-12T10:00:00Z',
                },
              ],
            }),
            '/v1/runs' => ok({
              'runs': [
                {'id': 'RN1', 'workflow': 'Chase overdue invoices', 'status': 'failed', 'started_at': '2026-11-12T07:00:00Z'},
              ],
            }),
            '/v1/dashboard' => ok({
              'runs': {'total': 12, 'failed': 1},
            }),
            '/v1/approvals' => ok({
              'approvals': [_approval],
            }),
            '/v1/form-requests' => ok({'requests': []}),
            '/v1/workflows' => ok({
              'workflows': [
                {
                  'id': 'W1',
                  'name': 'Morning reminder',
                  'active_version': 3,
                  'trigger': {
                    'type': 'schedule',
                    'config': {'cron': '0 9 * * 1-5'},
                  },
                  'recent': [1, 1, 0],
                },
              ],
            }),
            '/v1/onboarding' => ok({
              'items': [
                {'id': 'chat', 'done': true},
                {'id': 'invite', 'done': false},
              ],
              'done': 1,
              'total': 2,
              'complete': false,
              'dismissed': false,
              'self_serve': true,
            }),
            _ => null,
          };
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: TodayTab(auth: chats.auth, now: () => _now),
      ),
    );
    if (settled) await settle(t);
    return asked;
  }

  testWidgets('film: pay a supplier', (t) async {
    final keys = _FilmPasskeys();
    final was = passkeys;
    passkeys = keys;
    addTearDown(() => passkeys = was);
    await open(t, settled: false);
    final f = Film.start(t, 'pay-a-supplier');
    f.say('Ada needs to pay Bayo Rice Mills for the rice.', who: 'Ada · Ada\'s Kitchen');
    await f.play(2.0);
    await f.settle();
    await f.hold(0.5);

    f.say('On Today, scroll to the menu and tap Money.');
    final menu = find.byKey(const ValueKey('today-menu'));
    await f.drag(find.text('What would you like to do?'), const Offset(0, -300), seconds: 0.8);
    await f.hold(0.5);
    await f.tap(find.descendant(of: menu, matching: find.text('Money')), then: 1.3);
    await f.settle();

    f.say('Tap Pay someone.');
    await f.hold(0.7);
    await f.tap(find.text('Pay someone'), then: 1.4);
    await f.settle();

    f.say('Pick who to pay from your saved accounts: @bayo.');
    await f.hold(0.8);
    await f.tap(find.text('@bayo'), then: 0.6);

    f.say('Type how much.');
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    await f.tap(boxes.at(1), then: 0.2);
    await f.keyboard(true);
    await f.type(boxes.at(1), '156000');
    await f.hold(0.6);
    await f.keyboard(false);

    f.say('Say what it is for: stock bought.');
    await t.ensureVisible(find.text('stock bought'));
    await t.pump();
    await f.hold(0.4);
    await f.tap(find.text('stock bought'), then: 0.9);

    f.say('Tap Next. Taskiem checks the account with the bank.');
    await t.ensureVisible(find.text('Next'));
    await t.pump();
    await f.hold(0.5);
    await f.tap(find.text('Next'), then: 1.4);
    await f.settle();

    f.say('The bank says who owns the account. It is the first payment to it, so Taskiem says so.');
    await f.hold(3.4);

    f.say('Check the name and the amount, then pay.');
    await f.hold(1.2);
    final gate = Completer<void>();
    keys.gate = gate;
    await f.tap(find.text('Pay ₦156,000'));
    f.system('biometric', 1.9, title: 'Confirm payment', line: 'Pay ₦156,000 to Bayo Rice Mills');
    f.say('Confirm it is you with your fingerprint.');
    await f.play(1.9);
    gate.complete();
    f.say('Sent to the bank. Bayo is paid in moments.');
    await f.play(3.6);
    await f.finish();
  }, skip: !filming);
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  // The figure counts up and the day's line grows in: let both finish.
  await t.pump(const Duration(milliseconds: 1000));
}
