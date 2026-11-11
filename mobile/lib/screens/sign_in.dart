// Signing in: the web's sign-in page (web/src/App.tsx, signed out; and
// pages/Login.tsx), drawn the same: the brand on pine above, one card over
// its edge. Sizes are the web's at a phone's width (styles.css: .auth-side,
// .auth-brand, .auth-line, .auth-main .login). Two ways, as on the web:
// email and password, and a code sent to the person's WhatsApp or Telegram.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';

class SignInScreen extends StatefulWidget {
  const SignInScreen({super.key, this.auth, this.signup = true, this.onSignUp, this.onForgot, this.onPasskey});

  /// Signing in; none in a picture of the screen alone.
  final Auth? auth;

  /// Whether self-serve signup is open (GET /v1/signup): "New to Taskiem?".
  final bool signup;
  final VoidCallback? onSignUp;
  final VoidCallback? onForgot;
  final VoidCallback? onPasskey;

  @override
  State<SignInScreen> createState() => _SignInScreenState();
}

class _SignInScreenState extends State<SignInScreen> {
  bool _withChat = false;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ColoredBox(
      color: t.bg,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const AuthSide(),
            AuthCard(
              children: [
                const AuthTitle('Sign in to Taskiem'),
                if (_withChat)
                  _ChatSignIn(auth: widget.auth, onBack: () => setState(() => _withChat = false))
                else
                  _EmailSignIn(
                    auth: widget.auth,
                    onChat: () => setState(() => _withChat = true),
                    onForgot: widget.onForgot,
                    onPasskey: widget.onPasskey,
                  ),
                if (widget.signup)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        Text('New to Taskiem? ', style: tkHint(t)),
                        TkLink(label: 'Create an account', onTap: widget.onSignUp),
                      ],
                    ),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// A sign-in or sign-up action's run: busy while it runs, its error kept to
/// show (the web's useAction).
mixin _Acting<T extends StatefulWidget> on State<T> {
  bool busy = false;
  String? error;
  bool passkeyRequired = false;

  Future<void> act(Future<void> Function() f) async {
    // Pressing a button takes the cursor out of the field, as in a browser.
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      busy = true;
      error = null;
      passkeyRequired = false;
    });
    try {
      await f();
    } on ApiError catch (e) {
      error = e.message;
      passkeyRequired = e.has('passkey_required');
    } on Offline {
      error = 'You’re offline. Check your connection and try again.';
    } catch (_) {
      // Anything else (an answer the app could not read): said, never lost.
      error = 'Something went wrong. Try again.';
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }
}

class _EmailSignIn extends StatefulWidget {
  const _EmailSignIn({this.auth, this.onChat, this.onForgot, this.onPasskey});
  final Auth? auth;
  final VoidCallback? onChat;
  final VoidCallback? onForgot;
  final VoidCallback? onPasskey;

  @override
  State<_EmailSignIn> createState() => _EmailSignInState();
}

class _EmailSignInState extends State<_EmailSignIn> with _Acting {
  final _email = TextEditingController();
  final _password = TextEditingController();

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  void _submit() {
    final a = widget.auth;
    if (a == null || busy) return;
    act(() => a.signIn(_email.text.trim(), _password.text));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkField(
          label: 'Email',
          controller: _email,
          autofocus: true,
          keyboard: TextInputType.emailAddress,
          autofill: const [AutofillHints.username, AutofillHints.email],
          minHeight: 44,
        ),
        const SizedBox(height: 16),
        TkField(
          label: 'Password',
          controller: _password,
          obscure: true,
          autofill: const [AutofillHints.password],
          minHeight: 44,
          onSubmitted: (_) => _submit(),
        ),
        if (error != null) ...[const SizedBox(height: 4), TkErrorBox(error!)] else const SizedBox(height: 16),
        TkButton(label: busy ? 'Signing in…' : 'Sign in', primary: true, minHeight: 44, enabled: !busy, onPressed: _submit),
        Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: Center(child: TkLink(label: 'Forgot password?', onTap: widget.onForgot)),
        ),
        Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: Text('or', textAlign: TextAlign.center, style: tkHint(t)),
        ),
        TkButton(
          label: 'Sign in with a passkey',
          minHeight: 44,
          // Passkeys in the app need its link to Taskiem's address and its
          // signing key (docs/spec/mobile-app.md §5.6, M4); until then, said.
          onPressed: widget.onPasskey ??
              () => setState(() => error = 'Signing in with a passkey comes to the app soon. For now, sign in with your email or with WhatsApp or Telegram.'),
        ),
        const SizedBox(height: 8),
        TkButton(label: 'Sign in with WhatsApp or Telegram', minHeight: 44, onPressed: widget.onChat),
        if (passkeyRequired)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Text('Sign in with your passkey.', style: tkHint(t)),
          ),
      ],
    );
  }
}

/// Signing in with the chat (docs/spec/chat-sign-in.md): the phone number of
/// the person's WhatsApp or Telegram, then the six-digit code sent there.
class _ChatSignIn extends StatefulWidget {
  const _ChatSignIn({this.auth, required this.onBack});
  final Auth? auth;
  final VoidCallback onBack;

  @override
  State<_ChatSignIn> createState() => _ChatSignInState();
}

class _ChatSignInState extends State<_ChatSignIn> with _Acting {
  final _phone = TextEditingController();
  final _code = TextEditingController();
  String? _sent;

  @override
  void dispose() {
    _phone.dispose();
    _code.dispose();
    super.dispose();
  }

  void _send() {
    final a = widget.auth;
    if (a == null || busy) return;
    act(() async {
      final s = await a.sendCode(_phone.text.trim());
      setState(() => _sent = s);
    });
  }

  void _verify() {
    final a = widget.auth;
    if (a == null || busy || _code.text.length != 6) return;
    act(() => a.signInWithCode(_phone.text.trim(), _code.text));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final sent = _sent;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (sent == null) ...[
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Text('We send a code to the WhatsApp or Telegram on your Taskiem account.', style: tkHint(t)),
          ),
          TkField(
            key: const ValueKey('phone'),
            label: 'Phone number',
            controller: _phone,
            autofocus: true,
            keyboard: TextInputType.phone,
            autofill: const [AutofillHints.telephoneNumber],
            minHeight: 44,
            onSubmitted: (_) => _send(),
          ),
          if (error != null) ...[const SizedBox(height: 4), TkErrorBox(error!)] else const SizedBox(height: 16),
          TkButton(label: busy ? 'Sending…' : 'Send me a code', primary: true, minHeight: 44, enabled: !busy, onPressed: _send),
        ] else ...[
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Semantics(liveRegion: true, child: Text(sent, style: tkText(size: 14, color: t.text))),
          ),
          TkField(
            key: const ValueKey('code'),
            label: 'Code',
            controller: _code,
            autofocus: true,
            keyboard: TextInputType.number,
            autofill: const [AutofillHints.oneTimeCode],
            digits: 6,
            minHeight: 44,
            onChanged: (_) => setState(() {}),
            onSubmitted: (_) => _verify(),
          ),
          if (error != null) ...[const SizedBox(height: 4), TkErrorBox(error!)] else const SizedBox(height: 16),
          TkButton(
            label: busy ? 'Signing in…' : 'Sign in',
            primary: true,
            minHeight: 44,
            enabled: !busy && _code.text.length == 6,
            onPressed: _verify,
          ),
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Center(
              child: TkLinkButton(
                label: 'Send a new code',
                onPressed: () => setState(() {
                  _sent = null;
                  _code.clear();
                  error = null;
                }),
              ),
            ),
          ),
        ],
        Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: Center(child: TkLinkButton(label: 'Sign in with your email instead', onPressed: widget.onBack)),
        ),
      ],
    );
  }
}

/// The card over the pine's edge (.auth-main .login): 16 px at the sides,
/// 32 below; it rides 40 px up.
class AuthCard extends StatelessWidget {
  const AuthCard({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 32),
      child: Transform.translate(
        offset: const Offset(0, -40),
        child: Container(
          padding: const EdgeInsets.fromLTRB(24, 28, 24, 24),
          decoration: BoxDecoration(
            color: t.panel,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(24),
            boxShadow: t.shadowLg,
          ),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children),
        ),
      ),
    );
  }
}

/// The card's title (.auth-main .login h1).
class AuthTitle extends StatelessWidget {
  const AuthTitle(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 20),
      child: Semantics(
        header: true,
        child: Text(text, style: tkText(size: 24, weight: 700, lineHeight: 1.2, letterSpacing: -0.24, color: TkTheme.tokens(context).text)),
      ),
    );
  }
}

/// The brand on pine (.auth-side): the mark and the wordmark, then the line.
class AuthSide extends StatelessWidget {
  const AuthSide({super.key});

  @override
  Widget build(BuildContext context) {
    const white = Color(0xFFFFFFFF);
    final line = tkText(size: 18, weight: 600, lineHeight: 1.35, color: white);
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: SystemUiOverlayStyle.light,
      child: Container(
        color: TkTheme.tokens(context).brand,
        padding: EdgeInsets.fromLTRB(24, 28 + MediaQuery.paddingOf(context).top, 24, 64),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                SvgPicture.asset('assets/brand/mark.svg', width: 40, height: 40),
                const SizedBox(width: 12),
                SvgPicture.asset('assets/brand/wordmark-reversed.svg', height: 22, semanticsLabel: 'Taskiem'),
              ],
            ),
            // `gap: 12px` and the line's own 6 px above.
            const SizedBox(height: 18),
            ConstrainedBox(
              // `max-width: 30ch`: thirty of the font's zeros.
              constraints: BoxConstraints(maxWidth: 30 * _zero(line)),
              child: Text('Orders, payments and approvals for your business, in one place.', style: line),
            ),
          ],
        ),
      ),
    );
  }

  static double _zero(TextStyle s) {
    final p = TextPainter(text: TextSpan(text: '0', style: s), textDirection: TextDirection.ltr)..layout();
    return p.width;
  }
}
