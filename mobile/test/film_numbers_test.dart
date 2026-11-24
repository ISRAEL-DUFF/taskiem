// A film of Today as the owner's morning (episode 9, "Know your numbers"):
// Zainab opens Today at Zuri Atelier: money in so far against a usual
// Thursday, sales by the hour, orders, what needs her; then asks how the
// week went from the Sales menu. Set-up copied from today_tab_test.dart and
// film_today_test.dart; the story's names and amounts replace the fixture's.
// Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_numbers_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/know-your-numbers --out numbers.mp4 --captions --time 10:30

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/today_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

// Thursday 12 November 2026, 10:30 in Lagos.
final _now = DateTime.utc(2026, 11, 12, 9, 30);

final _approval = {
  'run_id': 'R1',
  'step_id': 'pay',
  'step_name': 'Pay Alhaja Sade',
  'workflow': 'Pay suppliers',
  'environment': 'prod',
  'requested_at': '2026-11-12T08:30:00Z',
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

  Future<void> open(WidgetTester t) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          if (req.method == 'POST' && req.url.path == '/v1/chat/actions/sales.week/run') {
            return http.Response.bytes(
              utf8.encode(jsonEncode({
                'messages': [
                  {'id': 'h1', 'channel': 'web', 'inbound': true, 'kind': 'text', 'text': 'How we did this week', 'created_at': '2026-11-12T09:30:00Z'},
                  {'id': 'h2', 'channel': 'web', 'inbound': false, 'kind': 'text', 'text': 'Sales this week: ₦2,871,000, up 17% on the week before.', 'created_at': '2026-11-12T09:30:00Z'},
                ],
              })),
              200,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          if (req.method != 'GET') return ok({'ok': true});
          return switch (req.url.path) {
            '/v1/chat/actions' => ok(_catalogue),
            // The same weekday in the weeks before: ₦25,000 by 9 am.
            '/v1/money/summary' when req.url.queryParameters['from'] != '2026-11-12' => ok({
              'in_settled_kobo': 2500000,
              'out_settled_kobo': 0,
              'buckets': [
                {'from': '${req.url.queryParameters['from']}T08:00:00Z', 'in_settled_kobo': 2500000, 'out_settled_kobo': 0, 'payments': 2},
              ],
            }),
            '/v1/money/summary' => ok({
              'in_settled_kobo': 9800000,
              'out_settled_kobo': 0,
              'buckets': [
                {'from': '2026-11-12T07:00:00Z', 'in_settled_kobo': 3800000, 'out_settled_kobo': 0, 'payments': 2},
                {'from': '2026-11-12T08:00:00Z', 'in_settled_kobo': 6000000, 'out_settled_kobo': 0, 'payments': 3},
              ],
            }),
            '/v1/money/unmatched' => ok({
              'payments': [
                {'entry_id': 'E1', 'amount_kobo': 3500000, 'channel': 'paystack/main', 'received_at': '2026-11-12T08:40:00Z'},
              ],
            }),
            '/v1/storefront/orders' => ok({
              'orders': [
                {'reference': 'ZA-14', 'status': 'received', 'customer': {'name': 'Nneka Eze'}, 'total_kobo': 4200000, 'created_at': '2026-11-12T08:50:00Z'},
                {'reference': 'ZA-13', 'status': 'paid', 'customer': {'name': 'Ada Obi'}, 'total_kobo': 5800000, 'created_at': '2026-11-12T07:20:00Z', 'paid_at': '2026-11-12T07:30:00Z'},
              ],
            }),
            '/v1/runs' => ok({
              'runs': [
                {'id': 'RN1', 'workflow': 'Chase overdue invoices', 'status': 'failed', 'started_at': '2026-11-12T06:00:00Z'},
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
            '/v1/onboarding' => ok({'items': <Object>[], 'done': 2, 'total': 2, 'complete': true, 'dismissed': true, 'self_serve': true}),
            _ => null,
          };
        },
      ),
      now: () => f.at,
    );
    final me = Map<String, dynamic>.from(f.json('/v1/me'));
    me['tenant_name'] = 'Zuri Atelier';
    me['user'] = {...(me['user'] as Map<String, dynamic>), 'name': 'Zainab Yusuf'};
    chats.auth.me = Me.fromJson(me);
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: TodayTab(auth: chats.auth, now: () => _now),
      ),
    );
  }

  testWidgets('film: know your numbers', (t) async {
    await open(t);
    final f = Film.start(t, 'know-your-numbers');
    f.say('Every morning, Zainab opens Today to see her business.', who: 'Zainab · Zuri Atelier');
    await f.play(2.2); // the screen rises in; the figure counts up
    await f.settle();
    await f.hold(1.2);
    f.say('Money in so far today, against a usual Thursday.');
    await f.hold(3.0);

    f.say('Sales by the hour show when the money came in.');
    await f.drag(find.byType(TodayTab), const Offset(0, -330), seconds: 0.8);
    await f.hold(3.0);

    f.say('Orders today, and how many are waiting for her.');
    await f.hold(3.4);

    f.say('Under Needs you: a payment to approve, and one to match.');
    await f.drag(find.byType(TodayTab), const Offset(0, -300), seconds: 0.8);
    await f.hold(3.4);

    final menu = find.byKey(const ValueKey('today-menu'));
    f.say('How did the week go? She taps Sales in the menu.');
    await t.ensureVisible(find.descendant(of: menu, matching: find.text('Sales')));
    await t.pump();
    await f.hold(0.8);
    await f.tap(find.descendant(of: menu, matching: find.text('Sales')), then: 1.6);
    await f.settle();
    f.say('Then How we did this week.');
    await f.hold(0.8);
    await f.tap(find.bySemanticsLabel(RegExp(r'^How we did this week\. ')), then: 2.0);
    await f.settle();
    f.say('Taskiem answers: ₦2,871,000 this week, up 17%.');
    await f.hold(3.4);
    await f.finish();
  }, skip: !filming);
}
