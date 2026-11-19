// The screenshot tests' pictures (test/looks.dart): each kept picture is
// one a test still takes, in both themes.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('every picture in test/looks/ is taken by a test, light and dark', () {
    final names = <String>{};
    for (final f in Directory('test').listSync().whereType<File>().where((f) => f.path.endsWith('_test.dart'))) {
      for (final m in RegExp(r"looks\(t, '([a-z0-9-]+)'\)").allMatches(f.readAsStringSync())) {
        names.add(m.group(1)!);
      }
    }
    expect(names, isNotEmpty);
    final kept = Directory('test/looks').listSync().whereType<File>().map((f) => f.uri.pathSegments.last).toSet();
    for (final k in kept) {
      final m = RegExp(r'^(.+)-(light|dark)\.png$').firstMatch(k);
      expect(m, isNotNull, reason: '$k: a picture is NAME-light.png or NAME-dark.png');
      expect(names, contains(m!.group(1)), reason: '$k: no test takes it any more; delete it');
    }
    for (final n in names) {
      for (final mode in ['light', 'dark']) {
        expect(kept, contains('$n-$mode.png'), reason: 'no picture $n-$mode.png: write it with `flutter test --update-goldens <its test>`, and look at it');
      }
    }
  });
}
