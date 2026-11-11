// The chat's words, as the web's say them: the list's previews and times,
// a link request's line, a link's state, and an avatar's initials and tone.
// Each is the web function named beside it, ported; test/words_test.dart
// holds them to the web's answers. The tables of an event's words are the
// web's own (words.g.dart, generated).

import '../api/api.g.dart';
import 'words.g.dart';

// --- times (web/src/lib/time.ts, web/src/lib/linkchat.ts) ---

const _days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const _longDays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const _longMonths = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

String _pad(int n) => n.toString().padLeft(2, '0');

int _daysBetween(DateTime d, DateTime now) {
  final a = DateTime(d.year, d.month, d.day), b = DateTime(now.year, now.month, now.day);
  return (b.difference(a).inHours / 24).round();
}

/// time.ts dayWords: "Today", "Yesterday", "Tue 14 Oct", "14 Oct", "14 Oct 2025".
String dayWords(DateTime d, DateTime now) {
  d = d.toLocal();
  now = now.toLocal();
  final days = _daysBetween(d, now);
  if (days == 0) return 'Today';
  if (days == 1) return 'Yesterday';
  final date = '${d.day} ${_months[d.month - 1]}';
  if (d.year != now.year) return '$date ${d.year}';
  if (days > 1 && days < 7) return '${_days[d.weekday % 7]} $date';
  return date;
}

/// time.ts whenWords: "Today, 09:41".
String whenWords(DateTime d, DateTime now) {
  final l = d.toLocal();
  return '${dayWords(d, now)}, ${_pad(l.hour)}:${_pad(l.minute)}';
}

/// linkchat.ts clock: a message's time, 09:41.
String clock(DateTime d) {
  final l = d.toLocal();
  return '${_pad(l.hour)}:${_pad(l.minute)}';
}

/// linkchat.ts dayLabel: the stream's day divider.
String dayLabel(DateTime d, DateTime now) {
  final l = d.toLocal();
  final diff = _daysBetween(l, now.toLocal());
  if (diff == 0) return 'Today';
  if (diff == 1) return 'Yesterday';
  if (diff < 7) return _longDays[l.weekday % 7];
  return '${l.day} ${_longMonths[l.month - 1]} ${l.year}';
}

/// linkchat.ts listTime: today's clock, else the day.
String listTime(DateTime d, DateTime now) {
  final label = dayLabel(d, now);
  return label == 'Today' ? clock(d) : label;
}

// --- messages (web/src/lib/linkchat.ts) ---

bool isDelivery(LinkDocCard c) => c.doc == 'delivery' || c.doc == 'dispatch';

/// linkchat.ts cardLine: "You sent order P-7QX4KD", "Dangote Stores sent invoice INV-0042".
String cardLine(LinkDocCard card, bool mine, String other) {
  final who = mine ? 'You' : other;
  if (isDelivery(card)) {
    final verb = deliveryVerbs[card.event] ?? card.event.replaceAll('_', ' ');
    return '$who $verb ${card.reference}';
  }
  final verb = cardVerbs[card.event] ??
      '${card.event.replaceAll('_', ' ')} ${card.doc == 'invoice' || card.doc == 'bill' ? 'invoice' : 'order'}';
  return '$who $verb ${card.reference}';
}

/// linkchat.ts attachmentLabel.
String attachmentLabel(LinkMessageAttachment a) {
  if (a.kind == 'photo') return 'Photo';
  if (a.kind == 'voice') return 'Voice note';
  return (a.name ?? '').isEmpty ? 'File' : a.name!;
}

/// linkchat.ts previewLine: a message as one line.
String previewLine(LinkMessage m, String other) {
  final card = m.card;
  if (m.kind == 'card' && card != null) return cardLine(card, m.mine, other);
  if (m.kind == 'diary') return '✓ ${m.body ?? ''}';
  final a = m.attachment;
  final words = a != null ? [attachmentLabel(a), m.body ?? ''].where((s) => s.isNotEmpty).join(': ') : (m.body ?? '');
  return (m.mine ? 'You: ' : '') + words;
}

/// The list's preview of a conversation's last message (Links.tsx): ours
/// lose their "You: ", as the ticks before them say it.
String listPreview(LinkMessage m, String other) {
  final line = previewLine(m, other);
  return m.mine && m.kind != 'card' && line.startsWith('You: ') ? line.substring(5) : line;
}

// --- links (web/src/lib/links.ts) ---

/// links.ts roleLine.
String roleLine(String role) => switch (role) {
      'supplier' => 'They supply you',
      'customer' => 'They buy from you',
      _ => 'They supply you and buy from you',
    };

enum Tone { ok, warn, info, muted }

/// links.ts stateWords: a link's state in a word or two, with its tone.
({String label, Tone tone})? stateWords(String state) => switch (state) {
      'active' => (label: 'Linked', tone: Tone.ok),
      'asked_us' => (label: 'Asks to link', tone: Tone.warn),
      'requested' => (label: 'Asked', tone: Tone.info),
      'sent' => (label: 'Request sent', tone: Tone.info),
      'not_accepted' => (label: 'Not accepted', tone: Tone.muted),
      'blocked' => (label: 'Blocked', tone: Tone.muted),
      'ended' => (label: 'Link ended', tone: Tone.muted),
      _ => null,
    };

// --- avatars (web/src/pages/ChatAvatar.tsx, web/src/lib/tone.ts) ---

final _startsWithLetter = RegExp(r'^[\p{L}\p{N}]', unicode: true);

/// ChatAvatar.tsx initialsOf: "Bayo Rice Mills" is "BR".
String initialsOf(String name) {
  final words = name.split(RegExp(r'\s+')).where((w) => _startsWithLetter.hasMatch(w)).toList();
  final s = words.take(2).map((w) => String.fromCharCode(w.runes.first).toUpperCase()).join();
  if (s.isNotEmpty) return s;
  return name.isEmpty ? '' : String.fromCharCode(name.runes.first).toUpperCase();
}

/// tone.ts TONES.
const tones = ['pine', 'lime', 'peach', 'yellow', 'green', 'lavender', 'blue', 'sand'];

/// tone.ts toneOf: a small hash of the name's letters over the tones.
String toneOf(String name) {
  var h = 0;
  for (final ch in name.trim().toLowerCase().runes) {
    h = (h * 31 + ch) & 0xFFFFFFFF;
  }
  return tones[h % tones.length];
}

// --- money (web/src/lib/storefront.ts formatPrice, invoices.ts naira) ---

/// "₦820,000", "₦1,250.50".
String naira(int kobo) {
  final neg = kobo < 0;
  final abs = kobo.abs();
  final whole = (abs ~/ 100).toString().replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
  final frac = abs % 100;
  return '${neg ? '-' : ''}₦$whole${frac != 0 ? '.${frac.toString().padLeft(2, '0')}' : ''}';
}

// --- cards (web/src/lib/cardframe.ts, linkchat.ts) ---

enum CardTone { lime, warn, danger, plain }

typedef CardStatus = ({String word, CardTone tone, int? step});

const orderSteps = ['Placed', 'Accepted', 'On the way', 'Delivered'];
const deliverySteps = ['Offered', 'Accepted', 'Picked up', 'Arrived', 'Delivered'];

List<String> stepsFor(LinkDocCard c) => isDelivery(c) ? deliverySteps : orderSteps;

const Map<String, CardStatus> _delivery = {
  'offered': (word: 'Offered', tone: CardTone.warn, step: 0),
  'accepted': (word: 'Accepted', tone: CardTone.lime, step: 1),
  'picked_up': (word: 'Picked up', tone: CardTone.lime, step: 2),
  'arrived': (word: 'At the door', tone: CardTone.lime, step: 3),
  'delivered': (word: 'Delivered', tone: CardTone.lime, step: 4),
  'confirmed': (word: 'Confirmed', tone: CardTone.lime, step: 4),
  'paid': (word: 'Fee paid', tone: CardTone.lime, step: 4),
  'not_paid': (word: 'Fee not paid', tone: CardTone.warn, step: 4),
  'cant': (word: 'Passed', tone: CardTone.danger, step: null),
  'passed': (word: 'Ran out', tone: CardTone.plain, step: null),
  'withdrawn': (word: 'Withdrawn', tone: CardTone.plain, step: null),
  'released': (word: 'Taken back', tone: CardTone.warn, step: null),
};

const Map<String, CardStatus> _order = {
  'ordered': (word: 'New', tone: CardTone.lime, step: 0),
  'offered': (word: 'Offer', tone: CardTone.warn, step: 0),
  'countered': (word: 'Counter', tone: CardTone.warn, step: 0),
  'proposed': (word: 'Draft', tone: CardTone.plain, step: 0),
  'confirmed': (word: 'Placed', tone: CardTone.plain, step: 0),
  'auto_changed': (word: 'Changed', tone: CardTone.warn, step: 0),
  'change': (word: 'Change asked', tone: CardTone.warn, step: 0),
  'accept': (word: 'Accepted', tone: CardTone.lime, step: 1),
  'auto_accepted': (word: 'Accepted', tone: CardTone.lime, step: 1),
  'change_accepted': (word: 'Accepted', tone: CardTone.lime, step: 1),
  'keep': (word: 'Kept', tone: CardTone.lime, step: 1),
  'ready': (word: 'Ready', tone: CardTone.lime, step: 1),
  'dispatched': (word: 'On the way', tone: CardTone.lime, step: 2),
  'delivered': (word: 'Delivered', tone: CardTone.lime, step: 3),
  'received': (word: 'Received', tone: CardTone.lime, step: 3),
  'agree': (word: 'Count agreed', tone: CardTone.lime, step: 3),
  'short': (word: 'Not all came', tone: CardTone.warn, step: 3),
  'decline': (word: 'Declined', tone: CardTone.danger, step: null),
  'cancel': (word: 'Cancelled', tone: CardTone.danger, step: null),
  'cancel_agreed': (word: 'Cancelled', tone: CardTone.danger, step: null),
  'cancelled_by_buyer': (word: 'Cancelled', tone: CardTone.danger, step: null),
  'cancel_asked': (word: 'Cancel asked', tone: CardTone.warn, step: null),
};

const Map<String, CardStatus> _invoice = {
  'bill': (word: 'To pay', tone: CardTone.lime, step: null),
  'declared': (word: 'Paid, they say', tone: CardTone.plain, step: null),
  'queried': (word: 'Queried', tone: CardTone.warn, step: null),
  'resolved': (word: 'Settled', tone: CardTone.lime, step: null),
  'reminder': (word: 'Reminder', tone: CardTone.warn, step: null),
};

/// cardframe.ts cardStatus: the pill and the progress.
CardStatus cardStatus(LinkDocCard c, DateTime now) {
  CardStatus plain() => (word: c.event.replaceAll('_', ' '), tone: CardTone.plain, step: null);
  if (isDelivery(c)) return _delivery[c.event] ?? plain();
  final order = c.doc == 'order' || c.doc == 'purchase';
  final until = c.deal?.until;
  if (order && until != null && const ['offered', 'countered', 'change'].contains(c.event) && !until.isAfter(now)) {
    return (word: 'Lapsed', tone: CardTone.plain, step: null);
  }
  if (order && c.event == 'change' && c.wasKobo != null) return (word: 'Counter', tone: CardTone.warn, step: 0);
  return (order ? _order : _invoice)[c.event] ?? plain();
}

/// cardframe.ts cardKicker: "Order · P-7QX4KD".
String cardKicker(LinkDocCard c) {
  final word = isDelivery(c) ? 'Delivery' : (c.doc == 'order' || c.doc == 'purchase' ? 'Order' : 'Invoice');
  return '$word · ${c.reference}';
}

/// cardframe.ts isDiary: a change we made ourselves that needs nothing more.
bool isDiary(LinkDocCard c, bool mine) => mine && !const ['ordered', 'offered', 'bill', 'proposed'].contains(c.event);

/// linkchat.ts cardSub: the amount, and a word.
String cardSub(LinkDocCard c) => [
      if (c.amountKobo != null) naira(c.amountKobo!),
      if (c.wasKobo != null && c.wasKobo! > (c.amountKobo ?? 0)) 'was ${naira(c.wasKobo!)}',
      if ((c.note ?? '').isNotEmpty) '“${c.note}”',
    ].join(' · ');

/// Chrome breaks a line after a hyphen even before a digit ("P-" then
/// "9XTV54"); Flutter's breaker does not (UAX 14's LB25). A zero-width
/// space after each hyphen between word characters lets it break there too.
String breakAfterHyphens(String s) => s.replaceAllMapped(RegExp(r'(?<=\w)-(?=\w)'), (_) => '-\u200B');
