// Their tools (made tools T5): in a person's chat with a business, its
// tools for customers under the tools button; a tool's form, then what
// will run and Yes (always), then its answer, drawn from what the business
// made public; a button on the answer opens another of their tools, filled
// in. A business with none shows only the chat's own tools.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/my_chat_their_tools.dart';
import 'package:taskiem/kit/sheet.dart' show TkKitField;

import 'looks.dart';
import 'my_chats_test.dart' show answer, chat, conversation, drain, openChat, phone, press, typeIn;
import 'support.dart' show loadFonts;

const _status = {
  'command': 'order-status',
  'name': 'Order status',
  'about': 'Where your order is',
  'answer': 'card',
  'inputs': {
    'type': 'object',
    'required': ['ref'],
    'properties': {
      'ref': {'type': 'string', 'title': 'Order number'},
    },
  },
};

const _track = {
  'command': 'track',
  'name': 'Track an order',
  'about': 'Where the rider is',
  'answer': 'text',
  'inputs': {
    'type': 'object',
    'required': ['ref'],
    'properties': {
      'ref': {'type': 'string', 'title': 'Order number'},
    },
  },
};

void main() {
  setUpAll(loadFonts);
  setUp(() => TheirToolSheet.every = const Duration(milliseconds: 10));

  testWidgets('their tools: the form, Yes always, the answer from what they made public, a button to another tool', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['GET /v1/me/chats/c1/tools'] = (_) => {
      'tools': [_status, _track],
    };
    api.routes['POST /v1/me/chats/c1/tools/order-status/start'] = (r) => {
      'care': 'yes',
      'confirm': 'k1',
      'expires_at': '2026-11-16T10:40:00Z',
      'values': [
        {'name': 'ref', 'label': 'Order number', 'value': api.body(r)['input']['ref']},
      ],
    };
    api.routes['POST /v1/me/chats/c1/tools/order-status/confirm'] = (_) => answer({'run_id': '0a1b2c3d-0000-4000-8000-000000000001', 'created': true}, 201);
    var reads = 0;
    api.routes['GET /v1/me/chats/c1/runs/0a1b2c3d-0000-4000-8000-000000000001/answer'] = (_) => ++reads == 1
        ? {'status': 'running', 'type': 'outcome'}
        : {
            'status': 'completed',
            'type': 'card',
            'tool': 'order-status',
            'title': 'On its way',
            'ref': 'MK-1042',
            'state': {'text': 'Out with the rider', 'tone': 'ok'},
            'lines': [
              {'label': 'Rider', 'value': 'Musa'},
              {'label': 'Expected', 'value': 'By 2pm'},
            ],
            'buttons': [
              {'label': 'Track it', 'tool': 'track', 'token': 'b1', 'inputs': {'ref': 'MK-1042'}},
            ],
          };
    api.routes['POST /v1/me/chats/c1/tools/buttons/b1'] = (_) => {
      'button': 'b1',
      'command': 'track',
      'name': 'Track an order',
      'inputs': {'ref': 'MK-1042'},
    };

    await openChat(t, api);
    await press(t, find.bySemanticsLabel('Chat tools'));
    expect(find.text('Their tools'), findsOneWidget);
    expect(find.text('Where your order is'), findsOneWidget);
    await looks(t, 'their-tools');
    await press(t, find.text('Order status'));
    final field = find.ancestor(of: find.text('Order number'), matching: find.byType(TkKitField));
    await typeIn(t, field, 'MK-1042');
    await looks(t, 'their-tool-form');
    await press(t, find.text('Next'));
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/tools/order-status/start').single), {
      'input': {'ref': 'MK-1042'},
    });
    // Yes, always: nothing has started.
    expect(find.text('Yes, go ahead'), findsOneWidget);
    expect(find.text('MK-1042'), findsOneWidget);
    expect(api.sent('POST', '/v1/me/chats/c1/tools/order-status/confirm'), isEmpty);
    await looks(t, 'their-tool-yes');
    await press(t, find.text('Yes, go ahead'));
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/tools/order-status/confirm').single), {
      'confirm': 'k1',
      'input': {'ref': 'MK-1042'},
    });
    await t.pump(const Duration(milliseconds: 50));
    await t.pumpAndSettle();
    expect(find.text('On its way'), findsOneWidget);
    expect(find.text('Musa'), findsOneWidget);
    await looks(t, 'their-tool-answer');
    await press(t, find.text('Track it'));
    expect(api.sent('POST', '/v1/me/chats/c1/tools/buttons/b1'), hasLength(1));
    expect(find.text('Track an order'), findsOneWidget);
    expect(find.text('MK-1042'), findsOneWidget, reason: 'the button filled the form');
    await drain(t);
  });

  testWidgets('their tools: none offered, or a tool they do not offer customers', (t) async {
    phone(t);
    final api = conversation(c: chat(offers: 'no', ask: false));
    api.routes['GET /v1/me/chats/c1/tools'] = (_) => {
      'tools': [_track],
    };
    api.routes['POST /v1/me/chats/c1/tools/track/start'] = (_) => answer({'error': 'This can’t be done from here. Ask the business in the chat.', 'not_offered': true}, 409);
    await openChat(t, api);
    await press(t, find.bySemanticsLabel('Chat tools'));
    await press(t, find.text('Track an order'));
    await typeIn(t, find.ancestor(of: find.text('Order number'), matching: find.byType(TkKitField)), 'MK-1');
    await press(t, find.text('Next'));
    expect(find.text('This can’t be done from here. Ask the business in the chat.'), findsOneWidget);
    expect(find.text('Yes, go ahead'), findsNothing);
    await drain(t);

    final none = conversation(c: chat(offers: 'no', ask: false));
    await openChat(t, none);
    await press(t, find.bySemanticsLabel('Chat tools'));
    expect(find.text('Their shop'), findsOneWidget);
    expect(find.text('Their tools'), findsNothing);
    await drain(t);
  });
}
