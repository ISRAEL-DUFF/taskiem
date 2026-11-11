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
}
