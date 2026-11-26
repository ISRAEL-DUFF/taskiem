// Reading account details from photos (I1; lib/chat/photo_text.dart): the
// photo read on the phone (a stand-in reader here: real ML Kit needs a
// phone), only its words sent, with from "photo". The Pay sheet filled in
// from a photo; a photo with no account; a chat photo's Pay this account
// (fetched, put in a file for a moment, read, the file deleted); Pay
// several people from a photo of a list; the lines put in reading order.

import 'dart:convert';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:image_picker/image_picker.dart' show ImageSource;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/chat/pay_many.dart';
import 'package:taskiem/chat/pay_sheet.dart';
import 'package:taskiem/chat/photo_text.dart';
import 'package:taskiem/kit/kit.dart' show TkField;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'pay_test.dart' show payment;
import 'support.dart' show Fixture, loadFonts, snap, chooseHeld, heldActions;

const _json = {'content-type': 'application/json; charset=utf-8'};

final _png = base64Decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==');

/// The phone's reader, standing in: the words it is given, and the files
/// it was asked to read (whether each was there then).
class _Reader implements PhotoTextReader {
  _Reader(this.words);
  String words;
  final read_ = <({String path, bool there, int size})>[];

  @override
  Future<String> read(String imagePath) async {
    final f = File(imagePath);
    read_.add((path: imagePath, there: f.existsSync(), size: f.existsSync() ? f.lengthSync() : 0));
    return words;
  }
}

/// A stand-in server: answers by "METHOD /path", every request kept.
class _Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  List<Map<String, dynamic>> sent(String method, String path) => [
    for (final c in calls.where((c) => c.method == method && c.url.path == path))
      c.body.isEmpty ? <String, dynamic>{} : jsonDecode(c.body) as Map<String, dynamic>,
  ];

  Auth auth() {
    final a = Auth(
      transport: Transport(
        base: 'http://localhost',
        token: 'tok',
        client: MockClient((req) async {
          calls.add(req);
          final f = routes['${req.method} ${req.url.path}'];
          if (f == null) return http.Response(jsonEncode({'error': 'not here'}), 404, headers: _json);
          final out = f(req);
          if (out is http.Response) return out;
          return http.Response(jsonEncode(out ?? {}), 200, headers: _json);
        }),
      ),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': 't1',
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': ['owner'],
      'permissions': ['money.pay'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ada Obi', 'email': 'ada@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }

  /// Nothing went up but words: no upload, no file, every body JSON.
  void sentNoPhoto() {
    for (final c in calls) {
      expect(c.url.path, isNot(contains('upload')));
      if (c.method != 'GET') {
        expect(c.headers['content-type'], contains('application/json'), reason: '${c.method} ${c.url.path}');
        expect(() => jsonDecode(c.body), returnsNormally);
      }
    }
  }
}

Map<String, dynamic> _f(String key, String label, String kind) => {'key': key, 'label': label, 'kind': kind};
final _payFor = {
  'key': 'for',
  'label': 'What is it for?',
  'kind': 'choice',
  'choices': ['a bill', 'wages', 'a delivery', 'a refund', 'stock bought', 'other'],
};
final _payAccountJson = {
  'id': 'pay.account',
  'title': 'Pay an account number',
  'line': 'Any bank: its name is checked first',
  'icon': 'money',
  'does': 'asks',
  'opens': 'form',
  'tool': 'pay',
  'confirm': 'passkey',
  'fields': [_f('number', 'What is the account number?', 'account'), _f('bank', 'Which bank is it with?', 'bank'), _f('amount', 'How much?', 'money'), _payFor],
};

/// A screen that opens [open]'s sheet over it.
class _Host extends StatefulWidget {
  const _Host(this.open);
  final Future<Object?> Function(BuildContext context) open;

  @override
  State<_Host> createState() => _HostState();
}

class _HostState extends State<_Host> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => widget.open(context));
  }

  @override
  Widget build(BuildContext context) => const SizedBox.expand();
}

void main() {
  setUpAll(loadFonts);
  setUp(() => TkField.debugHideCaret = true);

  late _Reader reader;
  late List<ImageSource> picked;
  setUp(() {
    final wasText = photoText, wasPick = pickPhotoToRead, wasDir = photoTempDir;
    reader = _Reader('');
    picked = [];
    photoText = reader;
    pickPhotoToRead = (from) async {
      picked.add(from);
      return '/phone/photos/IMG_0042.jpg';
    };
    addTearDown(() {
      photoText = wasText;
      pickPhotoToRead = wasPick;
      photoTempDir = wasDir;
    });
  });

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 600));
  }

  void phone(WidgetTester t, [double tall = 800]) {
    t.view.physicalSize = Size(360, tall);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  void banks(_Api api) {
    api.routes['GET /v1/payments/banks'] = (_) => {
      'banks': [
        {'code': '058', 'name': 'Guaranty Trust Bank', 'likely': true},
        {'code': '044', 'name': 'Access Bank'},
      ],
      'from': 'iswallet/taskiem',
    };
    api.routes['POST /v1/payments/name-check'] = (_) => {
      'account': '••6789',
      'banks': [
        {'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'account_name': 'CHIDI OKEKE'},
      ],
    };
  }

  test('the lines in reading order: top to bottom, a row’s columns left to right', () {
    expect(
      photoLinesText([
        (box: const Rect.fromLTWH(10, 60, 80, 20), text: 'Bayo'),
        (box: const Rect.fromLTWH(200, 12, 90, 20), text: '0123456789'),
        (box: const Rect.fromLTWH(10, 10, 80, 20), text: 'Chidi'),
        (box: const Rect.fromLTWH(200, 62, 90, 20), text: '45k'),
        (box: const Rect.fromLTWH(10, 110, 80, 20), text: '  '),
        (box: const Rect.fromLTWH(10, 140, 200, 20), text: 'Thank you '),
      ]),
      'Chidi 0123456789\nBayo 45k\nThank you',
    );
    expect(photoLinesText(const []), '');
  });

  testWidgets('the Pay sheet from a photo: read on the phone, only the words sent with from photo, filled in as from a message', (t) async {
    phone(t);
    final api = _Api();
    banks(api);
    reader.words = 'Acct: O123456789\nGTBank\nChidi Okafor\nN45,000';
    api.routes['POST /v1/payments/read'] = (_) => {
      'found': true,
      'account_number': '0123456789',
      'bank_name': 'Guaranty Trust Bank',
      'bank_code': '058',
      'amount_kobo': 4500000,
      'name': 'Chidi Okafor',
    };
    api.routes['POST /v1/payments'] = (_) => {'payment': payment('p1', payee: 'Account ••6789', name: 'CHIDI OKEKE', first: true)};
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: ChatAction.fromJson(_payAccountJson)))),
      ),
    );
    await settle(t);
    expect(find.text('Read on this phone, not sent.'), findsOneWidget);
    await snap(t, 'photo-pay-sheet');
    // Nothing asked of the phone until it is tapped.
    expect(picked, isEmpty);
    await t.tap(find.bySemanticsLabel(RegExp('^Fill this in from a photo')));
    await settle(t);
    expect(find.text('Take a photo'), findsOneWidget);
    expect(find.text('Read on this phone. The photo is not sent.'), findsOneWidget);
    await snap(t, 'photo-pick');
    await t.tap(find.text('Choose from your photos'));
    await settle(t);
    expect(picked, [ImageSource.gallery]);
    expect(reader.read_.single.path, '/phone/photos/IMG_0042.jpg');
    expect(api.sent('POST', '/v1/payments/read').single, {'text': 'Acct: O123456789\nGTBank\nChidi Okafor\nN45,000', 'from': 'photo'});
    // Filled in: the number, the bank, the amount; the bank checked, and
    // the photo's name in amber against the bank's.
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '0123456789');
    expect(t.widget<EditableText>(boxes.at(1)).controller.text, '45000');
    expect(find.text('Guaranty Trust Bank'), findsOneWidget);
    expect(api.sent('POST', '/v1/payments/name-check').last, {'account_number': '0123456789', 'bank_codes': ['058']});
    expect(find.text('The bank says CHIDI OKEKE'), findsOneWidget);
    expect(find.text('The photo says Chidi Okafor; the bank says CHIDI OKEKE.'), findsOneWidget);
    await snap(t, 'photo-pay-filled');
    await t.ensureVisible(find.text('a delivery'));
    await t.tap(find.text('a delivery'));
    await settle(t);
    await t.tap(find.text('Next'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments').single, {
      'account_number': '0123456789',
      'amount_kobo': 4500000,
      'bank_name': 'Guaranty Trust Bank',
      'purpose': 'delivery',
      'from': 'chat',
    });
    api.sentNoPhoto();
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a photo with no account number: said, and nothing filled in', (t) async {
    phone(t);
    final api = _Api();
    banks(api);
    reader.words = 'Happy birthday Mummy';
    api.routes['POST /v1/payments/read'] = (_) => {'found': false};
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.dark,
        home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: ChatAction.fromJson(_payAccountJson)))),
      ),
    );
    await settle(t);
    await snap(t, 'photo-pay-sheet-dark');
    await t.tap(find.bySemanticsLabel(RegExp('^Fill this in from a photo')));
    await settle(t);
    await t.tap(find.text('Take a photo'));
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 80)));
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    expect(find.text('No account number in that photo. Type it in.'), findsOneWidget);
    expect(picked, [ImageSource.camera]);
    expect(api.sent('POST', '/v1/payments/read').single['from'], 'photo');
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '');
    // A photo with no words at all is not sent.
    reader.words = '  ';
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Fill this in from a photo')));
    await settle(t);
    await t.tap(find.text('Take a photo'));
    await t.pump();
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 80)));
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    expect(find.text('No words in that photo. Type it in.'), findsOneWidget);
    expect(api.sent('POST', '/v1/payments/read'), hasLength(1));
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a photo in a customer’s chat: Pay this account reads it on the phone and opens the Pay sheet filled in', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final tmp = Directory.systemTemp.createTempSync('photo-text-');
    addTearDown(() => tmp.deleteSync(recursive: true));
    photoTempDir = () async => tmp;
    reader.words = '0123456789\nGTBank\nChidi Okafor';
    const id = '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab';
    final f = Fixture('customer-chidi');
    final reads = <Map<String, dynamic>>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: _json);
    final list = [
      ...(f.json('/v1/customer-chats/$id/messages')['messages'] as List),
      {
        'id': 'ph1',
        'mine': false,
        'kind': 'media',
        'body': 'Photo',
        'attachment': {'kind': 'photo', 'type': 'image/png', 'size': _png.length, 'name': 'account.png'},
        'read_at': null,
        'created_at': '2026-11-11T14:10:05Z',
      },
    ];
    final auth = f.auth(also: (req) async {
      final p = req.url.path;
      if (req.method == 'GET' && p == '/v1/customer-chats/$id/messages') return ok({'messages': list, 'more': false});
      if (req.method == 'GET' && p == '/v1/customer-chats/$id/files/ph1') return http.Response.bytes(_png, 200, headers: {'content-type': 'image/png'});
      if (req.method == 'GET' && p == '/v1/chat/actions') {
        return ok({
          'topics': [
            {'id': 'pay', 'name': 'Pay', 'icon': 'money', 'color': 'lime', 'actions': [_payAccountJson]},
          ],
        });
      }
      if (req.method == 'POST' && p == '/v1/payments/read') {
        reads.add(jsonDecode(req.body) as Map<String, dynamic>);
        return ok({'found': true, 'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'name': 'Chidi Okafor'});
      }
      if (req.method == 'GET' && p == '/v1/payments/banks') {
        return ok({
          'banks': [
            {'code': '058', 'name': 'Guaranty Trust Bank', 'likely': true},
          ],
          'from': 'iswallet/taskiem',
        });
      }
      if (req.method == 'POST' && p == '/v1/payments/name-check') {
        return ok({
          'account': '••6789',
          'banks': [
            {'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'account_name': 'CHIDI OKAFOR'},
          ],
        });
      }
      if (req.method != 'GET') expect(p, isNot(contains('upload')));
      return null;
    });
    // An owner who may pay.
    auth.me = Me.fromJson({...auth.me!.toJson(), 'permissions': [...auth.me!.permissions, 'money.pay']});
    final chats = ChatsData(auth, now: () => f.at)..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: CustomerScreen(data: CustomerData(chats, id, follow: false))));
    await settle(t);
    await snap(t, 'photo-chat');
    // The photo's bubble: the last from the customer (its time in it).
    await t.longPress(find.text('14:10').last);
    await settle(t);
    expect(heldActions(t), containsAll(['Save as…', 'Save the account']));
    await snap(t, 'photo-chat-menu');
    await chooseHeld(t, 'Pay this account');
    await settle(t);
    await settle(t);
    // Fetched as the chat fetches it, put in a file for a moment, read on
    // the phone, the file deleted; only the words sent.
    expect(reader.read_.single.there, isTrue);
    expect(reader.read_.single.size, _png.length);
    expect(reader.read_.single.path, startsWith(tmp.path));
    expect(File(reader.read_.single.path).existsSync(), isFalse);
    expect(reads.single, {'text': '0123456789\nGTBank\nChidi Okafor', 'from': 'photo'});
    expect(find.byType(PaySheet), findsOneWidget);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '0123456789');
    expect(find.text('Guaranty Trust Bank'), findsOneWidget);
    expect(find.text('The bank says CHIDI OKAFOR'), findsOneWidget);
    // Opened filled in: not offered again.
    expect(find.bySemanticsLabel(RegExp('^Fill this in from a photo')), findsNothing);
    await snap(t, 'photo-chat-pay');
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Pay several people from a photo of a list: the lines read as a pasted list, one not read kept to fix', (t) async {
    phone(t, 1200);
    final api = _Api();
    reader.words = 'Bayo 0123456789 45k\nNgozi 12k';
    api.routes['POST /v1/payment-batches/read'] = (_) => {
      'lines': [
        {'line': 1, 'text': 'Bayo 0123456789 45k', 'account_number': '0123456789', 'amount_kobo': 4500000, 'name': 'Bayo', 'payee': 'bayo'},
        {'line': 2, 'text': 'Ngozi 12k', 'problem': 'No account number of 10 digits on this line.'},
      ],
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => openPayMany(c, api.auth()))));
    await settle(t);
    expect(find.text('Read on this phone, not sent.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^Add people from a photo of a list')));
    await settle(t);
    await t.tap(find.text('Take a photo'));
    await settle(t);
    expect(picked, [ImageSource.camera]);
    expect(api.sent('POST', '/v1/payment-batches/read').single, {'text': 'Bayo 0123456789 45k\nNgozi 12k', 'from': 'photo'});
    expect(find.text('@bayo'), findsOneWidget);
    expect(find.text('Line 2: Ngozi 12k'), findsOneWidget);
    final box = find.descendant(of: find.byType(PayManySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(box.first).controller.text, 'Ngozi 12k');
    await snap(t, 'photo-pay-many');
    // Fixed and read again: still words from the photo.
    await t.enterText(box.first, 'Ngozi 0001112223 Opay 12k');
    await settle(t);
    await t.tap(find.text('Read the list'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/read').last['from'], 'photo');
    api.sentNoPhoto();
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('large text and what can be pressed: the Pay sheet’s From a photo, and camera or photos', (t) async {
    final api = _Api();
    banks(api);
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: ChatAction.fromJson(_payAccountJson)))),
      ),
      drive: settle,
    );
    await t.tap(find.bySemanticsLabel(RegExp('^Fill this in from a photo')));
    await settle(t);
    expect(find.text('Choose from your photos'), findsOneWidget);
    await expectPressable(t);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });
}
