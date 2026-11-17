// A forgotten password, in the app (as drawn): the email asked for, a link
// sent to it (POST /v1/auth/password/forgot), and the link, opened on this
// phone (https://<server>/reset-password#token=…), opens Choose a new
// password here (POST /v1/auth/password/reset); then signing in with it.
// The server answers the same whoever the email belongs to, and the new
// password signs every session out; it does not sign in.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/look.dart' show TkLook;
import '../theme/theme.dart';
import 'sign_in.dart';

/// The least a password may be (the server's minPasswordLen).
const minPasswordLength = 12;

/// What is wrong with a new password typed twice, or null when it will do.
String? newPasswordProblem(String a, String b) {
  if (a.length < minPasswordLength) return 'At least $minPasswordLength characters.';
  if (a != b) return 'The two are not the same.';
  return null;
}

/// The screens' frame: the brand on pine, one card over its edge.
class _AuthPage extends StatelessWidget {
  const _AuthPage({required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: TkLook.of(context).ground,
    child: SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [const AuthSide(), AuthCard(children: children)],
      ),
    ),
  );
}

/// The round-cornered tile above a step's title (as drawn): an envelope, a
/// lock, a tick.
class _Tile extends StatelessWidget {
  const _Tile(this.icon, {this.done = false});
  final String icon;
  final bool done;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final look = TkLook.of(context);
    // As drawn: the tick on lime, the envelope and the lock on blue.
    final ink = done ? (dark ? t.brandDark : t.brand) : look.blueInk;
    return Align(
      alignment: Alignment.centerLeft,
      child: Padding(
        padding: const EdgeInsets.only(bottom: 14),
        child: Container(
          width: 50,
          height: 50,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: done ? t.tkLimeSoft : look.blue.withValues(alpha: dark ? 0.22 : 0.16),
            borderRadius: BorderRadius.circular(14),
          ),
          child: TkIcon(icon, size: 22, stroke: 2, color: ink),
        ),
      ),
    );
  }
}

Text _lede(BuildContext context, String words) =>
    Text(words, style: tkText(size: 14, lineHeight: 1.45, color: TkTheme.tokens(context).muted));

String _said(Object e) => switch (e) {
  ApiError(:final message) => message,
  Offline() => 'You’re offline. Check your connection and try again.',
  _ => 'Something went wrong. Try again.',
};

/// Reset your password, then Check your email.
class ForgotPasswordScreen extends StatefulWidget {
  const ForgotPasswordScreen({super.key, this.auth, required this.onBack, this.email = ''});
  final Auth? auth;
  final VoidCallback onBack;

  /// What was typed on the sign-in card, to start from.
  final String email;

  @override
  State<ForgotPasswordScreen> createState() => _ForgotPasswordScreenState();
}

class _ForgotPasswordScreenState extends State<ForgotPasswordScreen> {
  late final _email = TextEditingController(text: widget.email);
  String? _sentTo;
  String? _error;
  bool _busy = false;
  bool _again = false;

  @override
  void dispose() {
    _email.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final a = widget.auth;
    final email = _email.text.trim();
    if (a == null || _busy) return;
    if (!email.contains('@')) {
      setState(() => _error = 'Enter the email you sign in with.');
      return;
    }
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await a.transport.send('POST', '/v1/auth/password/forgot', body: {'email': email});
      if (mounted) setState(() => _sentTo = email);
    } on Object catch (e) {
      if (mounted) setState(() => _error = _said(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _openMail() async {
    if (!await openMailApp() && mounted) {
      setState(() => _error = 'Open your email app and find the email from Taskiem.');
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final to = _sentTo;
    if (to != null) {
      return _AuthPage(
        children: [
          const _Tile('mail'),
          const AuthTitle('Check your email'),
          Semantics(
            liveRegion: true,
            child: _lede(context, 'If $to belongs to an account that signs in with a password, a link to reset it is on its way. It works once, for 30 minutes.'),
          ),
          const SizedBox(height: 10),
          _lede(context, 'Open it on this phone: it opens Taskiem here.'),
          if (_again) ...[
            const SizedBox(height: 10),
            Semantics(liveRegion: true, child: Text('Sent again. The newest link is the one that works.', style: tkText(size: 13.5, weight: 600, color: t.text))),
          ],
          const SizedBox(height: 16),
          if (_error != null) ...[TkErrorBox(_error!), const SizedBox(height: 4)],
          AuthButton(label: 'Open your email app', soft: true, onTap: _openMail),
          const SizedBox(height: 8),
          Wrap(
            alignment: WrapAlignment.center,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              AuthLink(
                label: 'Send it again',
                onTap: _busy
                    ? null
                    : () async {
                        await _send();
                        if (mounted && _error == null) setState(() => _again = true);
                      },
              ),
              Text('  ·  ', style: tkText(size: 14, color: t.muted)),
              AuthLink(label: 'Back to sign in', onTap: widget.onBack),
            ],
          ),
        ],
      );
    }
    return _AuthPage(
      children: [
        const AuthTitle('Reset your password'),
        _lede(context, 'Enter the email you sign in with. If it signs in with a password, we email a link to choose a new one.'),
        const SizedBox(height: 14),
        TkField(
          label: 'Email',
          controller: _email,
          autofocus: widget.email.isEmpty,
          keyboard: TextInputType.emailAddress,
          autofill: const [AutofillHints.email],
          minHeight: 44,
          onSubmitted: (_) => _send(),
        ),
        if (_error != null) ...[const SizedBox(height: 4), TkErrorBox(_error!)] else const SizedBox(height: 16),
        AuthButton(label: _busy ? 'Sending…' : 'Email me a link', enabled: !_busy, onTap: _send),
        const SizedBox(height: 6),
        Center(child: AuthLink(label: 'Back to sign in', onTap: widget.onBack)),
      ],
    );
  }
}

/// Choose a new password (from the email's link), then Password saved:
/// signing in with it.
class ResetPasswordScreen extends StatefulWidget {
  const ResetPasswordScreen({super.key, this.auth, required this.token, required this.onDone});
  final Auth? auth;

  /// The link's token (after #token=).
  final String token;

  /// Back to signing in (or, signed in, away): the link is spent or not wanted.
  final VoidCallback onDone;

  @override
  State<ResetPasswordScreen> createState() => _ResetPasswordScreenState();
}

class _ResetPasswordScreenState extends State<ResetPasswordScreen> {
  final _a = TextEditingController();
  final _b = TextEditingController();
  final _email = TextEditingController();
  bool _saved = false;
  bool _busy = false;
  bool _tried = false;
  String? _error;

  @override
  void dispose() {
    _a.dispose();
    _b.dispose();
    _email.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final a = widget.auth;
    if (a == null || _busy) return;
    setState(() => _tried = true);
    if (newPasswordProblem(_a.text, _b.text) != null) return;
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await a.transport.send('POST', '/v1/auth/password/reset', body: {'token': widget.token, 'password': _a.text});
      if (mounted) setState(() => _saved = true);
    } on Object catch (e) {
      if (mounted) setState(() => _error = _said(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _signIn() async {
    final a = widget.auth;
    if (a == null || _busy) return;
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await a.signIn(_email.text.trim(), _a.text);
    } on Object catch (e) {
      if (mounted) setState(() => _error = _said(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (_saved) {
      return _AuthPage(
        children: [
          const _Tile('check', done: true),
          const AuthTitle('Password saved'),
          Semantics(liveRegion: true, child: _lede(context, 'Sign in with your email and this password.')),
          const SizedBox(height: 14),
          TkField(
            label: 'Email',
            controller: _email,
            autofocus: true,
            keyboard: TextInputType.emailAddress,
            autofill: const [AutofillHints.username, AutofillHints.email],
            minHeight: 44,
          ),
          const SizedBox(height: 12),
          // The new one, already in: the person only adds their email.
          TkField(label: 'Password', controller: _a, obscure: true, autofill: const [AutofillHints.password], minHeight: 44, onSubmitted: (_) => _signIn()),
          if (_error != null) ...[const SizedBox(height: 4), TkErrorBox(_error!)] else const SizedBox(height: 16),
          AuthButton(label: _busy ? 'Signing in…' : 'Sign in', enabled: !_busy, onTap: _signIn),
          const SizedBox(height: 6),
          Center(child: AuthLink(label: 'Back to sign in', onTap: widget.onDone)),
        ],
      );
    }
    final problem = _tried ? newPasswordProblem(_a.text, _b.text) : null;
    return _AuthPage(
      children: [
        const _Tile('lock'),
        const AuthTitle('Choose a new password'),
        TkField(
          label: 'New password',
          controller: _a,
          autofocus: true,
          obscure: true,
          autofill: const [AutofillHints.newPassword],
          minHeight: 44,
          onChanged: (_) => setState(() {}),
        ),
        const SizedBox(height: 12),
        TkField(
          label: 'New password again',
          controller: _b,
          obscure: true,
          autofill: const [AutofillHints.newPassword],
          minHeight: 44,
          onChanged: (_) => setState(() {}),
          onSubmitted: (_) => _save(),
        ),
        const SizedBox(height: 6),
        Text(
          problem ?? 'At least $minPasswordLength characters. A short sentence is easy to remember.',
          style: tkText(size: 12.5, lineHeight: 1.4, color: problem != null ? t.danger : t.muted),
        ),
        if (_error != null) ...[const SizedBox(height: 4), TkErrorBox(_error!)] else const SizedBox(height: 14),
        AuthButton(label: _busy ? 'Saving…' : 'Save the new password', enabled: !_busy, onTap: _save),
        const SizedBox(height: 8),
        Text('Every session of yours is signed out.', textAlign: TextAlign.center, style: tkText(size: 12.5, color: t.muted)),
        const SizedBox(height: 2),
        Center(child: AuthLink(label: 'Back to sign in', onTap: widget.onDone)),
      ],
    );
  }
}
