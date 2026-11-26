// Editing a sent message (docs/spec/chat-previews-and-editing.md §3.2,
// §3.3), in a conversation with a linked business: Edit in the hold menu
// only where the rules allow; the "Editing message" strip, the tick and ✕;
// the bubble's "Edited"; refused after its time, the note with Send as a
// new message; an edit made offline kept in the outbox and sent when the
// connection is back; Edit on a message still waiting to send changes it
// there, so one message goes.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/edits.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  http.Response answer(Object body, [int code = 200]) => http.Response(jsonEncode(body), code, headers: {'content-type': 'application/json'});

  /// The conversation with Tunde: ours of five minutes ago, theirs, and
  /// ours of 40 minutes ago; the edits and sends the app makes, noted; the
  /// server's answer to an edit given by [edit], offline while [offline].
  Future<({List<Map<String, dynamic>> edits, List<Map<String, dynamic>> sends, ConversationData data})> open(
    WidgetTester t, {
    TkMode mode = TkMode.light,
    http.Response Function(Map<String, dynamic> body)? edit,
    bool Function()? offline,
  }) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final edits = <Map<String, dynamic>>[];
    final sends = <Map<String, dynamic>>[];
    Map<String, dynamic> msg(String id, String body, Duration ago, {bool mine = false, int n = 0, bool edited = false}) => {
      'id': id,
      'link': _link,
      'mine': mine,
      'kind': 'text',
      'body': body,
      'sender_name': mine ? 'Ada' : 'Tunde',
      'created_at': f.at.subtract(ago).toUtc().toIso8601String(),
      'read_at': f.at.toUtc().toIso8601String(),
      'edits': n,
      if (edited) 'edited_at': f.at.toUtc().toIso8601String(),
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (offline != null && offline() && req.method != 'GET') throw http.ClientException('no network');
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages' && !req.url.queryParameters.containsKey('after')) {
          if (offline != null && offline()) throw http.ClientException('no network');
          final page = f.json(p);
          (page['messages'] as List).addAll([
            msg('m-old', 'We open at eight', const Duration(minutes: 40), mine: true),
            msg('m-theirs', 'Is the rice in?', const Duration(minutes: 6)),
            msg('m-mine', 'Rice is 45,000 a bag', const Duration(minutes: 5), mine: true),
          ]);
          return answer(page);
        }
        if (req.method == 'PATCH' && p.startsWith('/v1/link-chats/$_link/messages/')) {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          edits.add({'id': p.split('/').last, ...b});
          if (edit != null) return edit(b);
          return answer({'message': msg(p.split('/').last, b['text'] as String, const Duration(minutes: 5), mine: true, n: (b['edits'] as int) + 1, edited: true)});
        }
        if (req.method == 'POST' && p == '/v1/link-chats/$_link/messages') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          sends.add(b);
          return answer({'message': msg('sent-${sends.length}', b['text'] as String, Duration.zero, mine: true)}, 201);
        }
        if (req.method == 'GET') return null;
        return answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    final data = ConversationData(chats, conv, follow: false);
    await t.pumpWidget(TaskiemApp(mode: mode, home: ConversationScreen(data: data)));
    await settle(t);
    for (var i = 0; i < 6 && find.textContaining('Rice is 45,000 a bag', findRichText: true).evaluate().isEmpty; i++) {
      await t.drag(find.byType(Scrollable).first, const Offset(0, -400));
      await settle(t);
    }
    await t.drag(find.byType(Scrollable).first, const Offset(0, -2000));
    await settle(t);
    return (edits: edits, sends: sends, data: data);
  }

  Future<void> hold(WidgetTester t, String words) async {
    await t.longPress(find.textContaining(words, findRichText: true).first);
    await t.pumpAndSettle();
  }

  Future<void> closeMenu(WidgetTester t) async {
    await t.tapAt(const Offset(195, 60));
    await t.pumpAndSettle();
  }

  test('the rules: our own words within 30 minutes, never a card, money, a voice note or a forward', () {
    final now = DateTime.utc(2026, 11, 26, 12);
    String? block({bool mine = true, String kind = 'text', int ago = 5, bool forwarded = false, bool money = false, String? file, List<String> tags = const [], String? line}) =>
        editBlock(mine: mine, kind: kind, createdAt: now.subtract(Duration(minutes: ago)), now: now, forwarded: forwarded, money: money, fileKind: file, tags: tags, line: line);
    expect(block(), isNull);
    expect(block(ago: 30), isNull);
    expect(block(ago: 31), 'too_old');
    expect(block(mine: false), 'not_yours');
    expect(block(kind: 'card'), 'card');
    expect(block(kind: 'diary'), 'card');
    expect(block(money: true), 'money');
    expect(block(tags: ['invoice']), 'money');
    expect(block(tags: ['business']), isNull);
    expect(block(kind: 'media', file: 'voice'), 'voice');
    expect(block(kind: 'media', file: 'photo'), isNull);
    expect(block(forwarded: true), 'forwarded');
    expect(block(line: 'whatsapp'), 'elsewhere');
  });

  testWidgets('Edit is in the menu only where it is allowed', (t) async {
    await open(t);
    await hold(t, 'Rice is 45,000 a bag');
    expect(find.text('Edit'), findsOneWidget);
    expect(find.text('Copy'), findsOneWidget);
    await closeMenu(t);
    await hold(t, 'Is the rice in?');
    expect(find.text('Copy'), findsOneWidget);
    expect(find.text('Edit'), findsNothing);
    await closeMenu(t);
    await hold(t, 'We open at eight');
    expect(find.text('Copy'), findsOneWidget);
    expect(find.text('Edit'), findsNothing, reason: 'past its 30 minutes');
  });

  testWidgets('the strip, ✕ to cancel, the tick to save, and Edited on the bubble', (t) async {
    final s = await open(t);
    await hold(t, 'Rice is 45,000 a bag');
    await t.tap(find.text('Edit'));
    await t.pumpAndSettle();
    expect(find.text('Editing message'), findsOneWidget);
    expect(find.bySemanticsLabel('Save the edit'), findsOneWidget);
    expect(find.bySemanticsLabel('Send'), findsNothing);
    // ✕: nothing saved, the box empty.
    await t.tap(find.bySemanticsLabel('Cancel the edit'));
    await t.pumpAndSettle();
    expect(find.text('Editing message'), findsNothing);
    expect(s.edits, isEmpty);

    await hold(t, 'Rice is 45,000 a bag');
    await t.tap(find.text('Edit'));
    await t.pumpAndSettle();
    await t.enterText(find.byType(EditableText).last, 'Rice is 44,000 a bag');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save the edit'));
    await settle(t);
    expect(s.edits, [
      {'id': 'm-mine', 'text': 'Rice is 44,000 a bag', 'edits': 0},
    ]);
    expect(s.sends, isEmpty, reason: 'an edit is not a new message');
    expect(find.textContaining('Rice is 44,000 a bag', findRichText: true), findsOneWidget);
    expect(find.textContaining('Rice is 45,000 a bag', findRichText: true), findsNothing);
    expect(find.text('Edited'), findsOneWidget);
    expect(find.text('Editing message'), findsNothing);
  });

  testWidgets('refused after its time: the words go back, with Send as a new message', (t) async {
    final s = await open(
      t,
      edit: (_) => answer({'error': 'This message can no longer be edited: messages can be edited for 30 minutes after they are sent.', 'not_editable': 'too_old'}, 403),
    );
    await hold(t, 'Rice is 45,000 a bag');
    await t.tap(find.text('Edit'));
    await t.pumpAndSettle();
    await t.enterText(find.byType(EditableText).last, 'Rice is 44,000 a bag');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save the edit'));
    await settle(t);
    // The message as the server has it, and a note under the refused words.
    expect(find.textContaining('Rice is 45,000 a bag', findRichText: true), findsOneWidget);
    expect(find.text('Your edit was not saved: the message can only be edited for 30 minutes after it was sent'), findsOneWidget);
    expect(find.text('Edited'), findsNothing);
    await t.tap(find.text('Send as a new message'));
    await settle(t);
    expect(s.sends.single['text'], 'Rice is 44,000 a bag');
    expect(find.textContaining('Your edit was not saved'), findsNothing);
  });

  testWidgets('an edit made offline waits in the outbox and goes when the connection is back', (t) async {
    var down = false;
    final s = await open(t, offline: () => down);
    down = true;
    await hold(t, 'Rice is 45,000 a bag');
    await t.tap(find.text('Edit'));
    await t.pumpAndSettle();
    await t.enterText(find.byType(EditableText).last, 'Rice is 44,000 a bag');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save the edit'));
    await settle(t);
    // Shown at once, marked Edited; kept until it has gone.
    expect(find.textContaining('Rice is 44,000 a bag', findRichText: true), findsOneWidget);
    expect(find.text('Edited'), findsOneWidget);
    expect(s.data.outbox.single.editOf, 'm-mine');
    expect(s.data.outbox.single.kept(_link).json['edit'], {'message': 'm-mine', 'edits': 0});
    expect(s.edits, isEmpty, reason: 'offline: nothing reached the server');
    down = false;
    await t.runAsync(() => s.data.openNewest());
    await settle(t);
    expect(s.edits, hasLength(1));
    expect(s.edits.last, {'id': 'm-mine', 'text': 'Rice is 44,000 a bag', 'edits': 0});
    expect(s.data.outbox, isEmpty);
    expect(find.text('Edited'), findsOneWidget);
  });

  testWidgets('Edit on a message still waiting to send changes it there: one message goes', (t) async {
    var down = false;
    final s = await open(t, offline: () => down);
    down = true;
    await t.enterText(find.byType(EditableText).last, 'Delivery on Monday');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(s.data.outbox.single.body, 'Delivery on Monday');
    await hold(t, 'Delivery on Monday');
    await t.tap(find.text('Edit'));
    await t.pumpAndSettle();
    await t.enterText(find.byType(EditableText).last, 'Delivery on Tuesday');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save the edit'));
    await settle(t);
    expect(s.data.outbox.single.body, 'Delivery on Tuesday');
    expect(s.data.outbox.single.editOf, isNull);
    down = false;
    await t.runAsync(() => s.data.openNewest());
    await settle(t);
    expect(s.sends.map((b) => b['text']), ['Delivery on Tuesday']);
    expect(s.edits, isEmpty);
  });

  // Pictures to look at (SNAP_DIR): the menu with Edit, the strip, the
  // edited bubble, and a refused edit's note, light and dark.
  for (final mode in TkMode.values) {
    testWidgets('pictures, ${mode.name}', (t) async {
      await open(t, mode: mode);
      await hold(t, 'Rice is 45,000 a bag');
      await snap(t, 'edit-menu-${mode.name}');
      await t.tap(find.text('Edit'));
      await t.pumpAndSettle();
      await t.enterText(find.byType(EditableText).last, 'Rice is 44,000 a bag, this week only');
      await t.pump();
      await snap(t, 'edit-strip-${mode.name}');
      await t.tap(find.bySemanticsLabel('Save the edit'));
      await settle(t);
      expect(find.text('Edited'), findsOneWidget);
      await snap(t, 'edit-edited-${mode.name}');
    });
    testWidgets('pictures, refused, ${mode.name}', (t) async {
      await open(t, mode: mode, edit: (_) => answer({'error': 'too old', 'not_editable': 'too_old'}, 403));
      await hold(t, 'Rice is 45,000 a bag');
      await t.tap(find.text('Edit'));
      await t.pumpAndSettle();
      await t.enterText(find.byType(EditableText).last, 'Rice is 44,000 a bag');
      await t.pump();
      await t.tap(find.bySemanticsLabel('Save the edit'));
      await settle(t);
      expect(find.text('Send as a new message'), findsOneWidget);
      await snap(t, 'edit-refused-${mode.name}');
    });
  }
}
