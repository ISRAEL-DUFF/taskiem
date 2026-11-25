// A film: order together (customers, Episode 9). Tobi opens the office group
// from You's Chats, taps the tools button, "Order together", picks Ada's
// Kitchen, opens the basket card, adds his items, Bola and Kemi's arrive on
// the live connection, he picks pick-up, closes, places with "Each pays their
// share". Set-up from groups_test.dart (group chat), basket_test.dart (the
// basket's sheet) and my_chats_test.dart (Api). Skipped unless VIDEO_DIR is set.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_you_order_together_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/people_live.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_you_ep910_world.dart';
import 'support.dart' show loadFonts;

void main() {
  setUpAll(loadFonts);

  testWidgets('film: you order together', (t) async {
    final api = tobiApi();
    youRoutes(api);
    var started = false;
    var stage = 0; // people in the basket
    var state = 'open';
    String? fulfilment;
    var placed = false;
    Map<String, dynamic> b() => lunchBasket(people: stage, state: state, fulfilment: fulfilment, placed: placed);
    groupRoutes(api, messages: () => [...officeThread(), if (started) basketMessage()]);
    api.routes['POST /v1/me/baskets'] = (_) {
      started = true;
      return {'basket': b(), 'message': basketMessage()};
    };
    api.routes['GET /v1/me/baskets/b1'] = (_) => b();
    api.routes['GET /v1/me/baskets/b1/shop'] = (_) => {
      'open': true, 'payment': 'at_order', 'pickup': true, 'delivery': false, 'zones': <Object>[],
      'items': [
        {'id': 'jollof', 'name': 'Jollof rice', 'price_kobo': 350000, 'available': true},
        {'id': 'zobo', 'name': 'Zobo', 'price_kobo': 150000, 'available': true},
      ],
    };
    api.routes['PUT /v1/me/baskets/b1/mine'] = (_) {
      stage = 1;
      return b();
    };
    api.routes['PUT /v1/me/baskets/b1'] = (_) {
      fulfilment = 'pickup';
      return b();
    };
    api.routes['POST /v1/me/baskets/b1/close'] = (_) {
      state = 'closed';
      return b();
    };
    api.routes['POST /v1/me/baskets/b1/place'] = (_) {
      state = 'placed';
      placed = true;
      return {'basket': b(), 'order': {'reference': 'AK-2041'}};
    };
    final auth = tobiAuth(api);
    final live = PeopleLive(auth.transport);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: auth, now: () => tobiNow, presence: false, live: live)));
    await _settle(t);
    hearLive(live, 'ready', {'connection': 'c1'});
    await _settle(t);

    final film = Film.start(t, 'you-order-together');
    film.say('Tobi and two colleagues want lunch. Tap Office lunch.', who: 'Tobi · Office lunch');
    await film.hold(3.2);
    await film.tap(find.text('Office lunch'), then: 1.4);
    await _settle(t);
    film.say('Tap the tools button beside the message box.');
    await film.hold(2.6);
    await film.tap(find.bySemanticsLabel(RegExp('tools')), then: 1.2);
    film.say('Tap Order together.');
    await film.hold(2.4);
    await film.tap(find.text('Order together'), then: 1.2);
    film.say('Pick the business to order from. Tap Ada’s Kitchen.');
    await film.hold(2.8);
    await film.tap(find.text('Ada’s Kitchen'), then: 1.0);
    await _settle(t);
    hearLive(live, 'group_message', {'group_id': 'g1', 'message': basketMessage()});
    await film.hold(0.8);
    film.say('A basket card is in the chat. Tap Open basket.');
    await film.hold(2.6);
    await film.tap(find.text('Open basket'), then: 1.2);
    await _settle(t);
    film.say('The basket is empty. Tap Add my items.');
    await film.hold(2.6);
    await film.tap(find.text('Add my items'), then: 1.2);
    await _settle(t);
    film.say('Add one jollof rice and one zobo with the plus buttons.');
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('One more').at(0), then: 0.8);
    await film.tap(find.bySemanticsLabel('One more').at(1), then: 1.4);
    film.say('Tap Put my 2 in the basket.');
    await film.hold(2.0);
    await film.tap(find.text('Put my 2 in the basket'), then: 1.6);
    await _settle(t);
    film.say('Bola and Kemi add theirs on their phones. It shows here.');
    stage = 3;
    hearLive(live, 'basket', {'basket': b()});
    await film.hold(4.2);
    film.say('Tobi holds the basket. He chooses Pick-up.');
    await t.ensureVisible(find.text('Pick-up'));
    await t.pump();
    await film.hold(1.8);
    await film.tap(find.text('Pick-up'), then: 1.6);
    await _settle(t);
    film.say('When everyone has added, tap Close the basket.');
    await t.ensureVisible(find.text('Close the basket'));
    await t.pump();
    await film.hold(2.4);
    await film.tap(find.text('Close the basket'), then: 1.6);
    await _settle(t);
    film.say('Then tap Place the order.');
    await t.ensureVisible(find.text('Place the order'));
    await t.pump();
    await film.hold(2.2);
    await film.tap(find.text('Place the order'), then: 1.4);
    await _settle(t);
    film.say('Choose who pays. Tap Each pays their share.');
    await film.hold(3.0);
    await film.tap(find.text('Each pays their share'), then: 1.0);
    film.say('Each pays Ada’s Kitchen for their own items.');
    await film.hold(3.0);
    await film.tap(find.text('Place the order').last, then: 1.6);
    await _settle(t);
    film.say('The order is placed. Each person sees their own share.');
    await film.hold(2.6);
    await film.drag(find.text('Who wants what'), const Offset(0, -260), seconds: 0.8);
    await film.hold(4.0);
    await film.finish();
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 6));
    live.dispose();
  }, skip: !filming);
}

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
