// A conversation's contact info and its head's menu, as a person uses
// them: Pin, Mute and Archive set (each said in a toast), the link ended
// once asked, a starred message opened, Search from there, and the menu.

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

const _link = '828e255f-dc0f-4dd3-bb19-0ba2764778d3';

http.Response _answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
}

void main() {
  setUpAll(loadFonts);

  Future<(ConversationData, List<(String, String, Object?)>)> open(WidgetTester t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-info');
    final sent = <(String, String, Object?)>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET') return null;
        if (req.url.path.endsWith('/read')) return _answer({'read': 0});
        sent.add((req.method, req.url.path, req.body.isEmpty ? null : jsonDecode(req.body)));
        return _answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    final data = ConversationData(chats, conv, follow: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: data)));
    await _settle(t);
    return (data, sent);
  }

  Future<void> contact(WidgetTester t) async {
    await t.tap(find.bySemanticsLabel("About Mama Tunde's Market"));
    await t.pumpAndSettle();
    await _settle(t);
  }

  testWidgets('Pin, Mute and Archive: set, and said', (t) async {
    final (_, sent) = await open(t);
    await contact(t);
    await t.tap(find.bySemanticsLabel('Pin'));
    await _settle(t);
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Mute notifications'));
    await _settle(t);
    // The toast sits over the row under it until it goes.
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Archive this chat'));
    await _settle(t);
    expect(sent.map((s) => (s.$1, s.$2)), List.filled(3, ('PUT', '/v1/link-chats/$_link')));
    expect(sent.map((s) => s.$3), [
      {'pinned': true},
      {'muted': true},
      {'archived': true},
    ]);
    expect(find.text('Archived'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('End link: asked first, then ended and said', (t) async {
    final (_, sent) = await open(t);
    await contact(t);
    await t.scrollUntilVisible(find.text("End link with Mama Tunde's Market"), 200);
    await t.tap(find.text("End link with Mama Tunde's Market"));
    await t.pumpAndSettle();
    expect(find.text("End the link with Mama Tunde's Market?"), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Cancel'));
    await t.pumpAndSettle();
    expect(sent, isEmpty);
    await t.tap(find.text("End link with Mama Tunde's Market"));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('End link'));
    await t.pumpAndSettle();
    await _settle(t);
    expect((sent.single.$1, sent.single.$2), ('POST', '/v1/links/$_link/end'));
    expect(find.text('Link ended'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a starred message: the thread at it, flashed', (t) async {
    final (data, _) = await open(t);
    await contact(t);
    await t.tap(find.bySemanticsLabel('Starred messages'));
    await t.pumpAndSettle();
    await t.tap(find.textContaining('Good morning ma'));
    await _settle(t);
    expect(find.text('Contact info'), findsNothing);
    expect(t.widget<ThreadFlash>(find.byType(ThreadFlash)).id, data.messages!.firstWhere((m) => (m.body ?? '').startsWith('Good morning')).id);
    await t.pump(const Duration(seconds: 2));
  });

  testWidgets('Search, from contact info', (t) async {
    await open(t);
    await contact(t);
    await t.tap(find.bySemanticsLabel('Search'));
    await t.pumpAndSettle();
    expect(find.text('Search messages'), findsOneWidget);
  });

  testWidgets('the head\'s menu: Contact info, Pin, Mute, Archive', (t) async {
    final (_, sent) = await open(t);
    await t.tap(find.bySemanticsLabel('More about this conversation'));
    await t.pumpAndSettle();
    expect(find.text('Keyboard'), findsNothing, reason: 'a computer\'s');
    await t.tap(find.text('Mute'));
    await _settle(t);
    expect(sent.single.$3, {'muted': true});
    expect(find.text('Muted'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('More about this conversation'));
    await t.pumpAndSettle();
    await t.tap(find.text('Contact info'));
    await t.pumpAndSettle();
    expect(find.text('Starred messages'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });
}
