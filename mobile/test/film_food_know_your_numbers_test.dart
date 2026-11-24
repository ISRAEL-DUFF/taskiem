// A film of the morning briefing (Food episode 9, "Know your numbers"): at 8am
// Ada opens Taskiem and its briefing card is there: yesterday's sales, what
// is owed, what is low, the day's deliveries; she puts it away and types
// "briefing" to bring it back; then she opens Today for the day so far.
// Set-up copied from taskiem_test.dart (the briefing card, "Got it", and
// "briefing" typed), film_reorder_test.dart (Taskiem's conversation) and
// today_tab_test.dart (Today); the story's numbers replace the fixture's
// briefing. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_food_know_your_numbers_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/food-know-your-numbers --out numbers.mp4 --captions --time 08:00

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/chat/today_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

// Thursday 12 November 2026, 08:00 in Lagos.
final _now = DateTime.utc(2026, 11, 12, 7);

http.Response _ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});

/// The briefing as the server sends it at 8am: yesterday (Wednesday) ₦486,500,
/// up 12% on the Wednesday before; owed ₦212,000; 3 low; 4 deliveries.
/// The other six days' bars are the app's test fixture's.
final _briefing = {
  'ready': true,
  'day': '2026-11-12',
  'key': 'briefing:2026-11-12',
  'put_off': false,
  'part': 'morning',
  'now': '08:00',
  'sales': {
    'yesterday_kobo': 48650000,
    'change_pct': 12,
    'weekday': 'Wednesday',
    'week': [
      {'day': '2026-11-05', 'label': 'Thu', 'kobo': 31250000},
      {'day': '2026-11-06', 'label': 'Fri', 'kobo': 40500000},
      {'day': '2026-11-07', 'label': 'Sat', 'kobo': 29800000},
      {'day': '2026-11-08', 'label': 'Sun', 'kobo': 52050000},
      {'day': '2026-11-09', 'label': 'Mon', 'kobo': 44100000},
      {'day': '2026-11-10', 'label': 'Tue', 'kobo': 38950000},
      {'day': '2026-11-11', 'label': 'Wed', 'kobo': 48650000},
    ],
  },
  'owed_kobo': 21200000,
  'low': 3,
  'deliveries': 4,
  'bookings': 0,
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

Future<void> _load(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}

void main() {
  setUpAll(loadFonts);

  /// Ada's Taskiem conversation, the briefing on it.
  Future<void> taskiem(WidgetTester t) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          if (req.method == 'POST' && req.url.path == '/v1/chat-alerts/later') return _ok({});
          if (req.method != 'GET') return null;
          return switch (req.url.path) {
            '/v1/taskiem-chat' => _ok({'messages': <Object>[]}),
            '/v1/chat-briefing' => _ok(_briefing),
            '/v1/chat-alerts' => _ok({'alerts': <Object>[]}),
            _ => null,
          };
        },
      ),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await _load(t);
  }

  /// Ada's Today, early on: a little in so far, nothing else waiting.
  Future<void> today(WidgetTester t) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          if (req.method != 'GET') return _ok({'ok': true});
          return switch (req.url.path) {
            '/v1/chat/actions' => _ok(_catalogue),
            // The same weekday in the weeks before.
            '/v1/money/summary' when req.url.queryParameters['from'] != '2026-11-12' => _ok({
              'in_settled_kobo': 2500000,
              'out_settled_kobo': 0,
              'buckets': [
                {'from': '${req.url.queryParameters['from']}T06:00:00Z', 'in_settled_kobo': 2500000, 'out_settled_kobo': 0, 'payments': 2},
              ],
            }),
            '/v1/money/summary' => _ok({
              'in_settled_kobo': 3800000,
              'out_settled_kobo': 0,
              'buckets': [
                {'from': '2026-11-12T06:00:00Z', 'in_settled_kobo': 3800000, 'out_settled_kobo': 0, 'payments': 2},
              ],
            }),
            '/v1/money/unmatched' => _ok({'payments': <Object>[]}),
            '/v1/storefront/orders' => _ok({'orders': <Object>[]}),
            '/v1/runs' => _ok({'runs': <Object>[]}),
            '/v1/dashboard' => _ok({'runs': {'total': 0, 'failed': 0}}),
            '/v1/approvals' => _ok({'approvals': <Object>[]}),
            '/v1/form-requests' => _ok({'requests': <Object>[]}),
            '/v1/workflows' => _ok({'workflows': <Object>[]}),
            '/v1/onboarding' => _ok({'items': <Object>[], 'done': 2, 'total': 2, 'complete': true, 'dismissed': true, 'self_serve': true}),
            _ => null,
          };
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TodayTab(auth: chats.auth, now: () => _now)));
  }

  testWidgets('film: know your numbers', (t) async {
    await taskiem(t);
    final f = Film.start(t, 'food-know-your-numbers');
    f.say('At 8am, Ada opens Taskiem and her morning briefing is there.', who: "Ada · Ada's Kitchen");
    await f.hold(3.6);
    f.say("Yesterday's sales, with the week's bars and the change.");
    await f.hold(3.4);
    f.say('Tiles show what she is owed, what is low, deliveries today.');
    await f.hold(3.6);

    final gotIt = find.bySemanticsLabel('Got it: put the briefing away for today');
    f.say('She taps Got it, and the briefing is put away for today.');
    await t.ensureVisible(gotIt);
    await t.pump();
    await f.hold(1.2);
    await f.tap(gotIt, then: 0.4);
    await _load(t);
    await f.hold(2.0);

    f.say('Later she types briefing, and the card comes back.');
    await f.hold(0.8);
    await f.tap(find.byType(EditableText), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(find.byType(EditableText), 'briefing');
    await f.hold(0.6);
    await f.tap(find.bySemanticsLabel('Send'), then: 0.3);
    await f.keyboard(false);
    await _load(t);
    await f.hold(2.4);

    // Today, the day so far.
    await today(t);
    f.say('Today shows the day so far, against a usual Thursday.');
    await f.play(2.2);
    await _load(t);
    await f.hold(1.6);
    await f.finish();
  }, skip: !filming);
}
