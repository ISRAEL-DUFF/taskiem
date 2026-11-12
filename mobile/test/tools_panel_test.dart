// The tools panel and a tool's sheet, as a person uses them (M5;
// web/src/pages/ChatTools.tsx): "/" and a word filter the panel, a tile
// opens its sheet, the chips and the search narrow its list, the tool is
// then among Recent; in a business's conversation, only what is between us.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  Future<Prefs> taskiem(WidgetTester t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tools');
    final prefs = Prefs(MemoryKeyStore());
    await t.runAsync(prefs.load);
    final chats = ChatsData(f.auth(), now: () => f.at, prefs: prefs)..load();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: TaskiemScreen(data: TaskiemData(chats, follow: false)),
      ),
    );
    await settle(t);
    return prefs;
  }

  Finder tile(String name) => find.bySemanticsLabel(RegExp('^$name, '));

  testWidgets('"/" and a word: the tools it names; Send would send it as a message when none', (t) async {
    await taskiem(t);
    await t.enterText(find.bySemanticsLabel('Message to Taskiem'), '/st');
    await settle(t);
    expect(find.text('Tools'), findsOneWidget);
    expect(tile('Stock'), findsOneWidget);
    expect(tile('Products'), findsNothing);
    await t.enterText(find.bySemanticsLabel('Message to Taskiem'), '/zebra');
    await settle(t);
    expect(find.textContaining('No tool called “zebra”'), findsOneWidget);
    // Not "/" and a word alone: the panel goes.
    await t.enterText(find.bySemanticsLabel('Message to Taskiem'), '/st now');
    await settle(t);
    expect(find.text('Tools'), findsNothing);
  });

  testWidgets('a tile opens its sheet: the chips and the search narrow it; then it is among Recent', (t) async {
    final prefs = await taskiem(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    // Each tile's line and badge from its list.
    expect(tile('Stock'), findsOneWidget);
    expect(find.bySemanticsLabel('Stock, In the store, 5 low'), findsOneWidget);
    await t.tap(tile('Products'));
    await settle(t);
    expect(find.text('Tools'), findsNothing, reason: 'the panel put away');
    expect(find.textContaining(RegExp(r'^Products.*34$'), findRichText: true), findsOneWidget, reason: 'the count by the title');
    expect(find.text('Afang soup and fufu'), findsOneWidget);
    // Low stock: its five.
    await t.tap(find.bySemanticsLabel('Low stock · 5'));
    await settle(t);
    expect(find.textContaining('left'), findsWidgets);
    // A word.
    await t.tap(find.bySemanticsLabel('All'));
    await settle(t);
    await t.enterText(find.bySemanticsLabel('Search products'), 'asun');
    await settle(t);
    expect(find.text('Asun'), findsOneWidget);
    expect(find.text('Afang soup and fufu'), findsNothing);
    await t.enterText(find.bySemanticsLabel('Search products'), 'zzzz');
    await settle(t);
    expect(find.text('Nothing matches'), findsOneWidget);
    // A row opens its form in the sheet; Back is the list again.
    await t.enterText(find.bySemanticsLabel('Search products'), '');
    await settle(t);
    await t.tap(find.text('Asun'));
    await settle(t);
    expect(find.bySemanticsLabel('Save changes'), findsOneWidget);
    expect(find.text('Show on your storefront'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Cancel'));
    await settle(t);
    expect(find.bySemanticsLabel('Search products'), findsOneWidget);
    expect(prefs.recentTools, ['products']);
    // Closed, and the panel opened again: Products among Recent.
    await t.tap(find.bySemanticsLabel('Close'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    expect(find.text('Recent'), findsOneWidget);
  });

  testWidgets("in a business's conversation: Their shop first, then only what is between us", (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: ConversationScreen(data: ConversationData(chats, conv, follow: false)),
      ),
    );
    await settle(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    expect(find.text('With Bayo Rice Mills'), findsOneWidget);
    expect(find.bySemanticsLabel('Their shop, From Bayo Rice Mills'), findsOneWidget);
    for (final k in ['Your orders', 'Bills', 'Invoices']) {
      expect(tile(k), findsOneWidget, reason: k);
    }
    // Ours alone stay in Taskiem's conversation.
    for (final k in ['Products', 'Stock', 'Sell', 'Tables', 'Money']) {
      expect(tile(k), findsNothing, reason: k);
    }
    expect(tile('Statement'), findsOneWidget);
  });

  testWidgets('a panel opened by "/" and a word closes by its button, the word going too; tools not here yet say so', (t) async {
    await taskiem(t);
    await t.enterText(find.bySemanticsLabel('Message to Taskiem'), '/s');
    await settle(t);
    expect(find.text('Tools'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Close tools'));
    await settle(t);
    expect(find.text('Tools'), findsNothing);
    expect(find.text('/s'), findsNothing, reason: 'the "/" and word put away with it');
    // Sell has a sheet of its own (M6): coming, not a list.
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    await t.tap(tile('Sell'));
    await t.pump();
    expect(find.text('This is coming to the app soon.'), findsOneWidget);
    expect(find.text('Search sell'), findsNothing);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  Future<void> shop(WidgetTester t, String whose) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          if (req.url.path.endsWith('/items') && req.url.path.startsWith('/v1/link-chats/')) {
            final j = f.json(req.url.path);
            return http.Response(jsonEncode({...j, 'whose': whose}), 200, headers: {'content-type': 'application/json'});
          }
          return null;
        },
      ),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: ConversationScreen(data: ConversationData(chats, conv, follow: false)),
      ),
    );
    await settle(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Their shop')));
    await settle(t);
  }

  testWidgets('their shop, from a business that sells to us: to order from', (t) async {
    await shop(t, 'theirs');
    expect(find.text('Order from Bayo Rice Mills'), findsOneWidget);
  });

  testWidgets('their shop, from one that does not sell to us: to look around', (t) async {
    await shop(t, 'ours');
    expect(find.text("Bayo Rice Mills's shop"), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^One more ')), findsNothing);
  });

  testWidgets('Make one product: two chosen, then its form', (t) async {
    await taskiem(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    await t.tap(tile('Products'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Make one product with sizes and colours'));
    await t.pump();
    expect(find.bySemanticsLabel('Choose at least two'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Asun'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Afang soup and fufu'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Make one product of 2'));
    await settle(t);
    expect(find.text('These 2 become one product’s sizes and colours. Each keeps its own stock, history and price.'), findsOneWidget);
  });
}
