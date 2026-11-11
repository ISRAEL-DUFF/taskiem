// Sending photos, PDFs and voice notes, as a person does: chosen from the
// clip's menu, waiting for a caption, sent (a multipart form with the
// client id, shown at once with a clock); a voice note held and released
// (sent at once) or tapped (kept for Done, then sent with a caption); a
// microphone the phone refuses.

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '828e255f-dc0f-4dd3-bb19-0ba2764778d3';

class _Pickers extends Pickers {
  _Pickers(this.photoBytes);
  final Uint8List photoBytes;
  @override
  Future<PickedFile?> photo() async => PickedFile(bytes: photoBytes, name: 'tomatoes.jpg', type: 'image/jpeg');
  @override
  Future<PickedFile?> document() async => PickedFile(bytes: Uint8List.fromList(utf8.encode('%PDF-1.4 x')), name: 'List.pdf', type: 'application/pdf');
}

class _Recorder extends Recorder {
  _Recorder({this.allowed = true});
  final bool allowed;
  bool on = false;
  @override
  Future<bool> start() async => on = allowed;
  @override
  Future<PickedFile?> stop({required bool keep}) async {
    on = false;
    return keep ? PickedFile(bytes: Uint8List.fromList(List.filled(2048, 1)), name: 'Voice note.m4a', type: 'audio/mp4') : null;
  }
}

/// The phone's microphone, its start held until [go] (a slow phone, the
/// microphone question showing).
class _SlowRecorder extends Recorder {
  final go = Completer<void>();
  bool ended = false;
  @override
  Future<bool> mayRecord() async => true;
  @override
  Future<String> place() async => '/tmp/voice.m4a';
  @override
  Future<void> begin(String path) => go.future;
  @override
  Future<String?> end() async {
    ended = true;
    return path;
  }

  String get path => '/tmp/voice.m4a';
  @override
  Future<Uint8List?> take(String path, {required bool keep}) async => keep ? Uint8List.fromList(List.filled(2048, 1)) : null;
}

/// What the stand-in server was sent: each upload's fields and file.
class _Sent {
  final uploads = <({Map<String, String> fields, String filename, String type, int size})>[];
  final gate = <Completer<void>>[];
}

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  Future<(_Sent, ConversationData)> open(WidgetTester t, {Recorder? recorder, bool hold = false}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-files');
    final sent = _Sent();
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path == '/v1/link-chats/$_link/files') {
          final m = await _multipart(req);
          sent.uploads.add(m);
          if (hold) {
            final c = Completer<void>();
            sent.gate.add(c);
            await c.future;
          }
          final kind = m.type.startsWith('image/') ? 'photo' : m.type.startsWith('audio/') ? 'voice' : 'file';
          return http.Response(
            jsonEncode({
              'message': {
                'id': 'm-${sent.uploads.length}',
                'link': _link,
                'mine': true,
                'kind': 'text',
                if ((m.fields['text'] ?? '').isNotEmpty) 'body': m.fields['text'],
                'attachment': {'kind': kind, 'type': m.type, 'size': m.size, 'name': m.filename},
                'created_at': f.at.toIso8601String(),
              },
            }),
            201,
          );
        }
        if (req.method == 'POST') return http.Response('{}', 200);
        return null;
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    final data = ConversationData(chats, conv, follow: false);
    final photo = File('test/parity/fixtures/chat-files/025.img').readAsBytesSync();
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: ConversationScreen(data: data, pickers: _Pickers(photo), recorder: recorder ?? _Recorder()),
    ));
    await settle(t);
    return (sent, data);
  }

  testWidgets('a photo from the clip waits for its caption, then goes with it and the client id', (t) async {
    final (sent, data) = await open(t, hold: true);
    await t.tap(find.bySemanticsLabel('Attach'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Photo'));
    await settle(t);
    expect(find.text('Photo: tomatoes.jpg · 13 KB'), findsOneWidget);
    expect(find.bySemanticsLabel('Take a photo'), findsNothing, reason: 'the camera goes while a file waits');
    await t.enterText(find.byType(EditableText), 'For the soup');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text('Photo: tomatoes.jpg · 13 KB'), findsNothing);
    expect(find.bySemanticsLabel('Sending'), findsOneWidget, reason: 'shown at once, with a clock');
    expect(sent.uploads.single.fields['text'], 'For the soup');
    expect(sent.uploads.single.fields['client_id'], isNotEmpty);
    expect(sent.uploads.single.type, 'image/jpeg');
    sent.gate.single.complete();
    await settle(t);
    expect(find.bySemanticsLabel('Sending'), findsNothing);
    expect(data.messages!.last.attachment!.kind, 'photo');
    expect(find.bySemanticsLabel('Your photo'), findsWidgets);
  });

  testWidgets('a PDF from the clip goes as a file', (t) async {
    final (sent, _) = await open(t);
    await t.tap(find.bySemanticsLabel('Attach'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Document'));
    await settle(t);
    expect(find.text('List.pdf · 1 KB'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(sent.uploads.single.type, 'application/pdf');
    expect(sent.uploads.single.filename, 'List.pdf');
  });

  testWidgets('a voice note held and released goes at once', (t) async {
    final rec = _Recorder();
    final (sent, _) = await open(t, recorder: rec);
    final mic = find.bySemanticsLabel('Record a voice note');
    final g = await t.startGesture(t.getCenter(mic));
    await settle(t);
    expect(rec.on, isTrue);
    expect(find.textContaining('release to send, slide left to cancel', findRichText: true), findsOneWidget);
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 450)));
    await g.up();
    await settle(t);
    expect(sent.uploads.single.type, 'audio/mp4');
    expect(sent.uploads.single.fields.containsKey('text'), isFalse);
  });

  testWidgets('a voice note let go before the microphone has started: nothing sent, nothing left recording', (t) async {
    final rec = _SlowRecorder();
    final (sent, _) = await open(t, recorder: rec);
    final g = await t.startGesture(t.getCenter(find.bySemanticsLabel('Record a voice note')));
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 450)));
    await g.up();
    await settle(t);
    rec.go.complete();
    await settle(t);
    expect(sent.uploads, isEmpty);
    expect(rec.ended, isTrue, reason: 'the microphone stopped once it had started');
    expect(find.text('Recording 0:00', findRichText: true), findsNothing);
    expect(find.bySemanticsLabel('Record a voice note'), findsOneWidget);
  });

  testWidgets('a voice note held and slid left is thrown away', (t) async {
    final rec = _Recorder();
    final (sent, _) = await open(t, recorder: rec);
    final g = await t.startGesture(t.getCenter(find.bySemanticsLabel('Record a voice note')));
    await settle(t);
    await g.moveBy(const Offset(-100, 0));
    await settle(t);
    await g.up();
    await settle(t);
    expect(rec.on, isFalse);
    expect(sent.uploads, isEmpty);
    expect(find.bySemanticsLabel('Record a voice note'), findsOneWidget);
  });

  testWidgets('a voice note tapped records until Done, then waits for its caption', (t) async {
    final (sent, _) = await open(t);
    await t.tap(find.bySemanticsLabel('Record a voice note'));
    await settle(t);
    expect(find.text('Recording 0:00', findRichText: true), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Done'));
    await settle(t);
    expect(find.text('Voice note · 2 KB'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(sent.uploads.single.type, 'audio/mp4');
  });

  testWidgets('a microphone the phone refuses says so', (t) async {
    await open(t, recorder: _Recorder(allowed: false));
    await t.tap(find.bySemanticsLabel('Record a voice note'));
    await settle(t);
    expect(find.textContaining('may not use your microphone'), findsOneWidget);
    expect(find.text('Recording 0:00', findRichText: true), findsNothing);
  });

  test('what may be sent', () {
    PickedFile f(String type, int size) => PickedFile(bytes: Uint8List(size), name: 'x', type: type);
    expect(fileProblem(f('image/jpeg', 10)), isNull);
    expect(fileProblem(f('audio/mp4', 10)), isNull);
    expect(fileProblem(f('application/pdf', 10)), isNull);
    expect(fileProblem(f('image/gif', 10)), contains('JPEG, PNG or WebP'));
    expect(fileProblem(f('image/jpeg', 26 * 1024 * 1024)), contains('larger than 25 MB'));
    expect(fileSize(13050), '13 KB');
    expect(fileSize(1500000), '1.4 MB');
    expect(fileSize(10), '1 KB');
  });
}

/// Reads a multipart form as the server would: its fields and its file.
Future<({Map<String, String> fields, String filename, String type, int size})> _multipart(http.Request req) async {
  final boundary = req.headers['content-type']!.split('boundary=').last;
  final body = latin1.decode(req.bodyBytes);
  final fields = <String, String>{};
  var filename = '', type = '';
  var size = 0;
  for (final part in body.split('--$boundary')) {
    final at = part.indexOf('\r\n\r\n');
    if (at < 0) continue;
    final head = part.substring(0, at);
    final value = part.substring(at + 4, part.length - 2);
    final name = RegExp(r'name="([^"]*)"').firstMatch(head)?.group(1);
    final file = RegExp(r'filename="([^"]*)"').firstMatch(head)?.group(1);
    if (file != null) {
      filename = file;
      type = RegExp(r'content-type: ([^\r\n]*)', caseSensitive: false).firstMatch(head)?.group(1) ?? '';
      size = value.length;
    } else if (name != null) {
      fields[name] = utf8.decode(latin1.encode(value));
    }
  }
  return (fields: fields, filename: filename, type: type, size: size);
}
