// Paying by fingerprint (docs/spec/fingerprint-key.md; lib/app/pay_key.dart):
// a key on this phone, behind its own fingerprint or face check, that pays
// up to ₦500,000 a payment and ₦1,000,000 a day, separate from the passkey
// and the PIN.
//
// - Settings › Fingerprint ([FingerprintScreen]): this phone on or off, what
//   it pays, and every phone the person turned it on for, each with Remove.
// - Turning it on ([FingerprintOnSheet], also offered on the Pay sheet)
//   makes the key, then proves it is the person as setting the PIN does:
//   the PIN, a passkey, a code to their phone, or an authenticator code.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../app/pay_key.dart';
import '../chat/approve.dart' show TkTextBox;
import '../chat/groups.dart' show askSure;
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show TkSwitch;
import '../kit/kit.dart' show TkLink, TkSmallButton;
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkLines, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'sign_in.dart' show CodeBoxes;
import 'you_pin.dart' show pinDigits, whatWentWrong;

/// The PIN's length, as the PIN screen's (you_pin.dart).
const payPinLength = pinDigits;

/// What it is for, behind the page's (i).
const fingerprintUses = 'Pay with this phone’s fingerprint or face, without your passkey or PIN: up to ₦500,000 a payment and ₦1,000,000 a day. '
    'A list of payments, an account changed since it was last paid, and anything waiting for the owner’s OK still take your passkey. '
    'The key stays in this phone’s secure chip and never leaves it.';

String _say(Object e) => switch (e) {
  ApiError() => e.message,
  Offline() => 'You’re offline. Try again when you’re back online.',
  FingerprintUnavailable() => e.message,
  FingerprintGone() => e.message,
  _ => 'Something went wrong (${whatWentWrong(e)}). Try again.',
};

/// Opens [FingerprintOnSheet]; true once it is on.
Future<bool> turnOnFingerprintSheet(BuildContext context, Auth auth, {String crumb = 'Settings'}) async =>
    await showTkSheet<bool>(context, (_) => FingerprintOnSheet(auth: auth, crumb: crumb)) == true;

/// Turning paying by fingerprint on for this phone: the limits, then one
/// way of proving it is the person.
class FingerprintOnSheet extends StatefulWidget {
  const FingerprintOnSheet({super.key, required this.auth, this.crumb = 'Settings'});
  final Auth auth;
  final String crumb;

  @override
  State<FingerprintOnSheet> createState() => _FingerprintOnSheetState();
}

class _FingerprintOnSheetState extends State<FingerprintOnSheet> {
  MeFingerprint? _me;
  List<String> _ways = const [];
  String? _way;
  final _pin = TextEditingController();
  final _code = TextEditingController();
  final _totp = TextEditingController();
  bool _sent = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final c in [_pin, _code, _totp]) {
      c.addListener(() => setState(() {}));
    }
    _load();
  }

  @override
  void dispose() {
    _pin.dispose();
    _code.dispose();
    _totp.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final me = await widget.auth.api.getMeFingerprint();
      final passkey = me.proofs.contains('passkey') && await passkeys.available();
      // The PIN first: it is what people type on a phone; then a code to
      // the phone, then a passkey, then an authenticator.
      final ways = [
        if (me.proofs.contains('pin')) 'pin',
        if (me.proofs.contains('code')) 'code',
        if (passkey) 'passkey',
        if (me.proofs.contains('totp')) 'totp',
      ];
      if (mounted) setState(() => (_me = me, _ways = ways, _way = ways.isEmpty ? null : ways.first));
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    }
  }

  Future<void> _sendCode() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.api.fingerprintCode(body: const FingerprintCodeRequest());
      if (mounted) setState(() => _sent = true);
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  bool get _ready => switch (_way) {
    'pin' => _pin.text.length == payPinLength,
    'code' => _sent && _code.text.length == 6,
    'totp' => _totp.text.trim().length == 6,
    'passkey' => true,
    _ => false,
  };

  Future<void> _on() async {
    setState(() => (_busy = true, _error = null));
    try {
      final proof = switch (_way) {
        'pin' => FingerprintProof(pin: _pin.text),
        'code' => FingerprintProof(code: _code.text),
        'totp' => FingerprintProof(totp: _totp.text.trim()),
        _ => FingerprintProof(passkey: await passkeyProof(widget.auth, 'account.reauth', 'fingerprint.add')),
      };
      await turnOnFingerprint(widget.auth, proof);
      if (!mounted) return;
      Navigator.of(context).pop(true);
      return;
    } on PasskeyCancelled {
      // Put away: nothing to say.
    } on FingerprintCancelled {
      // Put away: nothing to say.
    } on Object catch (e) {
      if (mounted) {
        setState(() {
          _error = _say(e);
          _pin.clear();
          if (e is ApiError && e.body['code_wrong'] == true) _code.clear();
        });
      }
    }
    if (mounted) setState(() => _busy = false);
  }

  static String _other(String way) => switch (way) {
    'pin' => 'Use my PIN instead',
    'passkey' => 'Use my passkey instead',
    'code' => 'Send a code to my phone instead',
    _ => 'Use my authenticator app instead',
  };

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final me = _me;
    final children = <Widget>[
      TkCallout(
        leading: TkSquare('fingerprint', color: look.lime),
        title: 'Up to ${naira(me?.oneKobo ?? 50000000)} a payment',
        sub: '${naira(me?.dayKobo ?? 100000000)} a day, on this phone only, after its fingerprint or face check. Bigger payments and lists still take your passkey.',
        subLines: 4,
        color: look.lime,
      ),
      if (me != null && _way == null)
        TkCallout(
          leading: TkSquare('b-key', color: look.violet),
          title: 'Set your PIN first',
          sub: 'Turning it on is confirmed with your PIN, your passkey or a code to your phone. Set a PIN in Settings, then come back.',
          subLines: 4,
          color: look.violet,
        ),
      if (_way == 'pin')
        TkFormGroup(
          title: 'Confirm it is you',
          children: [
            TkKitField(
              label: 'Your PIN',
              field: false,
              child: CodeBoxes(key: const ValueKey('fingerprint-pin'), controller: _pin, length: payPinLength, obscure: true, hint: 'Your PIN'),
            ),
          ],
        ),
      if (_way == 'code')
        TkFormGroup(
          title: 'Confirm it is you',
          children: [
            if (_sent)
              TkKitField(
                label: 'The code sent to your phone',
                field: false,
                child: CodeBoxes(key: const ValueKey('fingerprint-code'), controller: _code),
              )
            else
              TkKitField(
                label: 'A code to your phone',
                hint: 'By WhatsApp, or by SMS. It works for 10 minutes.',
                field: false,
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: TkSmallButton(label: 'Send the code', icon: 'b-send', primary: true, onPressed: _busy ? null : _sendCode),
                ),
              ),
          ],
        ),
      if (_way == 'totp')
        TkFormGroup(
          title: 'Confirm it is you',
          children: [TkKitField(label: 'A code from your authenticator app', child: TkTextBox(key: const ValueKey('fingerprint-totp'), controller: _totp, digits: 6))],
        ),
      if (_error != null)
        Semantics(
          liveRegion: true,
          child: TkCallout(leading: TkSquare('b-x', color: look.red), title: 'Not turned on', sub: _error, subLines: 4, color: look.red),
        ),
      for (final w in _ways)
        if (w != _way && !_busy)
          Center(
            child: TkLink(
              label: _other(w),
              bold: true,
              onTap: () => setState(() => (_way = w, _error = null)),
            ),
          ),
    ];
    return TkFormSheet(
      title: 'Pay by fingerprint',
      crumb: widget.crumb,
      action: _busy ? 'Turning on…' : (_way == 'passkey' ? 'Confirm with my passkey' : 'Turn it on'),
      actionIcon: 'fingerprint',
      onAction: !_busy && _ready ? _on : null,
      second: 'Not now',
      onSecond: _busy ? null : () => Navigator.of(context).pop(false),
      children: children,
    );
  }
}

/// Settings › Fingerprint: this phone on or off, what it pays, and the
/// phones it is on for.
class FingerprintScreen extends StatefulWidget {
  const FingerprintScreen({super.key, required this.auth, this.backLabel = 'Back to Settings', this.now = DateTime.now});
  final Auth auth;
  final String backLabel;
  final DateTime Function() now;

  @override
  State<FingerprintScreen> createState() => _FingerprintScreenState();
}

class _FingerprintScreenState extends State<FingerprintScreen> {
  MeFingerprint? _me;
  String? _here;
  bool _can = false;
  String? _loadError;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loadError = null);
    try {
      final can = await payKeys.available();
      var here = await payKeyHere(widget.auth);
      final me = await widget.auth.api.getMeFingerprint();
      if (here != null && !me.devices.any((d) => d.id == here)) {
        // Removed from another phone or the web: off here too.
        await turnOffFingerprintHere(widget.auth, server: false);
        here = null;
      }
      if (mounted) setState(() => (_me = me, _here = here, _can = can));
    } on Object catch (e) {
      if (mounted) setState(() => _loadError = _say(e));
    }
  }

  Future<void> _toggle(bool on) async {
    if (on) {
      if (await turnOnFingerprintSheet(context, widget.auth) && mounted) showTkToast(context, 'Paying by fingerprint is on for this phone');
      await _load();
      return;
    }
    final yes = await askSure(context, title: 'Turn off paying by fingerprint?', body: 'This phone will pay with your passkey or PIN instead.', ok: 'Turn off', danger: true);
    if (!yes || !mounted) return;
    await _run(() => turnOffFingerprintHere(widget.auth), 'Paying by fingerprint is off for this phone');
  }

  Future<void> _remove(FingerprintDevice d) async {
    if (d.id == _here) return _toggle(false);
    final yes = await askSure(context, title: 'Remove ${d.name}?', body: 'It will no longer pay by fingerprint.', ok: 'Remove', danger: true);
    if (!yes || !mounted) return;
    await _run(() => widget.auth.api.removeMeFingerprint(id: d.id), '${d.name} removed');
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => _busy = true);
    try {
      await f();
      if (mounted) showTkToast(context, done);
    } on Object catch (e) {
      if (mounted) showTkToast(context, _say(e), error: true);
    }
    if (mounted) setState(() => _busy = false);
    await _load();
  }

  String _when(DateTime d) {
    final now = widget.now();
    final days = DateTime(now.year, now.month, now.day).difference(DateTime(d.toLocal().year, d.toLocal().month, d.toLocal().day)).inDays;
    return switch (days) {
      0 => 'today',
      1 => 'yesterday',
      < 7 => '$days days ago',
      _ => '${d.toLocal().day}/${d.toLocal().month}/${d.toLocal().year}',
    };
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final t = TkTheme.tokens(context);
    final me = _me;
    final here = _here;
    final List<Widget> body;
    if (me == null) {
      body = [
        if (_loadError != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: 'This could not be read',
            sub: _loadError,
            color: look.orange,
            button: 'Try again',
            onPressed: _load,
          )
        else
          const TkLoading(label: 'Loading…'),
      ];
    } else {
      final phones = [...me.devices]..sort((a, b) => (b.id == here ? 1 : 0) - (a.id == here ? 1 : 0));
      body = [
        TkSection(
          title: 'This phone',
          inset: 56,
          children: [
            TkListRow(
              title: 'Pay with my fingerprint',
              titleLines: 2,
              sub: here != null ? 'On · up to ${naira(me.oneKobo)} a payment' : (_can ? 'Off' : 'Set up a fingerprint or face on this phone first'),
              icon: 'fingerprint',
              square: look.lime,
              chevron: false,
              trailing: TkSwitch(label: 'Pay with my fingerprint', on: here != null, onChanged: _busy || (!_can && here == null) ? null : _toggle),
            ),
          ],
        ),
        TkFormGroup(
          title: 'What it pays',
          children: [
            TkLines([
              ('A payment', 'Up to ${naira(me.oneKobo)}'),
              ('A day', 'Up to ${naira(me.dayKobo)}'),
              ('Left today', naira(me.leftTodayKobo)),
            ]),
          ],
        ),
        TkSection(
          title: 'Your phones',
          inset: 56,
          children: [
            if (phones.isEmpty) const TkListRow(title: 'None yet', sub: 'Turn it on above, on each phone you pay from', icon: 'b-device', chevron: false),
            for (final d in phones)
              TkListRow(
                title: d.name,
                sub: [
                  d.id == here ? 'This phone' : (d.platform == 'ios' ? 'iPhone' : 'Android'),
                  'turned on ${_when(d.createdAt)}',
                  if (d.lastUsedAt != null) 'paid ${_when(d.lastUsedAt!)}',
                ].join(' · '),
                icon: 'b-device',
                square: look.teal,
                chevron: false,
                trailing: TkSmallButton(label: 'Remove', danger: true, semantics: 'Remove ${d.name}', onPressed: _busy ? null : () => _remove(d)),
              ),
          ],
        ),
        Text(
          'A list of payments, an account changed since it was last paid, and anything waiting for the owner’s OK still take your passkey.',
          style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
        ),
      ];
    }
    return TkPageScreen(
      title: 'Fingerprint',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: fingerprintUses,
      onRefresh: _load,
      children: body,
    );
  }
}
