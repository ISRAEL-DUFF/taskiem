// The phone way in (docs/spec/taskiem-for-everyone.md §5.1): the app's
// first screen. A phone number, the 6-digit code sent to it, and for a new
// number a name, 18 or over and the terms. Drawn as signing in is: the brand
// on pine above, one card over its edge. Email, passkeys and the chat code
// stay behind **Sign in with email**.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'sign_in.dart';

/// Where the phone way in is.
enum WelcomeStep { phone, code, create }

class WelcomeScreen extends StatefulWidget {
  const WelcomeScreen({super.key, this.auth, this.onEmail, this.step = WelcomeStep.phone, this.phone = '', this.sentVia = 'whatsapp'});

  /// Signing in; none in a picture of the screen alone.
  final Auth? auth;

  /// **Sign in with email**: today's email, passkey and chat sign-in.
  final VoidCallback? onEmail;

  /// The step shown first, and its number and how the code went (a test
  /// or a picture of one step).
  final WelcomeStep step;
  final String phone;
  final String sentVia;

  @override
  State<WelcomeScreen> createState() => _WelcomeScreenState();
}

/// What went wrong with a code, in plain words (the web's codeProblem).
String codeProblem(ApiError e) {
  if (e.has('locked')) return 'Too many wrong codes. This number is locked for an hour: try again later.';
  if (e.has('sms_unavailable')) return 'We can\'t send codes by SMS here. Ask for the code on WhatsApp instead.';
  if (e.has('phone_signin_off')) return 'Signing in with a code to your phone is turned off for this account. Sign in with your email instead.';
  if (e.status == 401) return 'That code is not right, or it has expired. Check it, or ask for a new one.';
  if (e.status == 429) return e.body['error'] is String ? e.message : 'Too many tries. Wait a little, then try again.';
  if (e.status == 503) return 'We can\'t send codes to phones just now. Sign in with your email instead.';
  if (e.status == 400) return 'Enter your phone number, with its country code if it is not Nigerian.';
  return e.message;
}

class _WelcomeScreenState extends State<WelcomeScreen> with AuthActing {
  @override
  String words(ApiError e) => codeProblem(e);

  late WelcomeStep _step = widget.step;
  late final _phone = TextEditingController(text: widget.phone);
  final _code = TextEditingController();
  final _name = TextEditingController();
  late String _sentVia = widget.sentVia;
  String? _ticket;
  bool _adult = false;
  bool _terms = false;
  TermsAsked? _termsAsked;
  String? _note;

  @override
  void initState() {
    super.initState();
    // The terms' address, to read them before agreeing.
    widget.auth?.api.signupOptions().then((o) {
      if (mounted) setState(() => _termsAsked = o.terms);
    }, onError: (_) {});
  }

  @override
  void dispose() {
    _phone.dispose();
    _code.dispose();
    _name.dispose();
    super.dispose();
  }

  void _send({bool sms = false, bool again = false}) {
    final a = widget.auth;
    if (a == null || busy) return;
    if (_phone.text.trim().isEmpty) {
      setState(() => error = 'Enter your phone number, with its country code if it is not Nigerian.');
      return;
    }
    act(() async {
      final via = await a.sendPhoneCode(_phone.text.trim(), sms: sms);
      if (!mounted) return;
      setState(() {
        _sentVia = via;
        _step = WelcomeStep.code;
        _code.clear();
        _note = again || sms ? 'We sent a new code.' : null;
      });
    });
  }

  void _verify() {
    final a = widget.auth;
    if (a == null || busy || _code.text.length != 6) return;
    act(() async {
      final ticket = await a.verifyPhoneCode(_phone.text.trim(), _code.text);
      if (ticket != null && mounted) {
        setState(() {
          _ticket = ticket;
          _step = WelcomeStep.create;
        });
      }
    });
  }

  void _create() {
    final a = widget.auth;
    final ticket = _ticket;
    if (a == null || busy || ticket == null) return;
    final name = _name.text.trim();
    if (name.isEmpty) {
      setState(() => error = 'Enter your name.');
      return;
    }
    if (!_adult) {
      setState(() => error = 'You need to be 18 or over to use Taskiem.');
      return;
    }
    if (!_terms) {
      setState(() => error = 'Agree to the terms and privacy notice to go on.');
      return;
    }
    act(() => a.createPersonal(ticket, name));
  }

  void _back() => setState(() {
        _step = WelcomeStep.phone;
        _code.clear();
        error = null;
        _note = null;
      });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ColoredBox(
      color: t.bg,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const AuthSide(line: 'Chat with the businesses you buy from, and run your own.'),
            AuthCard(
              children: switch (_step) {
                WelcomeStep.phone => _phoneStep(t),
                WelcomeStep.code => _codeStep(t),
                WelcomeStep.create => _createStep(t),
              },
            ),
          ],
        ),
      ),
    );
  }

  List<Widget> _phoneStep(TkTokens t) => [
        const AuthTitle('Welcome to Taskiem'),
        Padding(
          padding: const EdgeInsets.only(bottom: 16),
          child: Text('Chat with the businesses you buy from, and run your own.', style: tkText(size: 14, color: t.muted)),
        ),
        TkField(
          key: const ValueKey('phone'),
          label: 'Your phone number',
          controller: _phone,
          keyboard: TextInputType.phone,
          autofill: const [AutofillHints.telephoneNumber],
          minHeight: 44,
          onSubmitted: (_) => _send(),
        ),
        Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Text('A Nigerian number can start with 0. For another country, start with + and its code.', style: tkHint(t)),
        ),
        if (error != null) ...[const SizedBox(height: 4), TkErrorBox(error!)] else const SizedBox(height: 16),
        TkButton(label: busy ? 'Sending…' : 'Continue', primary: true, minHeight: 44, enabled: !busy, onPressed: _send),
        Padding(
          padding: const EdgeInsets.only(top: 12, bottom: 12),
          child: Center(child: TkLink(label: 'Sign in with email', onTap: widget.onEmail)),
        ),
      ];

  List<Widget> _codeStep(TkTokens t) {
    return [
      const AuthTitle('Enter the code'),
      Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Semantics(
          liveRegion: true,
          child: Text(
            _sentVia == 'sms' ? 'We sent a 6-digit code by SMS to ${_phone.text.trim()}.' : 'We sent a 6-digit code to WhatsApp on ${_phone.text.trim()}.',
            style: tkText(size: 14, color: t.text)),
        ),
      ),
      TkField(
        key: const ValueKey('code'),
        label: '6-digit code',
        controller: _code,
        autofocus: true,
        keyboard: TextInputType.number,
        autofill: const [AutofillHints.oneTimeCode],
        digits: 6,
        minHeight: 44,
        onChanged: (_) => setState(() {}),
        onSubmitted: (_) => _verify(),
      ),
      if (error != null)
        ...[const SizedBox(height: 4), TkErrorBox(error!)]
      else if (_note != null)
        Padding(padding: const EdgeInsets.symmetric(vertical: 8), child: Text(_note!, style: tkHint(t)))
      else
        const SizedBox(height: 16),
      TkButton(
        label: busy ? 'Checking…' : 'Continue',
        primary: true,
        minHeight: 44,
        enabled: !busy && _code.text.length == 6,
        onPressed: _verify,
      ),
      if (_sentVia != 'sms')
        Center(child: TkLinkButton(label: 'Send by SMS instead', onPressed: busy ? null : () => _send(sms: true))),
      Center(child: TkLinkButton(label: 'Send a new code', onPressed: busy ? null : () => _send(sms: _sentVia == 'sms', again: true))),
      Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Center(child: TkLinkButton(label: 'Use another number', onPressed: _back)),
      ),
    ];
  }

  List<Widget> _createStep(TkTokens t) {
    final words = tkText(size: 14, color: t.text);
    final url = _termsAsked?.url;
    return [
      const AuthTitle('Make your account'),
      Padding(
        padding: const EdgeInsets.only(bottom: 16),
        child: Text('This number is new to Taskiem. Tell us your name to finish.', style: tkText(size: 14, color: t.muted)),
      ),
      TkField(
        key: const ValueKey('name'),
        label: 'Your name',
        controller: _name,
        autofocus: true,
        autofill: const [AutofillHints.name],
        minHeight: 44,
      ),
      const SizedBox(height: 16),
      TkChoiceRow(
        radio: false,
        control: TkCheckbox(checked: _adult),
        label: Text('I\'m 18 or over', style: words),
        onTap: () => setState(() => _adult = !_adult),
      ),
      TkChoiceRow(
        radio: false,
        control: TkCheckbox(checked: _terms),
        label: Text.rich(TextSpan(style: words, children: [
          const TextSpan(text: 'I agree to Taskiem\'s '),
          WidgetSpan(
            alignment: PlaceholderAlignment.baseline,
            baseline: TextBaseline.alphabetic,
            child: GestureDetector(
              onTap: url == null ? null : () => openInBrowser(Uri.parse(url)),
              child: Text(
                'terms and privacy notice',
                style: words.copyWith(color: t.accent, decoration: TextDecoration.underline, decorationColor: t.accent),
              ),
            ),
          ),
        ])),
        onTap: () => setState(() => _terms = !_terms),
      ),
      if (error != null) TkErrorBox(error!),
      TkButton(label: busy ? 'Creating…' : 'Create', primary: true, minHeight: 44, enabled: !busy, onPressed: _create),
      Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Center(child: TkLinkButton(label: 'Use another number', onPressed: _back)),
      ),
    ];
  }
}
