// A film of the Today tab (video/recording-app.md): it opens, the day's
// figure counts up, a finger taps Sales, then a row. Set-up copied from
// today_tab_test.dart (the Today tab on recorded answers); the filming is
// film.dart's. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_today_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/today --out today.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/today_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
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

final _catalogue = {
  'topics': [
    _topic('sales', 'Sales', 'sell', 'lime', [('sales.today', "Today's sales", 'What came in today'), ('sales.week', 'How we did this week', 'Sales, money in and out')]),
    _topic('stock', 'Stock', 'stock', 'orange', [('stock.low', "See what's low", 'Items about to run out')]),
    _topic('products', 'Products', 'products', 'teal', [('products.list', 'Your products', 'What you sell')]),
    _topic('orders', 'Orders', 'orders', 'blue', [('orders.open', 'Open orders', 'Waiting for you')]),
    _topic('money', 'Money', 'money', 'lime', [('money.today', 'Money today', 'In and out')]),
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

  testWidgets('film: Today', (t) async {
    await open(t, settled: false);
    final f = Film.start(t, 'today');
    f.say('Every morning, Ada opens Taskiem on Today.', who: 'Ada · Ada\'s Kitchen');
    await f.play(2.0); // the screen rises in; the figure counts up
    await f.settle();
    await f.hold(0.8);
    f.say('Her business, at a glance: money in, orders and what needs her.');
    await f.hold(1.6);
    final menu = find.byKey(const ValueKey('today-menu'));
    f.say('What would she like to do? Tap Sales.');
    await f.tap(find.descendant(of: menu, matching: find.text('Sales')), then: 1.6);
    await f.settle();
    f.say("Tap Today's sales.");
    await f.hold(0.8);
    await f.tap(find.bySemanticsLabel(RegExp(r"^Today's sales\. ")), then: 2.0);
    await f.settle();
    f.say('Sales today: ₦45,000 from 3 sales.');
    await f.hold(2.2);
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
