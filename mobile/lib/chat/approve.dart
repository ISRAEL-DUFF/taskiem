// Approve or Decline, confirmed (web/src/pages/ChatAlerts.tsx ApproveSheet):
// what it is, what the person was shown, a comment, and the confirmation
// its rules ask for (a code from their authenticator; a passkey once the
// app has them, M4); the server refuses a decision without it, and what it
// asks for is what the sheet asks next. POST /v1/approvals/{run}/{step}.

import 'dart:async';

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show RuneLimit;
import '../kit/kit.dart';
import '../kit/sheet.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'alerts.dart';
import 'cards.dart' show CardPill;
import 'words.dart';

/// The approve sheet; it closes with true once decided.
/// The decision, as the sheet sends it (POST /v1/approvals/{run}/{step}):
/// approved or rejected, with its comment and the code a rule asks for.
Future<void> decideApproval(Auth auth, Approval ap, String decision, {String comment = '', String code = ''}) => auth.transport.send(
  'POST',
  '/v1/approvals/${ap.runId}/${Uri.encodeComponent(ap.stepId)}',
  body: {'decision': decision, if (comment.trim().isNotEmpty) 'comment': comment.trim(), if (code.isNotEmpty) 'totp': code},
);

class ApproveSheet extends StatefulWidget {
  const ApproveSheet({super.key, required this.a, required this.decision, required this.auth});
  final ChatAlert a;

  /// "approved" or "rejected".
  final String decision;
  final Auth auth;

  @override
  State<ApproveSheet> createState() => _ApproveSheetState();
}

class _ApproveSheetState extends State<ApproveSheet> {
  final _comment = TextEditingController();
  final _code = TextEditingController();
  late String? _stepUp = widget.a.approval!.stepUp as String?;
  bool _busy = false;
  bool _saved = false;
  String? _error;

  bool get _approve => widget.decision == 'approved';

  @override
  void initState() {
    super.initState();
    _code.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _comment.dispose();
    _code.dispose();
    super.dispose();
  }

  Future<void> _confirm() async {
    final ap = widget.a.approval!;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await decideApproval(widget.auth, ap, widget.decision, comment: _comment.text, code: _code.text);
      setState(() => _saved = true);
      // The tick draws, then the sheet goes and the card with it.
      await Future<void>.delayed(const Duration(milliseconds: 1200));
      if (mounted) Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      // What the server asks for now is what the sheet asks next.
      final up = e.body['step_up'];
      setState(() {
        if (up is String) _stepUp = up;
        _error = e.message;
      });
    } on Offline {
      setState(() => _error = 'You’re offline. Try again when you’re back online.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = widget.a;
    final ap = a.approval!;
    final w = subjectWords(ap.subject);
    final passkeyOnly = _stepUp == 'passkey';
    final rows = <(String, String)>[
      if (w.amount != null) ('Amount', w.amount!),
      if (w.to != null) ('To', w.to!),
      if (w.account != null) ('Account', w.account!),
      ...w.rest,
    ];
    final notes = <Widget>[
      if (ap.firstPayment == true) const CardPill('First payment to this account', CardTone.warn),
      if (_stepUp != null) CardPill(passkeyOnly ? 'Needs your passkey' : 'Needs your passkey or a code', CardTone.plain),
      if (ap.levels > 1) CardPill('Level ${ap.level} of ${ap.levels}', CardTone.plain),
      if (ap.required > 1) CardPill('${ap.approvals} of ${ap.required} approvals', CardTone.plain),
    ];
    final ready = !_busy && !(_stepUp != null && (passkeyOnly || _code.text.length != 6));
    return TkSheet(
      title: _approve ? 'Approve it?' : 'Decline it?',
      overlay: _saved ? TkSavedTick(words: _approve ? 'Approved' : 'Declined') : null,
      body: TkFormBody(
        children: [
          Text(a.who != null ? '${a.who} asks: ${a.title}' : a.title, style: tkText(size: 16, weight: 600, color: t.text)),
          Text('From ${ap.workflow}. Your decision and what you were shown are recorded.', style: tkFormNow(t)),
          if (notes.isNotEmpty) Wrap(spacing: 6, runSpacing: 6, children: notes),
          if (rows.isNotEmpty) TkLines(rows),
          if (ap.firstPayment == true && _approve)
            Semantics(
              container: true,
              // `.tk-form-now` is a wrapping flex row: the bold words, then the rest under them.
              child: Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  Text('First payment to this account.', style: tkText(size: 14, weight: 700, color: t.muted)),
                  Text(
                    'Your business has not paid it before. Check the account number and name with the person you are paying before you approve.',
                    style: tkFormNow(t),
                  ),
                ],
              ),
            ),
          TkKitField(
            label: 'A comment (optional, recorded)',
            child: TkTextBox(controller: _comment, lines: 2),
          ),
          if (_stepUp != null && !passkeyOnly)
            TkKitField(
              label: 'Or a code from your authenticator app',
              hint: 'No authenticator yet? Add one under Account on the web.',
              child: TkTextBox(controller: _code, lines: 1, digits: 6),
            ),
          if (passkeyOnly) Text('This needs your passkey, and this phone cannot use one yet. Decide it on a device with your passkey.', style: tkFormNow(t)),
          if (_error != null) TkErrorBox(_error!),
        ],
      ),
      footer: TkFootTwo(
        // `.tk-btn.ghost`: 18 px each side and its line; `.tk-cta`: 14 px.
        firstOwn: 38,
        secondOwn: 28,
        first: TkGhostButton(label: 'Not now', onTap: _busy ? null : () => Navigator.of(context).pop(false)),
        second: TkCta(label: _approve ? 'Approve' : 'Decline', danger: !_approve, onTap: ready ? _confirm : null),
      ),
    );
  }
}

/// `.tk-field textarea` / `input`: the soft filled box, 17 px semibold, and
/// Chromium's resize grip in a text area's corner, as the web is seen.
class TkTextBox extends StatefulWidget {
  const TkTextBox({
    super.key,
    required this.controller,
    this.lines = 1,
    this.digits,
    this.height = 54,
    this.size = 17,
    this.hint,
    this.money = false,
    this.secret = false,
    this.plain = false,
    this.phone = false,
    this.max,
  });
  final TextEditingController controller;
  final int lines;

  /// `min-height` and the words' size: 54 and 17, or 44 and 16 in a
  /// card's form (`.tk-deal-form .tk-field input`).
  final double height;
  final double size;

  /// The words shown while it is empty (`placeholder`).
  final String? hint;

  /// A price typed (`inputmode="decimal"`).
  final bool money;

  /// A key (`type="password"`): hidden as typed.
  final bool secret;

  /// Pasted as it is (`autocomplete="off" spellcheck="false"`): no
  /// suggestions, no corrections.
  final bool plain;

  /// A phone number (`type="tel"`): the phone's dial pad.
  final bool phone;

  /// `maxLength`: at most this many characters.
  final int? max;

  /// Only digits, at most this many (a code).
  final int? digits;

  @override
  State<TkTextBox> createState() => _TkTextBoxState();
}

class _TkTextBoxState extends State<TkTextBox> {
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final area = widget.lines > 1;
    return TapField(
      child: Container(
        constraints: BoxConstraints(minHeight: widget.height),
        padding: EdgeInsets.symmetric(horizontal: 16, vertical: area ? 14 : 0),
        alignment: Alignment.centerLeft,
        decoration: BoxDecoration(
          color: _focus.hasFocus ? t.fieldFocus : t.field,
          border: Border.all(color: const Color(0x00000000), width: 1.5),
          borderRadius: BorderRadius.circular(14),
          boxShadow: _focus.hasFocus ? t.ring : const [],
        ),
        child: Stack(
          clipBehavior: Clip.none,
          children: [
            Material(
              type: MaterialType.transparency,
              child: TextField(
                controller: widget.controller,
                focusNode: _focus,
                minLines: widget.lines,
                maxLines: area ? null : 1,
                obscureText: widget.secret && !area,
                autocorrect: !(widget.secret || widget.plain),
                enableSuggestions: !(widget.secret || widget.plain),
                keyboardType: widget.digits != null
                    ? TextInputType.number
                    : widget.phone
                    ? TextInputType.phone
                    : widget.money
                    ? const TextInputType.numberWithOptions(decimal: true)
                    : (area ? TextInputType.multiline : TextInputType.text),
                autofillHints: widget.digits != null ? const [AutofillHints.oneTimeCode] : null,
                inputFormatters: [
                  if (widget.digits != null) ...[FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(widget.digits)],
                  if (widget.max != null) RuneLimit(widget.max!),
                ],
                cursorColor: t.text,
                showCursor: !TkField.debugHideCaret,
                style: tkText(size: widget.size, weight: 600, lineHeight: 1.5, color: t.text),
                decoration: InputDecoration.collapsed(
                  hintText: widget.hint,
                  hintStyle: tkText(
                    size: widget.size,
                    weight: 600,
                    lineHeight: 1.5,
                    color: t.muted.withValues(alpha: t.muted.a * 0.72),
                  ),
                ),
              ),
            ),
            if (area)
              Positioned(
                right: -14,
                bottom: -12,
                child: CustomPaint(size: const Size(9, 9), painter: _Grip(t.muted)),
              ),
          ],
        ),
      ),
    );
  }
}

/// Chromium's resize grip: two short diagonal strokes.
class _Grip extends CustomPainter {
  _Grip(this.ink);
  final Color ink;

  @override
  void paint(Canvas canvas, Size size) {
    final p = Paint()
      ..color = ink.withValues(alpha: 0.7)
      ..strokeWidth = 1;
    canvas.drawLine(Offset(size.width, 0), Offset(0, size.height), p);
    canvas.drawLine(Offset(size.width, size.height * 0.5), Offset(size.width * 0.5, size.height), p);
  }

  @override
  bool shouldRepaint(_Grip old) => old.ink != ink;
}
