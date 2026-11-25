// A film: a customer books a service (episode 6). Tobi Adewale, on You, opens
// his chat with Kola, taps Chat tools, Book, picks Haircut, a day and a free
// time, checks it and taps Book. Back on You, Orders shows the booking and
// opens it. Set-up copied from my_chats_test.dart ("Book as a customer": the
// services and free times, the booking) and you_stage3_test.dart (the order
// rows and a booking's page). Skipped unless VIDEO_DIR is set. Run with
// TZ=Africa/Lagos.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/book_sheet.dart' show forgetServices;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_you_ep56_world.dart';
import 'support.dart' show loadFonts;

void main() {
  setUpAll(loadFonts);

  testWidgets('film: you book', (t) async {
    forgetServices();
    final now = DateTime.utc(2026, 11, 24, 12, 0); // 13:00 in Lagos
    var booked = false;
    final api = YouApi();
    Map<String, dynamic> kola() => chatRow('c2', 'Kola', 'Hello Tobi, how can I help?', '2026-11-24T11:30:00Z', business: 'b3');
    api.routes['GET /v1/me/chats'] = (_) => {
      'chats': [
        chatRow('c1', 'Ada’s Kitchen', 'Your rider has picked it up.', '2026-11-24T11:50:00Z', business: 'b1'),
        kola(),
      ],
      'unread': 0,
    };
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    api.routes['GET /v1/me/chats/c2'] = (_) => kola();
    api.routes['POST /v1/me/chats/c2/read'] = (_) => ans({}, 204);
    api.routes['GET /v1/me/chats/c2/messages'] = (_) => {
      'messages': [
        {'id': 'm1', 'mine': true, 'kind': 'text', 'body': 'Hi Kola, can I get a haircut this week?', 'read_at': '2026-11-24T11:29:00Z', 'created_at': '2026-11-24T11:28:00Z'},
        {'id': 'm2', 'mine': false, 'kind': 'text', 'body': 'Hello Tobi, how can I help?', 'sender_name': 'Kola', 'read_at': null, 'created_at': '2026-11-24T11:30:00Z'},
      ],
      'more': false,
    };
    api.routes['GET /v1/me/chats/c2/services'] = (_) => {
      'open': true,
      'services': [
        {'id': 's1', 'name': 'Haircut', 'minutes': 45, 'price_kobo': 500000},
        {'id': 's2', 'name': 'Beard trim', 'minutes': 20, 'price_kobo': 200000},
      ],
    };
    api.routes['GET /v1/me/chats/c2/services/s1/times'] = (r) {
      final day = r.url.queryParameters['day'] ?? '2026-11-25';
      return {
        'day': day,
        'days': ['2026-11-25', '2026-11-26', '2026-11-27'],
        'service': {'id': 's1', 'name': 'Haircut', 'minutes': 45},
        'times': day == '2026-11-26' ? ['2026-11-26T09:00:00Z', '2026-11-26T13:00:00Z', '2026-11-26T14:30:00Z'] : ['2026-11-25T10:00:00Z', '2026-11-25T15:00:00Z'],
      };
    };
    api.routes['POST /v1/me/chats/c2/bookings'] = (_) {
      booked = true;
      return ans({
        'booking': {'reference': 'KO-3', 'service_id': 's1', 'service_name': 'Haircut', 'starts_at': '2026-11-26T13:00:00Z', 'ends_at': '2026-11-26T13:45:00Z'},
      }, 201);
    };
    api.routes['GET /v1/me/orders'] = (_) => {
      'orders': [
        if (booked) orderRow(ref: 'KO-3', business: 'b3', name: 'Kola', state: 'accepted', summary: 'Haircut', total: 500000, made: '2026-11-24T11:59:00Z', kind: 'booking', chatId: 'c2'),
        orderRow(ref: 'AK-1', business: 'b1', name: 'Ada’s Kitchen', state: 'completed', summary: '2 × Jollof rice, 1 × Zobo', total: 1000000, made: '2026-11-24T11:20:00Z', chatId: 'c1'),
      ],
      'next': null,
      'waiting_count': 0,
    };
    api.routes['GET /v1/me/orders/b3/KO-3'] = (_) => {
      ...orderRow(ref: 'KO-3', business: 'b3', name: 'Kola', state: 'accepted', summary: 'Haircut', total: 500000, made: '2026-11-24T11:59:00Z', kind: 'booking', chatId: 'c2'),
      'lines': <Object>[],
      'paid_kobo': 0,
      'service_name': 'Haircut',
      'starts_at': '2026-11-26T13:00:00Z',
      'ends_at': '2026-11-26T13:45:00Z',
    };

    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => now, presence: false)));
    await letIn(t);

    final film = Film.start(t, 'you-book');
    film.say('Tobi wants a haircut. He opens his chat with Kola.', who: 'Tobi · customer of Kola');
    await film.hold(2.6);
    await film.tap(find.text('Kola').first, then: 0.8);
    await letIn(t);
    await film.hold(0.8);
    film.say('He taps the tools button at the bottom of the chat.');
    await film.tap(find.bySemanticsLabel('Chat tools'), then: 0.8);
    await film.settle();
    await film.hold(1.2);
    film.say('He taps Book: their services and free times.');
    await film.tap(find.text('Book').first, then: 0.8);
    await film.settle();
    await film.hold(1.6);
    film.say('He picks the service he wants: Haircut.');
    await film.tap(find.text('Haircut').first, then: 0.8);
    await film.settle();
    await film.hold(1.8);
    film.say('He taps Thursday 26 November, then a free time: 14:00.');
    await film.tap(find.bySemanticsLabel('Thu 26 Nov'), then: 0.8);
    await film.settle();
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('14:00'), then: 1.0);
    await film.hold(0.8);
    film.say('He taps the day and time to check his booking.');
    await film.tap(find.bySemanticsLabel('Thu 26 Nov at 14:00'), then: 0.8);
    await film.settle();
    await film.hold(2.6);
    film.say('He taps Book. The booking is made.');
    await film.tap(find.bySemanticsLabel('Book'), then: 0.8);
    await film.settle();
    await film.hold(2.4);
    await film.settle();
    await film.hold(1.0);

    film.say('He goes back, then taps Orders. The booking is there.');
    await film.tap(find.bySemanticsLabel(RegExp('^Back')).first, then: 0.8);
    await film.settle();
    await film.hold(0.8);
    await film.tap(find.text('Orders').first, then: 0.8);
    await film.settle();
    await film.hold(2.8);
    film.say('He taps Kola to see the day and time.');
    await film.tap(find.text('Kola').first, then: 0.8);
    await film.settle();
    await film.hold(3.4);
    await film.finish();
  });
}
