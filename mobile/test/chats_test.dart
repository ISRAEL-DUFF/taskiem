// The chats list as a person uses it: a request to link answered (Block
// asked first), the Starred chip's messages, one opened at itself in its
// conversation, and that conversation back at its newest from there.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _kola = '69e1c5b4-3a47-49b6-827a-f0d24955eee6';
const _tunde = '3af349e2-b776-4306-bdb8-10df59687c55';
const _first = '01a1ca87-3b17-7eb0-85d8-4fd9c674a645';

http.Response _answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
}

void main() {
  setUpAll(loadFonts);

  Future<List<String>> list(WidgetTester t, {void Function(LinkConversation, String)? onOpenAt}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats-starred');
    final asked = <String>[];
    final data = ChatsData(
      f.auth(also: (req) async {
        if (req.method != 'GET') {
          asked.add('${req.method} ${req.url.path}');
          return _answer(<String, Object>{});
        }
        if (req.url.path == '/v1/link-chats/starred') asked.add('GET ${req.url.path}');
        return null;
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: data, onOpenAt: onOpenAt)));
    await _settle(t);
    return asked;
  }

  testWidgets('Accept a request to link, said once done', (t) async {
    final asked = await list(t);
    await t.tap(find.bySemanticsLabel("Accept Kola's Bakery"));
    await _settle(t);
    expect(asked, ['POST /v1/links/$_kola/accept']);
    expect(find.text("Linked with Kola's Bakery"), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Block asks first; Cancel sends nothing', (t) async {
    final asked = await list(t);
    await t.tap(find.bySemanticsLabel("Block Kola's Bakery"));
    await t.pumpAndSettle();
    expect(find.text("Block Kola's Bakery?"), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Cancel'));
    await t.pumpAndSettle();
    expect(asked, isEmpty);
    await t.tap(find.bySemanticsLabel("Block Kola's Bakery"));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Block').last);
    await t.pumpAndSettle();
    await _settle(t);
    expect(asked, ['POST /v1/links/$_kola/block']);
    expect(find.text('Blocked'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Starred: the messages, each opened at itself', (t) async {
    (LinkConversation, String)? opened;
    final asked = await list(t, onOpenAt: (c, m) => opened = (c, m));
    await t.tap(find.bySemanticsLabel(RegExp('^Starred')));
    await _settle(t);
    expect(asked, ['GET /v1/link-chats/starred']);
    expect(find.text('Starred messages'), findsOneWidget);
    await t.tap(find.textContaining('Good morning ma'));
    expect(opened?.$1.name, "Mama Tunde's Market");
    expect(opened?.$2, _first);
  });

  testWidgets('opened at a message: in the middle, then back to the newest', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final newest = f.json('/v1/link-chats/$_tunde/messages');
    final asked = <String>[];
    // Around the message: 25 newer ones after it, so the newest page is
    // beyond this one.
    final around = {
      'messages': [
        for (var n = 0; n < 10; n++)
          {'id': 'o$n', 'link': _tunde, 'mine': n.isOdd, 'kind': 'text', 'body': 'Earlier $n', 'created_at': DateTime.utc(2026, 11, 11, 11, n).toIso8601String(), 'read_at': '2026-11-11T11:50:00Z'},
        ...(newest['messages'] as List).take(1),
        for (var n = 0; n < 25; n++)
          {'id': 'n$n', 'link': _tunde, 'mine': n.isOdd, 'kind': 'text', 'body': 'Later $n', 'created_at': DateTime.utc(2026, 11, 11, 12, 14 + n).toIso8601String(), 'read_at': '2026-11-11T12:50:00Z'},
      ],
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/link-chats/$_tunde/messages') {
          final q = req.url.queryParameters;
          asked.add(q.isEmpty ? 'newest' : q.entries.map((e) => '${e.key}=${e.value}').join('&'));
          return _answer(q.containsKey('around') ? around : newest);
        }
        if (req.method == 'GET') return null;
        return _answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _tunde);
    final data = ConversationData(chats, conv, follow: false, focus: _first);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: data)));
    await _settle(t);
    expect(asked, ['around=$_first&limit=50']);
    expect(data.detached, isTrue);
    expect(find.text('2 unread messages'), findsNothing, reason: 'no unread mark away from the newest');
    // At it, in the middle of the thread (between the head and the box),
    // not at the newest; flashed.
    final hit = t.getCenter(find.textContaining('Good morning ma', findRichText: true)).dy;
    final view = t.getRect(find.byType(ThreadView<ThreadMessage>));
    expect(hit, moreOrLessEquals(view.center.dy, epsilon: 12), reason: 'its bubble centred; its words a little above');
    final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
    expect(thread.scroll.offset, lessThan(thread.scroll.position.maxScrollExtent - 100));
    expect(t.widget<ThreadFlash>(find.byType(ThreadFlash)).id, _first);
    await t.tap(find.bySemanticsLabel('Jump to the latest messages'));
    await _settle(t);
    expect(asked.last, 'newest');
    expect(data.detached, isFalse);
    expect(find.textContaining('No problem. My boy will be there', findRichText: true), findsOneWidget);
    await t.pump(const Duration(seconds: 2));
  });
}
