// A film: a customer's order from WhatsApp (episode 3b), for Ada's Kitchen.
// Chioma Nnadi asks for two jollof rice and a zobo in a WhatsApp message;
// Taskiem reads it into an order card under the conversation (N8,500). Ada
// types the delivery fee, 1,500, saves it and taps Confirm (N10,000). Adapted
// from film_customer_order_test.dart; set-up from customer_orders_test.dart
// (the card, its answers) and customer_test.dart (the customer's
// conversation). Skipped unless VIDEO_DIR is set.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/customer.dart' show CustomerData, CustomerScreen;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_ada_ep23_world.dart';
import 'support.dart';

// The fixture's customer chat (its id), shown here as Chioma Nnadi.
const _chioma = '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab';
const _address = '12 Herbert Macaulay Way, Yaba';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: a customer order', (t) async {
    final f = Fixture('customer-chidi');
    var version = 3;
    var fee = 0;
    var status = 'open';
    Map<String, dynamic> draft() => {
      'id': 'OD1',
      'chat_id': _chioma,
      'status': status,
      'by_reader': true,
      'version': version,
      'questions': 0,
      'ready': true,
      'waits': null,
      'fulfilment': 'delivery',
      'address': _address,
      'delivery_kobo': fee,
      'items_kobo': 850000,
      'total_kobo': 850000 + fee,
      'when': 'Today',
      'created_at': '2026-11-12T12:00:00Z',
      'updated_at': '2026-11-12T12:00:00Z',
      if (status == 'confirmed') 'reference': 'AK-1',
      if (status == 'confirmed') 'order_status': 'received',
      'lines': [
        {'id': 'L1', 'said': '2 jollof rice', 'item_id': '1', 'name': 'Jollof rice', 'quantity': 2, 'price_kobo': 350000},
        {'id': 'L2', 'said': '1 zobo', 'item_id': '27', 'name': 'Zobo', 'quantity': 1, 'price_kobo': 150000},
      ],
    };
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        final base = adaBase(req, f);
        if (base != null) return base;
        if (req.method == 'GET' && p == '/v1/customer-chats/$_chioma') {
          return adaOk({
            'id': _chioma,
            'name': 'Chioma Nnadi',
            'phone': '+2348031230142',
            'last': 'Good afternoon, 2 jollof rice and 1 zobo, deliver to $_address',
            'last_mine': false,
            'last_at': '2026-11-12T12:00:00Z',
            'unread': 0,
            'window_open': true,
            'window_until': '2026-11-13T12:00:00Z',
            'stopped': false,
            'archived': false,
            'owes_kobo': 0,
            'open_invoices': 0,
          });
        }
        if (req.method == 'GET' && p == '/v1/customer-chats/$_chioma/messages') {
          return adaOk({
            'messages': [
              {'id': 'm1', 'mine': false, 'kind': 'text', 'body': 'Good afternoon, 2 jollof rice and 1 zobo, deliver to $_address', 'read_at': null, 'created_at': '2026-11-12T12:00:00Z'},
            ],
            'more': false,
          });
        }
        if (req.method == 'GET' && p == '/v1/customer-chats/$_chioma/orders') {
          return adaOk({
            'orders': [draft()],
            'zones': <Object>[],
            'pay_page': true,
            'items': [
              {'id': '1', 'name': 'Jollof rice', 'price_kobo': 350000},
              {'id': '27', 'name': 'Zobo', 'price_kobo': 150000},
            ],
          });
        }
        if (req.method == 'GET') return null;
        sent.add('${req.method} $p ${req.body}');
        if (req.method == 'PUT' && p == '/v1/order-drafts/OD1') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          if (b['op'] == 'details' && b['delivery_kobo'] != null) fee = b['delivery_kobo'] as int;
          version++;
          return adaOk({});
        }
        if (p == '/v1/order-drafts/OD1/confirm') {
          status = 'confirmed';
          return adaOk({'told': true});
        }
        return adaOk({});
      }),
      now: () => DateTime.parse('2026-11-12T12:03:00Z'),
    );
    adaMe(chats.auth, f);
    chats.load();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: CustomerScreen(data: CustomerData(chats, _chioma, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'food-customer-order');
    film.say('Chioma messages Ada on WhatsApp. Taskiem reads it into an order.', who: "Ada · Ada's Kitchen");
    await film.play(4.5);
    film.say('The card shows two jollof rice, one zobo and the total, ₦8,500.');
    await film.hold(3.5);

    film.say('Chioma wants it delivered. Ada types the delivery fee, 1,500.');
    final feeBox = find.descendant(of: find.bySemanticsLabel(RegExp('^Delivery fee')), matching: find.byType(EditableText));
    await t.ensureVisible(feeBox);
    await t.pump();
    await film.hold(0.8);
    await film.tap(feeBox, then: 0.2);
    await film.keyboard(true);
    await film.type(feeBox, '1500', perKey: 0.15);
    await film.hold(0.6);
    await film.keyboard(false);
    await film.hold(0.6);
    film.say('Then she taps Save the address and fee to keep it.');
    await film.tap(find.bySemanticsLabel('Save the address and fee'), then: 1.2);
    await film.settle();
    await film.hold(1.6);

    film.say('The total is now ₦10,000. She taps Confirm.');
    await t.ensureVisible(find.bySemanticsLabel('Confirm'));
    await t.pump();
    await film.hold(1.6);
    await film.tap(find.bySemanticsLabel('Confirm'), then: 1.0);
    await film.settle();
    film.say('The order is confirmed as AK-1 for ₦10,000.');
    await film.play(4.0);
    await film.finish();
    expect(sent.where((s) => s.startsWith('PUT')).toList(), [
      'PUT /v1/order-drafts/OD1 {"version":3,"op":"details","address":"$_address","delivery_kobo":150000}',
    ]);
  }, skip: !filming);
}
