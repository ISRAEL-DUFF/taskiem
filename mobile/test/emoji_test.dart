// The app's emoji list and its work, held to the web's own tests
// (web/src/lib/emoji.test.ts).

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/emoji.dart';

void main() {
  test('the list: the web\'s groups, every emoji named and in one place', () {
    final seen = <String>{};
    for (final g in emojiGroups) {
      expect(g.items.length, greaterThan(50), reason: g.id);
      for (final i in g.items) {
        expect(i.char, isNotEmpty);
        expect(i.name, isNotEmpty, reason: i.char);
        expect(i.char, isNot(contains(' ')));
        expect(seen.add(i.char), isTrue, reason: '${i.char} twice');
      }
    }
    expect(seen.length, greaterThan(1700));
    expect(emojiGroups.map((g) => g.id), ['smileys', 'people', 'nature', 'food', 'travel', 'activity', 'objects', 'symbols', 'flags']);
    expect(emojiGroups.last.items.any((i) => i.char == '🇳🇬'), isTrue);
  });

  test('search: by name and other words, every word, any case', () {
    expect(searchEmoji('red heart').any((e) => e.char == '❤️'), isTrue);
    expect(searchEmoji('happy').any((e) => e.char == '😀'), isTrue);
    expect(searchEmoji('delivery truck').first.char, '🚚');
    expect(searchEmoji('okada').any((e) => e.char == '🏍️'), isTrue);
    expect(searchEmoji('naija').first.char, '🇳🇬');
    expect(searchEmoji('HEART love'), isNotEmpty);
    expect(searchEmoji('heart zzzz'), isEmpty);
    expect(searchEmoji('   '), isEmpty);
  });

  test('recent: the newest first, no repeats, the most recent kept', () {
    expect(addRecent(['a', 'b', 'c'], 'b'), ['b', 'a', 'c']);
    expect(addRecent(['a', 'b'], 'c', max: 2), ['c', 'a']);
    expect(addRecent([], 'x'), ['x']);
  });

  test('skin tones', () {
    expect(withTone('👍', 3), '👍🏽');
    expect(withTone('🙏', 5), '🙏🏿');
    expect(withTone('✌️', 2), '✌🏼');
    expect(withTone('👩‍🍳', 3), '👩🏽‍🍳');
    expect(withTone('🧑‍⚕️', 2), '🧑🏼‍⚕️');
    expect(insertTone('🤷‍♀️', 4), '🤷🏾‍♀️');
    expect(withTone('😀', 3), '😀');
    expect(withTone('👍', 0), '👍');
    expect(withTone('👍🏽', 0), '👍');
    expect(withTone('✌🏼', 0), '✌️');
    expect(takesTone('👍'), isTrue);
    expect(takesTone('🔥'), isFalse);
    expect(takesTone('👨‍👩‍👧'), isFalse);
    expect(emojiName('👍🏽'), 'thumbs up');
  });
}
