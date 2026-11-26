// Links in a chat (docs/spec/chat-previews-and-editing.md §2): a link in a
// message's words opens Taskiem's browser tab; holding it offers Open, Copy
// link and Share; the first link's card in its two layouts (a wide picture
// across it, a small one in a square on its left), its picture read from
// Taskiem's own address; and the card above the box while a link is typed,
// with ✕ sending the message without it.

import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/chat_link_page.dart' show shareWords;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/link_preview.dart';
import 'package:taskiem/kit/api_image.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

/// A w by h picture, as the server answers it.
Future<List<int>> _picture(int w, int h) async {
  final rec = ui.PictureRecorder();
  final c = Canvas(rec);
  c.drawRect(Rect.fromLTWH(0, 0, w.toDouble(), h.toDouble()), Paint()..color = const Color(0xFF2E7D5B));
  c.drawCircle(Offset(w * 0.3, h * 0.5), h * 0.3, Paint()..color = const Color(0xFFF2C94C));
  final img = await rec.endRecording().toImage(w, h);
  final png = await img.toByteData(format: ui.ImageByteFormat.png);
  return png!.buffer.asUint8List();
}

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  final wide = {'url': 'https://jumia.com.ng/phones', 'host': 'jumia.com.ng', 'site': 'Jumia', 'title': 'Phones at the best prices in Nigeria', 'description': 'Shop phones online and have them delivered to your door.', 'image': {'w': 1200, 'h': 630}};
  final square = {'url': 'https://example.com/rice', 'host': 'example.com', 'title': 'Rice, 50 kg bag', 'description': 'Long grain, from Kebbi.', 'image': {'w': 300, 'h': 300}};
  final taskiem = {'url': 'https://taskiem.ng/o/ORD-9', 'host': 'taskiem.ng', 'site': 'Taskiem', 'title': 'Taskiem · Order link', 'taskiem': 'order'};

  /// The conversation with Tunde, its newest messages ours with links;
  /// what the app asks for, and sends, noted.
  Future<({List<String> pictures, List<Map<String, dynamic>> sends, List<Map<String, dynamic>> asked})> open(WidgetTester t, {TkMode mode = TkMode.light}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final pictures = <String>[];
    final sends = <Map<String, dynamic>>[];
    final asked = <Map<String, dynamic>>[];
    final big = await t.runAsync(() => _picture(1200, 630));
    final small = await t.runAsync(() => _picture(300, 300));
    http.Response answer(Object body, [int code = 200]) => http.Response(jsonEncode(body), code, headers: {'content-type': 'application/json'});
    Map<String, dynamic> msg(String id, String body, Map<String, dynamic>? preview, {bool mine = false}) => {
      'id': id,
      'link': _link,
      'mine': mine,
      'kind': 'text',
      'body': body,
      'sender_name': 'Tunde',
      'created_at': f.at.subtract(const Duration(minutes: 1)).toUtc().toIso8601String(),
      'read_at': f.at.toUtc().toIso8601String(),
      'preview': ?preview,
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages' && !req.url.queryParameters.containsKey('after')) {
          final page = f.json(p);
          (page['messages'] as List).addAll([
            msg('m-wide', 'The phones: https://jumia.com.ng/phones', wide),
            msg('m-square', 'Rice is at example.com/rice, cheaper.', square, mine: true),
            msg('m-taskiem', 'Your order: https://taskiem.ng/o/ORD-9', taskiem),
          ]);
          return answer(page);
        }
        if (req.method == 'GET' && p.startsWith('/v1/link-chats/$_link/previews/')) {
          pictures.add(p);
          return http.Response.bytes(p.endsWith('m-wide') ? big! : small!, 200, headers: {'content-type': 'image/png'});
        }
        if (req.method == 'GET' && p == '/v1/link-preview/held1/image') {
          pictures.add(p);
          return http.Response.bytes(big!, 200, headers: {'content-type': 'image/png'});
        }
        if (req.method == 'POST' && p == '/v1/link-preview') {
          asked.add(jsonDecode(req.body) as Map<String, dynamic>);
          return answer({'id': 'held1', ...wide, 'image': {'id': 'held1', 'w': 1200, 'h': 630}});
        }
        if (req.method == 'POST' && p == '/v1/link-chats/$_link/messages') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          sends.add(b);
          return answer({'message': msg('sent-${sends.length}', b['text'] as String, null, mine: true)}, 201);
        }
        if (req.method == 'GET') return null;
        return answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    await t.pumpWidget(TaskiemApp(mode: mode, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    // Down to the newest, where the links are.
    for (var i = 0; i < 6 && find.textContaining('Your order:').evaluate().isEmpty; i++) {
      await t.drag(find.byType(Scrollable).first, const Offset(0, -400));
      await settle(t);
    }
    await t.drag(find.byType(Scrollable).first, const Offset(0, -2000));
    await settle(t);
    return (pictures: pictures, sends: sends, asked: asked);
  }

  /// Taps the words [words] inside a message's text.
  Future<void> tapWords(WidgetTester t, String words, {bool hold = false}) async {
    final para = find.textContaining(words).first;
    final r = t.renderObject<RenderParagraph>(para);
    final text = r.text.toPlainText();
    final i = text.indexOf(words);
    final boxes = r.getBoxesForSelection(TextSelection(baseOffset: i, extentOffset: i + words.length));
    final at = r.localToGlobal(boxes.first.toRect().center);
    if (hold) {
      await t.longPressAt(at);
    } else {
      await t.tapAt(at);
    }
    await t.pumpAndSettle();
  }

  testWidgets('tapping a link opens the browser tab; holding it offers Open, Copy link and Share', (t) async {
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    final shared = <String>[];
    final wasShare = shareWords;
    shareWords = (w) async => shared.add(w);
    addTearDown(() => shareWords = wasShare);
    await open(t);
    // A bare address, its full stop the sentence's, opens as https.
    await tapWords(t, 'example.com/rice');
    expect(opened, [Uri.parse('https://example.com/rice')]);
    await tapWords(t, 'jumia.com.ng/phones');
    expect(opened.last, Uri.parse('https://jumia.com.ng/phones'));
    // Held: its own menu, not the message's.
    await tapWords(t, 'example.com/rice', hold: true);
    expect(find.text('Open'), findsOneWidget);
    expect(find.text('Copy link'), findsOneWidget);
    expect(find.text('Share'), findsOneWidget);
    await snap(t, 'link-hold-menu');
    String? copied;
    t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied = (call.arguments as Map)['text'] as String;
      return null;
    });
    addTearDown(() => t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, null));
    await t.tap(find.text('Copy link'));
    await t.pumpAndSettle();
    expect(copied, 'https://example.com/rice');
    await tapWords(t, 'jumia.com.ng/phones', hold: true);
    await t.tap(find.text('Share'));
    await t.pumpAndSettle();
    expect(shared, ['https://jumia.com.ng/phones']);
    await tapWords(t, 'jumia.com.ng/phones', hold: true);
    await t.tap(find.text('Open'));
    await t.pumpAndSettle();
    expect(opened.last, Uri.parse('https://jumia.com.ng/phones'));
    // The card, tapped anywhere, opens the link too.
    await t.tap(find.bySemanticsLabel('Rice, 50 kg bag, example.com'));
    await t.pumpAndSettle();
    expect(opened.last, Uri.parse('https://example.com/rice'));
  });

  for (final mode in TkMode.values) {
    testWidgets('the card\'s two layouts, its picture from Taskiem (${mode.name})', (t) async {
      await open(t, mode: mode);
      // Wide: the picture across the card, above its words.
      final wideCard = find.ancestor(of: find.text('Phones at the best prices in Nigeria'), matching: find.byType(LinkPreviewCard));
      final wideImg = find.descendant(of: wideCard, matching: find.byType(Image));
      expect(wideImg, findsOneWidget);
      final wr = t.getRect(wideImg), wc = t.getRect(wideCard);
      expect(wr.width, moreOrLessEquals(wc.width - 3, epsilon: 1), reason: 'across the card');
      expect(wr.bottom, lessThanOrEqualTo(t.getRect(find.text('Phones at the best prices in Nigeria')).top));
      // Small: a square on the left of its words.
      final sqCard = find.ancestor(of: find.text('Rice, 50 kg bag'), matching: find.byType(LinkPreviewCard));
      final sqImg = find.descendant(of: sqCard, matching: find.byType(Image));
      final sr = t.getRect(sqImg);
      expect(sr.width, 76);
      expect(sr.right, lessThanOrEqualTo(t.getRect(find.text('Rice, 50 kg bag')).left));
      // Taskiem's own link: a plain card, nothing fetched.
      expect(find.text('Taskiem · Order link'), findsOneWidget);
      // Each picture from Taskiem's own address, the chat's previews route.
      final paths = find.byType(ApiImage).evaluate().map((e) => (e.widget as ApiImage).path).toSet();
      expect(paths, {'/v1/link-chats/$_link/previews/m-wide', '/v1/link-chats/$_link/previews/m-square'});
      await t.ensureVisible(find.text('Phones at the best prices in Nigeria'));
      await t.pumpAndSettle();
      await snap(t, 'link-cards-${mode.name}');
    });
  }

  for (final mode in TkMode.values) {
    testWidgets('the card above the box while a link is typed; ✕ sends without it (${mode.name})', (t) async {
      final seen = await open(t, mode: mode);
      final box = find.byType(EditableText);
      await t.enterText(box, 'Look at https://jumia.com.ng/phones');
      await t.pump();
      expect(find.byKey(const ValueKey('composer-preview')), findsNothing, reason: 'half a second first');
      await t.pump(const Duration(milliseconds: 600));
      await settle(t);
      expect(seen.asked, [
        {'url': 'https://jumia.com.ng/phones', 'chat': 'link:$_link'},
      ]);
      expect(find.byKey(const ValueKey('composer-preview')), findsOneWidget);
      expect(find.byWidgetPredicate((w) => w is ApiImage && w.path == '/v1/link-preview/held1/image'), findsOneWidget);
      await snap(t, 'link-composer-${mode.name}');
      // Sent as shown.
      await t.testTextInput.receiveAction(TextInputAction.send);
      await t.tap(find.bySemanticsLabel('Send'));
      await settle(t);
      expect(seen.sends.single['preview'], {'id': 'held1'});
      expect(find.byKey(const ValueKey('composer-preview')), findsNothing);
      // Closed with ✕: sent without.
      await t.enterText(box, 'Again https://jumia.com.ng/phones');
      await t.pump(const Duration(milliseconds: 600));
      await settle(t);
      expect(find.byKey(const ValueKey('composer-preview')), findsOneWidget);
      await t.tap(find.bySemanticsLabel('Send without the preview'));
      await t.pump();
      expect(find.byKey(const ValueKey('composer-preview')), findsNothing);
      await t.tap(find.bySemanticsLabel('Send'));
      await settle(t);
      expect(seen.sends.last['preview'], false);
      // The link taken out of the words: no card.
      await t.enterText(box, 'https://jumia.com.ng/phones');
      await t.pump(const Duration(milliseconds: 600));
      await settle(t);
      expect(find.byKey(const ValueKey('composer-preview')), findsOneWidget);
      await t.enterText(box, 'no link now');
      await t.pump();
      expect(find.byKey(const ValueKey('composer-preview')), findsNothing);
    });
  }

  test('a picture is never wide unless it is', () {
    expect(wideImage(1200, 630), isTrue);
    expect(wideImage(300, 300), isFalse);
    expect(wideImage(380, 200), isFalse);
    expect(wideImage(null, null), isFalse);
    expect(File('lib/chat/link_preview.dart').readAsStringSync(), contains('w >= 400 && w >= h * 1.3'), reason: 'the web\'s rule');
  });
}
