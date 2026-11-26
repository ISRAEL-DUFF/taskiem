// Saved things shared in chats (saved objects S1, part 3; lib/chat/
// saved_card.dart): a friend's @mum folded to a row, opened to each line
// with Copy, and Save this filling the sheet that saves it; and one's own
// shared from "@", after a yes to sharing the account number.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/saved_accounts.dart';

import 'looks.dart';
import 'people_test.dart' show adaChat, answer, drain, openPeople, phone, press, settle;
import 'saved_picks_test.dart' show thing;
import 'support.dart' show loadFonts, chooseHeld;

Map<String, dynamic> mumCard({bool mine = false, String id = 'c1'}) => {
  'id': id,
  'mine': mine,
  'kind': 'card',
  'body': 'Mum',
  'card': {
    'type': 'saved',
    'title': 'Mum',
    'saved': {
      'kind': 'bank_account',
      'name': 'mum',
      'title': 'Mum',
      'sensitive': true,
      'details': {'bank_name': 'Guaranty Trust Bank', 'bank_code': '000013', 'code_from': 'iswallet/taskiem', 'account_number': '0123452222'},
      'lines': [
        {'label': 'Bank', 'value': 'Guaranty Trust Bank'},
        {'label': 'Account number', 'value': '0123452222'},
      ],
    },
  },
  if (mine) 'status': 'sent',
  'read_at': null,
  'created_at': '2026-11-17T09:00:00Z',
};

void main() {
  setUpAll(loadFonts);

  testWidgets('a shared account: folded, opened with Copy, and Save this fills the sheet', (t) async {
    phone(t);
    final api = adaChat(messages: [mumCard()]);
    api.routes['GET /v1/me/banks'] = (_) => {
      'banks': [
        {'code': '000013', 'name': 'GTBank'},
        {'code': '000014', 'name': 'Access Bank'},
      ],
      'from': 'iswallet/taskiem',
    };
    await openPeople(t, api);
    expect(find.text('Mum'), findsOneWidget);
    expect(find.text('Bank account · Guaranty Trust Bank'), findsOneWidget);
    expect(find.text('0123452222'), findsNothing);

    await press(t, find.bySemanticsLabel(RegExp(r'^Mum, shared\. .*Show details$')));
    expect(find.text('Shared as @mum'), findsOneWidget);
    expect(find.text('0123452222'), findsOneWidget);
    expect(find.bySemanticsLabel('Copy account number'), findsOneWidget);
    await looks(t, 'saved-card');

    await press(t, find.text('Save this'));
    expect(find.byType(SavedAccountSheet), findsOneWidget);
    final fields = [for (final e in t.widgetList<EditableText>(find.descendant(of: find.byType(SavedAccountSheet), matching: find.byType(EditableText)))) e.controller.text];
    expect(fields, containsAll(['@mum', 'Mum', '0123452222']));
    expect(find.text('GTBank'), findsWidgets);
    await drain(t);
  });

  testWidgets('one’s own from “@”: asked first in the server’s words, then shared as a card', (t) async {
    phone(t);
    final api = adaChat(messages: const []);
    api.routes['GET /v1/me/saved'] = (_) => {
      'saved': [thing('s1', 'mine', 'bank_account', 'mum', 'Mum', 'GTBank ••2222', {'bank_name': 'Guaranty Trust Bank'})],
      'limit': 500,
    };
    var posts = 0;
    api.routes['POST /v1/me/people/pc1/messages'] = (r) {
      posts++;
      final saved = (api.body(r)['card'] as Map)['saved'] as Map;
      if (saved['confirm'] != true) return answer({'error': 'Share Mum’s account details with them?', 'confirm_share': true}, 409);
      return answer(mumCard(mine: true, id: 'c2'), 201);
    };
    await openPeople(t, api);
    final box = find.byType(EditableText).last;
    await t.enterText(box, '@mu');
    await settle(t);
    await press(t, find.text('@mum'));
    expect(t.widget<EditableText>(box).controller.text, '');
    expect(find.text('Share Mum’s account details with them?'), findsOneWidget);
    await press(t, find.text('Share'));
    expect(posts, 2);
    final sent = api.sent('POST', '/v1/me/people/pc1/messages');
    expect(api.body(sent.first)['card'], {'type': 'saved', 'saved': {'id': 's1', 'owner': 'mine', 'confirm': false}});
    expect((api.body(sent.last)['card'] as Map)['saved'], {'id': 's1', 'owner': 'mine', 'confirm': true});
    expect(find.bySemanticsLabel(RegExp(r'^Mum, shared\.')), findsOneWidget);
    // One's own: no Save this.
    await press(t, find.bySemanticsLabel(RegExp(r'^Mum, shared\. .*Show details$')));
    expect(find.text('Save this'), findsNothing);
    await drain(t);
  });

  testWidgets('Save as… on a message: its words read, the sheet filled in', (t) async {
    phone(t);
    final words = 'My sister’s account: Zenith Bank 2034567891, Account name: Ngozi Obi';
    final api = adaChat(messages: [
      {'id': 'w1', 'mine': false, 'kind': 'text', 'body': words, 'read_at': null, 'created_at': '2026-11-17T09:00:00Z'},
    ]);
    api.routes['POST /v1/me/saved/read'] = (r) {
      expect(api.body(r)['text'], words);
      return {'kind': 'bank_account', 'title': 'Ngozi Obi', 'details': {'bank_name': 'Zenith Bank', 'account_number': '2034567891'}};
    };
    api.routes['GET /v1/me/banks'] = (_) => {
      'banks': [
        {'code': '', 'name': 'Zenith Bank'},
        {'code': '', 'name': 'Access Bank'},
      ],
      'from': '',
    };
    await openPeople(t, api);
    await t.longPress(find.text(words));
    await settle(t);
    await chooseHeld(t, 'Save as…');
    await settle(t);
    expect(find.byType(SavedAccountSheet), findsOneWidget);
    final fields = [for (final e in t.widgetList<EditableText>(find.descendant(of: find.byType(SavedAccountSheet), matching: find.byType(EditableText)))) e.controller.text];
    expect(fields, containsAll(['', 'Ngozi Obi', '2034567891']));
    expect(find.text('Zenith Bank'), findsWidgets);
    await drain(t);
  });

  testWidgets('Save "@landlord"… in the picker when nothing has the name: what kind, then its sheet', (t) async {
    phone(t);
    final api = adaChat(messages: const []);
    api.routes['GET /v1/me/saved'] = (_) => {
      'saved': [thing('s1', 'mine', 'bank_account', 'mum', 'Mum', 'GTBank 2222', {'bank_name': 'Guaranty Trust Bank'})],
      'limit': 500,
    };
    await openPeople(t, api);
    final box = find.byType(EditableText).last;
    await t.enterText(box, '@mum');
    await settle(t);
    expect(find.text('Save "@mum"…'), findsNothing, reason: 'it has the name');
    await t.enterText(box, '@landlord');
    await settle(t);
    await looks(t, 'saved-save-new');
    await press(t, find.text('Save "@landlord"…'));
    expect(t.widget<EditableText>(box).controller.text, '');
    await press(t, find.text('A person'));
    expect(find.byType(SavedThingSheet), findsOneWidget);
    final fields = [for (final e in t.widgetList<EditableText>(find.descendant(of: find.byType(SavedThingSheet), matching: find.byType(EditableText)))) e.controller.text];
    expect(fields.first, '@landlord');
    await drain(t);
  });
}
