// What the app keeps on the phone of a business's chats (docs/spec/
// mobile-app-explore-and-offline.md §3): so it opens at once on what was
// there, reads it offline, and asks the server only for what changed.
//
// One store for each business and person (a "scope"), never shared: an
// SQLite database encrypted with a random key kept in the phone's secure
// storage (as the session is), and the photos, voice notes and files in
// the app's own cache folder, at most [LocalStore.mediaLimit] bytes, those
// seen least lately going first. Signing out, or the server ending the
// session, wipes every store; one not opened for 30 days is wiped when
// next seen.

import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:path_provider/path_provider.dart';
import 'package:sqlite3/sqlite3.dart';

import '../api/transport.dart' show PageKeep;
import 'session.dart' show KeyStore;

/// How many messages a conversation keeps (starred ones besides).
const keptMessages = 200;

/// How long a store may go unopened before it is wiped.
const storeLife = Duration(days: 30);

/// A message waiting to be sent: its conversation, what it says, and its
/// file's bytes when it has one.
typedef KeptOut = ({String id, String conv, DateTime at, Map<String, dynamic> json, Uint8List? file});

class LocalStore implements PageKeep {
  LocalStore._(this.scope, this._db, this._mediaDir, {this.mediaLimit = 300 << 20, DateTime Function()? now}) : _now = now ?? DateTime.now;

  /// Whose it is: "tenant_user".
  final String scope;
  final Database _db;
  final Directory _mediaDir;
  final DateTime Function() _now;

  /// The most the photos, voice notes and files kept may take.
  final int mediaLimit;

  bool _closed = false;

  /// The store of whoever is signed in now (set by Auth), for what draws
  /// pictures without the session at hand; null when signed out.
  static LocalStore? current;

  /// Whether this app keeps stores at all: turned on by the app as it
  /// starts (main.dart), and by a test that sets [dataRoot] and
  /// [mediaRoot]; off elsewhere, where the phone's folders cannot be asked.
  static bool enabled = false;

  /// Where the databases go, and the files: the app's own folders, or a
  /// test's.
  static Future<Directory> Function() dataRoot = () async => Directory('${(await getApplicationSupportDirectory()).path}/stores');
  static Future<Directory> Function() mediaRoot = () async => Directory('${(await getApplicationCacheDirectory()).path}/media');

  /// A scope from who is signed in: only letters, digits, dashes and one
  /// underscore, safe as a folder's name.
  static String scopeOf(String tenant, String user) {
    String clean(String s) => s.replaceAll(RegExp(r'[^A-Za-z0-9-]'), '');
    return '${clean(tenant)}_${clean(user)}';
  }

  /// Opens (or makes) the store of [scope], its key from [keys] (made at
  /// random the first time). A store that will not open with its key (the
  /// key lost with a phone's restore, the file damaged) is made afresh:
  /// it is only a copy of what the server has.
  static Future<LocalStore> open(String scope, KeyStore keys, {int mediaLimit = 300 << 20, DateTime Function()? now}) async {
    if (!enabled) throw StateError('No stores here');
    final data = await dataRoot();
    final media = Directory('${(await mediaRoot()).path}/$scope');
    await data.create(recursive: true);
    await media.create(recursive: true);
    final keyName = 'store-key:$scope';
    var key = await keys.read(keyName);
    if (key == null) {
      key = _randomHex(32);
      await keys.write(keyName, key);
    }
    final file = File('${data.path}/$scope.db');
    Database db;
    try {
      db = _openWith(file.path, key);
    } on Object {
      for (final f in [file, File('${file.path}-wal'), File('${file.path}-shm')]) {
        if (f.existsSync()) f.deleteSync();
      }
      db = _openWith(file.path, key);
    }
    final s = LocalStore._(scope, db, media, mediaLimit: mediaLimit, now: now);
    // Unopened for too long: what it kept is out of date and may not be
    // wanted on the phone any more.
    final last = s.read('opened_at');
    final at = last is String ? DateTime.tryParse(last) : null;
    if (at != null && s._now().difference(at) > storeLife) s._clear();
    s.write('opened_at', s._now().toUtc().toIso8601String());
    return s;
  }

  static Database _openWith(String path, String key) {
    final db = sqlite3.open(path);
    try {
      // SQLite3MultipleCiphers: the whole file encrypted with the key.
      db.execute("PRAGMA key = \"x'$key'\"");
      // The key is right only if the file can be read with it.
      db.select('SELECT count(*) FROM sqlite_master');
      db.execute('PRAGMA journal_mode = WAL');
      db.execute('''
        CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS msg (conv TEXT NOT NULL, id TEXT NOT NULL, at TEXT NOT NULL, starred INTEGER NOT NULL DEFAULT 0, j TEXT NOT NULL,
          PRIMARY KEY (conv, id));
        CREATE INDEX IF NOT EXISTS msg_at ON msg (conv, at);
        CREATE TABLE IF NOT EXISTS outbox (id TEXT PRIMARY KEY, conv TEXT NOT NULL, at TEXT NOT NULL, j TEXT NOT NULL, file BLOB);
        CREATE TABLE IF NOT EXISTS media (k TEXT PRIMARY KEY, name TEXT NOT NULL, bytes INTEGER NOT NULL, used INTEGER NOT NULL);
        CREATE TABLE IF NOT EXISTS pages (k TEXT PRIMARY KEY, body TEXT NOT NULL, etag TEXT, used INTEGER NOT NULL);
      ''');
      return db;
    } on Object {
      db.close();
      rethrow;
    }
  }

  static String _randomHex(int n) {
    final r = Random.secure();
    return List.generate(n, (_) => r.nextInt(256).toRadixString(16).padLeft(2, '0')).join();
  }

  // --- values ---

  /// A value kept under [k] (decoded JSON), or null.
  Object? read(String k) {
    if (_closed) return null;
    final rows = _db.select('SELECT v FROM kv WHERE k = ?', [k]);
    if (rows.isEmpty) return null;
    try {
      return jsonDecode(rows.first['v'] as String);
    } on FormatException {
      return null;
    }
  }

  /// Keeps [v] (JSON-encodable) under [k]; null forgets it.
  void write(String k, Object? v) {
    if (_closed) return;
    if (v == null) {
      _db.execute('DELETE FROM kv WHERE k = ?', [k]);
    } else {
      _db.execute('INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v', [k, jsonEncode(v)]);
    }
  }

  // --- messages ---

  /// The messages kept for a conversation, oldest first.
  List<Map<String, dynamic>> messages(String conv) {
    if (_closed) return const [];
    return [
      for (final r in _db.select('SELECT j FROM msg WHERE conv = ? ORDER BY at, id', [conv]))
        (jsonDecode(r['j'] as String) as Map).cast<String, dynamic>(),
    ];
  }

  /// Keeps messages (each with its "id" and "created_at"), replacing those
  /// kept with the same id; then only the newest [keep] stay (starred ones
  /// besides).
  void keepMessages(String conv, Iterable<Map<String, dynamic>> list, {int keep = keptMessages}) {
    if (_closed) return;
    _db.execute('BEGIN');
    try {
      final put = _db.prepare(
        'INSERT INTO msg (conv, id, at, starred, j) VALUES (?, ?, ?, ?, ?) ON CONFLICT (conv, id) DO UPDATE SET at = excluded.at, starred = excluded.starred, j = excluded.j',
      );
      for (final m in list) {
        final at = DateTime.tryParse('${m['created_at']}')?.toUtc().toIso8601String() ?? '';
        put.execute([conv, '${m['id']}', at, m['starred'] == true ? 1 : 0, jsonEncode(m)]);
      }
      put.close();
      _db.execute(
        'DELETE FROM msg WHERE conv = ? AND starred = 0 AND id NOT IN (SELECT id FROM msg WHERE conv = ? ORDER BY at DESC, id DESC LIMIT ?)',
        [conv, conv, keep],
      );
      _db.execute('COMMIT');
    } on Object {
      _db.execute('ROLLBACK');
      rethrow;
    }
  }

  /// Forgets a message (deleted, or no longer ours to see).
  void dropMessage(String conv, String id) {
    if (_closed) return;
    _db.execute('DELETE FROM msg WHERE conv = ? AND id = ?', [conv, id]);
    // Its photo, voice note or file goes with it (lib/chat/attachments.dart
    // ChatFiles.path): a deleted message leaves nothing on the phone.
    dropMedia('/v1/link-chats/${Uri.encodeComponent(conv)}/files/${Uri.encodeComponent(id)}');
  }

  /// Forgets one kept file, by its key.
  void dropMedia(String key) {
    if (_closed) return;
    final rows = _db.select('SELECT name FROM media WHERE k = ?', [key]);
    if (rows.isEmpty) return;
    _db.execute('DELETE FROM media WHERE k = ?', [key]);
    _deleteFile(rows.first['name'] as String);
  }

  /// Forgets a whole conversation's messages.
  void dropConversation(String conv) {
    if (_closed) return;
    _db.execute('DELETE FROM msg WHERE conv = ?', [conv]);
  }

  // --- the outbox ---

  /// Messages written and not yet sent, oldest first; of one conversation
  /// when [conv] is given.
  List<KeptOut> outbox([String? conv]) {
    if (_closed) return const [];
    final rows = conv == null
        ? _db.select('SELECT id, conv, at, j, file FROM outbox ORDER BY at, id')
        : _db.select('SELECT id, conv, at, j, file FROM outbox WHERE conv = ? ORDER BY at, id', [conv]);
    return [
      for (final r in rows)
        (
          id: r['id'] as String,
          conv: r['conv'] as String,
          at: DateTime.parse(r['at'] as String),
          json: (jsonDecode(r['j'] as String) as Map).cast<String, dynamic>(),
          file: r['file'] as Uint8List?,
        ),
    ];
  }

  void keepOut(KeptOut o) {
    if (_closed) return;
    _db.execute(
      'INSERT INTO outbox (id, conv, at, j, file) VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET j = excluded.j',
      [o.id, o.conv, o.at.toUtc().toIso8601String(), jsonEncode(o.json), o.file],
    );
  }

  void dropOut(String id) {
    if (_closed) return;
    _db.execute('DELETE FROM outbox WHERE id = ?', [id]);
  }

  // --- pages (Today, Money, Orders, the tools' lists: K4) ---

  /// How many pages are kept, the least lately read going first.
  static const keptPages = 400;

  /// A page's last answer, and its ETag.
  @override
  ({String body, String? etag})? readPage(String key) {
    if (_closed) return null;
    final rows = _db.select('SELECT body, etag FROM pages WHERE k = ?', ['page:$key']);
    if (rows.isEmpty) return null;
    _db.execute('UPDATE pages SET used = ? WHERE k = ?', [_now().microsecondsSinceEpoch, 'page:$key']);
    return (body: rows.first['body'] as String, etag: rows.first['etag'] as String?);
  }

  @override
  void writePage(String key, String body, String? etag) {
    if (_closed) return;
    _db.execute(
      'INSERT INTO pages (k, body, etag, used) VALUES (?, ?, ?, ?) ON CONFLICT (k) DO UPDATE SET body = excluded.body, etag = excluded.etag, used = excluded.used',
      ['page:$key', body, etag, _now().microsecondsSinceEpoch],
    );
    _db.execute('DELETE FROM pages WHERE k NOT IN (SELECT k FROM pages ORDER BY used DESC LIMIT ?)', [keptPages]);
  }

  // --- photos, voice notes and files ---

  /// The bytes kept for [key] (a file's path on the server, its version in
  /// it), or null; seen now, so it is the last to go.
  Future<Uint8List?> media(String key) async {
    if (_closed) return null;
    final rows = _db.select('SELECT name FROM media WHERE k = ?', [key]);
    if (rows.isEmpty) return null;
    final f = File('${_mediaDir.path}/${rows.first['name']}');
    try {
      final b = await f.readAsBytes();
      if (!_closed) _db.execute('UPDATE media SET used = ? WHERE k = ?', [_now().microsecondsSinceEpoch, key]);
      return b;
    } on FileSystemException {
      // Cleared by the phone (short of space): fetched again.
      if (!_closed) _db.execute('DELETE FROM media WHERE k = ?', [key]);
      return null;
    }
  }

  /// Keeps [bytes] for [key]; the least lately seen go while the kept are
  /// over [mediaLimit] (never the one just kept).
  Future<void> keepMedia(String key, List<int> bytes) async {
    if (_closed || bytes.length > mediaLimit) return;
    final name = _randomHex(12);
    await _mediaDir.create(recursive: true);
    await File('${_mediaDir.path}/$name').writeAsBytes(bytes, flush: true);
    if (_closed) return;
    final was = _db.select('SELECT name FROM media WHERE k = ?', [key]);
    _db.execute(
      'INSERT INTO media (k, name, bytes, used) VALUES (?, ?, ?, ?) ON CONFLICT (k) DO UPDATE SET name = excluded.name, bytes = excluded.bytes, used = excluded.used',
      [key, name, bytes.length, _now().microsecondsSinceEpoch],
    );
    if (was.isNotEmpty) _deleteFile(was.first['name'] as String);
    var total = (_db.select('SELECT COALESCE(sum(bytes), 0) AS n FROM media').first['n'] as int);
    if (total <= mediaLimit) return;
    for (final r in _db.select('SELECT k, name, bytes FROM media WHERE k <> ? ORDER BY used', [key])) {
      if (total <= mediaLimit) break;
      _db.execute('DELETE FROM media WHERE k = ?', [r['k']]);
      _deleteFile(r['name'] as String);
      total -= r['bytes'] as int;
    }
  }

  /// How many bytes of files are kept.
  int get mediaBytes => _closed ? 0 : _db.select('SELECT COALESCE(sum(bytes), 0) AS n FROM media').first['n'] as int;

  void _deleteFile(String name) {
    try {
      File('${_mediaDir.path}/$name').deleteSync();
    } on FileSystemException {
      // Gone already.
    }
  }

  void _clear() {
    _db.execute('DELETE FROM kv; DELETE FROM msg; DELETE FROM outbox; DELETE FROM media; DELETE FROM pages;');
    try {
      if (_mediaDir.existsSync()) {
        for (final f in _mediaDir.listSync()) {
          f.deleteSync(recursive: true);
        }
      }
    } on FileSystemException {
      // Left for the phone to clear.
    }
  }

  void close() {
    if (_closed) return;
    _closed = true;
    _db.close();
  }

  /// Wipes every store on the phone, their keys and their files: signed
  /// out, or the session ended.
  static Future<void> wipeAll(KeyStore keys, {Iterable<LocalStore> open = const []}) async {
    for (final s in open) {
      s.close();
    }
    if (!enabled) return;
    for (final root in [await dataRoot(), await mediaRoot()]) {
      if (!root.existsSync()) continue;
      for (final f in root.listSync()) {
        final name = f.uri.pathSegments.where((p) => p.isNotEmpty).last;
        final scope = name.endsWith('.db') ? name.substring(0, name.length - 3) : (name.contains('.db-') ? null : name);
        if (scope != null && root.path == (await dataRoot()).path) await keys.write('store-key:$scope', null);
        try {
          f.deleteSync(recursive: true);
        } on FileSystemException {
          // Left for the phone to clear.
        }
      }
    }
  }
}

/// The bytes kept on the phone for a picture or file at [path], or null.
Future<Uint8List?> keptMedia(String path) async {
  final s = LocalStore.current;
  if (s == null) return null;
  try {
    return await s.media(path);
  } on Object {
    return null;
  }
}

/// Keeps a picture or file fetched from [path].
Future<void> keepMediaBytes(String path, List<int> bytes) async {
  final s = LocalStore.current;
  if (s == null) return;
  try {
    await s.keepMedia(path, bytes);
  } on Object {
    // Not kept: fetched again next time.
  }
}
