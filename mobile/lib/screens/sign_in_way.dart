// A way to sign in on a new phone (docs/spec/taskiem-for-everyone.md §5.4;
// founder, 25 November 2026). A code to a phone proves only that someone
// holds the number today, so a person in a business proves a new phone with
// a passkey, a link to their email or their password: never the PIN, which
// pays and approves only. Someone with none of them is let in anyway (so
// nobody is locked out of their own business) and is asked to add one: as a
// step before a business opens, for an account new to the apps (the
// server's user.sign_in_way_step), else a reminder on Today and Settings
// (user.needs_sign_in_way) until they do. The web's SignInWay.tsx.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../chat/approve.dart' show TkTextBox;
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkCta, TkKitField;
import '../theme/look.dart';
import 'second_proof.dart' show noWayHere;

/// The reminder's title and why, in plain words.
const signInWayTitle = 'Add a way to sign in on a new phone';
const signInWayWhy = 'A code to your number isn’t enough on its own if someone gets your SIM.';

/// Whether the person is in a business with no way to prove a new phone.
bool needsSignInWay(Auth auth) => auth.me?.user?.needsSignInWay == true;

/// The reminder on Today and Settings, until a way is added: its button
/// opens [SignInWayScreen] over [context], or [onAdd] when given (on You,
/// which opens the business first).
class SignInWayCallout extends StatelessWidget {
  const SignInWayCallout({super.key, required this.auth, this.backLabel = 'Back', this.onAdd});
  final Auth auth;
  final String backLabel;
  final VoidCallback? onAdd;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkCallout(
      leading: TkSquare('b-shield', color: look.orange),
      title: '$signInWayTitle.',
      sub: signInWayWhy,
      subLines: 3,
      color: look.orange,
      button: 'Add',
      semantics: 'Add a way to sign in',
      onPressed: onAdd ??
          () => unawaited(Navigator.of(context).push(TkRoute<void>(
                pageBuilder: (_, _, _) => SignInWayScreen(auth: auth, backLabel: backLabel),
              ))),
    );
  }
}

enum _Way { email, password }

/// Adding a way: an email (confirmed by its link), a password, or a passkey
/// where this phone can make one. As a [step] it has no Back and opens the
/// business once a way is added (the server says so); otherwise it closes.
class SignInWayScreen extends StatefulWidget {
  const SignInWayScreen({super.key, required this.auth, this.step = false, this.backLabel = 'Back'});
  final Auth auth;
  final bool step;
  final String backLabel;

  @override
  State<SignInWayScreen> createState() => _SignInWayScreenState();
}

class _SignInWayScreenState extends State<SignInWayScreen> with WidgetsBindingObserver {
  final _email = TextEditingController();
  final _password = TextEditingController();
  final _again = TextEditingController();
  _Way? _way;
  String? _sentTo;
  bool _passkey = false;
  bool _busy = false;
  bool _done = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    unawaited(_canPasskey());
  }

  Future<void> _canPasskey() async {
    try {
      final can = await passkeys.available();
      if (mounted) setState(() => _passkey = can);
    } on Object {
      // Not offered.
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _email.dispose();
    _password.dispose();
    _again.dispose();
    super.dispose();
  }

  /// Back in the app after opening the email's link: read again, quietly.
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && _sentTo != null && !_busy) unawaited(_check(quiet: true));
  }

  Future<void> _run(Future<void> Function() f) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await f();
    } on PasskeyCancelled {
      // Closed the phone's passkey sheet.
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    } on Object {
      _error = 'Something went wrong. Try again.';
    }
    if (mounted) setState(() => _busy = false);
  }

  /// Whether a way is there now (the server says): done if so.
  Future<void> _check({bool quiet = false}) async {
    await widget.auth.refreshMe();
    if (!mounted) return;
    if (needsSignInWay(widget.auth) || widget.auth.me?.user?.signInWayStep == true) {
      if (!quiet && _sentTo != null) setState(() => _error = 'The link has not been opened yet. Open it from the email, then try again.');
      return;
    }
    if (_done) return;
    _done = true;
    showTkToast(context, 'Done. You can sign in on a new phone with it.');
    // As a step, the app opens by itself once the server says so.
    if (!widget.step) unawaited(Navigator.of(context).maybePop());
  }

  void _sendLink() {
    final email = _email.text.trim();
    if (!email.contains('@')) return setState(() => _error = 'That doesn’t look like an email address.');
    _run(() async {
      await widget.auth.api.addEmail(body: AddEmailRequest(email: email));
      if (mounted) setState(() => _sentTo = email);
    });
  }

  void _setPassword() {
    final p = _password.text;
    if (p.length < 12) return setState(() => _error = 'At least 12 characters.');
    if (p != _again.text) return setState(() => _error = 'The two passwords are not the same.');
    _run(() async {
      await widget.auth.transport.send('POST', '/v1/me/password', body: {'new_password': p});
      _password.clear();
      _again.clear();
      await _check();
    });
  }

  void _addPasskey() => _run(() async {
        await addPasskey(widget.auth, const {});
        await _check();
      });

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final sent = _sentTo;
    final business = widget.auth.me?.tenantName;
    final List<Widget> body = switch ((_way, sent)) {
      (_Way.email, final String to) => [
          Semantics(
            liveRegion: true,
            child: TkCallout(
              leading: TkSquare('b-mail', color: look.teal),
              title: 'We sent a link to $to',
              sub: 'Open it to confirm your email, then come back here.',
              subLines: 3,
              color: look.teal,
            ),
          ),
          TkCta(label: _busy ? 'Checking…' : 'I’ve opened the link', onTap: _busy ? null : () => _run(_check)),
          Center(child: TkLink(bold: true, label: 'Use another way', onTap: _busy ? null : () => setState(() => (_way = null, _sentTo = null))))
        ],
      (_Way.email, _) => [
          TkFormGroup(children: [
            TkKitField(
              label: 'Your email',
              hint: 'We send a link to it. Open the link to confirm it.',
              child: TkTextBox(key: const ValueKey('way-email'), controller: _email, plain: true, size: 15, height: 46),
            ),
          ]),
          TkCta(label: _busy ? 'Sending…' : 'Send the link', onTap: _busy ? null : _sendLink),
          Center(child: TkLink(bold: true, label: 'Use another way', onTap: _busy ? null : () => setState(() => _way = null))),
        ],
      (_Way.password, _) => [
          TkFormGroup(children: [
            TkKitField(label: 'New password', hint: '12 characters or more.', child: TkTextBox(key: const ValueKey('way-password'), controller: _password, secret: true, size: 15, height: 46)),
            TkKitField(label: 'New password again', child: TkTextBox(key: const ValueKey('way-again'), controller: _again, secret: true, size: 15, height: 46)),
          ]),
          TkCta(label: _busy ? 'Saving…' : 'Set password', onTap: _busy ? null : _setPassword),
          Center(child: TkLink(bold: true, label: 'Use another way', onTap: _busy ? null : () => setState(() => _way = null))),
        ],
      (null, _) => [
          TkSection(
            title: 'Choose one',
            inset: 56,
            children: [
              TkListRow(title: 'Add an email', sub: 'We send a link to confirm it', icon: 'b-mail', square: look.teal, onTap: _busy ? null : () => setState(() => _way = _Way.email)),
              TkListRow(title: 'Set a password', sub: '12 characters or more', icon: 'b-lock', square: look.blue, onTap: _busy ? null : () => setState(() => _way = _Way.password)),
              if (_passkey)
                TkListRow(title: 'Add a passkey', sub: 'This phone’s fingerprint, face or screen lock', icon: 'b-key', square: look.lime, onTap: _busy ? null : _addPasskey),
            ],
          ),
        ],
    };
    return TkPageScreen(
      title: signInWayTitle,
      titleSize: 30,
      back: !widget.step,
      backLabel: widget.backLabel,
      description: 'Taskiem asks for it when you sign in on a new phone, with the code to your number. $noWayHere',
      children: [
        TkCallout(
          leading: TkSquare('b-shield', color: look.orange),
          title: widget.step && business != null && business.isNotEmpty ? 'Before $business opens' : 'Why it matters',
          sub: signInWayWhy,
          subLines: 3,
          color: look.orange,
        ),
        ...body,
        if (_error != null) TkErrorBox(_error!),
        if (widget.step) Center(child: TkLink(bold: true, label: 'Sign out', onTap: _busy ? null : () => unawaited(widget.auth.signOut()))),
      ],
    );
  }
}
