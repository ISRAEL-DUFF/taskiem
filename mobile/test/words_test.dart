import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/words.dart';

// The chat's words, held to the web's (web/src/lib/linkchat.ts, links.ts,
// time.ts, tone.ts, ChatAvatar.tsx): where an answer is not plain, it was
// worked out by running the web's own function.

LinkMessage msg({required bool mine, String kind = 'text', String? body, LinkDocCard? card, LinkMessageAttachment? attachment}) =>
    LinkMessage(id: 'm', link: 'l', kind: kind, mine: mine, body: body, card: card, attachment: attachment, createdAt: DateTime.utc(2026, 11, 11, 12, 13));

void main() {
  final now = DateTime.utc(2026, 11, 11, 12, 15); // a Wednesday

  test('times as the web says them', () {
    expect(listTime(DateTime.utc(2026, 11, 11, 9, 5), now), '09:05');
    expect(listTime(DateTime.utc(2026, 11, 10, 18), now), 'Yesterday');
    expect(listTime(DateTime.utc(2026, 11, 8, 18), now), 'Sunday');
    expect(listTime(DateTime.utc(2026, 10, 30, 18), now), '30 October 2026');
    expect(whenWords(DateTime.utc(2026, 11, 11, 12, 13), now), 'Today, 12:13');
    expect(whenWords(DateTime.utc(2026, 11, 9, 8), now), 'Mon 9 Nov, 08:00');
    expect(whenWords(DateTime.utc(2025, 12, 31, 23, 59), now), '31 Dec 2025, 23:59');
    expect(whenWords(DateTime.utc(2026, 10, 1, 7), now), '1 Oct, 07:00');
  });

  test('a card in words, from either side', () {
    const bill = LinkDocCard(doc: 'bill', event: 'bill', reference: 'INV-0001');
    expect(previewLine(msg(mine: false, kind: 'card', card: bill), 'Bayo Rice Mills'), 'Bayo Rice Mills sent invoice INV-0001');
    expect(previewLine(msg(mine: true, kind: 'card', card: bill), 'Bayo'), 'You sent invoice INV-0001');
    const order = LinkDocCard(doc: 'order', event: 'something_new', reference: 'P-1');
    expect(cardLine(order, true, 'x'), 'You something new order P-1');
    const delivery = LinkDocCard(doc: 'delivery', event: 'picked_up', reference: 'D-1');
    expect(cardLine(delivery, false, 'CoolBox'), 'CoolBox picked up D-1');
  });

  test('previews: ours lose "You: " in the list, files say what they are, diary lines are ticked', () {
    expect(previewLine(msg(mine: true, body: 'Hello'), 'x'), 'You: Hello');
    expect(listPreview(msg(mine: true, body: 'Hello'), 'x'), 'Hello');
    expect(listPreview(msg(mine: false, body: 'Hi'), 'x'), 'Hi');
    expect(previewLine(msg(mine: false, body: 'look', attachment: const LinkMessageAttachment(kind: 'photo', size: 1, type: 'image/jpeg')), 'x'), 'Photo: look');
    expect(previewLine(msg(mine: false, attachment: const LinkMessageAttachment(kind: 'voice', size: 1, type: 'audio/ogg')), 'x'), 'Voice note');
    expect(previewLine(msg(mine: false, attachment: const LinkMessageAttachment(kind: 'file', name: 'price list.pdf', size: 1, type: 'application/pdf')), 'x'), 'price list.pdf');
    expect(previewLine(msg(mine: false, kind: 'diary', body: 'Price of Rice changed'), 'x'), '✓ Price of Rice changed');
  });

  test('links: how we trade, and a state in words', () {
    expect(roleLine('supplier'), 'They supply you');
    expect(roleLine('customer'), 'They buy from you');
    expect(roleLine('both'), 'They supply you and buy from you');
    expect(stateWords('asked_us')!.label, 'Asks to link');
    expect(stateWords('asked_us')!.tone, Tone.warn);
    expect(stateWords('whatever'), isNull);
  });

  test('avatars: initials and tone, as the web works them out', () {
    expect(initialsOf('Bayo Rice Mills'), 'BR');
    expect(initialsOf("Kola's Bakery"), 'KB');
    expect(initialsOf('  & Co'), 'C');
    expect(initialsOf('Ọlá Foods'), 'ỌF');
    expect(initialsOf('123 Stores'), '1S');
    expect(toneOf('Bayo Rice Mills'), 'yellow');
    expect(toneOf("Kola's Bakery"), 'yellow');
    expect(toneOf("Ada's Kitchen"), 'blue');
    expect(toneOf('Ẹ̀kọ́ Stores'), 'peach');
    expect(toneOf('Zuri Atelier'), 'blue');
    expect(toneOf("Mama Tunde's Market"), 'peach');
  });

  test('money, as storefront.ts formatPrice', () {
    expect(naira(82000000), '₦820,000');
    expect(naira(125050), '₦1,250.50');
    expect(naira(5), '₦0.05');
    expect(naira(-150000), '-₦1,500');
  });

  test('cards: pill, progress, kicker and second line, as cardframe.ts', () {
    final now = DateTime.utc(2026, 11, 11, 12);
    LinkDocCard c(String doc, String event, {int? amount, int? was, String? note, DateTime? until}) => LinkDocCard.fromJson({
          'doc': doc,
          'event': event,
          'reference': 'P-9XTV54',
          'amount_kobo': ?amount,
          'was_kobo': ?was,
          'note': ?note,
          if (until != null) 'deal': {'steps': <Object>[], 'until': until.toIso8601String()},
        });
    expect(cardStatus(c('purchase', 'accept'), now), (word: 'Accepted', tone: CardTone.lime, step: 1));
    expect(cardStatus(c('order', 'ordered'), now), (word: 'New', tone: CardTone.lime, step: 0));
    expect(cardStatus(c('bill', 'bill'), now), (word: 'To pay', tone: CardTone.lime, step: null));
    expect(cardStatus(c('delivery', 'arrived'), now).word, 'At the door');
    expect(cardStatus(c('order', 'offered', until: now.subtract(const Duration(minutes: 1))), now).word, 'Lapsed');
    expect(cardStatus(c('order', 'offered', until: now.add(const Duration(minutes: 1))), now).word, 'Offer');
    expect(cardStatus(c('purchase', 'change', was: 100), now), (word: 'Counter', tone: CardTone.warn, step: 0));
    expect(cardStatus(c('invoice', 'some_thing'), now), (word: 'some thing', tone: CardTone.plain, step: null));
    expect(cardKicker(c('purchase', 'accept')), 'Order · P-9XTV54');
    expect(cardKicker(c('bill', 'bill')), 'Invoice · P-9XTV54');
    expect(cardKicker(c('dispatch', 'delivered')), 'Delivery · P-9XTV54');
    expect(stepsFor(c('delivery', 'offered')).length, 5);
    expect(cardSub(c('order', 'change', amount: 7000000, was: 8000000, note: 'less rice')), '₦70,000 · was ₦80,000 · “less rice”');
    expect(isDiary(c('order', 'accept'), true), isTrue);
    expect(isDiary(c('order', 'ordered'), true), isFalse);
    expect(isDiary(c('order', 'accept'), false), isFalse);
    expect(breakAfterHyphens('accepted order P-9XTV54'), 'accepted order P-\u200B9XTV54');
    expect(breakAfterHyphens('a - b'), 'a - b');
  });

  test('long messages fold as readmore.ts folds them', () {
    expect(reveal('short', 0), (shown: 'short', folded: false));
    final long = List.generate(200, (i) => 'word$i').join(' ');
    final r = reveal(long, 0);
    expect(r.folded, isTrue);
    expect(r.shown.length, lessThanOrEqualTo(600));
    expect(long.startsWith(r.shown), isTrue);
    expect(r.shown.endsWith(' '), isFalse);
    expect(reveal(long, 1).shown, long, reason: 'a tap shows 1,200 more');
    final lines = List.generate(30, (i) => 'line $i').join('\n');
    expect(reveal(lines, 0).shown.split('\n').length, 10);
    expect(reveal('${'a' * 590} ${'b' * 100}', 0).folded, isFalse, reason: 'what is left is too short to fold');
  });

  test('Taskiem: bold, change, bars', () {
    expect(boldParts('Sales *today*: ₦5,000'), [(text: 'Sales ', bold: false), (text: 'today', bold: true), (text: ': ₦5,000', bold: false)]);
    expect(boldParts('*a* and *b*').where((p) => p.bold).map((p) => p.text), ['a', 'b']);
    expect(boldParts('no * marks'), [(text: 'no * marks', bold: false)]);
    expect(changeWords(17, 'the week before'), 'Up 17% on the week before');
    expect(changeWords(-5, 'yesterday'), 'Down 5% on yesterday');
    expect(changeWords(0, 'yesterday'), 'The same as yesterday');
    expect(changeWords(null, 'x'), isNull);
    expect(barHeights([0, 0]), [0.04, 0.04]);
    expect(barHeights([50, 100, 1]), [0.5, 1.0, 0.04]);
  });
}
