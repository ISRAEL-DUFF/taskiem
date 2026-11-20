// Saved things in "@" (saved objects S1, part 2; lib/chat/saved_picks.dart):
// the person's own first, then the business's, each saying whose when two
// share a name; a text's words into the box in place of what was typed;
// read only once "@" is typed.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/saved_picks.dart';
import 'package:taskiem/chat/tags.dart';

import 'my_chats_test.dart' show conversation, drain, openChat, phone, press, settle;
import 'support.dart' show loadFonts;

Map<String, dynamic> thing(String id, String owner, String kind, String name, String title, String hint, Map<String, dynamic> details) => {
  'id': id,
  'owner': owner,
  'name': name,
  'title': title,
  'kind': kind,
  'hint': hint,
  'details': details,
  'check': {'state': kind == 'bank_account' ? 'unchecked' : 'none', 'account_name': null, 'via': null, 'at': null, 'problem': null},
  'to_change': true,
  'created_at': '2026-11-20T10:00:00Z',
  'updated_at': '2026-11-20T10:00:00Z',
};

void main() {
  setUpAll(loadFonts);

  test('the person’s own first, a text with its words, whose when two share a name', () {
    final mine = SavedObject.fromJson(thing('m', 'mine', 'text', 'bank-details', 'My bank details', '1 line of text', {'body': 'Mine'}));
    final theirs = SavedObject.fromJson(thing('t', 'business', 'text', 'bank-details', 'Our bank details', '2 lines of text', {'body': 'Pay to Bola\nGTBank 0123'}));
    final acct = SavedObject.fromJson(thing('a', 'business', 'bank_account', 'bayo', 'Bayo', 'GTBank ••6789', {'bank_name': 'Guaranty Trust Bank'}));
    final all = savedTags([mine], [theirs, acct], textsOnly: false);
    expect([for (final s in all) s.id], ['m', 't', 'a']);
    expect(all[0].sub, 'My bank details · 1 line of text · Yours');
    expect(all[1].sub, 'Our bank details · 2 lines of text · The business’s');
    expect(all[2].body, isNull);
    expect([for (final s in savedTags([mine], [theirs, acct], textsOnly: true)) s.id], ['m', 't']);
    final q = tagQuery('Hello @bank', 11)!;
    expect(insertWords('Hello @bank', q, 11, 'Pay to Bola'), (text: 'Hello Pay to Bola', caret: 17));
  });

  testWidgets('in a chat with a business, “@” offers saved texts, and one picked fills the box', (t) async {
    phone(t);
    final api = conversation();
    var asked = 0;
    api.routes['GET /v1/me/saved'] = (_) {
      asked++;
      return {
        'saved': [
          thing('t1', 'mine', 'text', 'my-address', 'My address', '1 line of text', {'body': '12 Allen Avenue, Ikeja'}),
          thing('a1', 'mine', 'bank_account', 'mum', 'Mum', 'GTBank ••2222', {'bank_name': 'Guaranty Trust Bank'}),
        ],
        'limit': 500,
      };
    };
    await openChat(t, api);
    expect(asked, 0);
    final box = find.byType(EditableText).last;
    await t.enterText(box, 'Deliver to @my');
    await settle(t);
    expect(asked, 1);
    expect(find.bySemanticsLabel(RegExp('^Saved things')), findsWidgets);
    // Texts only in a chat with someone else; Mum's account is for later.
    expect(find.text('@mum'), findsNothing);
    await press(t, find.text('@my-address'));
    expect(t.widget<EditableText>(box).controller.text, 'Deliver to 12 Allen Avenue, Ikeja');
    await drain(t);
  });
}
