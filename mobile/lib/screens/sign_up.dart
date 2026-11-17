// Signing up: the web's page (web/src/pages/Signup.tsx), drawn the same,
// over the same brand on pine as signing in. A business and its owner, signed
// in at once on this phone (a device session, docs/spec/mobile-app.md §3.6).

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/look.dart' show TkLook;
import '../theme/theme.dart';
import 'sign_in.dart';

/// What a business says it does, in its own words (as the web's SIGNUP_KINDS),
/// and the tile's shorter words and icon (as drawn).
const signupKinds = <({String id, String label, String short, String icon})>[
  (id: 'shop', label: 'I sell things', short: 'I sell things', icon: 'b-bag'),
  (id: 'service', label: 'I bill customers for a service', short: 'I bill for a service', icon: 'b-receipt'),
  (id: 'delivery', label: 'I deliver for businesses', short: 'I deliver for businesses', icon: 'b-bike'),
  (id: 'school', label: 'I run a school', short: 'I run a school', icon: 'schools'),
  (id: 'workflows', label: 'Just workflows for now', short: 'Just workflows', icon: 'b-route'),
];

class SignUpScreen extends StatefulWidget {
  const SignUpScreen({super.key, this.auth, this.onSignIn, this.terms});

  final Auth? auth;
  final VoidCallback? onSignIn;

  /// The terms to agree to, when the deployment has them (GET /v1/signup);
  /// read here when not given.
  final TermsAsked? terms;

  @override
  State<SignUpScreen> createState() => _SignUpScreenState();
}

class _SignUpScreenState extends State<SignUpScreen> {
  final _tenant = TextEditingController();
  final _name = TextEditingController();
  final _email = TextEditingController();
  final _password = TextEditingController();
  String? _kind;
  bool _accept = false;
  TermsAsked? _terms;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _terms = widget.terms;
    final a = widget.auth;
    if (_terms == null && a != null) {
      a.api.signupOptions().then((o) {
        if (mounted) setState(() => _terms = o.terms);
      }, onError: (_) {});
    }
  }

  @override
  void dispose() {
    for (final c in [_tenant, _name, _email, _password]) {
      c.dispose();
    }
    super.dispose();
  }

  /// The browser's own checks on the web form (required, lengths), said in
  /// words, before anything is sent.
  String? _missing() {
    if (_tenant.text.trim().length < 2) return 'Give your business or organisation a name.';
    if (_kind == null) return 'Choose what your business does.';
    if (!_email.text.contains('@')) return 'Give your email.';
    if (_password.text.length < 12) return 'Your password needs at least 12 characters.';
    if (_terms != null && !_accept) return 'Agree to the terms and privacy notice to go on.';
    return null;
  }

  Future<void> _submit() async {
    final a = widget.auth;
    if (a == null || _busy) return;
    FocusManager.instance.primaryFocus?.unfocus();
    final missing = _missing();
    if (missing != null) return setState(() => _error = missing);
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final r = await a.api.signup(
        body: SignupRequest(
          tenant: _tenant.text.trim(),
          name: _name.text.trim(),
          email: _email.text.trim(),
          password: _password.text,
          acceptTerms: _accept,
          kinds: _kind == 'workflows' ? const [] : [_kind!],
          bearer: true,
          device: await a.store.device(),
        ),
      );
      await a.signedInWith(r.token);
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Check your connection and try again.';
    } catch (_) {
      _error = 'Something went wrong. Try again.';
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final hint = tkHint(t);
    final words = tkText(size: 14, color: t.text);
    return ColoredBox(
      color: TkLook.of(context).ground,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const AuthSide(),
            AuthCard(
              children: [
                const AuthTitle('Start with Taskiem'),
                TkField(label: 'Business or organisation name', controller: _tenant, autofocus: true, autofill: const [AutofillHints.organizationName], minHeight: 44),
                const SizedBox(height: 16),
                Semantics(
                  label: 'What does your business do?',
                  container: true,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Padding(
                        padding: const EdgeInsets.only(bottom: 6),
                        child: ExcludeSemantics(child: Text('What does your business do?', style: tkText(size: 12.5, weight: 600, color: t.muted))),
                      ),
                      // As drawn: tiles, two to a row, an icon and short words.
                      LayoutBuilder(
                        builder: (context, box) {
                          final w = (box.maxWidth - 8) / 2;
                          return Wrap(
                            spacing: 8,
                            runSpacing: 8,
                            children: [for (final k in signupKinds) SizedBox(width: w, child: _KindTile(kind: k, on: _kind == k.id, onTap: () => setState(() => _kind = k.id)))],
                          );
                        },
                      ),
                      const SizedBox(height: 16),
                    ],
                  ),
                ),
                TkField(label: 'Your name', controller: _name, autofill: const [AutofillHints.name], minHeight: 44),
                const SizedBox(height: 16),
                TkField(label: 'Email', controller: _email, keyboard: TextInputType.emailAddress, autofill: const [AutofillHints.email], minHeight: 44),
                const SizedBox(height: 16),
                TkField(label: 'Password', controller: _password, obscure: true, autofill: const [AutofillHints.newPassword], minHeight: 44),
                Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: Text('At least 12 characters. A short sentence is easy to remember.', style: hint),
                ),
                const SizedBox(height: 16),
                if (_terms != null)
                  TkChoiceRow(
                    radio: false,
                    control: TkCheckbox(checked: _accept),
                    label: Text.rich(TextSpan(style: words, children: [
                      const TextSpan(text: 'I agree to Taskiem\'s '),
                      TextSpan(text: 'terms and privacy notice', style: words.copyWith(color: t.accent, decoration: TextDecoration.underline, decorationColor: t.accent)),
                    ])),
                    onTap: () => setState(() => _accept = !_accept),
                  ),
                if (_error != null) ...[TkErrorBox(_error!)],
                if (_error == null) const SizedBox(height: 4),
                AuthButton(label: _busy ? 'Creating…' : 'Create account', enabled: !_busy, onTap: _submit),
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  // It wraps when the words are large.
                  child: Wrap(
                    alignment: WrapAlignment.center,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    children: [
                      Text('Already have an account? ', style: tkText(size: 13.5, color: t.muted)),
                      AuthLink(label: 'Sign in', onTap: widget.onSignIn, size: 13.5),
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

/// A kind of business as a tile (as drawn): pine when chosen.
class _KindTile extends StatelessWidget {
  const _KindTile({required this.kind, required this.on, required this.onTap});
  final ({String id, String label, String short, String icon}) kind;
  final bool on;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = on ? (dark ? (t.brandDark, t.brandDarkText) : (t.brand, const Color(0xFFFFFFFF))) : (t.field, t.text);
    return Semantics(
      button: true,
      selected: on,
      inMutuallyExclusiveGroup: true,
      label: kind.label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 160),
          constraints: const BoxConstraints(minHeight: 60),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(14)),
          child: Row(
            children: [
              TkIcon(kind.icon, size: 20, color: ink),
              const SizedBox(width: 10),
              Expanded(child: Text(kind.short, style: tkText(size: 13.5, weight: 600, lineHeight: 1.25, color: ink))),
            ],
          ),
        ),
      ),
    );
  }
}
