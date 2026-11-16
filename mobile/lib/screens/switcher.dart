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
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'second_proof.dart';

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

/// Opens the switcher over [context]; [open] switches to that business at
/// once, asking for its step if it has one.
Future<void> showSwitcher(BuildContext context, Auth auth, {BusinessEntry? open}) => showTkSheet<void>(context, (_) => SwitcherSheet(auth: auth, open: open));

/// A business's step asked for outside the switcher (an invitation
/// accepted): the step's sheet, then the business.
Future<void> showBusinessStep(BuildContext context, Auth auth, {required BusinessEntry to, required String need, ProofWays? ways}) =>
    showTkSheet<void>(context, (_) => SwitcherSheet(auth: auth, to: to, need: need, ways: ways));

class SwitcherSheet extends StatefulWidget {
  const SwitcherSheet({super.key, required this.auth, this.businesses, this.to, this.need, this.ways, this.open});
  final Auth auth;

  /// The businesses to show (a test's); else read from the server.
  final List<BusinessEntry>? businesses;

  /// Opened on a business's step at once (an invitation accepted whose
  /// business asks for one first): the business, the step and its ways.
  final BusinessEntry? to;
  final String? need;
  final ProofWays? ways;

  /// A business to switch to at once (a row of **Your businesses** on You).
  final BusinessEntry? open;

  @override
  State<SwitcherSheet> createState() => _SwitcherSheetState();
}

class _SwitcherSheetState extends State<SwitcherSheet> {
  late List<BusinessEntry>? _list = widget.businesses ?? widget.auth.me?.businesses;
  bool _busy = false;
  String? _error;

  /// The business asking for a step first, and the step.
  late BusinessEntry? _to = widget.to;
  late String? _need = widget.need;
  late ProofWays? _ways = widget.ways;

  @override
  void initState() {
    super.initState();
    if (widget.businesses == null) _load();
    final open = widget.open;
    if (open != null) WidgetsBinding.instance.addPostFrameCallback((_) => mounted ? _pick(open) : null);
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
        _ways = w is Map ? ProofWays.of(w) : ProofWays.ofMe(widget.auth.me);
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

  /// A business that opens only with a passkey: signing in with the one
  /// on this phone meets its rule; then the business opens.
  void _passkey() {
    final auth = widget.auth, to = _to!;
    _run(() async {
      await signInWithPasskey(auth);
      await auth.switchTo(to.id);
    });
  }

  void _back() {
    // Opened on the step (an invitation): back closes it.
    if (widget.to != null) {
      Navigator.of(context).maybePop();
      return;
    }
    setState(() {
      _to = null;
      _need = null;
      _error = null;
    });
  }

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
    if (need == 'second_proof_required') {
      final auth = widget.auth;
      return TkSheet(
        title: 'Prove it\'s you',
        onBack: _busy ? null : _back,
        body: SecondProof(
          auth: auth,
          ways: _ways ?? ProofWays.ofMe(auth.me),
          business: b.name,
          // Proved, and the session where it was: now the business opens.
          onProved: () => auth.switchTo(b.id),
        ),
      );
    }
    final List<Widget> body;
    if (need == 'sso_required') {
      body = [
        Text('${b.name} asks you to sign in with your organisation\'s sign-in.', style: tkText(size: 16, weight: 600, color: t.text)),
        Text('Sign out, then sign in to ${b.name} with your organisation\'s email.', style: tkFormNow(t)),
      ];
    } else {
      body = [
        Text('${b.name} opens with your passkey: your phone\'s fingerprint, face or PIN.', style: tkText(size: 16, weight: 600, color: t.text)),
        TkButton(label: 'Use your passkey', primary: true, minHeight: 44, enabled: !_busy, onPressed: _passkey),
      ];
    }
    return TkSheet(
      title: 'Open ${b.name}',
      onBack: _busy ? null : _back,
      body: TkFormBody(children: [...body, if (_error != null) TkErrorBox(_error!)]),
    );
  }
}
