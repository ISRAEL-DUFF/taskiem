// Reading photos with AI (I3; lib/chat/photo_text.dart PhotoAI,
// readPhotoWithAI): the owner's setting in Settings (owners turn it on;
// others see it, not to change); "Read it with AI" offered only when it is
// on and the phone found nothing, the same photo sent as a multipart
// `photo` part to /v1/photo-text, and its words read as the phone's are
// (from "photo"); a refusal said in a toast; off, as before (an owner told
// it can be turned on). And a photo in a chat with a linked business: Pay
// this account and Save the account. The phone's reader and the picker
// stand in (real ML Kit needs a phone).

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
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/attachments.dart' show AttachmentView;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/pay_many.dart';
import 'package:taskiem/chat/pay_sheet.dart';
import 'package:taskiem/chat/photo_text.dart';
import 'package:taskiem/chat/saved_accounts.dart' show SavedAccountSheet;
import 'package:taskiem/kit/kit.dart' show TkField;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/signed_in.dart';
import 'package:taskiem/theme/theme.dart';

import 'lock_test.dart' show FakeUnlocker;
import 'support.dart' show Fixture, loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

final _png = base64Decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==');

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

/// The phone's reader, standing in: the words it is given, and the files
/// it was asked to read (whether each was there then).
class _Reader implements PhotoTextReader {
  _Reader(this.words);
  String words;
  final read_ = <({String path, bool there})>[];

  @override
  Future<String> read(String imagePath) async {
    read_.add((path: imagePath, there: File(imagePath).existsSync()));
    return words;
  }
}

/// A stand-in server: answers by "METHOD /path", every request kept.
class _Api {
  _Api({this.roles = const ['owner'], this.on = true});
  final List<String> roles;
  bool on;
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  List<Map<String, dynamic>> sent(String method, String path) => [
    for (final c in calls.where((c) => c.method == method && c.url.path == path))
      c.body.isEmpty ? <String, dynamic>{} : jsonDecode(c.body) as Map<String, dynamic>,
  ];

  List<http.Request> uploads() => calls.where((c) => c.url.path == '/v1/photo-text').toList();

  Auth auth() {
    routes['GET /v1/settings/ai-photo-reading'] ??= (_) => {'ai_photo_reading': on, 'available': true};
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
      'roles': roles,
      'permissions': ['money.pay'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ada Obi', 'email': 'ada@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

/// The multipart upload: one `photo` part carrying [bytes].
void _carriesPhoto(http.Request r, List<int> bytes) {
  expect(r.method, 'POST');
  expect(r.headers['content-type'], startsWith('multipart/form-data'));
  final body = r.bodyBytes;
  final text = latin1.decode(body);
  expect(text, contains('name="photo"'));
  expect(text, contains('content-type: image/png'));
  expect(text, contains(latin1.decode(bytes)), reason: 'the same photo, as it is');
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
  late Directory tmp;
  late String picked;
  setUp(() {
    final wasText = photoText, wasPick = pickPhotoToRead, wasDir = photoTempDir;
    reader = _Reader('');
    tmp = Directory.systemTemp.createTempSync('photo-ai-');
    // The photo picked: a real file on the "phone", to be sent as it is.
    picked = '${tmp.path}/IMG_0042.png';
    File(picked).writeAsBytesSync(_png);
    photoText = reader;
    pickPhotoToRead = (ImageSource from) async => picked;
    photoTempDir = () async => tmp;
    addTearDown(() {
      photoText = wasText;
      pickPhotoToRead = wasPick;
      photoTempDir = wasDir;
      tmp.deleteSync(recursive: true);
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

  Future<void> fromPhoto(WidgetTester t) async {
    await t.tap(find.bySemanticsLabel(RegExp('^Fill this in from a photo')));
    await settle(t);
    await t.tap(find.text('Choose from your photos'));
    await settle(t);
  }

  Widget paySheet(_Api api, {TkMode mode = TkMode.light}) => TaskiemApp(
    mode: mode,
    home: _Host((c) => showTkSheet<Payment>(c, (_) => PaySheet(auth: api.auth(), action: ChatAction.fromJson(_payAccountJson)))),
  );

  // --- the setting ---

  Future<({Auth auth, List<(String, Object?)> puts})> settings(WidgetTester t, {required List<String> roles, required List<String> perms, bool on = false, bool available = true, TkMode mode = TkMode.light}) async {
    t.view.physicalSize = const Size(375, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final puts = <(String, Object?)>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: _json);
    final auth = f.auth(also: (req) async {
      final key = '${req.method} ${req.url.path}';
      if (key == 'GET /v1/settings/ai-photo-reading') return ok({'ai_photo_reading': on, 'available': available});
      if (key == 'PUT /v1/settings/ai-photo-reading') {
        final b = jsonDecode(req.body) as Map<String, dynamic>;
        puts.add((key, b));
        return ok({'ai_photo_reading': b['ai_photo_reading'], 'available': true});
      }
      if (key == 'GET /v1/me/whatsapp' || key == 'GET /v1/me/telegram' || key == 'GET /v1/me/slack') {
        return ok({'enabled': false, 'linked': false, 'pending': false, 'flows': false, 'pin': {'set': false}});
      }
      if (key == 'GET /v1/settings/chat-tools') return ok({'chat_tools': true});
      if (key == 'GET /v1/devices') return ok({'devices': <Object>[]});
      if (key == 'GET /v1/me/businesses') return ok({'businesses': <Object>[]});
      return null;
    });
    final me = f.json('/v1/me');
    me['roles'] = roles;
    me['permissions'] = perms;
    auth.me = Me.fromJson(me);
    final prefs = Prefs(MemoryKeyStore());
    await prefs.load();
    await t.pumpWidget(TaskiemApp(mode: mode, home: SignedInScreen(auth: auth, prefs: prefs, unlocker: FakeUnlocker())));
    await settle(t);
    return (auth: auth, puts: puts);
  }

  for (final mode in [TkMode.light, TkMode.dark]) {
    testWidgets('the setting: an owner turns Read handwriting with AI on, beside chat tools (${mode.name})', (t) async {
      final s = await settings(t, roles: ['owner'], perms: ['member.manage', 'money.pay'], mode: mode);
      final row = find.text('Read handwriting with AI');
      expect(row, findsOneWidget);
      expect(find.text('Chat tools'), findsOneWidget);
      expect(find.text('Photos this phone cannot read'), findsOneWidget);
      expect(find.textContaining('The photo is not kept, and its words go back only to whoever asked. It uses this month’s AI allowance.'), findsOneWidget);
      await t.ensureVisible(row);
      await t.pump();
      await snap(t, 'ai-setting-owner-${mode.name}');
      await t.tap(find.bySemanticsLabel('Read handwriting with AI').last);
      await settle(t);
      expect(s.puts.single.$1, 'PUT /v1/settings/ai-photo-reading');
      expect(s.puts.single.$2, {'ai_photo_reading': true});
      expect(find.text('Saved'), findsNWidgets(2), reason: 'the Saved section, and the toast');
      await t.pumpWidget(const SizedBox());
      await t.pump(const Duration(seconds: 5));
    });
  }

  testWidgets('the setting: anyone else sees it and cannot change it; not shown where it cannot be done', (t) async {
    final s = await settings(t, roles: ['staff'], perms: ['money.pay'], on: true);
    expect(find.text('Read handwriting with AI'), findsOneWidget);
    expect(find.text('Only an owner can change this'), findsOneWidget);
    // The chat tools are not theirs: its heading comes with this row.
    expect(find.text('Chat tools'), findsNothing);
    expect(find.text('For everyone here'), findsOneWidget);
    await t.ensureVisible(find.text('Read handwriting with AI'));
    await t.pump();
    await snap(t, 'ai-setting-staff');
    await t.tap(find.text('Read handwriting with AI'));
    await settle(t);
    expect(s.puts, isEmpty);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));

    await settings(t, roles: ['owner'], perms: ['member.manage'], available: false);
    expect(find.text('Read handwriting with AI'), findsNothing);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  // --- the Pay sheet ---

  for (final mode in [TkMode.light, TkMode.dark]) {
    testWidgets('the Pay sheet: the phone finds no account, Read it with AI sends the same photo, and its words fill it in (${mode.name})', (t) async {
      phone(t);
      final api = _Api();
      banks(api);
      reader.words = 'Chd 0l23 4S6 7B9';
      const aiWords = 'Chidi Okafor\n0123456789\nGTBank\nN45,000';
      api.routes['POST /v1/payments/read'] = (r) {
        final text = (jsonDecode(r.body) as Map)['text'];
        if (text != aiWords) return {'found': false};
        return {'found': true, 'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'amount_kobo': 4500000, 'name': 'Chidi Okafor'};
      };
      api.routes['POST /v1/photo-text'] = (_) => {'text': aiWords};
      await t.pumpWidget(paySheet(api, mode: mode));
      await settle(t);
      // Nothing asked of the setting until the phone has found nothing.
      expect(api.sent('GET', '/v1/settings/ai-photo-reading'), isEmpty);
      await fromPhoto(t);
      expect(reader.read_.single.path, picked);
      expect(api.sent('POST', '/v1/payments/read').single, {'text': 'Chd 0l23 4S6 7B9', 'from': 'photo'});
      expect(find.text('No account number found'), findsOneWidget);
      expect(find.text(photoAINote), findsOneWidget);
      expect(find.text('Read it with AI'), findsOneWidget);
      expect(api.uploads(), isEmpty, reason: 'nothing sent until it is tapped');
      await snap(t, 'ai-pay-sheet-offer-${mode.name}');
      await t.tap(find.text('Read it with AI'));
      await settle(t);
      _carriesPhoto(api.uploads().single, _png);
      expect(api.sent('POST', '/v1/payments/read').last, {'text': aiWords, 'from': 'photo'});
      final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
      expect(t.widget<EditableText>(boxes.at(0)).controller.text, '0123456789');
      expect(t.widget<EditableText>(boxes.at(1)).controller.text, '45000');
      expect(find.text('Read it with AI'), findsNothing);
      expect(find.text('The photo says Chidi Okafor; the bank says CHIDI OKEKE.'), findsOneWidget);
      // Asked once for this sheet.
      expect(api.sent('GET', '/v1/settings/ai-photo-reading'), hasLength(1));
      await snap(t, 'ai-pay-sheet-filled-${mode.name}');
      await t.pumpWidget(const SizedBox());
      await t.pump(const Duration(seconds: 5));
    });
  }

  testWidgets('the Pay sheet: the phone found the account, so no Read it with AI and the setting not asked', (t) async {
    phone(t);
    final api = _Api();
    banks(api);
    reader.words = '0123456789 GTBank';
    api.routes['POST /v1/payments/read'] = (_) => {'found': true, 'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'bank_code': '058'};
    await t.pumpWidget(paySheet(api));
    await settle(t);
    await fromPhoto(t);
    expect(find.text('Read it with AI'), findsNothing);
    expect(api.sent('GET', '/v1/settings/ai-photo-reading'), isEmpty);
    expect(api.uploads(), isEmpty);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the Pay sheet, off: as before, and an owner told it can be turned on; staff are not', (t) async {
    phone(t);
    final api = _Api(on: false);
    banks(api);
    reader.words = 'Happy birthday Mummy';
    api.routes['POST /v1/payments/read'] = (_) => {'found': false};
    await t.pumpWidget(paySheet(api));
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Fill this in from a photo')));
    await settle(t);
    await t.tap(find.text('Take a photo'));
    await t.pump();
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 120)));
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    expect(find.text('No account number in that photo. Type it in. Handwriting? You can turn on Read handwriting with AI in Settings.'), findsOneWidget);
    expect(find.text('Read it with AI'), findsNothing);
    expect(api.uploads(), isEmpty);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));

    final staff = _Api(on: false, roles: ['staff']);
    banks(staff);
    staff.routes['POST /v1/payments/read'] = (_) => {'found': false};
    await t.pumpWidget(paySheet(staff));
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Fill this in from a photo')));
    await settle(t);
    await t.tap(find.text('Take a photo'));
    await t.pump();
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 120)));
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    expect(find.text('No account number in that photo. Type it in.'), findsOneWidget);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a refusal from the AI reading: the server’s words in a toast, and nothing filled in', (t) async {
    phone(t);
    final api = _Api();
    banks(api);
    reader.words = '';
    api.routes['POST /v1/payments/read'] = (_) => {'found': false};
    api.routes['POST /v1/photo-text'] = (_) => http.Response(
      jsonEncode({'error': 'This month’s AI budget is used up: type the details in.', 'code': 'budget'}),
      409,
      headers: _json,
    );
    await t.pumpWidget(paySheet(api));
    await settle(t);
    await fromPhoto(t);
    // No words at all: not sent to be read, and offered.
    expect(api.sent('POST', '/v1/payments/read'), isEmpty);
    expect(find.text('No words found'), findsOneWidget);
    await t.tap(find.text('Read it with AI'));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await snap(t, 'ai-refused');
    expect(find.text('This month’s AI budget is used up: type the details in.'), findsOneWidget);
    _carriesPhoto(api.uploads().single, _png);
    expect(api.sent('POST', '/v1/payments/read'), isEmpty);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '');
    // Still offered, to try again.
    expect(find.text('Read it with AI'), findsOneWidget);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the picked photo is deleted once read; a long list cut short is said; a photo over 5 MB is not sent', (t) async {
    phone(t);
    final api = _Api();
    banks(api);
    reader.words = '';
    const aiWords = '0123456789\nGTBank';
    api.routes['POST /v1/payments/read'] = (r) => (jsonDecode(r.body) as Map)['text'] == aiWords
        ? {'found': true, 'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'bank_code': '058'}
        : {'found': false};
    api.routes['POST /v1/photo-text'] = (_) => {'text': aiWords, 'truncated': true};
    await t.pumpWidget(paySheet(api));
    await settle(t);
    await fromPhoto(t);
    expect(reader.read_.single.path, picked);
    // Read on the phone: the picker's copy is gone, the photo kept in memory.
    expect(File(picked).existsSync(), isFalse);
    await t.tap(find.text('Read it with AI'));
    await settle(t);
    _carriesPhoto(api.uploads().single, _png);
    expect(find.text(photoListLong), findsOneWidget);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '0123456789');
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));

    // Over the model's 5 MB: refused in words, nothing sent.
    final api2 = _Api();
    banks(api2);
    api2.routes['POST /v1/payments/read'] = (_) => {'found': false};
    api2.routes['POST /v1/photo-text'] = (_) => {'text': aiWords};
    File(picked).writeAsBytesSync([..._png, ...List<int>.filled(maxAIPhotoBytes, 0)]);
    await t.pumpWidget(paySheet(api2));
    await settle(t);
    await fromPhoto(t);
    await t.tap(find.text('Read it with AI'));
    await settle(t);
    expect(api2.uploads(), isEmpty);
    expect(find.text(photoTooLarge), findsOneWidget);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  // --- Pay several ---

  testWidgets('Pay several from a photo of a list: no line read, Read it with AI, its lines read with from photo', (t) async {
    phone(t, 1200);
    final api = _Api();
    reader.words = 'Bay0 Ol2E 4sk';
    const aiWords = 'Bayo 0123456789 45k';
    api.routes['POST /v1/payment-batches/read'] = (r) {
      final text = (jsonDecode(r.body) as Map)['text'];
      if (text != aiWords) {
        return {
          'lines': [
            {'line': 1, 'text': text, 'problem': 'No account number of 10 digits on this line.'},
          ],
        };
      }
      return {
        'lines': [
          {'line': 1, 'text': aiWords, 'account_number': '0123456789', 'amount_kobo': 4500000, 'name': 'Bayo', 'payee': 'bayo'},
        ],
      };
    };
    api.routes['POST /v1/photo-text'] = (_) => {'text': aiWords};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => openPayMany(c, api.auth()))));
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Add people from a photo of a list')));
    await settle(t);
    await t.tap(find.text('Take a photo'));
    await settle(t);
    expect(api.sent('POST', '/v1/payment-batches/read').single, {'text': 'Bay0 Ol2E 4sk', 'from': 'photo'});
    expect(find.text('No line of the list read'), findsOneWidget);
    await snap(t, 'ai-pay-many-offer');
    await t.tap(find.text('Read it with AI'));
    await settle(t);
    _carriesPhoto(api.uploads().single, _png);
    expect(api.sent('POST', '/v1/payment-batches/read').last, {'text': aiWords, 'from': 'photo'});
    expect(find.text('@bayo'), findsOneWidget);
    expect(find.text('Read it with AI'), findsNothing);
    await snap(t, 'ai-pay-many-read');
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  // --- a chat with a linked business ---

  testWidgets('a photo in a linked business’s chat: Save the account and Pay this account, read on the phone (with AI when it found nothing)', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: _json);
    final list = [
      ...(f.json('/v1/link-chats/$_link/messages')['messages'] as List),
      {
        'id': 'ph1',
        'link': _link,
        'mine': false,
        'kind': 'text',
        'sender_name': 'Funke',
        'body': '',
        'attachment': {'kind': 'photo', 'type': 'image/png', 'size': _png.length, 'name': 'account.png'},
        'created_at': f.at.subtract(const Duration(minutes: 1)).toIso8601String(),
      },
    ];
    final asked = <String, List<Object?>>{};
    final uploads = <http.Request>[];
    var fetched = 0;
    final auth = f.auth(also: (req) async {
      final p = req.url.path;
      final key = '${req.method} $p';
      if (key == 'GET /v1/link-chats/$_link/messages') return ok({'messages': list});
      if (key == 'GET /v1/link-chats/$_link/files/ph1') {
        fetched++;
        return http.Response.bytes(_png, 200, headers: {'content-type': 'image/png'});
      }
      if (key == 'GET /v1/settings/ai-photo-reading') return ok({'ai_photo_reading': true, 'available': true});
      if (key == 'POST /v1/photo-text') {
        uploads.add(req);
        return ok({'text': '0123456789\nGTBank\nChidi Okafor'});
      }
      if (key == 'GET /v1/chat/actions') {
        return ok({
          'topics': [
            {'id': 'pay', 'name': 'Pay', 'icon': 'money', 'color': 'lime', 'actions': [_payAccountJson]},
          ],
        });
      }
      if (key == 'POST /v1/me/saved/read') {
        (asked[key] ??= []).add(jsonDecode(req.body));
        return ok({'kind': 'bank_account', 'title': 'Chidi Okafor', 'details': {'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'account_name': 'Chidi Okafor'}});
      }
      if (key == 'POST /v1/payments/read') {
        (asked[key] ??= []).add(jsonDecode(req.body));
        return ok({'found': true, 'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'name': 'Chidi Okafor'});
      }
      if (key == 'GET /v1/payments/banks') {
        return ok({
          'banks': [
            {'code': '058', 'name': 'Guaranty Trust Bank', 'likely': true},
          ],
          'from': 'iswallet/taskiem',
        });
      }
      if (key == 'POST /v1/payments/name-check') {
        return ok({
          'account': '••6789',
          'banks': [
            {'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'account_name': 'CHIDI OKAFOR'},
          ],
        });
      }
      return null;
    });
    // An owner who may pay and manage the business's saved things.
    auth.me = Me.fromJson({...auth.me!.toJson(), 'permissions': [...auth.me!.permissions, 'money.pay', 'saved.manage']});
    final chats = ChatsData(auth, now: () => f.at);
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: Builder(
          builder: (context) => ChatsScreen(
            data: chats,
            onOpen: (c) => Navigator.of(context).push(
              PageRouteBuilder<void>(transitionDuration: Duration.zero, pageBuilder: (_, _, _) => ConversationScreen(data: ConversationData(chats, c, follow: false))),
            ),
          ),
        ),
      ),
    );
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    await settle(t);
    final photo = find.byType(AttachmentView);
    expect(photo, findsOneWidget);
    await t.longPress(photo, warnIfMissed: false);
    await settle(t);
    expect(find.text('Pay this account'), findsOneWidget);
    expect(find.text('Save the account'), findsOneWidget);
    await snap(t, 'ai-link-chat-menu');

    // Save the account: fetched with the session, read on the phone, the
    // file deleted, the words read with from photo.
    reader.words = 'Chidi Okafor 0123456789 GTBank';
    final before = fetched;
    await t.tap(find.text('Save the account'));
    await settle(t);
    await settle(t);
    expect(fetched, before + 1);
    expect(reader.read_.last.there, isTrue);
    expect(reader.read_.last.path, startsWith(tmp.path));
    expect(File(reader.read_.last.path).existsSync(), isFalse);
    expect(asked['POST /v1/me/saved/read'], [
      {'text': 'Chidi Okafor 0123456789 GTBank', 'from': 'photo'},
    ]);
    expect(find.byType(SavedAccountSheet), findsOneWidget);
    await snap(t, 'ai-link-chat-save');
    Navigator.of(t.element(find.byType(SavedAccountSheet))).pop();
    await settle(t);

    // Pay this account: the phone reads no words; Read it with AI sends
    // the same photo, and its words open the Pay sheet filled in.
    reader.words = '';
    await t.longPress(photo, warnIfMissed: false);
    await settle(t);
    await t.tap(find.text('Pay this account'));
    await settle(t);
    await settle(t);
    expect(find.text('This phone read no words in it'), findsOneWidget);
    expect(find.text(photoAINote), findsOneWidget);
    await snap(t, 'ai-link-chat-offer');
    await t.tap(find.text('Read it with AI'));
    await settle(t);
    await settle(t);
    _carriesPhoto(uploads.single, _png);
    expect(asked['POST /v1/payments/read'], [
      {'text': '0123456789\nGTBank\nChidi Okafor', 'from': 'photo'},
    ]);
    expect(find.byType(PaySheet), findsOneWidget);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '0123456789');
    await snap(t, 'ai-link-chat-pay');
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });
}
