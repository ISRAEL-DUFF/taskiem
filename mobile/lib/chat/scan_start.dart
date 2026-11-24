// A code scanned from the phone's home screen, with no workflow open
// (docs/spec/device-fields.md §7, D2): the person long-presses the app's
// icon and taps Scan a code; the camera reads it; the business's workflows
// that take a code of that kind are offered (GET /v1/scan/offers: those the
// person may start, tools first). A tool opens in Taskiem's conversation,
// its form filled in with the code; a workflow that needs nothing more is
// started on Yes; one that asks more is started on the web.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/forms.dart' show errorWords;
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkFormGroup, TkListRow, TkSection;
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkCta, TkLines;
import '../theme/look.dart';
import 'code_scanner.dart';
import 'groups.dart' show askSure;

/// Opens a tool in Taskiem's conversation with its form filled in.
typedef OpenToolWith = void Function(String command, Map<String, dynamic> initial);

/// Scans a code, then shows what it can start; nothing when put away.
Future<void> scanFromHome(BuildContext context, {required Auth auth, required OpenToolWith openTool}) async {
  for (;;) {
    if (!context.mounted) return;
    final code = await codeScanner.scanOne(context, title: 'Scan a code', backLabel: 'Back to Taskiem');
    if (code == null || !context.mounted) return;
    final again = await Navigator.of(context).push<String>(PageRouteBuilder<String>(
      transitionDuration: Duration.zero,
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (_, _, _) => ScanOffersScreen(auth: auth, code: code, openTool: openTool),
    ));
    if (again != 'again') return;
  }
}

/// What a scanned code can start.
class ScanOffersScreen extends StatefulWidget {
  const ScanOffersScreen({super.key, required this.auth, required this.code, required this.openTool});
  final Auth auth;
  final ScannedCode code;
  final OpenToolWith openTool;

  @override
  State<ScanOffersScreen> createState() => _ScanOffersScreenState();
}

class _ScanOffersScreenState extends State<ScanOffersScreen> {
  List<ScanOffer>? _offers;
  String? _error;
  String? _starting;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      // Only the code's kind is sent: what it starts depends on nothing else.
      final r = await widget.auth.api.scanOffers(format: widget.code.format);
      if (mounted) setState(() => _offers = r.offers);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _open(ScanOffer o) async {
    final initial = <String, dynamic>{o.input: widget.code.toJson()};
    if (o.kind == 'tool' && o.command != null) {
      widget.openTool(o.command!, initial);
      return;
    }
    if (o.asks.isNotEmpty) return;
    final yes = await askSure(context, title: 'Start ${o.name}?', body: 'With the code ${widget.code.text}. It runs as you.', ok: 'Start');
    if (!yes || !mounted) return;
    setState(() => _starting = o.workflowId);
    try {
      await widget.auth.transport.send(
        'POST',
        '/v1/workflows/${Uri.encodeComponent(o.workflowId)}/runs',
        body: {'input': initial, 'environment': o.environment},
        headers: {'Idempotency-Key': uuid4()},
      );
      if (!mounted) return;
      showTkToast(context, '${o.name} started.');
      Navigator.of(context).maybePop();
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _starting = null);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final offers = _offers;
    final tools = offers?.where((o) => o.kind == 'tool').toList() ?? const [];
    final flows = offers?.where((o) => o.kind != 'tool').toList() ?? const [];
    TkListRow row(ScanOffer o) {
      final ready = o.kind == 'tool' || o.asks.isEmpty;
      return TkListRow(
        title: o.name,
        sub: o.kind == 'tool'
            ? ((o.about ?? '').isNotEmpty ? o.about! : 'Opens with the code filled in')
            : ready
            ? 'Starts with the code; nothing more to fill in'
            : 'Asks more than the code: start it on the web',
        icon: o.kind == 'tool' ? 'b-bolt' : 'b-flow',
        square: o.kind == 'tool' ? look.violet : (ready ? look.teal : look.blue),
        status: _starting == o.workflowId ? 'Starting…' : null,
        chevron: ready,
        onTap: ready && _starting == null ? () => unawaited(_open(o)) : null,
        semanticsLabel: '${o.kind == 'tool' ? 'Open' : 'Start'} ${o.name}',
      );
    }

    return TkPageScreen(
      title: 'What this code starts',
      titleSize: 22,
      backLabel: 'Back to Taskiem',
      children: [
        TkFormGroup(children: [
          TkLines([('Code', widget.code.text), ('Kind', formatWords(widget.code.format))]),
        ]),
        if (_error != null)
          TkCallout(leading: TkSquare('b-alert', color: look.orange), title: 'This could not be read', sub: _error, color: look.orange, button: 'Try again', onPressed: _load),
        if (offers == null && _error == null) const TkLoading(rows: 2, label: 'Finding what takes this code…'),
        if (offers != null && offers.isEmpty)
          TkCallout(
            leading: TkSquare('b-qr', color: look.blue),
            title: 'Nothing takes this code yet',
            sub: 'None of your business’s tools or workflows takes ${aFormat(widget.code.format)}. A workflow takes one when one of its inputs is a scan.',
            subLines: 4,
            color: look.blue,
          ),
        if (tools.isNotEmpty) TkSection(title: 'Your tools', inset: 56, children: [for (final o in tools) row(o)]),
        if (flows.isNotEmpty) TkSection(title: 'Workflows', inset: 56, children: [for (final o in flows) row(o)]),
        TkCta(label: 'Scan another', icon: 'b-qr', onTap: () => Navigator.of(context).maybePop('again')),
      ],
    );
  }
}
