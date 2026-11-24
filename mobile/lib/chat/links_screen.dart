// Linked businesses (web/src/pages/Links.tsx LinkedBusinessesPage,
// /settings/links): being found, how other businesses see you, your link
// code and its QR code, and every link and request with its state and
// what can be done with it: Accept, Decline or Block a request; End a link;
// Withdraw a request or take one off the list; Unblock. Opened from the
// chats' ⋯ menu.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../app/lock.dart' show stepOut;
import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/api_image.dart';
import '../kit/forms.dart' show TkSwitch;
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/page.dart' show TkPageScreen;
import '../theme/look.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'attachments.dart' show handOffFile;
import 'chats.dart';
import 'pages_page.dart' show PlacedPageTabs;
import 'profile_screen.dart' show photoPath;
import 'words.dart';

/// links.ts groupLinks: asked, linked, waiting, then past.
/// links.ts groupLinks: asking us, linked, waiting, past; each but the
/// first by name.
List<BusinessLink> _ordered(List<BusinessLink> all) {
  List<BusinessLink> by(Set<String> states) => all.where((l) => states.contains(l.state)).toList()..sort((a, b) => a.name.toLowerCase().compareTo(b.name.toLowerCase()));
  return [
    ...all.where((l) => l.state == 'asked_us'),
    ...by({'active'}),
    ...by({'requested', 'not_accepted'}),
    ...by({'ended', 'blocked'}),
  ];
}

class LinksScreen extends StatefulWidget {
  const LinksScreen({super.key, required this.chats, this.onProfile, this.onFind});
  final ChatsData chats;

  /// Edit your business in Explore.
  final VoidCallback? onProfile;

  /// No links yet: Find a business (Explore), from this page's context.
  final void Function(BuildContext context)? onFind;

  @override
  State<LinksScreen> createState() => _LinksScreenState();
}

class _LinksScreenState extends State<LinksScreen> {
  LinkSettings? _settings;
  // Your card as others see it (the business profile), for the top card.
  Map<String, dynamic>? _card;
  Uint8List? _qr;
  String? _error;
  bool _busy = false;

  ChatsData get _d => widget.chats;

  @override
  void initState() {
    super.initState();
    // Links.tsx useLinks: the links read as the page opens, not as they
    // were when the chats were.
    unawaited(_d.load());
    _load();
  }

  Future<void> _load() async {
    unawaited(() async {
      try {
        final c = await _d.auth.transport.send('GET', '/v1/business-profile');
        if (mounted && c is Map) setState(() => _card = c.cast<String, dynamic>());
      } on Object {
        // No card read: the top card goes without it.
      }
    }());
    await _try(() async {
      final s = await _d.api.getLinkSettings();
      if (!mounted) return;
      setState(() => _settings = s);
      if (s.url != null && _qr == null) {
        // Not there, the card shows the address alone, as the web's.
        try {
          final png = await _d.auth.transport.bytes('/v1/link-settings/qr?format=png');
          if (mounted) setState(() => _qr = Uint8List.fromList(png));
        } on Object {
          // No QR code.
        }
      }
    });
  }

  Future<bool> _try(Future<void> Function() f) async {
    try {
      await f();
      return true;
    } on ApiError catch (e) {
      if (mounted) setState(() => _error = e.message);
    } on Offline {
      if (mounted) setState(() => _error = 'You’re offline. This shows when you’re back online.');
    }
    return false;
  }

  Future<void> _act(Future<void> Function() f, String done) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    final ok = await _try(f);
    if (mounted) {
      setState(() => _busy = false);
      if (ok) showTkToast(context, done);
    }
  }

  void _answer(BusinessLink l, String a, String done) => _act(() async {
    final why = await _d.answer(l, a);
    if (why != null) throw ApiError(400, {'error': why});
  }, done);

  void _findable(bool on) => _act(() async {
    await _d.auth.transport.send('PUT', '/v1/link-settings', body: {'findable': on});
    final s = await _d.api.getLinkSettings();
    if (mounted) setState(() => _settings = s);
    await _d.load();
  }, 'Saved');

  void _takeOff(BlindLinkRequest b) => _act(() async {
    await _d.auth.transport.send('DELETE', '/v1/links/sent/${Uri.encodeComponent(b.id)}');
    await _d.load();
  }, 'Taken off the list');

  /// Link with us on Taskiem: the QR code and the address, over the page.
  void _code() {
    final url = _settings?.url;
    if (url == null) return;
    showTkModal<void>(context, title: 'Link with us on Taskiem', builder: (_) => _CodeCard(url: url, qr: _qr, bare: true));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final manage = _d.auth.can('link.manage');
    final s = _settings;
    final url = s?.url;
    final card = _card;
    final name = (card?['name'] as String?) ?? '';
    final line = [(card?['tagline'] as String?) ?? '', (card?['area'] as String?) ?? ''].where((x) => x.isNotEmpty).join(' · ');
    final logo = photoPath(card?['logo'] as Map<String, dynamic>?);
    // As drawn (docs/spec/mobile-app-look.md, "Settings"): being found on
    // the top card with your card as others see it; then your links.
    return ListenableBuilder(
      listenable: _d,
      builder: (context, _) {
        final links = _ordered(_d.links);
        return TkPageScreen(
          title: 'Linked businesses',
          titleSize: 30,
          backLabel: 'Back to Chats',
          action: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (url != null) TkHeadButton(icon: 'b-qr', label: 'Link with us on Taskiem: the QR code', onTap: _code),
              if (url != null && widget.onFind != null) const SizedBox(width: 8),
              if (widget.onFind != null) TkHeadButton(icon: 'b-plus', label: 'Find a business to link with', primary: true, onTap: () => widget.onFind!(context)),
            ],
          ),
          children: [
            if ((_error ?? _d.error) != null) TkErrorBox((_error ?? _d.error)!),
            // The business's own pages placed under Linked businesses, as
            // tabs after this screen's own (pages H5).
            PlacedPageTabs(auth: _d.auth, areas: const ['links'], own: 'Links', label: 'Linked businesses', backLabel: 'Back to Linked businesses'),
            TkTopCard(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  TkEyebrow('Being found', color: look.tealInk, trailing: 'in Explore'),
                  const SizedBox(height: 10),
                  if (s == null)
                    Text('Loading the setting…', style: tkHint(t))
                  else
                    MergeSemantics(
                      child: GestureDetector(
                        behavior: HitTestBehavior.opaque,
                        onTap: manage && !_busy ? () => _findable(!s.findableNow) : null,
                        child: Row(
                          children: [
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text('Let other businesses find us', style: tkText(size: 15.5, weight: 600, color: t.text)),
                                  Text(findableLine(s), style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
                                ],
                              ),
                            ),
                            const SizedBox(width: 12),
                            TkSwitch(label: 'Let other businesses find us', on: s.findableNow, onChanged: manage && !_busy ? _findable : null),
                          ],
                        ),
                      ),
                    ),
                  if (name.isNotEmpty) ...[
                    const SizedBox(height: 12),
                    // Your card as other businesses see it.
                    Semantics(
                      label: 'How other businesses see you: $name${line.isNotEmpty ? ', $line' : ''}',
                      excludeSemantics: true,
                      child: Container(
                        padding: const EdgeInsets.all(10),
                        decoration: BoxDecoration(color: look.ground, borderRadius: BorderRadius.circular(16)),
                        child: Row(
                          children: [
                            ClipOval(
                              child: logo != null
                                  ? ApiImage(transport: _d.auth.transport, path: logo, width: 40, height: 40)
                                  : TkFace(tkInitials(name), size: 40, colors: tkFaceFor(name)),
                            ),
                            const SizedBox(width: 10),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14.5, weight: 600, color: t.text)),
                                  if (line.isNotEmpty) Text(line, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 12, lineHeight: 1.3, color: t.muted)),
                                ],
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ],
                  const SizedBox(height: 12),
                  TkButtonRow([
                    TkSmallButton(label: 'Edit your card', semantics: 'Edit your business in Explore', onPressed: widget.onProfile ?? () => showComingSoon(context)),
                    if (url != null)
                      TkSmallButton(
                        label: 'Share your link',
                        icon: 'b-share',
                        onPressed: () => stepOut(() => SharePlus.instance.share(ShareParams(text: url))),
                      ),
                  ]),
                ],
              ),
            ),
            if (!_d.loaded)
              Text('Loading your linked businesses…', style: tkHint(t))
            else if (links.isEmpty && _d.sent.isEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 12),
                child: Column(
                  children: [
                    Text('No links yet.', style: tkHint(t)),
                    const SizedBox(height: 12),
                    TkSmallButton(
                      label: 'Find a business',
                      primary: true,
                      onPressed: () => widget.onFind != null ? widget.onFind!(context) : Navigator.of(context).maybePop(),
                    ),
                  ],
                ),
              )
            else
              // As drawn: each link a row with its face and, unless linked,
              // its state; a request answered on its row; the rest from a
              // tap on the row.
              TkSection(
                title: 'Your links',
                inset: 66,
                trailing: Text('${links.length + _d.sent.length}', style: tkText(size: 15, weight: 600, color: t.muted)),
                children: [
                  for (final l in links) _row(l, manage, look),
                  for (final b in _d.sent)
                    TkListRow(
                      title: b.shown,
                      sub: 'Sent ${whenWords(b.createdAt, _d.now())}',
                      icon: 'b-chat',
                      status: stateWords('sent')?.label,
                      statusAccent: (fill: look.blue, ink: look.blueInk),
                      onTap: manage ? () => _more(b.shown, [('off', 'Take off the list', () => _takeOff(b))]) : null,
                    ),
                ],
              ),
          ],
        );
      },
    );
  }

  /// A link's row: its face, who they are to you and since when, its
  /// state unless linked; Accept and Decline under a request to answer.
  Widget _row(BusinessLink l, bool manage, TkLook look) {
    final st = stateWords(l.state);
    final accent = switch (st?.tone) {
      Tone.warn => (fill: look.orange, ink: look.orangeInk),
      Tone.info => (fill: look.blue, ink: look.blueInk),
      Tone.danger => (fill: look.red, ink: look.redInk),
      Tone.ok => (fill: look.lime, ink: look.limeInk),
      _ => null,
    };
    final more = <(String, String, VoidCallback)>[
      if (l.state == 'asked_us')
        (
          'block',
          'Block',
          () async {
            final yes = await showTkConfirm(context, title: 'Block ${l.name}?', body: 'Their requests will not reach you, and they are not told.', ok: 'Block', danger: true);
            if (yes) _answer(l, 'block', 'Blocked');
          },
        ),
      if (l.state == 'active')
        (
          'end',
          'End link',
          () async {
            final yes = await showTkConfirm(context, title: 'End the link with ${l.name}?', body: 'What passed between you stays on both sides.', ok: 'End link', danger: true);
            if (yes) _answer(l, 'end', 'Link ended');
          },
        ),
      if (l.state == 'requested') ('cancel', 'Withdraw request', () => _answer(l, 'cancel', 'Request withdrawn')),
      if (l.state == 'not_accepted') ('cancel', 'Take off the list', () => _answer(l, 'cancel', 'Request withdrawn')),
      if (l.state == 'blocked') ('unblock', 'Unblock', () => _answer(l, 'unblock', 'Unblocked')),
    ];
    final row = TkListRow(
      title: l.name,
      letters: l.name,
      sub: '${roleLine(l.role)} · ${l.state == 'active' && l.answeredAt != null ? 'Linked ${whenWords(l.answeredAt!, _d.now())}' : 'Asked ${whenWords(l.requestedAt, _d.now())}'}',
      status: l.state == 'active' ? null : st?.label,
      statusAccent: accent,
      onTap: manage && more.isNotEmpty ? () => _more(l.name, more) : null,
      semanticsLabel: manage && more.isNotEmpty ? 'More for ${l.name}' : null,
    );
    if (!manage || l.state != 'asked_us') return row;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        row,
        Padding(
          padding: const EdgeInsets.fromLTRB(66, 0, 14, 12),
          child: Row(
            children: [
              TkSmallButton(label: 'Accept', semantics: 'Accept ${l.name}', primary: true, onPressed: _busy ? null : () => _answer(l, 'accept', 'Linked with ${l.name}')),
              const SizedBox(width: 8),
              TkSmallButton(label: 'Decline', semantics: 'Decline ${l.name}', onPressed: _busy ? null : () => _answer(l, 'decline', 'Declined. They are not told.')),
            ],
          ),
        ),
      ],
    );
  }

  /// What else can be done with a row, over the page.
  Future<void> _more(String name, List<(String, String, VoidCallback)> what) async {
    final pick = await showTkModalChoices(context, title: name, choices: [for (final (k, words, _) in what) (k, words)]);
    if (pick == null) return;
    what.firstWhere((w) => w.$1 == pick).$3();
  }
}

/// The web's `.card`, as the settings' cards draw it.
class _Card extends StatelessWidget {
  const _Card({required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      explicitChildNodes: true,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 20),
        decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.border), borderRadius: BorderRadius.circular(Tk.radius)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: Semantics(header: true, child: Text(title, style: tkText(size: 16, weight: 600, lineHeight: 1.3, color: t.text))),
            ),
            ...children,
          ],
        ),
      ),
    );
  }
}

/// Links.tsx CodeCard: the address that opens a request to us and its QR
/// code; the link copied, or the code shared to print or send.
class _CodeCard extends StatefulWidget {
  const _CodeCard({required this.url, required this.qr, this.bare = false});
  final String url;
  final Uint8List? qr;

  /// In its own sheet: no card round it.
  final bool bare;

  @override
  State<_CodeCard> createState() => _CodeCardState();
}

class _CodeCardState extends State<_CodeCard> {
  bool _copied = false;
  final _share = GlobalKey();

  Rect _from() {
    final b = _share.currentContext?.findRenderObject();
    return b is RenderBox && b.hasSize ? b.localToGlobal(Offset.zero) & b.size : Rect.zero;
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final qr = widget.qr;
    Widget wrap(List<Widget> children) => widget.bare
        ? Column(crossAxisAlignment: CrossAxisAlignment.stretch, mainAxisSize: MainAxisSize.min, children: children)
        : _Card(title: 'Link with us on Taskiem', children: children);
    return wrap([
        Text(
          'A business on Taskiem that opens this or scans the code can ask to link with you, whether or not others find you by search.',
          style: tkHint(t),
        ),
        const SizedBox(height: 12),
        Center(
          child: qr == null
              ? SizedBox(width: 160, height: 160, child: Center(child: Text('Loading the QR code…', style: tkHint(t))))
              : Semantics(
                  image: true,
                  label: 'QR code for ${widget.url}',
                  child: Image.memory(qr, width: 160, height: 160, excludeFromSemantics: true),
                ),
        ),
        const SizedBox(height: 12),
        Text(widget.url, style: tkText(size: 13, color: t.text)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            IntrinsicWidth(
              child: TkSmallButton(
                label: _copied ? 'Copied' : 'Copy the link',
                onPressed: () async {
                  await Clipboard.setData(ClipboardData(text: widget.url));
                  if (mounted) setState(() => _copied = true);
                },
              ),
            ),
            IntrinsicWidth(
              key: _share,
              child: TkSmallButton(
                label: 'Share the QR code',
                onPressed: qr == null ? null : () => handOffFile(qr, 'link-with-us-qr.png', 'image/png', _from()),
              ),
            ),
            IntrinsicWidth(
              child: TkSmallButton(
                label: 'Share the link',
                onPressed: () => stepOut(() => SharePlus.instance.share(ShareParams(text: widget.url, sharePositionOrigin: _from()))),
              ),
            ),
          ],
        ),
      ]);
  }
}
