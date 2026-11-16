// The switcher (docs/spec/taskiem-for-everyone.md §5.4, §6): **You**, then
// each of the person's businesses (a workflows workspace with its mark),
// a locked one with what it needs. Choosing one moves this phone's session
// there (POST /v1/me/switch), asking first for what the business's rules
// want: a passkey, or a second proof on a new phone (a passkey, a link to
// the person's email, or their chat PIN). Single sign-on is the web's.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../chat/approve.dart' show TkTextBox;
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';

/// What a lock asks, in a few words under the business's name.
String lockWords(Object? lock) => switch (lock) {
      'sso_required' => 'Opens with your organisation\'s sign-in',
      'passkey_required' => 'Opens with your passkey',
      'second_proof_required' => 'New device: prove it\'s you to open it',
      _ => '',
    };

/// The line under a business in the switcher.
String businessLine(BusinessEntry b) {
  if (b.current == true) return b.kind == 'workflows' ? 'Workflows · open now' : 'Open now';
  if (b.locked) return lockWords(b.lock);
  return b.kind == 'workflows' ? 'Workflows' : 'Business';
}

/// Opens the switcher over [context].
Future<void> showSwitcher(BuildContext context, Auth auth) => showTkSheet<void>(context, (_) => SwitcherSheet(auth: auth));

class SwitcherSheet extends StatefulWidget {
  const SwitcherSheet({super.key, required this.auth, this.businesses});
  final Auth auth;

  /// The businesses to show (a test's); else read from the server.
  final List<BusinessEntry>? businesses;

  @override
  State<SwitcherSheet> createState() => _SwitcherSheetState();
}

class _SwitcherSheetState extends State<SwitcherSheet> {
  late List<BusinessEntry>? _list = widget.businesses ?? widget.auth.me?.businesses;
  bool _busy = false;
  String? _error;

  /// The business asking for a step first, and the step.
  BusinessEntry? _to;
  String? _need;
  MeSecondProofWays? _ways;
  String? _sentTo;
  final _pin = TextEditingController();

  @override
  void initState() {
    super.initState();
    if (widget.businesses == null) _load();
  }

  @override
  void dispose() {
    _pin.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listBusinesses();
      if (mounted) setState(() => _list = r.businesses);
    } on Object {
      // What /v1/me said stands.
    }
  }

  /// Runs [f], its failure said; a business's rules asking for a step
  /// open that step.
  Future<void> _run(Future<void> Function() f) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await f();
    } on PasskeyCancelled {
      // Closed the phone's passkey sheet.
    } on ApiError catch (e) {
      if (!mounted) return;
      if (e.has('second_proof_required')) {
        final w = e.body['ways'];
        _need = 'second_proof_required';
        _ways = w is Map ? MeSecondProofWays.fromJson(w.cast<String, dynamic>()) : widget.auth.me?.secondProof?.ways;
      } else if (e.has('passkey_required')) {
        _need = 'passkey_required';
      } else if (e.has('sso_required')) {
        _need = 'sso_required';
      } else {
        _error = e.message;
      }
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    } on Object {
      _error = 'Something went wrong. Try again.';
    }
    if (mounted) setState(() => _busy = false);
  }

  void _pick(BusinessEntry? b) {
    if (_busy) return;
    final auth = widget.auth;
    if (b == null) {
      if (auth.personal) return Navigator.of(context).pop();
      _run(auth.switchToYou);
      return;
    }
    if (b.current == true) return Navigator.of(context).pop();
    setState(() {
      _to = b;
      _need = null;
    });
    _run(() => auth.switchTo(b.id));
  }

  /// The passkey on this phone: signing in with it proves the phone and
  /// meets a passkey rule; then the business opens.
  void _passkey() {
    final auth = widget.auth, to = _to!;
    _run(() async {
      await signInWithPasskey(auth);
      await auth.switchTo(to.id);
    });
  }

  void _emailLink() {
    _run(() async {
      final r = await widget.auth.api.secondProofEmailStart();
      if (mounted) setState(() => _sentTo = r.sentTo);
    });
  }

  /// Back from the email: the server asked whether the link was opened.
  void _opened() {
    final auth = widget.auth, to = _to!;
    _run(() async {
      await auth.refreshMe();
      if (auth.me?.secondProof?.deviceProved != true) {
        throw ApiError(400, {'error': 'The link has not been opened yet. Open it from the email, then try again.'});
      }
      await auth.switchTo(to.id);
    });
  }

  void _usePin() {
    final pin = _pin.text.trim();
    if (pin.isEmpty) return;
    final auth = widget.auth, to = _to!;
    _run(() async {
      await auth.api.secondProofPin(body: SecondProofPinRequest(pin: pin));
      await auth.switchTo(to.id);
    });
  }

  void _back() => setState(() {
        _to = null;
        _need = null;
        _sentTo = null;
        _error = null;
        _pin.clear();
      });

  @override
  Widget build(BuildContext context) {
    final need = _need;
    if (need != null && _to != null) return _step(context, _to!, need);
    final t = TkTheme.tokens(context);
    final list = _list ?? const <BusinessEntry>[];
    final you = widget.auth.personal;
    return TkSheet(
      title: 'Switch to',
      body: TkFormBody(
        children: [
          TkPanel(
            children: [
              TkListRow(
                title: 'You',
                icon: 'home',
                sub: you ? 'Open now' : 'Your own chats and orders',
                onTap: () => _pick(null),
              ),
              for (final b in list)
                TkListRow(
                  title: b.name,
                  sub: businessLine(b),
                  // A workflows workspace shows the workflows mark.
                  icon: b.kind == 'workflows' ? 'workflows' : (b.locked ? 'lock' : null),
                  letters: b.name,
                  semanticsLabel: '${b.name}, ${b.kind == 'workflows' ? 'workflows' : 'business'}${b.locked ? ', ${lockWords(b.lock)}' : ''}',
                  onTap: () => _pick(b),
                ),
            ],
          ),
          if (_busy) Text('Switching…', style: tkFormNow(t)),
          if (_error != null) TkErrorBox(_error!),
        ],
      ),
    );
  }

  /// What the business asks before it opens.
  Widget _step(BuildContext context, BusinessEntry b, String need) {
    final t = TkTheme.tokens(context);
    final ways = _ways;
    final sent = _sentTo;
    final List<Widget> body;
    if (need == 'sso_required') {
      body = [
        Text('${b.name} asks you to sign in with your organisation\'s sign-in.', style: tkText(size: 16, weight: 600, color: t.text)),
        Text('Sign out, then sign in to ${b.name} with your organisation\'s email.', style: tkFormNow(t)),
      ];
    } else if (need == 'passkey_required') {
      body = [
        Text('${b.name} opens with your passkey: your phone\'s fingerprint, face or PIN.', style: tkText(size: 16, weight: 600, color: t.text)),
        TkButton(label: 'Use your passkey', primary: true, minHeight: 44, enabled: !_busy, onPressed: _passkey),
      ];
    } else if (sent != null) {
      body = [
        Text('We sent a link to $sent. Open it on this phone within 15 minutes.', style: tkText(size: 16, weight: 600, color: t.text)),
        Text('Opened it? Come back here and say so.', style: tkFormNow(t)),
        TkButton(label: _busy ? 'Checking…' : 'I’ve opened the link', primary: true, minHeight: 44, enabled: !_busy, onPressed: _opened),
        TkLinkButton(label: 'Send it again', onPressed: _busy ? null : _emailLink),
      ];
    } else {
      body = [
        Text(
          'This is a new device for your account. Before ${b.name} opens, prove it\'s you another way. This keeps your businesses safe if your number ever goes to someone else.',
          style: tkFormNow(t),
        ),
        if (ways?.passkey == true) TkButton(label: 'Use your passkey', primary: true, minHeight: 44, enabled: !_busy, onPressed: _passkey),
        if (ways?.email == true) TkButton(label: 'Email me a link', minHeight: 44, enabled: !_busy, onPressed: _emailLink),
        if (ways?.pin == true) ...[
          TkKitField(label: 'Your chat PIN', hint: 'The PIN you set for approving on WhatsApp.', child: TkTextBox(controller: _pin, digits: 8, secret: true)),
          TkButton(label: _busy ? 'Checking…' : 'Confirm with my PIN', minHeight: 44, enabled: !_busy, onPressed: _usePin),
        ],
        if (ways?.passkey != true && ways?.email != true && ways?.pin != true)
          Text('Your account has no other way to prove it\'s you yet. Add an email or a passkey from a device you have used before.', style: tkFormNow(t)),
      ];
    }
    return TkSheet(
      title: need == 'second_proof_required' ? 'Prove it\'s you' : 'Open ${b.name}',
      onBack: _busy ? null : _back,
      body: TkFormBody(children: [...body, if (_error != null) TkErrorBox(_error!)]),
    );
  }
}
