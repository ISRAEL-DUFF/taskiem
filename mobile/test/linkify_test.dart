// The cases shared with the web and the server (web/src/lib/linkify.cases.json):
// one rule for finding links everywhere.

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/linkify.dart';

void main() {
  final file = jsonDecode(File('../web/src/lib/linkify.cases.json').readAsStringSync()) as Map<String, dynamic>;
  for (final c in (file['cases'] as List).cast<Map<String, dynamic>>()) {
    final text = c['text'] as String;
    test(jsonEncode(text), () {
      final want = (c['links'] as List).cast<Map<String, dynamic>>();
      final got = findLinks(text);
      expect([for (final l in got) [l.text, l.url.toString()]], [for (final w in want) [w['text'], Uri.parse(w['url'] as String).toString()]]);
      for (var i = 0; i < got.length; i++) {
        expect(text.substring(got[i].start, got[i].end), got[i].text);
        final shown = want[i]['shown'] as String?;
        if (shown != null) expect(shownLink(got[i].text), shown);
      }
    });
  }

  test('the first link', () {
    expect(firstLink('no links'), isNull);
    expect(firstLink('a example.com b https://x.org').toString(), 'https://example.com');
  });
}
