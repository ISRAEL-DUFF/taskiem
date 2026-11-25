// A film: Share a business or an order with someone (customer series,
// episode 7, you-share). Tobi Adewale shares Ada's Kitchen from Discover and
// an order from Orders into his chat with Mama Tobi; then Mama Tobi's phone:
// Tobi is a stranger to her, so his first message waits under Message
// requests; she opens it, accepts, and opens the business's card.
// Set-up from people_test.dart, people_share_gaps_test.dart and
// you_stage3_test.dart. Skipped unless VIDEO_DIR is set.

import 'package:flutter_test/flutter_test.dart';

import 'film.dart';
import 'film_you_ep78_world.dart';
import 'people_test.dart' show Api, answer, card, peopleChat;
import 'support.dart' show loadFonts;

const _ada = 'Ada’s Kitchen';

Map<String, dynamic> _order() => {
  'business_id': 'b1',
  'business': _ada,
  'reference': 'AK-1042',
  'kind': 'order',
  'state': 'accepted',
  'summary': '2 × Jollof rice, 1 × Zobo',
  'total_kobo': 850000,
  'due_kobo': 0,
  'pay': false,
  'made_at': '2026-11-23T10:00:00Z',
  'chat_id': null,
};

void main() {
  setUpAll(loadFonts);

  testWidgets('film: share a business and an order', (t) async {
    // --- Tobi's phone ---
    final tobiApi = Api();
    youRoutes(tobiApi, [personRow(id: 'pc-mama', personId: 'p-mama', name: 'Mama Tobi', last: 'Good morning, Mama')]);
    tobiApi.routes['GET /v1/discover/categories'] = (_) => {'categories': <Object>[]};
    tobiApi.routes['POST /v1/discover/search'] = (_) => {
      'results': [
        {'business': 'b1', 'name': _ada, 'photo': <String, dynamic>{}, 'category': 'food', 'category_name': 'Food and drinks', 'area': 'Yaba', 'open_now': true, 'line': 'Open now, closes at 18:00', 'can_chat': true},
      ],
      'more': false,
    };
    tobiApi.routes['GET /v1/discover/b1'] = (_) => {
      'business': 'b1', 'name': _ada, 'tagline': 'Home cooking, every day', 'category': 'food', 'category_name': 'Food and drinks', 'area': 'Yaba',
      'photo': null, 'photos': <Object>[], 'hours': {'days': [for (var i = 0; i < 7; i++) i == 6 ? <Object>[] : [{'open': '09:00', 'close': '18:00'}]]}, 'open_now': true, 'line': 'Open now, closes at 18:00',
      'can_chat': true, 'has_shop': true, 'has_book': false, 'chat': null,
    };
    tobiApi.routes['GET /v1/me/orders'] = (_) => {'orders': [_order()], 'next': null, 'waiting_count': 0};
    tobiApi.routes['GET /v1/me/orders/b1/AK-1042'] = (_) => {
      ..._order(),
      'lines': [
        {'name': 'Jollof rice', 'quantity': 2, 'price_kobo': 350000, 'line_kobo': 700000},
        {'name': 'Zobo', 'quantity': 1, 'price_kobo': 150000, 'line_kobo': 150000},
      ],
      'fulfilment': 'pickup',
      'paid_kobo': 850000,
    };
    tobiApi.routes['POST /v1/me/people/pc-mama/messages'] = (_) => answer({'id': 'k1', 'mine': true, 'kind': 'card', 'body': '', 'status': 'sent', 'read_at': null, 'created_at': '2026-11-24T13:00:00Z'}, 201);
    final tobi = signedInAs(tobiApi, name: 'Tobi Adewale', phone: '+2348031230000');
    await openYouHome(t, tobi);

    final f = Film.start(t, 'you-share');
    f.say('Tobi likes Ada’s Kitchen. He wants Mama Tobi to see it.', who: 'Tobi · Taskiem for you');
    await f.hold(3.2);
    f.say('He taps Discover, then Ada’s Kitchen.');
    await f.tap(find.text('Discover').first, then: 1.0);
    await f.settle();
    await f.hold(0.8);
    await f.tap(find.text(_ada), then: 1.0);
    await f.settle();
    f.say('On its page he taps Share with someone.');
    await f.hold(1.6);
    await f.tap(find.bySemanticsLabel('Share $_ada with someone'), then: 1.0);
    await f.settle();
    f.say('He ticks Mama Tobi, then taps Send.');
    await f.hold(1.6);
    await f.tap(find.text('Mama Tobi'), then: 1.0);
    await f.tap(find.text('Send'), then: 0.6);
    await f.settle();
    f.say('Taskiem says Shared. Mama Tobi gets a card for the business.');
    await f.hold(3.0);

    // Back to the list, then Orders.
    f.say('An order shares the same way. Tobi opens Orders.');
    await f.tap(find.bySemanticsLabel(RegExp('^Back')).first, then: 0.8);
    await f.settle();
    await f.hold(0.6);
    await f.tap(find.text('Orders').first, then: 1.0);
    await f.settle();
    await f.hold(1.0);
    await f.tap(find.text(_ada), then: 1.0);
    await f.settle();
    f.say('He opens the order and taps Share with someone.');
    await t.ensureVisible(find.text('Share with someone'));
    await t.pump();
    await f.hold(1.8);
    await f.tap(find.text('Share with someone'), then: 1.0);
    await f.settle();
    f.say('Again he picks Mama Tobi and taps Send.');
    await f.hold(1.2);
    await f.tap(find.text('Mama Tobi'), then: 1.0);
    await f.tap(find.text('Send'), then: 0.6);
    await f.settle();
    await f.hold(2.4);

    // --- Mama Tobi's phone ---
    await letGo(t);
    final mamaApi = Api();
    final cardBiz = {'type': 'business', 'title': _ada, 'business': _ada, 'business_id': 'b1', 'area': 'Yaba'};
    final cardOrder = {'type': 'order', 'title': 'Order AK-1042', 'business': _ada, 'business_id': 'b1', 'reference': 'AK-1042', 'state': 'accepted'};
    var accepted = false;
    Map<String, dynamic> tobiChat() => {
      ...peopleChat(id: 'pc-tobi', name: 'Tobi Adewale', request: !accepted, last: _ada),
      'last_at': '2026-11-24T13:01:00Z',
      'person': card(id: 'p-tobi', name: 'Tobi Adewale', lastSeen: '2026-11-24T12:55:00Z'),
    };
    youRoutes(mamaApi, [], requests: 1);
    mamaApi.routes['GET /v1/me/people/requests'] = (_) => {'more': false, 'requests': [tobiChat()]};
    mamaApi.routes['GET /v1/me/people/pc-tobi'] = (_) => tobiChat();
    mamaApi.routes['GET /v1/me/people/pc-tobi/messages'] = (_) => {
      'messages': [
        {'id': 'k1', 'mine': false, 'kind': 'card', 'body': _ada, 'card': cardBiz, 'read_at': null, 'created_at': '2026-11-24T13:00:00Z'},
        {'id': 'k2', 'mine': false, 'kind': 'card', 'body': 'Order AK-1042', 'card': cardOrder, 'read_at': null, 'created_at': '2026-11-24T13:01:00Z'},
      ],
      'more': false,
    };
    mamaApi.routes['POST /v1/me/people/pc-tobi/read'] = (_) => answer({}, 204);
    mamaApi.routes['POST /v1/me/people/pc-tobi/accept'] = (_) {
      accepted = true;
      return tobiChat();
    };
    mamaApi.routes['GET /v1/me/people/pc-tobi/messages/k1/card'] = (_) => {
      'card': cardBiz,
      'live': {
        'yours': false,
        'business': {'name': _ada, 'tagline': 'Home cooking, every day', 'area': 'Yaba', 'open_now': true, 'takes_chats': true},
      },
    };
    final mama = signedInAs(mamaApi, name: 'Mama Tobi', phone: '+2348031230001');
    await openYouHome(t, mama);

    f.say('On Mama Tobi’s phone, Tobi is new to her, so his message waits.');
    await f.hold(4.0);
    f.say('She taps Message requests, then opens Tobi’s message.');
    await f.tap(find.bySemanticsLabel(RegExp('Message requests')), then: 1.0);
    await f.settle();
    await f.hold(1.2);
    await f.tap(find.text('Tobi Adewale'), then: 1.0);
    await f.settle();
    f.say('Both cards are there. Tobi is not told she has read them.');
    await f.hold(4.0);
    f.say('She taps Accept. Now Tobi can see when she reads.');
    await f.tap(find.text('Accept'), then: 1.0);
    await f.settle();
    await f.hold(2.0);
    f.say('She taps See the business to look at Ada’s Kitchen.');
    await f.tap(find.text('See the business').first, then: 1.0);
    await f.settle();
    await f.hold(1.2);
    f.say('The page is live, and Chat with them starts her own chat.');
    await f.hold(4.0);
    await f.finish();
  }, skip: !filming);
}
