// Saving after an action (lib/chat/save_after.dart): the name made from a
// title as the server makes it (accents off, as NFD), and a typed address
// cut to a place's line.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/save_after.dart';

void main() {
  test('a name from a title: accents off, lowercase, "-" between, 2 to 32', () {
    const cases = {
      'Bola Ade': 'bola-ade',
      'Ọlá Stores': 'ola-stores',
      'Ìfẹ́': 'ife',
      'İbrahim': 'ibrahim',
      'Café Crème & Co.': 'cafe-creme-co',
      'Ṣọ̀pẹ́ Ńnàmdí': 'sope-nnamdi',
      'Ụ̀ka Ǹgọ Ḿma': 'uka-ngo-mma',
      'Ọlá (decomposed)': 'ola-decomposed',
      '  --Bola__Ade--  ': 'bola-ade',
      'Nnamdi’s 2nd shop': 'nnamdi-s-2nd-shop',
      'Ḿ': '',
      'A': '',
      '•••': '',
      '': '',
    };
    for (final c in cases.entries) {
      expect(savedNameFor(c.key), c.value, reason: c.key);
    }
    // Cut to 32, without a "-" at the end.
    expect(savedNameFor('Abcdefghij Klmnopqrst Uvwxyzabcd Efgh'), 'abcdefghij-klmnopqrst-uvwxyzabcd');
    expect(savedNameFor('Abcdefghij Klmnopqrst Uvwxyzabcd Efgh').length, 32);
  });

  test('a typed address cut to a place’s 200: at the last ", ", else a space', () {
    expect(placeLineFor(' 12 Admiralty Way, Lekki '), '12 Admiralty Way, Lekki');
    final commas = '${'a' * 150}, ${'b' * 40}, ${'c' * 40}';
    expect(placeLineFor(commas), '${'a' * 150}, ${'b' * 40}');
    final spaces = '${'word ' * 50}end';
    expect(placeLineFor(spaces).length, lessThanOrEqualTo(200));
    expect(placeLineFor(spaces).endsWith('word'), isTrue);
    expect(placeLineFor('x' * 250), 'x' * 200);
  });
}
