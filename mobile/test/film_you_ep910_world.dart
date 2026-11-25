// The world of the customer films "Order together" (you-order-together) and
// "Keep it safe" (you-keep-safe): a stand-in server for You's own screens,
// built on the app's own test helpers (my_chats_test.dart's Api, people_test.dart's
// peopleChat, groups_test.dart's group and its messages, basket_test.dart's
// basket) with the story's names laid over them. The signed-in person is
// Tobi Adewale, a customer with a personal account.
//
// Stand-ins: the server's answers, the other people's phones (Bola and Kemi
// add their items through the app's own live "basket" event), the names.

import 'dart:convert';

import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/auth.dart' show Auth;
import 'package:taskiem/chat/people_live.dart';

import 'groups_test.dart' show gm, grp, member;
import 'my_chats_test.dart' show Api, answer, chat;
import 'people_test.dart' show peopleChat;

/// Tuesday 24 November 2026, 12:45 (the phone's clock reads 13:00 in the film).
final tobiNow = DateTime.utc(2026, 11, 24, 12, 45);

/// Tobi, signed in on his phone with a personal account.
Api tobiApi() => Api();

/// The same Api's auth, with Tobi as the person.
Auth tobiAuth(Api api) {
  final a = api.auth();
  a.me = Me.fromJson({
    'tenant_id': null,
    'personal': true,
    'roles': <String>[],
    'permissions': <String>[],
    'auth_method': 'phone',
    'user': {'id': 'u1', 'name': 'Tobi Adewale', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230000'},
    'businesses': <Object>[],
  });
  return a;
}

/// A group of the story's: the office's lunch group.
Map<String, dynamic> officeGroup({int unread = 0}) => {
  ...grp(name: 'Office lunch', members: 3, unread: unread, last: 'Jollof for me', lastSender: 'Bola Ige', role: 'admin'),
  'description': 'Lunch on Fridays',
};

Map<String, dynamic> lunchLine(String id, String item, String name, int q, int price) => {
  'id': id, 'item_id': item, 'name': name, 'quantity': q, 'price_kobo': price, 'added_price_kobo': price, 'price_changed': false,
  'line_kobo': q * price, 'available': true,
};

/// The basket of the group, as the server sends it, in the stage asked.
/// [people]: 0 none, 1 Tobi's own items, 3 all three have added.
Map<String, dynamic> lunchBasket({int people = 0, String state = 'open', String? fulfilment, bool placed = false, bool each = true}) {
  Map<String, dynamic>? share(int items) => placed ? {'items_kobo': items, 'delivery_kobo': 0, 'amount_kobo': items, 'paid': false, 'refund_due': false, 'card_sent': false} : null;
  final tobi = {
    'person_id': 'tobi', 'name': 'You', 'you': true, 'items_kobo': 500000,
    'lines': [lunchLine('l1', 'jollof', 'Jollof rice', 1, 350000), lunchLine('l2', 'zobo', 'Zobo', 1, 150000)],
    'share': ?share(500000),
  };
  final bola = {
    'person_id': 'bola', 'name': 'Bola Ige', 'you': false, 'items_kobo': 500000,
    'lines': [lunchLine('l3', 'jollof', 'Jollof rice', 1, 350000), lunchLine('l4', 'zobo', 'Zobo', 1, 150000)],
    'share': ?share(500000),
  };
  final kemi = {
    'person_id': 'kemi', 'name': 'Kemi Ade', 'you': false, 'items_kobo': 350000,
    'lines': [lunchLine('l5', 'jollof', 'Jollof rice', 1, 350000)],
    'share': ?share(350000),
  };
  final list = switch (people) {
    0 => <Map<String, dynamic>>[],
    1 => [tobi],
    _ => [tobi, bola, kemi],
  };
  final items = list.fold<int>(0, (a, p) => a + (p['items_kobo'] as int));
  return {
    'id': 'b1', 'business_id': 'ada', 'business': 'Ada’s Kitchen', 'you_id': 'tobi',
    'members': [
      {'person_id': 'tobi', 'name': 'You', 'you': true},
      {'person_id': 'bola', 'name': 'Bola Ige', 'you': false},
      {'person_id': 'kemi', 'name': 'Kemi Ade', 'you': false},
    ],
    'where': 'group', 'group_id': 'g1', 'state': state,
    'starter_id': 'tobi', 'holder_id': 'tobi', 'holder': 'You', 'you_hold': true, 'holder_gone': false,
    'shop': {'open': true, 'pickup': true, 'delivery': false, 'zones': <Object>[], 'payment': each ? 'at_order' : 'none', 'each_offered': each},
    'people': list,
    'fulfilment': ?fulfilment,
    'items_kobo': items, 'delivery_kobo': 0, 'total_kobo': items, 'problems': <Object>[],
    if (placed) 'payment': 'each',
    if (placed)
      'order': {
        'reference': 'AK-2041', 'status': 'awaiting_payment', 'line': 'Waiting for payment', 'total_kobo': items, 'paid_kobo': 0, 'left_kobo': items,
        'payment_state': 'unpaid', 'payable_now': true,
      },
    if (placed) 'placed_by': 'tobi',
    'created_at': '2026-11-24T12:30:00Z',
  };
}

/// The card the basket puts in the group, as the server sends the message.
Map<String, dynamic> basketMessage() => {
  ...gm('m9', mine: true, body: 'Order together from Ada’s Kitchen', kind: 'card', at: '2026-11-24T12:40:00Z'),
  'card': {'type': 'basket', 'title': 'Order together from Ada’s Kitchen', 'basket_id': 'b1', 'business': 'Ada’s Kitchen', 'business_id': 'ada'},
};

/// The office group's talk before the lunch.
List<Map<String, dynamic>> officeThread() => [
  gm('e1', kind: 'event', body: 'Tobi Adewale made the group', senderId: 'me', sender: 'Tobi Adewale', at: '2026-11-24T09:00:00Z', event: {'type': 'created', 'actor_id': 'me'}),
  gm('m1', body: 'Who is up for lunch today?', sender: 'Bola Ige', senderId: 'bola', at: '2026-11-24T12:20:00Z'),
  gm('m2', body: 'Me! Ada’s Kitchen again?', sender: 'Kemi Ade', senderId: 'kemi', at: '2026-11-24T12:22:00Z'),
  gm('m3', mine: true, body: 'Yes. I will start a basket.', at: '2026-11-24T12:25:00Z'),
];

/// What You's Chats list reads: the group, a friend, and a business.
Map<String, dynamic> conversationsList({bool group = true, bool person = false, int requests = 0}) => {
  'chats': [
    if (group)
      {
        'kind': 'group', 'id': 'g1', 'name': 'Office lunch', 'last': 'Yes. I will start a basket.', 'last_mine': true, 'last_at': '2026-11-24T12:25:00Z',
        'unread': 0, 'muted': false, 'archived': false, 'blocked': false, 'group': officeGroup(),
      },
    if (person) {...peopleChat(name: 'Dapo Ige', last: 'Did you see the match?', unread: 1), 'kind': 'person', 'last_at': '2026-11-24T11:50:00Z'},
    {
      'kind': 'business', 'id': 'c1', 'name': 'Ada’s Kitchen', 'business_id': 'ada', 'last': 'Your order is ready', 'last_mine': false, 'last_at': '2026-11-23T14:00:00Z',
      'unread': 0, 'muted': false, 'archived': false, 'blocked': false,
    },
  ],
  'unread': person ? 1 : 0,
  'requests': requests,
  'invites': 0,
  'more': false,
};

/// Routes every film of the pair needs: You's Chats list.
void youRoutes(Api api, {bool group = true, bool person = false}) {
  api.routes['GET /v1/me/conversations'] = (r) => r.url.queryParameters['archived'] == '1'
      ? {'chats': <Object>[], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false}
      : conversationsList(group: group, person: person);
  api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
  api.routes['GET /v1/me/chats'] = (_) => {
    'chats': [
      {...chat(id: 'c1', name: 'Ada’s Kitchen', unread: 0), 'business': 'ada'},
      {...chat(id: 'c2', name: 'Mama Put Corner', unread: 0), 'business': 'mamaput'},
    ],
    'unread': 0,
  };
}

/// The office group, served.
void groupRoutes(Api api, {required List<Map<String, dynamic>> Function() messages}) {
  api.routes['GET /v1/me/groups/g1'] = (_) => officeGroup();
  api.routes['GET /v1/me/groups/g1/messages'] = (_) => {'messages': messages(), 'more': false};
  api.routes['GET /v1/me/groups/g1/members'] = (_) => {
    'members': [member('me', 'Tobi Adewale', role: 'admin', you: true), member('bola', 'Bola Ige'), member('kemi', 'Kemi Ade')],
    'invited': <Object>[],
    'waiting': <Object>[],
  };
  api.routes['POST /v1/me/groups/g1/read'] = (_) => answer({}, 204);
}

/// A thing the live connection says, as the app hears it.
void hearLive(PeopleLive live, String event, Object data) => live.hearEvent((event: event, data: jsonEncode(data), id: null));
