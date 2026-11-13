// What the app keeps on the phone (lib/app/store.dart; docs/spec/
// mobile-app-explore-and-offline.md §3): encrypted, one store a business
// and person, messages to their limit, files to theirs, wiped on signing
// out and after 30 days unopened.

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/app/store.dart';

void main() {
  late Directory tmp;
  setUp(() {
    tmp = Directory.systemTemp.createTempSync('store');
    LocalStore.enabled = true;
    LocalStore.dataRoot = () async => Directory('${tmp.path}/data');
    LocalStore.mediaRoot = () async => Directory('${tmp.path}/media');
  });
  tearDown(() {
    LocalStore.enabled = false;
    tmp.deleteSync(recursive: true);
  });

  Map<String, dynamic> msg(String id, int minute, {bool starred = false}) => {
    'id': id,
    'created_at': DateTime.utc(2026, 11, 13, 9, minute).toIso8601String(),
    'body': 'Message $id',
    'starred': starred,
  };

  test('kept across openings, encrypted, and the key is per store', () async {
    final keys = MemoryKeyStore();
    final s = await LocalStore.open(LocalStore.scopeOf('t-1', 'u-1'), keys);
    expect(s.scope, 't-1_u-1');
    s.write('chats', {'conversations': ['Bayo Rice Mills']});
    s.keepMessages('L1', [msg('a', 1), msg('b', 2)]);
    s.close();
    // On disk, nothing readable: not the words, not SQLite's own header.
    final raw = File('${tmp.path}/data/t-1_u-1.db').readAsBytesSync();
    expect(String.fromCharCodes(raw).contains('Bayo Rice Mills'), isFalse);
    expect(String.fromCharCodes(raw.take(15)), isNot('SQLite format 3'));
    final again = await LocalStore.open('t-1_u-1', keys);
    expect(again.read('chats'), {'conversations': ['Bayo Rice Mills']});
    expect(again.messages('L1').map((m) => m['id']), ['a', 'b']);
    again.close();
    // Its key lost (a phone restored from a backup): made afresh, empty.
    keys.values.remove('store-key:t-1_u-1');
    final fresh = await LocalStore.open('t-1_u-1', keys);
    expect(fresh.read('chats'), isNull);
    fresh.close();
  });

  test('the newest messages kept, starred ones besides; one dropped', () async {
    final s = await LocalStore.open('t_u', MemoryKeyStore());
    s.keepMessages('L1', [msg('old-star', 0, starred: true), for (var i = 1; i <= 5; i++) msg('m$i', i)], keep: 3);
    expect(s.messages('L1').map((m) => m['id']), ['old-star', 'm3', 'm4', 'm5']);
    // A message changed (a reaction, read): replaced, not added.
    s.keepMessages('L1', [{...msg('m5', 5), 'body': 'Changed'}], keep: 3);
    expect(s.messages('L1').last['body'], 'Changed');
    s.dropMessage('L1', 'm4');
    expect(s.messages('L1').map((m) => m['id']), ['old-star', 'm3', 'm5']);
    expect(s.messages('L2'), isEmpty);
    s.close();
  });

  test('the outbox keeps what was written, with its file', () async {
    final s = await LocalStore.open('t_u', MemoryKeyStore());
    s.keepOut((id: 'c1', conv: 'L1', at: DateTime.utc(2026, 11, 13, 9), json: {'text': 'Hello'}, file: null));
    s.keepOut((id: 'c2', conv: 'L2', at: DateTime.utc(2026, 11, 13, 10), json: {'text': ''}, file: Uint8List.fromList([1, 2, 3])));
    expect(s.outbox().map((o) => o.id), ['c1', 'c2']);
    expect(s.outbox('L2').single.file, [1, 2, 3]);
    s.dropOut('c1');
    expect(s.outbox().map((o) => o.id), ['c2']);
    s.close();
  });

  test('files kept to their limit, the least lately seen going first', () async {
    var clock = DateTime.utc(2026, 11, 13);
    DateTime now() => clock = clock.add(const Duration(seconds: 1));
    final s = await LocalStore.open('t_u', MemoryKeyStore(), mediaLimit: 10, now: now);
    await s.keepMedia('/a', List.filled(4, 1));
    await s.keepMedia('/b', List.filled(4, 2));
    expect(await s.media('/a'), List.filled(4, 1), reason: 'seen: now the last to go');
    await s.keepMedia('/c', List.filled(4, 3));
    expect(await s.media('/b'), isNull);
    expect(await s.media('/a'), isNotNull);
    expect(s.mediaBytes, 8);
    // A file the phone cleared: asked for again.
    for (final f in Directory('${tmp.path}/media/t_u').listSync()) {
      f.deleteSync();
    }
    expect(await s.media('/a'), isNull);
    s.close();
  });

  test('a store unopened for 30 days is wiped when next opened', () async {
    final keys = MemoryKeyStore();
    var clock = DateTime.utc(2026, 10, 1);
    final s = await LocalStore.open('t_u', keys, now: () => clock);
    s.write('chats', {'x': 1});
    s.close();
    clock = DateTime.utc(2026, 11, 13);
    final later = await LocalStore.open('t_u', keys, now: () => clock);
    expect(later.read('chats'), isNull);
    later.close();
  });

  test('signing out wipes every store, its key and its files', () async {
    final keys = MemoryKeyStore();
    final a = await LocalStore.open('t1_u', keys);
    await a.keepMedia('/a', [1]);
    final b = await LocalStore.open('t2_u', keys);
    await LocalStore.wipeAll(keys, open: [a, b]);
    expect(Directory('${tmp.path}/data').listSync(), isEmpty);
    expect(Directory('${tmp.path}/media').listSync(), isEmpty);
    expect(keys.values.keys.where((k) => k.startsWith('store-key:')), isEmpty);
    expect(a.read('x'), isNull, reason: 'closed: nothing read');
  });
}
