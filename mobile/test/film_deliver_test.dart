// A film: deliver an order (Episode 5, Deliver). Nneka's confirmed order needs
// a rider. In the Taskiem chat, Zainab taps Assign rider on its card and picks
// Emeka. Then, on the Orders tab, the order and its delivery follow through the
// steps the app shows: offered, accepted, on the way, delivered. The rider's
// side is on WhatsApp, not a phone-app screen, so only the app's own reading of
// it is filmed. Set-up as storefront_orders_page_test.dart (RiderSheet),
// orders_tab_test.dart; the world is film_zuri_world.dart.
//
//   VIDEO_DIR=<out> flutter test test/film_deliver_test.dart

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/orders_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_zuri_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: deliver an order', (t) async {
    final world = ZuriWorld(deliver: true);
    final chats = await openZuriChat(t, world);
    final f = Film.start(t, 'deliver');

    f.say("Nneka's order is confirmed and needs a rider to reach her.", who: 'Zainab · Zuri Atelier');
    await f.play(3.0);

    f.say('In the chat, the order asks for a rider. Tap Assign rider.');
    await f.tap(find.text('Assign rider'), then: 1.2);
    await f.settle();
    await f.hold(1.0);

    f.say('Pick Emeka, who is free and covers Surulere.');
    await f.tap(find.text('Emeka'), then: 0.8);
    await f.settle();
    await f.hold(1.8);

    // A cut: later, on the Orders tab (the app reads the delivery again).
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: OrdersTab(auth: chats.auth, now: () => DateTime.utc(2026, 11, 12, 12))));
    await f.settle();
    f.say('Later, the Orders tab lists the order. Zainab taps it open.');
    await f.hold(2.4);
    await f.tap(find.textContaining('Nneka Eze'), then: 1.2);
    await f.settle();
    f.say('The order shows Emeka has been offered the delivery.');
    await f.hold(2.0);

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

    f.say('Delivered. The order is done, with the time.');
    world.riderMoves('delivered');
    await t.pump(const Duration(seconds: 31));
    await f.settle();
    await f.hold(3.4);
    await f.finish();
    expect(world.sent.any((s) => s.startsWith('POST /v1/storefront/orders/ZA-8412/offer')), isTrue);
  }, skip: !filming);
}
