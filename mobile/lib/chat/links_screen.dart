// Linked businesses (web/src/pages/Links.tsx LinkedBusinessesPage,
// /settings/links): being found, how other businesses see you, your link
// code and its QR code, and every link and request with its state and
// what can be done with it: Accept, Decline or Block a request; End a link;
// Withdraw a request or take one off the list; Unblock. Opened from the
// chats' ⋯ menu.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'attachments.dart' show handOffFile;
import 'chats.dart';
import 'thread.dart' show ChatHeadBar;
import 'words.dart';

/// links.ts groupLinks: asked, linked, waiting, then past.
List<BusinessLink> _ordered(List<BusinessLink> all) => [
  ...all.where((l) => l.state == 'asked_us'),
  ...all.where((l) => l.state == 'active'),
  ...all.where((l) => l.state == 'requested' || l.state == 'not_accepted'),
  ...all.where((l) => l.state == 'ended' || l.state == 'blocked' || l.state == 'declined'),
];

class LinksScreen extends StatefulWidget {
  const LinksScreen({super.key, required this.chats, this.onProfile});
  final ChatsData chats;

  /// Edit your business in Explore.
  final VoidCallback? onProfile;

  @override
  State<LinksScreen> createState() => _LinksScreenState();
}

class _LinksScreenState extends State<LinksScreen> {
  LinkSettings? _settings;
  Uint8List? _qr;
  String? _error;
  bool _busy = false;

  ChatsData get _d => widget.chats;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
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

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    final manage = _d.auth.can('link.manage');
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ColoredBox(
        color: t.bg,
        child: Column(
          children: [
            ColoredBox(
              color: t.panel,
              child: Padding(
                padding: EdgeInsets.only(top: pad.top),
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(8, 10, 16, 10),
                  child: Row(
                    children: [
                      ChatHeadBar.iconButton('Back to the chats', TkIcon('back', size: 20, color: t.text), () => Navigator.of(context).maybePop()),
                      const SizedBox(width: 6),
                      Expanded(
                        child: Semantics(
                          header: true,
                          child: Text('Linked businesses', style: tkText(size: 17, weight: 600, color: t.text)),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
            Expanded(
              child: ListenableBuilder(
                listenable: _d,
                builder: (context, _) {
                  final links = _ordered(_d.links);
                  return ListView(
                    padding: EdgeInsets.fromLTRB(16, 16, 16, 24 + pad.bottom),
                    children: [
                      Text(
                        'Businesses on Taskiem you have linked with, and requests either way. Each side keeps its own records.',
                        style: tkHint(t),
                      ),
                      const SizedBox(height: 16),
                      if (_error != null) TkErrorBox(_error!),
                      _Card(
                        title: 'Let other businesses find us',
                        children: [
                          if (_settings == null)
                            Text('Loading the setting…', style: tkHint(t))
                          else
                            TkChoiceRow(
                              radio: false,
                              control: TkCheckbox(checked: _settings!.findableNow),
                              label: Text(findableLine(_settings!), style: tkText(size: 14, color: t.text)),
                              onTap: manage && !_busy ? () => _findable(!_settings!.findableNow) : () {},
                            ),
                          Text(
                            'A search shows only what your storefront already shows the public. Someone with your phone number or page address can always ask.',
                            style: tkHint(t),
                          ),
                        ],
                      ),
                      const SizedBox(height: 16),
                      _Card(
                        title: 'How other businesses see you',
                        children: [
                          Text('Your name, a line about what you do, your area, a logo and up to 12 photos.', style: tkHint(t)),
                          const SizedBox(height: 12),
                          Align(
                            alignment: Alignment.centerLeft,
                            child: TkButton(label: 'Edit your business in Explore', onPressed: widget.onProfile ?? () => showComingSoon(context)),
                          ),
                        ],
                      ),
                      if (_settings?.url != null) ...[const SizedBox(height: 16), _CodeCard(url: _settings!.url!, qr: _qr)],
                      const SizedBox(height: 8),
                      if (!_d.loaded)
                        Padding(padding: const EdgeInsets.only(top: 16), child: Text('Loading your linked businesses…', style: tkHint(t)))
                      else if (links.isEmpty && _d.sent.isEmpty)
                        Padding(
                          padding: const EdgeInsets.only(top: 24),
                          child: Center(child: Text('No links yet.', style: tkHint(t))),
                        )
                      else
                        LinkSection(
                          title: 'Your links',
                          top: 16,
                          rows: [
                            for (final l in links)
                              (
                                letter: l.name,
                                icon: null,
                                tone: stateWords(l.state)?.tone,
                                title: l.name,
                                badge: stateWords(l.state)?.label,
                                badgeTone: stateWords(l.state)?.tone,
                                sub:
                                    '${roleLine(l.role)} · ${l.state == 'active' && l.answeredAt != null ? 'Linked ${whenWords(l.answeredAt!, _d.now())}' : 'Asked ${whenWords(l.requestedAt, _d.now())}'}',
                                actions: manage ? _actions(l) : null,
                              ),
                            for (final b in _d.sent)
                              (
                                letter: null,
                                icon: 'chat',
                                tone: Tone.info,
                                title: b.shown,
                                badge: stateWords('sent')?.label,
                                badgeTone: stateWords('sent')?.tone,
                                sub: 'Sent ${whenWords(b.createdAt, _d.now())}',
                                actions: manage ? _buttons([('Take off the list', 'Take off the list ${b.shown}', false, false, () => _takeOff(b))]) : null,
                              ),
                          ],
                        ),
                    ],
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// Links.tsx LinkActions: what can be done with a link in its state.
  Widget? _actions(BusinessLink l) => switch (l.state) {
    'asked_us' => _buttons([
      ('Accept', 'Accept ${l.name}', true, false, () => _answer(l, 'accept', 'Linked with ${l.name}')),
      ('Decline', 'Decline ${l.name}', false, false, () => _answer(l, 'decline', 'Declined. They are not told.')),
      (
        'Block',
        'Block ${l.name}',
        false,
        true,
        () async {
          final yes = await showTkConfirm(context, title: 'Block ${l.name}?', body: 'Their requests will not reach you, and they are not told.', ok: 'Block', danger: true);
          if (yes) _answer(l, 'block', 'Blocked');
        },
      ),
    ]),
    'active' => _buttons([
      (
        'End link',
        'End link ${l.name}',
        false,
        true,
        () async {
          final yes = await showTkConfirm(context, title: 'End the link with ${l.name}?', body: 'What passed between you stays on both sides.', ok: 'End link', danger: true);
          if (yes) _answer(l, 'end', 'Link ended');
        },
      ),
    ]),
    'requested' || 'not_accepted' => _buttons([
      (
        l.state == 'requested' ? 'Withdraw request' : 'Take off the list',
        '${l.state == 'requested' ? 'Withdraw request' : 'Take off the list'} ${l.name}',
        false,
        false,
        () => _answer(l, 'cancel', 'Request withdrawn'),
      ),
    ]),
    'blocked' => _buttons([('Unblock', 'Unblock ${l.name}', false, false, () => _answer(l, 'unblock', 'Unblocked'))]),
    _ => null,
  };

  Widget _buttons(List<(String, String, bool, bool, VoidCallback)> bs) => Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        for (final b in bs)
          IntrinsicWidth(
            child: TkSmallButton(label: b.$1, semantics: b.$2, primary: b.$3, danger: b.$4, onPressed: _busy ? null : b.$5),
          ),
      ],
    ),
  );
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
  const _CodeCard({required this.url, required this.qr});
  final String url;
  final Uint8List? qr;

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
    return _Card(
      title: 'Link with us on Taskiem',
      children: [
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
                onPressed: () => SharePlus.instance.share(ShareParams(text: widget.url, sharePositionOrigin: _from())),
              ),
            ),
          ],
        ),
      ],
    );
  }
}
