// What a conversation leaves behind when it is closed early, and what the
// app does with what it cannot read (docs/spec/mobile-app-m3-fixes.md
// B4–B7).

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/chat/taskiem.dart';

import 'auth_test.dart' show FakeServer, json, me;
import 'support.dart';

const _tunde = '3af349e2-b776-4306-bdb8-10df59687c55';
const _chidi = '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab';

/// A recorder whose start is held until [go], and whose file is
/// [kept] (null: the file is not there).
class _SlowRecorder extends Recorder {
  final go = Completer<void>();
  bool began = false, ended = false;
  Uint8List? kept = Uint8List.fromList(List.filled(2048, 1));
  @override
  Future<bool> mayRecord() async => true;
  @override
  Future<String> place() async => '/tmp/voice.m4a';
  @override
  Future<void> begin(String path) async {
    await go.future;
    began = true;
  }

  @override
  Future<String?> end() async {
    ended = true;
    return '/tmp/voice.m4a';
  }

  @override
  Future<Uint8List?> take(String path, {required bool keep}) async => keep ? kept : null;
}

class _BrokenKeyStore implements KeyStore {
  @override
  Future<String?> read(String key) => throw Exception('the phone\'s keys are gone');
  @override
  Future<void> write(String key, String? value) => throw Exception('the phone\'s keys are gone');
}

void main() {
  /// Every request; the first page of each conversation held at [gate].
  ({ChatsData chats, List<String> asked, Completer<void> gate}) world(String fixture, {bool offline = false}) {
    final f = Fixture(fixture);
    final asked = <String>[];
    final gate = Completer<void>();
    final chats = ChatsData(
      f.auth(also: (req) async {
        asked.add('${req.method} ${req.url.path}');
        final p = req.url.path;
        if (req.method == 'GET' && (p == '/v1/link-chats/$_tunde/messages' || p == '/v1/taskiem-chat' || p == '/v1/customer-chats/$_chidi/messages')) {
          await gate.future;
        }
        if (offline && req.method == 'POST') throw http.ClientException('no network');
        return null;
      }),
      now: () => f.at,
    );
    return (chats: chats, asked: asked, gate: gate);
  }

  Future<void> closedEarly(WidgetTester t, String fixture, ChangeNotifier Function(ChatsData) make, Future<void> Function(ChangeNotifier) open) async {
    final w = world(fixture);
    final d = make(w.chats);
    final opening = open(d);
    await t.pump();
    d.dispose();
    w.gate.complete();
    await opening;
    await t.pump();
    w.asked.clear();
    // Were its polls started, they would ask within these 30 seconds (and
    // leave timers the test would find pending).
    await t.pump(const Duration(seconds: 30));
    expect(w.asked, isEmpty);
  }

  group('B4: closed before the first page came, nothing is left following', () {
    testWidgets('a business\'s conversation', (t) async {
      final conv = ListLinkChatsResponse.fromJson(Fixture('chat-tunde').json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _tunde);
      await closedEarly(t, 'chat-tunde', (c) => ConversationData(c, conv), (d) => (d as ConversationData).open());
    });
    testWidgets('Taskiem\'s', (t) async {
      await closedEarly(t, 'chat-taskiem', (c) => TaskiemData(c), (d) => (d as TaskiemData).open());
    });
    testWidgets('a customer\'s', (t) async {
      await closedEarly(t, 'customer-chidi', (c) => CustomerData(c, _chidi), (d) => (d as CustomerData).open());
    });
  });

  group('B5: a send failing after the screen is gone is let be', () {
    testWidgets('to a customer', (t) async {
      final w = world('customer-chidi', offline: true);
      w.gate.complete();
      final d = CustomerData(w.chats, _chidi, follow: false);
      final heard = <String>[];
      d.givenBack.listen(heard.add);
      final sending = d.send('Are you open today?');
      d.dispose();
      await sending;
      expect(heard, isEmpty);
    });
    testWidgets('to Taskiem', (t) async {
      final w = world('chat-taskiem', offline: true);
      w.gate.complete();
      final d = TaskiemData(w.chats, follow: false);
      final sending = d.say(text: 'status');
      d.dispose();
      await sending;
    });
  });

  group('B6: opening the app never stays on the opening screen', () {
    test('the phone\'s storage cannot be read: signed out', () async {
      final a = Auth(transport: Transport(base: 'http://localhost', client: FakeServer().client()), store: SessionStore(_BrokenKeyStore()));
      await a.start();
      expect(a.state, AuthState.signedOut);
    });
    test('who-am-I answers what this app cannot read: signed out, the token forgotten', () async {
      final server = FakeServer();
      server.answers['GET /v1/me'] = () => json({'tenant_id': 7});
      server.answers['GET /v1/app/config'] = () => json({'min_version': 3});
      final keys = MemoryKeyStore()..values['token'] = 'tok-1';
      final a = Auth(transport: Transport(base: 'http://localhost', client: server.client()), store: SessionStore(keys));
      await a.start();
      expect(a.state, AuthState.signedOut);
      expect(keys.values['token'], isNull);
    });
    test('a good session still opens', () async {
      final server = FakeServer();
      server.answers['GET /v1/me'] = () => json(me);
      final keys = MemoryKeyStore()..values['token'] = 'tok-1';
      final a = Auth(transport: Transport(base: 'http://localhost', client: server.client()), store: SessionStore(keys));
      await a.start();
      expect(a.state, AuthState.signedIn);
    });
  });

  group('B7: a voice note stopped while the microphone is starting', () {
    test('waits for the start, then stops it, and keeps nothing', () async {
      final r = _SlowRecorder();
      final starting = r.start();
      final stopping = r.stop(keep: true);
      r.go.complete();
      expect(await starting, isTrue);
      expect(await stopping, isNull);
      expect(r.began && r.ended, isTrue, reason: 'nothing left recording');
    });
    test('a note that has begun is kept; a missing file is no note; nothing to stop is nothing', () async {
      final r = _SlowRecorder()..go.complete();
      await r.start();
      expect((await r.stop(keep: true))!.type, 'audio/mp4');
      expect(await r.stop(keep: true), isNull, reason: 'stopped already');
      await r.start();
      r.kept = null;
      expect(await r.stop(keep: true), isNull);
    });
  });
}
