// A film: a customer's order from WhatsApp (Gadgets), for Techline Gadgets.
// Chiamaka Eze asks for one Nova X5, 128 GB, blue in a WhatsApp message; the
// order card shows the line (N185,000). Ikenna types the delivery fee, 2,000,
// saves it and taps Confirm (N187,000). Adapted from
// film_food_customer_order_test.dart. Skipped unless VIDEO_DIR is set.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/customer.dart' show CustomerData, CustomerScreen;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_tech_ep23_world.dart';
import 'support.dart';

// The fixture's customer chat (its id), shown here as Chiamaka Eze.
const _chioma = '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab';
const _said = 'Good afternoon, one Nova X5, 128 GB, blue, deliver to Allen Avenue, Ikeja';
const _address = 'Allen Avenue, Ikeja';

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
      'items_kobo': 18500000,
      'total_kobo': 18500000 + fee,
      'when': 'Today',
      'created_at': '2026-11-12T12:00:00Z',
      'updated_at': '2026-11-12T12:00:00Z',
      if (status == 'confirmed') 'reference': 'TG-1',
      if (status == 'confirmed') 'order_status': 'received',
      'lines': [
        {'id': 'L1', 'said': 'one Nova X5, 128 GB, blue', 'item_id': 'nv-128 GB-Blue', 'name': 'Nova X5 – 128 GB, Blue', 'quantity': 1, 'price_kobo': 18500000},
      ],
    };
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        final base = techBase(req, f);
        if (base != null) return base;
        if (req.method == 'GET' && p == '/v1/customer-chats/$_chioma') {
          return techOk({
            'id': _chioma,
            'name': 'Chiamaka Eze',
            'phone': '+2348031230142',
            'last': _said,
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
          return techOk({
            'messages': [
              {'id': 'm1', 'mine': false, 'kind': 'text', 'body': _said, 'read_at': null, 'created_at': '2026-11-12T12:00:00Z'},
            ],
            'more': false,
          });
        }
        if (req.method == 'GET' && p == '/v1/customer-chats/$_chioma/orders') {
          return techOk({
            'orders': [draft()],
            'zones': <Object>[],
            'pay_page': true,
            'items': [
              {'id': 'nv-128 GB-Blue', 'name': 'Nova X5 – 128 GB, Blue', 'price_kobo': 18500000},
              {'id': 'c1', 'name': 'Charger', 'price_kobo': 650000},
            ],
          });
        }
        if (req.method == 'GET') return null;
        sent.add('${req.method} $p ${req.body}');
        if (req.method == 'PUT' && p == '/v1/order-drafts/OD1') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          if (b['op'] == 'details' && b['delivery_kobo'] != null) fee = b['delivery_kobo'] as int;
          version++;
          return techOk({});
        }
        if (p == '/v1/order-drafts/OD1/confirm') {
          status = 'confirmed';
          return techOk({'told': true});
        }
        return techOk({});
      }),
      now: () => DateTime.parse('2026-11-12T12:03:00Z'),
    );
    techMe(chats.auth, f);
    chats.load();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: CustomerScreen(data: CustomerData(chats, _chioma, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'gadgets-customer-order');
    film.say('Chiamaka messages Techline on WhatsApp. Taskiem reads it into an order.', who: 'Ikenna · Techline Gadgets');
    await film.play(4.5);
    film.say('The card shows one Nova X5, 128 GB, Blue, at ₦185,000.');
    await film.hold(3.5);

    film.say('She wants it delivered. Ikenna types the delivery fee, 2,000.');
    final feeBox = find.descendant(of: find.bySemanticsLabel(RegExp('^Delivery fee')), matching: find.byType(EditableText));
    await t.ensureVisible(feeBox);
    await t.pump();
    await film.hold(0.8);
    await film.tap(feeBox, then: 0.2);
    await film.keyboard(true);
    await film.type(feeBox, '2000', perKey: 0.15);
    await film.hold(0.6);
    await film.keyboard(false);
    await film.hold(0.6);
    film.say('Then he taps Save the address and fee to keep it.');
    await film.tap(find.bySemanticsLabel('Save the address and fee'), then: 1.2);
    await film.settle();
    await film.hold(1.6);

    film.say('The total is now ₦187,000. He taps Confirm.');
    await t.ensureVisible(find.bySemanticsLabel('Confirm'));
    await t.pump();
    await film.hold(1.6);
    await film.tap(find.bySemanticsLabel('Confirm'), then: 1.0);
    await film.settle();
    film.say('The order is confirmed as TG-1 for ₦187,000.');
    await film.play(4.0);
    await film.finish();
    expect(sent.where((s) => s.startsWith('PUT')).toList(), [
      'PUT /v1/order-drafts/OD1 {"version":3,"op":"details","address":"$_address","delivery_kobo":200000}',
    ]);
  }, skip: !filming);
}
