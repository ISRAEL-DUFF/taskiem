// Your orders (M5 part O; web ChatBuyer.tsx PurchaseView): an order's
// state and steps, and what can be done now, each sending what it says.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/purchase_view.dart';
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

Map<String, dynamic> _order(Map<String, dynamic> over) => {
  'id': 'P1',
  'reference': 'PO-0007',
  'supplier': 'Bayo Rice Mills',
  'link_id': 'L1',
  'status': 'sent',
  'seller_status': '',
  'total_kobo': 9000000,
  'created_at': '2026-11-01T10:00:00Z',
  'lines': [
    {'item': 'Ofada rice', 'item_id': 'I1', 'quantity': 2, 'cost_kobo': 4500000},
  ],
  ...over,
};

void main() {
  setUpAll(loadFonts);

  test('what the buyer may do, by where the order is', () {
    expect(buyerSteps(_order({})).cancel, isTrue);
    expect(buyerSteps(_order({'status': 'drafted', 'seller_status': 'proposed'})).confirmProposed, isTrue);
    expect(buyerSteps(_order({'seller_status': 'changed', 'change': <Object>[]})).acceptChange, isTrue);
    expect(buyerSteps(_order({'seller_status': 'delivered'})).cancel, isFalse);
    expect(answersDelivery(_order({'seller_status': 'delivered'})), isTrue);
    expect(answersDelivery(_order({'seller_status': 'delivered', 'short_at': 'x'})), isFalse);
    expect(sameName([{'id': 'a', 'name': ' Ofada Rice '}], 'ofada rice'), 'a');
  });

  Future<({List<String> sent, List<String> said, List<Map<String, dynamic>> reorders})> open(WidgetTester t, Map<String, dynamic> p) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final said = <String>[];
    final reorders = <Map<String, dynamic>>[];
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET') {
          return req.url.path == '/v1/stock'
              ? ok({
                  'keeps_stock': true,
                  'items': [
                    {'id': 'I1', 'name': 'Ofada rice'},
                    {'id': 'I2', 'name': 'Beans'},
                  ],
                })
              : null;
        }
        sent.add('${req.method} ${req.url.path} ${req.body}');
        return ok({});
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: TkSheet(
          title: 'PO-0007',
          body: PurchaseView(auth: chats.auth, purchase: p, onSaved: said.add, onReorder: reorders.add),
        ),
      ),
    );
    await t.pumpAndSettle();
    return (sent: sent, said: said, reorders: reorders);
  }

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  testWidgets('sent: its steps; cancel asks first; reorder', (t) async {
    final r = await open(t, _order({}));
    expect(find.text('Sent'), findsOneWidget);
    expect(find.bySemanticsLabel('Placed: step 1 of 4'), findsOneWidget);
    expect(find.text('2 × Ofada rice'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Reorder'));
    expect(r.reorders.single['id'], 'P1');
    await t.tap(find.bySemanticsLabel('Cancel'));
    await t.pumpAndSettle();
    expect(find.text('Cancel PO-0007?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Cancel the order'));
    await settle(t);
    expect(r.sent.single, startsWith('POST /v1/stock/purchases/P1/cancel'));
    expect(r.said.single, 'Cancelled PO-0007');
  });

  testWidgets('their quote: which of ours each is, then confirmed', (t) async {
    final r = await open(t, _order({'status': 'drafted', 'seller_status': 'proposed'}));
    expect(find.text('Their quote: confirm it'), findsOneWidget);
    expect(find.bySemanticsLabel('Decline'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Confirm their quote'));
    await settle(t);
    // The same name, chosen already.
    expect(find.bySemanticsLabel('Their Ofada rice: Ofada rice'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Confirm the order'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/stock/purchases/P1/confirm-proposed {"items":["I1"]}');
  });

  testWidgets('delivered: all came, or how many did', (t) async {
    final r = await open(t, _order({'seller_status': 'delivered', 'delivered_at': '2026-11-03T09:00:00Z'}));
    expect(find.text('Delivered 3 Nov'), findsOneWidget);
    expect(find.bySemanticsLabel('Cancel'), findsNothing);
    expect(find.bySemanticsLabel('Reorder'), findsNothing);
    await t.tap(find.bySemanticsLabel('Not all came'));
    await t.pumpAndSettle();
    await t.enterText(find.bySemanticsLabel(RegExp('Ofada rice: of 2')), '1');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Tell Bayo Rice Mills'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/stock/purchases/P1/not-all-came {"counts":[1]}');
    expect(r.said.single, 'Told Bayo Rice Mills what came of PO-0007');
  });
}
