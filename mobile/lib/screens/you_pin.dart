// The person's PIN (GET and PUT /v1/me/pin; docs/spec/transaction-pin.md):
// the one 4-digit PIN that approves on WhatsApp and pays up to ₦100,000
// (₦200,000 a day) in the Pay sheet's pad and WhatsApp's secure form. Set
// here, never in a message, and never shown: its digits are dots.
//
// Setting or changing it is confirmed by a code sent to the person's phone
// (POST /v1/me/pin/code), or by their passkey on this phone, or an
// authenticator code; whichever it is, the PIN pays at once.
//
// - At sign-up, a new account is offered "Set your PIN" once, with Not now
//   ([PinStepScreen]).
// - In Settings, the PIN's page ([PinScreen]): when it was set, whether it
//   is locked, Change PIN and Remove my PIN.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../app/phone.dart' show codeProblem;
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
import 'sign_in.dart' show CodeBoxes;
import 'you.dart' show ChangeNumberScreen;

/// How many digits a PIN is.
const pinDigits = 4;

/// What the PIN is for, said under the boxes.
const pinUses = 'It approves on WhatsApp, and pays small amounts (up to ₦100,000, ₦200,000 a day) when your passkey is not to hand. '
    'It can pay as soon as it is set. Taskiem never asks for it in a message.';

/// No phone to send a code to, and no passkey on this phone.
const pinNoWay = 'Add your phone number first: we send a code to it to check it is you.';

/// One digit repeated, or a run up or down (1234, 9876, 8901), as the
/// server refuses.
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
  if (!RegExp('^[0-9]{$pinDigits}\$').hasMatch(pin)) return 'Type the $pinDigits digits of your new PIN.';
  if (weakPin(pin)) return 'Choose a PIN that is not one digit repeated or a run like 1234.';
  if (again.length != pinDigits) return 'Type your new PIN again, to check it.';
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

/// A failure that is not the server's answer, in a few words a person can
/// pass on: what kind of failure it was and its first words, so "could not
/// be read" says why (an answer the app could not read, the phone's
/// fingerprint chip refusing). Never the answer itself.
String whatWentWrong(Object e) {
  final words = e.toString().replaceAll(RegExp(r'\s+'), ' ').trim();
  final short = words.length > 120 ? '${words.substring(0, 120)}…' : words;
  return short.isEmpty ? '${e.runtimeType}' : short;
}

String _say(Object e) => switch (e) {
  ApiError() when e.has('code_wrong') || e.has('locked') || e.status == 429 => codeProblem(e),
  ApiError() => e.message,
  Offline() => 'You’re offline. Try again when you’re back online.',
  _ => 'Something went wrong (${whatWentWrong(e)}). Try again.',
};

/// How setting the PIN is confirmed here: a code to the phone first (what
/// everyone has), else the passkey on this phone ([byPasskey] when the
/// person chose it), else an authenticator code; null when none can.
String? pinWay(MePin p, {required bool passkey, bool byPasskey = false}) {
  final ways = p.ways;
  final canPasskey = ways.contains('passkey') && passkey;
  if (ways.contains('code') && !(byPasskey && canPasskey)) return 'code';
  if (canPasskey) return 'passkey';
  if (ways.contains('totp')) return 'totp';
  return null;
}

/// The new PIN, typed twice as dots in four boxes, then confirmed: with a
/// code sent to the phone (Send me a code, then its six boxes), the
/// passkey on this phone, or an authenticator code. [onSaved] with the
/// PIN as the server has it once saved.
class PinForm extends StatefulWidget {
  const PinForm({super.key, required this.auth, required this.pin, required this.passkey, required this.onSaved, required this.first, this.hint = pinUses, this.onBusy});
  final Auth auth;
  final MePin pin;

  /// This phone can confirm with the person's passkey.
  final bool passkey;
  final ValueChanged<MePin> onSaved;

  /// No PIN is set yet: "Set your PIN", not "Save the new PIN".
  final bool first;
  final String hint;
  final ValueChanged<bool>? onBusy;

  @override
  State<PinForm> createState() => _PinFormState();
}

class _PinFormState extends State<PinForm> {
  final _pin = TextEditingController();
  final _again = TextEditingController();
  final _code = TextEditingController();
  final _totp = TextEditingController();
  String? _problem;
  bool _busy = false;
  bool _byPasskey = false;

  /// Where the code went (masked), once sent.
  String? _sentTo;
  String? _sentVia;

  @override
  void dispose() {
    _pin.dispose();
    _again.dispose();
    _code.dispose();
    _totp.dispose();
    super.dispose();
  }

  String? get _way => pinWay(widget.pin, passkey: widget.passkey, byPasskey: _byPasskey);

  void _setBusy(bool b) {
    setState(() => _busy = b);
    widget.onBusy?.call(b);
  }

  bool _checked() {
    final problem = pinProblem(_pin.text, _again.text);
    setState(() => _problem = problem);
    if (problem != null && _pin.text != _again.text && _again.text.length == pinDigits) _again.clear();
    return problem == null;
  }

  Future<void> _sendCode() async {
    if (!_checked()) return;
    _setBusy(true);
    try {
      final r = await widget.auth.api.mePinCode(body: const MePinCodeRequest());
      if (!mounted) return;
      _code.clear();
      setState(() => (_sentTo = r.codeTo, _sentVia = r.sentVia));
    } on Object catch (e) {
      if (mounted) setState(() => _problem = _say(e));
    }
    if (mounted) _setBusy(false);
  }

  Future<void> _save() async {
    if (!_checked()) return;
    final way = _way;
    _setBusy(true);
    try {
      final PutMePinRequest body;
      switch (way) {
        case 'passkey':
          body = PutMePinRequest(pin: _pin.text, passkey: await passkeyProof(widget.auth, 'account.reauth', 'pin.set'));
        case 'code':
          final code = _code.text.trim();
          if (code.length != 6) {
            setState(() => _problem = 'Type the 6-digit code we sent.');
            _setBusy(false);
            return;
          }
          body = PutMePinRequest(pin: _pin.text, code: code);
        default:
          final code = _totp.text.trim();
          if (code.length != 6) {
            setState(() => _problem = 'Type the 6-digit code from your authenticator app.');
            _setBusy(false);
            return;
          }
          body = PutMePinRequest(pin: _pin.text, totp: code);
      }
      final now = await widget.auth.api.putMePin(body: body);
      if (!mounted) return;
      _setBusy(false);
      widget.onSaved(now);
      return;
    } on PasskeyCancelled {
      // Put away: nothing to say.
    } on Object catch (e) {
      if (mounted) {
        if (e is ApiError && e.has('code_wrong')) _code.clear();
        setState(() => _problem = _say(e));
      }
    }
    if (mounted) _setBusy(false);
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final t = TkTheme.tokens(context);
    final way = _way;
    final sent = way == 'code' && _sentTo != null;
    final String action;
    if (way == 'code' && !sent) {
      action = 'Send me a code';
    } else if (way == 'passkey') {
      action = 'Confirm with your passkey and save';
    } else {
      action = widget.first ? 'Set your PIN' : 'Save the new PIN';
    }
    final hint = switch (way) {
      'code' => '${widget.hint} We send a code to ${widget.pin.codeTo ?? 'your phone'} to check it is you.',
      _ => widget.hint,
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: widget.first ? 'Your PIN' : 'New PIN',
              field: false,
              child: CodeBoxes(key: const ValueKey('pin-new'), controller: _pin, length: pinDigits, obscure: true, hint: 'New PIN', onChanged: (_) => setState(() => (_problem = null, _sentTo = null))),
            ),
            TkKitField(
              label: 'The same PIN again',
              field: false,
              hint: sent ? null : hint,
              child: CodeBoxes(
                key: const ValueKey('pin-again'),
                controller: _again,
                length: pinDigits,
                obscure: true,
                hint: 'New PIN again',
                onChanged: (_) => setState(() => (_problem = null, _sentTo = null)),
                onSubmitted: (_) => way == 'code' && !sent ? _sendCode() : _save(),
              ),
            ),
            if (way == 'totp') TkKitField(label: 'A code from your authenticator app', child: TkTextBox(key: const ValueKey('pin-totp'), controller: _totp, digits: 6)),
          ],
        ),
        if (sent) ...[
          const SizedBox(height: 14),
          TkFormGroup(
            title: 'The code we sent',
            children: [
              TkKitField(
                label: 'Code',
                field: false,
                hint: 'Sent to $_sentTo${_sentVia == 'sms' ? ' by SMS' : ' on WhatsApp'}. It works for 10 minutes, for your PIN only.',
                child: CodeBoxes(key: const ValueKey('pin-code'), controller: _code, hint: 'Code', onChanged: (_) => setState(() => _problem = null), onSubmitted: (_) => _save()),
              ),
            ],
          ),
        ],
        if (_problem != null) ...[
          const SizedBox(height: 14),
          Semantics(
            liveRegion: true,
            child: TkCallout(leading: TkSquare('b-x', color: look.red), title: 'Not saved', sub: _problem, subLines: 4, color: look.red),
          ),
        ],
        const SizedBox(height: 14),
        TkCta(label: _busy ? (way == 'code' && !sent ? 'Sending…' : 'Saving…') : action, onTap: _busy ? null : (way == 'code' && !sent ? _sendCode : _save)),
        if (sent) Center(child: TkLink(label: 'Send the code again', bold: true, onTap: _busy ? null : _sendCode)),
        if (way == 'passkey' && widget.pin.ways.contains('code'))
          Center(child: TkLink(label: 'Use a code to my phone instead', bold: true, onTap: _busy ? null : () => setState(() => (_byPasskey = false, _problem = null)))),
        if (way == 'code' && !sent && widget.passkey && widget.pin.ways.contains('passkey'))
          Center(child: TkLink(label: 'Use my passkey instead', bold: true, onTap: _busy ? null : () => setState(() => (_byPasskey = true, _problem = null)))),
        if (way == 'code' && !sent)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text('Your PIN is saved once you type the code.', textAlign: TextAlign.center, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          ),
      ],
    );
  }
}

/// No way to confirm a PIN yet: a phone to add, right here; [onBack]
/// once its page is closed, to look again.
class _NoWay extends StatelessWidget {
  const _NoWay({required this.auth, required this.onBack});
  final Auth auth;
  final VoidCallback onBack;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Semantics(
          liveRegion: true,
          child: TkCallout(leading: TkSquare('b-phone', color: look.violet), title: 'Add your phone first', sub: pinNoWay, subLines: 4, color: look.violet),
        ),
        const SizedBox(height: 14),
        TkCta(
          label: 'Add my phone',
          onTap: () async {
            await tkPush<void>(context, ChangeNumberScreen(auth: auth));
            onBack();
          },
        ),
      ],
    );
  }
}

/// The note that the old 6-digit PIN went.
class _Renew extends StatelessWidget {
  const _Renew();

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkCallout(
      leading: TkSquare('b-key', color: look.orange),
      title: 'PINs are now 4 digits',
      sub: 'Your old PIN was removed. Set a new one to approve and pay with it.',
      subLines: 3,
      color: look.orange,
    );
  }
}

/// This phone can confirm with the person's passkey: not offered in the
/// app, where a passkey does not work yet; the code to the phone does.
Future<bool> _phonePasskey(Auth auth) async => false;

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
  MePin? _pin;
  bool _passkey = false;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _check();
  }

  Future<void> _check() async {
    try {
      final has = await _phonePasskey(widget.auth);
      final p = await widget.auth.api.getMePin();
      if (!mounted) return;
      if (p.set) return widget.onDone();
      setState(() => (_pin = p, _passkey = has));
    } on Object {
      if (mounted) widget.onDone();
    }
  }

  void _saved(MePin p) {
    showTkToast(context, 'Your PIN is set. It can pay now.');
    widget.onDone();
  }

  @override
  Widget build(BuildContext context) {
    // Nothing shown while the server is asked: for someone with a PIN, the
    // app opens as it always does.
    final p = _pin;
    if (p == null) return ColoredBox(color: TkLook.of(context).ground, child: const SizedBox.expand());
    final way = pinWay(p, passkey: _passkey);
    return TkPageScreen(
      title: 'Set your PIN',
      titleSize: 30,
      back: false,
      description: '4 digits of your own, for approving on WhatsApp and paying small amounts. You can set it later in Settings.',
      children: [
        if (way == null) _NoWay(auth: widget.auth, onBack: _check) else PinForm(auth: widget.auth, pin: p, passkey: _passkey, first: true, onSaved: _saved, onBusy: (b) => setState(() => _busy = b)),
        Center(child: TkLink(label: 'Not now', bold: true, onTap: _busy ? null : widget.onDone)),
      ],
    );
  }
}

/// Settings › PIN: when it was set, whether it is locked; Set your PIN,
/// or Change PIN.
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

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loadError = null);
    final has = await _phonePasskey(widget.auth);
    try {
      final p = await widget.auth.api.getMePin();
      if (mounted) setState(() => (_pin = p, _passkey = has));
    } on Object catch (e) {
      if (mounted) setState(() => _loadError = _say(e));
    }
  }

  void _saved(MePin now) {
    final was = _pin?.set ?? false;
    showTkToast(context, '${was ? 'Your new PIN is saved.' : 'Your PIN is set.'} It can pay now.');
    setState(() => (_pin = now, _changing = false));
  }

  /// Removing it takes no proof: it only takes a way to approve and pay.
  Future<void> _remove() async {
    final yes = await askSure(context, title: 'Remove your PIN?', body: 'It will no longer approve on WhatsApp or pay. Setting one again takes a code to your phone.', ok: 'Remove', danger: true);
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
      final way = pinWay(p, passkey: _passkey);
      body = [
        if (p.renew == true) const _Renew(),
        if (way == null) _NoWay(auth: widget.auth, onBack: _load) else PinForm(auth: widget.auth, pin: p, passkey: _passkey, first: true, onSaved: _saved),
      ];
    } else {
      final locked = p.lockedUntil != null && p.lockedUntil!.isAfter(now) ? p.lockedUntil : null;
      final way = pinWay(p, passkey: _passkey);
      body = [
        TkFormGroup(
          children: [
            TkLines([
              ('PIN', '••••'),
              if (p.setAt != null) ('Set', pinWhen(p.setAt!, now)),
              ('Pays', 'Up to ₦100,000 a payment'),
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
        if (_error != null)
          Semantics(
            liveRegion: true,
            child: TkCallout(leading: TkSquare('b-x', color: look.red), title: 'Not done', sub: _error, subLines: 4, color: look.red),
          ),
        if (!_changing) ...[
          TkCta(label: 'Change PIN', onTap: () => setState(() => (_changing = true, _error = null))),
          Center(child: TkLink(label: 'Remove my PIN', bold: true, onTap: _busy ? null : _remove)),
        ] else if (way == null)
          _NoWay(auth: widget.auth, onBack: _load)
        else
          PinForm(auth: widget.auth, pin: p, passkey: _passkey, first: false, onSaved: _saved, onBusy: (b) => setState(() => _busy = b)),
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
