// Connecting a service from Taskiem's conversation, and a draft's canvas
// (docs/spec/mobile-app.md §3.5; docs/spec/mobile-app-m3-fixes.md G1, G2):
// the link in Taskiem's words pressed; a service with keys gets the form
// here, one that signs in and a workflow's canvas open in the browser tab;
// the link outlives the history's copy of the words, which keeps none.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/connect.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _tok = 'AbCdEfGhIjKlMnOpQrStUvWxYz012345';
const _link = 'https://taskiem.test/c/$_tok';
const _said = 'Tap the button below to connect Paystack. It works once, for 15 minutes, and only for you. Never send a key in this chat: type it in the form.';

/// connections.test.ts secretKey.
const _secretKey = {
  'key': 'secret_key',
  'label': 'Secret key',
  'secret': true,
  'prefixes': [
    {'prefix': 'sk_live_', 'mode': 'live', 'label': 'Live (real money)'},
    {'prefix': 'sk_test_', 'mode': 'test', 'label': 'Test (practice)'},
  ],
  'refuse': [
    {'prefix': 'pk_', 'message': 'That is your public key.'},
  ],
};

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

Map<String, dynamic> _view({bool oauth = false}) => {
  'business': "Ada's Kitchen",
  'channel': 'web',
  'expires_at': '2026-11-11T13:45:00Z',
  'connector': {
    'ref': 'paystack@1',
    'id': 'paystack',
    'version': '1.0.0',
    'name': oauth ? 'Google Sheets' : 'Paystack',
    'actions': <String, Object>{},
    'triggers': <Object>[],
    'oauth_linkable': oauth,
    'auth': oauth
        ? {
            'type': 'oauth2',
            'fields': null,
            'oauth2': {'authorize_url': 'https://accounts.example/o', 'token_url': 'https://accounts.example/t', 'scopes': <String>[], 'pkce': true},
          }
        : {
            'type': 'api_key',
            'fields': [
              _secretKey,
              {'key': 'hash', 'label': 'Hash', 'required': false},
            ],
          },
  },
};

void main() {
  group('the words (connections.test.ts)', () {
    test('what is wrong with a pasted key, as the server says', () {
      expect(keyProblem(_secretKey, 'pk_live_1', 'live'), 'That is your public key.');
      expect(keyProblem(_secretKey, 'sk_test_1', 'live'), 'that is a test (practice) secret key, but you chose live (real money); copy the live (real money) secret key instead');
      expect(keyProblem(_secretKey, 'hello', 'test'), 'that does not look like your secret key: it starts with sk_test_');
      expect(keyProblem(_secretKey, 'sk_live_1', 'live'), '');
      expect(keyProblem(_secretKey, '', 'live'), '');
      expect(keyProblem({'key': 'k', 'label': 'API key'}, 'anything'), '');
    });

    test('live and test as choices; optional fields aside; a token Taskiem makes never asked', () {
      final auth = {
        'type': 'api_key',
        'fields': [
          _secretKey,
          {'key': 'hash', 'label': 'Hash', 'required': false},
          {'key': 'callback_token', 'label': 'Webhook token', 'required': false, 'generate': 'webhook_token'},
        ],
      };
      expect(modesOf(auth), [(mode: 'live', label: 'Live (real money)', available: true), (mode: 'test', label: 'Test (practice)', available: true)]);
      expect(splitFields(auth).main.map((f) => f['key']), ['secret_key']);
      expect(splitFields(auth).more.map((f) => f['key']), ['hash']);
      expect(
        modesOf({
          'modes': [
            {'mode': 'live', 'label': 'Live (real money)'},
            {'mode': 'test', 'label': 'Test (practice)', 'base_url': 'https://synledger.name.ng/iwallet'},
          ],
        }),
        [(mode: 'live', label: 'Live (real money)', available: false), (mode: 'test', label: 'Test (practice)', available: true)],
      );
    });

    test('which addresses open from Taskiem\'s words', () {
      final found = linksIn('Saved *Weekly debts* as a draft at https://taskiem.test/workflows/01a1cac4-7150-72ec-bfd6-2ac7455b506b. Connect: $_link, then (https://example.com/x).');
      expect(found.map((l) => l.uri.toString()), ['https://taskiem.test/workflows/01a1cac4-7150-72ec-bfd6-2ac7455b506b', _link, 'https://example.com/x']);
      expect(found.map((l) => opensFromTaskiem(l.uri)), [true, true, false]);
      expect(connectToken(Uri.parse(_link)), _tok);
      expect(connectToken(Uri.parse('https://taskiem.test/c/short')), isNull);
      expect(isCanvas(Uri.parse('https://taskiem.test/workflows/01a1cac4-7150-72ec-bfd6-2ac7455b506b')), isTrue);
      expect(isCanvas(Uri.parse('https://taskiem.test/money')), isFalse);
    });

    test('the history\'s copy keeps no link: the live words are kept', () {
      expect(keptWithoutLinks('$_said\nConnect Paystack: $_link', _said), isTrue);
      expect(
        keptWithoutLinks('Saved *X* as a draft at https://taskiem.test/workflows/01a1cac4-7150-72ec-bfd6-2ac7455b506b.', 'Saved *X* as a draft at https://taskiem.test/workflows/….'),
        isTrue,
      );
      expect(keptWithoutLinks('status', 'status'), isFalse);
      expect(keptWithoutLinks('$_said\nConnect Paystack: $_link', 'Something else'), isFalse);
      TaskiemChatMessagesItem m(String text) => TaskiemChatMessagesItem(id: 'a1', channel: 'web', inbound: false, kind: 'text', text: text, createdAt: DateTime.utc(2026, 11, 11, 13));
      final merged = mergeTaskiem(mergeTaskiem(const [], [m('$_said\nConnect Paystack: $_link')]), [m(_said)]);
      expect(merged.single.m.text, '$_said\nConnect Paystack: $_link');
    });
  });

  group('the screen', () {
    setUpAll(loadFonts);

    Future<void> settle(WidgetTester t) async {
      for (var i = 0; i < 6; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
        await t.pump();
      }
    }

    /// Taskiem's conversation, "connect paystack" answered with [answer];
    /// what was sent to /v1/connect kept.
    Future<List<Object?>> ask(WidgetTester t, String answer, {bool oauth = false}) async {
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final f = Fixture('chat-taskiem');
      final posted = <Object?>[];
      final chats = ChatsData(
        f.auth(also: (req) async {
          final p = req.url.path;
          if (req.method == 'POST' && p == '/v1/taskiem-chat') {
            return _json({
              'messages': [
                {'id': 'q1', 'channel': 'web', 'inbound': true, 'kind': 'text', 'text': 'connect paystack', 'created_at': '2026-11-11T13:30:00Z'},
                {'id': 'a1', 'channel': 'web', 'inbound': false, 'kind': 'text', 'text': answer, 'created_at': '2026-11-11T13:30:01Z'},
              ],
            });
          }
          if (req.method == 'GET' && p == '/v1/connect/$_tok') return _json(_view(oauth: oauth));
          if (req.method == 'POST' && p == '/v1/connect/$_tok') {
            final body = jsonDecode(req.body) as Map<String, dynamic>;
            posted.add(body);
            if ((body['credentials'] as Map)['secret_key'] == 'sk_live_bad') return _json({'error': 'Paystack refused that key: check it and try again.'}, 400);
            return _json(
              {
                'connected': 'Paystack',
                'business': "Ada's Kitchen",
                'name': 'paystack',
                'webhooks': [
                  {'trigger': 'charge', 'title': 'Payments', 'where': 'Settings, API keys and webhooks', 'url': 'https://taskiem.test/hooks/paystack/abc'},
                ],
              },
              201,
            );
          }
          return null;
        }),
        now: () => f.at,
      )..load();
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
      await settle(t);
      await t.enterText(find.byType(EditableText), 'connect paystack');
      await t.pump();
      await t.tap(find.bySemanticsLabel('Send'));
      await settle(t);
      return posted;
    }

    Future<void> tapLink(WidgetTester t, String link) async {
      await t.ensureVisible(find.textContaining(link, findRichText: true));
      await t.pumpAndSettle();
      await t.tapOnText(find.textRange.ofSubstring(link));
      await settle(t);
      await t.pumpAndSettle();
    }

    testWidgets('a service with keys: the form here, checked as typed, then connected with its address to paste', (t) async {
      final opened = <Uri>[];
      final was = openInBrowser;
      openInBrowser = (u) async {
        opened.add(u);
        return true;
      };
      addTearDown(() => openInBrowser = was);
      final posted = await ask(t, '$_said\nConnect Paystack: $_link');
      await tapLink(t, _link);
      expect(opened, isEmpty, reason: 'a key is typed here, never in a browser or the chat');
      expect(find.text("Connect Paystack to Ada's Kitchen"), findsOneWidget);
      await t.enterText(find.byType(EditableText).last, 'sk_test_1');
      await t.pump();
      expect(find.text('That is a test (practice) secret key, but you chose live (real money); copy the live (real money) secret key instead.'), findsOneWidget);
      expect(find.bySemanticsLabel('Connect Paystack'), findsOneWidget);
      await t.enterText(find.byType(EditableText).last, 'sk_live_bad');
      await t.pump();
      await t.tap(find.bySemanticsLabel('Connect Paystack'));
      await settle(t);
      expect(find.text('Paystack refused that key: check it and try again.'), findsOneWidget);
      await t.enterText(find.byType(EditableText).last, 'sk_live_good');
      await t.pump();
      await t.tap(find.bySemanticsLabel('Connect Paystack'));
      await settle(t);
      expect(posted.last, {
        'credentials': {'secret_key': 'sk_live_good'},
        'mode': 'live',
      });
      expect(find.text('Paystack is connected'), findsOneWidget);
      expect(find.text('https://taskiem.test/hooks/paystack/abc'), findsOneWidget);
      await t.tap(find.bySemanticsLabel('Done'));
      await t.pumpAndSettle();
      expect(find.text('Paystack is connected'), findsNothing);
    });

    testWidgets('a service that signs in: its connect page in the browser tab', (t) async {
      final opened = <Uri>[];
      final was = openInBrowser;
      openInBrowser = (u) async {
        opened.add(u);
        return true;
      };
      addTearDown(() => openInBrowser = was);
      await ask(t, 'Tap the button below to connect Google Sheets.\nConnect Google Sheets: $_link', oauth: true);
      await tapLink(t, _link);
      expect(opened, [Uri.parse(_link)]);
      expect(find.textContaining('Connect Google Sheets to'), findsNothing);
    });

    testWidgets('a draft saved: its canvas in the browser tab', (t) async {
      final opened = <Uri>[];
      final was = openInBrowser;
      openInBrowser = (u) async {
        opened.add(u);
        return true;
      };
      addTearDown(() => openInBrowser = was);
      const canvas = 'https://taskiem.test/workflows/01a1cac4-7150-72ec-bfd6-2ac7455b506b';
      await ask(t, 'Saved *Weekly debts* as a draft at $canvas.');
      await tapLink(t, canvas);
      expect(opened, [Uri.parse(canvas)]);
    });
  });
}
