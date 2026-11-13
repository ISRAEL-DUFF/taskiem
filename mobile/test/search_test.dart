// Search in a conversation, as a person uses it: the words typed, the
// messages found with the words marked, one in the thread and one older,
// and a day from the calendar (or none on or after it).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/chat/words.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';
const _first = '01a1ca87-3b17-7eb0-85d8-4fd9c674a645';
const _older = '01a1ca86-0000-7000-8000-00000000c0de';

http.Response _answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
}

void main() {
  setUpAll(loadFonts);

  test('a result\'s line: the words around the hit, marked', () {
    expect(highlight('Big Tomatoes today', 'tomato'), [(text: 'Big ', hit: false), (text: 'Tomato', hit: true), (text: 'es today', hit: false)]);
    final long = '${'word ' * 40}the tomato is here ${'more ' * 30}';
    final a = around(long, 'tomato');
    expect(a.length, lessThanOrEqualTo(142));
    expect(a, startsWith('…'));
    expect(a, contains('tomato'));
    expect(around('short one', 'x'), 'short one');
  });

  Future<(ConversationData, List<String>)> open(WidgetTester t, {Object? onDay}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-search');
    final asked = <String>[];
    final around = {
      'messages': [
        {'id': _older, 'link': _link, 'mine': true, 'kind': 'text', 'body': 'Do you still have the big tomatoes from last week?', 'created_at': '2026-11-04T09:12:00Z', 'read_at': '2026-11-04T09:20:00Z'},
      ],
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        final q = req.url.queryParameters;
        if (req.method == 'GET' && req.url.path == '/v1/link-chats/$_link/messages' && q.isNotEmpty) {
          asked.add(q.entries.map((e) => '${e.key}=${e.value}').join('&'));
          if (q.containsKey('around')) return _answer(around);
          if (q['limit'] == '1') return _answer(onDay ?? {'messages': <Object>[]});
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    final data = ConversationData(chats, conv, follow: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: data)));
    await _settle(t);
    await t.tap(find.bySemanticsLabel('Search this conversation'));
    await t.pumpAndSettle();
    return (data, asked);
  }

  String? flashed(WidgetTester t) => t.widget<ThreadFlash>(find.byType(ThreadFlash)).id;

  testWidgets('words found: one in the thread, flashed there', (t) async {
    final (data, asked) = await open(t);
    expect(find.text('Search for messages within Mama Tunde\'s Market.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).last, 'tomato');
    await t.pump(const Duration(milliseconds: 100));
    expect(asked, isEmpty, reason: 'not while still typing');
    await t.pump(const Duration(milliseconds: 200));
    await _settle(t);
    expect(asked, ['q=tomato']);
    expect(find.text('Funke'), findsOneWidget);
    expect(find.text('4 November 2026'), findsOneWidget);
    await t.tap(find.text('Funke'));
    await _settle(t);
    expect(find.text('Search messages'), findsNothing, reason: 'closed to show it');
    expect(flashed(t), _first);
    expect(data.detached, isFalse);
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('words found: an older one, the page around it', (t) async {
    final (data, asked) = await open(t);
    await t.enterText(find.byType(EditableText).last, 'tomato');
    await t.pump(const Duration(milliseconds: 300));
    await _settle(t);
    await t.tap(find.text('4 November 2026'));
    await _settle(t);
    expect(asked.last, 'around=$_older&limit=50');
    expect(data.messages!.single.id, _older);
    expect(flashed(t), _older);
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('a day: its first message; a day with none says so', (t) async {
    final (_, asked) = await open(t);
    await t.tap(find.bySemanticsLabel('Search by date'));
    await t.pumpAndSettle();
    expect(find.text('November 2026'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Thursday 12 November 2026'), warnIfMissed: false);
    await t.pump();
    expect(asked, isEmpty, reason: 'a day after today is not to be chosen');
    expect(find.text('November 2026'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Previous month'));
    await t.pump();
    expect(find.text('October 2026'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Next month'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Tuesday 10 November 2026'));
    await t.pumpAndSettle();
    await _settle(t);
    expect(asked.single, 'after=${DateTime(2026, 11, 10).subtract(const Duration(milliseconds: 1)).toUtc().toIso8601String()}&limit=1');
    expect(find.text('No messages on or after that day.'), findsOneWidget);
  });

  testWidgets('Back closes the calendar first, then the search', (t) async {
    await open(t);
    await t.tap(find.bySemanticsLabel('Search by date'));
    await t.pumpAndSettle();
    await t.binding.handlePopRoute();
    await t.pumpAndSettle();
    expect(find.text('November 2026'), findsNothing);
    expect(find.text('Search messages'), findsOneWidget);
    await t.binding.handlePopRoute();
    await t.pumpAndSettle();
    expect(find.text('Search messages'), findsNothing);
  });
}
