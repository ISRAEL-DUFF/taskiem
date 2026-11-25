// A second proof for a new phone (docs/spec/taskiem-for-everyone.md §5.4):
// whichever ways the account has (a passkey, a link to its email, its
// password; never the PIN, which pays and approves only). Each proves this
// phone and leaves the session
// where it is (POST /v1/auth/second-proof/*); then [SecondProof.onProved]
// says what next: a business opens, an invitation's business, or Change
// number goes on. The web's ProofStep (web/src/pages/Switch.tsx).

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../app/phone.dart';
import '../chat/approve.dart' show TkTextBox;
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';

/// The ways this person can prove a new phone is theirs: never the PIN,
/// which pays and approves only (founder, 25 November 2026).
class ProofWays {
  const ProofWays({this.passkey = false, this.email = false, this.password = false});

  /// From a 401's `ways`, or /v1/me's `second_proof.ways`.
  factory ProofWays.of(Object? j) {
    final m = j is Map ? j : const {};
    bool on(String k) => m[k] == true;
    return ProofWays(passkey: on('passkey'), email: on('email'), password: on('password'));
  }

  /// What /v1/me says, as kept.
  factory ProofWays.ofMe(Me? me) => ProofWays.of(me?.secondProof?.ways?.toJson());

  final bool passkey, email, password;

  bool get none => !passkey && !email && !password;
}

/// What to do with no way left to prove a new phone: an owner cannot add
/// one for someone, so the old phone, where they are signed in, is where.
const noWayHere = 'Sign in on your old phone and add a way there: an email, a password or a passkey.';

/// Proves this phone with its passkey: the session stays as it is (C2).
Future<void> passkeyProof(Auth auth) async {
  final options = await auth.transport.send('POST', '/v1/auth/second-proof/passkey/options', body: const {});
  final assertion = await passkeys.get(((options as Map)['publicKey'] as Map).cast<String, dynamic>());
  await auth.transport.send('POST', '/v1/auth/second-proof/passkey', body: {'credential': assertion});
}

/// The ways, and what each asks, as the body of a sheet.
class SecondProof extends StatefulWidget {
  const SecondProof({super.key, required this.auth, required this.ways, required this.onProved, this.business});
  final Auth auth;
  final ProofWays ways;

  /// The business that opens after (its name in the words), if any.
  final String? business;

  /// Proved: what next. Its failure is said here.
  final Future<void> Function() onProved;

  @override
  State<SecondProof> createState() => _SecondProofState();
}

class _SecondProofState extends State<SecondProof> with WidgetsBindingObserver {
  final _password = TextEditingController();
  String? _sentTo;
  String? _error;
  bool _busy = false;
  bool _done = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _password.dispose();
    super.dispose();
  }

  /// Back in the app after opening the email's link: asked again, quietly.
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && _sentTo != null && !_busy && !_done) _opened(quiet: true);
  }

  Future<void> _run(Future<void> Function() f, {bool quiet = false}) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await f();
    } on PasskeyCancelled {
      // Closed the phone's passkey sheet.
    } on ApiError catch (e) {
      // Too many tries in the web's words; else the server's own sentence.
      if (!quiet) _error = e.body['tries_left'] is num || e.status == 429 ? codeProblem(e) : sentence(e.message);
    } on Offline {
      if (!quiet) _error = 'You’re offline. Try again when you’re back online.';
    } on Object {
      if (!quiet) _error = 'Something went wrong. Try again.';
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _proved() async {
    if (_done) return;
    _done = true;
    try {
      await widget.onProved();
    } on Object {
      _done = false;
      rethrow;
    }
  }

  void _passkey() => _run(() async {
        await passkeyProof(widget.auth);
        await _proved();
      });

  void _emailLink() => _run(() async {
        final r = await widget.auth.api.secondProofEmailStart();
        if (mounted) setState(() => _sentTo = r.sentTo);
      });

  /// Whether the link was opened: the server says (GET /v1/me).
  void _opened({bool quiet = false}) => _run(() async {
        await widget.auth.refreshMe();
        if (widget.auth.me?.secondProof?.deviceProved != true) {
          if (quiet) return;
          throw ApiError(400, {'error': 'The link has not been opened yet. Open it from the email, then try again.'});
        }
        await _proved();
      }, quiet: quiet);

  void _usePassword() {
    final password = _password.text;
    if (password.isEmpty) return;
    _run(() async {
      try {
        await widget.auth.transport.send('POST', '/v1/auth/second-proof/password', body: {'password': password});
      } on ApiError catch (e) {
        if (e.status == 401) throw ApiError(401, {'error': 'That password is not right.'});
        rethrow;
      }
      await _proved();
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final ways = widget.ways;
    final sent = _sentTo;
    final what = widget.business == null ? 'First' : 'Before ${widget.business} opens';
    final look = TkLook.of(context);
    // The app's own look: what to do as a callout, the ways in as rows of
    // a section, a password in a form group with its button.
    final List<Widget> body = sent != null
        ? [
            Semantics(
              liveRegion: true,
              child: TkCallout(
                leading: TkSquare('b-mail', color: look.teal),
                title: 'We sent a link to $sent',
                sub: 'Open it on this phone within 15 minutes, then come back here and say so.',
                subLines: 3,
                color: look.teal,
              ),
            ),
            TkCta(label: _busy ? 'Checking…' : 'I’ve opened the link', onTap: _busy ? null : _opened),
            Center(child: TkLink(bold: true, label: 'Send it again', onTap: _busy ? null : _emailLink)),
          ]
        : [
            Text(
              'This is a new device for your account. $what, prove it\'s you another way. This keeps your businesses safe if your number ever goes to someone else.',
              style: tkText(size: 14, lineHeight: 1.45, color: t.muted),
            ),
            if (ways.passkey || ways.email)
              TkSection(
                inset: 56,
                children: [
                  if (ways.passkey)
                    TkListRow(title: 'Use your passkey', sub: 'Your fingerprint, face or screen lock', icon: 'b-key', square: look.lime, onTap: _busy ? null : _passkey),
                  if (ways.email)
                    TkListRow(title: 'Email me a link', sub: 'To the email on your account', icon: 'b-mail', square: look.teal, onTap: _busy ? null : _emailLink),
                ],
              ),
            if (ways.password) ...[
              TkFormGroup(children: [TkKitField(label: 'Your password', child: TkTextBox(key: const ValueKey('proof-password'), controller: _password, secret: true))]),
              TkCta(label: _busy ? 'Checking…' : 'Confirm with my password', onTap: _busy ? null : _usePassword),
            ],
            if (ways.none)
              TkCallout(
                leading: TkSquare('b-alert', color: look.orange),
                title: 'No other way on this phone',
                sub: noWayHere,
                subLines: 4,
                color: look.orange,
              ),
          ];
    return TkFormBody(children: [...body, if (_error != null) TkErrorBox(_error!)]);
  }
}

/// The second proof as a sheet of its own (You, Change number, an
/// invitation): closed once [onProved] has run.
Future<void> showSecondProof(BuildContext context, Auth auth, {required ProofWays ways, required Future<void> Function() onProved, String? business}) =>
    showTkSheet<void>(
      context,
      (c) => TkAppLook(
        child: TkSheet(
        title: 'Prove it\'s you',
        body: SecondProof(
          auth: auth,
          ways: ways,
          business: business,
          onProved: () async {
            await onProved();
            if (c.mounted) Navigator.of(c).maybePop();
          },
        ),
      ),
      ),
    );
