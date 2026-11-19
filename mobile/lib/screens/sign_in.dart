// Signing in: the web's sign-in page (web/src/App.tsx, signed out; and
// pages/Login.tsx), drawn the same: the brand on pine above, one card over
// its edge. Sizes are the web's at a phone's width (styles.css: .auth-side,
// .auth-brand, .auth-line, .auth-main .login). Two ways, as on the web:
// email and password, and a code sent to the person's WhatsApp or Telegram.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter_svg/flutter_svg.dart';

import '../theme/look.dart' show TkLook;

import '../api/transport.dart';
import '../app/auth.dart';
import '../app/phone.dart';
import '../app/passkey.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';

class SignInScreen extends StatefulWidget {
  const SignInScreen({super.key, this.auth, this.signup = true, this.onSignUp, this.onForgot, this.onPasskey, this.passkeys = true, this.onPhone});

  /// Back to the phone way in (the app's first screen), when it is.
  final VoidCallback? onPhone;

  /// Signing in; none in a picture of the screen alone.
  final Auth? auth;

  /// Whether self-serve signup is open (GET /v1/signup): "New to Taskiem?".
  final bool signup;
  final VoidCallback? onSignUp;

  /// Forgot password?, with what was typed in Email to start from.
  final ValueChanged<String>? onForgot;
  final VoidCallback? onPasskey;

  /// Whether the server has passkeys on (GET /v1/app/config): off, the
  /// passkey button is not offered.
  final bool passkeys;

  @override
  State<SignInScreen> createState() => _SignInScreenState();
}

class _SignInScreenState extends State<SignInScreen> {
  bool _withChat = false;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ColoredBox(
      color: TkLook.of(context).ground,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const AuthSide(),
            AuthCard(
              children: [
                AuthTitle(_withChat ? 'Sign in with a code' : 'Sign in to Taskiem'),
                if (_withChat)
                  _ChatSignIn(auth: widget.auth, onBack: () => setState(() => _withChat = false))
                else
                  _EmailSignIn(
                    auth: widget.auth,
                    onChat: () => setState(() => _withChat = true),
                    onForgot: widget.onForgot,
                    onPasskey: widget.onPasskey,
                    passkeys: widget.passkeys,
                  ),
                // Back to the phone way in (the web's "Use your phone number
                // instead", shown under either way).
                if (widget.onPhone != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 12),
                    child: Center(child: AuthLink(label: 'Use your phone number instead', onTap: widget.onPhone)),
                  ),
                if (widget.signup && !_withChat)
                  Padding(
                    padding: const EdgeInsets.only(top: 12),
                    // It wraps when the words are large.
                    child: Wrap(
                      alignment: WrapAlignment.center,
                      crossAxisAlignment: WrapCrossAlignment.center,
                      children: [
                        Text('New to Taskiem? ', style: tkText(size: 13.5, color: t.muted)),
                        AuthLink(label: 'Create an account', onTap: widget.onSignUp, size: 13.5),
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
mixin AuthActing<T extends StatefulWidget> on State<T> {
  bool busy = false;
  String? error;
  bool passkeyRequired = false;

  /// The server's answer in the words shown (its own, unless a screen has
  /// plainer ones).
  String words(ApiError e) => e.status == 429 ? tooMany : sentence(e.message);

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
    } on PasskeyCancelled {
      // Closed the phone's passkey sheet: nothing to say.
    } on ApiError catch (e) {
      error = words(e);
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
  const _EmailSignIn({this.auth, this.onChat, this.onForgot, this.onPasskey, this.passkeys = true});
  final Auth? auth;
  final VoidCallback? onChat;
  final ValueChanged<String>? onForgot;
  final VoidCallback? onPasskey;
  final bool passkeys;

  @override
  State<_EmailSignIn> createState() => _EmailSignInState();
}

class _EmailSignInState extends State<_EmailSignIn> with AuthActing {
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
        AuthButton(label: busy ? 'Signing in…' : 'Sign in', enabled: !busy, onTap: _submit),
        const SizedBox(height: 8),
        Center(child: AuthLink(label: 'Forgot password?', onTap: widget.onForgot == null ? null : () => widget.onForgot!(_email.text.trim()))),
        const SizedBox(height: 4),
        // As drawn: a line, "or", a line.
        Row(
          children: [
            Expanded(child: Container(height: 1, color: t.border)),
            Padding(padding: const EdgeInsets.symmetric(horizontal: 10), child: Text('or', style: tkText(size: 13, color: t.muted))),
            Expanded(child: Container(height: 1, color: t.border)),
          ],
        ),
        const SizedBox(height: 12),
        if (widget.passkeys) ...[
          AuthButton(
            label: 'Sign in with a passkey',
            icon: 'fingerprint',
            soft: true,
            // The phone's own passkey (lib/app/passkey.dart; M4 N6).
            onTap: widget.onPasskey ?? (widget.auth == null || busy ? null : () => act(() => signInWithPasskey(widget.auth!))),
          ),
          const SizedBox(height: 14),
        ],
        // As drawn: the two apps' logos, not a long button; either asks for
        // the number, and the code goes to whichever the person has.
        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            _ChatLogo(app: 'WhatsApp', onTap: widget.onChat),
            const SizedBox(width: 18),
            _ChatLogo(app: 'Telegram', onTap: widget.onChat),
          ],
        ),
        const SizedBox(height: 6),
        ExcludeSemantics(child: Text('A code on WhatsApp or Telegram', textAlign: TextAlign.center, style: tkText(size: 12.5, color: t.muted))),
        if (passkeyRequired)
          Padding(
            padding: const EdgeInsets.only(top: 12),
            child: Text('Sign in with your passkey.', style: tkHint(t)),
          ),
      ],
    );
  }
}

/// WhatsApp's or Telegram's logo, round in its own colour (as drawn).
class _ChatLogo extends StatelessWidget {
  const _ChatLogo({required this.app, this.onTap});
  final String app;
  final VoidCallback? onTap;

  static const _wa = '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M12 3.2a8.8 8.8 0 00-7.6 13.2L3.3 20.7l4.4-1.1A8.8 8.8 0 1012 3.2z" fill="#fff"/>'
      '<path d="M9.2 7.9c.2 0 .4 0 .5.4l.7 1.6c.1.2 0 .4-.1.6l-.5.6c.6 1.2 1.6 2.2 2.8 2.8l.6-.6c.2-.2.4-.2.6-.1l1.6.7c.3.1.4.3.4.5 0 1.1-.9 1.9-2 1.9-3.2-.2-6-3-6.3-6.3 0-1.1.8-2.1 1.7-2.1z" fill="#25d366"/></svg>';
  static const _tg = '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M5 11.6l12.6-4.9c.6-.2 1.1.1.9 1l-2.1 10c-.2.7-.6.9-1.2.5l-3.2-2.4-1.6 1.5c-.2.2-.3.3-.6.3l.2-3.3 6-5.4c.3-.2-.1-.4-.4-.2l-7.4 4.7-3.2-1c-.7-.2-.7-.7.1-1z" fill="#fff"/></svg>';

  @override
  Widget build(BuildContext context) {
    final wa = app == 'WhatsApp';
    return TapArea(
      child: Semantics(
        button: true,
        label: 'Sign in with a code on $app',
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            width: 52,
            height: 52,
            alignment: Alignment.center,
            decoration: BoxDecoration(color: wa ? const Color(0xFF25D366) : const Color(0xFF2AABEE), shape: BoxShape.circle),
            child: SvgPicture.string(wa ? _wa : _tg, width: 30, height: 30),
          ),
        ),
      ),
    );
  }
}

/// The sign-in card's buttons, as drawn: a pine pill (lime in the dark
/// theme), or a soft one; with an icon before the words.
class AuthButton extends StatelessWidget {
  const AuthButton({super.key, required this.label, this.onTap, this.enabled = true, this.soft = false, this.icon});
  final String label;
  final VoidCallback? onTap;
  final bool enabled;
  final bool soft;
  final String? icon;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = soft ? (t.field, t.text) : (dark ? t.brandDark : t.brand, dark ? t.brandDarkText : const Color(0xFFFFFFFF));
    final on = enabled && onTap != null;
    return TapArea(
      child: Semantics(
        button: true,
        enabled: on,
        label: label,
        onTap: on ? onTap : null,
        excludeSemantics: true,
        child: Opacity(
          opacity: on ? 1 : 0.45,
          child: TkPress(
            onTap: on ? onTap : null,
            child: Container(
              constraints: const BoxConstraints(minHeight: 48),
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              alignment: Alignment.center,
              decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (icon != null) ...[TkIcon(icon!, size: 18, color: ink), const SizedBox(width: 8)],
                  Flexible(child: Text(label, textAlign: TextAlign.center, style: tkText(size: 15, weight: 700, lineHeight: 1.2, color: ink))),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A link on the sign-in card, as drawn: bold, pine (lime in the dark), no
/// line under it.
class AuthLink extends StatelessWidget {
  const AuthLink({super.key, required this.label, this.onTap, this.size = 14});
  final String label;
  final VoidCallback? onTap;
  final double size;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return TapArea(
      child: Semantics(
        link: true,
        enabled: onTap != null,
        label: label,
        onTap: onTap,
        excludeSemantics: true,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Text(label, textAlign: TextAlign.center, style: tkText(size: size, weight: 700, color: dark ? t.brandDark : t.brand)),
          ),
        ),
      ),
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

class _ChatSignInState extends State<_ChatSignIn> with AuthActing {
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
            child: Text('We send a code to the WhatsApp or Telegram on your Taskiem account.', style: tkText(size: 14, lineHeight: 1.45, color: t.muted)),
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
          AuthButton(label: busy ? 'Sending…' : 'Send me a code', enabled: !busy, onTap: _send),
        ] else ...[
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Semantics(liveRegion: true, child: Text(sent, style: tkText(size: 14, lineHeight: 1.45, color: t.muted))),
          ),
          CodeBoxes(
            key: const ValueKey('code'),
            controller: _code,
            onChanged: (_) => setState(() {}),
            onSubmitted: (_) => _verify(),
          ),
          if (error != null) ...[const SizedBox(height: 4), TkErrorBox(error!)] else const SizedBox(height: 16),
          AuthButton(
            label: busy ? 'Signing in…' : 'Sign in',
            enabled: !busy && _code.text.length == 6,
            onTap: _verify,
          ),
          const SizedBox(height: 6),
          Center(
            child: AuthLink(
              label: 'Send a new code',
              onTap: () => setState(() {
                _sent = null;
                _code.clear();
                error = null;
              }),
            ),
          ),
        ],
        Center(child: AuthLink(label: 'Sign in with your email instead', onTap: widget.onBack)),
      ],
    );
  }
}

/// The code as drawn: six boxes, a digit in each. One field under them
/// takes the typing, a paste and the phone's offer of the code from a
/// message; read as one field, "Code".
class CodeBoxes extends StatefulWidget {
  const CodeBoxes({super.key, required this.controller, this.onChanged, this.onSubmitted, this.length = 6});
  final TextEditingController controller;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onSubmitted;
  final int length;

  @override
  State<CodeBoxes> createState() => _CodeBoxesState();
}

class _CodeBoxesState extends State<CodeBoxes> {
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(_redraw);
    widget.controller.addListener(_redraw);
  }

  void _redraw() => setState(() {});

  @override
  void dispose() {
    widget.controller.removeListener(_redraw);
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final text = widget.controller.text;
    final focused = _focus.hasFocus;
    return SizedBox(
      height: 54,
      child: Stack(
        children: [
          ExcludeSemantics(
            child: Row(
              children: [
                for (var i = 0; i < widget.length; i++) ...[
                  if (i > 0) const SizedBox(width: 8),
                  Expanded(
                    child: Container(
                      height: 54,
                      alignment: Alignment.center,
                      decoration: BoxDecoration(
                        color: focused && i == text.length.clamp(0, widget.length - 1) ? t.fieldFocus : t.field,
                        borderRadius: BorderRadius.circular(12),
                        boxShadow: focused && i == text.length.clamp(0, widget.length - 1) ? t.ring : const [],
                      ),
                      child: Text(i < text.length ? text[i] : '', style: tkDisplay(size: 22, color: t.text)),
                    ),
                  ),
                ],
              ],
            ),
          ),
          Positioned.fill(
            child: Opacity(
              opacity: 0.011,
              child: Material(
                type: MaterialType.transparency,
                child: TextField(
                  controller: widget.controller,
                  focusNode: _focus,
                  autofocus: true,
                  keyboardType: TextInputType.number,
                  autofillHints: const [AutofillHints.oneTimeCode],
                  showCursor: false,
                  enableInteractiveSelection: false,
                  onChanged: widget.onChanged,
                  onSubmitted: widget.onSubmitted,
                  style: tkText(size: 1, color: t.text),
                  decoration: const InputDecoration.collapsed(hintText: 'Code'),
                  inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(widget.length)],
                ),
              ),
            ),
          ),
        ],
      ),
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
    final look = TkLook.of(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    // As drawn: the card 14 px from the sides, 46 px up over the pine; its
    // fields and soft buttons on the page's ground (black in the dark).
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 0, 14, 32),
      child: Transform.translate(
        offset: const Offset(0, -46),
        child: Container(
          padding: const EdgeInsets.fromLTRB(18, 20, 18, 18),
          decoration: BoxDecoration(
            color: look.card,
            borderRadius: BorderRadius.circular(24),
            boxShadow: dark ? null : const [BoxShadow(color: Color(0x40000000), offset: Offset(0, 10), blurRadius: 30, spreadRadius: -14)],
          ),
          child: TkAppLook(
            child: TkTheme.recolour(
              context,
              (x) => x.copyWith(field: look.ground, panel: look.card),
              Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children),
            ),
          ),
        ),
      ),
    );
  }
}

/// The card's title (.auth-main .login h1).
class AuthTitle extends StatelessWidget {
  const AuthTitle(this.text, {super.key, this.bottom = 14});
  final String text;

  /// The space under it: 14, as drawn; 8 where a line follows it (the
  /// web's `.way-in-line`).
  final double bottom;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(bottom: bottom),
      child: Semantics(
        header: true,
        child: Text(text, style: tkDisplay(size: 24, lineHeight: 1.2, letterSpacing: -0.6, color: TkTheme.tokens(context).text)),
      ),
    );
  }
}

/// The brand on pine, as drawn: Taskiem's mark on a lime tile, its name,
/// then the line.
class AuthSide extends StatelessWidget {
  const AuthSide({super.key, this.line = 'Orders, payments and approvals for your business, in one place.'});

  /// The line under the brand.
  final String line;

  @override
  Widget build(BuildContext context) {
    const white = Color(0xFFFFFFFF);
    final line = tkText(size: 14, weight: 500, lineHeight: 1.4, color: white.withValues(alpha: 0.8));
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: SystemUiOverlayStyle.light,
      child: Container(
        color: const Color(0xFF0D3B2E),
        padding: EdgeInsets.fromLTRB(22, 60 + MediaQuery.paddingOf(context).top, 22, 70),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: 40,
              height: 40,
              alignment: Alignment.center,
              decoration: BoxDecoration(color: const Color(0xFFC8F169), borderRadius: BorderRadius.circular(12)),
              child: const TkTaskiemMark(size: 22, color: Color(0xFF0D3B2E)),
            ),
            const SizedBox(height: 8),
            Semantics(header: true, child: Text('Taskiem', style: tkDisplay(size: 26, letterSpacing: -0.6, color: white))),
            const SizedBox(height: 6),
            ConstrainedBox(
              // `max-width: 30ch`: thirty of the font's zeros.
              constraints: BoxConstraints(maxWidth: 30 * _zero(line)),
              child: Text(this.line, style: line),
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
