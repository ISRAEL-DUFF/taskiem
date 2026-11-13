// A service, a price list's prices, and booking with a linked business
// (M6 parts V, L, B; web ChatToolForms.tsx ServiceForm, PriceListForm;
// ChatBook.tsx BookSheet).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/book_sheet.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/service_form.dart';
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('a service row in the table\'s own columns', () {
    final cols = {'name': 'Service', 'price': 'Price', 'price_unit': 'naira', 'duration': 'Minutes', 'available': 'Shown'};
    expect(serviceRow(cols, ' Braids ', '90', '15,000').row, {'Service': 'Braids', 'Price': 15000, 'Minutes': 90});
    expect(serviceRow(cols, 'Braids', '3', '').error, 'Say how long it takes in minutes, from 5 to 480.');
    expect(serviceRow({...cols, 'price_unit': 'kobo'}, 'Braids', '30', '1,500').row?['Price'], 150000);
    expect(serviceLine('Braids', 90, null), 'New service: Braids, 90 min, price on the day');
  });

  test('times as Lagos reads them', () {
    expect(lagosTime('2026-10-24T09:30:00Z'), '10:30');
    expect(dayLabel('2026-10-24'), 'Sat 24 Oct');
    expect(whenLabel('2026-10-24T09:30:00Z'), 'Sat 24 Oct, 10:30');
    expect(formatDuration(90), '1 hour 30 min');
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData chats) build, {Object? bookRefusal}) async {
    t.view.physicalSize = const Size(390, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var refuse = bookRefusal;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET') {
          if (p == '/v1/storefront') return ok({'services_table': 'T1', 'services_columns': {'name': 'Name', 'price': 'Price', 'duration': 'Minutes'}});
          if (p == '/v1/link-chats/L1/services') {
            return ok({
              'open': true,
              'phone': '0803 000 0000',
              'services': [
                {'id': 'S1', 'name': 'Rice milling', 'minutes': 30, 'price_kobo': 250000},
              ],
            });
          }
          if (p == '/v1/link-chats/L1/services/S1/times') {
            final day = req.url.queryParameters['day'] ?? '2026-11-13';
            return ok({
              'days': ['2026-11-13', '2026-11-14'],
              'day': day,
              'times': day == '2026-11-13' ? ['2026-11-13T09:00:00Z', '2026-11-13T10:00:00Z'] : ['2026-11-14T08:00:00Z'],
            });
          }
          return null;
        }
        sent.add('${req.method} $p ${req.body}');
        if (p == '/v1/link-chats/L1/bookings') {
          if (refuse != null) {
            final r = refuse;
            refuse = null;
            return http.Response(jsonEncode(r), 409, headers: {'content-type': 'application/json'});
          }
          return ok({
            'booking': {'reference': 'BK-1', 'service_id': 'S1', 'service_name': 'Rice milling', 'starts_at': '2026-11-14T08:00:00Z'},
          });
        }
        return ok({});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: build(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('add a service: its row, then the page read again', (t) async {
    final said = <String?>[];
    final sent = await open(t, (chats) => TkSheet(title: 'Add service', body: ServiceForm(auth: chats.auth, onSaved: said.add)));
    await t.enterText(find.byType(EditableText).first, 'Rice milling');
    await t.tap(find.bySemanticsLabel('One more'));
    await t.pump();
    await t.enterText(find.bySemanticsLabel(RegExp('^Price in naira')), '2,500');
    await t.tap(find.widgetWithText(TkCta, 'Add service'));
    await settle(t);
    expect(sent, [
      'POST /v1/tables/T1/rows {"row":{"Name":"Rice milling","Price":2500,"Minutes":31}}',
      'POST /v1/storefront/catalogue/refresh {}',
    ]);
    expect(said.single, 'New service: Rice milling, 31 min, ₦2,500');
  });

  testWidgets('a price list: only a list with a change is sent', (t) async {
    final said = <String?>[];
    final list = {
      'id': 'PL1',
      'name': 'Trade',
      'items': [
        {'item_id': 'rice', 'name': 'Rice', 'unit': 'bag', 'price_kobo': 4000000, 'min_quantity': 2, 'available': true},
      ],
    };
    final sent = await open(t, (chats) => TkSheet(title: 'Trade', body: PriceListForm(auth: chats.auth, list: list, onSaved: said.add)));
    expect(find.text('Rice, a bag'), findsOneWidget);
    await t.enterText(find.bySemanticsLabel(RegExp('^Rice, a bag in naira')), '41,000');
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    expect(sent.single, 'PUT /v1/price-lists/PL1/items {"items":[{"item_id":"rice","price_kobo":4100000,"min_quantity":2,"available":true}]}');
    expect(said.single, 'Trade: saved');
  });

  testWidgets('book: a service, a day, a time; taken since, chosen again', (t) async {
    final sent = await open(t, (chats) => BookSheet(auth: chats.auth, link: 'L1', business: 'Bayo Rice Mills'), bookRefusal: {'error': 'Taken', 'code': 'slot_taken'});
    expect(find.text('Book with Bayo Rice Mills'), findsOneWidget);
    await t.tap(find.text('Rice milling'));
    await settle(t);
    expect(find.text('30 min · ₦2,500'), findsOneWidget);
    expect(find.bySemanticsLabel('10:00'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Sat 14 Nov'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('09:00'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Sat 14 Nov at 09:00'));
    await t.pumpAndSettle();
    expect(find.text('Check your booking'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Book'));
    await settle(t);
    expect(find.text('That time was just taken. Please choose another.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('09:00'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Sat 14 Nov at 09:00'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Book'));
    await settle(t);
    expect(sent.length, 2);
    expect(sent.last, 'POST /v1/link-chats/L1/bookings {"service_id":"S1","start":"2026-11-14T08:00:00Z","note":"","phone":"0803 000 0000"}');
    expect(find.text('Booked with Bayo Rice Mills: Rice milling, Sat 14 Nov, 09:00'), findsOneWidget);
    await t.pump(const Duration(seconds: 2));
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pumpAndSettle();
}
