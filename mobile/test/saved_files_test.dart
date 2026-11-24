// Saved files and Save as… in a person's chat with a business (saved
// objects S1; lib/chat/saved_files.dart, lib/chat/save_as.dart): Save as…
// on the business's words and on its PDF; a saved photo picked with "@"
// attached to the message; a photo saved from Saved.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/attachments.dart' show PickedFile;
import 'package:taskiem/chat/composer.dart' show Pickers;
import 'package:taskiem/chat/saved_accounts.dart';
import 'package:taskiem/chat/saved_files.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'my_chats_test.dart' show answer, chat, conversation, drain, openChat, phone, press, settle, typeIn;
import 'saved_accounts_test.dart' show field;
import 'saved_picks_test.dart' show thing;
import 'support.dart' show loadFonts;

final _png = base64Decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==');

class _Pickers extends Pickers {
  @override
  Future<PickedFile?> photo({double? side}) async => PickedFile(bytes: Uint8List.fromList(_png), name: 'shop.jpg', type: 'image/jpeg');
}

Map<String, dynamic> _upload(String name, String type) => {
  'upload': {'file': '0192e2e0-0000-7000-8000-0000000000f2', 'name': name, 'type': type, 'size': 120000},
};

void main() {
  setUpAll(loadFonts);

  final msgs = [
    {'id': 'w1', 'mine': false, 'kind': 'text', 'body': 'Or pay to 0123456789 GTBank, Ngozi', 'read_at': null, 'created_at': '2026-11-16T09:05:00Z'},
    {'id': 'f1', 'mine': false, 'kind': 'media', 'body': 'Menu', 'attachment': {'kind': 'file', 'type': 'application/pdf', 'size': 120000, 'name': 'Menu.pdf'}, 'read_at': null, 'created_at': '2026-11-16T09:06:00Z'},
  ];

  testWidgets('Save as… on the business’s words: an account read from them', (t) async {
    phone(t);
    final api = conversation();
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {'messages': msgs, 'more': false};
    api.routes['POST /v1/me/saved/read'] = (_) => {'kind': 'bank_account', 'title': 'Ngozi', 'details': {'bank_name': 'Guaranty Trust Bank', 'account_number': '0123456789'}};
    api.routes['GET /v1/me/banks'] = (_) => {'banks': [{'code': '', 'name': 'Guaranty Trust Bank'}], 'from': ''};
    await openChat(t, api);
    await t.longPress(find.text('Or pay to 0123456789 GTBank, Ngozi'));
    await settle(t);
    await press(t, find.text('Save as…'));
    final fields = [for (final e in t.widgetList<EditableText>(find.descendant(of: find.byType(SavedAccountSheet), matching: find.byType(EditableText)))) e.controller.text];
    expect(fields, containsAll(['Ngozi', '0123456789']));
    await drain(t);
  });

  testWidgets('Save the file as… on its PDF: fetched, uploaded to save, the file sheet with it', (t) async {
    phone(t);
    final api = conversation();
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {'messages': msgs, 'more': false};
    api.routes['GET /v1/me/chats/c1/files/f1'] = (_) => http.Response.bytes(utf8.encode('%PDF-1.4 x'), 200, headers: {'content-type': 'application/pdf'});
    api.routes['POST /v1/me/saved/uploads'] = (_) => answer(_upload('Menu.pdf', 'application/pdf'), 201);
    api.routes['POST /v1/me/saved'] = (r) => answer({'saved': thing('s9', 'mine', 'text', 'menu', 'Menu', 'PDF · 117 KB', {})}, 201);
    await openChat(t, api);
    await t.longPress(find.text('Menu.pdf'));
    await settle(t);
    expect(find.text('Save the words as…'), findsOneWidget);
    await press(t, find.text('Save the file as…'));
    expect(api.sent('POST', '/v1/me/saved/uploads'), hasLength(1));
    expect(find.byType(SavedFileSheet), findsOneWidget);
    expect(find.text('Menu.pdf · 117 KB'), findsOneWidget);
    await typeIn(t, field('Name to call it up by'), 'menu');
    await press(t, find.text('Save'));
    final body = api.body(api.sent('POST', '/v1/me/saved').last);
    expect(body['kind'], 'file');
    expect(body['details'], {'note': '', 'file': '0192e2e0-0000-7000-8000-0000000000f2'});
    expect(body['title'], 'Menu');
    await drain(t);
  });

  testWidgets('a saved photo from “@”: attached to the message as itself', (t) async {
    phone(t);
    final api = conversation();
    api.routes['GET /v1/me/saved'] = (_) => {
      'saved': [thing('lg', 'mine', 'file', 'logo', 'Our logo', 'Photo · 1 KB', {'file': '0192e2e0-0000-7000-8000-0000000000f1', 'file_name': 'logo.png', 'file_type': 'image/png', 'file_size': 900})],
      'limit': 500,
    };
    api.routes['GET /v1/me/saved/lg/file'] = (_) => http.Response.bytes(_png, 200, headers: {'content-type': 'image/png'});
    await openChat(t, api);
    final box = find.byType(EditableText).last;
    await t.enterText(box, '@lo');
    await settle(t);
    await press(t, find.text('@logo'));
    expect(t.widget<EditableText>(box).controller.text, '');
    expect(api.sent('GET', '/v1/me/saved/lg/file'), hasLength(1));
    expect(find.textContaining('logo.png'), findsWidgets);
    await drain(t);
  });

  testWidgets('a photo saved from Saved: chosen, checked, saved as a file', (t) async {
    phone(t, 1200);
    final api = conversation(c: chat());
    api.routes['GET /v1/me/saved'] = (_) => {'saved': <Object>[], 'limit': 500};
    api.routes['POST /v1/me/saved/uploads'] = (_) => answer(_upload('shop.jpg', 'image/jpeg'), 201);
    api.routes['POST /v1/me/saved'] = (r) => answer({'saved': thing('s9', 'mine', 'text', 'shop', 'Shop front', 'Photo · 117 KB', {})}, 201);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(), mine: true, pickers: _Pickers())));
    await settle(t);
    await press(t, find.text('Save something'));
    await press(t, find.text('A photo or document'));
    await press(t, find.text('Choose a photo or PDF'));
    await press(t, find.text('A photo'));
    expect(find.text('shop.jpg · 117 KB'), findsOneWidget);
    await looks(t, 'saved-file-sheet');
    await typeIn(t, field('Name to call it up by'), 'shop');
    await typeIn(t, field('Title'), 'Shop front');
    await press(t, find.text('Save'));
    expect(api.body(api.sent('POST', '/v1/me/saved').last), {
      'name': 'shop',
      'title': 'Shop front',
      'kind': 'file',
      'details': {'note': '', 'file': '0192e2e0-0000-7000-8000-0000000000f2'},
    });
    await drain(t);
  });
}
