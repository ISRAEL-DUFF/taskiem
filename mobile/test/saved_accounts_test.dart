// Saved accounts (saved objects S0; lib/chat/saved_accounts.dart): a
// business's list, saving one with the bank's name shown first, changing
// one with a passkey, the step to open a Taskiem wallet, and a person's own
// number shown with a passkey.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/chat/groups.dart' show choiceMatches;
import 'package:taskiem/chat/saved_accounts.dart';
import 'package:taskiem/kit/sheet.dart' show TkKitField;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'my_chats_test.dart' show Api, answer, chat, conversation, drain, phone, press, settle, typeIn;
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart' show loadFonts;

Map<String, dynamic> saved(String id, String name, String title, String hint, {String owner = 'business', String? checkedName, String? problem}) => {
  'id': id,
  'owner': owner,
  'name': name,
  'title': title,
  'kind': 'bank_account',
  'hint': hint,
  'details': {'bank_name': 'Guaranty Trust Bank', 'bank_code': '000013', 'code_from': 'iswallet/taskiem', 'account_number': '••${hint.substring(hint.length - 4)}'},
  'check': {
    'state': checkedName == null ? 'unchecked' : 'checked',
    'account_name': checkedName,
    'via': checkedName == null ? null : 'iswallet/taskiem',
    'at': checkedName == null ? null : '2026-11-20T10:00:00Z',
    'problem': problem,
  },
  'to_change': true,
  'created_at': '2026-11-20T10:00:00Z',
  'updated_at': '2026-11-20T10:00:00Z',
};

const _wallet = {
  'connected': {'channel': 'iswallet/taskiem', 'name': 'Your Taskiem wallet', 'wallet': true},
  'owner': true,
};

Api business({Map<String, dynamic> provider = _wallet}) {
  final api = conversation(c: chat());
  final list = [
    saved('s1', 'bayo', 'Bayo (rice)', 'GTBank ••6789', checkedName: 'BAYO ADEWALE'),
    saved('s2', 'landlord', 'Landlord', 'Zenith Bank ••1111', problem: 'Your Taskiem wallet does not list Zenith Bank by that name: choose the bank again from its list to check the name.'),
  ];
  api.routes['GET /v1/saved'] = (_) => {'saved': list, 'limit': 2000, 'can_check': false};
  api.routes['GET /v1/saved/provider'] = (_) => provider;
  api.routes['GET /v1/banks'] = (_) => {
    'banks': [
      {'code': '000014', 'name': 'Access Bank'},
      {'code': '000009', 'name': 'Citibank Nigeria'},
      {'code': '000010', 'name': 'Ecobank Nigeria'},
      {'code': '000007', 'name': 'Fidelity Bank'},
      {'code': '000016', 'name': 'First Bank of Nigeria'},
      {'code': '000003', 'name': 'First City Monument Bank'},
      {'code': '000013', 'name': 'Guaranty Trust Bank'},
      {'code': '090267', 'name': 'Kuda Microfinance Bank'},
      {'code': '000015', 'name': 'Zenith Bank'},
    ],
    'from': 'iswallet/taskiem',
  };
  api.routes['POST /v1/name-check'] = (_) => {'account_name': 'ADA OBI STORES', 'via': 'iswallet/taskiem'};
  api.routes['POST /v1/saved'] = (r) {
    final b = api.body(r);
    final x = saved('s3', b['name'] as String, b['title'] as String, 'GTBank ••4321', checkedName: 'ADA OBI STORES');
    list.add(x);
    return answer({'saved': x}, 201);
  };
  api.routes['POST /v1/saved/step-up/options'] = (_) => {'publicKey': {'challenge': 'abc', 'rpId': 'taskiem.test'}};
  api.routes['PUT /v1/saved/s1'] = (r) => {'saved': saved('s1', 'bayo', 'Bayo (rice)', 'GTBank ••0000', checkedName: 'BAYO ADEWALE')};
  return api;
}

Finder field(String label) => find.ancestor(of: find.text(label), matching: find.byType(TkKitField));

void main() {
  setUpAll(loadFonts);

  testWidgets('the other kinds: saved through the chooser, filtered by kind, changed with no passkey', (t) async {
    phone(t);
    final api = conversation(c: chat());
    final list = <Map<String, dynamic>>[
      {
        ...saved('w1', 'warehouse', 'Our warehouse', 'Yaba'),
        'kind': 'place',
        'details': {'line': '12 Herbert Macaulay Way', 'area': 'Yaba'},
        'check': {'state': 'none', 'account_name': null, 'via': null, 'at': null, 'problem': null},
      },
      saved('s1', 'bayo', 'Bayo (rice)', 'GTBank ••6789', checkedName: 'BAYO ADEWALE'),
    ];
    api.routes['GET /v1/saved'] = (_) => {'saved': list, 'limit': 2000, 'can_check': false};
    api.routes['GET /v1/saved/provider'] = (_) => _wallet;
    api.routes['POST /v1/saved'] = (r) {
      final b = api.body(r);
      final x = {
        ...saved('t1', b['name'] as String, b['title'] as String, '2 lines of text'),
        'kind': b['kind'],
        'details': b['details'],
        'check': {'state': 'none', 'account_name': null, 'via': null, 'at': null, 'problem': null},
      };
      list.add(x);
      return answer({'saved': x}, 201);
    };
    api.routes['PUT /v1/saved/w1'] = (r) => {'saved': list.first};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    // Two kinds: a filter; a place has no check pill.
    expect(find.text('Places'), findsOneWidget);
    expect(find.text('Checked'), findsOneWidget);
    await press(t, find.text('Places'));
    expect(find.text('Bayo (rice)'), findsNothing);
    expect(find.text('Our warehouse'), findsOneWidget);
    await press(t, find.text('All'));

    await press(t, find.text('Save something'));
    await press(t, find.text('A text'));
    await press(t, find.text('Save'));
    expect(find.text('Give it a short name to call it up by, like @mum or @bayo.'), findsOneWidget);
    await typeIn(t, field('Name to call it up by'), 'bank-details');
    await typeIn(t, field('Title'), 'Our bank details');
    await press(t, find.text('Save'));
    expect(find.text('Write the text.'), findsOneWidget);
    await typeIn(t, field('The text'), 'Pay to Bola Stores\nGTBank 0123456789');
    await press(t, find.text('Save'));
    expect(api.body(api.sent('POST', '/v1/saved').last), {
      'details': {'body': 'Pay to Bola Stores\nGTBank 0123456789'},
      'kind': 'text',
      'name': 'bank-details',
      'title': 'Our bank details',
    });
    expect(find.text('Our bank details'), findsOneWidget);

    // A place changed field by field, no passkey asked.
    await press(t, find.text('Our warehouse'));
    expect(find.text('Show the number'), findsNothing);
    await press(t, find.text('Change'));
    await typeIn(t, field('Landmark'), 'by the blue gate');
    await press(t, find.text('Save'));
    final body = api.body(api.sent('PUT', '/v1/saved/w1').last);
    expect((body['details'] as Map)['landmark'], 'by the blue gate');
    expect((body['details'] as Map)['phone'], isNull);
    expect(body.containsKey('passkey'), isFalse);
    expect(api.sent('POST', '/v1/saved/step-up/options'), isEmpty);
    await drain(t);
  });

  test('a search finds choices by the start of their words', () {
    expect(choiceMatches('Guaranty Trust Bank', 'guar'), isTrue);
    expect(choiceMatches('First City Monument Bank', 'first city'), isTrue);
    expect(choiceMatches('GTBank', 'gtb'), isTrue);
    expect(choiceMatches('Kuda Microfinance Bank', 'fi'), isFalse);
    expect(choiceMatches('Zenith Bank', ''), isTrue);
    expect(choiceMatches('Zenith Bank', 'acc'), isFalse);
  });

  testWidgets('a business’s saved accounts, and saving one with the bank’s name shown first', (t) async {
    phone(t);
    final api = business();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    expect(find.text('Bayo (rice)'), findsOneWidget);
    expect(find.text('Checked'), findsOneWidget);
    expect(find.text('Not checked'), findsOneWidget);
    expect(find.text('Names are checked with the bank through your Taskiem wallet.'), findsOneWidget);
    expect(find.text('Check names with the bank'), findsNothing);
    // Why the landlord's name is not checked, on its actions.
    await press(t, find.text('Landlord'));
    expect(find.textContaining('does not list Zenith Bank by that name'), findsOneWidget);
    await t.tapAt(const Offset(5, 5));
    await settle(t);
    await looks(t, 'saved');

    await press(t, find.text('Save something'));
    await press(t, find.text('A bank account'));
    await press(t, find.text('Save account'));
    expect(find.text('Give it a short name to call it up by, like @mum or @bayo.'), findsOneWidget);
    await typeIn(t, field('Name to call it up by'), '@Ada Obi');
    await typeIn(t, field('Title'), 'Ada Obi Stores');
    // The wallet lists many banks: a search narrows them.
    await press(t, find.text('Choose the bank'));
    expect(find.text('Zenith Bank'), findsOneWidget);
    await typeIn(t, find.bySemanticsLabel('Find the bank: guaranty, kuda…').last, 'guar');
    expect(find.text('Zenith Bank'), findsNothing);
    expect(find.text('Access Bank'), findsNothing);
    await press(t, find.text('Guaranty Trust Bank').last);
    await typeIn(t, field('Account number'), '0123454321');
    await settle(t);
    expect(find.text('ADA OBI STORES'), findsOneWidget);
    expect(api.body(api.sent('POST', '/v1/name-check').last), {'account_number': '0123454321', 'bank_code': '000013', 'bank_name': 'Guaranty Trust Bank', 'code_from': 'iswallet/taskiem'});
    await press(t, find.text('Save account'));
    expect(api.body(api.sent('POST', '/v1/saved').last), {
      'account_number': '0123454321',
      'bank_code': '000013',
      'bank_name': 'Guaranty Trust Bank',
      'code_from': 'iswallet/taskiem',
      'kind': 'bank_account',
      'name': 'ada-obi',
      'note': '',
      'title': 'Ada Obi Stores',
    });
    expect(find.text('Ada Obi Stores'), findsOneWidget);
    await drain(t);
  });

  testWidgets('changing an account asks for a passkey for that account; a title alone does not', (t) async {
    phone(t);
    final was = passkeys;
    final fake = FakePasskeys();
    passkeys = fake;
    addTearDown(() => passkeys = was);
    final api = business();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    await press(t, find.text('Bayo (rice)'));
    await press(t, find.text('Change'));
    // The saved bank, found again in the wallet's list.
    expect(find.text('Guaranty Trust Bank'), findsOneWidget);
    await typeIn(t, field('Account number'), '0123450000');
    expect(find.text('Changing the account asks for your passkey'), findsOneWidget);
    await press(t, find.text('Save changes'));
    expect(api.body(api.sent('POST', '/v1/saved/step-up/options').last), {'operation': 'saved.change', 'target': 's1'});
    expect(fake.asked, hasLength(1));
    final body = api.body(api.sent('PUT', '/v1/saved/s1').last);
    expect(body['account_number'], '0123450000');
    expect(body['passkey'], isA<Map<String, dynamic>>());

    await press(t, find.text('Bayo (rice)'));
    await press(t, find.text('Change'));
    await typeIn(t, field('Title'), 'Bayo Rice');
    await press(t, find.text('Save changes'));
    expect(api.sent('POST', '/v1/saved/step-up/options'), hasLength(1));
    final title = api.body(api.sent('PUT', '/v1/saved/s1').last);
    expect(title.containsKey('passkey'), isFalse);
    expect(title.containsKey('account_number'), isFalse);
    expect(title['title'], 'Bayo Rice');
    await drain(t);
  });

  testWidgets('an owner with no payments account opens a Taskiem wallet from Saved; staff are not offered it', (t) async {
    phone(t);
    final api = business(provider: {
      'connected': null,
      'owner': true,
      'wallet': {'available': true, 'plan': true},
      'providers': [
        {'id': 'paystack', 'name': 'Paystack'},
        {'id': 'anchor', 'name': 'Anchor'},
      ],
    });
    api.routes['POST /v1/settings/receive-money'] = (_) => answer({'on': true}, 201);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    expect(find.text('Check names with the bank'), findsOneWidget);
    expect(find.text('Paystack, Anchor'), findsOneWidget);
    // The save form says the name is not checked until one is connected.
    await press(t, find.text('Save something'));
    await press(t, find.text('A bank account'));
    expect(find.text('The bank’s name is not checked yet'), findsOneWidget);
    await t.tapAt(const Offset(5, 5));
    await settle(t);
    await press(t, find.text('Open a Taskiem wallet'));
    await press(t, find.text('Open my wallet'));
    expect(find.text('Give your business email and the director’s 11-digit BVN.'), findsOneWidget);
    await typeIn(t, field('Your business email'), 'ada@obi.test');
    await typeIn(t, field('The director’s BVN'), '22212345678');
    await press(t, find.text('Open my wallet'));
    expect(api.body(api.sent('POST', '/v1/settings/receive-money').last), {'business_email': 'ada@obi.test', 'bvn': '22212345678'});
    await drain(t);

    final staff = business(provider: {'connected': null, 'owner': false});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: staff.auth(personal: false), mine: false)));
    await settle(t);
    expect(find.text('Check names with the bank'), findsNothing);
    expect(find.text('Save something'), findsNothing);
    expect(find.text('The business’s owners and admins add and change them.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('a person’s own: private, and the number shown in full with a passkey', (t) async {
    phone(t);
    final was = passkeys;
    final fake = FakePasskeys();
    passkeys = fake;
    addTearDown(() => passkeys = was);
    final api = conversation(c: chat());
    api.routes['GET /v1/me/saved'] = (_) => {'saved': [saved('m1', 'mum', 'Mum', 'GTBank ••2222', owner: 'mine')], 'limit': 500};
    api.routes['POST /v1/me/saved/step-up/options'] = (_) => {'publicKey': {'challenge': 'abc', 'rpId': 'taskiem.test'}};
    api.routes['POST /v1/me/saved/m1/reveal'] = (_) => {'account_number': '0123452222'};
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: SavedAccountsPage(auth: api.auth(), mine: true)));
    await settle(t);
    expect(find.text('Only you see these. No business you work in sees this list.'), findsOneWidget);
    expect(api.sent('GET', '/v1/saved/provider'), isEmpty);
    await press(t, find.text('Mum'));
    await press(t, find.text('Show the number'));
    expect(api.body(api.sent('POST', '/v1/me/saved/step-up/options').last), {'operation': 'saved.reveal', 'target': 'm1'});
    expect(find.textContaining('0123452222'), findsOneWidget);
    await drain(t);
  });
}
