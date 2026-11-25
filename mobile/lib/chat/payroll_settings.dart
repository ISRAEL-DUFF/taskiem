// Payroll settings (P4b-1, docs/spec/chat-pay-people.md): how Taskiem
// reaches the business's Payrolla (connected in one step, with a key, or
// ended), and who may approve the payroll in Payrolla, with Link my
// Payrolla account by one code Payrolla sends to that account's own phone,
// email or authenticator app (GET /v1/payroll/connection,
// /v1/payroll/approvers, POST …/link and …/link/confirm, DELETE …/link).
// Connecting and reconnecting happen on Taskiem's web: Payrolla sends the
// browser back there, bound to the browser that started it. The code is
// sent to Payrolla once and kept nowhere.

import 'dart:async';
import 'dart:math';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/links.dart' show openOutside;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show openOrSay, showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart';
import '../screens/sign_in.dart' show CodeBoxes;
import '../theme/look.dart';
import 'groups.dart' show askSure;
import 'payroll_schedules.dart' show openPayrollSetup;

/// How the business's Payrolla is connected.
enum PayrollConnectionState { none, ended, key, oneStep }

PayrollConnectionState payrollConnectionState(GetPayrollConnectionResponse c) {
  final conn = c.connection;
  if (conn == null) return PayrollConnectionState.none;
  if (c.ended == true || conn.status == 'expired') return PayrollConnectionState.ended;
  return conn.auth == 'key' ? PayrollConnectionState.key : PayrollConnectionState.oneStep;
}

/// An approver's line: their role and what Payrolla lets them do.
String payrollApproverSub(PayrollApprover a) {
  final role = switch (a.role) { 'OWNER' => 'Owner', 'ADMIN' => 'Admin', 'APPROVER' => 'Approver', _ => 'In Payrolla' };
  final can = [if (a.canApprove) 'approves', if (a.canPay) 'pays'].join(' and ');
  return [role, if (can.isNotEmpty) can[0].toUpperCase() + can.substring(1), if (!a.canConfirm) 'No phone for codes yet'].join(' · ');
}

String _linkedName(PayrollApprover a) {
  final l = a.linked;
  return l is Map ? (l['name'] is String ? l['name'] as String : '') : '';
}

/// Who an approver is linked to in Taskiem, in words.
String payrollLinkedWords(PayrollApprover a) {
  if (a.linked == null) return a.suggested ? 'Looks like you (same email)' : 'Not linked';
  return a.me ? 'Linked to you' : 'Linked to ${_linkedName(a)}';
}

/// An id for one submission, kept through its retries: the server makes
/// Payrolla's Idempotency-Key from it.
String payrollKey() {
  final r = Random.secure();
  return List.generate(16, (_) => r.nextInt(256).toRadixString(16).padLeft(2, '0')).join();
}

class PayrollSettingsPage extends StatefulWidget {
  const PayrollSettingsPage({super.key, required this.auth, this.backLabel = 'Back to Payroll'});
  final Auth auth;
  final String backLabel;

  @override
  State<PayrollSettingsPage> createState() => _PayrollSettingsPageState();
}

class _PayrollSettingsPageState extends State<PayrollSettingsPage> {
  GetPayrollConnectionResponse? _conn;
  GetPayrollApproversResponse? _approvers;
  Object? _error;
  Object? _approversError;
  bool _busy = false;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final c = await _auth.api.getPayrollConnection();
      if (!mounted) return;
      setState(() => (_conn = c, _error = null));
      if (payrollConnectionState(c) == PayrollConnectionState.oneStep && (c.connection?.scopes ?? const []).contains('payroll:approve')) await _loadApprovers();
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _loadApprovers() async {
    try {
      final a = await _auth.api.getPayrollApprovers();
      if (mounted) setState(() => (_approvers = a, _approversError = null));
    } on Object catch (e) {
      if (mounted) setState(() => _approversError = e);
    }
  }

  /// Connecting happens on Taskiem's web: Payrolla sends the browser back
  /// there.
  Future<void> _openWeb() =>
      openOrSay(context, () => openOutside(Uri.parse('${_auth.transport.base}/payroll/settings')), 'Taskiem on the web could not be opened.');

  Future<void> _link(PayrollApprover a) async {
    final linked = await showTkSheet<bool>(context, (_) => PayrollLinkSheet(auth: _auth, approver: a));
    if (linked == true && mounted) {
      showTkToast(context, 'Your Payrolla account is linked');
      await _loadApprovers();
    }
  }

  Future<void> _unlink(PayrollApprover a) async {
    final sure = await askSure(context,
        title: a.me ? 'Unlink your Payrolla account?' : 'Unlink ${_linkedName(a)}?', body: 'Approving from Taskiem then needs the account linked again, with a code.', ok: 'Unlink', danger: true);
    if (!sure || !mounted) return;
    setState(() => _busy = true);
    try {
      await _auth.api.unlinkPayroll(user: a.userId, key: payrollKey());
      if (mounted) showTkToast(context, 'Unlinked');
      await _loadApprovers();
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  List<Widget> _connection(BuildContext context, GetPayrollConnectionResponse c) {
    final look = TkLook.of(context);
    final owner = c.isOwner;
    final company = c.connection?.company?.name ?? '';
    Widget webButton(String label) =>
        Align(alignment: AlignmentDirectional.centerStart, child: TkSmallButton(label: label, primary: true, onPressed: () => unawaited(_openWeb())));
    switch (payrollConnectionState(c)) {
      case PayrollConnectionState.none:
        return [
          TkCallout(
            leading: TkSquare('b-calendar', color: look.violet),
            title: 'Connect Payrolla',
            sub: owner
                ? 'Connect in one step on Taskiem’s web: you allow Taskiem in Payrolla, and can sign up there if you are not on Payrolla yet.'
                : 'Ask the owner to connect Payrolla.',
            subLines: 4,
            color: look.violet,
          ),
          if (owner) webButton('Connect Payrolla'),
        ];
      case PayrollConnectionState.ended:
        return [
          TkCallout(
            leading: TkSquare('b-alert', color: look.red),
            title: owner ? 'Payrolla is no longer connected' : 'Payroll isn’t connected right now',
            sub: owner ? 'Connect again to see and run your payroll here.' : 'The owner has been told.',
            subLines: 3,
            color: look.red,
          ),
          if (owner) webButton('Reconnect Payrolla'),
        ];
      case PayrollConnectionState.key:
        return [
          TkSection(title: 'Payrolla', inset: 56, children: [
            TkListRow(title: company.isEmpty ? 'Your company' : company, sub: 'Connected with a key', icon: 'b-calendar', square: look.teal, chevron: false),
            TkListRow(
              title: 'How payroll is set up',
              sub: 'Pay days, tax and pension',
              subLines: 1,
              icon: 'b-tools',
              square: look.teal,
              onTap: () => unawaited(openPayrollSetup(context, _auth)),
            ),
          ]),
          if (owner) ...[
            TkCallout(
              leading: TkSquare('b-alert', color: look.orange),
              title: 'Reconnect Payrolla',
              sub: 'Connect again in one step so you can run payroll here.',
              subLines: 3,
              color: look.orange,
            ),
            webButton('Reconnect Payrolla'),
          ],
        ];
      case PayrollConnectionState.oneStep:
        return [
          TkSection(title: 'Payrolla', inset: 56, children: [
            TkListRow(
              title: company.isEmpty ? 'Your company' : company,
              sub: c.connection?.test == true ? 'Connected · Test, no real money' : 'Connected',
              icon: 'b-calendar',
              square: look.lime,
              chevron: false,
            ),
            TkListRow(
              title: 'How payroll is set up',
              sub: 'Pay days, tax and pension',
              subLines: 1,
              icon: 'b-tools',
              square: look.teal,
              onTap: () => unawaited(openPayrollSetup(context, _auth)),
            ),
          ]),
        ];
    }
  }

  Widget _approver(BuildContext context, PayrollApprover a, GetPayrollApproversResponse list) {
    final look = TkLook.of(context);
    final mayLink = list.mayLink && list.me == null && a.linked == null && a.canConfirm;
    final mayUnlink = a.linked != null && (a.me || list.isOwner);
    return TkListRow(
      title: a.name,
      letters: a.name,
      sub: '${payrollApproverSub(a)} · ${payrollLinkedWords(a)}',
      subLines: 3,
      square: a.me ? look.lime : null,
      chevron: false,
      semanticsLabel: '${a.name}: ${payrollApproverSub(a)}, ${payrollLinkedWords(a)}',
      trailing: mayLink
          ? TkSmallButton(label: 'Link', primary: a.suggested, semantics: 'Link my Payrolla account: ${a.name}', onPressed: () => unawaited(_link(a)))
          : mayUnlink
              ? TkSmallButton(label: 'Unlink', semantics: 'Unlink ${a.name}', onPressed: _busy ? null : () => unawaited(_unlink(a)))
              : null,
    );
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final c = _conn;
    final list = _approvers;
    final rows = [...?list?.approvers]..sort((a, b) => (b.me ? 2 : b.suggested ? 1 : 0) - (a.me ? 2 : a.suggested ? 1 : 0));
    return TkPageScreen(
      title: 'Payroll settings',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'How Taskiem reaches your Payrolla, and who approves your payroll. Approving from Taskiem is done as your own Payrolla account, linked once with a code.',
      onRefresh: _load,
      children: [
        if (_error != null)
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'Payroll settings did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (c == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading…'),
        if (c != null) ..._connection(context, c),
        if (_approversError != null)
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'Who can approve did not load', sub: errorWords(_approversError!), subLines: 4, color: look.red),
        if (list != null)
          TkSection(
            title: 'Who can approve in Payrolla',
            inset: 56,
            empty: 'No one in Payrolla can approve payroll yet.',
            children: [for (final a in rows) _approver(context, a, list)],
          ),
      ],
    );
  }
}

/// Link my Payrolla account: Payrolla sends a code to that account's own
/// phone, email or authenticator app, and the person types it here. Closes
/// with true once linked.
class PayrollLinkSheet extends StatefulWidget {
  const PayrollLinkSheet({super.key, required this.auth, required this.approver});
  final Auth auth;
  final PayrollApprover approver;

  @override
  State<PayrollLinkSheet> createState() => _PayrollLinkSheetState();
}

class _PayrollLinkSheetState extends State<PayrollLinkSheet> {
  final _code = TextEditingController();
  StartPayrollLinkResponse? _sent;
  String? _problem;
  bool _needNew = false;
  bool _busy = false;
  String _key = payrollKey();

  @override
  void dispose() {
    _code.dispose();
    super.dispose();
  }

  Future<void> _send({bool again = false}) async {
    if (again) _key = payrollKey();
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.startPayrollLink(user: widget.approver.userId, body: StartPayrollLinkRequest(key: _key));
      if (!mounted) return;
      _code.clear();
      setState(() => (_sent = r, _needNew = false));
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _confirm() async {
    final sent = _sent;
    final code = _code.text.replaceAll(RegExp(r'\D'), '');
    if (sent == null || code.length != 6) return;
    setState(() => (_busy = true, _problem = null));
    try {
      // Each try at a code is its own submission.
      await widget.auth.api.confirmPayrollLink(
          user: widget.approver.userId, body: ConfirmPayrollLinkRequest(challengeId: sent.challenge.challengeId, code: code, key: payrollKey()));
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      final c = e is ApiError ? e.body['code'] : null;
      if (mounted) setState(() => (_problem = errorWords(e), _needNew = c == 'code_expired' || c == 'code_locked' || c == 'code_used'));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final sent = _sent;
    final who = widget.approver.name;
    return TkFormSheet(
      title: 'Link your Payrolla account',
      crumb: who,
      action: sent == null ? (_busy ? 'Sending…' : 'Send me a code') : (_needNew ? 'Send a new code' : 'Link my account'),
      onAction: _busy ? null : () => unawaited(sent == null || _needNew ? _send(again: _needNew) : _confirm()),
      second: sent != null && !_needNew ? 'Send a new code' : null,
      onSecond: sent != null && !_needNew && !_busy ? () => unawaited(_send(again: true)) : null,
      children: [
        if (sent == null)
          TkCallout(
            leading: TkSquare('b-lock', color: look.teal),
            title: 'A code from Payrolla',
            sub: 'Payrolla sends a code to $who’s own phone, email or authenticator app. Only they can finish this.',
            subLines: 4,
            color: look.teal,
          ),
        if (sent != null) ...[
          TkCallout(leading: TkSquare('b-lock', color: look.teal), title: sent.sentTo, sub: 'Type it here. Taskiem sends it to Payrolla once and keeps it nowhere.', subLines: 4, color: look.teal),
          TkFormGroup(children: [
            TkKitField(
              label: 'The code from Payrolla',
              hint: sent.safer ? 'A code from an authenticator app is safer: set one up in Payrolla.' : null,
              field: false,
              child: CodeBoxes(key: const ValueKey('payroll-link-code'), controller: _code, hint: 'The code from Payrolla', onChanged: (_) => setState(() {})),
            ),
          ]),
        ],
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}
