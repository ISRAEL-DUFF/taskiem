// A film: a customer's order from WhatsApp (episode 3b). Nneka Eze asks for an
// Ankara dress in a WhatsApp message; Taskiem reads it into an order card
// under the conversation. Zainab adds one more dress, types a delivery fee and
// taps Confirm. Set-up copied from customer_orders_test.dart (the card, its
// answers) and customer_test.dart (the customer's conversation). Skipped
// unless VIDEO_DIR is set.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/customer.dart' show CustomerData, CustomerScreen;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_zuri_shop_world.dart';
import 'support.dart';

const _nneka = '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: a customer order', (t) async {
    final f = Fixture('customer-chidi');
    var version = 3;
    var qty = 1;
    var fee = 0;
    var status = 'open';
    Map<String, dynamic> draft() => {
      'id': 'OD1',
      'chat_id': _nneka,
      'status': status,
      'by_reader': true,
      'version': version,
      'questions': 0,
      'ready': true,
      'waits': null,
      'fulfilment': 'delivery',
      'address': '14 Adeniran Ogunsanya Street, Surulere',
      'delivery_kobo': fee,
      'items_kobo': qty * 2800000,
      'total_kobo': qty * 2800000 + fee,
      'when': 'Saturday',
      'created_at': '2026-11-12T10:00:00Z',
      'updated_at': '2026-11-12T10:00:00Z',
      if (status == 'confirmed') 'reference': 'ZA-1',
      if (status == 'confirmed') 'order_status': 'received',
      'lines': [
        {'id': 'L1', 'said': 'one ankara dress, size 12, gold', 'item_id': 'dress1', 'name': 'Ankara dress (12, Gold)', 'quantity': qty, 'price_kobo': 2800000},
      ],
    };
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        final base = zuriBase(req, f);
        if (base != null) return base;
        if (req.method == 'GET' && p == '/v1/customer-chats/$_nneka') {
          return okJson({
            'id': _nneka,
            'name': 'Nneka Eze',
            'phone': '+2348031230142',
            'last': 'Saturday please. 14 Adeniran Ogunsanya Street, Surulere',
            'last_mine': false,
            'last_at': '2026-11-12T10:00:00Z',
            'unread': 0,
            'window_open': true,
            'window_until': '2026-11-13T10:00:00Z',
            'stopped': false,
            'archived': false,
            'owes_kobo': 0,
            'open_invoices': 0,
          });
        }
        if (req.method == 'GET' && p == '/v1/customer-chats/$_nneka/messages') {
          return okJson({
            'messages': [
              {'id': 'm1', 'mine': false, 'kind': 'text', 'body': 'Good afternoon. Please I want one Ankara dress, size 12, Gold.', 'read_at': null, 'created_at': '2026-11-12T09:58:00Z'},
              {'id': 'm2', 'mine': true, 'kind': 'text', 'body': 'Good afternoon Nneka! Yes, it is ₦28,000. Where should we send it?', 'sender_name': 'Zainab', 'sent_as': 'text', 'delivery': 'read', 'read_at': null, 'created_at': '2026-11-12T09:59:00Z'},
              {'id': 'm3', 'mine': false, 'kind': 'text', 'body': 'Saturday please. 14 Adeniran Ogunsanya Street, Surulere', 'read_at': null, 'created_at': '2026-11-12T10:00:00Z'},
            ],
            'more': false,
          });
        }
        if (req.method == 'GET' && p == '/v1/customer-chats/$_nneka/orders') {
          return okJson({
            'orders': [draft()],
            'zones': <Object>[],
            'pay_page': true,
            'items': [
              {'id': 'dress1', 'name': 'Ankara dress', 'price_kobo': 2800000},
            ],
          });
        }
        if (req.method == 'GET') return null;
        sent.add('${req.method} $p ${req.body}');
        if (req.method == 'PUT' && p == '/v1/order-drafts/OD1') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          if (b['op'] == 'quantity') qty = b['quantity'] as int;
          if (b['op'] == 'details' && b['delivery_kobo'] != null) fee = b['delivery_kobo'] as int;
          version++;
          return okJson({});
        }
        if (p == '/v1/order-drafts/OD1/confirm') {
          status = 'confirmed';
          return okJson({'told': true});
        }
        return okJson({});
      }),
      now: () => f.at,
    );
    zuriMe(chats.auth, f);
    chats.load();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: CustomerScreen(data: CustomerData(chats, _nneka, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'customer-order');
    film.say('Nneka messages on WhatsApp. Taskiem reads it into an order.', who: 'Zainab · Zuri Atelier');
    await film.play(4.5);
    film.say('The order card shows one Ankara dress, size 12, Gold, and the total.');
    await film.hold(3.5);

    film.say('Nneka wants two. Zainab taps One more.');
    await film.tap(find.bySemanticsLabel('One more Ankara dress (12, Gold)'), then: 1.2);
    await film.settle();
    await film.hold(1.0);

    film.say('She types the delivery fee, 2,000, then saves it.');
    final feeBox = find.descendant(of: find.bySemanticsLabel(RegExp('^Delivery fee')), matching: find.byType(EditableText));
    await t.ensureVisible(feeBox);
    await t.pump();
    await film.hold(0.5);
    await film.tap(feeBox, then: 0.2);
    await film.keyboard(true);
    await film.type(feeBox, '2000', perKey: 0.15);
    await film.hold(0.4);
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Save the address and fee'), then: 1.2);
    await film.settle();
    await film.hold(1.0);

    film.say('The total is now ₦58,000. She taps Confirm.');
    await t.ensureVisible(find.bySemanticsLabel('Confirm'));
    await t.pump();
    await film.hold(1.2);
    await film.tap(find.bySemanticsLabel('Confirm'), then: 1.0);
    await film.settle();
    film.say('The order is confirmed as ZA-1 for ₦58,000.');
    await film.play(4.0);
    await film.finish();
    expect(sent.where((s) => s.startsWith('PUT')).toList(), [
      'PUT /v1/order-drafts/OD1 {"version":3,"op":"quantity","line":0,"quantity":2}',
      'PUT /v1/order-drafts/OD1 {"version":4,"op":"details","address":"14 Adeniran Ogunsanya Street, Surulere","delivery_kobo":200000}',
    ]);
  }, skip: !filming);
}
