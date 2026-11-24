// A film: deliver an order (Episode 5, Deliver; Ada's Kitchen). Chioma's order (2 jollof rice, a zobo,
// delivery) needs
// a rider. In the Taskiem chat, Ada taps Assign rider on its card and picks
// Emeka. Then, on the Orders tab, the order and its delivery follow through the
// steps the app shows: offered, accepted, on the way, delivered. The rider's
// side is on WhatsApp, not a phone-app screen, so only the app's own reading of
// it is filmed. Set-up as storefront_orders_page_test.dart (RiderSheet),
// orders_tab_test.dart; the world is film_ada_ep45_world.dart.
//
//   VIDEO_DIR=<out> flutter test test/film_food_deliver_test.dart

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/orders_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_ada_ep45_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: deliver an order', (t) async {
    final world = AdaWorld(deliver: true);
    final chats = await openAdaChat(t, world);
    final f = Film.start(t, 'food-deliver');

    f.say("Chioma's lunch order is ready and needs a rider to reach her.", who: 'Ada · Ada\'s Kitchen');
    await f.play(3.0);

    f.say('In the chat, the order asks for a rider. Tap Assign rider.');
    await f.tap(find.text('Assign rider'), then: 1.2);
    await f.settle();
    await f.hold(1.0);

    f.say('Pick Emeka, who is free and covers Yaba.');
    await f.tap(find.text('Emeka'), then: 0.8);
    await f.settle();
    await f.hold(1.8);

    // A cut: later, on the Orders tab (the app reads the delivery again).
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: OrdersTab(auth: chats.auth, now: () => DateTime.utc(2026, 11, 12, 12))));
    await f.settle();
    f.say('Later, the Orders tab lists the order. Ada taps it open.');
    await f.hold(2.4);
    await f.tap(find.textContaining('Chioma Nnadi'), then: 1.2);
    await f.settle();
    f.say('The order shows Emeka has been offered the delivery.');
    await f.hold(2.6);

    f.say('Emeka accepts. The order shows his name and the step.');
    world.riderMoves('accepted');
    await t.pump(const Duration(seconds: 31));
    await f.settle();
    await f.hold(2.4);

    f.say('Emeka picks it up. The order is on the way.');
    world.riderMoves('picked_up');
    await t.pump(const Duration(seconds: 31));
    await f.settle();
    await f.hold(3.0);

    f.say('Delivered, paid in cash at the door. The order is done.');
    world.riderMoves('delivered');
    await t.pump(const Duration(seconds: 31));
    await f.settle();
    await f.hold(3.4);
    await f.finish();
    expect(world.sent.any((s) => s.startsWith('POST /v1/storefront/orders/AK-2031/offer')), isTrue);
  }, skip: !filming);
}
