// A film: a customer orders and pays in the chat (Episode 4 of "Taskiem for
// you"). Tobi opens his chat with Ada's Kitchen from You, opens the + (Chat
// tools), Their shop, adds 2 jollof rice and a zobo, reviews, picks delivery
// and the area, types the address, places the order. Ada's order card and pay
// card come into the chat; he taps Pay. Set-up from my_chats_test.dart (their
// shop, order placed, Pay). Skipped unless VIDEO_DIR is set.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/kit/sheet.dart' show TkKitField;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_you_ep34_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: order and pay in the chat', (t) async {
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);

    final api = YouApi();
    var chat = adaChat(offers: 'no', ask: false, last: 'Zobo is ₦1,500. Tell me what you would like.');
    var messages = <Map<String, dynamic>>[
      textMsg('m1', true, 'Good afternoon, do you have jollof rice today?', '12:59'),
      textMsg('m2', false, 'Good afternoon Tobi! Yes, jollof rice is ₦3,500 a pack.', '13:00'),
      textMsg('m3', false, 'Zobo is ₦1,500. Tell me what you would like.', '13:00'),
    ];
    var orders = <Map<String, dynamic>>[];
    api.routes['GET /v1/me/chats'] = (r) => r.url.queryParameters['archived'] == '1' ? {'chats': <Object>[], 'unread': 0} : {'chats': [chat], 'unread': 0};
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    api.routes['GET /v1/me/storefront-orders'] = (_) => {'orders': <Object>[], 'waiting': false, 'waiting_count': 0};
    api.routes['GET /v1/me/chats/c1'] = (_) => chat;
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {'messages': messages, 'more': false};
    api.routes['POST /v1/me/chats/c1/read'] = (_) => answer({}, 204);
    api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
      'open': true,
      'payment': 'after_accept',
      'pickup': true,
      'delivery': true,
      'zones': [
        {'name': 'Yaba', 'fee_kobo': 150000},
        {'name': 'Surulere', 'fee_kobo': 250000},
      ],
      'items': [
        {'id': 'i1', 'name': 'Jollof rice', 'price_kobo': 350000, 'available': true},
        {'id': 'i2', 'name': 'Zobo', 'price_kobo': 150000, 'available': true},
        {'id': 'i3', 'name': 'Small chops', 'price_kobo': 200000, 'available': true},
      ],
    };
    api.routes['POST /v1/me/chats/c1/orders'] = (_) {
      orders = [
        {'reference': 'AK-1052', 'kind': 'app', 'status': 'sent', 'summary': '2 × Jollof rice, 1 × Zobo', 'total_kobo': 1000000, 'due_kobo': 0, 'pay': false, 'created_at': '2026-11-24T13:00:00Z'},
      ];
      return answer({'order': orders.first}, 201);
    };
    api.routes['GET /v1/me/chats/c1/orders'] = (_) => {'orders': orders};
    api.routes['GET /v1/me/chats/c1/orders/AK-1052/pay'] = (_) => {'reference': 'AK-1052', 'due_kobo': 1000000, 'url': 'https://app.taskiem.com/o/ak1052'};

    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => filmNow)));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'you-order-pay');
    film.say('On the Chats tab, Tobi opens his chat with Ada’s Kitchen.', who: 'Tobi · Ada’s Kitchen');
    await film.hold(3.0);
    await film.tap(find.text('Ada’s Kitchen'), then: 0.8);
    await film.settle();
    await film.hold(1.5);
    film.say('He taps the button by the box to open the chat tools.');
    await film.hold(2.0);
    await film.tap(find.bySemanticsLabel('Chat tools'), then: 0.8);
    await film.settle();
    await film.hold(1.5);
    film.say('He taps Their shop.');
    await film.hold(1.0);
    await film.tap(find.text('Their shop'), then: 0.8);
    await film.settle();
    await film.hold(1.5);
    film.say('He taps plus twice on Jollof rice, then once on Zobo.');
    await film.hold(1.0);
    await film.tap(find.bySemanticsLabel('One more Jollof rice'), then: 0.5);
    await film.tap(find.bySemanticsLabel('One more Jollof rice'), then: 0.5);
    await film.tap(find.bySemanticsLabel('One more Zobo'), then: 1.0);
    film.say('The bar shows 3 items, ₦8,500. He taps Review order.');
    await film.hold(2.5);
    await film.tap(find.bySemanticsLabel('Review order'), then: 0.8);
    await film.settle();
    await film.hold(1.5);
    film.say('He chooses Deliver it to me.');
    await film.hold(1.0);
    await film.tap(find.text('Deliver it to me'), then: 0.8);
    await film.hold(1.0);
    film.say('He picks his area, Yaba, with its delivery fee.');
    await t.ensureVisible(find.text('Yaba · ₦1,500'));
    await t.pump();
    await film.hold(1.0);
    await film.tap(find.text('Yaba · ₦1,500'), then: 1.2);
    film.say('He types the address.');
    final addr = find.descendant(of: find.ancestor(of: find.text('Address'), matching: find.byType(TkKitField)), matching: find.byType(EditableText));
    await t.ensureVisible(addr);
    await t.pump();
    await film.hold(0.8);
    await film.tap(addr, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(addr, '12 Herbert Macaulay Way', perKey: 0.08);
    await film.hold(0.6);
    await film.keyboard(false);
    await film.hold(0.8);
    film.say('The total is ₦10,000. He taps Place order.');
    await film.hold(2.5);
    await film.tap(find.text('Place order · ₦10,000'), then: 0.3);
    for (var i = 0; i < 3; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    film.say('The order goes to Ada’s Kitchen. Now he waits for her.');
    await film.play(3.0);
    await t.pumpAndSettle();

    // Ada accepts: her order card and her pay card arrive in the chat.
    orders = [
      {'reference': 'AK-1052', 'kind': 'app', 'status': 'accepted', 'summary': '2 × Jollof rice, 1 × Zobo', 'total_kobo': 1000000, 'due_kobo': 1000000, 'pay': true, 'created_at': '2026-11-24T13:00:00Z'},
    ];
    messages = [
      ...messages,
      {
        'id': 'k1',
        'mine': false,
        'kind': 'card',
        'body': 'Order AK-1052',
        'read_at': null,
        'created_at': '2026-11-24T13:01:00Z',
        'card': {'type': 'order', 'reference': 'AK-1052', 'title': 'Order AK-1052', 'status': 'accepted', 'total_kobo': 1000000, 'line': '2 × Jollof rice, 1 × Zobo'},
      },
      {
        'id': 'k2',
        'mine': false,
        'kind': 'card',
        'body': 'Pay for AK-1052',
        'read_at': null,
        'created_at': '2026-11-24T13:01:00Z',
        'card': {'type': 'pay', 'reference': 'AK-1052', 'title': 'Pay for AK-1052', 'total_kobo': 1000000, 'url': 'https://app.taskiem.com/o/ak1052'},
      },
    ];
    await t.runAsync(() => (t.state(find.byType(MyChatScreen)) as dynamic).widget.data.refresh() as Future<void>);
    await film.play(0.5);
    film.say('Ada accepts. Her order card and pay card come into the chat.');
    await film.play(4.5);
    film.say('He taps Pay on the card to open the pay page.');
    await film.hold(1.0);
    await film.tap(find.text('Pay').last, then: 2.5);
    await film.finish();
    expect(opened, [Uri.parse('https://app.taskiem.com/o/ak1052')]);
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/orders').single)['zone'], 'Yaba');
  }, skip: !filming);
}
