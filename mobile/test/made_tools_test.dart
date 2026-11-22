// Your tools in Taskiem's conversation (made tools M1; docs/tools.md,
// docs/spec/made-tools.md §6–§8): tiles after the built-in ones, "/" finding
// them; the tool's sheet with its form, then its care (answers at once with
// an echo, Yes with the same values, Yes and a passkey); its answer in the
// conversation (a text, a card, a list and every row, a failure); refusals
// in plain words; an owner's "Always ask before it runs".

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'made_tools_stub.dart';
import 'support.dart';

class _Passkeys implements PasskeyPlatform {
  final asked = <Map<String, dynamic>>[];
  @override
  Future<bool> available() async => true;
  @override
  Future<Map<String, dynamic>> create(Map<String, dynamic> publicKey) => throw UnimplementedError();
  @override
  Future<Map<String, dynamic>> get(Map<String, dynamic> publicKey) async {
    asked.add(publicKey);
    return {'id': 'k1', 'rawId': 'k1', 'type': 'public-key', 'response': {'clientDataJSON': 'c', 'authenticatorData': 'd', 'signature': 's'}};
  }
}

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  /// Read again after the runs' gentle wait.
  Future<void> later(WidgetTester t) async {
    await t.pump(const Duration(seconds: 2));
    await settle(t);
  }

  Future<MadeServer> taskiem(WidgetTester t, {bool builtIn = true, TkMode mode = TkMode.light}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tools');
    final server = MadeServer();
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          if (!builtIn && req.url.path == '/v1/settings/chat-tools') {
            return http.Response(jsonEncode({'chat_tools': false}), 200, headers: {'content-type': 'application/json'});
          }
          return server.call(req);
        },
      ),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: mode, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await settle(t);
    return server;
  }

  Finder tile(String name) => find.bySemanticsLabel(RegExp('^$name, '));
  Finder box() => find.bySemanticsLabel('Message to Taskiem');

  Future<void> open(WidgetTester t, String name) async {
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    await t.ensureVisible(tile(name));
    await t.pumpAndSettle();
    await t.tap(tile(name));
    await settle(t);
  }

  Future<void> type(WidgetTester t, String label, String words) async {
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^$label')), matching: find.byType(EditableText)), words);
    await t.pump();
  }

  testWidgets('Your tools: tiles after the built-in ones, and "/" finds them by command or name', (t) async {
    await taskiem(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    expect(find.text('Your tools'), findsOneWidget);
    expect(tile('Stock'), findsOneWidget, reason: 'the built-in ones first');
    expect(find.bySemanticsLabel('Leave left, Days of leave a person has left'), findsOneWidget);
    expect(tile('Log an expense'), findsOneWidget);
    expect(t.getTopLeft(tile('Log an expense')).dy, greaterThan(t.getTopLeft(tile('Stock')).dy));
    await t.tap(find.bySemanticsLabel('Close tools'));
    await settle(t);
    // Its command's start.
    await t.enterText(box(), '/exp');
    await settle(t);
    expect(tile('Log an expense'), findsOneWidget);
    expect(tile('Leave left'), findsNothing);
    // A word of its name.
    await t.enterText(box(), '/tick');
    await settle(t);
    expect(tile('Open tickets'), findsOneWidget);
    expect(tile('Stock'), findsNothing);
    await t.enterText(box(), '/pay-s');
    await settle(t);
    expect(tile('Pay a supplier'), findsOneWidget);
    // Pressed: its sheet, the "/" word gone.
    await t.tap(tile('Pay a supplier'));
    await settle(t);
    expect(find.text('Pay a supplier'), findsWidgets);
    expect(find.text('/pay-supplier'), findsOneWidget);
  });

  testWidgets('with the built-in tools off, the tools button still shows Your tools alone', (t) async {
    await taskiem(t, builtIn: false);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    expect(find.text('Your tools'), findsOneWidget);
    expect(tile('Leave left'), findsOneWidget);
    expect(tile('Stock'), findsNothing);
    expect(tile('Tasks'), findsNothing);
  });

  testWidgets('a tool that only reads: started at once, the echo of its values, "Working on it…", then its answer', (t) async {
    final server = await taskiem(t);
    await open(t, 'Leave left');
    expect(find.bySemanticsLabel('Run it'), findsOneWidget);
    await type(t, 'Name', 'Musa');
    await t.tap(find.bySemanticsLabel('Run it'));
    await settle(t);
    expect(server.sent.map((s) => s.path), ['/v1/tools/leave/start']);
    expect(server.sent.single.body, {
      'input': {'name': 'Musa'},
    });
    // The sheet gone; what was read, as the person's message.
    expect(find.text('/leave'), findsNothing);
    expect(find.textContaining('Name: Musa', findRichText: true), findsOneWidget);
    expect(find.textContaining('Working on it…', findRichText: true), findsOneWidget);
    await snap(t, 'made-working');
    await later(t);
    expect(find.textContaining('Musa has 12 days of leave left', findRichText: true), findsOneWidget);
    expect(find.textContaining('Working on it…', findRichText: true), findsNothing);
    await snap(t, 'made-text');
  });

  testWidgets('a tool that asks Yes: what will run, then Yes sends the same values; its card, and its button fills the form again', (t) async {
    final server = await taskiem(t);
    await open(t, 'Log an expense');
    expect(find.bySemanticsLabel('Next'), findsOneWidget);
    await type(t, 'Amount', '4,500');
    await type(t, 'What for', 'Diesel for the generator');
    await t.tap(find.bySemanticsLabel('Cash'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    final start = server.sent.single;
    expect(start.body['input'], {'amount': 4500, 'what': 'Diesel for the generator', 'paid': 'Cash'});
    expect(find.text('What will run'), findsOneWidget);
    expect(find.text('Diesel for the generator'), findsOneWidget);
    expect(find.text('It adds a row to Expenses.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Yes, run it'));
    await settle(t);
    final confirm = server.sent.last;
    expect(confirm.path, '/v1/tools/expense/confirm');
    expect(confirm.body['confirm'], '0193c000-0000-7000-8000-000000000001');
    expect(confirm.body['input'], start.body['input'], reason: 'the same values');
    expect(confirm.body.containsKey('passkey'), isFalse);
    // Its card: the amount in naira, the state, the lines, the button.
    expect(find.text('₦4,500'), findsOneWidget);
    expect(find.text('Recorded'), findsOneWidget);
    expect(find.text('EXP-0142'), findsOneWidget);
    expect(find.text('Ada Obi'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Log another'));
    await settle(t);
    expect(find.text('/expense'), findsOneWidget);
    expect(find.bySemanticsLabel('Cash'), findsOneWidget);
  });

  testWidgets('a tool that moves money: Yes with the passkey for this tool; its failure in plain words with the run', (t) async {
    final keys = _Passkeys();
    final was = passkeys;
    passkeys = keys;
    addTearDown(() => passkeys = was);
    final server = await taskiem(t);
    await open(t, 'Pay a supplier');
    await type(t, 'Supplier', 'Bayo Rice Mills');
    await type(t, 'Amount', '120000');
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    expect(find.text('It moves money out of the business'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Yes, with passkey'));
    await settle(t);
    expect(keys.asked, hasLength(1));
    final options = server.sent.firstWhere((s) => s.path == '/v1/me/step-up/options');
    expect(options.body, {'operation': 'tool.start', 'target': 'tool:pay-supplier'});
    final confirm = server.sent.last;
    expect(confirm.path, '/v1/tools/pay-supplier/confirm');
    expect(confirm.body['passkey'], isNotNull);
    expect(confirm.body['input'], {'supplier': 'Bayo Rice Mills', 'amount': 120000});
    expect(find.text('Pay a supplier didn’t finish'), findsOneWidget);
    expect(find.text('The bank refused the transfer: the account is closed.'), findsOneWidget);
    expect(find.bySemanticsLabel('See the run of Pay a supplier'), findsOneWidget);
    await snap(t, 'made-failed');
  });

  testWidgets('a list: its first rows in the chat, See all every row; a row\'s button opens the tool it names, filled', (t) async {
    final server = await taskiem(t);
    await open(t, 'Open tickets');
    expect(find.text('What customers are waiting on'), findsOneWidget, reason: 'no inputs: what it does');
    await t.tap(find.bySemanticsLabel('Run it'));
    await settle(t);
    expect(server.sent.single.body, {'input': <String, dynamic>{}});
    expect(find.text('Rice short by two bags'), findsOneWidget);
    expect(find.text('Invoice sent twice'), findsOneWidget);
    expect(find.text('Change of address'), findsNothing, reason: 'five in the chat');
    expect(find.text('7 rows'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('See all of Open tickets'));
    await settle(t);
    expect(find.text('Change of address'), findsOneWidget);
    await t.scrollUntilVisible(find.text('Price on the receipt'), 200, scrollable: find.byType(Scrollable).last);
    expect(find.text('Price on the receipt'), findsOneWidget);
    await t.tap(find.text('Price on the receipt'));
    await settle(t);
    expect(find.text('/expense'), findsOneWidget);
    expect(find.text('Ticket 7'), findsOneWidget);
  });

  testWidgets('a button is pressed by its token: the sheet filled from the press, the start carrying the button, and a once button spent', (t) async {
    final server = await taskiem(t);
    await open(t, 'Open tickets');
    await t.tap(find.bySemanticsLabel('Run it'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('See all of Open tickets'));
    await settle(t);
    await t.tap(find.text('Rice short by two bags'));
    await settle(t);
    expect(server.sent.last.path, '/v1/tools/buttons/tok-row-0', reason: 'pressed by its token');
    // The tool and values the press gave.
    expect(find.text('/expense'), findsOneWidget);
    expect(find.text('Ticket 1'), findsOneWidget);
    await type(t, 'Amount', '4,500');
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    final start = server.sent.last;
    expect(start.path, '/v1/tools/expense/start');
    expect(start.body['button'], 'tok-row-0');
    expect(start.body['input'], {'amount': 4500, 'what': 'Ticket 1'});
    await t.tap(find.bySemanticsLabel('Yes, run it'));
    await settle(t);
    expect(server.sent.last.path, '/v1/tools/expense/confirm', reason: 'the confirm keeps the button on the server');
    // Spent: the row offers it no more.
    await t.ensureVisible(find.bySemanticsLabel('See all of Open tickets').first);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('See all of Open tickets').first);
    await settle(t);
    expect(find.text('Change of address'), findsOneWidget, reason: 'every row');
    final pressed = server.sent.length;
    await t.tap(find.text('Rice short by two bags'));
    await settle(t);
    expect(server.sent.length, pressed);
    expect(find.text('/expense'), findsNothing);
  });

  testWidgets('a once button someone used first says who took it, and greys; one that is gone says so', (t) async {
    final server = await taskiem(t);
    await open(t, 'Open tickets');
    await t.tap(find.bySemanticsLabel('Run it'));
    await settle(t);
    server.taken['tok-row-1'] = 'Already done: taken by Ada Obi at 10:42 on 22 Nov.';
    await t.tap(find.bySemanticsLabel('See all of Open tickets'));
    await settle(t);
    await t.tap(find.text('Wrong size delivered'));
    await t.pump();
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
    expect(find.text('Already done: taken by Ada Obi at 10:42 on 22 Nov.'), findsOneWidget);
    await snap(t, 'made-taken');
    expect(find.text('/expense'), findsNothing, reason: 'no sheet');
    await t.tap(find.bySemanticsLabel('Dismiss'));
    await settle(t);
    // Greyed: the row is not pressed again.
    await t.tap(find.bySemanticsLabel('See all of Open tickets'));
    await settle(t);
    expect(find.text('Change of address'), findsOneWidget, reason: 'every row');
    final pressed = server.sent.length;
    await t.tap(find.text('Wrong size delivered'));
    await settle(t);
    expect(server.sent.length, pressed);
    expect(find.text('Change of address'), findsOneWidget, reason: 'still on the list');
    await t.tap(find.bySemanticsLabel(RegExp('Back to Taskiem')).first);
    await settle(t);

    // A card's button that is gone.
    await open(t, 'Log an expense');
    await type(t, 'Amount', '4,500');
    await type(t, 'What for', 'Diesel for the generator');
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Yes, run it'));
    await settle(t);
    expect(find.text('Log another'), findsOneWidget);
    expect(find.text('Old button'), findsNothing, reason: 'a button without its token is left out');
    server.gone.add('tok-card-0');
    final card = find.bySemanticsLabel('Log another');
    await t.ensureVisible(card);
    await t.pumpAndSettle();
    await t.tap(card);
    await t.pump();
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
    expect(find.text('This button is not available any more.'), findsOneWidget);
    await snap(t, 'made-gone');
    expect(find.text('/expense'), findsNothing);
    await t.tap(find.bySemanticsLabel('Dismiss'));
    await settle(t);
    await snap(t, 'made-greyed');
    // Greyed: pressed again, nothing is sent.
    final sent = server.sent.length;
    await t.tap(card, warnIfMissed: false);
    await settle(t);
    expect(server.sent.length, sent);
  });

  testWidgets('refused: values that do not fit stay on the form; not yours and the limit as toasts', (t) async {
    final server = await taskiem(t);
    await open(t, 'Leave left');
    await type(t, 'Name', 'Musa');
    server.refuse = (status: 422, body: {'error': "the values do not fit the tool's form", 'problems': ['name: must be at least 5 characters']});
    await t.tap(find.bySemanticsLabel('Run it'));
    await settle(t);
    expect(find.text('Check these'), findsOneWidget);
    await snap(t, 'made-422');
    expect(find.text('Name: must be at least 5 characters.'), findsOneWidget);
    server.refuse = (status: 403, body: {'error': 'only managers may use Leave left'});
    await t.tap(find.bySemanticsLabel('Run it'));
    await t.pump();
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
    expect(find.text('This tool isn’t yours to use. Only managers may use Leave left.'), findsOneWidget);
    await t.pump(const Duration(seconds: 6));
    server.refuse = (status: 429, body: {'error': 'the day\'s runs are used; more at midnight'});
    await t.tap(find.bySemanticsLabel('Run it'));
    await t.pump();
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
    expect(find.textContaining('The business has used its runs for now. The day\'s runs are used'), findsOneWidget);
    await t.pump(const Duration(seconds: 6));
  });

  testWidgets('an owner\'s "Always ask before it runs", on a tool that only reads', (t) async {
    final server = await taskiem(t);
    await open(t, 'Leave left');
    expect(find.text('Always ask before it runs'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Always ask before it runs'));
    await settle(t);
    expect(server.sent.single.path, '/v1/tools/leave/settings');
    expect(server.sent.single.body, {'always_ask': true});
    expect(find.bySemanticsLabel('Next'), findsOneWidget, reason: 'it asks Yes now');
    await t.pump(const Duration(seconds: 6));
  });

  testWidgets('pictures: the sheet, and a card and a list answered', (t) async {
    await taskiem(t);
    await open(t, 'Log an expense');
    await type(t, 'Amount', '4,500');
    await type(t, 'What for', 'Diesel for the generator');
    await t.tap(find.bySemanticsLabel('Cash'));
    FocusManager.instance.primaryFocus?.unfocus();
    await settle(t);
    await looks(t, 'made-tool-sheet');
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    await looks(t, 'made-tool-yes');
    await t.tap(find.bySemanticsLabel('Yes, run it'));
    await settle(t);
    await open(t, 'Open tickets');
    await t.tap(find.bySemanticsLabel('Run it'));
    await settle(t);
    await looks(t, 'made-tool-answers');
    await t.tap(find.bySemanticsLabel('See all of Open tickets'));
    await settle(t);
    await looks(t, 'made-tool-list');
  });
}
