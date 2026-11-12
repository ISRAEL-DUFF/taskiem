// The Bookings screen (M6 part O; web StorefrontBookings.tsx, lib/
// bookings.ts): bookings by day, Move and Cancel; the hours, days off and
// rules saved as the API takes them.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/bookings_page.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/kit/forms.dart';
import 'package:taskiem/kit/modal.dart' show TkModal;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('bookings by day, their spans and states', () {
    final g = groupByDay([
      {'id': 'b', 'starts_at': '2026-11-14T09:00:00Z'},
      {'id': 'a', 'starts_at': '2026-11-13T09:00:00Z', 'ends_at': '2026-11-13T09:45:00Z'},
      {'id': 'c', 'starts_at': '2026-11-13T23:30:00Z'},
    ]);
    expect(g.map((x) => x.label), ['Fri 13 Nov', 'Sat 14 Nov'], reason: '23:30 UTC is the 14th in Lagos');
    expect(g.last.bookings.map((b) => b['id']), ['c', 'b']);
    expect(timeSpan(g.first.bookings.first), '10:00–10:45');
    expect(canChange({'kind': 'booking', 'status': 'paid'}), isTrue);
    expect(canChange({'kind': 'booking', 'status': 'completed'}), isFalse);
    expect(Notice.of(90).minutes, 90);
    expect(Notice.of(120).hours, isTrue);
  });

  Future<List<String>> open(WidgetTester t) async {
    t.view.physicalSize = const Size(390, 5000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final sf = {
      'services_table': 'T1',
      'services_columns': {'name': 'Name'},
      'published_version': 2,
      'availability': {
        'days': [
          [
            {'open': '09:00', 'close': '17:00'},
          ],
          <Object>[],
          <Object>[],
          <Object>[],
          <Object>[],
          <Object>[],
          <Object>[],
        ],
        'off': ['2020-01-01'],
      },
      'draft': {'hours': {'days': <Object>[]}},
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET') {
          if (p == '/v1/storefront') return ok(sf);
          if (p == '/v1/storefront/services') {
            return ok({
              'items': [
                {'id': 'S1', 'name': 'Braids', 'duration_minutes': 90, 'price_kobo': 1500000},
              ],
            });
          }
          if (p == '/v1/storefront/bookings') {
            return ok({
              'bookings': [
                {
                  'id': 'B1',
                  'kind': 'booking',
                  'reference': 'BK-1',
                  'status': 'accepted',
                  'service_name': 'Braids',
                  'starts_at': '2099-11-13T09:00:00Z',
                  'ends_at': '2099-11-13T10:30:00Z',
                  'customer': {'name': 'Ngozi', 'phone': '0803'},
                },
              ],
            });
          }
          return null;
        }
        sent.add('${req.method} $p ${req.body}');
        if (p.endsWith('/move')) {
          return ok({
            'booking': {'reference': 'BK-1', 'starts_at': '2099-11-14T10:00:00Z'},
          });
        }
        if (p.endsWith('/cancel')) {
          return ok({
            'booking': {'reference': 'BK-1', 'status': 'cancelled'},
          });
        }
        return ok(sf);
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: BookingsPage(auth: chats.auth)));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
    return sent;
  }

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  testWidgets('a booking moved and cancelled; the services listed', (t) async {
    final sent = await open(t);
    expect(find.text('10:00–11:30 · Braids'), findsOneWidget);
    expect(find.text('Ngozi · 0803 · BK-1'), findsOneWidget);
    expect(find.text('1 hour 30 min · ₦15,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Move Braids BK-1'));
    await t.pumpAndSettle();
    final inModal = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    await t.enterText(inModal.at(0), '2099-11-14');
    await t.enterText(inModal.at(1), '11:00');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Move'));
    await settle(t);
    expect(sent.single, 'POST /v1/storefront/bookings/BK-1/move {"starts_at":"2099-11-14T11:00:00+01:00"}');
    expect(find.text('BK-1 is moved to Sat 14 Nov, 11:00; the customer is told.'), findsOneWidget);
    sent.clear();
    await t.tap(find.bySemanticsLabel('Cancel Braids BK-1'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Cancel the booking'));
    await settle(t);
    expect(sent.single, 'POST /v1/storefront/bookings/BK-1/cancel {}');
  });

  testWidgets('the hours: Monday 09:00 to 17:00 kept, Tuesday opened, auto-confirm; past days off removed; saved as the API takes them', (t) async {
    final sent = await open(t);
    expect(find.text('Saving…'), findsNothing);
    await t.tap(find.text('Tuesday'));
    await t.pump();
    await t.tap(find.text('Remove past days off'));
    await t.pump();
    await t.tap(find.byType(TkSwitch).last);
    await t.pump();
    expect(find.text('Unsaved changes'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Save booking settings'));
    await settle(t);
    final body = jsonDecode(sent.single.substring(sent.single.indexOf('{'))) as Map<String, dynamic>;
    final av = body['availability'] as Map<String, dynamic>;
    expect(av['days'], [
      [
        {'open': '09:00', 'close': '17:00'},
      ],
      [
        {'open': '09:00', 'close': '17:00'},
      ],
      [],
      [],
      [],
      [],
      [],
    ]);
    expect(av.containsKey('off'), isFalse);
    expect(av['auto_confirm'], true);
    expect(body['services_table'], 'T1');
    expect(find.text('Saved; your page is updated.'), findsOneWidget);
  });
}
