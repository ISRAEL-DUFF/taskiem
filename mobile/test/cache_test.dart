// What the app keeps of what it fetched, and when it asks again
// (docs/spec/mobile-app-m3-fixes.md B9, B10, B12).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

LinkMessage _pdf(String id) => LinkMessage(
  id: id,
  link: 'l1',
  kind: 'file',
  mine: false,
  createdAt: DateTime.utc(2026, 11, 11),
  attachment: const LinkMessageAttachment(kind: 'pdf', size: 1000, type: 'application/pdf'),
);

void main() {
  group('B10: the files kept', () {
    test('at most the limit, those drawn least lately let go first', () async {
      final asked = <String>[];
      final files = ChatFiles(
        Transport(
          base: 'http://localhost',
          client: MockClient((req) async {
            asked.add(req.url.pathSegments.last);
            return http.Response.bytes(List.filled(1000, 1), 200);
          }),
        ),
        limit: 3000,
      );
      for (final id in ['a', 'b', 'c']) {
        await files.fetch(_pdf(id));
      }
      expect(files.kept, 3000);
      // a drawn again: b is now the least lately drawn.
      expect(files.of(_pdf('a')), isNotNull);
      await files.fetch(_pdf('d'));
      expect(files.kept, 3000);
      asked.clear();
      expect(files.of(_pdf('a')), isNotNull);
      expect(files.of(_pdf('c')), isNotNull);
      expect(files.of(_pdf('d')), isNotNull);
      expect(asked, isEmpty);
      expect(files.of(_pdf('b')), isNull, reason: 'let go: fetched again');
      await Future<void>.delayed(Duration.zero);
      expect(asked, ['b']);
    });

    test('a file that failed is not asked for on every redraw, but after a minute, or pressed', () async {
      var now = DateTime(2026, 11, 11, 9);
      var asked = 0;
      final files = ChatFiles(
        Transport(
          base: 'http://localhost',
          client: MockClient((req) async {
            asked++;
            return http.Response('{"error":"not found"}', 404);
          }),
        ),
        now: () => now,
      );
      expect(files.of(_pdf('x')), isNull);
      await Future<void>.delayed(Duration.zero);
      expect(asked, 1);
      for (var i = 0; i < 5; i++) {
        expect(files.of(_pdf('x')), isNull);
      }
      await Future<void>.delayed(Duration.zero);
      expect(asked, 1, reason: 'redrawn, not asked again');
      await files.fetch(_pdf('x'));
      expect(asked, 2, reason: 'pressed (a voice note played): asked');
      now = now.add(const Duration(minutes: 1));
      files.of(_pdf('x'));
      await Future<void>.delayed(Duration.zero);
      expect(asked, 3);
    });
  });

  test('B9: a logo that failed is asked again a minute later; one there is none of, never', () async {
    var now = DateTime(2026, 11, 11, 9);
    var down = true;
    final asked = <String>[];
    final f = Fixture('chats');
    final auth = f.auth(also: (req) async {
      if (!req.url.path.endsWith('/logo')) return null;
      asked.add(req.url.path);
      if (req.url.path.contains('none')) return http.Response('{"error":"no logo"}', 404);
      if (down) throw http.ClientException('no network');
      return http.Response.bytes([1, 2, 3], 200);
    });
    final chats = ChatsData(auth, now: () => now);
    Future<void> settle() => Future<void>.delayed(const Duration(milliseconds: 10));
    expect(chats.logo('some'), isNull);
    expect(chats.logo('none'), isNull);
    await settle();
    expect(asked, hasLength(2));
    chats.logo('some');
    chats.logo('none');
    await settle();
    expect(asked, hasLength(2), reason: 'not again at once');
    down = false;
    now = now.add(const Duration(minutes: 1));
    chats.logo('some');
    chats.logo('none');
    await settle();
    expect(asked, ['/v1/links/some/logo', '/v1/links/none/logo', '/v1/links/some/logo']);
    expect(chats.logo('some'), [1, 2, 3]);
  });

  testWidgets('B12: moving the cursor in the chats\' search asks nothing', (t) async {
    await loadFonts();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final searched = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.url.path.endsWith('/search')) {
          searched.add(req.url.query);
          return http.Response(jsonEncode({'results': <Object>[], 'messages': <Object>[], 'links': <Object>[]}), 200, headers: {'content-type': 'application/json'});
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: chats)));
    await t.pump();
    final box = find.byType(EditableText).first;
    await t.enterText(box, 'rice');
    await t.pump(const Duration(seconds: 1));
    await t.pump();
    final once = searched.length;
    expect(once, greaterThan(0), reason: 'typing searched');
    final editable = t.state<EditableTextState>(box);
    for (var i = 0; i < 4; i++) {
      editable.userUpdateTextEditingValue(editable.textEditingValue.copyWith(selection: TextSelection.collapsed(offset: i)), SelectionChangedCause.tap);
      await t.pump(const Duration(seconds: 1));
    }
    expect(searched.length, once, reason: 'the words did not change');
  });
}
