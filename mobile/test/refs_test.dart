// What "/" tags and the cards of what is tagged, held to the web's own
// tests (web/src/lib/refs.test.ts, mentions.test.ts): G6, G8, G14 of
// docs/spec/mobile-app-m3-fixes.md.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/ref_card.dart';
import 'package:taskiem/chat/tags.dart';
import 'package:taskiem/chat/words.dart';

final _now = DateTime.utc(2026, 11, 9, 12);

ChatRef _ref({String kind = 'invoice', String id = 'i1', String name = 'INV-0001', String? party = "Ada's Kitchen", int? total = 4500000, String? status = 'overdue'}) =>
    ChatRef(kind: kind, id: id, name: name, party: party, when: DateTime.utc(2026, 11, 3, 10), totalKobo: total, status: status);

TagSuggestion _tag(ChatRef r) => refTag(r, _now, date: shortDate, status: refStatusWords);

ChatCard _card({String? status, int? due, int? total, int? paid, String kind = 'invoice'}) =>
    ChatCard(id: 'i1', kind: kind, name: 'INV-0001', when: _now, status: status, dueKobo: due, totalKobo: total, paidKobo: paid);

ExploreCard _found(String name, String state) =>
    ExploreCard(business: 'b-$name', name: name, logo: false, photos: 0, captions: const [], state: state, v: '1', area: 'Yaba');

void main() {
  group('/ references', () {
    test('reads each word with the words after', () {
      expect(tagQuery('/invoice ada', 12), (mark: '/', query: 'ada', start: 0, kind: 'invoice'));
      expect(tagQuery('see /invoices/INV-00', 20), (mark: '/', query: 'INV-00', start: 4, kind: 'invoice'));
      expect(tagQuery('/bill dang', 10)?.kind, 'bill');
      expect(tagQuery('/purchase P-', 12)?.kind, 'purchase');
      expect(tagQuery('/product ', 9), (mark: '/', query: '', start: 0, kind: 'product'));
      expect(tagQuery('/customer bisi ok', 17), (mark: '/', query: 'bisi ok', start: 0, kind: 'customer'));
      expect(tagQuery('/invoice', 8), isNull);
      expect(tagQuery('1/2 bags', 8), isNull);
      expect(tagQuery('/stock rice', 11), isNull);
      expect(tagQuery('/invoice INV-0001 @Da', 21)?.mark, '@');
    });

    test('writes the thing picked as # writes an item', () {
      final q = tagQuery('Did /invoice ad', 15)!;
      expect(insertTag('Did /invoice ad', q, 15, 'INV-0001'), (text: 'Did #INV-0001 ', caret: 14));
    });

    test('says who, when, how much and where it stands on each row', () {
      final s = _tag(_ref());
      expect((s.kind, s.id, s.name, s.badge, s.words), ('invoice', 'i1', 'INV-0001', 'Invoice', "Ada's Kitchen"));
      expect(s.sub, "Ada's Kitchen · 3 Nov · ${naira(4500000)} · overdue");
      expect(_tag(_ref(kind: 'customer', name: 'Bisi Okafor', party: '•••0555', total: null, status: null)).sub, '•••0555 · last wrote 3 Nov');
      expect(shortDate(DateTime.utc(2025, 12, 31, 10), _now), '31 Dec 2025');
    });

    test('gives /invoice invoices and bills, /order orders and purchases', () {
      final src = refSources([
        _tag(_ref()),
        _tag(_ref(kind: 'bill', id: 'b1', name: 'INV-0420')),
        _tag(_ref(kind: 'order', id: 'o1')),
        _tag(_ref(kind: 'purchase', id: 'p1')),
        _tag(_ref(kind: 'customer', id: 'c1')),
      ]);
      expect(src['invoice']!.map((s) => s.id), ['i1', 'b1']);
      expect(src['bill']!.map((s) => s.id), ['b1']);
      expect(src['order']!.map((s) => s.id), ['o1', 'p1']);
      expect(src['purchase']!.map((s) => s.badge), ['Your order']);
      expect(src['customer']!.map((s) => s.id), ['c1']);
    });

    test('offers the business\'s own products, not their sizes', () {
      final got = productRefs(const [
        StockItem(available: true, id: 's1', name: 'Rice', priceKobo: 7800000, unit: 'bag'),
        StockItem(available: true, id: 's2', name: 'Rice 50kg', variantOf: 's1'),
      ]);
      expect(got.map((s) => (s.kind, s.id, s.name, s.sub)), [('item', 'own:s1', 'Rice', 'Yours · ${naira(7800000)} a bag')]);
    });

    test('what a message sends: a document by its id; a customer never', () {
      final picked = [
        _tag(_ref()),
        _tag(_ref(kind: 'customer', id: 'c1', name: 'Bisi Okafor')),
      ];
      final sent = tagRefs('Paid #INV-0001 for #Bisi Okafor', picked);
      expect(sent.map((m) => (m.kind, m.ref)), [('invoice', 'i1')]);
    });
  });

  group('references in messages', () {
    final known = <KnownRef>[
      (kind: 'invoice', id: 'i1', name: 'INV-0001'),
      (kind: 'customer', id: 'c1', name: 'Bisi Okafor'),
      (kind: 'product', id: 's1', name: 'Rice'),
      (kind: 'product', id: 's2', name: 'Rice 50kg'),
    ];

    test('marks each #name known, the longest first, at a word\'s end', () {
      expect(refParts('Send #INV-0001 and #rice 50kg, #Rice.', known).where((p) => p.ref != null).map((p) => (p.text, p.ref!.kind, p.ref!.id)), [
        ('#INV-0001', 'invoice', 'i1'),
        ('#rice 50kg', 'product', 's2'),
        ('#Rice', 'product', 's1'),
      ]);
      expect(refParts('#INV-00012', known).map((p) => (p.text, p.ref)), [('#INV-00012', null)]);
      expect(refParts('no tags', known).map((p) => (p.text, p.ref)), [('no tags', null)]);
    });

    test('cards each invoice, order or customer once, not products', () {
      expect(cardRefs('#INV-0001 for #Bisi Okafor, again #INV-0001 and #Rice', known).map((r) => r.kind), ['invoice', 'customer']);
    });

    test('cards a business conversation\'s invoices and orders, not items', () {
      expect(
        docMentions(const [
          LinkMention(kind: 'invoice', name: 'INV-0001'),
          LinkMention(kind: 'item', name: 'Rice'),
          LinkMention(kind: 'invoice', name: 'INV-0001'),
          LinkMention(kind: 'purchase', name: 'P-ABC123'),
        ]),
        [(kind: 'invoice', name: 'INV-0001'), (kind: 'purchase', name: 'P-ABC123')],
      );
    });
  });

  group('the card', () {
    test('says where it stands', () {
      expect(cardPill(_card(status: 'paid')), (text: 'Paid', tone: CardTone.lime));
      expect(cardPill(_card(status: 'overdue', due: 1)), (text: 'Overdue', tone: CardTone.danger));
      expect(cardPill(_card(status: 'sent', due: 100)), (text: 'Sent', tone: CardTone.warn));
      expect(cardPill(_card(status: 'open', due: 100, kind: 'bill'))?.text, 'To pay');
      expect(cardPill(_card(kind: 'customer')), isNull);
    });

    test('fills the bar by what is paid', () {
      expect(paidShare(_card(total: 4000, paid: 1000)), 0.25);
      expect(paidShare(_card(total: 0)), 0);
      expect(paidShare(_card(total: 100, paid: 500)), 1);
    });
  });

  group('businesses not linked, from Explore (G14)', () {
    test('say where asking stands; linked and blocked are not offered', () {
      expect(unlinkedTag(_found("Kola's Bakery", 'none'))?.sub, 'Not linked · Yaba');
      expect(unlinkedTag(_found("Kola's Bakery", 'requested'))?.sub, 'Not linked · you asked to link');
      expect(unlinkedTag(_found("Kola's Bakery", 'asked_us'))?.sub, 'Not linked · asked you to link');
      expect(unlinkedTag(_found("Kola's Bakery", 'active')), isNull);
      expect(unlinkedTag(_found("Kola's Bakery", 'blocked_by_us')), isNull);
      expect(unlinkedTag(_found("Kola's Bakery", 'none'))?.explore?.name, "Kola's Bakery");
    });

    test('come after the linked ones, once each, three at most', () {
      const linked = [TagSuggestion(kind: 'business', id: 'l1', name: 'Kola Foods')];
      final found = [for (final n in ['Kola Foods', "Kola's Bakery", 'Kolawole Mills', 'Kola Stores', 'Kola Market']) unlinkedTag(_found(n, 'none'))!];
      final g = groupTags('kola', items: const [], services: const [], businesses: linked, taskiem: false, unlinked: found);
      expect(g, hasLength(1));
      expect(g.single.items.map((s) => s.name), ['Kola Foods', "Kola's Bakery", 'Kolawole Mills', 'Kola Stores']);
      expect(g.single.items.first.explore, isNull);
    });
  });
}
