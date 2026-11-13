// The app with no connection, as WhatsApp works (docs/spec/mobile-app-
// explore-and-offline.md §3): it opens at once on what the phone kept,
// who is signed in included; the chats, a conversation and its files read
// offline; a message written offline is kept and goes once back; signing
// out leaves nothing behind.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/app/store.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

void main() {
  setUpAll(loadFonts);
  late Directory tmp;
  late MemoryKeyStore keys;
  setUp(() {
    tmp = Directory.systemTemp.createTempSync('offline');
    LocalStore.enabled = true;
    LocalStore.dataRoot = () async => Directory('${tmp.path}/data');
    LocalStore.mediaRoot = () async => Directory('${tmp.path}/media');
    keys = MemoryKeyStore();
  });
  tearDown(() {
    LocalStore.current?.close();
    LocalStore.current = null;
    LocalStore.enabled = false;
    tmp.deleteSync(recursive: true);
  });

  /// The fixture's answers while [online] says so; else no connection.
  ({Auth auth, List<String> asked}) signedIn(Fixture f, bool Function() online, {Future<http.Response?> Function(http.Request)? also}) {
    final asked = <String>[];
    final a = f.auth(also: (req) async {
      if (!online()) throw http.ClientException('no connection');
      asked.add('${req.method} ${req.url.path}');
      return also?.call(req);
    });
    return (auth: a, asked: asked);
  }

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  test('a file seen once opens again from the phone, offline too', () async {
    final store = await LocalStore.open('t_u', keys);
    LocalStore.current = store;
    var fetched = 0;
    var online = true;
    final transport = Transport(
      base: 'http://localhost',
      token: 'x',
      client: MockClient((req) async {
        if (!online) throw http.ClientException('no connection');
        fetched++;
        return http.Response.bytes([1, 2, 3, 4], 200, headers: {'content-type': 'application/pdf'});
      }),
    );
    final m = LinkMessage(
      id: 'f1',
      link: 'L1',
      kind: 'file',
      mine: false,
      createdAt: DateTime.utc(2026, 11, 13),
      attachment: const LinkMessageAttachment(kind: 'pdf', size: 4, type: 'application/pdf'),
    );
    expect((await ChatFiles(transport).fetch(m))!.bytes, [1, 2, 3, 4]);
    await Future<void>.delayed(const Duration(milliseconds: 50));
    online = false;
    // The app opened again: nothing in memory, no connection.
    expect((await ChatFiles(transport).fetch(m))!.bytes, [1, 2, 3, 4]);
    expect(fetched, 1);
  });

  test('opened offline: signed in at once as who was kept, permissions and all; a session the server ended signs out', () async {
    final f = Fixture('chats');
    // Signed in before: the token, and who it was, kept.
    var online = true;
    var meStatus = 200;
    final client = f.client(also: (req) async {
      if (!online) throw http.ClientException('no connection');
      if (req.url.path == '/v1/me' && meStatus != 200) return http.Response('{"error":"Session ended."}', meStatus);
      return null;
    });
    final first = Auth(transport: Transport(base: 'http://localhost', client: client), store: SessionStore(keys));
    await keys.write('token', 'tok');
    await first.start();
    expect(first.state, AuthState.signedIn);
    expect(first.local, isNotNull, reason: 'its store opened once who it is was known');
    first.local!.close();

    online = false;
    final later = Auth(transport: Transport(base: 'http://localhost', client: client), store: SessionStore(keys));
    await later.start();
    expect(later.state, AuthState.signedIn);
    expect(later.restored, isTrue);
    expect(later.can('link.manage'), isTrue, reason: 'permissions as kept');
    expect(later.me!.tenantName, f.json('/v1/me')['tenant_name']);
    later.local!.close();

    // Back online, the session ended meanwhile: signed out, nothing left.
    online = true;
    meStatus = 401;
    final ended = Auth(transport: Transport(base: 'http://localhost', client: client), store: SessionStore(keys));
    await ended.start();
    for (var i = 0; i < 20 && ended.state != AuthState.signedOut; i++) {
      await Future<void>.delayed(const Duration(milliseconds: 20));
    }
    expect(ended.state, AuthState.signedOut);
    expect(Directory('${tmp.path}/data').listSync(), isEmpty);
    expect(keys.values.keys.where((k) => k.startsWith('store-key:')), isEmpty);
  });

  testWidgets('the chats offline: as kept, said quietly; read whole again once back', (t) async {
    final f = Fixture('chats');
    final store = await t.runAsync(() => LocalStore.open('t_u', keys));
    var online = true;
    final s = signedIn(f, () => online);
    s.auth.local = store;
    final first = ChatsData(s.auth, now: () => f.at);
    await t.runAsync(first.load);
    expect(first.conversations, isNotEmpty);
    first.dispose();

    // The app opened again, offline: the list at once, as kept.
    online = false;
    final again = signedIn(f, () => online);
    again.auth.local = store;
    final chats = ChatsData(again.auth, now: () => f.at);
    expect(chats.loaded, isTrue);
    expect(chats.conversations.map((c) => c.name), first.conversations.map((c) => c.name));
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: chats)));
    await settle(t);
    expect(find.text('Bayo Rice Mills'), findsOneWidget, reason: 'drawn from what was kept, before any answer');
    await t.runAsync(chats.load);
    await t.pump();
    expect(find.textContaining('You’re offline. These are your chats as they were'), findsOneWidget);
    // Back.
    online = true;
    await t.runAsync(chats.load);
    await t.pump();
    expect(find.textContaining('You’re offline'), findsNothing);
    store!.close();
  });

  test('a conversation offline: as kept; a message written then is kept, and goes once back, once', () async {
    final f = Fixture('chat-tunde');
    final store = await LocalStore.open('t_u', keys);
    var online = true;
    final sent = <Map<String, dynamic>>[];
    final s = signedIn(
      f,
      () => online,
      also: (req) async {
        if (req.method == 'POST' && req.url.path == '/v1/link-chats/$_link/messages') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          sent.add(b);
          return http.Response(
            jsonEncode({
              'message': {'id': 'm-new', 'link': _link, 'mine': true, 'kind': 'text', 'body': b['text'], 'created_at': f.at.toIso8601String()},
            }),
            201,
            headers: {'content-type': 'application/json'},
          );
        }
        return null;
      },
    );
    s.auth.local = store;
    final chats = ChatsData(s.auth, now: () => f.at);
    await chats.load();
    final conv = chats.conversations.firstWhere((c) => c.link == _link);
    final first = ConversationData(chats, conv, follow: false);
    await first.open();
    final shown = first.messages!.map((m) => m.id).toList();
    expect(shown, isNotEmpty);
    first.dispose();

    // Opened again with no connection: the messages as kept.
    online = false;
    final again = ConversationData(chats, conv, follow: false);
    await again.open();
    expect(again.messages!.map((m) => m.id), shown);
    expect(again.offline, isTrue);
    expect(again.error, isNull, reason: 'said quietly, not as an error');
    await again.send('Are you open tomorrow?');
    expect(again.outbox.single.failed, isFalse, reason: 'waiting with its clock, not failed');
    expect(store.outbox().single.json['text'], 'Are you open tomorrow?');
    again.dispose();

    // The app closed; back online later: it goes from the list's load, once.
    online = true;
    await chats.load();
    for (var i = 0; i < 20 && store.outbox().isNotEmpty; i++) {
      await Future<void>.delayed(const Duration(milliseconds: 20));
    }
    expect(store.outbox(), isEmpty);
    expect(sent.single['text'], 'Are you open tomorrow?');
    expect(sent.single['client_id'], isNotEmpty);
    store.close();
  });

  test('signing out leaves nothing of the business on the phone', () async {
    final f = Fixture('chats');
    final s = signedIn(f, () => true);
    await keys.write('token', 'tok');
    final a = Auth(transport: s.auth.transport, store: SessionStore(keys));
    a.me = s.auth.me;
    a.local = await LocalStore.open('t_u', keys);
    a.local!.write('chats', {'x': 1});
    await a.local!.keepMedia('/v1/links/x/logo', Uint8List.fromList([1]));
    await a.signOut();
    expect(a.local, isNull);
    expect(LocalStore.current, isNull);
    expect(Directory('${tmp.path}/data').listSync(), isEmpty);
    expect(Directory('${tmp.path}/media').listSync(), isEmpty);
  });

  test('what changed while closed comes into what is kept: written, reacted to, deleted', () async {
    final f = Fixture('chats');
    final store = await LocalStore.open('t_u', keys);
    store.keepMessages(_link, [
      {'id': 'old', 'link': _link, 'kind': 'text', 'mine': false, 'body': 'Gone soon', 'created_at': '2026-11-12T09:00:00Z'},
      {'id': 'kept', 'link': _link, 'kind': 'text', 'mine': true, 'body': 'Two bags', 'created_at': '2026-11-12T09:01:00Z'},
    ]);
    final asked = <String>[];
    final s = signedIn(
      f,
      () => true,
      also: (req) async {
        if (req.url.path != '/v1/link-chats/changes') return null;
        final since = req.url.queryParameters['since'];
        asked.add(since ?? '');
        final body = since == null
            ? {'messages': <Object>[], 'gone': <Object>[], 'until': '2026-11-13T08:00:00Z', 'more': false}
            : {
                'messages': [
                  {'id': 'new', 'link': _link, 'kind': 'text', 'mine': false, 'body': 'Rice is back', 'created_at': '2026-11-13T08:30:00Z'},
                  {
                    'id': 'kept',
                    'link': _link,
                    'kind': 'text',
                    'mine': true,
                    'body': 'Two bags',
                    'created_at': '2026-11-12T09:01:00Z',
                    'reactions': [
                      {'emoji': '👍', 'count': 1},
                    ],
                  },
                ],
                'gone': [
                  {'id': 'old', 'link': _link},
                ],
                'until': '2026-11-13T09:00:00Z',
                'more': false,
              };
        return http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
      },
    );
    s.auth.local = store;
    final chats = ChatsData(s.auth, now: () => f.at);
    // The first time: only where to begin.
    await chats.syncChanges();
    expect(store.messages(_link).map((m) => m['id']), ['old', 'kept']);
    // Then what changed since.
    await chats.syncChanges();
    expect(asked, ['', '2026-11-13T08:00:00Z']);
    final kept = store.messages(_link);
    expect(kept.map((m) => m['id']), ['kept', 'new']);
    expect(kept.first['reactions'], isNotEmpty);
    expect(store.read('changes_until'), '2026-11-13T09:00:00Z');
    chats.dispose();
    store.close();
  });

  test('pages kept: asked again with their ETag, unchanged a 304; offline, the kept page; the chats keep their own', () async {
    final store = await LocalStore.open('t_u', keys);
    var online = true;
    final seen = <String?>[];
    final transport = Transport(
      base: 'http://localhost',
      token: 'x',
      client: MockClient((req) async {
        if (!online) throw http.ClientException('no connection');
        seen.add(req.headers['If-None-Match']);
        if (req.headers['If-None-Match'] == 'W/"v1"') return http.Response('', 304, headers: {'etag': 'W/"v1"'});
        return http.Response(jsonEncode({'orders': ['AK-1']}), 200, headers: {'content-type': 'application/json', 'etag': 'W/"v1"'});
      }),
    );
    transport.pages = store;
    expect(await transport.send('GET', '/v1/storefront/orders'), {'orders': ['AK-1']});
    expect(await transport.send('GET', '/v1/storefront/orders'), {'orders': ['AK-1']}, reason: 'a 304: the kept page');
    expect(seen, [null, 'W/"v1"']);
    expect(transport.offline.value, isFalse);
    online = false;
    expect(await transport.send('GET', '/v1/storefront/orders'), {'orders': ['AK-1']});
    expect(transport.offline.value, isTrue);
    // Never read before: offline is offline.
    await expectLater(transport.send('GET', '/v1/money/summary'), throwsA(isA<Offline>()));
    // The chats are not kept here: their own store says offline its way.
    online = true;
    await transport.send('GET', '/v1/link-chats');
    online = false;
    await expectLater(transport.send('GET', '/v1/link-chats'), throwsA(isA<Offline>()));
    // Nor a search, nor a write.
    expect(Transport.keeps('/v1/stock', {'q': 'rice'}), isFalse);
    online = true;
    await transport.send('POST', '/v1/storefront/orders', body: const {});
    expect(transport.offline.value, isFalse);
    store.close();
  });
}
