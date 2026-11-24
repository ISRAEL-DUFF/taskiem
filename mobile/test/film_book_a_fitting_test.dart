// A film: a customer books a fitting (episode 3c). Nneka Eze, in her chat with
// Zuri Atelier, books the Fitting service: a day and a free time, then Book
// (BookSheet as a customer, taskiem-for-everyone 8.3). Then Zainab opens
// Services from the chat's tools, taps Open services and sees the booking on
// the bookings page. Set-up copied from service_book_test.dart (the sheet),
// bookings_page_test.dart (the page) and tools_panel_test.dart (the tools).
// Skipped unless VIDEO_DIR is set.


import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/book_sheet.dart' show forgetServices, showBookSheet;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_zuri_shop_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: book a fitting', (t) async {
    forgetServices();
    final f = Fixture('chat-tools');
    final bookings = <Map<String, dynamic>>[];
    final sent = <String>[];
    const base = '/v1/me/chats/Z1';
    final sf = {
      'services_table': 'T1',
      'services_columns': {'name': 'Name', 'price': 'Price', 'duration': 'Minutes'},
      'published_version': 2,
      'availability': {
        'days': [
          for (var i = 0; i < 7; i++) i == 0 ? <Object>[] : [{'open': '09:00', 'close': '17:00'}],
        ],
        'off': <Object>[],
        'ahead_days': 30,
        'soonest_minutes': 120,
        'cutoff_minutes': 120,
      },
      'draft': {'hours': {'days': <Object>[]}},
    };
    Future<http.Response?> server(http.Request req) async {
      final p = req.url.path;
      final base0 = zuriBase(req, f);
      if (base0 != null) return base0;
      if (req.method == 'GET') {
        switch (p) {
          case '$base/services':
            return okJson({
              'open': true,
              'phone': '+234 803 123 0142',
              'services': [
                {'id': 'F1', 'name': 'Fitting', 'minutes': 45, 'price_kobo': 500000},
                {'id': 'F2', 'name': 'Alteration', 'minutes': 30, 'price_kobo': 300000},
              ],
            });
          case '$base/services/F1/times':
            final day = req.url.queryParameters['day'] ?? '2026-11-27';
            return okJson({
              'days': ['2026-11-27', '2026-11-28', '2026-11-30'],
              'day': day,
              'times': day == '2026-11-28'
                  ? ['2026-11-28T09:00:00Z', '2026-11-28T10:00:00Z', '2026-11-28T13:00:00Z']
                  : ['2026-11-27T09:00:00Z', '2026-11-27T12:00:00Z'],
            });
          case '/v1/storefront':
            return okJson(sf);
          case '/v1/storefront/services':
            return okJson({
              'availability': sf['availability'],
              'error': null,
              'items': [
                {'id': 'F1', 'name': 'Fitting', 'price_kobo': 500000, 'available': true, 'duration_minutes': 45},
                {'id': 'F2', 'name': 'Alteration', 'price_kobo': 300000, 'available': true, 'duration_minutes': 30},
              ],
            });
          case '/v1/storefront/bookings':
            return okJson({'bookings': bookings});
        }
        return null;
      }
      sent.add('${req.method} $p ${req.body}');
      if (p == '$base/bookings') {
        bookings.add({
          'id': 'B1',
          'kind': 'booking',
          'reference': 'BK-1',
          'status': 'accepted',
          'service_name': 'Fitting',
          'starts_at': '2026-11-28T10:00:00Z',
          'ends_at': '2026-11-28T10:45:00Z',
          'customer': {'name': 'Nneka Eze', 'phone': '+234 803 123 0142'},
        });
        return okJson({
          'booking': {'reference': 'BK-1', 'service_id': 'F1', 'service_name': 'Fitting', 'starts_at': '2026-11-28T10:00:00Z'},
        });
      }
      return okJson({});
    }

    Future<void> let() async {
      for (var i = 0; i < 8; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
        await t.pump();
      }
      await t.pumpAndSettle();
    }

    // Nneka's phone: the sheet that books, in her chat with the business.
    final hers = f.auth(also: server);
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => showBookSheet(c, auth: hers, link: '', business: 'Zuri Atelier', customer: base))));
    await let();

    final film = Film.start(t, 'book-a-fitting');
    film.say('Nneka wants a fitting for her dress. She opens Book.', who: 'Nneka · customer of Zuri Atelier');
    await film.play(3.0);
    film.say('She taps the service she wants: Fitting.');
    await film.tap(find.text('Fitting'), then: 1.2);
    await film.settle();
    await film.hold(0.8);
    film.say('She picks Saturday 28 November, then a free time: 11:00.');
    await film.tap(find.bySemanticsLabel('Sat 28 Nov'), then: 1.0);
    await film.settle();
    await film.hold(0.6);
    await film.tap(find.bySemanticsLabel('11:00'), then: 1.0);
    await film.hold(0.6);
    film.say('She taps the day and time to check her booking.');
    await film.tap(find.bySemanticsLabel('Sat 28 Nov at 11:00'), then: 1.2);
    await film.settle();
    await film.hold(1.2);
    film.say('She taps Book. It is saved.');
    await film.tap(find.bySemanticsLabel('Book'), then: 0.8);
    await film.settle();
    await film.play(2.2);

    // Zainab's phone: the chat, the Services tool, the bookings page.
    final mine = f.auth(also: server);
    final chats = ChatsData(mine, now: () => f.at);
    zuriMe(mine, f);
    chats.load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await let();
    film.say('Later, Zainab checks her bookings. She opens the tools.', who: 'Zainab · Zuri Atelier');
    await film.play(1.8);
    await film.tap(find.bySemanticsLabel('Open tools'), then: 1.0);
    await film.settle();
    film.say('She opens Services, then Open services.');
    await film.hold(0.4);
    await film.tap(find.bySemanticsLabel(RegExp('^Services, ')), then: 1.2);
    await film.settle();
    await film.hold(0.6);
    await film.tap(find.text('Open services'), then: 1.4);
    await film.settle();
    film.say('Saturday 28 Nov, 11:00: Nneka Eze booked a fitting.');
    await film.hold(3.2);
    await film.finish();
    expect(sent.first, startsWith('POST $base/bookings {"service_id":"F1","start":"2026-11-28T10:00:00Z"'));
  }, skip: !filming);
}

/// A plain page that opens a sheet over itself, as the chat does.
class _Host extends StatefulWidget {
  const _Host(this.open);
  final void Function(BuildContext context) open;
  @override
  State<_Host> createState() => _HostState();
}

class _HostState extends State<_Host> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => widget.open(context));
  }

  @override
  Widget build(BuildContext context) => const ColoredBox(color: Color(0xFFEFEAE2));
}
