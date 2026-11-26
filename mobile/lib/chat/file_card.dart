// A chat's PDF or document as WhatsApp shows it
// (docs/spec/chat-previews-and-editing.md §1; the web's ChatFiles.tsx and
// lib/chatfiles.ts): a PDF with the picture of its first page, its name and
// "PDF · 3 pages · 1.2 MB", opening in Taskiem's PDF viewer; a Word, Excel
// or other document with its type's icon and colour, opening in another
// app on the phone, or the share sheet when none can.

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/widgets.dart';
import 'package:open_filex/open_filex.dart';
import 'package:path_provider/path_provider.dart';
import 'package:pdfrx/pdfrx.dart';

import '../api/api.g.dart';
import '../app/lock.dart' show stepOut;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/modal.dart' show showTkToast;
import '../theme/look.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'thread.dart' show ChatHeadBar;
import 'words.dart';

/// A file's family, for its icon and colour (chatfiles.ts fileFamily).
enum FileFamily { pdf, word, excel, powerpoint, opendocument, csv, text, file }

const _byExt = {
  'pdf': FileFamily.pdf,
  'doc': FileFamily.word,
  'docx': FileFamily.word,
  'xls': FileFamily.excel,
  'xlsx': FileFamily.excel,
  'ppt': FileFamily.powerpoint,
  'pptx': FileFamily.powerpoint,
  'odt': FileFamily.opendocument,
  'ods': FileFamily.opendocument,
  'odp': FileFamily.opendocument,
  'csv': FileFamily.csv,
  'txt': FileFamily.text,
};

/// The family of a file: the server's word for it, else its type, else
/// its name's ending (a file still being sent).
FileFamily fileFamily({required String type, String? doc, String? name}) {
  switch (doc) {
    case 'word':
      return FileFamily.word;
    case 'excel':
      return FileFamily.excel;
    case 'powerpoint':
      return FileFamily.powerpoint;
    case 'opendocument':
      return FileFamily.opendocument;
    case 'csv':
      return FileFamily.csv;
    case 'text':
      return FileFamily.text;
  }
  final t = type.split(';').first.trim().toLowerCase();
  if (t == 'application/pdf') return FileFamily.pdf;
  if (t.contains('wordprocessingml') || t == 'application/msword') return FileFamily.word;
  if (t.contains('spreadsheetml') || t == 'application/vnd.ms-excel') return FileFamily.excel;
  if (t.contains('presentationml') || t == 'application/vnd.ms-powerpoint') return FileFamily.powerpoint;
  if (t.startsWith('application/vnd.oasis.opendocument')) return FileFamily.opendocument;
  if (t == 'text/csv') return FileFamily.csv;
  if (t == 'text/plain') return FileFamily.text;
  final ext = (name ?? '').toLowerCase().split('.').last;
  return _byExt[ext] ?? FileFamily.file;
}

const _familyWords = {
  FileFamily.pdf: 'PDF',
  FileFamily.word: 'Word',
  FileFamily.excel: 'Excel',
  FileFamily.powerpoint: 'PowerPoint',
  FileFamily.opendocument: 'OpenDocument',
  FileFamily.csv: 'CSV',
  FileFamily.text: 'Text',
  FileFamily.file: 'File',
};

/// "84 KB", "1.2 MB" (chatfiles.ts sizeWords).
String sizeWords(int bytes) {
  if (bytes < 1024) return '$bytes B';
  if (bytes < 1024 * 1024) return '${(bytes / 1024).round()} KB';
  final mb = (bytes / (1024 * 1024)).toStringAsFixed(1);
  return '${mb.endsWith('.0') ? mb.substring(0, mb.length - 2) : mb} MB';
}

/// The line under a file's name: "PDF · 3 pages · 1.2 MB", "Protected
/// PDF · 1.2 MB", "Excel · 84 KB".
String fileLine(LinkMessageAttachment a) {
  final f = fileFamily(type: a.type, doc: a.doc, name: a.name);
  final parts = <String>[];
  final protected = a.protected ?? false;
  parts.add(f == FileFamily.pdf && protected ? 'Protected PDF' : _familyWords[f]!);
  final pages = a.pages;
  if (f == FileFamily.pdf && !protected && pages != null && pages > 0) parts.add(pages == 1 ? '1 page' : '$pages pages');
  parts.add(sizeWords(a.size));
  return parts.join(' · ');
}

/// A PDF shows its first page's picture unless it has none.
bool hasPicture(LinkMessageAttachment a) =>
    fileFamily(type: a.type, doc: a.doc, name: a.name) == FileFamily.pdf && !(a.protected ?? false) && a.preview != 'none' && a.preview != 'failed';

/// A type's colour: Word blue, Excel green, PowerPoint orange, PDF red,
/// others grey (the web's --doc-* tokens).
Color familyColor(TkTokens t, FileFamily f) => switch (f) {
  FileFamily.pdf => t.docPdf,
  FileFamily.word => t.docWord,
  FileFamily.excel => t.docExcel,
  FileFamily.powerpoint => t.docPowerpoint,
  _ => t.docOther,
};

/// `.file-type`: the type's icon in its colour, on a tint of it.
class FileTypeTile extends StatelessWidget {
  const FileTypeTile(this.family, {super.key, this.size = 36});
  final FileFamily family;
  final double size;

  @override
  Widget build(BuildContext context) {
    final c = familyColor(TkTheme.tokens(context), family);
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: c.withValues(alpha: 0.14), borderRadius: BorderRadius.circular(8)),
      alignment: Alignment.center,
      child: TkIcon('file', size: size * 0.6, color: c),
    );
  }
}

/// The documents a chat takes, as the phone's file picker asks for them.
const documentTypeGroups = [
  XTypeGroup(label: 'PDF', extensions: ['pdf'], mimeTypes: ['application/pdf'], uniformTypeIdentifiers: ['com.adobe.pdf']),
  XTypeGroup(
    label: 'Documents',
    extensions: ['doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'odt', 'ods', 'odp', 'csv', 'txt'],
    mimeTypes: [
      'application/msword',
      'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      'application/vnd.ms-excel',
      'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      'application/vnd.ms-powerpoint',
      'application/vnd.openxmlformats-officedocument.presentationml.presentation',
      'application/vnd.oasis.opendocument.text',
      'application/vnd.oasis.opendocument.spreadsheet',
      'application/vnd.oasis.opendocument.presentation',
      'text/csv',
      'text/plain',
    ],
    uniformTypeIdentifiers: [
      'com.microsoft.word.doc',
      'org.openxmlformats.wordprocessingml.document',
      'com.microsoft.excel.xls',
      'org.openxmlformats.spreadsheetml.sheet',
      'com.microsoft.powerpoint.ppt',
      'org.openxmlformats.presentationml.presentation',
      'org.oasis-open.opendocument.text',
      'org.oasis-open.opendocument.spreadsheet',
      'org.oasis-open.opendocument.presentation',
      'public.comma-separated-values-text',
      'public.plain-text',
    ],
  ),
];

const _typeOfExt = {
  'pdf': 'application/pdf',
  'doc': 'application/msword',
  'docx': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'xls': 'application/vnd.ms-excel',
  'xlsx': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  'ppt': 'application/vnd.ms-powerpoint',
  'pptx': 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  'odt': 'application/vnd.oasis.opendocument.text',
  'ods': 'application/vnd.oasis.opendocument.spreadsheet',
  'odp': 'application/vnd.oasis.opendocument.presentation',
  'csv': 'text/csv',
  'txt': 'text/plain',
};

/// A picked document's type by its name's ending (the server reads the
/// real one from its bytes); '' for a kind a chat does not take.
String documentTypeOf(String name) => _typeOfExt[name.toLowerCase().split('.').last] ?? '';

/// Opens a document in another app on the phone ("Open with"); false when
/// none can. A test stands in for it.
typedef FileOpener = Future<bool> Function(String path, String type);

FileOpener openWithApp = (path, type) async {
  final r = await stepOut(() => OpenFilex.open(path, type: type));
  return r.type == ResultType.done;
};

/// Saves a file where the person chooses (Downloads, Files); false when
/// the phone offers no place, and the share sheet is offered instead. A
/// test stands in for it.
typedef FileSaver = Future<bool> Function(Uint8List bytes, String name, String type);

FileSaver saveToPhone = (bytes, name, type) async {
  try {
    final at = await stepOut(() => getSaveLocation(suggestedName: name));
    if (at == null) return true; // the person chose not to
    await XFile.fromData(bytes, mimeType: type, name: name).saveTo(at.path);
    return true;
  } on UnimplementedError {
    return false;
  }
};

/// Draws a PDF's pages: swiped and scrolled, pinched and double-tapped to
/// zoom, saying each page as it comes. A test stands in for it (PDFium is
/// not there in a widget test).
typedef PdfPagesBuilder = Widget Function(Uint8List bytes, String name, void Function(int page, int pages) onPage);

PdfPagesBuilder pdfPages = (bytes, name, onPage) => PdfViewer.data(
  bytes,
  sourceName: name,
  params: PdfViewerParams(
    backgroundColor: const Color(0x00000000),
    onViewerReady: (doc, controller) => onPage(controller.pageNumber ?? 1, doc.pages.length),
    onPageChanged: (n) => onPage(n ?? 1, -1),
  ),
);

/// A chat's PDF or document in its bubble.
class FileCard extends StatelessWidget {
  const FileCard({super.key, required this.m, required this.files, required this.other, required this.room, this.local});
  final LinkMessage m;
  final ChatFiles files;

  /// Who the chat is with, for "From Ada" in the viewer.
  final String other;

  /// The width the bubble's content may take.
  final double room;

  /// Still being sent: this phone's own copy.
  final PickedFile? local;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = m.attachment!;
    final family = fileFamily(type: a.type, doc: a.doc, name: a.name);
    final name = a.name ?? attachmentLabel(a);
    final width = room < 280 ? room : 280.0;
    final pdf = family == FileFamily.pdf;
    Widget card(Uint8List? picture) => Container(
      width: width,
      clipBehavior: Clip.antiAlias,
      decoration: BoxDecoration(color: t.panel2, borderRadius: BorderRadius.circular(8)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          if (picture != null)
            // `.file-picture`: the first page across the card, its top.
            Container(
              constraints: const BoxConstraints(maxHeight: 200),
              decoration: BoxDecoration(color: t.panel, border: Border(bottom: BorderSide(color: t.border))),
              child: ClipRect(
                child: Align(
                  alignment: Alignment.topCenter,
                  heightFactor: 1,
                  child: Image.memory(picture, width: width, fit: BoxFit.fitWidth, alignment: Alignment.topCenter, gaplessPlayback: true, excludeFromSemantics: true),
                ),
              ),
            ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
            child: Row(
              children: [
                if (picture == null) ...[FileTypeTile(family), const SizedBox(width: 10)],
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(name, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 600, color: t.text)),
                      const SizedBox(height: 1),
                      Text(fileLine(a), style: tkText(size: 12, color: m.mine ? t.chatOutMeta : t.muted)),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
    final body = local != null || !pdf || !hasPicture(a)
        ? card(null)
        : ListenableBuilder(listenable: files, builder: (context, _) => card(files.previewOf(m)));
    if (local != null) return Padding(padding: const EdgeInsets.only(top: 2, bottom: 4), child: body);
    return Padding(
      padding: const EdgeInsets.only(top: 2, bottom: 4),
      child: Builder(
        builder: (context) {
          Rect from() {
            final box = context.findRenderObject() as RenderBox?;
            return box == null ? Rect.zero : box.localToGlobal(Offset.zero) & box.size;
          }

          void open() => pdf ? _openPdf(context, a, name) : _openDocument(context, a, name, from());
          return TapArea(
            child: Semantics(
              button: true,
              label: '$name, ${fileLine(a)}',
              onTapHint: pdf ? 'Open it' : 'Open it in another app',
              onTap: open,
              excludeSemantics: true,
              child: GestureDetector(onTap: open, child: body),
            ),
          );
        },
      ),
    );
  }

  Future<Uint8List?> _bytes(BuildContext context) async {
    final f = files.of(m) ?? await files.fetch(m);
    if (f == null && context.mounted) showTkToast(context, 'The file didn\'t load. Try again.', error: true);
    return f?.bytes;
  }

  Future<void> _openPdf(BuildContext context, LinkMessageAttachment a, String name) async {
    final bytes = await _bytes(context);
    if (bytes == null || !context.mounted) return;
    await Navigator.of(context).push<void>(
      PageRouteBuilder<void>(
        transitionDuration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 160),
        reverseTransitionDuration: Duration.zero,
        pageBuilder: (context, _, _) => PdfViewerScreen(
          bytes: bytes,
          name: name,
          type: a.type,
          from: m.mine ? 'You' : (m.senderName != null && m.senderName!.isNotEmpty ? m.senderName! : other),
          at: m.createdAt,
        ),
        transitionsBuilder: (context, anim, _, child) => FadeTransition(opacity: anim, child: child),
      ),
    );
  }

  /// A document: from the kept copy, written where the other app can read
  /// it, and opened there; the share sheet when no app can.
  Future<void> _openDocument(BuildContext context, LinkMessageAttachment a, String name, Rect from) async {
    final bytes = await _bytes(context);
    if (bytes == null) return;
    final opened = await openDocumentBytes(bytes, name, a.type);
    if (!opened) await handOffFile(bytes, name, a.type, from);
  }
}

/// Writes a document to the app's own place for files it hands on, and
/// opens it in another app; false when none can.
Future<bool> openDocumentBytes(Uint8List bytes, String name, String type) async {
  try {
    Directory base;
    try {
      base = await getTemporaryDirectory();
    } on Object {
      base = Directory.systemTemp; // no app folder to ask for (a test)
    }
    final dir = Directory('${base.path}/open');
    await dir.create(recursive: true);
    final safe = name.replaceAll(RegExp(r'[/\\]'), '_');
    final file = File('${dir.path}/$safe');
    await file.writeAsBytes(bytes, flush: true);
    return await openWithApp(file.path, type);
  } on Object {
    return false;
  }
}

/// Taskiem's PDF viewer: the whole screen, the file's name and who sent it
/// when in its head; the pages to swipe, pinch and double-tap, "Page 2 of
/// 3"; Share and Save. Back returns to the chat where it was. The PDF is
/// read from memory (the chat's kept copy), never written anywhere else.
class PdfViewerScreen extends StatefulWidget {
  const PdfViewerScreen({super.key, required this.bytes, required this.name, required this.type, required this.from, required this.at});
  final Uint8List bytes;
  final String name;
  final String type;
  final String from;
  final DateTime at;

  @override
  State<PdfViewerScreen> createState() => _PdfViewerScreenState();
}

class _PdfViewerScreenState extends State<PdfViewerScreen> {
  int _page = 1;
  int _pages = 0;
  late final Widget _view = pdfPages(widget.bytes, widget.name, (page, pages) {
    if (!mounted) return;
    setState(() {
      _page = page;
      if (pages > 0) _pages = pages;
    });
  });

  Future<void> _share(BuildContext context) => handOffFile(widget.bytes, widget.name, widget.type, null);

  Future<void> _save(BuildContext context) async {
    final saved = await saveToPhone(widget.bytes, widget.name, widget.type);
    if (!saved) await handOffFile(widget.bytes, widget.name, widget.type, null);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final pad = MediaQuery.paddingOf(context);
    return TkAppLook(
      child: ColoredBox(
        color: TkLook.of(context).ground,
        child: Semantics(
          scopesRoute: true,
          namesRoute: true,
          explicitChildNodes: true,
          label: widget.name,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: EdgeInsets.fromLTRB(6, 8 + pad.top, 12, 6),
                child: Row(
                  children: [
                    ChatHeadBar.iconButton('Back to the chat', TkIcon('back', size: 22, color: t.text), () => Navigator.of(context).maybePop()),
                    const SizedBox(width: 4),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Semantics(
                            header: true,
                            child: Text(widget.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkDisplay(size: 18, letterSpacing: -0.3, color: t.text)),
                          ),
                          Text('${widget.from} · ${chatTimeWords(widget.at)}', maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, color: t.muted)),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
              Expanded(
                child: Stack(
                  children: [
                    Positioned.fill(child: ColoredBox(color: t.panel2, child: _view)),
                    if (_pages > 0)
                      Positioned(
                        bottom: 12,
                        left: 0,
                        right: 0,
                        child: Center(
                          child: Semantics(
                            liveRegion: true,
                            child: Container(
                              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                              decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(999), border: Border.all(color: t.border)),
                              child: Text('Page $_page of $_pages', style: tkText(size: 13, weight: 600, color: t.text)),
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
              Padding(
                padding: EdgeInsets.fromLTRB(16, 10, 16, 10 + pad.bottom),
                child: TkButtonRow([
                  TkSmallButton(label: 'Share', icon: 'b-share', semantics: 'Share ${widget.name}', onPressed: () => _share(context)),
                  TkSmallButton(label: 'Save', primary: true, semantics: 'Save ${widget.name} to the phone', onPressed: () => _save(context)),
                ]),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A short time for the viewer's head: "Today, 12:59", "26 Nov, 09:14".
String chatTimeWords(DateTime at) {
  final l = at.toLocal();
  final now = DateTime.now();
  final hm = '${l.hour.toString().padLeft(2, '0')}:${l.minute.toString().padLeft(2, '0')}';
  if (l.year == now.year && l.month == now.month && l.day == now.day) return 'Today, $hm';
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return '${l.day} ${months[l.month - 1]}, $hm';
}
