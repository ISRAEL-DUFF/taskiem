// Import from a file (saved objects S3; lib/chat/saved_import.dart): a
// business's saved things from a CSV, the plan shown and saved with its
// hash, the lines to put right, and no entry for those who may not manage.

import 'dart:convert' show utf8;
import 'dart:typed_data' show Uint8List;

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/money_page.dart' show pickStatement;
import 'package:taskiem/chat/saved_accounts.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'my_chats_test.dart' show Api, answer, phone, press, settle;
import 'saved_accounts_test.dart' show business, saved;
import 'support.dart' show loadFonts;

const csv = 'title,name,bank,account_number,phone,street,area\n'
    'Ada Obi Stores,ada,GTBank,0123454321,,,\n'
    'Mum,mum,,,08031234567,,\n'
    'Our warehouse,warehouse,,,,12 Herbert Macaulay Way,Yaba\n';

void chooses(String text) {
  final was = pickStatement;
  pickStatement = () async => (name: 'saved.csv', bytes: Uint8List.fromList(utf8.encode(text)));
  addTearDown(() => pickStatement = was);
}

Api importing(List<Map<String, dynamic>> list) {
  final api = business();
  api.routes['GET /v1/saved'] = (_) => {'saved': list, 'limit': 2000, 'can_check': false};
  return api;
}

void main() {
  setUpAll(loadFonts);

  testWidgets('a file: its plan shown, saved with the hash, what was added and what failed, the list read again', (t) async {
    phone(t);
    final list = [saved('s1', 'bayo', 'Bayo (rice)', 'GTBank ••6789', checkedName: 'BAYO ADEWALE')];
    final api = importing(list);
    api.routes['POST /v1/saved/import'] = (r) {
      final b = api.body(r);
      if (b['confirm'] == null) {
        return {
          'saved': false,
          'hash': 'h1',
          'total': 3,
          'rows': [
            {'line': 2, 'name': 'ada', 'title': 'Ada Obi Stores', 'kind': 'bank_account', 'hint': 'GTBank ••4321'},
            {'line': 3, 'name': 'mum', 'title': 'Mum', 'kind': 'person', 'hint': '0803•••4567'},
            {'line': 4, 'name': 'warehouse', 'title': 'Our warehouse', 'kind': 'place', 'hint': 'Yaba'},
          ],
        };
      }
      list.add({...saved('s2', 'mum', 'Mum', '0803•••4567'), 'kind': 'person'});
      return {
        'saved': true,
        'hash': 'h1',
        'total': 3,
        'added': ['mum', 'warehouse'],
        'failed': [
          {'line': 2, 'name': 'ada', 'message': 'GTBank does not know that account number: check it.'},
        ],
      };
    };
    chooses(csv);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    await press(t, find.text('Import from a file'));
    expect(find.text('Choose a CSV file'), findsOneWidget);
    await press(t, find.text('Choose a CSV file'));
    expect(api.body(api.sent('POST', '/v1/saved/import').single), {'csv': csv});
    expect(find.text('Ada Obi Stores'), findsOneWidget);
    expect(find.text('@ada · Bank account · GTBank ••4321'), findsOneWidget);
    expect(find.text('@mum · Person · 0803•••4567'), findsOneWidget);
    expect(find.text('saved.csv · 3 lines'), findsOneWidget);
    await looks(t, 'saved-import-plan');

    await press(t, find.text('Save 3 things'));
    expect(api.body(api.sent('POST', '/v1/saved/import').last), {'csv': csv, 'confirm': 'h1'});
    expect(find.text('2 things saved'), findsOneWidget);
    expect(find.text('Line 2: @ada'), findsOneWidget);
    expect(find.text('GTBank does not know that account number: check it.'), findsOneWidget);
    expect(find.text('@warehouse'), findsOneWidget);
    await looks(t, 'saved-import-done');

    final reads = api.sent('GET', '/v1/saved').length;
    await press(t, find.text('Done'));
    expect(api.sent('GET', '/v1/saved').length, greaterThan(reads));
    expect(find.text('Mum'), findsOneWidget);
  });

  testWidgets('a file that cannot be taken: its lines and what to fix, nothing saved', (t) async {
    phone(t);
    final api = importing([saved('s1', 'bayo', 'Bayo (rice)', 'GTBank ••6789', checkedName: 'BAYO ADEWALE')]);
    api.routes['POST /v1/saved/import'] = (_) => answer({
      'error': 'Some lines cannot be saved: put them right and send the file again.',
      'code': 'import_unreadable',
      'total': 3,
      'problems': [
        {'line': 3, 'message': '@bayo is saved already: give this one another name in the name column.'},
        {'line': 4, 'message': 'Give the account number: 10 digits.'},
      ],
    }, 422);
    chooses(csv);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    await press(t, find.text('Import from a file'));
    await press(t, find.text('Choose a CSV file'));
    expect(find.text('Nothing was saved'), findsOneWidget);
    expect(find.text('Some lines cannot be saved: put them right and send the file again.'), findsOneWidget);
    expect(find.text('Line 3'), findsOneWidget);
    expect(find.text('@bayo is saved already: give this one another name in the name column.'), findsOneWidget);
    expect(find.text('Line 4'), findsOneWidget);
    expect(find.text('Save'), findsOneWidget);
    await looks(t, 'saved-import-problems');
    // Save does nothing until a file is taken.
    await press(t, find.text('Save'));
    expect(api.sent('POST', '/v1/saved/import').length, 1);
  });

  testWidgets('those who may not manage saved things, and a person’s own list, have no import', (t) async {
    phone(t);
    final staff = importing([saved('s1', 'bayo', 'Bayo (rice)', 'GTBank ••6789', checkedName: 'BAYO ADEWALE')]);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: staff.auth(personal: false), mine: false)));
    await settle(t);
    expect(find.text('Bayo (rice)'), findsOneWidget);
    expect(find.text('Import from a file'), findsNothing);
    expect(find.text('Save something'), findsNothing);
  });
}
