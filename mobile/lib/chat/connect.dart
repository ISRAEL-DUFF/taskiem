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
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
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

/// The web addresses in [text], as the server finds them (chat_history.go
/// historyURL): a full stop, comma or bracket after one is not part of it.
List<({int start, int end, Uri uri})> linksIn(String text) {
  final out = <({int start, int end, Uri uri})>[];
  for (final m in RegExp(r'https?://[^\s<>"]+').allMatches(text)) {
    var end = m.end;
    while (end > m.start && '.,)'.contains(text[end - 1])) {
      end--;
    }
    final u = Uri.tryParse(text.substring(m.start, end));
    if (u != null && u.host.isNotEmpty) out.add((start: m.start, end: end, uri: u));
  }
  return out;
}

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
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkKitField(
            label: '${f['label']}${f['required'] == false ? ' (optional)' : ''}',
            hint: help.isEmpty ? null : help,
            error: problem.isEmpty ? null : '${problem[0].toUpperCase()}${problem.substring(1)}.',
            child: TkTextBox(controller: _box(key), lines: f['multiline'] == true ? 5 : 1, secret: f['secret'] == true, plain: true),
          ),
          if (link != null && link.host.isNotEmpty)
            Align(alignment: Alignment.centerLeft, child: TkLink(label: 'Open it', onTap: () => openInBrowser(link))),
        ],
      );
    }

    return TkSheet(
      title: 'Connect ${c.name} to ${widget.view.business}',
      body: TkFormBody(
        children: [
          Text(
            'Copy each value from ${c.name} and paste it here. Taskiem checks it with ${c.name} before saving it, and it never appears in your chat.',
            style: tkFormNow(t),
          ),
          if (modes.length > 1)
            Semantics(
              container: true,
              label: 'Which kind?',
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text('Which kind?', style: tkText(size: 13, weight: 600, color: t.muted)),
                  const SizedBox(height: 8),
                  for (final m in modes)
                    Opacity(
                      opacity: m.available ? 1 : 0.45,
                      child: Semantics(
                        enabled: m.available,
                        child: TkChoiceRow(
                          control: TkRadio(checked: chosen == m.mode),
                          label: Text(m.label, style: tkText(size: 14, color: t.text)),
                          onTap: m.available ? () => setState(() => _mode = m.mode) : () {},
                        ),
                      ),
                    ),
                ],
              ),
            ),
          ...main.map(field),
          if (more.isNotEmpty) ...[
            Semantics(
              button: true,
              expanded: _more,
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () => setState(() => _more = !_more),
                child: Text('More options', style: tkText(size: 14, weight: 600, color: t.accent)),
              ),
            ),
            if (_more) ...more.map(field),
          ],
          if (_error != null) TkErrorBox(_error!),
          Text('This link works once, until ${clock(widget.view.expiresAt)}.', style: tkHint(t)),
        ],
      ),
      footer: TkCta(label: _busy ? 'Checking…' : 'Connect ${c.name}', onTap: _busy || blocked ? null : _connect),
    );
  }

  /// Connect.tsx Connected, with MadeWebhooks.
  Widget _connected(TkTokens t, ConnectWithKeyResponse done) {
    final hooks = done.webhooks ?? const <ConnectionWebhook>[];
    final service = done.connected;
    final one = hooks.length == 1;
    return TkSheet(
      title: '$service is connected',
      body: TkFormBody(
        children: [
          Semantics(
            liveRegion: true,
            child: Text('$service is connected to ${done.business}. Taskiem has told your chat, and carries on there.', style: tkText(size: 15, color: t.text)),
          ),
          if (hooks.isNotEmpty) ...[
            Text(
              'Copy ${one ? 'this address' : 'these addresses'} now and paste ${one ? 'it' : 'each'} into $service. ${one ? 'It carries' : 'They carry'} a secret Taskiem made for this connection, and Taskiem refuses anything $service sends without it. It is shown only once; to get a new one, connect again.',
              style: tkText(size: 14, weight: 600, color: t.text),
            ),
            for (final h in hooks)
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text('${h.title}: ${h.where}', style: tkFormNow(t)),
                  const SizedBox(height: 6),
                  SelectableText(h.url, style: tkText(size: 13, color: t.text)),
                  const SizedBox(height: 6),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TkPlainButton(
                      label: _copied.contains(h.trigger) ? 'Copied' : 'Copy the address',
                      onTap: () async {
                        await Clipboard.setData(ClipboardData(text: h.url));
                        if (mounted) setState(() => _copied.add(h.trigger));
                      },
                    ),
                  ),
                ],
              ),
          ],
        ],
      ),
      footer: TkCta(label: 'Done', onTap: () => Navigator.of(context).pop(true)),
    );
  }
}
