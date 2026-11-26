// Holding messages, as WhatsApp's on a phone (the founder's feedback of 25
// November 2026): a message held is selected, its row tinted and the head
// turned into the selection's bar; a tap adds another or takes it away;
// with several only what may be done with all of them is offered; Back, ✕
// or letting the last one go ends it; and the reactions float on their own
// over the one held, on the screen even over a very long message.

import 'dart:convert';
import 'dart:io' show Platform;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/menu.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

/// A long message: far taller than the phone.
final _long = List.generate(60, (i) => 'Line ${i + 1} of the price list, every item and its price.').join('\n');

Map<String, Object> _msg(String id, String body, {required bool mine, required int minute}) => {
  'id': id,
  'link': _link,
  'mine': mine,
  'kind': 'text',
  'body': body,
  'created_at': DateTime.utc(2026, 11, 11, 9, minute).toIso8601String(),
  'read_at': '2026-11-11T12:00:00Z',
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  /// The conversation with Mama Tunde's Market, its messages [messages];
  /// what the screen sends, kept.
  Future<List<(String, String, Object?)>> open(WidgetTester t, List<Map<String, Object>> messages, {TkMode? mode}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final calls = <(String, String, Object?)>[];
    http.Response answer(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages') {
          if (req.url.queryParameters.containsKey('after')) return answer({'messages': <Object>[]});
          return answer({'messages': messages});
        }
        if (req.method == 'GET') return null;
        if (p.endsWith('/read') || p.endsWith('/typing')) return answer(<String, Object>{});
        final body = req.body.isEmpty ? null : jsonDecode(req.body);
        calls.add((req.method, p, body));
        if (p.endsWith('/star')) return answer({'starred': req.method == 'PUT'});
        return answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    await t.pumpWidget(TaskiemApp(mode: mode ?? (Platform.environment['SNAP_DARK'] == '1' ? TkMode.dark : TkMode.light), home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    return calls;
  }

  final three = [
    _msg('a', 'Good morning ma', mine: false, minute: 1),
    _msg('b', 'Bring 4 baskets', mine: true, minute: 2),
    _msg('c', 'No problem', mine: false, minute: 3),
  ];

  Finder words(String w) => find.textContaining(w, findRichText: true);

  /// A tap on a message's row, at its edge (clear of the reactions
  /// floating over the one held).
  Future<void> tapRow(WidgetTester t, String w) => t.tapAt(Offset(4, t.getCenter(words(w)).dy));
  final bar = find.byType(SelectionBar);
  final count = find.descendant(of: bar, matching: find.byType(Text));

  testWidgets('held: selected, the bar in the head; a tap adds another; several offer only what suits all', (t) async {
    String? copied;
    t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied = (call.arguments as Map)['text'] as String;
      return null;
    });
    final calls = await open(t, three);
    expect(bar, findsNothing);
    await t.longPress(words('Bring 4 baskets'));
    await t.pumpAndSettle();
    expect(bar, findsOneWidget);
    expect(t.widget<Text>(count.first).data, '1');
    expect(heldActions(t), containsAllInOrder(['Reply', 'Star', 'Delete', 'Forward', 'Copy', 'Message info', 'Save as…']));
    expect(find.bySemanticsLabel('React with heart'), findsOneWidget, reason: 'the reactions over the one held');
    await snap(t, 'selected-one');

    // A tap on another: added (its own action not done).
    await tapRow(t, 'Good morning ma');
    await t.pumpAndSettle();
    expect(t.widget<Text>(count.first).data, '2');
    expect(heldActions(t), ['Star', 'Delete', 'Forward', 'Copy'], reason: 'Reply, Edit, Info and Save as need exactly one');
    expect(find.bySemanticsLabel('React with heart'), findsNothing, reason: 'reactions with one only');
    await snap(t, 'selected-two');

    // Copy: their words, in the thread's order, and the selection ends.
    await chooseHeld(t, 'Copy');
    expect(copied, 'Good morning ma\nBring 4 baskets');
    expect(bar, findsNothing);
    expect(find.text('2 messages copied'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));

    // Star for both, in one press.
    await t.longPress(words('Good morning ma'));
    await t.pumpAndSettle();
    await tapRow(t, 'No problem');
    await t.pumpAndSettle();
    await chooseHeld(t, 'Star');
    await settle(t);
    expect([for (final c in calls) '${c.$1} ${c.$2}'], [
      'PUT /v1/link-chats/$_link/messages/a/star',
      'PUT /v1/link-chats/$_link/messages/c/star',
    ]);
    expect(bar, findsNothing);
  });

  testWidgets('Delete for several: their choices once, then each deleted', (t) async {
    final calls = await open(t, three);
    await t.longPress(words('Good morning ma'));
    await t.pumpAndSettle();
    await tapRow(t, 'Bring 4 baskets');
    await t.pumpAndSettle();
    await chooseHeld(t, 'Delete');
    await t.pumpAndSettle();
    expect(find.text('Delete 2 messages?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Delete for me').last);
    await settle(t);
    expect([for (final c in calls) c.$2], ['/v1/link-chats/$_link/messages/a/hide', '/v1/link-chats/$_link/messages/b/hide']);
    expect(words('Good morning ma'), findsNothing);
    expect(words('No problem'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the selection ends with Back, with ✕, and with the last one let go', (t) async {
    await open(t, three);
    await t.longPress(words('Good morning ma'));
    await t.pumpAndSettle();
    expect(bar, findsOneWidget);
    // Android's Back: the selection ends, the chat stays.
    await t.binding.handlePopRoute();
    await t.pumpAndSettle();
    expect(bar, findsNothing);
    expect(find.byType(ConversationScreen), findsOneWidget);

    await t.longPress(words('Good morning ma'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Close the selection'));
    await t.pumpAndSettle();
    expect(bar, findsNothing);

    await t.longPress(words('Good morning ma'));
    await t.pumpAndSettle();
    await tapRow(t, 'No problem');
    await t.pumpAndSettle();
    await tapRow(t, 'Good morning ma');
    await t.pumpAndSettle();
    expect(t.widget<Text>(count.first).data, '1');
    expect(find.bySemanticsLabel('React with heart'), findsOneWidget, reason: 'one again: its reactions');
    await tapRow(t, 'No problem');
    await t.pumpAndSettle();
    expect(bar, findsNothing);
  });

  testWidgets('the reactions stay on the screen over a very long message, and follow it', (t) async {
    await open(t, [
      _msg('a', 'Good morning ma', mine: false, minute: 1),
      _msg('long', _long, mine: false, minute: 2),
    ]);
    final view = t.getRect(find.byType(ThreadView<ThreadMessage>));
    // Unfolded to its end: far taller than the thread.
    final more = find.textContaining('… Read more', findRichText: true);
    for (var i = 0; i < 6 && more.evaluate().isNotEmpty; i++) {
      await t.ensureVisible(more);
      await t.pumpAndSettle();
      await t.tapAt(t.getBottomLeft(more) + const Offset(40, -10));
      await t.pumpAndSettle();
    }
    expect(t.getRect(words('Line 1 of')).height, greaterThan(view.height));
    // The message fills the thread: held in its middle.
    await t.longPressAt(view.center);
    await t.pumpAndSettle();
    final pill = find.bySemanticsLabel('React');
    expect(pill, findsOneWidget);
    var r = t.getRect(pill);
    expect(r.top, greaterThanOrEqualTo(view.top), reason: 'on the screen, not above it');
    expect(r.bottom, lessThanOrEqualTo(view.bottom));
    await snap(t, 'selected-long');
    // Scrolled up to the message's top: it sits just over it.
    final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
    thread.scroll.jumpTo(thread.scroll.position.minScrollExtent);
    await t.pumpAndSettle();
    final top = t.getRect(words('Line 1 of the price list'));
    expect(top.top, greaterThan(view.top), reason: 'the message\'s top in sight');
    r = t.getRect(pill);
    expect(r.bottom, lessThan(top.top), reason: 'above the message');
    expect(r.top, greaterThanOrEqualTo(view.top));
    // The reaction given: the selection ends.
    await t.tap(find.bySemanticsLabel('React with heart'));
    await settle(t);
    expect(bar, findsNothing);
  });
}
