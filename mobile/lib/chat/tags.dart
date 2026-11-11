// Tags in a business's conversation (web/src/lib/mentions.ts,
// pricelists.ts, stock.ts, links.ts linkedLine): "@" offers things and
// businesses (with chat tools, in groups, Taskiem last), "#" the items of
// the price list with their prices; a pick puts the name in the words and a
// structured reference beside them, sent with the message; a message's
// words are drawn with its tags marked.
//
// Not yet: businesses not linked, found by Explore (their card is the
// Explore screen's); categories (they open the Order and Products tools);
// "/invoice" and the like (with the tools).

import '../api/api.g.dart';
import 'words.dart';

/// mentions.ts Suggestion: a thing or a business to tag.
class TagSuggestion {
  const TagSuggestion({required this.kind, required this.id, required this.name, this.sub, this.category, this.words, this.mention});

  /// "business", "item", "service" or "taskiem".
  final String kind;

  /// The link (a business), the seller's item id, or our service's.
  final String id;
  final String name;

  /// A second line: "₦9,500 a bag", or how we trade.
  final String? sub;

  /// An item's category and words (its description): search reads both.
  final String? category;
  final String? words;

  /// What a message stores of it, priced as now.
  final LinkMention? mention;
}

/// mentions.ts TASKIEM: always offered last with chat tools.
const taskiemTag = TagSuggestion(kind: 'taskiem', id: 'taskiem', name: 'Taskiem', sub: 'Ask Taskiem to do it for you');

/// mentions.ts MentionQuery: the mark, what follows it, and where it is.
typedef TagQuery = ({String mark, String query, int start});

/// mentions.ts mentionQuery: "@" or "#" at the start or after a space or
/// "(", then up to 40 characters on the line, before the caret.
TagQuery? tagQuery(String text, int caret) {
  final before = text.substring(0, caret.clamp(0, text.length));
  for (var i = before.length - 1; i >= 0 && before.length - i <= 41; i--) {
    final ch = before[i];
    if (ch == '\n') return null;
    if ((ch == '@' || ch == '#') && (i == 0 || RegExp(r'[\s(]').hasMatch(before[i - 1]))) {
      return (mark: ch, query: before.substring(i + 1), start: i);
    }
  }
  return null;
}

bool _wordStarts(String text, String q) => text.startsWith(q) || text.split(RegExp(r'\s+')).any((w) => w.startsWith(q));

/// mentions.ts suggest: a word of the name starting with the query first,
/// then the name holding it, then its category or words holding it; six
/// at most.
List<TagSuggestion> suggest(List<TagSuggestion> list, String query) {
  final q = query.trim().toLowerCase();
  final starts = <TagSuggestion>[], holds = <TagSuggestion>[], about = <TagSuggestion>[];
  for (final s in list) {
    final n = s.name.toLowerCase();
    if (q.isEmpty || _wordStarts(n, q)) {
      starts.add(s);
    } else if (n.contains(q)) {
      holds.add(s);
    } else if ((s.category ?? '').toLowerCase().contains(q) || (s.words ?? '').toLowerCase().contains(q)) {
      about.add(s);
    }
  }
  return [...starts, ...holds, ...about].take(6).toList();
}

/// A group of the "@" picker: "Products", "Services", "Businesses", or the
/// Taskiem row (no heading).
typedef TagGroup = ({String label, List<TagSuggestion> items});

/// mentions.ts groupSuggestions: things first, then businesses, then
/// Taskiem; a few of each.
List<TagGroup> groupTags(String query, {required List<TagSuggestion> items, required List<TagSuggestion> services, required List<TagSuggestion> businesses, required bool taskiem}) {
  final out = <TagGroup>[];
  void add(String label, List<TagSuggestion> list, int n) {
    final got = suggest(list, query).take(n).toList();
    if (got.isNotEmpty) out.add((label: label, items: got));
  }

  add('Products', items, 4);
  add('Services', services, 3);
  add('Businesses', businesses, 4);
  if (taskiem) out.add((label: '', items: const [taskiemTag]));
  return out;
}

/// mentions.ts insertMention: the query replaced by the picked name and a
/// space; the text and where the caret goes.
({String text, int caret}) insertTag(String text, TagQuery q, int caret, String name) {
  final head = '${text.substring(0, q.start)}${q.mark}$name ';
  return (text: head + text.substring(caret.clamp(0, text.length)), caret: head.length);
}

/// mentions.ts stillNamed: the picks still named in the words (a name typed
/// over is gone), once each.
List<TagSuggestion> stillNamed(String text, List<TagSuggestion> picked) {
  final lower = text.toLowerCase();
  final out = <TagSuggestion>[];
  for (final p in picked) {
    final name = p.name.toLowerCase();
    final named = lower.contains('@$name') || (p.kind != 'business' && lower.contains('#$name'));
    if (named && !out.any((o) => o.kind == p.kind && o.name == p.name)) out.add(p);
  }
  return out;
}

/// What a message sends of its tags (useMentions refs).
List<SendLinkMessageRequestMentionsItem> tagRefs(String text, List<TagSuggestion> picked) => [
  for (final p in stillNamed(text, picked))
    switch (p.kind) {
      'business' => SendLinkMessageRequestMentionsItem(kind: p.kind, link: p.id),
      'service' => SendLinkMessageRequestMentionsItem(kind: p.kind, serviceId: p.id),
      _ => SendLinkMessageRequestMentionsItem(kind: p.kind, itemId: p.id),
    },
];

/// mentions.ts mentionParts: a message's words in parts, its tags marked.
List<({String text, LinkMention? tag})> tagParts(String text, List<LinkMention>? mentions) {
  if (mentions == null || mentions.isEmpty) return [(text: text, tag: null)];
  final lower = text.toLowerCase();
  final found = <({int at, int len, LinkMention m})>[];
  for (final m in mentions) {
    final tokens = m.kind == 'business' ? ['@${m.name}'] : ['#${m.name}', '@${m.name}'];
    for (final token in tokens) {
      var from = 0;
      while (true) {
        final at = lower.indexOf(token.toLowerCase(), from);
        if (at < 0) break;
        if (!found.any((f) => at < f.at + f.len && f.at < at + token.length)) found.add((at: at, len: token.length, m: m));
        from = at + token.length;
      }
    }
  }
  found.sort((a, b) => a.at.compareTo(b.at));
  final out = <({String text, LinkMention? tag})>[];
  var pos = 0;
  for (final f in found) {
    if (f.at > pos) out.add((text: text.substring(pos, f.at), tag: null));
    out.add((text: text.substring(f.at, f.at + f.len), tag: f.m));
    pos = f.at + f.len;
  }
  if (pos < text.length) out.add((text: text.substring(pos), tag: null));
  return out;
}

/// stock.ts plural: "bag" is "bags", "box" is "boxes", "tray" is "trays".
String unitPlural(String unit) {
  final l = unit.toLowerCase();
  if (RegExp(r'(s|x|ch|sh)$').hasMatch(l)) return l.endsWith('s') && !l.endsWith('ss') ? unit : '${unit}es';
  if (RegExp(r'[^aeiou]y$').hasMatch(l)) return '${unit.substring(0, unit.length - 1)}ies';
  return '${unit}s';
}

/// stock.ts quantity: "12 bags", "1 carton", "4".
String quantity(num n, String? unit) {
  final u = unit?.trim() ?? '';
  final shown = n == n.roundToDouble() ? n.toInt().toString() : n.toString();
  if (u.isEmpty) return shown;
  return '$shown ${n.abs() == 1 ? u : unitPlural(u)}';
}

/// pricelists.ts priceWords: "₦9,500 a bag · at least 2".
String priceWords(PriceItem it) {
  final parts = ['${naira(it.priceKobo)}${(it.unit ?? '').isNotEmpty ? ' a ${it.unit}' : ''}'];
  if (it.minQuantity > 1) parts.add('at least ${it.minQuantity}');
  final breaks = it.breaks ?? const [];
  if (breaks.isNotEmpty) {
    // deals.ts breakWords.
    parts.add([
      for (var i = 0; i < breaks.length; i++)
        'from ${breaks[i].minQuantity}${i == 0 && (it.unit ?? '').isNotEmpty ? ' ${breaks[i].minQuantity == 1 || it.unit!.endsWith('s') ? it.unit : '${it.unit}s'}' : ''} ${naira(breaks[i].priceKobo)}',
    ].join(' · '));
  }
  if (!it.available) parts.add('not available now');
  return parts.join(' · ');
}

/// links.ts linkedLine: "Linked · supplies you".
String linkedLine(String? role) => switch (role) {
  'supplier' => 'Linked · supplies you',
  'customer' => 'Linked · buys from you',
  'both' => 'Linked · supplies you and buys from you',
  _ => 'Linked',
};
