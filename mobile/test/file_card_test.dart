// Files in a chat as WhatsApp shows them (docs/spec/chat-previews-and-
// editing.md §1): a PDF with the picture of its first page, or its icon
// when it has none; a Word, Excel, PowerPoint or text document with its
// type's icon, colour and line; Taskiem's PDF viewer with its page count,
// Share and Save; a document opened in another app, or the share sheet
// when none can; the card a document shows before it is sent.

import 'dart:convert';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/file_card.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '828e255f-dc0f-4dd3-bb19-0ba2764778d3';

/// A page's picture, as the server answers ?preview=1.
Future<List<int>> _page(int w, int h) async {
  final rec = ui.PictureRecorder();
  final c = Canvas(rec);
  c.drawRect(Rect.fromLTWH(0, 0, w.toDouble(), h.toDouble()), Paint()..color = const Color(0xFFFFFFFF));
  c.drawRect(Rect.fromLTWH(40, 40, w * 0.6, 40), Paint()..color = const Color(0xFF0E1A14));
  c.drawRect(Rect.fromLTWH(40, 120, w * 0.5, 160), Paint()..color = const Color(0xFF2E7D5B));
  for (var i = 0; i < 14; i++) {
    c.drawRect(Rect.fromLTWH(40, 310.0 + i * 26, w * (0.4 + (i % 3) * 0.15), 10), Paint()..color = const Color(0xFF9AA39D));
  }
  final img = await rec.endRecording().toImage(w, h);
  return (await img.toByteData(format: ui.ImageByteFormat.png))!.buffer.asUint8List();
}

class _Pickers extends Pickers {
  const _Pickers();
  @override
  Future<PickedFile?> document() async =>
      PickedFile(bytes: Uint8List.fromList(List.filled(86016, 1)), name: 'Order form.docx', type: documentTypeOf('Order form.docx'));
}

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  /// Pumps until [done], the phone's own work (files kept, read back)
  /// given real time to finish.
  Future<void> until(WidgetTester t, bool Function() done) async {
    for (var i = 0; i < 60 && !done(); i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
      await t.pump(const Duration(milliseconds: 50));
    }
    await t.pump(const Duration(milliseconds: 300));
  }

  /// The conversation, its newest messages files of each kind; what was
  /// fetched noted.
  Future<List<String>> open(WidgetTester t, {TkMode mode = TkMode.light}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-files');
    final fetched = <String>[];
    final picture = await t.runAsync(() => _page(640, 905));
    Map<String, dynamic> msg(String id, Map<String, dynamic> a, {bool mine = false, String? body}) => {
      'id': id,
      'link': _link,
      'mine': mine,
      'kind': 'text',
      'body': ?body,
      'sender_name': 'Tunde',
      'attachment': a,
      'created_at': f.at.subtract(const Duration(minutes: 1)).toUtc().toIso8601String(),
      'read_at': f.at.toUtc().toIso8601String(),
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages' && !req.url.queryParameters.containsKey('after')) {
          final page = f.json(p);
          (page['messages'] as List).addAll([
            msg('m-pdf', {'kind': 'file', 'type': 'application/pdf', 'size': 1258291, 'name': 'Prices November.pdf', 'pages': 3, 'preview': 'ready', 'w': 640, 'h': 905}, body: 'This week\'s prices'),
            msg('m-locked', {'kind': 'file', 'type': 'application/pdf', 'size': 20480, 'name': 'Statement.pdf', 'protected': true, 'preview': 'none'}, mine: true),
            msg('m-docx', {'kind': 'file', 'type': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'size': 86016, 'name': 'Order form.docx', 'doc': 'word'}),
            msg('m-xlsx', {'kind': 'file', 'type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'size': 86016, 'name': 'Stock.xlsx', 'doc': 'excel'}, mine: true),
            msg('m-pptx', {'kind': 'file', 'type': 'application/vnd.openxmlformats-officedocument.presentationml.presentation', 'size': 2306867, 'name': 'Our shop.pptx', 'doc': 'powerpoint'}),
            msg('m-csv', {'kind': 'file', 'type': 'text/csv', 'size': 900, 'name': 'Orders.csv', 'doc': 'csv'}, mine: true),
          ]);
          return http.Response(jsonEncode(page), 200, headers: {'content-type': 'application/json'});
        }
        if (req.method == 'GET' && p.startsWith('/v1/link-chats/$_link/files/')) {
          final id = p.split('/').last;
          if (req.url.queryParameters['preview'] == '1') {
            fetched.add('$id?preview=1');
            return id == 'm-pdf' ? http.Response.bytes(picture!, 200, headers: {'content-type': 'image/webp'}) : http.Response('{"error":"none"}', 404);
          }
          fetched.add(id);
          return http.Response.bytes(utf8.encode(id == 'm-pdf' ? '%PDF-1.4 the prices' : 'PK the document'), 200);
        }
        if (req.method == 'GET') return null;
        return http.Response('{}', 200);
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    await t.pumpWidget(TaskiemApp(mode: mode, home: ConversationScreen(data: ConversationData(chats, conv, follow: false), pickers: const _Pickers())));
    await settle(t);
    await t.drag(find.byType(Scrollable).first, const Offset(0, -4000));
    await settle(t);
    return fetched;
  }

  for (final mode in TkMode.values) {
    testWidgets('a PDF with its first page or its icon, and documents by type (${mode.name})', (t) async {
      final fetched = await open(t, mode: mode);
      // The PDF: its picture across the card, from ?preview=1, and its line.
      final pdf = find.bySemanticsLabel('Prices November.pdf, PDF · 3 pages · 1.2 MB');
      await t.ensureVisible(pdf);
      await settle(t);
      expect(fetched, contains('m-pdf?preview=1'));
      final pdfCard = find.ancestor(of: find.text('Prices November.pdf'), matching: find.byType(FileCard));
      expect(find.descendant(of: pdfCard, matching: find.byType(Image)), findsOneWidget);
      expect(find.descendant(of: pdfCard, matching: find.byType(FileTypeTile)), findsNothing);
      // A protected PDF: its icon, "Protected PDF", no picture asked for.
      expect(find.bySemanticsLabel('Statement.pdf, Protected PDF · 20 KB'), findsOneWidget);
      expect(fetched, isNot(contains('m-locked?preview=1')));
      // Each document with its type's icon and its line.
      for (final (name, line, family) in [
        ('Order form.docx', 'Word · 84 KB', FileFamily.word),
        ('Stock.xlsx', 'Excel · 84 KB', FileFamily.excel),
        ('Our shop.pptx', 'PowerPoint · 2.2 MB', FileFamily.powerpoint),
        ('Orders.csv', 'CSV · 900 B', FileFamily.csv),
      ]) {
        expect(find.bySemanticsLabel('$name, $line'), findsOneWidget, reason: name);
        final card = find.ancestor(of: find.text(name), matching: find.byType(FileCard));
        final tile = t.widget<FileTypeTile>(find.descendant(of: card, matching: find.byType(FileTypeTile)));
        expect(tile.family, family);
        expect(find.descendant(of: card, matching: find.byType(Image)), findsNothing);
      }
      await t.ensureVisible(pdf);
      await t.pumpAndSettle();
      await snap(t, 'files-pdf-${mode.name}');
      await t.drag(find.byType(Scrollable).first, const Offset(0, -4000));
      await settle(t);
      await snap(t, 'files-docs-${mode.name}');
    });
  }

  for (final mode in TkMode.values) {
    testWidgets('the PDF viewer: its head, Page 1 of 3, Share and Save; Back returns (${mode.name})', (t) async {
      final was = pdfPages;
      pdfPages = (bytes, name, onPage) {
        WidgetsBinding.instance.addPostFrameCallback((_) => onPage(1, 3));
        return Center(child: Container(width: 300, height: 424, color: const Color(0xFFFFFFFF), alignment: Alignment.topLeft, padding: const EdgeInsets.all(24), child: Text(utf8.decode(bytes))));
      };
      addTearDown(() => pdfPages = was);
      final handed = <String>[];
      final wasHand = handOffFile;
      handOffFile = (bytes, name, type, from) async => handed.add('$name $type');
      addTearDown(() => handOffFile = wasHand);
      final saved = <String>[];
      final wasSave = saveToPhone;
      saveToPhone = (bytes, name, type) async {
        saved.add(name);
        return true;
      };
      addTearDown(() => saveToPhone = wasSave);
      final fetched = await open(t, mode: mode);
      final pdf = find.bySemanticsLabel('Prices November.pdf, PDF · 3 pages · 1.2 MB');
      await t.ensureVisible(pdf);
      await t.pumpAndSettle();
      await t.tap(pdf);
      await until(t, () => find.text('Page 1 of 3').evaluate().isNotEmpty);
      expect(fetched, contains('m-pdf'));
      expect(find.byType(PdfViewerScreen), findsOneWidget);
      expect(find.text('Page 1 of 3'), findsOneWidget);
      expect(find.textContaining('Tunde · '), findsOneWidget);
      expect(find.text('%PDF-1.4 the prices'), findsOneWidget, reason: 'the kept copy, read from memory');
      await snap(t, 'files-viewer-${mode.name}');
      await t.tap(find.bySemanticsLabel('Share Prices November.pdf'));
      await settle(t);
      expect(handed, ['Prices November.pdf application/pdf']);
      await t.tap(find.bySemanticsLabel('Save Prices November.pdf to the phone'));
      await settle(t);
      expect(saved, ['Prices November.pdf']);
      await t.tap(find.bySemanticsLabel('Back to the chat'));
      await t.pumpAndSettle();
      expect(find.byType(PdfViewerScreen), findsNothing);
      expect(pdf, findsOneWidget, reason: 'back where it was');
    });
  }

  testWidgets('Save goes to the share sheet where the phone has no place to choose', (t) async {
    final wasSave = saveToPhone;
    saveToPhone = (bytes, name, type) async => false;
    addTearDown(() => saveToPhone = wasSave);
    final was = pdfPages;
    pdfPages = (bytes, name, onPage) => const SizedBox();
    addTearDown(() => pdfPages = was);
    final handed = <String>[];
    final wasHand = handOffFile;
    handOffFile = (bytes, name, type, from) async => handed.add(name);
    addTearDown(() => handOffFile = wasHand);
    await open(t);
    final pdf = find.bySemanticsLabel('Prices November.pdf, PDF · 3 pages · 1.2 MB');
    await t.ensureVisible(pdf);
    await t.pumpAndSettle();
    await t.tap(pdf);
    await until(t, () => find.byType(PdfViewerScreen).evaluate().isNotEmpty);
    await t.tap(find.bySemanticsLabel('Save Prices November.pdf to the phone'));
    await settle(t);
    expect(handed, ['Prices November.pdf']);
  });

  testWidgets('a document opens in another app; with none for it, the share sheet', (t) async {
    final opened = <String>[];
    var canOpen = true;
    final was = openWithApp;
    openWithApp = (path, type) async {
      opened.add('${path.split('/').last} $type');
      return canOpen;
    };
    addTearDown(() => openWithApp = was);
    final handed = <String>[];
    final wasHand = handOffFile;
    handOffFile = (bytes, name, type, from) async => handed.add(name);
    addTearDown(() => handOffFile = wasHand);
    await open(t);
    final doc = find.bySemanticsLabel('Order form.docx, Word · 84 KB');
    await t.ensureVisible(doc);
    await t.pumpAndSettle();
    await t.tap(doc);
    await until(t, () => opened.isNotEmpty);
    expect(opened, ['Order form.docx application/vnd.openxmlformats-officedocument.wordprocessingml.document']);
    expect(handed, isEmpty);
    canOpen = false;
    await t.tap(doc);
    await until(t, () => handed.isNotEmpty);
    expect(handed, ['Order form.docx']);
  });

  for (final mode in TkMode.values) {
    testWidgets('a document waits for its caption as the card the chat will show (${mode.name})', (t) async {
      await open(t, mode: mode);
      await t.tap(find.bySemanticsLabel('Attach'));
      await t.pumpAndSettle();
      await t.tap(find.bySemanticsLabel('Document'));
      await settle(t);
      expect(find.text('Order form.docx · Word · 84 KB'), findsOneWidget);
      expect(t.widget<FileTypeTile>(find.byType(FileTypeTile).last).family, FileFamily.word);
      await snap(t, 'files-send-${mode.name}');
    });
  }

  test('what may be sent, and the words for a file', () {
    PickedFile p(String name, String type, [int size = 10]) => PickedFile(bytes: Uint8List(size), name: name, type: type);
    expect(fileProblem(p('Order.docx', documentTypeOf('Order.docx'))), isNull);
    expect(fileProblem(p('Stock.xls', documentTypeOf('Stock.xls'))), isNull);
    expect(fileProblem(p('notes.txt', documentTypeOf('notes.txt'))), isNull);
    expect(fileProblem(p('setup.exe', 'application/x-msdownload')), contains('Taskiem can send photos, voice notes, PDFs'));
    expect(fileProblem(p('invoice.pdf.exe', 'application/pdf')), contains('Taskiem can send'));
    expect(fileProblem(p('budget.xlsm', 'application/vnd.ms-excel')), contains('Taskiem can send'));
    expect(fileProblem(p('big.pdf', 'application/pdf', 26 * 1024 * 1024)), 'This file is larger than 25 MB.');
    expect(documentTypeOf('a.zip'), '');
    expect(sizeWords(3 * 1024 * 1024), '3 MB');
    expect(fileLine(const LinkMessageAttachment(kind: 'file', type: 'application/pdf', size: 2048, pages: 1)), 'PDF · 1 page · 2 KB');
    expect(fileFamily(type: '', name: 'Deck.PPTX'), FileFamily.powerpoint);
    expect(hasPicture(const LinkMessageAttachment(kind: 'file', type: 'application/pdf', size: 1, preview: 'failed')), isFalse);
  });
}
