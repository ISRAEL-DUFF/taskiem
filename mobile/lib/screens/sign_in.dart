// Signing in: the web's sign-in page (web/src/App.tsx, signed out; and
// pages/Login.tsx), drawn the same: the brand on pine above, one card over
// its edge. Sizes are the web's at a phone's width (styles.css: .auth-side,
// .auth-brand, .auth-line, .auth-main .login).

import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../kit/kit.dart';
import '../theme/theme.dart';

class SignInScreen extends StatefulWidget {
  const SignInScreen({super.key, this.onSignIn, this.onPasskey, this.onChat, this.onForgot, this.onSignUp});

  final void Function(String email, String password)? onSignIn;
  final VoidCallback? onPasskey;
  final VoidCallback? onChat;
  final VoidCallback? onForgot;
  final VoidCallback? onSignUp;

  @override
  State<SignInScreen> createState() => _SignInScreenState();
}

class _SignInScreenState extends State<SignInScreen> {
  final _email = TextEditingController();
  final _password = TextEditingController();

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  void _submit() => widget.onSignIn?.call(_email.text.trim(), _password.text);

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final hint = tkHint(t);
    return ColoredBox(
      color: t.bg,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const _AuthSide(),
            // .auth-main: 16 px at the sides, 32 below; the card rides 40 px
            // up over the pine.
            Padding(
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
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Padding(
                        padding: const EdgeInsets.only(bottom: 20),
                        child: Semantics(
                          header: true,
                          child: Text(
                            'Sign in to Taskiem',
                            style: tkText(size: 24, weight: 700, lineHeight: 1.2, letterSpacing: -0.24, color: t.text),
                          ),
                        ),
                      ),
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
                      const SizedBox(height: 16),
                      TkButton(label: 'Sign in', primary: true, minHeight: 44, onPressed: _submit),
                      Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: Center(child: TkLink(label: 'Forgot password?', onTap: widget.onForgot)),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: Text('or', textAlign: TextAlign.center, style: hint),
                      ),
                      TkButton(label: 'Sign in with a passkey', minHeight: 44, onPressed: widget.onPasskey),
                      const SizedBox(height: 8),
                      TkButton(label: 'Sign in with WhatsApp or Telegram', minHeight: 44, onPressed: widget.onChat),
                      Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: Row(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            Text('New to Taskiem? ', style: hint),
                            TkLink(label: 'Create an account', onTap: widget.onSignUp),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The brand on pine (.auth-side): the mark and the wordmark, then the line.
class _AuthSide extends StatelessWidget {
  const _AuthSide();

  @override
  Widget build(BuildContext context) {
    const white = Color(0xFFFFFFFF);
    final line = tkText(size: 18, weight: 600, lineHeight: 1.35, color: white);
    return Container(
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
    );
  }

  static double _zero(TextStyle s) {
    final p = TextPainter(text: TextSpan(text: '0', style: s), textDirection: TextDirection.ltr)..layout();
    return p.width;
  }
}
