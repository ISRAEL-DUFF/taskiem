// Get started (docs/spec/mobile-app-m4.md N7): signing up as on WhatsApp,
// in the app. The number, the code WhatsApp brings, then a chat with Taskiem
// asking the same questions WhatsApp's sign-up asks, with its buttons. At the
// end the business is ready and the app opens on it.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../app/app_signup.dart';
import '../app/auth.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'sign_in.dart' show AuthCard, AuthSide, AuthTitle;

class AppSignUpScreen extends StatefulWidget {
  const AppSignUpScreen({super.key, required this.auth, this.onSignIn, this.onEmail, this.signup});
  final Auth auth;

  /// Back to signing in (a number already on an account, or "Sign in").
  final VoidCallback? onSignIn;

  /// The web's sign-up form, where the server has it open.
  final VoidCallback? onEmail;

  /// Kept by the screen that shows it (or a test's own).
  final AppSignup? signup;

  @override
  State<AppSignUpScreen> createState() => _AppSignUpScreenState();
}

class _AppSignUpScreenState extends State<AppSignUpScreen> {
  late final AppSignup _s = widget.signup ?? AppSignup(widget.auth);
  final _phone = TextEditingController();
  final _code = TextEditingController();
  final _answer = TextEditingController();
  final _scroll = ScrollController();

  @override
  void initState() {
    super.initState();
    _s.addListener(_changed);
  }

  void _changed() {
    if (!mounted) return;
    setState(() {});
    // The newest message in view, as a chat keeps it.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) _scroll.jumpTo(_scroll.position.maxScrollExtent);
    });
  }

  @override
  void dispose() {
    _s.removeListener(_changed);
    if (widget.signup == null) _s.dispose();
    for (final c in [_phone, _code, _answer]) {
      c.dispose();
    }
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final words = _answer.text;
    if (words.trim().isEmpty) return;
    _answer.clear();
    await _s.answer(words);
    // Not gone through: the words back in the box, to send again.
    if (mounted && _s.error != null && _answer.text.isEmpty) _answer.text = words;
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (_s.step == AppSignupStep.chat || _s.step == AppSignupStep.ended) return _chat(t);
    return ColoredBox(
      color: t.bg,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const AuthSide(),
            AuthCard(
              children: [
                const AuthTitle('Get started'),
                if (_s.step == AppSignupStep.phone) ...[
                  Text('Your business on Taskiem in a few minutes, as on WhatsApp. First, your WhatsApp number: we send a code to it.', style: tkHint(t)),
                  const SizedBox(height: 12),
                  TkField(label: 'Your WhatsApp number', controller: _phone, keyboard: TextInputType.phone, autofill: const [AutofillHints.telephoneNumber]),
                  Padding(
                    padding: const EdgeInsets.only(top: 6, bottom: 12),
                    child: Text('With the country code, e.g. +2348012345678', style: tkHint(t)),
                  ),
                  if (_s.error != null) TkErrorBox(_s.error!),
                  TkButton(
                    label: _s.busy ? 'Sending…' : 'Send me a code',
                    primary: true,
                    minHeight: 44,
                    enabled: !_s.busy,
                    onPressed: () => _s.sendCode(_phone.text),
                  ),
                ] else if (_s.step == AppSignupStep.code) ...[
                  Text(_s.sent ?? 'We sent a code to your WhatsApp.', style: tkHint(t)),
                  const SizedBox(height: 12),
                  TkField(label: 'The code', controller: _code, digits: 6, keyboard: TextInputType.number, autofill: const [AutofillHints.oneTimeCode]),
                  const SizedBox(height: 12),
                  if (_s.error != null) TkErrorBox(_s.error!),
                  TkButton(
                    label: _s.busy ? 'Checking…' : 'Continue',
                    primary: true,
                    minHeight: 44,
                    enabled: !_s.busy,
                    onPressed: () => _s.verify(_code.text),
                  ),
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: Center(child: TkLink(label: 'Use another number', onTap: _s.busy ? null : _s.restart)),
                  ),
                ] else if (_s.step == AppSignupStep.signIn) ...[
                  if (_s.error != null) TkErrorBox(_s.error!),
                  TkButton(label: 'Sign in', primary: true, minHeight: 44, onPressed: widget.onSignIn),
                ],
                const SizedBox(height: 12),
                Wrap(
                  alignment: WrapAlignment.center,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text('Already on Taskiem? ', style: tkHint(t)),
                    TkLink(label: 'Sign in', onTap: widget.onSignIn),
                  ],
                ),
                if (widget.onEmail != null)
                  Center(child: TkLink(label: 'Sign up with email instead', onTap: widget.onEmail)),
              ],
            ),
          ],
        ),
      ),
    );
  }

  /// The conversation with Taskiem: its questions on the left, the answers
  /// on the right, its buttons under its last question, a box to type in.
  Widget _chat(TkTokens t) {
    final pad = MediaQuery.paddingOf(context);
    final ended = _s.step == AppSignupStep.ended;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: SystemUiOverlayStyle.light,
      child: ColoredBox(
        color: t.chatWallpaper,
        child: Column(
          children: [
            Container(
              color: t.brand,
              padding: EdgeInsets.fromLTRB(16, pad.top + 12, 16, 12),
              child: Row(
                children: [
                  Expanded(
                    child: Semantics(
                      header: true,
                      child: Text('Taskiem', style: tkText(size: 17, weight: 600, color: t.tkOnPine)),
                    ),
                  ),
                  if (!ended) Text('Setting up your business', style: tkText(size: 12.5, color: t.tkOnPine)),
                ],
              ),
            ),
            Expanded(
              child: ListView(
                controller: _scroll,
                padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
                children: [
                  for (final l in _s.lines) _Bubble(line: l),
                  if (_s.buttons.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 4),
                      child: Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        children: [
                          for (final b in _s.buttons)
                            TkButton(label: b.title, minHeight: 44, enabled: !_s.busy, onPressed: () => _s.answer(b.title, reply: b.id)),
                        ],
                      ),
                    ),
                  if (_s.error != null) TkErrorBox(_s.error!),
                ],
              ),
            ),
            Container(
              color: t.panel,
              padding: EdgeInsets.fromLTRB(12, 8, 12, 8 + pad.bottom),
              child: ended
                  ? Row(
                      children: [
                        Expanded(child: TkButton(label: 'Start again', minHeight: 44, onPressed: _s.restart)),
                        const SizedBox(width: 8),
                        Expanded(child: TkButton(label: 'Sign in', minHeight: 44, onPressed: widget.onSignIn)),
                      ],
                    )
                  : Row(
                      children: [
                        Expanded(child: TkField(label: 'Your answer', controller: _answer)),
                        const SizedBox(width: 8),
                        TkButton(label: _s.busy ? '…' : 'Send', primary: true, minHeight: 44, enabled: !_s.busy, onPressed: _send),
                      ],
                    ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Bubble extends StatelessWidget {
  const _Bubble({required this.line});
  final SignupLine line;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final mine = line.mine;
    return Align(
      alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: MediaQuery.sizeOf(context).width * 0.82),
        child: Container(
          margin: const EdgeInsets.only(bottom: 8),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: mine ? t.chatOut : t.chatIn,
            borderRadius: BorderRadius.circular(Tk.radius),
          ),
          // WhatsApp's *bold* shown as the words alone.
          child: Text(line.text.replaceAll('*', ''), style: tkText(size: 15, lineHeight: 1.4, color: t.text)),
        ),
      ),
    );
  }
}
