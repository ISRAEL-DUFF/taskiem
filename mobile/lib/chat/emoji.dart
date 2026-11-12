// The emoji panel's list and what is done with it (web/src/lib/emojiList.ts
// and emoji.ts): the web's groups (emoji.g.dart, generated from the web's),
// an emoji's name, the ones that take a skin tone and how a tone is put in,
// the search by words, and the recently used. Unicode's own characters,
// drawn by the phone's emoji font as any text is.

import 'emoji.g.dart';

/// An emoji, its name and more words that find it ("happy" finds 😀).
class EmojiItem {
  const EmojiItem(this.char, this.name, this.words);
  final String char;
  final String name;
  final String words;
}

/// A group of the panel: its id, its name, its icon, its emoji.
class EmojiGroup {
  const EmojiGroup(this.id, this.label, this.icon, this.items);
  final String id;
  final String label;
  final String icon;
  final List<EmojiItem> items;
}

/// Every group, read once when the panel is first opened.
final List<EmojiGroup> emojiGroups = [
  for (final (id, label, icon, text) in emojiSource)
    EmojiGroup(id, label, icon, [
      for (final line in text.split('\n'))
        if (line.contains(' '))
          () {
            final space = line.indexOf(' ');
            final rest = line.substring(space + 1).split('|');
            return EmojiItem(line.substring(0, space), rest[0].trim(), rest.length > 1 ? rest[1].trim() : '');
          }(),
    ]),
];

final RegExp _modifier = RegExp('[\u{1F3FB}-\u{1F3FF}]', unicode: true);
const _fe0f = '️';

/// The emoji without any skin tone modifier.
String stripTone(String char) => char.replaceAll(_modifier, '');

String _key(String c) => stripTone(c).replaceAll(_fe0f, '');

final Map<String, EmojiItem> _written = {
  for (final g in emojiGroups)
    for (final i in g.items) _key(i.char): i,
};
final Set<String> _toneable = toneableSource.split(' ').map(_key).toSet();

/// The name of an emoji, with or without a skin tone.
String? emojiName(String char) => _written[_key(char)]?.name;

/// Whether an emoji takes a skin tone.
bool takesTone(String char) => _toneable.contains(_key(char));

/// The emoji in skin tone [tone] (1 to 5): the modifier after the first
/// character, a selector right after it dropped, as Unicode writes them.
String insertTone(String char, int tone) {
  final cps = stripTone(char).runes.toList();
  if (cps.isEmpty) return char;
  final first = cps.removeAt(0);
  if (cps.isNotEmpty && cps.first == 0xFE0F) cps.removeAt(0);
  return String.fromCharCodes([first, 0x1F3FA + tone, ...cps]);
}

/// The emoji in skin tone [tone] (0 for the default), if it takes one.
String withTone(String char, int tone) {
  final plain = _written[_key(char)]?.char ?? stripTone(char);
  if (!takesTone(char) || tone < 1 || tone > 5) return plain;
  return insertTone(plain, tone);
}

/// The emoji whose name or words hold every word typed.
List<EmojiItem> searchEmoji(String query) {
  final terms = query.toLowerCase().split(RegExp(r'\s+')).where((w) => w.isNotEmpty).toList();
  if (terms.isEmpty) return const [];
  return [
    for (final g in emojiGroups)
      for (final i in g.items)
        if (terms.every('${i.name} ${i.words}'.toLowerCase().contains)) i,
  ];
}

/// The recent emoji with [char] first, no repeats, at most [max].
List<String> addRecent(List<String> list, String char, {int max = 24}) => [char, ...list.where((c) => c != char)].take(max).toList();
