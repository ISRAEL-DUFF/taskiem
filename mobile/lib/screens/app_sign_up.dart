// Get started (docs/spec/mobile-app-m4.md N7): signing up as on WhatsApp,
// in the app. The number, the code WhatsApp brings, then a chat with Taskiem
// asking the same questions WhatsApp's sign-up asks, with its buttons. At the
// end the business is ready and the app opens on it.

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../app/app_signup.dart';
import '../app/auth.dart';
import '../chat/taskiem.dart' show TaskiemSignupBubble;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'sign_in.dart' show AuthButton, AuthCard, AuthLink, AuthSide, AuthTitle, CodeBoxes;

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
                  Text('Your business on Taskiem in a few minutes, as on WhatsApp. First, your WhatsApp number: we send a code to it.', style: _lede(t)),
                  const SizedBox(height: 12),
                  TkField(label: 'Your WhatsApp number', controller: _phone, keyboard: TextInputType.phone, autofill: const [AutofillHints.telephoneNumber], minHeight: 44),
                  Padding(
                    padding: const EdgeInsets.only(top: 6, bottom: 12),
                    child: Text('With the country code, e.g. +2348012345678', style: tkText(size: 12.5, color: t.muted)),
                  ),
                  if (_s.error != null) TkErrorBox(_s.error!),
                  AuthButton(label: _s.busy ? 'Sending…' : 'Send me a code', enabled: !_s.busy, onTap: () => _s.sendCode(_phone.text)),
                ] else if (_s.step == AppSignupStep.code) ...[
                  Text(_s.sent ?? 'We sent a code to your WhatsApp.', style: _lede(t)),
                  const SizedBox(height: 12),
                  CodeBoxes(controller: _code, onSubmitted: (_) => _s.verify(_code.text)),
                  const SizedBox(height: 14),
                  if (_s.error != null) TkErrorBox(_s.error!),
                  AuthButton(label: _s.busy ? 'Checking…' : 'Continue', enabled: !_s.busy, onTap: () => _s.verify(_code.text)),
                  const SizedBox(height: 6),
                  Center(child: AuthLink(label: 'Use another number', onTap: _s.busy ? null : _s.restart)),
                ] else if (_s.step == AppSignupStep.signIn) ...[
                  if (_s.error != null) TkErrorBox(_s.error!),
                  AuthButton(label: 'Sign in', onTap: widget.onSignIn),
                ],
                const SizedBox(height: 8),
                Wrap(
                  alignment: WrapAlignment.center,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text('Already on Taskiem? ', style: tkText(size: 13.5, color: t.muted)),
                    AuthLink(label: 'Sign in', onTap: widget.onSignIn, size: 13.5),
                  ],
                ),
                if (widget.onEmail != null)
                  Center(child: AuthLink(label: 'Sign up with email instead', onTap: widget.onEmail, size: 13.5)),
              ],
            ),
          ],
        ),
      ),
    );
  }

  TextStyle _lede(TkTokens t) => tkText(size: 14, lineHeight: 1.45, color: t.muted);

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
              // As drawn: Taskiem's mark on lime, its name, and what this is.
              child: Row(
                children: [
                  Container(
                    width: 36,
                    height: 36,
                    alignment: Alignment.center,
                    decoration: const BoxDecoration(color: Color(0xFFC8F169), shape: BoxShape.circle),
                    child: TkTaskiemMark(size: 20, color: t.brand),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Semantics(header: true, child: Text('Taskiem', style: tkText(size: 16, weight: 700, color: t.tkOnPine))),
                        if (!ended) Text('Setting up your business', style: tkText(size: 12.5, color: t.tkOnPine.withValues(alpha: 0.75))),
                      ],
                    ),
                  ),
                ],
              ),
            ),
            Expanded(
              child: ListView(
                controller: _scroll,
                padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
                children: [
                  // Taskiem's own bubbles and buttons, as its conversation draws them.
                  for (final (i, l) in _s.lines.indexed)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 6),
                      child: TaskiemSignupBubble(
                        id: '$i',
                        text: l.text,
                        mine: l.mine,
                        first: i == 0 || _s.lines[i - 1].mine != l.mine,
                        at: l.at,
                        buttons: l.buttons,
                        pressable: !_s.busy && i == _s.lines.length - 1 && !l.mine,
                        onPress: (id, title) => _s.answer(title, reply: id),
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
                        Expanded(child: AuthButton(label: 'Start again', soft: true, onTap: _s.restart)),
                        const SizedBox(width: 8),
                        Expanded(child: AuthButton(label: 'Sign in', onTap: widget.onSignIn)),
                      ],
                    )
                  // As drawn: the answer in a pill, sent with the round button.
                  : Row(
                      children: [
                        Expanded(
                          child: Semantics(
                            label: 'Your answer',
                            textField: true,
                            child: Container(
                              constraints: const BoxConstraints(minHeight: 46),
                              padding: const EdgeInsets.symmetric(horizontal: 16),
                              alignment: Alignment.centerLeft,
                              decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(999)),
                              child: Material(
                                type: MaterialType.transparency,
                                child: TextField(
                                  controller: _answer,
                                  textInputAction: TextInputAction.send,
                                  onSubmitted: (_) => _send(),
                                  style: tkText(size: 15, color: t.text),
                                  cursorColor: t.text,
                                  showCursor: !TkField.debugHideCaret,
                                  decoration: InputDecoration.collapsed(hintText: 'Your answer', hintStyle: tkText(size: 15, color: t.muted)),
                                ),
                              ),
                            ),
                          ),
                        ),
                        const SizedBox(width: 8),
                        Semantics(
                          button: true,
                          enabled: !_s.busy,
                          label: 'Send',
                          onTap: _s.busy ? null : _send,
                          excludeSemantics: true,
                          child: TkPress(
                            onTap: _s.busy ? null : _send,
                            child: Container(
                              width: 46,
                              height: 46,
                              alignment: Alignment.center,
                              decoration: BoxDecoration(color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand, shape: BoxShape.circle),
                              child: TkIcon('send', size: 20, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : const Color(0xFFC8F169)),
                            ),
                          ),
                        ),
                      ],
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
