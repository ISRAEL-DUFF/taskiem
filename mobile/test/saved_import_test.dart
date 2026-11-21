// Import from a file (saved objects S3; lib/chat/saved_import.dart): a
// business's saved things from a CSV, the plan shown and saved with its
// hash, the lines to put right, and no entry for those who may not manage.

import 'dart:convert' show utf8;
import 'dart:typed_data' show Uint8List;

import 'package:flutter/widgets.dart' show SizedBox;
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/saved_import.dart' show pickSavedFile, savedImportKind, savedImportMax, savedImportText;
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

void chooses(String text, {List<int>? bytes, String name = 'saved.csv'}) {
  final was = pickSavedFile;
  pickSavedFile = () async => (name: name, bytes: Uint8List.fromList(bytes ?? utf8.encode(text)));
  addTearDown(() => pickSavedFile = was);
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
    // No plan: the button says what it does, not "Save".
    expect(find.text('Save'), findsNothing);
    expect(find.text('Choose another file'), findsOneWidget);
    await looks(t, 'saved-import-problems');
  });

  testWidgets('before a file: the button chooses one; a file over 256 KB is not sent', (t) async {
    phone(t);
    final api = importing([]);
    api.routes['POST /v1/saved/import'] = (_) => {'saved': false, 'hash': 'h', 'total': 0, 'rows': <Object>[]};
    chooses('', bytes: List.filled(savedImportMax + 1, 0x41), name: 'big.csv');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    await press(t, find.text('Import from a file'));
    expect(find.text('Save'), findsNothing);
    await press(t, find.text('Choose a file'));
    expect(find.text('The file is too big: up to 256 KB.'), findsOneWidget);
    expect(api.sent('POST', '/v1/saved/import'), isEmpty);
    expect(find.text('Choose another file'), findsOneWidget);
  });

  testWidgets('a UTF-16 file (Excel’s Unicode text, with tabs): read as its text', (t) async {
    for (final little in [true, false]) {
      phone(t);
      final api = importing([]);
      api.routes['POST /v1/saved/import'] = (_) => {'saved': false, 'hash': 'h', 'total': 0, 'rows': <Object>[]};
      const text = 'title\tphone\nỌlá Ìfẹ́\t08031234567\n';
      final bytes = <int>[if (little) ...[0xFF, 0xFE] else ...[0xFE, 0xFF]];
      for (final u in text.codeUnits) {
        bytes.addAll(little ? [u & 0xFF, u >> 8] : [u >> 8, u & 0xFF]);
      }
      chooses('', bytes: bytes, name: 'saved.txt');
      await t.pumpWidget(const SizedBox());
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
      await settle(t);
      await press(t, find.text('Import from a file'));
      await press(t, find.text('Choose a CSV file'));
      expect(api.body(api.sent('POST', '/v1/saved/import').single), {'csv': text}, reason: little ? 'FF FE' : 'FE FF');
    }
    // UTF-8 with its mark: the mark left out.
    expect(savedImportText(Uint8List.fromList([0xEF, 0xBB, 0xBF, ...utf8.encode('title\nỌlá\n')])), 'title\nỌlá\n');
  });

  testWidgets('a plan’s warnings and those saved already skipped; what was skipped, not checked and left', (t) async {
    phone(t);
    final api = importing([]);
    api.routes['POST /v1/saved/import'] = (r) {
      if (api.body(r)['confirm'] == null) {
        return {
          'saved': false,
          'hash': 'h1',
          'total': 3,
          'rows': [
            {'line': 2, 'name': 'ada', 'title': 'Ada Obi Stores', 'kind': 'bank_account', 'hint': 'GTBank ••4321', 'warning': 'The bank could not be asked: its name is checked later.'},
            {'line': 3, 'name': 'mum', 'title': 'Mum', 'kind': 'person', 'hint': '0803•••4567', 'skip': true},
            {'line': 4, 'name': 'nepa', 'title': 'Light bill', 'kind': 'provider_account', 'hint': 'Ikeja Electric'},
          ],
        };
      }
      return {
        'saved': true,
        'hash': 'h1',
        'total': 3,
        'added': ['ada'],
        'skipped': ['mum'],
        'unchecked': [
          {'line': 2, 'name': 'ada', 'problem': 'The bank did not answer.'},
        ],
        'left': [
          {'line': 4, 'name': 'nepa'},
        ],
      };
    };
    chooses(csv);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    await press(t, find.text('Import from a file'));
    await press(t, find.text('Choose a CSV file'));
    expect(find.text('@ada · Bank account · GTBank ••4321\nThe bank could not be asked: its name is checked later.'), findsOneWidget);
    expect(find.text('@mum · Person · 0803•••4567\nSaved already: skipped'), findsOneWidget);
    expect(find.text('@nepa · Account with a provider · Ikeja Electric'), findsOneWidget);
    expect(find.text('Save 2 things'), findsOneWidget, reason: 'the skipped row not counted');
    await press(t, find.text('Save 2 things'));
    expect(find.text('1 thing saved'), findsOneWidget);
    expect(find.text('Not checked yet'), findsOneWidget);
    expect(find.text('The bank did not answer.'), findsOneWidget);
    expect(find.text('Check names on the Saved page'), findsOneWidget);
    expect(find.text('Saved already, skipped'), findsOneWidget);
    expect(find.text('@mum'), findsOneWidget);
    expect(find.text('Not saved yet: send the file again'), findsOneWidget);
    expect(find.text('Line 4: @nepa'), findsOneWidget);
  });

  test('the word for each kind is the Saved page’s', () {
    expect(savedImportKind('provider_account'), 'Account with a provider');
    expect(savedImportKind('bank_account'), 'Bank account');
    expect(savedImportKind('file'), 'Photo or document');
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
