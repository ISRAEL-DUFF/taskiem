// The phone way in (docs/spec/taskiem-for-everyone.md §5.1): the app's
// first screen. A phone number, the 6-digit code sent to it, and for a new
// number a name, 18 or over and the terms. Drawn as signing in is: the brand
// on pine above, one card over its edge. Email, passkeys and the chat code
// stay behind **Sign in with email**.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../app/phone.dart';
import '../theme/look.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'chat_link.dart';
import 'sign_in.dart';

/// Where the phone way in is.
enum WelcomeStep { phone, code, create }

class WelcomeScreen extends StatefulWidget {
  const WelcomeScreen({super.key, this.auth, this.onEmail, this.step = WelcomeStep.phone, this.phone = '', this.sentVia = 'whatsapp', this.chatCode});

  /// A business's chat link opened while signed out (taskiem-for-everyone
  /// §8.2): its card shows over the way in, the code goes with the phone
  /// code (`chat_code`), and the chat starts once signed in.
  final String? chatCode;

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

  /// The number as sent (+234…), once it was.
  late String _e164 = toE164(widget.phone) ?? widget.phone;

  /// Seconds before a new code can be asked for, as the web counts them.
  int _left = 0;
  Timer? _tick;

  void _wait() {
    _tick?.cancel();
    _left = resendAfter;
    _tick = Timer.periodic(const Duration(seconds: 1), (t) {
      if (!mounted) return t.cancel();
      setState(() => _left--);
      if (_left <= 0) t.cancel();
    });
  }

  /// The business of the chat link that opened the app, or why there is none.
  CardAnswer? _link;

  @override
  void initState() {
    super.initState();
    // The terms' address, to read them before agreeing.
    widget.auth?.api.signupOptions().then((o) {
      if (mounted) setState(() => _termsAsked = o.terms);
    }, onError: (_) {});
    final code = widget.chatCode;
    if (code != null && widget.auth != null) {
      readChatLinkCard(widget.auth!.api, code).then((a) {
        if (mounted) setState(() => _link = a);
      }, onError: (_) {});
    }
  }

  @override
  void dispose() {
    _tick?.cancel();
    _phone.dispose();
    _code.dispose();
    _name.dispose();
    super.dispose();
  }

  void _send({bool sms = false}) {
    final a = widget.auth;
    if (a == null || busy) return;
    // Said before anything is sent (the server would answer phone_invalid).
    final number = _step == WelcomeStep.phone ? toE164(_phone.text) : _e164;
    if (number == null) {
      setState(() {
        error = notAPhone;
        _note = null;
      });
      return;
    }
    act(() async {
      final via = await a.sendPhoneCode(number, sms: sms, chatCode: _link?.card == null ? null : widget.chatCode);
      if (!mounted) return;
      setState(() {
        _e164 = number;
        _sentVia = via;
        _step = WelcomeStep.code;
        _code.clear();
        _note = null;
        _wait();
      });
    });
  }

  /// A code run out: back to the number, saying so (as the web does).
  bool _expired(Object e) {
    if (e is! ApiError || !(e.has('ticket_expired') || (_step == WelcomeStep.create && e.status == 401))) return false;
    _back(note: yourCodeExpired);
    return true;
  }

  void _verify() {
    final a = widget.auth;
    if (a == null || busy || _code.text.length != 6) return;
    act(() async {
      try {
        final ticket = await a.verifyPhoneCode(_e164, _code.text);
        if (ticket != null && mounted) {
          setState(() {
            _ticket = ticket;
            _step = WelcomeStep.create;
          });
        }
      } on ApiError catch (e) {
        if (!_expired(e)) rethrow;
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
    act(() async {
      try {
        await a.createPersonal(ticket, name);
      } on ApiError catch (e) {
        if (!_expired(e)) rethrow;
      }
    });
  }

  void _back({String? note}) => setState(() {
        _step = WelcomeStep.phone;
        _code.clear();
        _ticket = null;
        error = null;
        _note = note;
        _tick?.cancel();
        _left = 0;
      });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ColoredBox(
      color: TkLook.of(context).ground,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            AuthSide(
              line: _link?.card != null && taskiemChoice(_link!.card!)
                  ? 'Sign in to chat with ${_link!.card!.name}.'
                  : 'Chat with the businesses you buy from, and run your own.',
            ),
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
        if (_link?.card != null) ...[
          ChatLinkCardView(card: _link!.card!),
          const SizedBox(height: 20),
          // Their own WhatsApp number (§11): no signing in needed for it.
          // While they take no chats in Taskiem it is the way to them: the
          // main button, and signing in is not for chatting with them.
          if (whatsappChoice(_link!.card!) case final wa?) ...[
            AuthButton(label: 'Chat on WhatsApp', soft: taskiemChoice(_link!.card!), onTap: () => chatOnWhatsApp(context, wa)),
            const SizedBox(height: 20),
          ],
          if (!taskiemChoice(_link!.card!)) ...[
            Text(
              '${_link!.card!.name} isn’t taking chats in Taskiem right now.',
              textAlign: TextAlign.center,
              style: tkText(size: 14, color: t.muted),
            ),
            const SizedBox(height: 20),
          ],
        ],
        if (_link?.gone != null) ...[LinkGone(words: _link!.gone!), const SizedBox(height: 16)],
        const AuthTitle('Welcome to Taskiem', bottom: 8),
        Padding(
          padding: const EdgeInsets.only(bottom: 18),
          child: Text('Chat with the businesses you buy from, and run your own.', style: tkText(size: 14, color: t.muted)),
        ),
        if (_note != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Semantics(liveRegion: true, child: Text(_note!, style: tkText(size: 14, color: t.text))),
          ),
        TkField(
          key: const ValueKey('phone'),
          label: 'Your phone number',
          hint: '0803 123 4567',
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
        AuthButton(label: busy ? 'Sending…' : 'Continue', enabled: !busy, onTap: _send),
        Padding(
          // `.way-in-other`: 14 px above, nothing below.
          padding: const EdgeInsets.only(top: 14),
          child: Center(child: AuthLink(label: 'Sign in with email', onTap: widget.onEmail, size: 13.5)),
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
            sentWords(_sentVia, _e164),
            style: tkText(size: 14, color: t.text)),
        ),
      ),
      // As main's sign-in draws a code: six boxes.
      CodeBoxes(
        key: const ValueKey('code'),
        controller: _code,
        onChanged: (_) => setState(() {}),
        onSubmitted: (_) => _verify(),
      ),
      // The web's line under the code: the new-code timer, then the links.
      if (_left > 0)
        Padding(
          padding: const EdgeInsets.only(top: 8),
          child: Text('You can ask for a new code in ${countdown(_left)}.', style: tkHint(t)),
        ),
      if (error != null) ...[const SizedBox(height: 4), TkErrorBox(error!)] else const SizedBox(height: 16),
      AuthButton(label: busy ? 'Checking…' : 'Continue', enabled: !busy && _code.text.length == 6, onTap: _verify),
      const SizedBox(height: 6),
      if (_sentVia != 'sms')
        Center(child: AuthLink(label: 'Send by SMS instead', onTap: busy ? null : () => _send(sms: true))),
      if (_left <= 0) Center(child: AuthLink(label: 'Send a new code', onTap: busy ? null : () => _send(sms: _sentVia == 'sms'))),
      Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Center(child: AuthLink(label: 'Use another number', onTap: _back)),
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
      AuthButton(label: busy ? 'Creating…' : 'Create', enabled: !busy, onTap: _create),
      const SizedBox(height: 6),
      Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Center(child: AuthLink(label: 'Use another number', onTap: _back)),
      ),
    ];
  }
}
