// Sell and Handover (M6 part S; web ChatStaff.tsx): a counter sale picked,
// paid and recorded once however often it is tried; the shift handed over
// with the cash counted against the cash sold.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/sell_sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('a sale and the cash', () {
    final s = saleOf([
      {'id': 'a', 'price_kobo': 50000},
      {'id': 'b', 'price_kobo': 20000},
    ], {'a': 2, 'b': 1});
    expect(s.total, 120000);
    expect(s.count, '3 things');
    expect(cashWords(100, 100), 'As sold');
    expect(cashWords(5000, 10000), '₦50 short');
  });

  Future<({List<String> sent, List<String> keys})> open(WidgetTester t, Widget Function(ChatsData chats) sheet, {bool failOnce = false}) async {
    t.view.physicalSize = const Size(390, 1200);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final keys = <String>[];
    var fail = failOnce;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET') {
          return switch (p) {
            '/v1/stock' => ok({
              'keeps_stock': true,
              'items': [
                {'id': 'coke', 'name': 'Coke 50cl', 'price_kobo': 50000, 'available': true, 'stock': 1},
                {'id': 'water', 'name': 'Water', 'price_kobo': 20000, 'available': true, 'stock': null},
                {'id': 'free', 'name': 'Napkins', 'available': true, 'stock': 9},
              ],
            }),
            '/v1/chat-handover' => ok({
              'day': '2026-11-12',
              'sales': {
                'total_kobo': 1500000,
                'count': 4,
                'by_how': {'cash': 1000000, 'transfer': 500000},
                'waiting': 0,
              },
              'low': ['Coke 50cl'],
            }),
            _ => null,
          };
        }
        sent.add('${req.method} $p ${req.body}');
        keys.add(req.headers['Idempotency-Key'] ?? req.headers['idempotency-key'] ?? '');
        if (fail) {
          fail = false;
          return http.Response(jsonEncode({'error': 'Try again.'}), 500, headers: {'content-type': 'application/json'});
        }
        return ok({});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: sheet(chats)));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
    return (sent: sent, keys: keys);
  }

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
  }

  testWidgets('sell: only priced things; over what is left said; paid by transfer; tried twice, recorded once', (t) async {
    final r = await open(t, (chats) => SellSheet(auth: chats.auth), failOnce: true);
    expect(find.text('Napkins'), findsNothing, reason: 'no price');
    await t.tap(find.bySemanticsLabel('One more Coke 50cl'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('One more Coke 50cl'));
    await t.pump();
    expect(find.text('Only 1 left in your stock'), findsOneWidget);
    expect(find.text('2 things'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Next'));
    await t.pumpAndSettle();
    expect(find.text('How was it paid?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Transfer'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record the sale'));
    await settle(t);
    expect(find.text('Try again.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Record the sale'));
    await settle(t);
    expect(r.sent, List.filled(2, 'POST /v1/stock/sales {"lines":[{"item_id":"coke","quantity":2}],"paid_how":"transfer"}'));
    expect(r.keys[0], r.keys[1], reason: 'the same sale, the same key');
    expect(find.text('Sold: ₦1,000'), findsOneWidget);
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('handover: what was sold; the cash counted against it; sent', (t) async {
    final r = await open(t, (chats) => HandoverSheet(auth: chats.auth));
    expect(find.text('₦15,000'), findsOneWidget);
    expect(find.text('4 sales'), findsOneWidget);
    expect(find.text('Coke 50cl'), findsOneWidget);
    await t.enterText(find.byType(EditableText).first, '9,500');
    await t.pump();
    expect(find.text('₦500 short · ₦10,000 sold in cash'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Hand over'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/chat-handover {"cash_counted_kobo":950000,"note":""}');
    await t.pump(const Duration(seconds: 2));
  });
}
