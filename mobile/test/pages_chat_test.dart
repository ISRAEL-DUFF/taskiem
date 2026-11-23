// Pages in the chat (pages G3, docs/spec/pages.md §9): in Taskiem's
// conversation a pinned page is a tile of the tools panel, under Your
// pages; "/" and a word finds any page the person may open; "/" and a
// page's name sent opens the page over the chat. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/chat/tools_panel.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'made_tools_stub.dart';
import 'support.dart';

Map<String, dynamic> _summary(String name, String title, {bool pinned = false, String about = ''}) => {
  'name': name,
  'title': title,
  'icon': 'tables',
  'about': about,
  'roles': <String>[],
  'pinned': pinned,
  'published': true,
  'has_draft': false,
  'version': 1,
  'updated_at': '2026-11-22T10:00:00Z',
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  final asked = <String>[];

  Future<void> taskiem(WidgetTester t, {TkMode mode = TkMode.light}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tools');
    final server = MadeServer();
    final json = {'content-type': 'application/json'};
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          asked.add(req.url.path);
          if (req.url.path == '/v1/pages') {
            return http.Response(
              jsonEncode({
                'pages': [_summary('tickets', 'Tickets', pinned: true, about: 'The support desk'), _summary('leave-days', 'Leave days')],
                'can_edit': false,
                'can_publish': false,
                'max_pinned': 5,
                'max_pages': 10,
              }),
              200,
              headers: json,
            );
          }
          if (req.url.path == '/v1/pages/tickets/view') {
            return http.Response(
              jsonEncode({
                'page': _summary('tickets', 'Tickets', pinned: true),
                'draft': false,
                'can_edit': false,
                'drawn_at': '2026-11-22T10:00:00Z',
                'filters': <Object>[],
                'blocks': [
                  {'id': 'open', 'type': 'figure', 'label': 'Open', 'value': 7, 'shown': '7', 'tone': 'warn', 'lead': true, 'naira': false},
                ],
              }),
              200,
              headers: json,
            );
          }
          return server.call(req);
        },
      ),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: mode, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await settle(t);
  }

  Finder tile(String name) => find.bySemanticsLabel(RegExp('^$name, '));
  Finder box() => find.bySemanticsLabel('Message to Taskiem');

  testWidgets('a pinned page is a tile; "/" finds any page; "/" and its name opens it', (t) async {
    await taskiem(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    expect(find.text('Your pages'), findsOneWidget);
    expect(tile('Tickets'), findsOneWidget);
    expect(tile('Leave days'), findsNothing, reason: 'only pinned ones without a word');
    await t.ensureVisible(tile('Tickets'));
    await t.pumpAndSettle();
    await looks(t, 'chat-pages-tile');
    await t.tap(find.bySemanticsLabel('Close tools'));
    await settle(t);

    await t.enterText(box(), '/leave');
    await settle(t);
    expect(tile('Leave days'), findsOneWidget);

    // Sent: the page opens over the chat; nothing goes to Taskiem.
    await t.enterText(box(), '/tickets');
    await settle(t);
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(asked, contains('/v1/pages/tickets/view'));
    expect(find.text('7'), findsOneWidget);
  });

  test('which pages a panel shows, and the page "/" and a name opens', () {
    final pages = [PageSummary.fromJson(_summary('tickets', 'Tickets', pinned: true)), PageSummary.fromJson(_summary('leave-days', 'Leave days'))];
    expect(pagesMatching(pages, '').map((p) => p.name), ['tickets']);
    expect(pagesMatching(pages, 'lea').map((p) => p.name), ['leave-days']);
    expect(pagesMatching(pages, 'tick').map((p) => p.name), ['tickets']);
  });
}
