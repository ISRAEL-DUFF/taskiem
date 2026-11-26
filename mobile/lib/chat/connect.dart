// Connecting a service from Taskiem's conversation (docs/spec/mobile-app.md
// §3.5; web/src/pages/Connect.tsx ConnectPage, lib/connections.ts): the
// one-time connect link Taskiem answers `connect <service>` with. A service
// that signs in (OAuth) opens the link in the phone's browser tab, where
// its consent runs by the provider's rules and the browser is bound to it;
// a service connected with keys gets the form here, in the app, never in
// the chat: each value pasted, checked with the service before it is
// saved. Either way the conversation is told and carries on. A saved
// workflow's link opens its canvas in the browser tab.

import 'package:flutter/material.dart' show SelectableText;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkChips, TkFormGroup, TkListRow, TkSection;
import '../theme/look.dart' show TkLook;
import 'approve.dart' show TkTextBox;
import 'linkify.dart' show findLinks;
import 'words.dart' show clock;

final _connectPath = RegExp(r'^/c/([A-Za-z0-9_-]{32})$');
final _canvasPath = RegExp(r'^/workflows/[0-9a-fA-F-]{36}(/[^/]*)?$');

bool _web(Uri u) => u.scheme == 'https' || u.scheme == 'http';

/// The token of a connect link (`/c/<token>`), or null.
String? connectToken(Uri u) => _web(u) ? _connectPath.firstMatch(u.path)?.group(1) : null;

/// A workflow's canvas (`/workflows/<id>`), as "Saved … as a draft at" says.
bool isCanvas(Uri u) => _web(u) && _canvasPath.hasMatch(u.path);

/// What the app opens from Taskiem's words (§3.5): a connect link, or a
/// workflow's canvas. Other addresses stay words, as the web shows them.
bool opensFromTaskiem(Uri u) => connectToken(u) != null || isCanvas(u);

/// The web addresses in [text], by the rule every chat shares
/// (linkify.dart; chat-previews-and-editing.md §2.2): a full stop, comma or
/// bracket after one is not part of it.
List<({int start, int end, Uri uri})> linksIn(String text) => [for (final l in findLinks(text)) (start: l.start, end: l.end, uri: l.url)];

// --- lib/connections.ts ---

/// A kind of key (live, test): its label, and whether this Taskiem has it.
typedef KeyMode = ({String mode, String label, bool available});

List<Map<String, dynamic>> _fields(Map<String, dynamic> auth) => [for (final f in (auth['fields'] as List?) ?? const []) (f as Map).cast<String, dynamic>()];

List<Map<String, dynamic>> _list(Object? v) => [for (final x in (v as List?) ?? const []) (x as Map).cast<String, dynamic>()];

/// connections.ts modesOf.
List<KeyMode> modesOf(Map<String, dynamic> auth) {
  final modes = _list(auth['modes']);
  if (modes.isNotEmpty) {
    return [for (final m in modes) (mode: m['mode'] as String, label: m['label'] as String? ?? m['mode'] as String, available: (m['base_url'] as String? ?? '').isNotEmpty)];
  }
  final out = <KeyMode>[];
  for (final f in _fields(auth)) {
    for (final p in _list(f['prefixes'])) {
      final mode = p['mode'] as String?;
      if (mode != null && mode.isNotEmpty && !out.any((m) => m.mode == mode)) out.add((mode: mode, label: p['label'] as String? ?? mode, available: true));
    }
  }
  return out;
}

/// connections.ts keyProblem: what is wrong with a pasted value, in plain
/// words, as the server checks it; '' when it looks right.
String keyProblem(Map<String, dynamic> f, String value, [String mode = '']) {
  final v = value.trim();
  if (v.isEmpty) return '';
  for (final r in _list(f['refuse'])) {
    if (v.startsWith(r['prefix'] as String)) return r['message'] as String;
  }
  final prefixes = _list(f['prefixes']);
  if (prefixes.isEmpty) return '';
  Map<String, dynamic>? other;
  for (final p in prefixes) {
    if (!v.startsWith(p['prefix'] as String)) continue;
    final pm = p['mode'] as String? ?? '';
    if (mode.isEmpty || pm.isEmpty || pm == mode) return '';
    other = p;
  }
  final label = (f['label'] as String).toLowerCase();
  if (other != null) {
    final wanted = prefixes.where((p) => p['mode'] == mode).firstOrNull;
    final want = (wanted?['label'] as String? ?? mode).toLowerCase();
    final got = (other['label'] as String? ?? other['mode'] as String? ?? '').toLowerCase();
    return 'that is a $got $label, but you chose $want; copy the $want $label instead';
  }
  final starts = [for (final p in prefixes) if (mode.isEmpty || (p['mode'] as String? ?? '').isEmpty || p['mode'] == mode) p['prefix'] as String];
  return 'that does not look like your $label: it starts with ${starts.join(' or ')}';
}

/// connections.ts splitFields: the required first, the optional under More
/// options; one the server makes (webhook_token) never shown.
({List<Map<String, dynamic>> main, List<Map<String, dynamic>> more}) splitFields(Map<String, dynamic> auth) {
  final fields = _fields(auth).where((f) => f['generate'] != 'webhook_token').toList();
  return (main: fields.where((f) => f['required'] != false).toList(), more: fields.where((f) => f['required'] == false).toList());
}

// --- opening a link ---

/// A link in Taskiem's words pressed: a connect link (the browser tab for a
/// service that signs in, the form here for keys) or a workflow's canvas.
/// [onDone] once a service is connected here.
Future<void> openTaskiemLink(BuildContext context, Auth auth, Uri u, {VoidCallback? onDone}) async {
  void say(String words) {
    if (context.mounted) showTkToast(context, words, error: true);
  }

  final token = connectToken(u);
  if (token == null) {
    if (!await openInBrowser(u)) say('That page could not be opened.');
    return;
  }
  final GetConnectLinkResponse view;
  try {
    view = await auth.api.getConnectLink(token: token);
  } on ApiError catch (e) {
    say(e.message);
    return;
  } on Offline {
    say('You’re offline. Try again when you’re back online.');
    return;
  }
  if (!context.mounted) return;
  final c = view.connector;
  if (c.auth['oauth2'] != null && c.oauthLinkable == true) {
    // Its consent: in the browser tab, by the provider's rules.
    if (!await openInBrowser(u)) say('That page could not be opened.');
    return;
  }
  final made = await showTkSheet<bool>(context, (_) => ConnectSheet(auth: auth, token: token, view: view));
  if (made == true) onDone?.call();
}

/// Connect.tsx ConnectPage's key form, in a sheet; then what was made, with
/// the webhook addresses to paste at the service (shown this once).
class ConnectSheet extends StatefulWidget {
  const ConnectSheet({super.key, required this.auth, required this.token, required this.view});
  final Auth auth;
  final String token;
  final GetConnectLinkResponse view;

  @override
  State<ConnectSheet> createState() => _ConnectSheetState();
}

class _ConnectSheetState extends State<ConnectSheet> {
  final _boxes = <String, TextEditingController>{};
  String _mode = '';
  bool _more = false;
  bool _busy = false;
  String? _error;
  ConnectWithKeyResponse? _done;
  final _copied = <String>{};

  Connector get _c => widget.view.connector;

  TextEditingController _box(String key) => _boxes.putIfAbsent(key, () => TextEditingController()..addListener(() => setState(() {})));

  @override
  void dispose() {
    for (final b in _boxes.values) {
      b.dispose();
    }
    super.dispose();
  }

  String get _chosen {
    final modes = modesOf(_c.auth);
    return _mode.isNotEmpty ? _mode : (modes.where((m) => m.available).firstOrNull?.mode ?? '');
  }

  Future<void> _connect() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final out = await widget.auth.api.connectWithKey(
        token: widget.token,
        body: ConnectWithKeyRequest(
          credentials: {for (final e in _boxes.entries) if (e.value.text.trim().isNotEmpty) e.key: e.value.text.trim()},
          mode: _chosen.isEmpty ? null : _chosen,
        ),
      );
      if (mounted) setState(() => _done = out);
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final c = _c;
    final done = _done;
    if (done != null) return _connected(t, done);
    final modes = modesOf(c.auth);
    final chosen = _chosen;
    final (:main, :more) = splitFields(c.auth);
    final problems = {for (final f in _fields(c.auth)) f['key'] as String: keyProblem(f, _box(f['key'] as String).text, chosen)};
    final blocked = problems.values.any((p) => p.isNotEmpty);
    Widget field(Map<String, dynamic> f) {
      final key = f['key'] as String;
      final where = f['where'] as String? ?? '';
      final help = [if ((f['help'] as String? ?? '').isNotEmpty) f['help'] as String, if (where.isNotEmpty) 'Find it in $where.'].join(' ');
      final problem = problems[key] ?? '';
      final link = Uri.tryParse(f['link'] as String? ?? '');
      final opens = link != null && link.host.isNotEmpty;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkKitField(
            label: '${f['label']}${f['required'] == false ? ' (optional)' : ''}',
            hint: help.isEmpty || opens ? null : help,
            error: problem.isEmpty ? null : '${problem[0].toUpperCase()}${problem.substring(1)}.',
            child: TkTextBox(controller: _box(key), lines: f['multiline'] == true ? 5 : 1, secret: f['secret'] == true, plain: true, size: 15, height: 46),
          ),
          // As drawn: where to find it, then Open it on the same line.
          if (opens)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Text.rich(
                TextSpan(
                  style: tkHint(t),
                  children: [
                    if (help.isNotEmpty) TextSpan(text: '$help '),
                    WidgetSpan(
                      alignment: PlaceholderAlignment.baseline,
                      baseline: TextBaseline.alphabetic,
                      child: TkLink(label: 'Open it', onTap: () => openInBrowser(link)),
                    ),
                  ],
                ),
              ),
            ),
        ],
      );
    }

    final open = [for (final m in modes) if (m.available) m];
    // In the app's own look, as drawn: the kind as a switch (Live, Test),
    // the values on a card, More options as a row.
    return TkAppLook(child: TkSheet(
      title: 'Connect ${c.name} to ${widget.view.business}',
      body: TkFormBody(
        children: [
          Text(
            'Copy each value from ${c.name} and paste it here. Taskiem checks it with ${c.name} before saving it, and it never appears in your chat.',
            style: tkFormNow(t),
          ),
          if (open.length > 1)
            TkChips<String>(
              label: 'Which kind?',
              padding: EdgeInsets.zero,
              chips: [for (final m in open) (m.mode, m.label.split(' (').first)],
              on: chosen,
              onPick: (v) => setState(() => _mode = v),
            ),
          if (main.isNotEmpty) TkFormGroup(children: [...main.map(field)]),
          if (more.isNotEmpty) ...[
            Semantics(
              expanded: _more,
              child: TkSection(
                inset: 56,
                children: [
                  TkListRow(
                    title: _more ? 'Fewer options' : 'More options',
                    semanticsLabel: 'More options',
                    icon: 'b-key',
                    square: TkLook.of(context).violet,
                    onTap: () => setState(() => _more = !_more),
                  ),
                ],
              ),
            ),
            if (_more) TkFormGroup(children: [...more.map(field)]),
          ],
          if (_error != null) TkErrorBox(_error!),
          Text('This link works once, until ${clock(widget.view.expiresAt)}.', style: tkHint(t)),
        ],
      ),
      footer: TkCta(label: _busy ? 'Checking…' : 'Connect ${c.name}', onTap: _busy || blocked ? null : _connect),
    ));
  }

  /// Connect.tsx Connected, with MadeWebhooks.
  Widget _connected(TkTokens t, ConnectWithKeyResponse done) {
    final hooks = done.webhooks ?? const <ConnectionWebhook>[];
    final service = done.connected;
    final one = hooks.length == 1;
    final look = TkLook.of(context);
    return TkAppLook(child: TkSheet(
      title: '$service is connected',
      body: TkFormBody(
        children: [
          Semantics(
            liveRegion: true,
            child: Text('$service is connected to ${done.business}. Taskiem has told your chat, and carries on there.', style: tkFormNow(t)),
          ),
          if (hooks.isNotEmpty) ...[
            // As drawn: what to do now as a warning, each address on a card.
            TkCallout(
              leading: TkSquare('copy', color: look.orange),
              title: 'Copy ${one ? 'this address' : 'these addresses'} now',
              sub: 'Paste ${one ? 'it' : 'each'} into $service. ${one ? 'It is' : 'They are'} shown only once.',
              color: look.orange,
            ),
            for (final h in hooks)
              TkFormGroup(
                children: [
                  Text('${h.title}: ${h.where}', style: tkText(size: 13, color: t.muted)),
                  Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(12)),
                    child: SelectableText(h.url, style: tkText(size: 13, lineHeight: 1.4, color: t.text).copyWith(fontFamily: 'monospace', fontFamilyFallback: const ['Menlo', 'Courier', 'Roboto Mono'])),
                  ),
                  // As drawn: plain bold words.
                  Center(
                    child: TapArea(
                      child: Semantics(
                        button: true,
                        label: _copied.contains(h.trigger) ? 'Copied' : 'Copy the address',
                        excludeSemantics: true,
                        onTap: () async {
                          await Clipboard.setData(ClipboardData(text: h.url));
                          if (mounted) setState(() => _copied.add(h.trigger));
                        },
                        child: GestureDetector(
                          behavior: HitTestBehavior.opaque,
                          onTap: () async {
                            await Clipboard.setData(ClipboardData(text: h.url));
                            if (mounted) setState(() => _copied.add(h.trigger));
                          },
                          child: Padding(
                            padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 12),
                            child: Text(_copied.contains(h.trigger) ? 'Copied' : 'Copy the address', style: tkText(size: 14, weight: 700, color: t.text)),
                          ),
                        ),
                      ),
                    ),
                  ),
                ],
              ),
          ],
        ],
      ),
      footer: TkCta(label: 'Done', onTap: () => Navigator.of(context).pop(true)),
    ));
  }
}
