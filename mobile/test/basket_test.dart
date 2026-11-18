// Ordering together in the app (docs/spec/people-chatting.md §10.3, §11 A
// and C; people-chatting-5b.md C with C1, C2, C3; "As built: stage 5b,
// ordering together"): the basket's words, what each may do, the shop's
// sizes as lines, "Order together" in the + menu, and the basket's sheet:
// adding your items, closing, placing with who pays, sending share cards.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/basket.dart';
import 'package:taskiem/chat/people_cards.dart' show PeopleShare, showPeopleShareMenu;
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'my_chats_test.dart' show Api, answer, drain, phone, press, settle;
import 'support.dart' show loadFonts;

Map<String, dynamic> line(String id, String item, String name, int q, int price) => {
  'id': id, 'item_id': item, 'name': name, 'quantity': q, 'price_kobo': price, 'added_price_kobo': price, 'price_changed': false,
  'line_kobo': q * price, 'available': true,
};

Map<String, dynamic> basketJson({String state = 'open', bool youHold = true, bool each = false, bool mine = true, String? payment, Map<String, dynamic>? order, bool shares = false}) {
  Map<String, dynamic>? share(int items) => shares ? {'items_kobo': items, 'delivery_kobo': 0, 'amount_kobo': items, 'paid': false, 'card_sent': false} : null;
  return {
    'id': 'b1', 'business_id': 'biz', 'business': 'Mama’s Kitchen', 'where': 'group', 'group_id': 'g1', 'state': state,
    'starter_id': 'ada', 'holder_id': youHold ? 'ada' : 'bola', 'holder': youHold ? 'You' : 'Bola Ade', 'you_hold': youHold, 'holder_gone': false,
    'shop': {'open': true, 'pickup': true, 'delivery': false, 'zones': <Object>[], 'payment': each ? 'at_order' : 'none', 'each_offered': each},
    'people': [
      if (mine) {'person_id': 'ada', 'name': 'You', 'you': true, 'items_kobo': 450000, 'lines': [line('l1', '1', 'Jollof rice', 1, 450000)], 'share': ?share(450000)},
      {'person_id': 'bola', 'name': 'Bola Ade', 'you': false, 'items_kobo': 300000, 'lines': [line('l2', '2', 'Chapman', 2, 150000)], 'share': ?share(300000)},
    ],
    'items_kobo': mine ? 750000 : 300000, 'delivery_kobo': 0, 'total_kobo': mine ? 750000 : 300000, 'problems': <Object>[],
    'payment': ?payment, 'order': ?order, if (state == 'placed') 'placed_by': 'ada', 'created_at': '2026-11-18T10:00:00Z',
  };
}

Basket basket({String state = 'open', bool youHold = true, bool each = false, String? payment, Map<String, dynamic>? order, bool shares = false}) =>
    Basket.fromJson(basketJson(state: state, youHold: youHold, each: each, payment: payment, order: order, shares: shares));

final placedOrder = {
  'reference': 'MA-1', 'status': 'awaiting_payment', 'line': 'Waiting for payment', 'total_kobo': 750000, 'paid_kobo': 0, 'left_kobo': 750000,
  'payment_state': 'unpaid', 'payable_now': true,
};

void main() {
  setUpAll(loadFonts);

  test('the words: state, holder, share, who pays', () {
    expect(basketStateWords(basket()), ('Open: add what you want', CardTone.lime));
    expect(basketStateWords(basket(state: 'closed')).$2, CardTone.warn);
    expect(basketStateWords(basket(state: 'placed', order: placedOrder)).$1, 'Waiting for payment');
    expect(basketHolderWords(basket()), contains('You hold this basket'));
    expect(basketHolderWords(basket(youHold: false)), 'Bola Ade holds this basket: they close it and place the order.');
    expect(basketHolderWords(basket(state: 'placed', order: placedOrder)), '');
    expect(basketShareWords(BasketShare.fromJson({'items_kobo': 300000, 'delivery_kobo': 33333, 'amount_kobo': 333333, 'paid': false, 'card_sent': false})),
        '₦3,333.33: items ₦3,000 and delivery ₦333.33');
    expect(basketPayNote(false, 'Mama’s Kitchen'), contains('takes no payment in the app, so you pay for everyone'));
    expect(basketPayNote(true, 'Mama’s Kitchen'), contains('equal part of the delivery'));
  });

  test('what each may do (C1, C2)', () {
    final open = BasketCan(basket());
    expect((open.addMine, open.close, open.holder, open.place), (true, true, true, false));
    final member = BasketCan(basket(youHold: false));
    expect((member.addMine, member.close, member.holder), (true, false, false));
    final closed = BasketCan(basket(state: 'closed'));
    expect((closed.place, closed.reopen, closed.addMine), (true, true, false));
    final each = BasketCan(basket(state: 'placed', payment: 'each', order: placedOrder, shares: true));
    expect((each.payMine, each.payOrder, each.shareCards), (true, false, false));
    expect(each.payFor.map((p) => p.personId), ['bola']);
    final all = BasketCan(basket(state: 'placed', payment: 'all', order: placedOrder, shares: true));
    expect((all.payOrder, all.shareCards, all.payMine), (true, true, false));
  });

  test('a shop’s sizes are lines of their own', () {
    final picks = basketPicks([
      ShopItem.fromJson({'id': '1', 'name': 'Jollof rice', 'price_kobo': 450000, 'available': true}),
      ShopItem.fromJson({
        'id': '9', 'name': 'Shirt', 'price_kobo': 800000, 'available': true,
        'sizes': [
          {'id': '9a', 'variant': {'colour': 'Red', 'size': '42'}, 'available': true},
          {'id': '9b', 'variant': {'colour': 'Blue'}, 'price_kobo': 900000, 'available': false},
        ],
      }),
    ]);
    expect(picks.map((p) => (p.id, p.name, p.price, p.available)), [
      ('1', 'Jollof rice', 450000, true),
      ('9a', 'Shirt (Red, 42)', 800000, true),
      ('9b', 'Shirt (Blue)', 900000, false),
    ]);
  });

  testWidgets('“Order together” is in the + menu only where it may be', (t) async {
    phone(t);
    PeopleShare? got;
    var together = true;
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: Builder(builder: (c) => GestureDetector(onTap: () async => got = await showPeopleShareMenu(c, together: together), child: const Text('Open'))),
    ));
    await press(t, find.text('Open'));
    expect(find.text('Order together'), findsOneWidget);
    await press(t, find.text('Order together'));
    expect(got, PeopleShare.together);
    together = false;
    await press(t, find.text('Open'));
    expect(find.text('Order together'), findsNothing);
    expect(find.text('Your share'), findsOneWidget);
  });

  testWidgets('the basket: add my items, close, place paying all, send share cards', (t) async {
    phone(t);
    final api = Api();
    var b = basketJson(mine: false);
    api.routes['GET /v1/me/baskets/b1'] = (_) => b;
    api.routes['GET /v1/me/baskets/b1/shop'] = (_) => {
      'open': true, 'payment': 'none', 'pickup': true, 'delivery': false, 'zones': <Object>[],
      'items': [
        {'id': '1', 'name': 'Jollof rice', 'price_kobo': 450000, 'available': true},
        {'id': '2', 'name': 'Chapman', 'price_kobo': 150000, 'available': true},
      ],
    };
    api.routes['PUT /v1/me/baskets/b1/mine'] = (_) => b = basketJson();
    api.routes['POST /v1/me/baskets/b1/close'] = (_) => b = basketJson(state: 'closed');
    api.routes['POST /v1/me/baskets/b1/place'] = (_) =>
        {'basket': b = basketJson(state: 'placed', payment: 'all', order: placedOrder, shares: true), 'order': {'reference': 'MA-1'}};
    api.routes['POST /v1/me/baskets/b1/share-cards'] = (r) => api.body(r)['pay_to'] == null
        ? answer({'error': 'Add the bank account to be paid to (pay_to), or save yours first.'}, 400)
        : {'sent': ['bola'], 'failed': <Object>[], 'basket': b};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: BasketSheet(auth: api.auth(), id: 'b1', every: const Duration(hours: 1))));
    await settle(t);
    expect(find.text('Order together from Mama’s Kitchen'), findsOneWidget);
    expect(find.text('Bola Ade'), findsWidgets);
    expect(find.textContaining('2 × Chapman'), findsOneWidget);

    // Add my items: one Jollof rice.
    await press(t, find.text('Add my items'));
    expect(find.text('Jollof rice'), findsOneWidget);
    await press(t, find.bySemanticsLabel('One more').first);
    await press(t, find.text('Put my 1 in the basket'));
    expect(api.body(api.sent('PUT', '/v1/me/baskets/b1/mine').single), {
      'lines': [
        {'item_id': '1', 'quantity': 1},
      ],
    });
    expect(find.text('Close the basket'), findsOneWidget);

    // Closed, then placed: the business takes no payment in the app.
    await press(t, find.text('Close the basket'));
    await press(t, find.text('Place the order'));
    expect(find.textContaining('takes no payment in the app, so you pay for everyone'), findsOneWidget);
    expect(find.text('Each pays their share'), findsNothing);
    await press(t, find.text('Place the order').last);
    expect(api.body(api.sent('POST', '/v1/me/baskets/b1/place').single), containsPair('payment', 'all'));
    expect(api.body(api.sent('POST', '/v1/me/baskets/b1/place').single), containsPair('total_kobo', 750000));
    expect(find.text('Order MA-1'), findsOneWidget);

    // Share cards: no saved account, so it is asked for once.
    await press(t, find.text('Send each their share'));
    expect(find.text('Account number'), findsOneWidget);
    await t.enterText(find.byType(EditableText).at(0), 'GTBank');
    await t.enterText(find.byType(EditableText).at(1), '0123456789');
    await t.enterText(find.byType(EditableText).at(2), 'Ada Obi');
    await press(t, find.text('Send each their share'));
    final sent = api.sent('POST', '/v1/me/baskets/b1/share-cards');
    expect(api.body(sent.last)['pay_to'], {'bank': 'GTBank', 'account_number': '0123456789', 'account_name': 'Ada Obi'});
    await drain(t);
  });
}
