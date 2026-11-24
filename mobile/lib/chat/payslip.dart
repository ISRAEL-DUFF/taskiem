// Payslips as Taskiem's own cards (P4b-4 of docs/spec/chat-pay-people.md):
// the full breakdown, the employer's name and RC number, and the payment,
// read from Payrolla when opened and kept nowhere. Anyone's from the run
// for those with payroll.read; a linked employee's own on My pay, and as
// a card in their Taskiem chat. Save as PDF hands the phone a PDF made
// with the business's name; Send a link gives Payrolla's 7-day link, for
// someone not on Taskiem.

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'attachments.dart' show handOffFile;
import 'cards.dart' show CardPill;
import 'invoices_page.dart' show shortDay;
import 'payroll_page.dart' show payrollLineState;
import 'payroll_settings.dart' show payrollKey;
import 'words.dart' show naira;

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// A payslip's card as the Taskiem conversation keeps it: Payrolla's id
/// and the period; null for anything else.
({String payslip, String period})? payslipCardOf(String text) {
  try {
    final v = jsonDecode(text);
    if (v is Map && v['payslip_id'] is String && (v['payslip_id'] as String).isNotEmpty) {
      return (payslip: v['payslip_id'] as String, period: v['period'] is String ? v['period'] as String : '');
    }
  } on FormatException {
    return null;
  }
  return null;
}

/// "Your payslip for November is ready".
String payslipReady(String month) => 'Your payslip for ${month.isEmpty ? 'this month' : month} is ready';

/// The PDF's file name: payslip-november-2026.pdf.
String payslipFile(String period) {
  final slug = period.toLowerCase().replaceAll(RegExp(r'[^a-z0-9]+'), '-').replaceAll(RegExp(r'^-|-$'), '');
  return 'payslip-${slug.isEmpty ? 'payroll' : slug}.pdf';
}

/// A payslip's lines: what was earned, gross, what was taken, net.
List<(String, String)> payslipLines(PayrollPayslip p) => [
      for (final i in p.items.where((i) => i.kind == 'earning')) (i.label, naira(i.amountKobo)),
      ('Gross pay', naira(p.totalEarningsKobo > 0 ? p.totalEarningsKobo : p.grossKobo)),
      for (final i in p.items.where((i) => i.kind == 'deduction')) (i.label, '−${naira(i.amountKobo)}'),
      ('Net pay', naira(p.netKobo)),
    ];

String _path(String id, bool own) => '/v1/payroll/${own ? 'me/payslips' : 'payslips'}/${Uri.encodeComponent(id)}';

/// One payslip: anyone's ([own] false, payroll.read) or one's own.
class PayslipPage extends StatefulWidget {
  const PayslipPage({super.key, required this.auth, required this.payslip, required this.own, this.backLabel = 'Back'});
  final Auth auth;
  final String payslip;
  final bool own;
  final String backLabel;

  @override
  State<PayslipPage> createState() => _PayslipPageState();
}

class _PayslipPageState extends State<PayslipPage> {
  PayrollPayslip? _slip;
  Object? _error;
  String? _link;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final p = widget.own ? (await widget.auth.api.getMyPayslip(payslip: widget.payslip)).payslip : (await widget.auth.api.getPayrollPayslip(payslip: widget.payslip)).payslip;
      if (mounted) setState(() => (_slip = p, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _pdf(PayrollPayslip p) async {
    setState(() => _busy = true);
    try {
      final bytes = await widget.auth.transport.bytes('${_path(widget.payslip, widget.own)}/pdf');
      await handOffFile(Uint8List.fromList(bytes), payslipFile(p.period), 'application/pdf', null);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _sendLink() async {
    setState(() => _busy = true);
    try {
      final r = await widget.auth.api.sendPayrollPayslipLink(payslip: widget.payslip, body: SendPayrollPayslipLinkRequest(key: payrollKey()));
      if (mounted) setState(() => _link = r.url);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _slip;
    final st = payrollLineState(p?.payment.state ?? '');
    final notFinal = _error is ApiError && (_error! as ApiError).body['code'] == 'payslip_not_final';
    final employer = p == null ? '' : [p.employer.name, if ((p.employer.rcNumber ?? '').isNotEmpty) p.employer.rcNumber!].join(' · ');
    final day = p == null ? '' : shortDay(p.payDate);
    final employerItems = p?.items.where((i) => i.kind == 'employer').toList() ?? const [];
    return TkPageScreen(
      title: p == null ? 'Payslip' : 'Payslip for ${p.period}',
      titleSize: 22,
      backLabel: widget.backLabel,
      description: widget.own
          ? 'Your pay for the month, line by line, as Payrolla worked it out. Only you see it here.'
          : 'This person’s pay for the month, line by line. Only those who may see payroll open it.',
      onRefresh: _load,
      children: [
        if (notFinal)
          TkCallout(leading: TkSquare('b-clock', color: look.teal), title: 'This payslip is ready once the payroll is locked.', subLines: 3, color: look.teal)
        else if (_error != null)
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'The payslip did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (p == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the payslip…'),
        if (p != null) ...[
          TkTopCard(
            semanticsLabel: '${p.employee.name}: net pay ${naira(p.netKobo)} for ${p.period}. ${p.payment.words}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Net pay', color: look.limeInk, icon: 'b-cash', trailing: day.isEmpty ? null : 'Pay day $day'),
                const SizedBox(height: 8),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(p.netKobo), style: tkFigure(context, size: 38))),
                const SizedBox(height: 2),
                Text([p.employee.name, if ((p.employee.department ?? '').isNotEmpty) p.employee.department!].join(' · '), style: tkText(size: 14, lineHeight: 1.35, color: t.muted)),
                const SizedBox(height: 12),
                Row(children: [CardPill(p.payment.words, st.tone)]),
              ],
            ),
          ),
          TkFormGroup(title: 'Breakdown', children: [TkLines(payslipLines(p))]),
          if (employerItems.isNotEmpty) TkFormGroup(title: 'The business also pays', children: [TkLines([for (final i in employerItems) (i.label, naira(i.amountKobo))])]),
          TkSection(title: 'Employer', inset: 56, children: [
            TkListRow(title: employer, icon: 'b-store', square: look.teal, titleLines: 2, chevron: false),
          ]),
          if (_link != null)
            TkCallout(
              key: const ValueKey('payslip-link'),
              leading: TkSquare('b-share', color: look.teal),
              title: 'Send this to ${p.employee.name}',
              sub: '$_link\nIt works for 7 days, after they type the last four digits of their phone.',
              subLines: 5,
              color: look.teal,
            ),
          TkCta(label: _busy ? 'Making the PDF…' : 'Save as PDF', icon: 'b-receipt', onTap: _busy ? null : () => unawaited(_pdf(p))),
          if (!widget.own)
            Center(
              child: TkSmallButton(
                label: 'Send a link',
                semantics: 'Send a link, for someone not on Taskiem',
                onPressed: _busy ? null : () => unawaited(_sendLink()),
              ),
            ),
        ],
      ],
    );
  }
}

/// My pay's Payslips: one's own, newest first.
class MyPayslipsSection extends StatefulWidget {
  const MyPayslipsSection({super.key, required this.auth});
  final Auth auth;

  @override
  State<MyPayslipsSection> createState() => _MyPayslipsSectionState();
}

class _MyPayslipsSectionState extends State<MyPayslipsSection> {
  GetMyPayslipsResponse? _got;
  Object? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getMyPayslips();
      if (mounted) setState(() => (_got = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final got = _got;
    if (got != null && !got.linked) return const SizedBox.shrink();
    return TkSection(
      title: 'Payslips',
      inset: 56,
      empty: got == null ? (_error == null ? 'Loading your payslips…' : errorWords(_error!)) : 'No payslips yet. They come once a payroll is locked.',
      children: [
        for (final x in got?.payslips ?? const <GetMyPayslipsResponsePayslipsItem>[])
          TkListRow(
            title: x.period,
            sub: x.payDate.isEmpty ? '' : 'Pay day ${shortDay(x.payDate)}',
            icon: 'b-cash',
            square: look.lime,
            amount: naira(x.netKobo),
            status: x.words,
            statusTone: payrollLineState(x.state).tone,
            semanticsLabel: 'Payslip for ${x.period}: ${naira(x.netKobo)}, ${x.words}. Open',
            onTap: () => unawaited(_push(context, PayslipPage(auth: widget.auth, payslip: x.payslipId, own: true, backLabel: 'Back to My pay'))),
          ),
      ],
    );
  }
}

/// A payslip's card in the employee's own Taskiem chat: drawn from
/// Payrolla when shown, for them only.
class PayslipChatCard extends StatefulWidget {
  const PayslipChatCard({super.key, required this.auth, required this.payslip, required this.period});
  final Auth auth;
  final String payslip;
  final String period;

  @override
  State<PayslipChatCard> createState() => _PayslipChatCardState();
}

class _PayslipChatCardState extends State<PayslipChatCard> {
  PayrollPayslip? _slip;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getMyPayslip(payslip: widget.payslip);
      if (mounted) setState(() => _slip = r.payslip);
    } on Object {
      if (mounted) setState(() => _failed = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _slip;
    final month = p?.month ?? (widget.period.split(' ').firstOrNull ?? '');
    return TkSection(
      title: payslipReady(month),
      inset: 56,
      children: [
        if (_failed) const TkListRow(title: 'You can’t see this payslip here', icon: 'b-lock', chevron: false),
        if (p != null)
          TkListRow(
            title: naira(p.netKobo),
            sub: [p.period, p.employer.name].where((s) => s.isNotEmpty).join(' · '),
            subLines: 2,
            icon: 'b-cash',
            square: look.lime,
            status: p.payment.words,
            statusTone: payrollLineState(p.payment.state).tone,
            semanticsLabel: 'Your payslip for ${p.period}: ${naira(p.netKobo)}, ${p.payment.words}. Open',
            onTap: () => unawaited(_push(context, PayslipPage(auth: widget.auth, payslip: widget.payslip, own: true, backLabel: 'Back to the chat'))),
          ),
        if (p == null && !_failed) Padding(padding: const EdgeInsets.all(12), child: Text('Loading your payslip…', style: tkText(size: 14, color: t.muted))),
      ],
    );
  }
}
