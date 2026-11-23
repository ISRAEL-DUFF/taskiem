// Tags, held to the web's own tests (web/src/lib/mentions.test.ts), and
// as a person uses them: "@" and "#" offered, a pick put in the words, the
// tags still named sent with them, and a tagged business opened.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart' show Composer;
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/tags.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart';

const _bayo = 'ef99249d-9322-40ca-91c2-e24fdd450c15';
const _tunde = '3af349e2-b776-4306-bdb8-10df59687c55';

TagSuggestion _b(String id, String name) => TagSuggestion(kind: 'business', id: id, name: name);
TagSuggestion _i(String id, String name) => TagSuggestion(kind: 'item', id: id, name: name);

void main() {
  group('the words (mentions.test.ts)', () {
    final list = [_b('l1', 'Dangote Stores'), _b('l2', 'Cherry Blocks'), _b('l3', "Ada's Kitchen")];

    test('finds the tag being typed', () {
      expect(tagQuery('Buy 4 from @Dan', 15), (mark: '@', query: 'Dan', start: 11, kind: null));
      expect(tagQuery('#ri', 3), (mark: '#', query: 'ri', start: 0, kind: null));
      expect(tagQuery('ada@example.com', 15), isNull);
      expect(tagQuery('@Dangote\nok', 11), isNull);
      expect(tagQuery('no mark here', 12), isNull);
    });

    test('"/mum" looks for a saved thing; "/invoice" alone is the tool\'s', () {
      expect(tagQuery('Pay /mum', 8), (mark: '/', query: 'mum', start: 4, kind: null));
      expect(tagQuery('/invoice ada', 12), (mark: '/', query: 'ada', start: 0, kind: 'invoice'));
      expect(tagQuery('/invoice', 8), isNull);
      expect(tagQuery('and/or', 6), isNull);
      expect(tagQuery('/mum and', 8), isNull);
      final q = tagQuery('Pay /mu', 7)!;
      expect(insertTag('Pay /mu', q, 7, 'mum'), (text: 'Pay @mum ', caret: 9));
    });

    test('suggests by a word\'s start, then by what a name holds', () {
      expect(suggest(list, 'bl').map((s) => s.id), ['l2']);
      expect(suggest(list, '').map((s) => s.id), ['l1', 'l2', 'l3']);
      expect(suggest(list, 'te').map((s) => s.id), ['l1']);
    });

    test('puts the pick in the words', () {
      const q = (mark: '@', query: 'Dan', start: 11, kind: null);
      expect(insertTag('Buy 4 from @Dan please', q, 15, 'Dangote Stores'), (text: 'Buy 4 from @Dangote Stores  please', caret: 27));
    });

    test('keeps only the tags still named', () {
      final picked = [_b('l1', 'Dangote Stores'), _i('c', 'Cement')];
      expect(stillNamed('Ask @dangote stores for #Cement', picked), hasLength(2));
      expect(stillNamed('Ask Dangote for cement', picked), isEmpty);
      expect(stillNamed('@Cement is out', [_i('c', 'Cement')]), hasLength(1), reason: 'an item written with @ too');
    });

    test('marks a message\'s tags', () {
      final parts = tagParts('400 #Cement, then @Cherry Blocks.', const [
        LinkMention(kind: 'item', name: 'Cement', itemId: 'c', priceKobo: 950000, unit: 'bag'),
        LinkMention(kind: 'business', name: 'Cherry Blocks', link: 'l2'),
      ]);
      expect(parts.map((p) => (p.text, p.tag?.kind ?? '')), [('400 ', ''), ('#Cement', 'item'), (', then ', ''), ('@Cherry Blocks', 'business'), ('.', '')]);
      expect(tagParts('plain', null).single.text, 'plain');
    });

    test('groups things, then businesses, then Taskiem', () {
      final items = [_i('i1', 'Rice, 50kg bag'), _i('i2', 'Rice, 25kg bag'), _i('i3', 'Beans')];
      final businesses = [_b('l1', 'Rivers Foods Ltd')];
      final g = groupTags('ri', items: items, services: const [], businesses: businesses, taskiem: true);
      expect(g.map((x) => x.label), ['Products', 'Businesses', '']);
      expect(g.first.items.map((x) => x.id), ['i1', 'i2']);
      expect(groupTags('zzz', items: items, services: const [], businesses: businesses, taskiem: true).map((x) => x.label), ['']);
      expect(groupTags('', items: items, services: const [], businesses: businesses, taskiem: false).any((x) => x.label.isEmpty), isFalse);
    });

    test('a quantity, and a price', () {
      expect(quantity(6, 'bag'), '6 bags');
      expect(quantity(1, 'carton'), '1 carton');
      expect(quantity(3, 'box'), '3 boxes');
      expect(quantity(2, 'tray'), '2 trays');
      expect(priceWords(const PriceItem(itemId: '1', name: 'Rice', unit: 'bag', priceKobo: 7800000, minQuantity: 2, available: true)), '₦78,000 a bag · at least 2');
    });
  });

  group('in a conversation', () {
    setUpAll(loadFonts);

    Future<(List<Object?>, List<String>)> open(WidgetTester t, {Future<http.Response?> Function(http.Request)? also}) async {
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final f = Fixture('chat-tag');
      final sent = <Object?>[];
      final opened = <String>[];
      final chats = ChatsData(
        f.auth(also: (req) async {
          if (req.method == 'POST' && req.url.path.endsWith('/messages')) {
            final b = jsonDecode(req.body) as Map<String, dynamic>;
            sent.add(b);
            return http.Response(
              jsonEncode({
                'message': {'id': 'sent-1', 'link': _bayo, 'mine': true, 'kind': 'text', 'body': b['text'], 'created_at': '2026-11-11T12:44:00Z'},
              }),
              201,
              headers: {'content-type': 'application/json'},
            );
          }
          if (req.method != 'GET') return http.Response('{}', 200, headers: {'content-type': 'application/json'});
          return also?.call(req);
        }),
        now: () => f.at,
      )..load();
      final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _bayo);
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: ConversationScreen(data: ConversationData(chats, conv, follow: false), onOpenLink: opened.add),
      ));
      for (var i = 0; i < 8; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
        await t.pump();
      }
      return (sent, opened);
    }

    testWidgets('"@": a thing picked and sent as a tag; one typed over is not', (t) async {
      final (sent, _) = await open(t);
      final box = _box;
      await t.enterText(box, 'Send 2 @');
      await t.pump();
      expect(find.bySemanticsLabel('Search what to tag'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('^Taskiem, Ask Taskiem')), findsOneWidget, reason: 'Taskiem last');
      await t.enterText(box, 'Send 2 @ric');
      await t.pump();
      expect(_searchWords(t), 'ric', reason: 'the search box says what was typed');
      await t.tap(find.bySemanticsLabel(RegExp('^Rice, 50kg bag, ')));
      await t.pump();
      expect(t.widget<EditableText>(box).controller.text, 'Send 2 @Rice, 50kg bag ');
      expect(find.bySemanticsLabel('Search what to tag'), findsNothing, reason: 'closed once picked');
      await t.enterText(box, 'Send 2 @Rice, 50kg bag and @mam');
      await t.pump();
      await t.tap(find.bySemanticsLabel(RegExp("^Mama Tunde's Market, Linked")));
      await t.pump();
      expect(t.widget<EditableText>(box).controller.text, "Send 2 @Rice, 50kg bag and @Mama Tunde's Market ");
      // The business's name typed over: only the item is still named.
      await t.enterText(box, 'Send 2 @Rice, 50kg bag and Mama');
      await t.pump();
      await t.tap(find.bySemanticsLabel('Send'));
      await t.pump();
      for (var i = 0; i < 6; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
        await t.pump();
      }
      expect((sent.single as Map)['mentions'], [
        {'item_id': '1', 'kind': 'item'},
      ]);
    });

    testWidgets('"#": the price list, above the box', (t) async {
      await open(t);
      await t.enterText(_box, '#');
      await t.pump();
      expect(find.text('#Rice, 50kg bag'), findsOneWidget);
      expect(find.bySemanticsLabel('Search what to tag'), findsNothing, reason: 'the plain list, not the picker');
      await t.tap(find.text('#Basmati rice, 5kg'));
      await t.pump();
      expect(t.widget<EditableText>(_box).controller.text, '#Basmati rice, 5kg ');
    });

    testWidgets('the picker searches (menus-everywhere.md §7): a box, the kinds as chips, six then Show more, every invoice on the server', (t) async {
      final asked = <Map<String, String>>[];
      Map<String, dynamic> inv(int i) => {
        'kind': 'invoice', 'id': 'i$i', 'name': 'INV-${(200 - i).toString().padLeft(4, '0')}', 'party': 'Bayo Rice Mills',
        'when': DateTime.utc(2026, 11, 11).subtract(Duration(days: i)).toIso8601String(), 'total_kobo': 100000 * (i + 1), 'status': 'sent',
      };
      await open(t, also: (req) async {
        if (req.url.path != '/v1/link-chats/$_bayo/refs') return null;
        final q = req.url.queryParameters;
        if (q.isEmpty) return http.Response(jsonEncode({'refs': [for (var i = 0; i < 10; i++) inv(i)]}), 200);
        asked.add(q);
        // Every one of 150 on the server, 20 at a time; "0060" only the one.
        final all = [for (var i = 0; i < 150; i++) inv(i)].where((r) => (r['name'] as String).contains(q['q'] ?? '')).toList();
        final from = int.tryParse((q['cursor'] ?? 'invoice:0').split(':').last) ?? 0;
        final page = all.skip(from).take(20).toList();
        return http.Response(jsonEncode({'refs': page, 'cursor': from + 20 < all.length ? 'invoice:${from + 20}' : ''}), 200);
      });
      await t.enterText(_box, '/invoice ');
      await t.pump();
      await _wait(t);
      // The box, the kinds there are, Invoices on; six of them, and more.
      expect(find.bySemanticsLabel('Search what to tag'), findsOneWidget);
      expect(find.text('Invoices'), findsWidgets);
      expect(find.text('Products'), findsOneWidget);
      expect(asked.last, {'q': '', 'kind': 'invoice,bill', 'limit': '20'});
      expect(_invoices, findsNWidgets(6));
      await looks(t, 'tag-picker-search');
      await t.ensureVisible(find.text('Show more'));
      await t.pump();
      await t.tap(find.text('Show more'));
      await _wait(t);
      expect(_invoices, findsNWidgets(12));
      // Typed in the search box: the message box says it too, and the server
      // finds one older than any the phone has.
      await t.enterText(find.descendant(of: find.bySemanticsLabel('Search what to tag'), matching: find.byType(EditableText)), '0060');
      await _wait(t);
      expect(t.widget<EditableText>(_box).controller.text, '/invoice 0060');
      expect(asked.last['q'], '0060');
      expect(find.bySemanticsLabel(RegExp(r'^INV-0060, ')), findsOneWidget);
      expect(find.text('Show more'), findsNothing);
      await t.tap(find.bySemanticsLabel(RegExp(r'^INV-0060, ')));
      await t.pump();
      expect(t.widget<EditableText>(_box).controller.text, '#INV-0060 ');
      // Another chip: the price list's items, searched on the phone.
      await t.enterText(_box, '/invoice ');
      await _wait(t);
      await t.tap(find.text('Products'));
      await _wait(t);
      expect(find.bySemanticsLabel(RegExp('^Rice, 50kg bag, ')), findsOneWidget);
      expect(_invoices, findsNothing);
    });

    testWidgets('a tagged business opens its conversation', (t) async {
      final (_, opened) = await open(t);
      final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
      thread.scroll.jumpTo(thread.scroll.position.maxScrollExtent);
      await t.pump();
      // The tag is a chip in the text, pressed as a link.
      await t.tap(find.byWidgetPredicate((w) => w is TagChip && w.words.contains("Mama Tunde's Market")));
      await t.pump();
      expect(opened, [_tunde]);
    });
  });
}

/// The invoices the picker lists, scrolled out of sight too.
Finder get _invoices => find.textContaining(RegExp(r'^INV-\d{4}$'), skipOffstage: false);

/// The message box (the picker's search box is another).
Finder get _box => find.descendant(of: find.byType(Composer), matching: find.byType(EditableText));

String _searchWords(WidgetTester t) =>
    t.widget<EditableText>(find.descendant(of: find.bySemanticsLabel('Search what to tag'), matching: find.byType(EditableText))).controller.text;

Future<void> _wait(WidgetTester t) async {
  // The search asks a moment after the last key: the clock moved past it.
  await t.pump(const Duration(milliseconds: 300));
  for (var i = 0; i < 10; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
  }
}
