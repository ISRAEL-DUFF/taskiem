// Taskiem for everyone, stage 3 in the app (docs/spec/taskiem-for-everyone.md
// §7, §10, §13): You's tabs; Orders on You (the waiting count and "These
// are mine", rows, older pages, Pay, Chat about this, one order); Discover
// (categories, a search and its pages, 429 said plainly, Chat, a profile
// with Their shop, Book and Report); saved addresses in Settings and at
// their shop's check step; and a business's Settings > Discover.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/addresses.dart';
import 'package:taskiem/chat/discover_settings.dart';
import 'package:taskiem/kit/sheet.dart' show TkKitField;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'my_chats_test.dart' show Api, answer, at, chat, conversation, drain, openChat, phone, press, settle, typeIn;
import 'guidelines.dart';
import 'support.dart' show loadFonts;

Map<String, dynamic> order({
  String ref = 'MK-1042',
  String business = 'b1',
  String name = 'Mama’s Kitchen',
  String state = 'accepted',
  bool pay = false,
  String? chatId,
  String kind = 'order',
  String made = '2026-11-15T10:00:00Z',
}) => {
  'business_id': business,
  'business': name,
  'reference': ref,
  'kind': kind,
  'state': state,
  'summary': kind == 'booking' ? 'Braids' : '2 × Jollof rice, 1 × Chapman',
  'total_kobo': 450000,
  'due_kobo': pay ? 450000 : 0,
  'pay': pay,
  'made_at': made,
  'chat_id': chatId,
};

/// You, served: no chats, no invitations.
Api you() {
  final api = conversation(c: chat(offers: 'no', ask: false));
  api.routes['GET /v1/me/chats'] = (_) => {'chats': <Object>[], 'unread': 0};
  api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
  return api;
}

Future<void> openYou(WidgetTester t, Api api) async {
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at)));
  await settle(t);
}

Map<String, dynamic> result(String id, String name, {bool open = true, bool canChat = true}) => {
  'business': id,
  'name': name,
  'photo': <String, dynamic>{},
  'category': 'food',
  'category_name': 'Food and drinks',
  'area': 'Yaba',
  'open_now': open,
  'line': open ? 'Open now, closes at 18:00' : 'Closed now. Opens tomorrow at 09:00',
  'can_chat': canChat,
};

Map<String, dynamic> address(String id, String label, {bool def = false, String? phone}) => {
  'id': id,
  'label': label,
  'line': '12 Herbert Macaulay Way',
  'area': 'Yaba',
  'landmark': 'By the filling station',
  'phone': phone,
  'default': def,
  'created_at': '2026-11-10T10:00:00Z',
  'updated_at': '2026-11-10T10:00:00Z',
};

void main() {
  setUpAll(loadFonts);

  testWidgets('You has three tabs: Chats, Orders, Discover', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/orders'] = (_) => {'orders': <Object>[], 'next': null, 'waiting_count': 0};
    api.routes['GET /v1/discover/categories'] = (_) => {'categories': <Object>[]};
    api.routes['POST /v1/discover/search'] = (_) => {'results': <Object>[], 'more': false};
    await openYou(t, api);
    expect(find.text('Chats'), findsWidgets);
    expect(find.text('No chats yet'), findsOneWidget);
    expect(api.sent('GET', '/v1/me/orders'), isEmpty, reason: 'a tab is read when first opened');
    await press(t, find.text('Orders').first);
    expect(find.text('No orders yet'), findsOneWidget);
    await press(t, find.text('Discover').first);
    expect(find.text('No businesses found'), findsOneWidget);
    await drain(t);
  });

  testWidgets('Orders: "These are mine", rows with Pay, older pages, Chat about this', (t) async {
    phone(t);
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    final api = you();
    var confirmed = false;
    api.routes['GET /v1/me/orders'] = (r) => r.url.queryParameters['before'] == 'cur-2'
        ? {
            'orders': [order(ref: 'BS-1', business: 'b2', name: 'Bola Stores', state: 'completed', made: '2026-09-01T10:00:00Z')],
            'next': null,
            'waiting_count': 0,
          }
        : {
            'orders': [
              order(pay: true, chatId: 'c1'),
              if (confirmed) order(ref: 'TK-9', business: 'b3', name: 'Tunde Kicks', state: 'completed', made: '2026-10-01T10:00:00Z'),
            ],
            'next': 'cur-2',
            'waiting_count': confirmed ? 0 : 1,
          };
    api.routes['POST /v1/me/storefront-orders/confirm'] = (_) {
      confirmed = true;
      return {'confirmed': true};
    };
    api.routes['GET /v1/me/orders/b1/MK-1042/pay'] = (_) => {'reference': 'MK-1042', 'due_kobo': 450000, 'url': 'https://app.taskiem.com/o/abc'};
    api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {
      ...order(pay: true, chatId: 'c1'),
      'lines': [
        {'name': 'Jollof rice', 'quantity': 2, 'price_kobo': 150000, 'line_kobo': 300000},
        {'name': 'Chapman', 'quantity': 1, 'price_kobo': 50000, 'line_kobo': 50000},
      ],
      'fulfilment': 'delivery',
      'zone': 'Yaba',
      'delivery_kobo': 100000,
      'paid_kobo': 0,
      'address': {'address': '12 Herbert Macaulay Way, Yaba', 'landmark': 'By the filling station'},
      'delivery': {'status': 'picked_up', 'line': 'On its way to you', 'accepted_at': '2026-11-15T10:20:00Z', 'picked_up_at': '2026-11-15T10:40:00Z'},
    };
    api.routes['POST /v1/me/chats'] = (_) => chat(id: 'c1', offers: 'no', ask: false);
    await openYou(t, api);
    await press(t, find.text('Orders').first);
    expect(find.textContaining('We found an order made with your number'), findsOneWidget);
    expect(find.text('Tunde Kicks'), findsNothing, reason: 'nothing of a waiting order shows');
    expect(find.text('₦4,500 due'), findsOneWidget);
    await press(t, find.text('It’s mine'));
    expect(api.sent('POST', '/v1/me/storefront-orders/confirm'), hasLength(1));
    expect(find.text('Tunde Kicks'), findsOneWidget);
    expect(find.textContaining('We found an order'), findsNothing);

    await press(t, find.text('Show older orders'));
    expect(api.sent('GET', '/v1/me/orders').last.url.queryParameters['before'], 'cur-2');
    expect(find.text('Bola Stores'), findsOneWidget);
    expect(find.text('Show older orders'), findsNothing, reason: 'the last page');

    // One order: its lines, the rider's progress, the address; Pay and Chat.
    await press(t, find.text('Mama’s Kitchen'));
    expect(find.text('2 × Jollof rice'), findsOneWidget);
    expect(find.text('On its way to you'), findsOneWidget);
    expect(find.text('12 Herbert Macaulay Way, Yaba'), findsOneWidget);
    expect(find.text('By the filling station'), findsOneWidget);
    await press(t, find.text('Pay ₦4,500'));
    expect(opened, [Uri.parse('https://app.taskiem.com/o/abc')]);
    await press(t, find.text('Chat about this'));
    expect(api.sent('POST', '/v1/me/chats'), isEmpty, reason: 'the chat they have is opened');
    expect((t.widget(find.byType(EditableText)) as EditableText).controller.text, 'Hello, about my order MK-1042: ');
    await drain(t);
  });

  testWidgets('Chat about this with no chat yet starts one from the order', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/orders'] = (_) => {'orders': [order(ref: 'TK-9', business: 'b3', name: 'Tunde Kicks', kind: 'booking')], 'next': null, 'waiting_count': 0};
    api.routes['GET /v1/me/orders/b3/TK-9'] = (_) => {
      ...order(ref: 'TK-9', business: 'b3', name: 'Tunde Kicks', kind: 'booking'),
      'lines': <Object>[],
      'paid_kobo': 0,
      'service_name': 'Braids',
      'starts_at': '2026-11-17T09:00:00Z',
      'ends_at': '2026-11-17T10:30:00Z',
    };
    api.routes['POST /v1/me/chats'] = (_) => chat(id: 'c1', offers: 'no', ask: false);
    await openYou(t, api);
    await press(t, find.text('Orders').first);
    await press(t, find.text('Tunde Kicks'));
    expect(find.text('Booking TK-9'), findsOneWidget);
    expect(find.textContaining('10:00 to 11:30'), findsOneWidget);
    await press(t, find.text('Chat about this'));
    expect(api.body(api.sent('POST', '/v1/me/chats').single), {'business_id': 'b3', 'from': 'order'});
    expect((t.widget(find.byType(EditableText)) as EditableText).controller.text, 'Hello, about my order TK-9: ');
    await drain(t);
  });

  testWidgets('Discover: categories, a search and more, too fast said plainly, Chat from a row', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/orders'] = (_) => {'orders': <Object>[], 'next': null, 'waiting_count': 0};
    api.routes['GET /v1/discover/categories'] = (_) => {
      'categories': [
        {'key': 'food', 'name': 'Food and drinks'},
        {'key': 'beauty', 'name': 'Beauty and personal care'},
      ],
    };
    var fast = false;
    api.routes['POST /v1/discover/search'] = (r) {
      if (fast) return answer({'error': 'You are searching too fast. Wait a moment and try again.'}, 429);
      final b = api.body(r);
      if (b['offset'] == 2) return {'results': [result('b9', 'Zainab Cakes')], 'more': false};
      return {
        'results': [result('b1', 'Mama’s Kitchen'), result('b2', 'Bola Stores', open: false, canChat: false)],
        'more': true,
      };
    };
    api.routes['POST /v1/me/chats'] = (_) => chat(id: 'c1', offers: 'no', ask: false);
    await openYou(t, api);
    await press(t, find.text('Discover').first);
    expect(api.body(api.sent('POST', '/v1/discover/search').first), <String, dynamic>{});
    expect(find.text('Mama’s Kitchen'), findsOneWidget);
    expect(find.text('Food and drinks · Yaba\nOpen now, closes at 18:00'), findsOneWidget, reason: 'the hours line under the category and area');
    expect(find.text('Open now'), findsOneWidget);
    expect(find.text('Closed now'), findsOneWidget);
    expect(find.bySemanticsLabel('Chat with Bola Stores'), findsNothing, reason: 'not taking chats');

    await press(t, find.text('Show more businesses'));
    expect(api.body(api.sent('POST', '/v1/discover/search').last), {'offset': 2}, reason: 'after the results given');
    expect(find.text('Zainab Cakes'), findsOneWidget);

    await press(t, find.text('Beauty and personal care'));
    expect(api.body(api.sent('POST', '/v1/discover/search').last), {'category': 'beauty'});

    await typeIn(t, find.bySemanticsLabel('Search by name or what you want'), 'rice');
    await settle(t);
    await settle(t);
    expect(api.body(api.sent('POST', '/v1/discover/search').last), {'q': 'rice', 'category': 'beauty'});

    fast = true;
    await typeIn(t, find.bySemanticsLabel('Area (Yaba, Lekki…)'), 'Yaba');
    await settle(t);
    await settle(t);
    expect(find.text('You are searching too fast. Wait a moment and try again.'), findsOneWidget);
    expect(find.text('Mama’s Kitchen'), findsNothing, reason: 'a failed search leaves no results of the one before');
    expect(find.text('Show more businesses'), findsNothing);
    fast = false;
    await typeIn(t, find.bySemanticsLabel('Area (Yaba, Lekki…)'), 'Yab');
    await settle(t);
    await settle(t);

    await press(t, find.bySemanticsLabel('Chat with Mama’s Kitchen'));
    expect(api.body(api.sent('POST', '/v1/me/chats').single), {'business_id': 'b1', 'from': 'discover'});
    expect(find.text('Open now, closes at 18:00'), findsOneWidget, reason: 'the conversation is open');
    await drain(t);
  });

  testWidgets('a profile: photos, hours, address and map; Their shop opens the chat then the shop; Report', (t) async {
    phone(t);
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    final api = you();
    api.routes['GET /v1/discover/categories'] = (_) => {'categories': <Object>[]};
    api.routes['POST /v1/discover/search'] = (_) => {'results': [result('b1', 'Mama’s Kitchen')], 'more': false};
    api.routes['GET /v1/discover/b1'] = (_) => {
      'business': 'b1',
      'name': 'Mama’s Kitchen',
      'tagline': 'Home cooking, every day',
      'category': 'food',
      'category_name': 'Food and drinks',
      'area': 'Yaba',
      'address': '3 Montgomery Road, Yaba',
      'map_link': 'https://maps.example/mk',
      'photo': null,
      'photos': [
        {'urls': {'480': 'https://img.example/p1.jpg'}, 'caption': 'Our jollof'},
      ],
      'hours': {
        'days': [
          [{'open': '09:00', 'close': '18:00'}],
          [], [], [], [], [], [],
        ],
      },
      'open_now': true,
      'line': 'Open now, closes at 18:00',
      'can_chat': true,
      'has_shop': true,
      'has_book': true,
      'chat': null,
    };
    api.routes['POST /v1/me/chats'] = (_) => chat(id: 'c1', offers: 'no', ask: false);
    api.routes['GET /v1/me/chats/c1/shop'] = (_) => {'open': true, 'payment': 'at_order', 'pickup': true, 'delivery': false, 'zones': <Object>[], 'items': <Object>[]};
    api.routes['POST /v1/discover/b1/report'] = (_) => answer({'reported': true, 'words': 'Thank you. Taskiem\'s moderators will look at this business.'}, 202);
    await openYou(t, api);
    await press(t, find.text('Discover').first);
    await press(t, find.text('Mama’s Kitchen'));
    expect(find.text('Home cooking, every day'), findsOneWidget);
    expect(find.bySemanticsLabel('Our jollof'), findsOneWidget);
    expect(find.text('09:00–18:00'), findsOneWidget);
    expect(find.text('3 Montgomery Road, Yaba'), findsOneWidget);
    await press(t, find.text('Open in your map'));
    expect(opened, [Uri.parse('https://maps.example/mk')]);

    await press(t, find.text('Report Mama’s Kitchen'));
    await press(t, find.text('Send report'));
    expect(find.text('Choose why you are reporting them.'), findsOneWidget);
    await press(t, find.text('Pretends to be someone else'));
    await typeIn(t, find.ancestor(of: find.text('What happened'), matching: find.byType(TkKitField)), 'Uses another shop’s name');
    await press(t, find.text('Send report'));
    expect(api.body(api.sent('POST', '/v1/discover/b1/report').single), {'reason': 'impersonation', 'note': 'Uses another shop’s name'});
    expect(find.text('Thank you. Taskiem\'s moderators will look at this business.'), findsOneWidget);
    await press(t, find.text('Done'));

    await press(t, find.text('Their shop'));
    expect(api.body(api.sent('POST', '/v1/me/chats').single), {'business_id': 'b1', 'from': 'discover'});
    expect(find.text('Mama’s Kitchen has nothing in their shop yet'), findsOneWidget, reason: 'the shop opened over the chat');
    await drain(t);
  });

  testWidgets('saved addresses in Settings: the list, add (a phone that is not one), make default, delete', (t) async {
    phone(t);
    final api = you();
    final list = [address('a1', 'Home', def: true)];
    api.routes['GET /v1/me/addresses'] = (_) => {'addresses': list};
    var tries = 0;
    api.routes['POST /v1/me/addresses'] = (r) {
      if (++tries == 1) return answer({'error': 'The phone for delivery doesn\'t look like a phone number.', 'phone_invalid': true}, 400);
      list.add(address('a2', 'Work'));
      return answer(list.last, 201);
    };
    api.routes['PUT /v1/me/addresses/a2'] = (_) => list.last;
    api.routes['DELETE /v1/me/addresses/a2'] = (_) {
      list.removeLast();
      return answer({}, 204);
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouSettingsScreen(auth: api.auth())));
    await settle(t);
    await press(t, find.text('Saved addresses'));
    expect(find.text('Home'), findsOneWidget);
    expect(find.text('Default'), findsOneWidget);

    await press(t, find.text('Add an address'));
    await press(t, find.text('Save'));
    expect(find.text('Give the address a name (Home, Work) and its street and number.'), findsOneWidget);
    Finder field(String label) => find.ancestor(of: find.text(label), matching: find.byType(TkKitField));
    await typeIn(t, field('Name'), 'Work');
    await typeIn(t, field('Street and number'), '5 Broad Street');
    await typeIn(t, field('Phone for delivery'), '123');
    await press(t, find.text('Save'));
    expect(find.text('The phone for delivery doesn\'t look like a phone number.'), findsOneWidget);
    await typeIn(t, field('Phone for delivery'), '08031234567');
    await press(t, find.bySemanticsLabel('Make this my default').last);
    await press(t, find.text('Save'));
    final added = api.body(api.sent('POST', '/v1/me/addresses').last);
    expect(added, {'label': 'Work', 'line': '5 Broad Street', 'area': '', 'landmark': '', 'phone': '08031234567', 'default': true});
    expect(find.text('Work'), findsOneWidget);

    await press(t, find.text('Work'));
    await press(t, find.text('Delete'));
    await press(t, find.text('Delete').last);
    expect(api.sent('DELETE', '/v1/me/addresses/a2'), hasLength(1));
    expect(find.text('Work'), findsNothing);
    await drain(t);
  });

  testWidgets('20 addresses: no more can be added', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/addresses'] = (_) => {
      'addresses': [for (var i = 0; i < maxSavedAddresses; i++) address('a$i', 'Place $i', def: i == 0)],
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAddressesPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Add an address'), findsNothing);
    expect(find.textContaining('You can keep up to 20 addresses'), findsOneWidget);
    await drain(t);
  });

  testWidgets('their shop\'s check step: a saved address, or one typed and saved', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
      'open': true,
      'payment': 'at_order',
      'pickup': false,
      'delivery': true,
      'zones': <Object>[],
      'items': [
        {'id': 'i1', 'name': 'Jollof rice', 'price_kobo': 250000, 'available': true},
      ],
    };
    api.routes['GET /v1/me/addresses'] = (_) => {
      'addresses': [address('a1', 'Home', def: true), address('a2', 'Work')],
    };
    var n = 0;
    api.routes['POST /v1/me/chats/c1/orders'] = (_) => answer({
      'order': {'reference': 'MK-105${++n}', 'kind': 'app', 'status': 'sent', 'summary': '1 × Jollof rice', 'total_kobo': 250000, 'due_kobo': 0, 'pay': false, 'created_at': '2026-11-16T10:30:00Z'},
    }, 201);
    api.routes['POST /v1/me/addresses'] = (_) => answer(address('a3', 'Mum'), 201);
    Future<void> toCheck() async {
      await press(t, find.bySemanticsLabel('Chat tools'));
      await press(t, find.text('Their shop'));
      await press(t, find.bySemanticsLabel('One more Jollof rice'));
      await press(t, find.text('Review order'));
    }

    await openChat(t, api);
    await toCheck();
    expect(find.text('Home · 12 Herbert Macaulay Way, Yaba'), findsOneWidget);
    await press(t, find.text('Work · 12 Herbert Macaulay Way, Yaba'));
    await press(t, find.textContaining('Place order'));
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/orders').last), {
      'lines': [
        {'item_id': 'i1', 'quantity': 1},
      ],
      'fulfilment': 'delivery',
      'address_id': 'a2',
    });
    await t.pump(const Duration(seconds: 3));
    await settle(t);

    await toCheck();
    await press(t, find.text('Another address'));
    Finder field(String label) => find.ancestor(of: find.text(label), matching: find.byType(TkKitField));
    await typeIn(t, field('Address'), '7 Allen Avenue, Ikeja');
    await typeIn(t, field('Landmark'), 'Blue gate');
    await press(t, find.text('Save this address'));
    await typeIn(t, field('Name it'), 'Mum');
    await press(t, find.textContaining('Place order'));
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/orders').last), {
      'lines': [
        {'item_id': 'i1', 'quantity': 1},
      ],
      'fulfilment': 'delivery',
      'address': '7 Allen Avenue, Ikeja',
      'landmark': 'Blue gate',
    });
    expect(api.body(api.sent('POST', '/v1/me/addresses').single), {'label': 'Mum', 'line': '7 Allen Avenue, Ikeja', 'landmark': 'Blue gate'});
    await drain(t);
  });

  testWidgets('a business\'s Settings > Discover: the switch, what is missing, hidden, without Chat, its category and hours', (t) async {
    phone(t);
    final api = Api();
    Map<String, dynamic> settings({bool listed = true, String? category, Map<String, dynamic>? hours, bool hidden = false}) => {
      'listed': listed,
      'chosen': listed ? null : false,
      'default': true,
      'category': category,
      'hours': hours,
      'missing': category == null ? ['category'] : <String>[],
      'missing_line': category == null ? 'Customers can\'t find you in Discover until you add a category.' : null,
      'shown': listed && category != null && !hidden,
      'hidden': hidden ? 'reported' : null,
      'chat_off': 'orders_off',
      'listing': {
        'business': 't1',
        'name': 'Mama’s Kitchen',
        'area': 'Yaba',
        'category': category,
        'can_chat': false,
        'has_shop': true,
        'has_book': false,
        'hours': {
          'days': [
            [{'open': '09:00', 'close': '18:00'}],
            [], [], [], [], [], [],
          ],
        },
        'photos': <Object>[],
      },
    };
    var s = settings();
    api.routes['GET /v1/settings/discover'] = (_) => s;
    api.routes['PUT /v1/settings/discover'] = (r) {
      final b = api.body(r);
      if (b['listed'] == false) s = settings(listed: false);
      if (b['category'] != null) s = settings(category: b['category'] as String, hidden: true);
      if (b['hours'] != null) s = settings(category: 'food', hours: b['hours'] as Map<String, dynamic>);
      return s;
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: DiscoverSettingsPage(auth: api.auth(permissions: ['storefront.manage'], personal: false))));
    await settle(t);
    expect(find.text('Customers can\'t find you in Discover until you add a category.'), findsOneWidget);
    expect(find.text('Your Orders area is off, so your listing shows without Chat.'), findsOneWidget);
    expect(find.text('Their shop'), findsOneWidget, reason: 'the listing\'s preview');

    await press(t, find.text('Category'));
    await press(t, find.text('Food and drinks'));
    expect(api.body(api.sent('PUT', '/v1/settings/discover').last), {'category': 'food'});
    expect(find.text('Your listing is hidden while Taskiem’s moderators look at reports about it.'), findsOneWidget);

    await press(t, find.text('Opening hours'));
    await press(t, find.bySemanticsLabel('Tuesday open'));
    await press(t, find.text('Save').last);
    final hours = api.body(api.sent('PUT', '/v1/settings/discover').last)['hours'] as Map<String, dynamic>;
    expect((hours['days'] as List)[1], [
      {'open': '09:00', 'close': '18:00'},
    ]);
    expect(find.text('Use your shop’s hours'), findsOneWidget);

    await press(t, find.bySemanticsLabel('Found by the public in the Taskiem app').last);
    expect(api.body(api.sent('PUT', '/v1/settings/discover').last), {'listed': false});
    expect(find.text('Customers can’t find you in Discover.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('Orders and Discover can be pressed: words, actions, 48 by 48; large text cuts nothing off', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/orders'] = (_) => {'orders': [order(pay: true, chatId: 'c1')], 'next': 'cur-2', 'waiting_count': 2};
    api.routes['GET /v1/discover/categories'] = (_) => {
      'categories': [
        {'key': 'food', 'name': 'Food and drinks'},
      ],
    };
    api.routes['POST /v1/discover/search'] = (_) => {'results': [result('b1', 'Mama’s Kitchen')], 'more': true};
    await openYou(t, api);
    await press(t, find.text('Orders').first);
    await expectPressable(t);
    await press(t, find.text('Discover').first);
    await expectPressable(t);
    await drain(t);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: YouHome(auth: api.auth(), now: () => at)), drive: (t) async {
      await settle(t);
      await t.tap(find.text('Orders').first);
      await settle(t);
    });
    await drain(t);
  });

  test('where a listing stands, in words', () {
    expect(discoverHiddenWords('removed'), 'Taskiem’s moderators took your listing out of Discover.');
    expect(discoverChatOffWords('chats_off'), 'You aren’t taking chats in the app, so your listing shows without Chat.');
    expect(discoverHiddenWords(null), isNull);
  });
}
