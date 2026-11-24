// A deal on its card, held to the web's own tests (web/src/lib/deals.test.ts),
// and as a person answers one: the buyer accepting Bayo's counter,
// countering it (a price that makes no sense refused first), cancelling
// once asked; the seller accepting an offer and keeping the price.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/deal.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _bayo = 'ef99249d-9322-40ca-91c2-e24fdd450c15';
const _order = '01a1ca87-1f7a-752f-99df-17e296553a3a';

void main() {
  group('the words (deals.test.ts)', () {
    test('reads naira as people type them in a deal', () {
      expect(dealKobo('760k'), 76000000);
      expect(dealKobo('₦760,000'), 76000000);
      expect(dealKobo('1.2m'), 120000000);
      expect(dealKobo('N 5,000.50'), 500050);
      expect(dealKobo('lots'), isNull);
      expect(dealKobo(''), isNull);
    });

    test('says a deal against what it was', () {
      expect(offWords(74000000, 79000000), '6% off');
      expect(offWords(80000000, 79000000), '');
    });

    test('tells the history and when it lapses', () {
      LinkDocCardDealStepsItem s(String by, int kobo, [String? who]) => LinkDocCardDealStepsItem(by: by, totalKobo: kobo, who: who, at: DateTime.utc(2026));
      expect(stepWords(s('buyer', 74000000), 0, 'seller'), 'They offered ₦740,000');
      expect(stepWords(s('seller', 77000000, 'Tunde'), 1, 'seller'), 'Tunde countered ₦770,000');
      expect(stepWords(s('buyer', 76000000), 2, 'buyer'), 'You countered ₦760,000');
      final now = DateTime.utc(2026, 11, 7, 10);
      expect(lapseWords(DateTime.utc(2026, 11, 9, 9, 30), now), 'Lapses in 47 hours');
      expect(lapseWords(DateTime.utc(2026, 11, 7, 10, 20), now), 'Lapses in 20 minutes');
      expect(lapseWords(DateTime.utc(2026, 11, 7, 9), now), 'Lapsed');
      expect(lapseWords(null, now), '');
    });

    test('knows a card\'s side and whether its deal is still open', () {
      expect(cardSide('order'), 'seller');
      expect(cardSide('purchase'), 'buyer');
      final now = DateTime.utc(2026, 11, 7, 10);
      expect(dealOpen(null, now), isTrue);
      expect(dealOpen(LinkDocCardDeal(steps: const [], until: DateTime.utc(2026, 11, 8, 10)), now), isTrue);
      expect(dealOpen(LinkDocCardDeal(steps: const [], until: DateTime.utc(2026, 11, 7, 9)), now), isFalse);
    });
  });

  group('on its card', () {
    setUpAll(loadFonts);

    /// Bayo's conversation with a deal at its end: by default Bayo's counter
    /// on our order; [card] puts another in its place.
    Future<List<(String, String, Object?)>> open(WidgetTester t, {Map<String, Object?>? card, Map<String, Object?>? answer}) async {
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final f = Fixture('chat-deal');
      final sent = <(String, String, Object?)>[];
      final chats = ChatsData(
        f.auth(also: (req) async {
          if (req.method == 'GET' && req.url.path == '/v1/link-chats/$_bayo/messages' && req.url.queryParameters.isEmpty && card != null) {
            final page = f.json(req.url.path);
            final list = page['messages'] as List;
            (list.last as Map)['card'] = card;
            return http.Response(jsonEncode(page), 200, headers: {'content-type': 'application/json'});
          }
          if (req.method == 'GET') return null;
          if (req.url.path.endsWith('/read')) return http.Response('{}', 200, headers: {'content-type': 'application/json'});
          sent.add((req.method, req.url.path, req.body.isEmpty ? null : jsonDecode(req.body)));
          return http.Response(jsonEncode(answer ?? {}), 200, headers: {'content-type': 'application/json'});
        }),
        now: () => f.at,
      )..load();
      final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _bayo);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
      for (var i = 0; i < 8; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
        await t.pump();
      }
      final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
      thread.scroll.jumpTo(thread.scroll.position.maxScrollExtent);
      await t.pump();
      return sent;
    }

    Future<void> settle(WidgetTester t) async {
      for (var i = 0; i < 6; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
        await t.pump();
      }
    }

    testWidgets('the buyer accepts the counter', (t) async {
      final sent = await open(t);
      expect(find.text('Ada offered ₦740,000'), findsOneWidget);
      expect(find.text('They countered ₦770,000'), findsOneWidget);
      expect(find.text('Lapses in 47 hours'), findsOneWidget);
      await t.tap(find.bySemanticsLabel('Accept ₦770,000 P-9XTV54'));
      await settle(t);
      expect(sent.single.$2, '/v1/stock/purchases/$_order/accept-change');
      expect(find.text('Agreed ₦770,000 on P-9XTV54'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('the buyer counters: a price that makes no sense first', (t) async {
      final sent = await open(t);
      await t.tap(find.bySemanticsLabel('Counter P-9XTV54'));
      await t.pump();
      final boxes = find.descendant(of: find.byType(DealActions), matching: find.byType(EditableText));
      await t.enterText(boxes.first, 'lots');
      await t.pump();
      expect(find.text('A price in naira, like 770,000 or 770k.'), findsOneWidget);
      await t.enterText(boxes.first, '800k');
      await t.pump();
      expect(find.text('A counter is under their ₦770,000.'), findsOneWidget);
      await t.ensureVisible(find.bySemanticsLabel('Send counter P-9XTV54'));
      await t.pump();
      await t.tap(find.bySemanticsLabel('Send counter P-9XTV54'));
      await t.pump();
      expect(sent, isEmpty, reason: 'not sent while it makes no sense');
      await t.enterText(boxes.first, '755k');
      await t.enterText(boxes.last, 'Meet me halfway');
      await t.pump();
      expect(find.text('4% off their price'), findsOneWidget);
      await t.ensureVisible(find.bySemanticsLabel('Send counter P-9XTV54'));
      await t.pump();
      await t.tap(find.bySemanticsLabel('Send counter P-9XTV54'));
      await settle(t);
      expect((sent.single.$1, sent.single.$2), ('POST', '/v1/stock/purchases/$_order/counter'));
      expect(sent.single.$3, {'total_kobo': 75500000, 'note': 'Meet me halfway'});
      expect(find.text('Counter sent: ₦755,000'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('the buyer cancels, once asked', (t) async {
      final sent = await open(t);
      await t.tap(find.bySemanticsLabel('Cancel P-9XTV54'));
      await t.pumpAndSettle();
      expect(find.text('Cancel P-9XTV54?'), findsOneWidget);
      await t.tap(find.bySemanticsLabel('Cancel the order'));
      await t.pumpAndSettle();
      await settle(t);
      expect(sent.single.$2, '/v1/stock/purchases/$_order/cancel');
      expect(find.text('P-9XTV54 cancelled'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    final offer = <String, Object?>{
      'doc': 'order',
      'event': 'offered',
      'reference': 'O-77QK2D',
      'id': 'o77',
      'amount_kobo': 74000000,
      'was_kobo': 79000000,
      'deal': {
        'steps': [
          {'by': 'buyer', 'total_kobo': 74000000, 'at': '2026-11-11T12:20:00Z'},
        ],
        'until': '2026-11-13T12:20:00Z',
      },
    };

    testWidgets('the seller accepts an offer, keeping the price 14 days', (t) async {
      final sent = await open(t, card: offer);
      await t.tap(find.bySemanticsLabel('Keep this price for them: This order only'));
      await t.pumpAndSettle();
      await t.tap(find.bySemanticsLabel('14 days'));
      await t.pumpAndSettle();
      await t.tap(find.bySemanticsLabel('Accept ₦740,000 O-77QK2D'));
      await settle(t);
      expect(sent.single.$2, '/v1/trade-orders/o77/accept');
      expect(sent.single.$3, {'keep_days': 14});
      expect(find.text('Agreed ₦740,000, kept for them 14 days'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('under the lowest price, it waits for the owner', (t) async {
      await open(t, card: offer, answer: {'waiting': 'owner'});
      await t.tap(find.bySemanticsLabel('Accept ₦740,000 O-77QK2D'));
      await settle(t);
      expect(find.text('Under your lowest price: O-77QK2D waits for the owner\'s OK'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('a lapsed offer has nothing to answer', (t) async {
      await open(t, card: {
        ...offer,
        'deal': {
          'steps': [
            {'by': 'buyer', 'total_kobo': 74000000, 'at': '2026-11-09T12:20:00Z'},
          ],
          'until': '2026-11-10T12:20:00Z',
        },
      });
      expect(find.text('This offer lapsed: nothing to answer'), findsOneWidget);
      expect(find.bySemanticsLabel('Accept ₦740,000 O-77QK2D'), findsNothing);
    });
  });
}
