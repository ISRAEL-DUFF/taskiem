// A card's own steps, as a person uses them: an order that came in,
// Declined only once asked (Cancel sends nothing), Accepted at once; each
// said in a toast afterwards, as the web's are.

import 'dart:convert';

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  Future<List<String>> open(WidgetTester t, {Size size = const Size(390, 844)}) async {
    t.view.physicalSize = size;
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final posts = <String>[];
    http.Response answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages' && !req.url.queryParameters.containsKey('after')) {
          // The newest page, with an order from them after it.
          final page = f.json(p);
          (page['messages'] as List).add({
            'id': 'ord-7',
            'link': _link,
            'mine': false,
            'kind': 'card',
            'card': {'doc': 'order', 'event': 'ordered', 'reference': 'ORD-7', 'id': 'o7', 'amount_kobo': 5600000},
            'sender_name': 'Tunde',
            'created_at': '2026-11-11T12:25:00Z',
            'read_at': '2026-11-11T12:26:00Z',
          });
          return answer(page);
        }
        if (req.method == 'GET') return null;
        if (p.endsWith('/read')) return answer({'read': 0});
        posts.add(p);
        return answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    return posts;
  }

  // Phone feedback: on a phone with large text the three buttons squeezed
  // into one row, "Accept" broken over two lines or cut to "Acc…".
  for (final scale in [1.0, 1.3, 2.0]) {
    testWidgets('a new order on a small phone, text ×$scale: Accept and Decline side by side, View order the whole width under them', (t) async {
      t.platformDispatcher.textScaleFactorTestValue = scale;
      addTearDown(t.platformDispatcher.clearTextScaleFactorTestValue);
      await open(t, size: const Size(360, 640));
      final accept = find.bySemanticsLabel('Accept ORD-7');
      final decline = find.bySemanticsLabel('Decline ORD-7');
      final view = find.bySemanticsLabel('View order ORD-7');
      await t.ensureVisible(view);
      await t.pumpAndSettle();
      final a = t.getRect(accept), d = t.getRect(decline), v = t.getRect(view);
      if (scale < 2) {
        expect(a.top, moreOrLessEquals(d.top, epsilon: 1), reason: 'one row');
        expect(a.width, moreOrLessEquals(d.width, epsilon: 1), reason: 'as wide as each other');
        expect(v.width, moreOrLessEquals(d.right - a.left, epsilon: 1), reason: 'the whole width');
      } else {
        // Twice the size, the two do not fit side by side in the bubble:
        // each the whole width, one under the other, rather than cut.
        expect(d.top, greaterThan(a.bottom));
        expect(a.width, moreOrLessEquals(v.width, epsilon: 1));
        expect(d.width, moreOrLessEquals(v.width, epsilon: 1));
      }
      expect(v.top, greaterThan(d.bottom), reason: 'View order under them');
      // Each word whole, on one line.
      for (final w in ['Accept', 'Decline', 'View order']) {
        final text = t.renderObject<RenderParagraph>(find.text(w));
        expect(text.didExceedMaxLines, isFalse, reason: '$w cut');
        expect(text.size.height, lessThan(14 * scale * 1.2 * 1.5), reason: '$w on one line');
      }
    });
  }

  testWidgets('Decline asks first; then it is sent and said', (t) async {
    final posts = await open(t);
    await t.tap(find.bySemanticsLabel('Decline ORD-7'));
    await t.pumpAndSettle();
    expect(find.text('Decline ORD-7?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Cancel'));
    await t.pumpAndSettle();
    expect(posts, isEmpty);
    await t.tap(find.bySemanticsLabel('Decline ORD-7'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Decline').last);
    await t.pumpAndSettle();
    await settle(t);
    expect(posts, ['/v1/trade-orders/o7/decline']);
    expect(find.text('ORD-7 declined'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    expect(find.text('ORD-7 declined'), findsNothing);
  });

  testWidgets('Accept goes at once, and is said', (t) async {
    final posts = await open(t);
    await t.tap(find.bySemanticsLabel('Accept ORD-7'));
    await settle(t);
    expect(posts, ['/v1/trade-orders/o7/accept']);
    expect(find.text('ORD-7 accepted'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });
}
