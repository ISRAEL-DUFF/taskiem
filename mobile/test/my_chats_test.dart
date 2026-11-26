// Taskiem for everyone, stage 2 in the app, the person's side
// (docs/spec/taskiem-for-everyone.md §7, §8, §13, §15): You's chats with
// businesses and the archived ones; a conversation (messages, the hours
// line, typing, read marks, the first chat's notice, the offers question,
// offers with Stop offers, a pay card); its menu (Mute, Block, Report with
// consent); its tools (their shop with sizes, few left and delivery, an
// order placed once with its Idempotency-Key; Book; Pay); a chat link (the
// card, the chat started, a link that no longer works, the way in signed
// out passing chat_code). Orders on You, Discover and saved addresses are
// test/you_stage3_test.dart's.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/links.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/app/route.dart';
import 'package:taskiem/chat/book_sheet.dart' show forgetServices;
import 'package:taskiem/chat/door.dart';
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/chat/shop.dart' show ShopBag;
import 'package:taskiem/kit/sheet.dart' show TkKitField;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/chat_link.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'guidelines.dart';
import 'support.dart' show loadFonts;

/// A stand-in server: answers by "METHOD /path", every request kept.
class Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  http.Client client() => MockClient((req) async {
    calls.add(req);
    final f = routes['${req.method} ${req.url.path}'];
    if (f == null) return http.Response(jsonEncode({'error': 'not here'}), 404, headers: _json);
    // A route may answer later (a Future): a test holds an answer back.
    var out = f(req);
    if (out is Future) out = await out;
    if (out is http.Response) return out;
    return http.Response(jsonEncode(out ?? {}), 200, headers: _json);
  });

  List<http.Request> sent(String method, String path) => calls.where((c) => c.method == method && c.url.path == path).toList();
  Map<String, dynamic> body(http.Request r) => jsonDecode(r.body) as Map<String, dynamic>;

  Auth auth({List<String> permissions = const [], bool personal = true}) {
    final a = Auth(
      transport: Transport(base: 'http://localhost', token: 'tok', client: client()),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': personal ? null : 't1',
      'tenant_name': personal ? null : 'Mama’s Kitchen',
      'personal': personal,
      'roles': <String>[],
      'permissions': permissions,
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Tolu Ade', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

const _json = {'content-type': 'application/json; charset=utf-8'};
http.Response answer(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: _json);

final at = DateTime.utc(2026, 11, 16, 10, 30);

Map<String, dynamic> chat({String offers = 'unasked', bool ask = true, bool blocked = false, bool blockedYou = false, bool typing = false, bool muted = false, bool archived = false, String id = 'c1', String name = 'Mama’s Kitchen', int unread = 2}) => {
  'id': id,
  'business': 'b1',
  'name': name,
  'last': 'Yes! Big pack today.',
  'last_at': '2026-11-16T10:00:00Z',
  'last_mine': false,
  'unread': unread,
  'muted': muted,
  'archived': archived,
  'blocked': blocked,
  'blocked_you': blockedYou,
  'offers': offers,
  'ask_offers': ask,
  'hours': {'open': true, 'line': 'Open now, closes at 18:00'},
  'typing': typing,
};

final messages = [
  {'id': 'm1', 'mine': true, 'kind': 'text', 'body': 'Hello, do you have jollof?', 'read_at': '2026-11-16T09:01:00Z', 'created_at': '2026-11-16T09:00:00Z'},
  {'id': 'm2', 'mine': false, 'kind': 'text', 'body': 'Yes! Big pack today.', 'sender_name': 'Bisi', 'read_at': null, 'created_at': '2026-11-16T09:05:00Z'},
  {'id': 'm3', 'mine': false, 'kind': 'text', 'body': 'Small chops, 20% off this weekend', 'offer': true, 'read_at': null, 'created_at': '2026-11-16T09:06:00Z'},
  {
    'id': 'm4',
    'mine': false,
    'kind': 'card',
    'body': 'Pay for MK-1042',
    'card': {'type': 'pay', 'reference': 'MK-1042', 'title': 'Pay for MK-1042', 'total_kobo': 450000, 'url': 'https://pay.example/MK-1042'},
    'read_at': null,
    'created_at': '2026-11-16T09:07:00Z',
  },
];

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 600));
}

/// The screen let go, and the toasts' timers run out.
Future<void> drain(WidgetTester t) async {
  await t.pumpWidget(const SizedBox());
  await t.pump(const Duration(seconds: 5));
}

Future<void> press(WidgetTester t, Finder f) async {
  await t.ensureVisible(f);
  await t.pump();
  await t.tap(f);
  await settle(t);
}

Future<void> typeIn(WidgetTester t, Finder field, String text) async {
  final box = find.descendant(of: field, matching: find.byType(EditableText));
  await t.enterText(box.first, text);
  await t.pump();
}

void phone(WidgetTester t, [double height = 1400]) {
  t.view.physicalSize = Size(390, height);
  t.view.devicePixelRatio = 1;
  addTearDown(t.view.reset);
}

/// A conversation with Mama's Kitchen, served.
Api conversation({Map<String, dynamic>? c}) {
  final api = Api();
  api.routes['GET /v1/me/chats/c1'] = (_) => c ?? chat();
  api.routes['GET /v1/me/chats/c1/messages'] = (_) => {'messages': messages, 'more': false};
  api.routes['POST /v1/me/chats/c1/read'] = (_) => answer({}, 204);
  return api;
}

Future<MyChatData> openChat(WidgetTester t, Api api, {String? notice, String draft = ''}) async {
  final d = MyChatData(api.auth(), 'c1', follow: false, notice: notice, now: () => at);
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MyChatScreen(data: d, draft: draft)));
  await settle(t);
  return d;
}

void main() {
  setUpAll(loadFonts);

  test('a chat link opens the app: /m/{code}', () {
    expect(appPageOf(Uri.parse('$taskiemUrl/m/k7m2q9x4pz')), '/m/k7m2q9x4pz');
    expect(appPageOf(Uri.parse('$taskiemUrl/m/k7m2q9x4pz/')), '/m/k7m2q9x4pz');
    expect(appPageOf(Uri.parse('$taskiemUrl/m/../x')), isNull);
    expect(chatCodeOf('/m/k7m2q9x4pz'), 'k7m2q9x4pz');
    expect(chatCodeOf('/chat'), isNull);
  });

  testWidgets('You: chats with businesses, unread, archived apart; a row opens the conversation', (t) async {
    phone(t);
    final api = conversation();
    api.routes['GET /v1/me/chats'] = (r) => r.url.queryParameters['archived'] == '1'
        ? {'chats': [chat(id: 'c2', name: 'Bola Stores', archived: true, unread: 0)], 'unread': 0}
        : {'chats': [chat()], 'unread': 2};
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    api.routes['GET /v1/me/storefront-orders'] = (_) => {'orders': <Object>[], 'waiting': false, 'waiting_count': 0};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at)));
    await settle(t);
    await looks(t, 'you-chats');
    expect(find.text('Mama’s Kitchen'), findsOneWidget);
    expect(find.text('Yes! Big pack today.'), findsOneWidget);
    expect(find.text('2'), findsNWidgets(2), reason: 'two unread, on the row and on the Chats tab');
    expect(find.text('Archived · 1'), findsOneWidget, reason: 'the Archived chip, with its count');
    expect(find.text('Bola Stores'), findsNothing, reason: 'archived chats are apart');
    expect(find.text('No chats yet'), findsNothing);

    await press(t, find.text('Mama’s Kitchen'));
    expect(find.text('Open now, closes at 18:00'), findsOneWidget);
    expect(find.text('Hello, do you have jollof?'), findsOneWidget);
    expect(api.sent('POST', '/v1/me/chats/c1/read'), hasLength(1), reason: 'their messages read as shown');
    await drain(t);
  });

  testWidgets('a conversation: the notice once, the offers question, an offer and Stop offers, typing', (t) async {
    phone(t);
    final api = conversation();
    api.routes['PUT /v1/me/chats/c1'] = (r) {
      final b = api.body(r);
      return chat(offers: b['offers'] == true ? 'yes' : (b['offers'] == false ? 'no' : 'unasked'), ask: false);
    };
    await openChat(t, api, notice: 'Mama’s Kitchen will see your name and phone number, for your orders and deliveries. Messages with businesses can be read by Taskiem to help with your orders.');
    expect(find.textContaining('will see your name and phone number'), findsOneWidget);
    await press(t, find.text('OK'));
    expect(find.textContaining('will see your name and phone number'), findsNothing);

    // Asked once: Yes gets offers, recorded with the business.
    expect(find.text('Get offers from Mama’s Kitchen?'), findsOneWidget);
    expect(find.text('Offer'), findsOneWidget);
    expect(find.text('Stop offers from Mama’s Kitchen'), findsNothing, reason: 'no yes yet');
    await press(t, find.text('Yes'));
    expect(api.body(api.sent('PUT', '/v1/me/chats/c1').single), {'offers': true});
    expect(find.text('Get offers from Mama’s Kitchen?'), findsNothing);
    expect(find.text('Stop offers from Mama’s Kitchen'), findsOneWidget);
    await press(t, find.text('Stop offers from Mama’s Kitchen'));
    expect(api.body(api.sent('PUT', '/v1/me/chats/c1').last), {'offers': false});
    expect(find.text('Stop offers from Mama’s Kitchen'), findsNothing);

    // Their words, cards and read ticks.
    expect(find.text('Yes! Big pack today.'), findsOneWidget);
    expect(find.text('Pay for MK-1042'), findsOneWidget);
    expect(find.text('₦4,500 due'), findsOneWidget);
    expect(find.bySemanticsLabel('Read'), findsOneWidget);

    // They are typing.
    api.routes['GET /v1/me/chats/c1'] = (_) => chat(typing: true, offers: 'no', ask: false);
    await t.runAsync(() => (t.state(find.byType(MyChatScreen)) as dynamic).widget.data.refresh() as Future<void>);
    await settle(t);
    expect(find.text('typing…'), findsOneWidget);
    await drain(t);
  });

  testWidgets('writing: shown at once, sent with its client id; typing told; a refused message gives the words back', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['POST /v1/me/chats/c1/typing'] = (_) => answer({}, 204);
    var fail = false;
    api.routes['POST /v1/me/chats/c1/messages'] = (r) {
      if (fail) return answer({'error': 'You blocked this business. Unblock it to write to them.', 'you_blocked': true}, 409);
      final b = api.body(r);
      return answer({'id': 'm9', 'mine': true, 'kind': 'text', 'body': b['text'], 'read_at': null, 'created_at': '2026-11-16T10:29:00Z'}, 201);
    };
    await openChat(t, api);
    await looks(t, 'business-chat');
    await t.enterText(find.byType(EditableText), 'Two packs please');
    await t.pump();
    expect(api.sent('POST', '/v1/me/chats/c1/typing'), hasLength(1));
    await press(t, find.bySemanticsLabel('Send'));
    final sent = api.body(api.sent('POST', '/v1/me/chats/c1/messages').single);
    expect(sent['text'], 'Two packs please');
    expect(sent['client_id'], isNotEmpty);
    expect(find.text('Two packs please'), findsOneWidget);

    fail = true;
    await t.enterText(find.byType(EditableText), 'And a drink');
    await t.pump();
    await press(t, find.bySemanticsLabel('Send'));
    expect(find.text('You blocked this business. Unblock it to write to them.'), findsOneWidget);
    expect((t.widget(find.byType(EditableText)) as EditableText).controller.text, 'And a drink');
    await drain(t);
  });

  testWidgets('the menu: Mute, Block (nothing deleted) then Unblock, Report with consent', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['PUT /v1/me/chats/c1'] = (r) => chat(offers: 'no', ask: false, muted: api.body(r)['muted'] == true);
    api.routes['POST /v1/me/chats/c1/block'] = (_) => chat(offers: 'no', ask: false, blocked: true);
    api.routes['DELETE /v1/me/chats/c1/block'] = (_) => chat(offers: 'no', ask: false);
    api.routes['POST /v1/me/chats/c1/report'] = (_) => answer({'id': 'r1', 'messages_sent': true, 'said': 'Thank you. Taskiem’s moderators will look at this conversation. You can also block this business.'}, 201);
    await openChat(t, api);

    Future<void> menu(String item) async {
      await press(t, find.bySemanticsLabel('More about this conversation'));
      await press(t, find.text(item));
    }

    await menu('Mute notifications');
    expect(api.body(api.sent('PUT', '/v1/me/chats/c1').single), {'muted': true});

    await menu('Block Mama’s Kitchen');
    expect(find.textContaining('Nothing is deleted'), findsOneWidget);
    await press(t, find.text('Block').last);
    expect(api.sent('POST', '/v1/me/chats/c1/block'), hasLength(1));
    expect(find.textContaining('You blocked Mama’s Kitchen.'), findsOneWidget);
    expect(find.byType(EditableText), findsNothing, reason: 'no box while blocked');
    await press(t, find.text('Unblock'));
    expect(api.sent('DELETE', '/v1/me/chats/c1/block'), hasLength(1));
    expect(find.byType(EditableText), findsOneWidget);

    await menu('Report Mama’s Kitchen');
    await press(t, find.text('Send report'));
    expect(find.text('Choose why you are reporting them.'), findsOneWidget);
    await press(t, find.text('A scam or fraud'));
    await press(t, find.text('Send report'));
    expect(find.textContaining('Agree to send the last 20 messages'), findsOneWidget);
    expect(api.sent('POST', '/v1/me/chats/c1/report'), isEmpty, reason: 'nothing sent without consent');
    await press(t, find.textContaining('Send the last 20 messages of this chat'));
    await press(t, find.text('Send report'));
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/report').single), {'reason': 'scam', 'consent': true});
    expect(find.textContaining('Taskiem’s moderators will look at this conversation'), findsOneWidget);
    await drain(t);
  });

  testWidgets('their shop: sizes and colours, few left, delivery; an order placed once with its Idempotency-Key', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
      'open': true,
      'payment': 'after_accept',
      'pickup': true,
      'delivery': true,
      'zones': [
        {'name': 'Yaba', 'fee_kobo': 100000},
      ],
      'items': [
        {'id': 'i1', 'name': 'Jollof rice', 'price_kobo': 250000, 'available': true, 'few_left': 3},
        {
          'id': 'i2',
          'name': 'Ankara shirt',
          'price_kobo': 1500000,
          'available': true,
          'sizes': [
            {'id': 'i2-m', 'variant': {'size': 'M', 'colour': 'Red'}, 'available': true},
            {'id': 'i2-l', 'variant': {'size': 'L', 'colour': 'Red'}, 'available': false},
          ],
        },
      ],
    };
    var tries = 0;
    api.routes['POST /v1/me/chats/c1/orders'] = (r) {
      tries++;
      if (tries == 1) return answer({'error': 'Too many tries. Wait a moment and try again.'}, 429);
      return answer({
        'order': {'reference': 'MK-1050', 'kind': 'app', 'status': 'sent', 'summary': '2 × Jollof rice', 'total_kobo': 600000, 'due_kobo': 0, 'pay': false, 'created_at': '2026-11-16T10:30:00Z'},
      }, 201);
    };
    await openChat(t, api);
    await press(t, find.bySemanticsLabel('Chat tools'));
    expect(find.text('With Mama’s Kitchen', findRichText: true), findsOneWidget);
    await press(t, find.text('Their shop'));
    // The shop's screen: the items as photos, their marks and prices.
    expect(find.bySemanticsLabel('Jollof rice, ₦2,500, Only 3 left'), findsOneWidget);
    expect(find.bySemanticsLabel('Ankara shirt, ₦15,000'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Add Jollof rice to the bag'));
    await press(t, find.bySemanticsLabel('One more Jollof rice'));
    expect(find.bySemanticsLabel('View bag: 2 items · ₦5,000'), findsOneWidget);
    // Sizes are chosen on the item's own screen: L is gone.
    await press(t, find.bySemanticsLabel('Ankara shirt, ₦15,000'));
    expect(find.text('M · Red · ₦15,000'), findsOneWidget);
    expect(find.text('L · Red · ₦15,000'), findsOneWidget);
    expect(find.bySemanticsLabel('Add to bag'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Back to the shop'));
    await press(t, find.bySemanticsLabel('View bag: 2 items · ₦5,000'));
    expect(find.text('Your bag'), findsOneWidget);
    expect(find.text('₦2,500 each'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Check out, ₦5,000'));
    expect(find.text('2 × Jollof rice'), findsOneWidget);
    expect(find.text('You pay once Mama’s Kitchen accepts your order. They’ll send the pay link here.'), findsOneWidget);
    await press(t, find.text('Deliver it to me'));
    await press(t, find.textContaining('Place order'));
    expect(find.text('Choose where to deliver to.'), findsOneWidget);
    await press(t, find.text('Yaba · ₦1,000'));
    await typeIn(t, find.ancestor(of: find.text('Address'), matching: find.byType(TkKitField)), '12 Herbert Macaulay Way');
    await press(t, find.text('Place order · ₦6,000'));
    expect(find.text('Too many tries. Wait a moment and try again.'), findsOneWidget);
    await press(t, find.text('Place order · ₦6,000'));
    final placed = api.sent('POST', '/v1/me/chats/c1/orders');
    expect(placed, hasLength(2));
    expect(placed[0].headers['Idempotency-Key'], placed[1].headers['Idempotency-Key'], reason: 'tried again, one key');
    expect(api.body(placed[1]), {
      'lines': [
        {'item_id': 'i1', 'quantity': 2},
      ],
      'fulfilment': 'delivery',
      'zone': 'Yaba',
      'address': '12 Herbert Macaulay Way',
    });
    expect(find.text('Order MK-1050 sent to Mama’s Kitchen'), findsOneWidget);
    // Placed: the bag emptied, and back to the chat.
    await t.pump(const Duration(seconds: 3));
    await t.pumpAndSettle();
    expect(find.text('Your bag'), findsNothing);
    expect(find.bySemanticsLabel(RegExp('^View bag')), findsNothing);
    expect(ShopBag.of('c1').count, 0);
    await drain(t);
  });

  testWidgets('Pay: what is due, its pay link opened in the app; a pay card opens its link', (t) async {
    phone(t);
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['GET /v1/me/chats/c1/orders'] = (_) => {
      'orders': [
        {'reference': 'MK-1042', 'kind': 'app', 'status': 'accepted', 'summary': '1 × Jollof rice', 'total_kobo': 450000, 'due_kobo': 450000, 'pay': true, 'created_at': '2026-11-15T10:00:00Z'},
        {'reference': 'MK-1001', 'kind': 'app', 'status': 'completed', 'summary': '3 × Puff puff', 'total_kobo': 90000, 'due_kobo': 0, 'pay': false, 'created_at': '2026-11-01T10:00:00Z'},
      ],
    };
    api.routes['GET /v1/me/chats/c1/orders/MK-1042/pay'] = (_) => {'reference': 'MK-1042', 'due_kobo': 450000, 'url': 'https://app.taskiem.com/o/abc'};
    await openChat(t, api);
    await press(t, find.bySemanticsLabel('Chat tools'));
    await press(t, find.text('Pay').last);
    expect(find.textContaining('Pay Mama’s Kitchen', findRichText: true), findsOneWidget);
    expect(find.text('3 × Puff puff'), findsNothing, reason: 'only what is due');
    await press(t, find.bySemanticsLabel('Pay ₦4,500 for MK-1042'));
    expect(opened, [Uri.parse('https://app.taskiem.com/o/abc')]);
    await press(t, find.bySemanticsLabel('Close').last);

    await press(t, find.text('Pay').first);
    expect(opened.last, Uri.parse('https://pay.example/MK-1042'));
    await drain(t);
  });

  testWidgets('Book as a customer: their services and free times, no phone asked, one Idempotency-Key', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['GET /v1/me/chats/c1/services'] = (_) => {
      'open': true,
      'services': [
        {'id': 's1', 'name': 'Braids', 'minutes': 90, 'price_kobo': 800000},
      ],
    };
    api.routes['GET /v1/me/chats/c1/services/s1/times'] = (_) => {
      'day': '2026-11-17',
      'days': ['2026-11-17'],
      'service': {'id': 's1', 'name': 'Braids', 'minutes': 90},
      'times': ['2026-11-17T09:00:00Z'],
    };
    api.routes['POST /v1/me/chats/c1/bookings'] = (_) => answer({
      'booking': {'reference': 'MK-B7', 'service_id': 's1', 'service_name': 'Braids', 'starts_at': '2026-11-17T09:00:00Z', 'ends_at': '2026-11-17T10:30:00Z'},
    }, 201);
    await openChat(t, api);
    await press(t, find.bySemanticsLabel('Chat tools'));
    await press(t, find.text('Book'));
    await press(t, find.text('Braids'));
    await press(t, find.text('10:00'));
    await press(t, find.textContaining('at 10:00'));
    expect(find.text('Phone number they can reach you on'), findsNothing);
    await press(t, find.text('Book').last);
    final b = api.sent('POST', '/v1/me/chats/c1/bookings').single;
    expect(api.body(b), {'service_id': 's1', 'start': '2026-11-17T09:00:00Z', 'note': ''});
    expect(b.headers['Idempotency-Key'], startsWith('book-'));
    await drain(t);
  });

  testWidgets('a chat link: the business\'s card, the chat started from it, the notice said once', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'unasked', ask: true, unread: 0));
    api.routes['GET /v1/chat-links/k7m2q9x4pz'] = (_) => {
      'name': 'Mama’s Kitchen',
      'tagline': 'Jollof, small chops and cakes',
      'area': 'Yaba, Lagos',
      'open_now': true,
      'hours': {
        'days': [
          [
            {'open': '09:00', 'close': '18:00'},
          ],
          [],
          [],
          [],
          [],
          [],
          [],
        ],
      },
      'takes_chats': true,
      'code': 'k7m2q9x4pz',
      'link': 'https://app.taskiem.com/m/k7m2q9x4pz',
      'words': 'Chat with Mama’s Kitchen on Taskiem: https://app.taskiem.com/m/k7m2q9x4pz',
    };
    api.routes['POST /v1/me/chats'] = (_) => answer({...chat(unread: 0), 'notice': 'Mama’s Kitchen will see your name and phone number, for your orders and deliveries.'}, 201);
    final auth = api.auth();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkScreen(auth: auth, code: 'k7m2q9x4pz')));
    await settle(t);
    expect(find.text('Jollof, small chops and cakes'), findsOneWidget);
    expect(find.text('Open now'), findsOneWidget);
    expect(find.text('09:00–18:00'), findsOneWidget);
    expect(find.text('Closed'), findsNWidgets(6));
    await press(t, find.text('Chat with Mama’s Kitchen'));
    expect(api.body(api.sent('POST', '/v1/me/chats').single), {'code': 'k7m2q9x4pz'});
    expect(find.text('Mama’s Kitchen will see your name and phone number, for your orders and deliveries.'), findsOneWidget);
    expect(find.text('Get offers from Mama’s Kitchen?'), findsOneWidget);
    await drain(t);
  });

  testWidgets('a link that was replaced: "This link no longer works"', (t) async {
    phone(t);
    final api = Api();
    api.routes['GET /v1/chat-links/oldcode123'] = (_) => answer({'gone': true, 'error': 'This link no longer works. Ask the business for its new link.'}, 410);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkScreen(auth: api.auth(), code: 'oldcode123')));
    await settle(t);
    expect(find.text('This link no longer works'), findsOneWidget);
    expect(find.text('Ask the business for its new link.'), findsOneWidget);
    expect(find.textContaining('Chat with'), findsNothing);
    await drain(t);
  });

  testWidgets('signed out, a chat link: its card over the way in, and chat_code sent with the phone', (t) async {
    phone(t);
    final api = Api();
    api.routes['GET /v1/chat-links/k7m2q9x4pz'] = (_) => {'name': 'Mama’s Kitchen', 'takes_chats': true, 'code': 'k7m2q9x4pz', 'link': 'https://app.taskiem.com/m/k7m2q9x4pz', 'words': ''};
    api.routes['POST /v1/auth/phone/start'] = (_) => answer({'sent_via': 'whatsapp', 'expires_in': 600}, 202);
    final auth = api.auth()..state = AuthState.signedOut;
    final links = LinkInbox(links: const Stream.empty(), first: () async => null);
    links.receive(Uri.parse('$taskiemUrl/m/k7m2q9x4pz'));
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: auth, links: links));
    await settle(t);
    expect(find.text('Sign in to chat with Mama’s Kitchen.'), findsOneWidget);
    expect(find.text('Welcome to Taskiem'), findsOneWidget);
    await typeIn(t, find.byKey(const ValueKey('phone')), '0803 123 0000');
    await press(t, find.text('Continue'));
    expect(api.body(api.sent('POST', '/v1/auth/phone/start').single), {'phone': '+2348031230000', 'chat_code': 'k7m2q9x4pz'});
    expect(links.shownSignedOut, isTrue, reason: 'the chat starts at once once signed in');
    expect(links.waiting, isTrue, reason: 'the link waits behind signing in');
    await drain(t);
  });

  testWidgets('a conversation and its shop can be pressed: words, actions, 48 by 48; large text cuts nothing off', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'yes', ask: false));
    api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
      'open': true,
      'payment': 'at_order',
      'pickup': true,
      'delivery': false,
      'zones': <Object>[],
      'items': [
        {'id': 'i1', 'name': 'Jollof rice', 'price_kobo': 250000, 'available': true, 'few_left': 3},
      ],
    };
    await openChat(t, api);
    await expectPressable(t);
    await press(t, find.bySemanticsLabel('Chat tools'));
    await press(t, find.text('Their shop'));
    await expectPressable(t);
    await drain(t);
    final d = MyChatData(api.auth(), 'c1', follow: false, now: () => at);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: MyChatScreen(data: d)), drive: settle);
    await drain(t);
  });

  test('a push for You opens the person\'s chat over the chats: /you/chats/{id}', () {
    final r = routeOf('/you/chats/c1');
    expect(r.screen, RouteScreen.you);
    expect(r.id, 'c1');
    expect(routeOf('/you/chats/../x').screen, isNot(RouteScreen.you));
  });

  testWidgets('the head: their photo, what they proved in plain words; the transfer warning; the offers question passed over', (t) async {
    phone(t);
    final api = conversation(c: {
      ...chat(),
      'photo': {'urls': {'96': 'https://cdn.example/logo-96.webp', '256': 'https://cdn.example/logo-256.webp'}},
      'verified': {'phone': true, 'business': false, 'payments': true, 'months': 8},
    });
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {
      'messages': [
        ...messages,
        {'id': 'm5', 'mine': false, 'kind': 'text', 'body': 'Pay to 0123456789, GTBank, Bisi Ade', 'warn_transfer': true, 'read_at': null, 'created_at': '2026-11-16T09:08:00Z'},
      ],
      'more': false,
    };
    api.routes['PUT /v1/me/chats/c1'] = (r) => {...chat(ask: false), 'verified': {'phone': true, 'business': false, 'payments': true, 'months': 8}};
    await openChat(t, api);
    expect(find.bySemanticsLabel('Phone checked · Payments through Taskiem · Trading 8 months'), findsOneWidget);
    expect(find.textContaining('Business checked'), findsNothing, reason: 'only what is so');
    expect(find.byType(Image), findsWidgets, reason: 'their photo in the head');
    expect(photoUrl({'96': 'https://cdn.example/logo-96.webp', '256': 'https://cdn.example/logo-256.webp'}, width: 80), 'https://cdn.example/logo-96.webp');
    expect(photoUrl({'96': 'https://cdn.example/logo-96.webp', '256': 'https://cdn.example/logo-256.webp'}, width: 200), 'https://cdn.example/logo-256.webp');
    expect(photoUrl({'96': 'javascript:alert(1)'}), isNull);
    expect(verifiedWords(const PersonChatVerified(phone: false, business: true, payments: false, months: 0)), 'Business checked · New on Taskiem');

    // The warning under their message asking for a transfer.
    expect(find.bySemanticsLabel('Warning: $transferWarning'), findsOneWidget);
    expect(find.text(transferWarning), findsOneWidget);

    // Passed over: not asked again.
    expect(find.text('Get offers from Mama’s Kitchen?'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Not now'));
    expect(api.body(api.sent('PUT', '/v1/me/chats/c1').single), {'offers_asked': true});
    expect(find.text('Get offers from Mama’s Kitchen?'), findsNothing);
    await drain(t);
  });

  testWidgets('an answer about offers the business did not take is said', (t) async {
    phone(t);
    final api = conversation();
    api.routes['PUT /v1/me/chats/c1'] = (_) => {...chat(ask: false), 'offers_refused': true};
    await openChat(t, api);
    await press(t, find.text('Yes'));
    expect(find.textContaining('your answer about offers was not saved'), findsOneWidget);
    await drain(t);
  });

  testWidgets('invoice and delivery cards; Your orders follows the rider; a booking offer opens that service', (t) async {
    phone(t);
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {
      'messages': [
        {
          'id': 'k1', 'mine': false, 'kind': 'card', 'body': 'Invoice INV-0007', 'read_at': null, 'created_at': '2026-11-16T09:00:00Z',
          'card': {'type': 'invoice', 'invoice_id': 'inv1', 'reference': 'INV-0007', 'title': 'Invoice INV-0007', 'status': 'sent', 'total_kobo': 1250000, 'line': 'Catering for 40', 'url': 'https://app.taskiem.com/i/xyz'},
        },
        {
          'id': 'k2', 'mine': false, 'kind': 'card', 'body': 'Delivery of MK-1042', 'read_at': null, 'created_at': '2026-11-16T09:01:00Z',
          'card': {'type': 'delivery', 'reference': 'MK-1042', 'title': 'Delivery of MK-1042', 'status': 'picked_up', 'line': 'On its way to you', 'delivery': {'status': 'picked_up', 'line': 'On its way to you', 'accepted_at': '2026-11-16T08:40:00Z', 'picked_up_at': '2026-11-16T08:55:00Z'}},
        },
        {
          'id': 'k3', 'mine': false, 'kind': 'card', 'body': 'Braids', 'read_at': null, 'created_at': '2026-11-16T09:02:00Z',
          'card': {'type': 'booking', 'service_id': 's2', 'title': 'Braids', 'minutes': 90, 'total_kobo': 800000},
        },
      ],
      'more': false,
    };
    api.routes['GET /v1/me/chats/c1/orders'] = (_) => {
      'orders': [
        {'reference': 'MK-1042', 'kind': 'app', 'status': 'dispatched', 'summary': '1 × Jollof rice', 'total_kobo': 450000, 'due_kobo': 0, 'pay': false, 'created_at': '2026-11-15T10:00:00Z',
          'delivery': {'status': 'picked_up', 'line': 'On its way to you'}},
      ],
    };
    api.routes['GET /v1/me/chats/c1/services'] = (_) => {
      'open': true,
      'services': [
        {'id': 's1', 'name': 'Cornrows', 'minutes': 60, 'price_kobo': 500000},
        {'id': 's2', 'name': 'Braids', 'minutes': 90, 'price_kobo': 800000},
      ],
    };
    api.routes['GET /v1/me/chats/c1/services/s2/times'] = (_) => {
      'day': '2026-11-17',
      'days': ['2026-11-17'],
      'service': {'id': 's2', 'name': 'Braids', 'minutes': 90},
      'times': ['2026-11-17T09:00:00Z'],
    };
    // Their services as read in another test are not these.
    forgetServices();
    await openChat(t, api);
    expect(find.text('₦12,500 due'), findsOneWidget);
    expect(find.text('To pay'), findsOneWidget);
    expect(find.text('On its way to you'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('Picked up: step 2 of 4')), findsOneWidget);

    // Pay offers its ways (the payments flow, part 1): by transfer first,
    // the invoice's page under it.
    await press(t, find.text('Pay'));
    await t.pumpAndSettle();
    expect(find.text('Pay by transfer'), findsOneWidget);
    await press(t, find.text('Pay on the invoice’s page'));
    expect(opened, [Uri.parse('https://app.taskiem.com/i/xyz')]);

    await press(t, find.text('View delivery'));
    expect(find.textContaining('On its way to you', findRichText: true), findsWidgets);
    expect(find.text('On its way'), findsNWidgets(2), reason: 'the rider\'s step as its status, on the card and the row');
    await press(t, find.bySemanticsLabel('Close').last);

    // Book on the offer: that service's times at once, not the list.
    await press(t, find.text('Book'));
    expect(api.sent('GET', '/v1/me/chats/c1/services/s2/times'), hasLength(1));
    expect(find.text('Cornrows'), findsNothing);
    expect(find.text('10:00'), findsOneWidget);
    await drain(t);
  });

  testWidgets('erased by the business: the box goes and it is said plainly; typing is not told while blocked', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['POST /v1/me/chats/c1/typing'] = (_) => answer({}, 204);
    api.routes['POST /v1/me/chats/c1/messages'] = (_) => answer({'error': 'This business erased your details from its records, so this conversation is closed.'}, 409);
    await openChat(t, api);
    await t.enterText(find.byType(EditableText), 'Hello?');
    await t.pump();
    await press(t, find.bySemanticsLabel('Send'));
    expect(find.text('This business erased your details from its records, so this conversation is closed.'), findsOneWidget);
    expect(find.byType(EditableText), findsNothing, reason: 'nothing more can be sent');
    await drain(t);

    final blocked = conversation(c: chat(offers: 'no', ask: false, blockedYou: true));
    blocked.routes['POST /v1/me/chats/c1/typing'] = (_) => answer({'error': 'This business is not taking messages from you.', 'blocked': true}, 403);
    final d = await openChat(t, blocked);
    d.typing();
    await settle(t);
    expect(blocked.sent('POST', '/v1/me/chats/c1/typing'), isEmpty);
    expect(find.text('Mama’s Kitchen is not taking messages from you.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('a message whose answer never came goes again with the same client id', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    var lost = true;
    api.routes['POST /v1/me/chats/c1/messages'] = (r) {
      if (lost) return answer({'error': 'Bad gateway'}, 502);
      final b = api.body(r);
      return answer({'id': 'm9', 'mine': true, 'kind': 'text', 'body': b['text'], 'read_at': null, 'created_at': '2026-11-16T10:29:00Z'}, 201);
    };
    await openChat(t, api);
    await t.enterText(find.byType(EditableText), 'Two packs please');
    await t.pump();
    await press(t, find.bySemanticsLabel('Send'));
    expect((t.widget(find.byType(EditableText)) as EditableText).controller.text, 'Two packs please', reason: 'given back');
    lost = false;
    await press(t, find.bySemanticsLabel('Send'));
    final sent = api.sent('POST', '/v1/me/chats/c1/messages');
    expect(sent, hasLength(2));
    expect(api.body(sent[0])['client_id'], api.body(sent[1])['client_id']);
    // Other words: a new id.
    await t.enterText(find.byType(EditableText), 'And a drink');
    await t.pump();
    await press(t, find.bySemanticsLabel('Send'));
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/messages').last)['client_id'], isNot(api.body(sent[0])['client_id']));
    await drain(t);
  });
}
