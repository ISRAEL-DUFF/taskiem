// Taskiem for everyone, stage 2 in the app, the business's side
// (docs/spec/taskiem-for-everyone.md §8.2, §8.3, §8.6, §15): a customer
// who chats in the app (no 24-hour window, typing, their files, cards and
// offers shown); rich replies (a product, an order's pay link, a booking
// offer, a photo, an offer); Block and "This customer has stopped
// messages"; Your chat link (its QR code, words to the share sheet, Make a
// new link) and the customer chat settings.

import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/chat_link_page.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'my_chats_test.dart' show Api, answer, at, drain, press, settle, phone;
import 'guidelines.dart';
import 'support.dart' show loadFonts;

Map<String, dynamic> thread({bool stopped = false, bool blocked = false, bool typing = false, String offers = 'yes'}) => {
  'id': 'cc1',
  'name': 'Tolu Ade',
  'phone': '+2348031230000',
  'line': 'app',
  'stopped': stopped,
  if (stopped) 'notice': 'This customer has stopped messages',
  'blocked': blocked,
  'window_open': false,
  'archived': false,
  'last': 'Do you deliver to Yaba?',
  'last_at': '2026-11-16T10:00:00Z',
  'last_mine': false,
  'unread': 0,
  'offers': offers,
  'typing': typing,
};

final threadMessages = [
  {'id': 'x1', 'mine': false, 'kind': 'text', 'body': 'Do you deliver to Yaba?', 'read_at': null, 'created_at': '2026-11-16T09:00:00Z'},
  {
    'id': 'x2',
    'mine': false,
    'kind': 'text',
    'body': 'File',
    'attachment': {'kind': 'file', 'name': 'transfer-receipt.pdf', 'size': 2048, 'type': 'application/pdf'},
    'read_at': null,
    'created_at': '2026-11-16T09:01:00Z',
  },
  {
    'id': 'x3',
    'mine': true,
    'kind': 'card',
    'body': 'Ankara shirt',
    'sender_name': 'Bisi',
    'delivery': 'read',
    'card': {'type': 'product', 'item_id': 'i2', 'title': 'Ankara shirt', 'line': 'Hand-sewn, cotton', 'total_kobo': 1500000},
    'read_at': '2026-11-16T09:03:00Z',
    'created_at': '2026-11-16T09:02:00Z',
  },
  {'id': 'x4', 'mine': true, 'kind': 'text', 'body': 'Weekend: 10% off shirts', 'offer': true, 'sender_name': 'Bisi', 'delivery': 'delivered', 'read_at': null, 'created_at': '2026-11-16T09:04:00Z'},
];

class _Pick extends Pickers {
  const _Pick();
  @override
  Future<PickedFile?> photo({double? side}) async => PickedFile(bytes: Uint8List(64), name: 'cake.jpg', type: 'image/jpeg');
}

Api business({Map<String, dynamic>? c}) {
  final api = Api();
  api.routes['GET /v1/customer-chats/cc1'] = (_) => c ?? thread();
  api.routes['GET /v1/customer-chats/cc1/messages'] = (_) => {'messages': threadMessages, 'more': false};
  api.routes['POST /v1/customer-chats/cc1/read'] = (_) => answer({}, 204);
  api.routes['GET /v1/customer-chats/cc1/orders'] = (_) => {'items': <Object>[], 'orders': <Object>[], 'pay_page': false, 'zones': <Object>[]};
  api.routes['GET /v1/customer-chats'] = (_) => {'chats': <Object>[], 'unread': 0};
  return api;
}

Future<CustomerData> openThread(WidgetTester t, Api api) async {
  final chats = ChatsData(api.auth(permissions: const ['storefront.manage', 'order.handle'], personal: false), now: () => at);
  final d = CustomerData(chats, 'cc1', follow: false);
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: CustomerScreen(data: d, pickers: const _Pick())));
  await settle(t);
  return d;
}

void main() {
  setUpAll(loadFonts);

  testWidgets('an app customer: no 24-hour window, typing, their file, a product card and an offer', (t) async {
    phone(t);
    final api = business(c: thread(typing: true));
    final d = await openThread(t, api);
    expect(find.text('typing…'), findsOneWidget);
    expect(find.textContaining('More than a day since they last wrote'), findsNothing, reason: 'no window in the app');
    expect(find.text('Do you deliver to Yaba?'), findsOneWidget);
    expect(find.text('transfer-receipt.pdf'), findsOneWidget);
    expect(find.text('File'), findsNothing, reason: 'the file says what it is');
    expect(find.text('PRODUCT'), findsOneWidget);
    expect(find.text('Ankara shirt'), findsOneWidget);
    expect(find.text('₦15,000'), findsOneWidget);
    expect(find.text('Offer'), findsOneWidget);
    expect(find.bySemanticsLabel('Send an order, a pay link, a booking, a product or a file'), findsOneWidget);
    await expectPressable(t);
    await drain(t);
    d.dispose();
  });

  testWidgets('rich replies: a product, a pay link by reference, a photo, the box\'s words as an offer', (t) async {
    phone(t);
    final api = business();
    final sent = <Map<String, dynamic>>[];
    api.routes['POST /v1/customer-chats/cc1/messages'] = (r) {
      final b = api.body(r);
      sent.add(b);
      return answer({
        'id': 'r${sent.length}',
        'mine': true,
        'kind': b['card'] == null ? 'text' : 'card',
        'body': b['text'] ?? 'Card',
        'offer': b['offer'],
        'delivery': 'delivered',
        'read_at': null,
        'created_at': '2026-11-16T10:29:00Z',
      }, 201);
    };
    api.routes['POST /v1/customer-chats/cc1/files'] = (r) => answer({
      'id': 'f1',
      'mine': true,
      'kind': 'text',
      'body': 'Photo',
      'attachment': {'kind': 'photo', 'name': 'cake.jpg', 'size': 64, 'type': 'image/jpeg'},
      'delivery': 'delivered',
      'read_at': null,
      'created_at': '2026-11-16T10:29:00Z',
    }, 201);
    api.routes['POST /v1/customer-chats/cc1/typing'] = (_) => answer({}, 204);
    api.routes['GET /v1/storefront/catalogue'] = (_) => {
      'items': [
        {'id': 'i1', 'name': 'Jollof rice', 'price_kobo': 250000},
        {'id': 'i1-big', 'name': 'Jollof rice (big)', 'variant_of': 'i1'},
      ],
    };
    final d = await openThread(t, api);
    final more = find.bySemanticsLabel('Send an order, a pay link, a booking, a product or a file');

    await press(t, more);
    await press(t, find.text('A product'));
    expect(find.text('Jollof rice (big)'), findsNothing, reason: 'sizes are sent with their product');
    await press(t, find.text('Jollof rice'));
    expect(sent.last['card'], {'type': 'product', 'item_id': 'i1'});

    api.routes['GET /v1/storefront/orders'] = (_) => {'orders': <Object>[]};
    await press(t, more);
    await press(t, find.text('A pay link'));
    await t.enterText(find.byType(EditableText).last, 'MK-1042');
    await t.pump();
    await press(t, find.text('Send').last);
    expect(sent.last['card'], {'type': 'pay', 'reference': 'MK-1042'});

    await press(t, more);
    await press(t, find.text('A photo'));
    expect(api.sent('POST', '/v1/customer-chats/cc1/files'), hasLength(1));

    // The box's words, as an offer (to a customer who said yes).
    await t.enterText(find.byType(EditableText).first, 'Free delivery today');
    await t.pump();
    expect(api.sent('POST', '/v1/customer-chats/cc1/typing'), hasLength(1), reason: 'the customer sees staff typing');
    await press(t, more);
    await press(t, find.text('Send as an offer'));
    expect(sent.last['text'], 'Free delivery today');
    expect(sent.last['offer'], true);
    await drain(t);
    d.dispose();
  });

  testWidgets('a customer who blocked the business: "This customer has stopped messages", no box', (t) async {
    phone(t);
    final d = await openThread(t, business(c: thread(stopped: true, offers: 'no')));
    expect(find.text('This customer has stopped messages'), findsOneWidget);
    expect(find.byType(EditableText), findsNothing);
    await drain(t);
    d.dispose();
  });

  testWidgets('the business blocks a customer from the menu, and unblocks', (t) async {
    phone(t);
    final api = business(c: thread(offers: 'no'));
    var blocked = false;
    api.routes['GET /v1/customer-chats/cc1'] = (_) => thread(offers: 'no', blocked: blocked);
    api.routes['POST /v1/customer-chats/cc1/block'] = (_) {
      blocked = true;
      return answer({}, 204);
    };
    api.routes['DELETE /v1/customer-chats/cc1/block'] = (_) {
      blocked = false;
      return answer({}, 204);
    };
    final d = await openThread(t, api);
    await press(t, find.bySemanticsLabel('More about this conversation'));
    await press(t, find.text('Block this customer'));
    await press(t, find.text('Block').last);
    expect(api.sent('POST', '/v1/customer-chats/cc1/block'), hasLength(1));
    expect(find.text('You blocked this customer. Unblock them to write to them.'), findsOneWidget);
    expect(find.byType(EditableText), findsNothing);
    await press(t, find.bySemanticsLabel('More about this conversation'));
    await press(t, find.text('Unblock this customer'));
    expect(api.sent('DELETE', '/v1/customer-chats/cc1/block'), hasLength(1));
    expect(find.byType(EditableText), findsOneWidget);
    await drain(t);
    d.dispose();
  });

  testWidgets('Your chat link: QR code, words to the share sheet, Make a new link; the settings saved', (t) async {
    phone(t, 2400);
    final api = Api();
    var code = 'k7m2q9x4pz';
    Map<String, dynamic> link({Map<String, dynamic>? settings}) => {
      'code': code,
      'url': 'https://app.taskiem.com/m/$code',
      'created_at': '2026-11-16T08:00:00Z',
      'words': 'Chat with Mama’s Kitchen on Taskiem: https://app.taskiem.com/m/$code',
      'qr': '/v1/customers/chat-link/qr',
      'settings': settings ?? {'takes_chats': true, 'storefront_button': 'both', 'link_in_updates': null, 'updates_link': true},
    };
    api.routes['GET /v1/customers/chat-link'] = (_) => link();
    api.routes['POST /v1/customers/chat-link'] = (_) {
      code = 'n3wc0de456';
      return link();
    };
    api.routes['GET /v1/customers/chat-link/qr'] = (r) {
      expect(r.url.queryParameters['format'], 'png');
      return answer({}, 404);
    };
    api.routes['PUT /v1/customers/chat-settings'] = (r) => {'takes_chats': api.body(r)['takes_chats'] ?? true, 'storefront_button': api.body(r)['storefront_button'] ?? 'both', 'link_in_updates': null, 'updates_link': true};
    api.routes['GET /v1/settings/customer-chats'] = (_) => {'closed_reply': true, 'closed_words': '', 'needs_you': true, 'tell_owner': true};
    api.routes['PUT /v1/settings/customer-chats'] = (r) => {'closed_reply': true, 'closed_words': api.body(r)['closed_words'] ?? '', 'needs_you': api.body(r)['needs_you'] ?? true, 'tell_owner': true};
    final shared = <String>[];
    final was = shareWords;
    shareWords = (w) async => shared.add(w);
    addTearDown(() => shareWords = was);

    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkPage(auth: api.auth(permissions: const ['storefront.manage', 'order.handle'], personal: false))));
    await settle(t);
    expect(find.text('https://app.taskiem.com/m/k7m2q9x4pz'), findsOneWidget);
    expect(api.sent('GET', '/v1/customers/chat-link/qr'), hasLength(1));
    await press(t, find.text('Share'));
    expect(shared, ['Chat with Mama’s Kitchen on Taskiem: https://app.taskiem.com/m/k7m2q9x4pz']);

    await press(t, find.text('Make a new link'));
    expect(find.textContaining('“This link no longer works” for 30 days'), findsOneWidget);
    await press(t, find.text('Make a new link').last);
    expect(api.sent('POST', '/v1/customers/chat-link'), hasLength(1));
    expect(find.text('https://app.taskiem.com/m/n3wc0de456'), findsOneWidget);

    await press(t, find.bySemanticsLabel('Take chats in the Taskiem app').last);
    expect(api.body(api.sent('PUT', '/v1/customers/chat-settings').single), {'takes_chats': false});
    await press(t, find.text('Chat with us, in Taskiem'));
    expect(api.body(api.sent('PUT', '/v1/customers/chat-settings').last), {'storefront_button': 'taskiem'});

    await t.enterText(find.byType(EditableText).last, 'Closed for prayers. Back at 2pm.');
    await t.pump();
    await press(t, find.text('Save the words'));
    expect(api.body(api.sent('PUT', '/v1/settings/customer-chats').single), {'closed_words': 'Closed for prayers. Back at 2pm.'});
    await press(t, find.bySemanticsLabel('Needs you').last);
    expect(api.body(api.sent('PUT', '/v1/settings/customer-chats').last), {'needs_you': false});
    await drain(t);
  });

  testWidgets('Your chat link without managing the shop: shown, not changed', (t) async {
    phone(t, 2000);
    final api = Api();
    api.routes['GET /v1/customers/chat-link'] = (_) => {
      'code': 'k7m2q9x4pz',
      'url': 'https://app.taskiem.com/m/k7m2q9x4pz',
      'created_at': '2026-11-16T08:00:00Z',
      'words': 'Chat with Mama’s Kitchen on Taskiem: https://app.taskiem.com/m/k7m2q9x4pz',
      'qr': '/v1/customers/chat-link/qr',
      'settings': {'takes_chats': true, 'storefront_button': 'both', 'link_in_updates': null, 'updates_link': false},
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkPage(auth: api.auth(permissions: const ['order.handle'], personal: false))));
    await settle(t);
    expect(find.text('Share'), findsOneWidget);
    expect(find.text('Make a new link'), findsNothing);
    expect(find.text('The owner, or someone who manages your shop, changes these.'), findsOneWidget);
    await expectPressable(t);
    await drain(t);
  });
}
