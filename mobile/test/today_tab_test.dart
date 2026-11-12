// The home's Today tab (web Home.tsx, lib/home.ts): the day's money and
// orders, what needs the person (an approval approved here), the latest
// runs, the set-up left, the workflows running; each leading to its screen,
// the web's in the browser.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/approve.dart' show ApproveSheet;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/invoice_match.dart' show InvoiceMatchPage;
import 'package:taskiem/chat/today_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
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

void main() {
  setUpAll(loadFonts);

  test('the day in words', () {
    expect(greeting(_now), 'Good afternoon');
    expect(greeting(DateTime.utc(2026, 11, 12, 6)), 'Good morning');
    expect(todayWords(_now), 'Thursday 12 November');
    expect(shortWhen('2026-11-12T10:30:00Z', _now), '11:30');
    expect(shortWhen('2026-11-11T10:30:00Z', _now), 'Yesterday');
    expect(shortWhen('2026-11-01T10:30:00Z', _now), '1 Nov');
    final orders = [
      {'status': 'paid', 'paid_at': '2026-11-12T09:00:00Z', 'created_at': '2026-11-12T08:00:00Z', 'total_kobo': 500000},
      {'status': 'received', 'created_at': '2026-11-11T08:00:00Z', 'total_kobo': 200000},
    ];
    expect(moneyToday(orders, _now), (kobo: 500000, count: 1));
    expect(ordersToday(orders, _now), (count: 1, waiting: 1));
    expect(runWords('needs_reconciliation'), 'needs checking');
    expect(scheduleWords('0 9 * * 1-5'), 'Every weekday (Monday to Friday) at 09:00');
    expect(scheduleWords('30 10 * * 5'), 'Every Friday at 10:30');
    expect(scheduleWords('0 * * * *'), 'Every hour, on the hour');
    expect(
      triggerWords({
        'type': 'link',
        'config': {'event': 'order.received'},
      }),
      'A linked customer sends an order',
    );
    expect(triggerWords(null), 'Not set up yet');
  });

  Future<List<String>> open(WidgetTester t) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 3000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final asked = <String>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          asked.add('${req.method} ${req.url.path}${req.url.query.isEmpty ? '' : '?${req.url.query}'}');
          if (req.method != 'GET') return ok({'ok': true});
          return switch (req.url.path) {
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
    await settle(t);
    return asked;
  }

  testWidgets('the day: money, orders, what needs you, activity, set-up, workflows', (t) async {
    final opened = <String>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u.path);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    final asked = await open(t);
    await snap(t, 'today_tab-0');
    expect(asked, contains('GET /v1/money/summary?from=2026-11-12&to=2026-11-12'));
    expect(find.textContaining('Good afternoon'), findsOneWidget);
    expect(find.text('₦45,000'), findsOneWidget);
    expect(find.textContaining('3 payments'), findsOneWidget);
    expect(find.text('Orders today: 1'), findsOneWidget);
    expect(find.text('1 waiting for you'), findsOneWidget);
    expect(find.text('Jobs done this week: 12'), findsOneWidget);
    expect(find.text('1 needs a look'), findsOneWidget);
    expect(find.text('Pay Bayo Rice Mills ₦156,000'), findsOneWidget);
    expect(find.text('3'), findsOneWidget, reason: 'needs you: an approval, a payment to match, an order');
    expect(find.text('Match ₦7,000'), findsOneWidget);
    expect(find.text('Order from Tunde'), findsOneWidget);
    expect(find.text('Chase overdue invoices failed'), findsOneWidget);
    expect(find.text('Invite a teammate'), findsOneWidget);
    expect(find.text('Link WhatsApp or Telegram'), findsNothing, reason: 'done');
    expect(find.textContaining('Every weekday (Monday to Friday) at 09:00 · ran 2 times in 2 weeks'), findsOneWidget);
    expect(find.bySemanticsLabel('Back'), findsNothing, reason: 'a tab of its own');

    await t.tap(find.text('Chase overdue invoices failed'));
    await t.pump();
    expect(opened.last, '/runs/RN1');
    await t.tap(find.bySemanticsLabel('Approve Pay Bayo Rice Mills'));
    await t.pumpAndSettle();
    expect(find.byType(ApproveSheet), findsOneWidget);
    await t.tapAt(const Offset(20, 20));
    await t.pumpAndSettle();
    await t.tap(find.text('Match ₦7,000'));
    await t.pumpAndSettle();
    expect(find.byType(InvoiceMatchPage), findsOneWidget);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(also: (req) async => req.method == 'GET' && req.url.path == '/v1/onboarding' ? http.Response('{}', 404) : null),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: TodayTab(auth: chats.auth, now: () => _now),
      ),
      drive: settle,
    );
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
