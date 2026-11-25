// A film: a customer follows their orders (episode 5, "Your orders in one
// place"). Tobi Adewale, on You, taps Orders: every order and booking with any
// business. He opens his lunch from Ada's Kitchen, sees the rider's steps, pulls
// down to refresh when the rider is at his door, and goes back to the list.
// Set-up copied from you_stage3_test.dart (Orders on You: the rows, one order
// with its delivery) and my_chats_test.dart (the stand-in server). Skipped
// unless VIDEO_DIR is set. Run with TZ=Africa/Lagos so the app's clock is the
// phone's.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_you_ep56_world.dart';
import 'support.dart' show loadFonts;

void main() {
  setUpAll(loadFonts);

  testWidgets('film: you follow orders', (t) async {
    // 13:00 in Lagos (12:00 UTC); the rider arrives a few minutes on.
    var now = DateTime.utc(2026, 11, 24, 12, 0);
    var arrived = false;
    final api = YouApi();
    String state() => arrived ? 'arrived' : 'dispatched';
    api.routes['GET /v1/me/chats'] = (_) => {
      'chats': [
        chatRow('c1', 'Ada’s Kitchen', 'Your rider has picked it up.', '2026-11-24T11:50:00Z', business: 'b1'),
        chatRow('c2', 'Kola', 'See you on Thursday.', '2026-11-23T16:00:00Z', business: 'b3'),
        chatRow('c3', 'Bayo Rice Mills', 'Thank you, Tobi.', '2026-11-21T09:00:00Z', business: 'b2'),
      ],
      'unread': 0,
    };
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    api.routes['GET /v1/me/orders'] = (_) => {
      'orders': [
        orderRow(ref: 'AK-1', business: 'b1', name: 'Ada’s Kitchen', state: state(), summary: '2 × Jollof rice, 1 × Zobo', total: 1000000, made: '2026-11-24T11:20:00Z', chatId: 'c1'),
        orderRow(ref: 'KO-3', business: 'b3', name: 'Kola', state: 'accepted', summary: 'Haircut', total: 500000, made: '2026-11-23T15:30:00Z', kind: 'booking', chatId: 'c2'),
        orderRow(ref: 'BR-7', business: 'b2', name: 'Bayo Rice Mills', state: 'completed', summary: '1 × Ofada rice, 10 kg', total: 1850000, made: '2026-11-18T10:00:00Z', chatId: 'c3'),
      ],
      'next': null,
      'waiting_count': 0,
    };
    api.routes['GET /v1/me/orders/b1/AK-1'] = (_) => {
      ...orderRow(ref: 'AK-1', business: 'b1', name: 'Ada’s Kitchen', state: state(), summary: '2 × Jollof rice, 1 × Zobo', total: 1000000, made: '2026-11-24T11:20:00Z', chatId: 'c1'),
      'lines': [
        {'name': 'Jollof rice', 'quantity': 2, 'price_kobo': 350000, 'line_kobo': 700000},
        {'name': 'Zobo', 'quantity': 1, 'price_kobo': 150000, 'line_kobo': 150000},
      ],
      'fulfilment': 'delivery',
      'zone': 'Yaba',
      'delivery_kobo': 150000,
      'paid_kobo': 1000000,
      'address': {'address': '12 Herbert Macaulay Way, Yaba', 'landmark': 'By the filling station'},
      'delivery': arrived
          ? {'status': 'arrived', 'line': 'The rider is at your address', 'accepted_at': '2026-11-24T11:35:00Z', 'picked_up_at': '2026-11-24T11:50:00Z', 'arrived_at': '2026-11-24T12:06:00Z'}
          : {'status': 'picked_up', 'line': 'On its way to you', 'accepted_at': '2026-11-24T11:35:00Z', 'picked_up_at': '2026-11-24T11:50:00Z'},
    };

    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => now, presence: false)));
    await letIn(t);

    final film = Film.start(t, 'you-follow-orders');
    film.say('Tobi ordered lunch from Ada’s Kitchen. He taps Orders on You.', who: 'Tobi · customer of Ada’s Kitchen');
    await film.hold(2.8);
    await film.tap(find.text('Orders').first, then: 0.8);
    await film.settle();
    film.say('Every order and booking with any business is here, newest first.');
    await film.hold(3.2);
    film.say('He taps Ada’s Kitchen to open his order.');
    await film.hold(1.0);
    await film.tap(find.text('Ada’s Kitchen').first, then: 0.8);
    await film.settle();
    film.say('The order shows what he got, the total and who has it.');
    await film.hold(3.2);
    film.say('Under Delivery, the steps show the rider has picked it up.');
    await film.hold(3.6);

    // A few minutes on: the rider reaches the door.
    arrived = true;
    now = DateTime.utc(2026, 11, 24, 12, 7);
    film.say('Minutes later, he pulls down to refresh the order.');
    await film.drag(find.byType(Scrollable).first, const Offset(0, 330), seconds: 0.9);
    await film.settle();
    await film.hold(0.6);
    film.say('The steps now show the rider at his door.');
    await film.hold(3.6);

    film.say('He goes back. In Orders, the row says At the door too.');
    await film.tap(find.bySemanticsLabel('Back to Orders'), then: 0.8);
    await film.settle();
    await film.hold(3.2);
    await film.finish();
  });
}
