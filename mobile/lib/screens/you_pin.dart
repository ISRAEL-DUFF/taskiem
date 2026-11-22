// The person's PIN (GET and PUT /v1/me/pin; chat-pay-people, Settled row
// 3): the one PIN that approves on WhatsApp and pays up to ₦100,000
// (₦200,000 a day) in the Pay sheet's pad and WhatsApp's secure form. Set
// here, never in a message, and never shown: its six digits are dots.
//
// - At sign-up, a new account is offered "Set your PIN" once, with Not now
//   ([PinStepScreen]).
// - In Settings, the PIN's page ([PinScreen]): when it was set, from when
//   it pays, whether it is locked, and Change PIN. A change is confirmed
//   with the passkey (it then pays at once), or without one within 10
//   minutes of signing in (it then pays 24 hours on).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../chat/approve.dart' show TkTextBox;
import '../chat/groups.dart' show askSure;
import '../chat/words.dart' show clock, dayWords;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/kit.dart' show TkLink;
import '../kit/sheet.dart' show TkCta, TkKitField, TkLines;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'settings_more.dart' show factorsOf;
import 'sign_in.dart' show CodeBoxes;

/// What the PIN is for, said under the boxes.
const pinUses = 'It approves on WhatsApp, and pays small amounts (up to ₦100,000, ₦200,000 a day) when your passkey is not to hand. '
    'It can pay from 24 hours after it is set. Taskiem never asks for it in a message.';

/// Changing it with no passkey on this phone and no sign-in moments ago.
const pinSignInAgain = 'Sign out and sign in again, then change it within 10 minutes; it can pay 24 hours later.';

/// One digit repeated, or a run up or down (123456, 987654, 890123), as
/// the server refuses.
bool weakPin(String pin) {
  var same = true, up = true, down = true;
  for (var i = 1; i < pin.length; i++) {
    final d = pin.codeUnitAt(i) - pin.codeUnitAt(i - 1);
    same = same && d == 0;
    up = up && (d == 1 || d == -9);
    down = down && (d == -1 || d == 9);
  }
  return same || up || down;
}

/// What is wrong with the two entries, said before anything is sent.
String? pinProblem(String pin, String again) {
  if (!RegExp(r'^[0-9]{6}$').hasMatch(pin)) return 'Type the 6 digits of your new PIN.';
  if (weakPin(pin)) return 'Choose a PIN that is not one digit repeated or a run like 123456.';
  if (again.length != 6) return 'Type your new PIN again, to check it.';
  if (pin != again) return 'The two PINs are not the same. Type them again.';
  return null;
}

/// "today at 10:30", "tomorrow at 10:30", "Tue 14 Oct at 10:30".
String pinWhen(DateTime d, DateTime now) {
  final l = d.toLocal(), n = now.toLocal();
  final days = DateTime(l.year, l.month, l.day).difference(DateTime(n.year, n.month, n.day)).inHours ~/ 24;
  final day = switch (days) {
    0 => 'today',
    1 => 'tomorrow',
    -1 => 'yesterday',
    _ => dayWords(d, now),
  };
  return '$day at ${clock(d)}';
}

String _say(Object e) => switch (e) {
  ApiError() => e.message,
  Offline() => 'You’re offline. Try again when you’re back online.',
  _ => 'Something went wrong. Try again.',
};

/// The new PIN, typed twice as dots in six boxes; [onSave] with it once
/// the two agree and it is not a weak one.
class PinForm extends StatefulWidget {
  const PinForm({super.key, required this.action, required this.onSave, this.extra, this.busy = false, this.error, this.hint = pinUses});
  final String action;
  final Future<void> Function(String pin) onSave;

  /// More under the boxes (an authenticator code's box).
  final Widget? extra;
  final bool busy;

  /// The server's refusal, in its words.
  final String? error;
  final String hint;

  @override
  State<PinForm> createState() => _PinFormState();
}

class _PinFormState extends State<PinForm> {
  final _pin = TextEditingController();
  final _again = TextEditingController();
  String? _problem;

  @override
  void dispose() {
    _pin.dispose();
    _again.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final problem = pinProblem(_pin.text, _again.text);
    setState(() => _problem = problem);
    if (problem != null) {
      if (_pin.text != _again.text && _again.text.length == 6) _again.clear();
      return;
    }
    await widget.onSave(_pin.text);
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final error = _problem ?? widget.error;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: 'New PIN',
              field: false,
              child: CodeBoxes(key: const ValueKey('pin-new'), controller: _pin, obscure: true, hint: 'New PIN', onChanged: (_) => setState(() => _problem = null)),
            ),
            TkKitField(
              label: 'The same PIN again',
              field: false,
              hint: widget.hint,
              child: CodeBoxes(key: const ValueKey('pin-again'), controller: _again, obscure: true, hint: 'New PIN again', onChanged: (_) => setState(() => _problem = null), onSubmitted: (_) => _save()),
            ),
            ?widget.extra,
          ],
        ),
        if (error != null) ...[
          const SizedBox(height: 14),
          Semantics(
            liveRegion: true,
            child: TkCallout(leading: TkSquare('b-x', color: look.red), title: 'Not saved', sub: error, subLines: 4, color: look.red),
          ),
        ],
        const SizedBox(height: 14),
        TkCta(label: widget.busy ? 'Saving…' : widget.action, onTap: widget.busy ? null : _save),
      ],
    );
  }
}

/// At sign-up, once the phone is proven: the PIN offered when there is
/// none (GET /v1/me/pin `set: false`), with Not now. Anything else (one
/// already set, the server not answering) goes straight on with [onDone].
class PinStepScreen extends StatefulWidget {
  const PinStepScreen({super.key, required this.auth, required this.onDone, this.now = DateTime.now});
  final Auth auth;
  final VoidCallback onDone;
  final DateTime Function() now;

  @override
  State<PinStepScreen> createState() => _PinStepScreenState();
}

class _PinStepScreenState extends State<PinStepScreen> {
  bool _asking = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _check();
  }

  Future<void> _check() async {
    try {
      final p = await widget.auth.api.getMePin();
      if (!mounted) return;
      if (p.set) return widget.onDone();
      setState(() => _asking = true);
    } on Object {
      if (mounted) widget.onDone();
    }
  }

  Future<void> _save(String pin) async {
    setState(() => (_busy = true, _error = null));
    try {
      final p = await widget.auth.api.putMePin(body: PutMePinRequest(pin: pin));
      if (!mounted) return;
      final from = p.paysFrom;
      showTkToast(context, from != null && from.isAfter(widget.now()) ? 'Your PIN is set. It can pay from ${pinWhen(from, widget.now())}.' : 'Your PIN is set.');
      widget.onDone();
      return;
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    // Nothing shown while the server is asked: for someone with a PIN, the
    // app opens as it always does.
    if (!_asking) return ColoredBox(color: TkLook.of(context).ground, child: const SizedBox.expand());
    return TkPageScreen(
      title: 'Set your PIN',
      titleSize: 30,
      back: false,
      description: 'Six digits of your own, for approving on WhatsApp and paying small amounts. You can set it later in Settings.',
      children: [
        PinForm(action: 'Set your PIN', onSave: _save, busy: _busy, error: _error),
        Center(child: TkLink(label: 'Not now', bold: true, onTap: _busy ? null : widget.onDone)),
      ],
    );
  }
}

/// Settings › PIN: when it was set, from when it pays, whether it is
/// locked; Set your PIN, or Change PIN.
class PinScreen extends StatefulWidget {
  const PinScreen({super.key, required this.auth, this.now = DateTime.now, this.backLabel = 'Back to Settings'});
  final Auth auth;
  final DateTime Function() now;
  final String backLabel;

  @override
  State<PinScreen> createState() => _PinScreenState();
}

class _PinScreenState extends State<PinScreen> {
  MePin? _pin;
  String? _loadError;
  bool _changing = false;
  bool _busy = false;
  String? _error;

  /// This phone can confirm with the person's passkey.
  bool _passkey = false;
  final _totp = TextEditingController();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _totp.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() => _loadError = null);
    final has = factorsOf(widget.auth).passkey && await passkeys.available();
    try {
      final p = await widget.auth.api.getMePin();
      if (mounted) setState(() => (_pin = p, _passkey = has));
    } on Object catch (e) {
      if (mounted) setState(() => _loadError = _say(e));
    }
  }

  bool get _totpOnly => !_passkey && factorsOf(widget.auth).totp;

  Future<void> _save(String pin) async {
    final p = _pin!;
    setState(() => (_busy = true, _error = null));
    try {
      final PutMePinRequest body;
      if (!p.set || p.changeWithout) {
        body = PutMePinRequest(pin: pin);
      } else if (_passkey) {
        // The passkey first (it then pays at once), then the PIN saved.
        body = PutMePinRequest(pin: pin, passkey: await passkeyProof(widget.auth, 'account.reauth', 'pin.set'));
      } else {
        final code = _totp.text.trim();
        if (code.length != 6) {
          setState(() => (_busy = false, _error = 'Type the 6-digit code from your authenticator app.'));
          return;
        }
        body = PutMePinRequest(pin: pin, totp: code);
      }
      final now = await widget.auth.api.putMePin(body: body);
      if (!mounted) return;
      final from = now.paysFrom;
      final later = from != null && from.isAfter(widget.now());
      showTkToast(context, '${p.set ? 'Your new PIN is saved.' : 'Your PIN is set.'}${later ? ' It can pay from ${pinWhen(from, widget.now())}.' : ''}');
      _totp.clear();
      setState(() => (_pin = now, _changing = false, _busy = false));
      return;
    } on PasskeyCancelled {
      // Put away: nothing to say.
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  /// Removing it takes no proof: it only takes a way to approve and pay
  /// (and the next one is set as a change is, not as a first PIN).
  Future<void> _remove() async {
    final yes = await askSure(context, title: 'Remove your PIN?', body: 'It will no longer approve on WhatsApp or pay. Setting one again takes your passkey, or signing in again.', ok: 'Remove', danger: true);
    if (!yes || !mounted) return;
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.api.deleteMePin();
      if (mounted) showTkToast(context, 'PIN removed');
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final t = TkTheme.tokens(context);
    final p = _pin;
    final now = widget.now();
    final List<Widget> body;
    if (p == null) {
      body = [
        if (_loadError != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: 'Your PIN could not be read',
            sub: _loadError,
            color: look.orange,
            button: 'Try again',
            onPressed: _load,
          ),
      ];
    } else if (!p.set) {
      body = [PinForm(action: 'Set your PIN', onSave: _save, busy: _busy, error: _error)];
    } else {
      final locked = p.lockedUntil != null && p.lockedUntil!.isAfter(now) ? p.lockedUntil : null;
      final later = p.paysFrom != null && p.paysFrom!.isAfter(now) ? p.paysFrom : null;
      final until = p.changeWithoutUntil;
      body = [
        TkFormGroup(
          children: [
            TkLines([
              ('PIN', '••••••'),
              if (p.setAt != null) ('Set', pinWhen(p.setAt!, now)),
              ('Pays', later == null ? 'Now, up to ₦100,000' : 'From ${pinWhen(later, now)}'),
            ]),
          ],
        ),
        if (locked != null)
          TkCallout(
            leading: TkSquare('b-lock', color: look.red),
            title: 'Locked until ${pinWhen(locked, now)}',
            sub: 'Too many wrong tries. Until then, confirm with your passkey.',
            subLines: 3,
            color: look.red,
          ),
        if (later != null)
          TkCallout(
            leading: TkSquare('b-clock', color: look.orange),
            title: 'Can pay from ${pinWhen(later, now)}',
            sub: 'Set without your passkey, so it waits 24 hours. It approves on WhatsApp now.',
            subLines: 3,
            color: look.orange,
          ),
        if (!_changing) ...[
          TkCta(label: 'Change PIN', onTap: () => setState(() => (_changing = true, _error = null))),
          Center(child: TkLink(label: 'Remove my PIN', bold: true, onTap: _busy ? null : _remove)),
        ]
        else if (!p.changeWithout && !_passkey && !_totpOnly)
          Semantics(
            liveRegion: true,
            child: TkCallout(leading: TkSquare('b-key', color: look.violet), title: 'No passkey on this phone', sub: pinSignInAgain, subLines: 4, color: look.violet),
          )
        else
          PinForm(
                action: p.changeWithout || !_passkey ? 'Save the new PIN' : 'Confirm with your passkey and save',
                onSave: _save,
                busy: _busy,
                error: _error,
                hint: p.changeWithout
                    ? 'You signed in moments ago, so no passkey is needed${until == null ? '' : ' until ${clock(until)}'}. The new PIN can pay 24 hours after it is saved.'
                    : (_passkey ? 'Confirmed with your passkey, it can pay at once.' : 'Confirmed with your authenticator code, it can pay at once.'),
                extra: !p.changeWithout && _totpOnly
                    ? TkKitField(label: 'A code from your authenticator app', child: TkTextBox(key: const ValueKey('pin-totp'), controller: _totp, digits: 6))
                    : null,
              ),
        if (_changing) Center(child: TkLink(label: 'Cancel', bold: true, onTap: _busy ? null : () => setState(() => (_changing = false, _error = null)))),
        Text(
          'Your PIN is never shown, and Taskiem never asks for it in a message.',
          style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
        ),
      ];
    }
    return TkPageScreen(
      title: p != null && !p.set ? 'Set your PIN' : 'PIN',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: pinUses,
      children: body,
    );
  }
}
