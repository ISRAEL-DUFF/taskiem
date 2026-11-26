// Links in chats (docs/spec/chat-previews-and-editing.md §2), as the web
// draws them (web/src/pages/ChatLinks.tsx): every web address in a
// message's words, found by the rule every chat shares (linkify.dart),
// opens in Taskiem's browser tab; holding one offers Open, Copy link and
// Share. The first link's card sits at the top of the bubble: the page's
// picture (Taskiem's own copy, never the page's site's) across it when
// wide, or in a square on the left when small or square; its title, words
// and site. While a link is typed, its card waits above the box, with ✕ to
// send without it.

import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/links.dart' show openInBrowser;
import '../kit/api_image.dart';
import '../kit/icon.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'chat_link_page.dart' show shareWords;
import 'groups.dart' show pickAction;
import 'linkify.dart';

/// Opens a link from a chat in the browser tab; says so when it could not.
Future<void> openChatLink(BuildContext context, Uri url) => openOrSay(context, () => openInBrowser(url), 'That link could not be opened.');

/// What holding a link offers: Open, Copy link, Share.
Future<void> showLinkMenu(BuildContext context, Uri url) async {
  final pick = await pickAction(
    context,
    title: shownLink(url.toString()),
    actions: const [('open', 'Open', 'b-arrow', false), ('copy', 'Copy link', 'b-copy', false), ('share', 'Share', 'b-share', false)],
  );
  if (!context.mounted) return;
  switch (pick) {
    case 'open':
      await openChatLink(context, url);
    case 'copy':
      await Clipboard.setData(ClipboardData(text: url.toString()));
      if (context.mounted) showTkToast(context, 'Link copied.');
    case 'share':
      await shareWords(url.toString());
  }
}

/// A link's gesture in words: a tap opens it, holding it offers the menu
/// (before the bubble's own hold, which starts at the same moment but
/// later in the arena). A tap, so screen readers offer it as one.
class LinkRecognizer extends TapGestureRecognizer {
  LinkRecognizer();
  final LongPressGestureRecognizer _hold = LongPressGestureRecognizer();

  set onHold(VoidCallback? f) => _hold.onLongPress = f;

  @override
  void addAllowedPointer(PointerDownEvent event) {
    super.addAllowedPointer(event);
    _hold.addPointer(event);
  }

  @override
  void dispose() {
    _hold.dispose();
    super.dispose();
  }
}

/// Owns the gestures of the links in a text, letting go of those no longer
/// in it (as thread.dart's TapsOwner does for taps).
class LinkTapsOwner extends StatefulWidget {
  const LinkTapsOwner({super.key, required this.builder});
  final Widget Function(BuildContext context, GestureRecognizer Function(FoundLink l) link) builder;

  @override
  State<LinkTapsOwner> createState() => _LinkTapsOwnerState();
}

class _LinkTapsOwnerState extends State<LinkTapsOwner> {
  final _all = <String, LinkRecognizer>{};

  @override
  void dispose() {
    for (final r in _all.values) {
      r.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final used = <String>{};
    final w = widget.builder(context, (l) {
      final key = '${l.start}:${l.url}';
      used.add(key);
      final r = _all[key] ??= LinkRecognizer();
      r.onTap = () {
        openChatLink(context, l.url);
      };
      r.onHold = () {
        showLinkMenu(context, l.url);
      };
      return r;
    });
    final gone = [for (final k in _all.keys) if (!used.contains(k)) k].map((k) => _all.remove(k)!).toList();
    if (gone.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        for (final r in gone) {
          r.dispose();
        }
      });
    }
    return w;
  }
}

/// The bubble's link colour: the read ticks' blue, as on the web
/// (`.chat-link`).
Color chatLinkColor(BuildContext context) => TkTheme.tokens(context).chatTickRead;

/// [text] as spans, its web addresses tappable (shortened in the middle
/// when long, opened in full); the words between them as [plain] draws
/// them (a group's @mentions), else as they are.
List<InlineSpan> linkSpans(BuildContext context, String text, GestureRecognizer Function(FoundLink l) link, {InlineSpan Function(String words)? plain}) {
  final links = findLinks(text);
  InlineSpan words(String s) => plain == null ? TextSpan(text: s) : plain(s);
  if (links.isEmpty) return [words(text)];
  final ink = chatLinkColor(context);
  final out = <InlineSpan>[];
  var at = 0;
  for (final l in links) {
    if (l.start > at) out.add(words(text.substring(at, l.start)));
    out.add(TextSpan(
      text: shownLink(l.text),
      style: TextStyle(color: ink, decoration: TextDecoration.underline, decorationColor: ink),
      recognizer: link(l),
    ));
    at = l.end;
  }
  if (at < text.length) out.add(words(text.substring(at)));
  return out;
}

/// A message's words with their links tappable.
class LinkedText extends StatelessWidget {
  const LinkedText(this.text, {super.key, required this.style, this.plain});
  final String text;
  final TextStyle style;

  /// Draws the words between links (a group's @mentions picked out).
  final InlineSpan Function(String words, TextStyle style)? plain;

  @override
  Widget build(BuildContext context) => LinkTapsOwner(
    builder: (context, link) => Text.rich(
      TextSpan(style: style, children: linkSpans(context, text, link, plain: plain == null ? null : (w) => plain!(w, style))),
    ),
  );
}

/// Whether a picture goes across the card (wide) or in a square on its
/// left (small or square): the web's rule (ChatLinks.tsx wideImage).
bool wideImage(int? w, int? h) => w != null && h != null && w >= 400 && w >= h * 1.3;

/// A link's card: at the top of its bubble, or above the box while typed.
/// [path] is where Taskiem keeps its picture (the chat's previews route);
/// a Taskiem link is a plain card ("Taskiem · Order link").
class LinkPreviewCard extends StatelessWidget {
  const LinkPreviewCard({super.key, required this.preview, this.transport, this.path, this.onClose});
  final LinkPreview preview;
  final Transport? transport;
  final String? path;

  /// Above the box: ✕ sends the message without it.
  final VoidCallback? onClose;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final p = preview;
    final img = p.image;
    final taskiem = p.taskiem != null;
    final wide = !taskiem && wideImage(img?.w, img?.h);
    final site = taskiem ? 'Taskiem' : ((p.site ?? '').isNotEmpty ? p.site! : p.host);
    final url = Uri.tryParse(p.url);
    final hasPic = !taskiem && img != null && transport != null && path != null;
    final words = Padding(
      padding: EdgeInsets.fromLTRB(10, 6, onClose != null ? 44 : 10, 7),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(p.title, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 600, lineHeight: 1.3, color: t.text)),
          if ((p.description ?? '').isNotEmpty && !taskiem) ...[
            const SizedBox(height: 2),
            Text(p.description!, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 13, lineHeight: 1.3, color: t.muted)),
          ],
          const SizedBox(height: 2),
          Text(site, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 12, color: t.muted)),
        ],
      ),
    );
    final Widget body;
    if (wide && hasPic) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          // Across the card at its own shape, cut to no taller than 1.6 to 1
          // (3 to 1 above the box, where the words being written need the
          // room). No LayoutBuilder: a bubble measures its children.
          AspectRatio(
            aspectRatio: (img.w / img.h).clamp(onClose != null ? 3.0 : 1.6, 4.0),
            child: ApiImage(transport: transport!, path: path!),
          ),
          words,
        ],
      );
    } else {
      body = IntrinsicHeight(
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (hasPic) SizedBox(width: 76, child: ApiImage(transport: transport!, path: path!, width: 76, height: 76)),
            if (taskiem)
              SizedBox(width: 44, child: Center(child: TkIcon('link', size: 22, stroke: 2, color: chatLinkColor(context)))),
            Expanded(child: words),
          ],
        ),
      );
    }
    final card = Container(
      margin: const EdgeInsets.only(top: 2, bottom: 6),
      clipBehavior: Clip.antiAlias,
      decoration: BoxDecoration(
        color: onClose != null ? t.panel : t.panel2,
        borderRadius: BorderRadius.circular(6),
        border: Border(left: BorderSide(color: chatLinkColor(context), width: 3)),
      ),
      child: Semantics(
        link: true,
        label: '${p.title}, $site',
        excludeSemantics: true,
        onTap: url == null ? null : () => openChatLink(context, url),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: url == null ? null : () => openChatLink(context, url),
          onLongPress: url == null ? null : () => showLinkMenu(context, url),
          child: body,
        ),
      ),
    );
    if (onClose == null) return card;
    return Stack(
      children: [
        card,
        Positioned(
          top: 0,
          right: 0,
          child: Semantics(
            button: true,
            label: 'Send without the preview',
            excludeSemantics: true,
            onTap: onClose,
            child: GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: onClose,
              child: SizedBox(
              width: 48,
              height: 48,
              child: Center(
                child: Container(
                  width: 28,
                  height: 28,
                  decoration: BoxDecoration(color: t.panel2, shape: BoxShape.circle),
                  child: Center(child: TkIcon('close', size: 16, stroke: 2, color: t.text)),
                ),
              ),
            ),
            ),
          ),
        ),
      ],
    );
  }
}

/// The card of the first link typed in a chat's box (POST
/// /v1/link-preview), half a second after the person pauses; what the
/// message is sent with.
class ComposerPreview extends ChangeNotifier {
  ComposerPreview({required this.api, required this.transport, required this.chat, this.wait = const Duration(milliseconds: 500)});
  final TaskiemApi api;

  /// Where its picture is read from (Taskiem's own address).
  final Transport transport;

  /// The chat it is for: link:…, people:…, group:…, door:… or customer:….
  final String chat;
  final Duration wait;

  PreviewLinkResponse? _card;
  bool _loading = false;
  String _url = '';
  String _asked = '';
  String? _closed;
  Timer? _timer;
  bool _off = false;

  /// Not shown, nor asked for (a reply going on WhatsApp, which draws its own).
  set off(bool v) {
    if (_off == v) return;
    _off = v;
    notifyListeners();
  }

  /// The card to show, once made.
  PreviewLinkResponse? get card => _off || _closed == _url || _card?.url != _url ? null : _card;

  /// Asking for it now.
  bool get loading => !_off && _url.isNotEmpty && _closed != _url && _loading;

  /// The words changed.
  void typed(String text) {
    final url = firstLink(text)?.toString() ?? '';
    if (url == _url) return;
    _url = url;
    _timer?.cancel();
    if (url.isEmpty) {
      _asked = '';
      _loading = false;
      notifyListeners();
      return;
    }
    _timer = Timer(wait, () => _ask(url));
    notifyListeners();
  }

  Future<void> _ask(String url) async {
    if (_off || url == _asked) return;
    _asked = url;
    _loading = true;
    notifyListeners();
    PreviewLinkResponse? got;
    try {
      got = await api.previewLink(body: PreviewLinkRequest(url: url, chat: chat));
    } on Object {
      // None (204), or not now: nothing is shown.
      got = null;
    }
    if (_asked != url) return;
    _card = got;
    _loading = false;
    notifyListeners();
  }

  /// ✕: the message goes without it.
  void close() {
    _closed = _url;
    notifyListeners();
  }

  /// What a message of [words] goes with: the card shown ({id}), none
  /// (false, closed), or nothing said (null: the server makes it).
  Object? choice(String words) {
    final u = firstLink(words)?.toString();
    if (u == null || _off) return null;
    if (_closed == u) return false;
    final c = _card;
    if (c != null && c.url == u) return {'id': c.id};
    return null;
  }

  /// Sent: ready for the next message.
  void reset() {
    _timer?.cancel();
    _closed = null;
    _card = null;
    _url = '';
    _asked = '';
    _loading = false;
    notifyListeners();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }
}

/// The card above the box, with ✕; "Loading preview…" while it is made.
class ComposerLinkCard extends StatelessWidget {
  const ComposerLinkCard({super.key, required this.preview});
  final ComposerPreview preview;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: preview,
    builder: (context, _) {
      final t = TkTheme.tokens(context);
      final c = preview.card;
      if (c != null) {
        final img = c.image;
        return Padding(
          padding: const EdgeInsets.fromLTRB(10, 6, 10, 0),
          child: Semantics(
            container: true,
            label: 'Link preview',
            child: LinkPreviewCard(
              key: const ValueKey('composer-preview'),
              preview: LinkPreview(url: c.url, host: c.host, title: c.title, description: c.description, site: c.site, taskiem: c.taskiem,
                  image: img == null ? null : LinkPreviewImage(w: img.w, h: img.h)),
              transport: preview.transport,
              path: img == null ? null : '/v1/link-preview/${Uri.encodeComponent(img.id)}/image',
              onClose: preview.close,
            ),
          ),
        );
      }
      if (preview.loading) {
        return Padding(
          padding: const EdgeInsets.fromLTRB(14, 6, 14, 0),
          child: Semantics(liveRegion: true, child: Text('Loading preview…', style: tkText(size: 13, color: t.muted))),
        );
      }
      return const SizedBox.shrink();
    },
  );
}
