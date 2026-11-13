// The chat's moments (docs/spec/mobile-app-m3-fixes.md G12; web
// lib/moments.ts): a chime for a new order or payment while a conversation
// is open, and for a new alert card, switched off from the chats' menu;
// a ring round a good moment.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/app/sound.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _bayo = 'ef99249d-9322-40ca-91c2-e24fdd450c15';

http.Response _json(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

void main() {
  test('the two notes: a WAV half a second long, rising, quiet', () {
    final w = chimeWav();
    expect(String.fromCharCodes(w.sublist(0, 4)), 'RIFF');
    expect(String.fromCharCodes(w.sublist(8, 12)), 'WAVE');
    final data = ByteData.sublistView(w);
    final n = data.getUint32(40, Endian.little) ~/ 2;
    expect(n, (22050 * 0.52).round());
    var loudest = 0;
    for (var k = 0; k < n; k++) {
      final v = data.getInt16(44 + k * 2, Endian.little).abs();
      if (v > loudest) loudest = v;
    }
    expect(loudest, inInclusiveRange(2000, 32767 ~/ 4), reason: 'soft, as the web\'s 0.08 gain');
  });

  test('what is news, and what a good moment (moments.ts)', () {
    expect(isNews('ordered', false), isTrue);
    expect(isNews('declared', false), isTrue);
    expect(isNews('ordered', true), isFalse);
    expect(isNews('accept', false), isFalse);
    expect(isGoodMoment('declared', false, false), isTrue);
    expect(isGoodMoment('ordered', false, true), isTrue);
    expect(isGoodMoment('ordered', false, false), isFalse);
    expect(isGoodMoment('declared', true, true), isFalse);
  });

  group('heard', () {
    late List<String> rang;
    late Future<void> Function() was;
    setUp(() {
      rang = [];
      was = chime;
      chime = () async => rang.add('ding');
    });
    tearDown(() => chime = was);

    test('a new alert card chimes; the first read is quiet; off is off', () async {
      final f = Fixture('chat-taskiem');
      var extra = false;
      final prefs = Prefs(MemoryKeyStore());
      await prefs.load();
      final chats = ChatsData(
        f.auth(also: (req) async {
          if (req.url.path != '/v1/chat-alerts') return null;
          final j = f.json('/v1/chat-alerts');
          if (extra) {
            (j['alerts'] as List).add({'key': 'new_order:x1', 'kind': 'new_order', 'at': '2026-11-11T13:40:00Z', 'title': 'New order P-NEW from Rivers Foods Ltd', 'line': '₦90,000', 'id': 'x1'});
          }
          return _json(j);
        }),
        now: () => f.at,
        prefs: prefs,
      );
      await chats.load();
      expect(rang, isEmpty, reason: 'the first read');
      await chats.load();
      expect(rang, isEmpty, reason: 'nothing new');
      extra = true;
      await chats.load();
      expect(rang, ['ding']);
      await prefs.setSound(false);
      extra = false;
      await chats.load();
      extra = true;
      await chats.load();
      expect(rang, ['ding'], reason: 'switched off');
    });

    testWidgets('an order or payment arriving in an open conversation: the chime, and the ring for a good moment', (t) async {
      await loadFonts();
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final f = Fixture('chat-bayo');
      final page = f.json('/v1/link-chats/$_bayo/messages');
      final bill = (page['messages'] as List).cast<Map<String, dynamic>>().firstWhere((m) => (m['card'] as Map?)?['event'] == 'bill');
      Map<String, dynamic>? arriving;
      final chats = ChatsData(
        f.auth(also: (req) async {
          if (req.method == 'GET' && req.url.path == '/v1/link-chats/$_bayo/messages' && req.url.queryParameters.containsKey('after')) {
            return _json({'messages': [?arriving]});
          }
          if (req.method == 'POST') return _json(<String, Object>{});
          return null;
        }),
        now: () => f.at,
      );
      await chats.load();
      expect(chats.chatTools, isTrue);
      final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _bayo);
      final d = ConversationData(chats, conv, follow: false);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: d)));
      for (var i = 0; i < 6; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
        await t.pump();
      }
      expect(rang, isEmpty, reason: 'what was there already just shows');
      arriving = {
        ...bill,
        'id': 'paid-1',
        'created_at': f.at.add(const Duration(minutes: 1)).toUtc().toIso8601String(),
        'card': {...bill['card'] as Map<String, dynamic>, 'event': 'declared'},
      };
      await t.runAsync(d.pull);
      await t.pump();
      expect(rang, ['ding']);
      expect(d.cheer, 'paid-1');
      expect(find.byType(CheerRing), findsOneWidget);
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 950)));
      await t.pump(const Duration(milliseconds: 950));
      expect(d.cheer, isNull);
      expect(find.byType(CheerRing), findsNothing);
      await t.runAsync(d.pull);
      await t.pump();
      expect(rang, ['ding'], reason: 'told once');
    });
  });

  testWidgets('the chats\' menu: Settings, and the sound switched', (t) async {
    await loadFonts();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final prefs = Prefs(MemoryKeyStore());
    await prefs.load();
    final chats = ChatsData(f.auth(), now: () => f.at, prefs: prefs);
    var settings = 0;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: chats, onMenu: () => settings++)));
    await t.runAsync(chats.load);
    await t.pump();
    await t.tap(find.bySemanticsLabel('More about the chats'));
    await t.pumpAndSettle();
    if (chats.chatTools) {
      await t.tap(find.text('Sound for new orders and payments: on'));
      await t.pumpAndSettle();
      expect(prefs.sound, isFalse);
      await t.tap(find.bySemanticsLabel('More about the chats'));
      await t.pumpAndSettle();
      expect(find.text('Sound for new orders and payments: off'), findsOneWidget);
    }
    await t.tap(find.text('Settings'));
    await t.pumpAndSettle();
    expect(settings, 1);
  });
}
