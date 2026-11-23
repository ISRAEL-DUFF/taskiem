// A tool's form filled from typed words, saved things as its inputs, and
// who a waiting run waits for (made tools M1, M3, §7; docs/tools.md):
// "/expense 4500 diesel" sent opens the form filled in from POST
// /v1/tools/{command}/read, nothing started before its own button; the
// box at the form's top reads more words, a saved thing not there said;
// an input a saved kind fits has the Saved button; a waiting run says
// "Waiting for Ada to approve".

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show MadeTool, ToolAnswerWait;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/made_tools.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'made_tools_stub.dart';
import 'saved_stub.dart';
import 'support.dart';

/// Send a parcel: where it goes (a place, by its words), the phone at the
/// door (by its format), and what it is (no saved kind).
final _parcel = <String, dynamic>{
  'workflow_id': '0193a000-0000-7000-8000-000000000009',
  'environment': 'prod',
  'version': 1,
  'command': 'parcel',
  'name': 'Send a parcel',
  'about': 'Where a parcel goes',
  'says': <String>[],
  'care': {'level': 'yes'},
  'answer': 'text',
  'always_ask': false,
  'inputs': {
    'type': 'object',
    'required': ['deliver_to'],
    'properties': {
      'deliver_to': {'type': 'string', 'title': 'Deliver to'},
      'door_phone': {'type': 'string', 'title': 'Phone at the door', 'format': 'phone-ng'},
      'what': {'type': 'string', 'title': 'What it is'},
    },
  },
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  Future<MadeServer> taskiem(WidgetTester t, {MadeServer? server, SavedStub? saved, TkMode mode = TkMode.light}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tools');
    final s = server ?? MadeServer();
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final got = saved?.answer(req);
          if (got != null) return got;
          return s.call(req);
        },
      ),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: mode, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await settle(t);
    return s;
  }

  Finder box() => find.bySemanticsLabel('Message to Taskiem');
  Finder field(String label) => find.descendant(of: find.bySemanticsLabel(RegExp('^$label')), matching: find.byType(EditableText));
  String valueOf(WidgetTester t, String label) => t.widget<EditableText>(field(label)).controller.text;

  Future<void> send(WidgetTester t, String words) async {
    await t.enterText(box(), words);
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
  }

  test('a tool\'s input takes a saved kind by its hint, its format, then its words', () {
    SchemaField f(String name, Map<String, dynamic> p) => schemaFields({
      'type': 'object',
      'properties': {name: p},
    }).single;
    expect(toolSavedKind(f('branch', {'type': 'string', 'x-saved': 'place'})), 'place');
    expect(toolSavedKind(f('phone', {'type': 'string', 'x-saved': 'file'})), isNull);
    expect(toolSavedKind(f('contact', {'type': 'string', 'format': 'phone-ng'})), 'phone');
    expect(toolSavedKind(f('x', {'type': 'string', 'format': 'email'})), 'email');
    expect(toolSavedKind(f('site', {'type': 'string', 'format': 'uri'})), isNull);
    expect(toolSavedKind(f('customer_phone', {'type': 'string'})), 'phone');
    expect(toolSavedKind(f('customerEmail', {'type': 'string'})), 'email');
    expect(toolSavedKind(f('x', {'type': 'string', 'title': 'Delivery address'})), 'place');
    expect(toolSavedKind(f('account_number', {'type': 'string'})), 'bank_account');
    expect(toolSavedKind(f('due', {'type': 'string'})), 'date');
    expect(toolSavedKind(f('customer', {'type': 'string'})), 'person');
    expect(toolSavedKind(f('what', {'type': 'string', 'title': 'What for'})), isNull);
    expect(toolSavedKind(f('customer', {'type': 'string', 'enum': ['a', 'b']})), isNull);
    expect(toolSavedKind(f('phone', {'type': 'integer'})), isNull);
  });

  test('"/command words" names a tool and its words; "/command" alone does not', () {
    final tools = [for (final j in madeTools) MadeTool.fromJson(j)];
    final got = madeByCommand(tools, '/expense  4500   diesel');
    expect(got?.tool.command, 'expense');
    expect(got?.words, '4500 diesel');
    expect(madeByCommand(tools, '/expense'), isNull);
    expect(madeByCommand(tools, '/nope 4500'), isNull);
    expect(madeByCommand(tools, 'spent 4500'), isNull);
  });

  testWidgets('"/expense 4500 diesel" opens its form filled in from the words; nothing starts before Next', (t) async {
    final server = await taskiem(t);
    await send(t, '/expense 4500 diesel');
    expect(server.sent.map((s) => '${s.method} ${s.path}'), ['POST /v1/tools/expense/read']);
    expect(server.sent.single.body, {'text': '4500 diesel'});
    expect(find.text('/expense'), findsOneWidget, reason: 'its sheet');
    expect(valueOf(t, 'Amount'), '4500');
    expect(valueOf(t, 'What for'), 'diesel');
    expect(valueOf(t, 'Fill from words'), '4500 diesel');
    expect(find.text('Filled 2 fields from your words. Check them before you go on.'), findsOneWidget);
    // Not sent to Taskiem as a message.
    expect(find.textContaining('/expense 4500 diesel', findRichText: true), findsNothing);
    await snap(t, 'made-words');

    // More words in the box: a saved thing not there is said, nothing filled.
    await t.enterText(field('Fill from words'), '@nowhere');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Fill the form from these words'));
    await settle(t);
    expect(find.text('Nothing the business saved is called @nowhere.'), findsOneWidget);
    expect(valueOf(t, 'What for'), 'diesel');
    await t.enterText(field('Fill from words'), '5,000 fuel');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Fill the form from these words'));
    await settle(t);
    expect(valueOf(t, 'Amount'), '5000');
    expect(valueOf(t, 'What for'), 'fuel');
    expect(server.sent.where((s) => s.path.endsWith('/start')), isEmpty);
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    expect(server.sent.last.path, '/v1/tools/expense/start');
    expect((server.sent.last.body['input'] as Map)['what'], 'fuel');
  });

  testWidgets('an input a saved kind fits has the Saved button: a place fills its address, a phone its number', (t) async {
    final saved = SavedStub(
      business: [
        savedThing('B1', 'business', 'place', 'yaba-shop', 'Yaba shop', 'Yaba', {'line': '3 Herbert Macaulay Way', 'area': 'Yaba', 'phone': '08031234567'}),
      ],
    );
    final server = await taskiem(t, server: MadeServer(tools: [...madeTools, _parcel]), saved: saved);
    await send(t, '/parcel a box of rice');
    expect(find.text('/parcel'), findsOneWidget);
    expect(find.bySemanticsLabel('Pick a saved place for Deliver to'), findsOneWidget);
    expect(find.bySemanticsLabel('Pick a saved phone number for Phone at the door'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Pick a saved .* for What it is')), findsNothing);
    await t.tap(find.bySemanticsLabel('Pick a saved place for Deliver to'));
    await settle(t);
    expect(find.text('Yaba shop'), findsOneWidget);
    await t.tap(find.text('Yaba shop'));
    await settle(t);
    expect(valueOf(t, 'Deliver to'), '3 Herbert Macaulay Way, Yaba');
    await t.tap(find.bySemanticsLabel('Pick a saved phone number for Phone at the door'));
    await settle(t);
    await t.tap(find.text('Yaba shop'));
    await settle(t);
    expect(valueOf(t, 'Phone at the door'), '08031234567');
    await snap(t, 'made-saved');
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    expect((server.sent.last.body['input'] as Map)['deliver_to'], '3 Herbert Macaulay Way, Yaba');
  });

  testWidgets('a waiting run says who it waits for: Ada to approve, or your answer under To fill in', (t) async {
    final server = await taskiem(t);
    // The next two runs wait: on Ada's approval, then on the person's form.
    server.answers['0193b000-0000-7000-8000-000000000001'] = [
      {
        'status': 'waiting',
        'type': 'outcome',
        'tool': 'expense',
        'waiting_for': [
          {'kind': 'approval', 'who': 'Ada'},
        ],
      },
    ];
    server.answers['0193b000-0000-7000-8000-000000000002'] = [
      {
        'status': 'waiting',
        'type': 'outcome',
        'tool': 'expense',
        'waiting_for': [
          {'kind': 'form', 'who': 'you', 'you': true, 'request': '0193d000-0000-7000-8000-000000000001'},
        ],
      },
    ];
    for (final w in ['4500 diesel', '2000 water']) {
      await send(t, '/expense $w');
      await t.tap(find.bySemanticsLabel('Next'));
      await settle(t);
      await t.tap(find.bySemanticsLabel('Yes, run it'));
      await settle(t);
    }
    expect(find.textContaining('Waiting for Ada to approve.', findRichText: true), findsOneWidget);
    expect(find.textContaining('Waiting for your answer: open To fill in.', findRichText: true), findsOneWidget);
    await snap(t, 'made-waiting');
    expect(
      waitWords(const ToolAnswerWait(kind: 'form', who: 'the cashiers')),
      'Waiting for the cashiers to answer.',
    );
  });

  testWidgets('pictures: the form filled from words, with its Saved buttons, in the dark', (t) async {
    final saved = SavedStub();
    await taskiem(t, server: MadeServer(tools: [...madeTools, _parcel]), saved: saved, mode: TkMode.dark);
    await send(t, '/parcel @nowhere');
    expect(find.text('Nothing the business saved is called @nowhere.'), findsOneWidget);
    await snap(t, 'made-saved-dark');
  });
}
