// Every web link in the chat is a screen in the app (docs/spec/mobile-app.md
// §9.3; T4 of docs/spec/mobile-app-m3-fixes.md): the addresses the web
// chat's pages and its tools (web/src/lib/tools.ts) link to, each listed in
// web_links.txt with where it is in the app. A new tool or card on the web
// cannot quietly send app users to the web.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

/// The web chat's pages: what the chat is drawn from.
List<File> _chatSources() {
  final pages = Directory('../web/src/pages');
  const named = {
    'Conversation', 'Links', 'TaskiemChat', 'CustomerChat', 'CustomerOrders', 'Mentions', 'RequestChip', 'BubbleShell', //
    'ThreadParts', 'VoiceNote', 'AttachMenu', 'CameraSheet', 'ReadMore', 'Connect', 'Tasks',
  };
  return [
    for (final f in pages.listSync().whereType<File>())
      if (RegExp(r'/Chat[A-Za-z]*\.tsx$').hasMatch(f.path) || named.any((n) => f.path.endsWith('/$n.tsx'))) f,
    File('../web/src/lib/tools.ts'),
  ];
}

/// The addresses in a source: a path in quotes, not the API's or a file's.
Set<String> _addresses(String source) => {
  for (final m in RegExp(r'''["'`](/[a-z][a-z0-9/_-]*)''').allMatches(source))
    if (!m[1]!.startsWith('/v1') && !m[1]!.startsWith('/brand')) m[1]!,
};

void main() {
  final listed = <String, String>{};
  for (final line in File('web_links.txt').readAsLinesSync()) {
    final l = line.trim();
    if (l.isEmpty || l.startsWith('#')) continue;
    final parts = l.split('|').map((s) => s.trim()).toList();
    listed[parts[0]] = parts[1];
  }

  test('every address the web chat links to says where it is in the app', () {
    final found = <String, Set<String>>{};
    for (final f in _chatSources()) {
      for (final a in _addresses(f.readAsStringSync())) {
        found.putIfAbsent(a, () => {}).add(f.path.split('/').last);
      }
    }
    expect(found, isNotEmpty, reason: 'the web chat was read');
    final missing = [for (final e in found.entries) if (!listed.containsKey(e.key)) '${e.key} (${e.value.join(', ')})'];
    expect(missing, isEmpty, reason: 'add each to web_links.txt: a screen in the app, the milestone that brings one, or §3.5');
    final gone = [for (final a in listed.keys) if (!found.containsKey(a)) a];
    expect(gone, isEmpty, reason: 'the web no longer links to these: take them out of web_links.txt');
  });

  test('each is a screen here, a milestone, the browser, or not a page', () {
    for (final e in listed.entries) {
      final where = e.value;
      if (where.startsWith('app ')) {
        expect(File(where.substring(4)).existsSync(), isTrue, reason: '${e.key}: ${where.substring(4)} is not in the app');
      } else {
        expect(where, matches(RegExp(r'^(M[4-8]|browser|not a page)$')), reason: '${e.key}: "$where"');
      }
    }
  });
}
