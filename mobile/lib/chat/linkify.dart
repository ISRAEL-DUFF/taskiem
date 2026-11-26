// The web addresses in a chat message's words
// (docs/spec/chat-previews-and-editing.md §2.2). One rule with the web
// (web/src/lib/linkify.ts) and the server (engine/linkify), tested against
// the same cases (web/src/lib/linkify.cases.json).
//
// What counts: http:// and https:// addresses, and bare ones such as
// jumia.com.ng/phones or www.example.com, taken as https. Nothing else:
// no other scheme, no email address, no address with a user name in it.

/// One web address found in words.
typedef FoundLink = ({int start, int end, String text, Uri url});

// No look-behind, as Go's expressions have none: what comes before and
// after a candidate is checked by hand, the same in every language.
final _candidate = RegExp(
  r'https?://[^\s<>"]+|(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,24}(?::[0-9]{1,5})?(?:[/?#][^\s<>"]*)?',
  caseSensitive: false,
);

const _bareTLDs = {
  'com', 'net', 'org', 'ng', 'io', 'co', 'app', 'dev', 'ai', 'info', 'biz', 'me', 'shop', 'store', 'online', 'site', 'xyz', 'tech', //
  'africa', 'edu', 'gov', 'uk', 'us', 'ca', 'za', 'ke', 'gh', 'de', 'fr', 'ly', 'gl', 'tv', 'link', 'page', 'blog', 'news',
};

const _trailing = '.,;:!?)"\'';

final _word = RegExp(r'^[A-Za-z0-9_]$');
final _hostChars = RegExp(r'^[a-z0-9.-]+$');

bool _validHost(String h) => h.isNotEmpty && !h.startsWith('.') && !h.endsWith('.') && !h.contains('..') && _hostChars.hasMatch(h);

int _count(String s, String c) => s.split(c).length - 1;

/// Every web address in [text], in order.
List<FoundLink> findLinks(String text) {
  final out = <FoundLink>[];
  for (final m in _candidate.allMatches(text)) {
    final start = m.start, end = m.end;
    if (start > 0) {
      final prev = text[start - 1];
      if (_word.hasMatch(prev) || '@._/-:'.contains(prev)) continue;
    }
    if (end < text.length) {
      final next = text[end];
      if (next == '@' || _word.hasMatch(next)) continue;
    }
    var raw = m[0]!;
    while (raw.isNotEmpty && _trailing.contains(raw[raw.length - 1])) {
      if (raw.endsWith(')') && _count(raw, '(') >= _count(raw, ')')) break;
      raw = raw.substring(0, raw.length - 1);
    }
    final lower = raw.toLowerCase();
    final scheme = lower.startsWith('http://') || lower.startsWith('https://');
    var host = scheme ? raw.substring(raw.indexOf('://') + 3) : raw;
    final cut = host.indexOf(RegExp(r'[/?#]'));
    if (cut >= 0) host = host.substring(0, cut);
    if (host.isEmpty || host.contains('@')) continue;
    var name = host.toLowerCase();
    final colon = name.lastIndexOf(':');
    if (colon >= 0) name = name.substring(0, colon);
    if (!scheme) {
      if (!name.startsWith('www.') && !_bareTLDs.contains(name.substring(name.lastIndexOf('.') + 1))) continue;
    } else if (!_validHost(name)) {
      continue;
    }
    final i = raw.indexOf('://');
    final url = Uri.tryParse(scheme ? lower.substring(0, i) + raw.substring(i) : 'https://$raw');
    if (url == null) continue;
    out.add((start: start, end: start + raw.length, text: raw, url: url));
  }
  return out;
}

/// The first web address in [text], or null.
Uri? firstLink(String text) {
  final l = findLinks(text);
  return l.isEmpty ? null : l.first.url;
}

/// How a long address is shown, shortened in the middle
/// (jumia.com.ng/…/phones); it opens in full. Counted in characters.
String shownLink(String text) {
  const most = 40;
  int len(String s) => s.runes.length;
  String take(String s, int n) => String.fromCharCodes(s.runes.take(n));
  if (len(text) <= most) return text;
  var rest = text;
  final i = rest.indexOf('://');
  if (i >= 0) rest = rest.substring(i + 3);
  var host = rest, path = '';
  final slash = rest.indexOf('/');
  if (slash >= 0) {
    host = rest.substring(0, slash);
    path = rest.substring(slash + 1);
  }
  final segs = path.split('/').where((s) => s.isNotEmpty).toList();
  if (segs.isNotEmpty) {
    var last = segs.last;
    if (len(last) > 20) last = '${take(last, 19)}…';
    if (len(host) <= 30) return segs.length == 1 ? '$host/$last' : '$host/…/$last';
  }
  final r = rest.runes.toList();
  if (r.length <= most) return rest;
  return '${String.fromCharCodes(r.take(24))}…${String.fromCharCodes(r.skip(r.length - 15))}';
}
