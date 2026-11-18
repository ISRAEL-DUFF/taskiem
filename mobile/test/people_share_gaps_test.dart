// Sharing into a chat with a person from outside one (docs/spec/
// people-chatting.md §10.1; "As built: stage 5a gaps, app"): a business from
// its Discover profile, a product from a business's shop in its chat, and a
// delivery from an order's page once a rider has it, each into chats picked.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/discover.dart' show DiscoverProfileScreen;
import 'package:taskiem/chat/your_orders.dart' show YourOrderPage;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'my_chats_test.dart' show Api, answer, at, chat, conversation, drain, openChat, phone, press, settle;
import 'support.dart' show loadFonts;
import 'you_stage3_test.dart' show order;

/// A friend to share with, and what was sent to them.
void friend(Api api) {
  api.routes['GET /v1/me/people'] = (_) => {
    'chats': [
      {
        'id': 'f1', 'kind': 'person', 'person_id': 'p1', 'name': 'Friend 1', 'gone': false, 'last': '', 'last_mine': false,
        'last_at': '2026-11-17T09:10:00Z', 'unread': 0, 'muted': false, 'archived': false, 'request': false, 'waiting': false, 'blocked': false,
      },
    ],
    'unread': 0,
    'requests': 0,
    'more': false,
  };
  api.routes['POST /v1/me/people/f1/messages'] = (_) => answer({'id': 'k1', 'mine': true, 'kind': 'card', 'body': '', 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T10:29:00Z'}, 201);
}

Future<Map<String, dynamic>> shareTo(WidgetTester t, Api api) async {
  await press(t, find.text('Friend 1'));
  await press(t, find.text('Send'));
  await t.pump(const Duration(seconds: 5));
  return api.body(api.sent('POST', '/v1/me/people/f1/messages').single)['card'] as Map<String, dynamic>;
}

void main() {
  setUpAll(loadFonts);

  testWidgets('Discover’s profile: Share with someone sends the business’s card', (t) async {
    phone(t);
    final api = Api();
    friend(api);
    api.routes['GET /v1/discover/b1'] = (_) => {
      'business': 'b1', 'name': 'Mama’s Kitchen', 'tagline': 'Home cooking', 'category': 'food', 'category_name': 'Food and drinks', 'area': 'Yaba',
      'photo': null, 'photos': <Object>[], 'hours': {'days': [[], [], [], [], [], [], []]}, 'open_now': true, 'line': 'Open now',
      'can_chat': true, 'has_shop': true, 'has_book': false, 'chat': null,
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: DiscoverProfileScreen(auth: api.auth(), business: 'b1', onOpenChat: (id, {notice, draft = '', tool}) async {})));
    await settle(t);
    await press(t, find.bySemanticsLabel('Share Mama’s Kitchen with someone'));
    expect(await shareTo(t, api), {'type': 'business', 'business_id': 'b1'});
    await drain(t);
  });

  testWidgets('a business’s shop, in its chat: Share on a product sends its card', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    friend(api);
    api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
      'open': true, 'payment': 'at_order', 'pickup': true, 'delivery': false, 'zones': <Object>[],
      'items': [
        {'id': 'i1', 'name': 'Jollof rice', 'price_kobo': 250000, 'available': true},
      ],
    };
    await openChat(t, api);
    await press(t, find.bySemanticsLabel('Chat tools'));
    await press(t, find.text('Their shop'));
    await press(t, find.bySemanticsLabel('Share Jollof rice with someone'));
    expect(await shareTo(t, api), {'type': 'product', 'business_id': 'b1', 'item_id': 'i1'});
    await drain(t);
  });

  testWidgets('an order’s page: Share the delivery only once a rider has it', (t) async {
    phone(t);
    final api = Api();
    friend(api);
    var rider = false;
    api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {
      ...order(chatId: 'c1'),
      'lines': <Object>[],
      'paid_kobo': 0,
      'fulfilment': 'delivery',
      if (rider) 'delivery': {'status': 'picked_up', 'line': 'On its way to you', 'accepted_at': '2026-11-15T10:20:00Z', 'picked_up_at': '2026-11-15T10:40:00Z'},
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YourOrderPage.of(auth: api.auth(), business: 'b1', reference: 'MK-1042', onOpenChat: (id, {notice, draft = '', tool}) async {}, now: () => at)));
    await settle(t);
    expect(find.text('Share the delivery'), findsNothing, reason: 'no rider yet');
    expect(find.text('Share with someone'), findsOneWidget, reason: 'the order itself can be shared');
    await drain(t);

    rider = true;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YourOrderPage.of(auth: api.auth(), business: 'b1', reference: 'MK-1042', onOpenChat: (id, {notice, draft = '', tool}) async {}, now: () => at)));
    await settle(t);
    await press(t, find.text('Share the delivery'));
    expect(await shareTo(t, api), {'type': 'delivery', 'business_id': 'b1', 'reference': 'MK-1042'});
    await drain(t);
  });
}
