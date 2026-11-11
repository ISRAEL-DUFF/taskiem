import 'dart:convert';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/theme/theme.dart';

/// Loads the app's own fonts, so a test draws the web's Inter, not the test
/// font.
Future<void> loadFonts() async {
  const files = {
    'Inter': 'Inter-latin.ttf',
    'InterLatinExt': 'Inter-latin-ext.ttf',
    'InterVietnamese': 'Inter-vietnamese.ttf',
    'InterGreek': 'Inter-greek.ttf',
    'InterGreekExt': 'Inter-greek-ext.ttf',
    'InterCyrillic': 'Inter-cyrillic.ttf',
    'InterCyrillicExt': 'Inter-cyrillic-ext.ttf',
  };
  assert(files.keys.skip(1).toList().toString() == tkFallback.toString());
  for (final e in files.entries) {
    final loader = FontLoader(e.key)
      ..addFont(Future.value(ByteData.sublistView(File('assets/fonts/${e.value}').readAsBytesSync())));
    await loader.load();
  }
}

/// A parity fixture (`test/parity/fixtures/NAME`, recorded from the demo
/// world by tool/capture.mjs): its answers by path, and when they were taken.
class Fixture {
  Fixture(this.name) {
    final j = jsonDecode(File('$dir/answers.json').readAsStringSync()) as Map<String, dynamic>;
    at = DateTime.parse(j['at'] as String);
    answers = (j['answers'] as Map<String, dynamic>).cast<String, Map<String, dynamic>>();
  }

  final String name;
  late final DateTime at;
  late final Map<String, Map<String, dynamic>> answers;

  String get dir => 'test/parity/fixtures/$name';

  /// The recorded answer for a request, as the web half finds it.
  Map<String, dynamic>? answerFor(Uri u) => answers[u.path + (u.query.isEmpty ? '' : '?${u.query}').replaceAll(RegExp(r'after=[^&]*'), 'after=*')];

  /// A stand-in server answering from the fixture; anything else is 404,
  /// unless [also] answers it first (a test's sends).
  http.Client client({Future<http.Response?> Function(http.Request req)? also}) => MockClient((req) async {
        final own = await also?.call(req);
        if (own != null) return own;
        // What the screen sends (a read mark) is taken, as the web half
        // takes it.
        if (req.method != 'GET') return http.Response('{}', 200, headers: {'content-type': 'application/json'});
        final a = answerFor(req.url);
        if (a == null) return http.Response(jsonEncode({'error': 'not in the fixture'}), 404);
        return http.Response.bytes(File('$dir/${a['file']}').readAsBytesSync(), a['status'] as int, headers: {'content-type': a['type'] as String});
      });

  /// A recorded answer's JSON.
  Map<String, dynamic> json(String path) => jsonDecode(File('$dir/${answers[path]!['file']}').readAsStringSync()) as Map<String, dynamic>;

  /// Signed in as the fixture's person, who-am-I read from it.
  Auth auth({Future<http.Response?> Function(http.Request req)? also}) {
    final a = Auth(
      transport: Transport(base: 'http://localhost', token: 'fixture', client: client(also: also)),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    final me = answers['/v1/me'];
    if (me != null) a.me = Me.fromJson(jsonDecode(File('$dir/${me['file']}').readAsStringSync()) as Map<String, dynamic>);
    a.state = AuthState.signedIn;
    return a;
  }
}
