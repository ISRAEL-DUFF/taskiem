// You: a person's own side (docs/spec/taskiem-for-everyone.md §6, §7). A
// personal session opens here: the chats with businesses (none yet in this
// stage), **Start something**, invitations waiting, the switcher when the
// person is in businesses, and Settings: name, phone (Change number, §5.5)
// and signing out.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/applinks.dart';
import '../app/auth.dart';
import '../chat/approve.dart' show TkTextBox;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'start_something.dart';
import 'switcher.dart';

/// The words for an error, as the sign-in screens say them.
String _say(Object e) => switch (e) {
      ApiError() => e.message,
      Offline() => 'You’re offline. Try again when you’re back online.',
      _ => 'Something went wrong. Try again.',
    };

class YouHome extends StatefulWidget {
  const YouHome({super.key, required this.auth, this.links});
  final Auth auth;

  /// Links that open the app: a second proof's link opened on this phone.
  final LinkInbox? links;

  @override
  State<YouHome> createState() => _YouHomeState();
}

class _YouHomeState extends State<YouHome> {
  List<Invitation> _invitations = const [];
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
    widget.links?.addListener(_linked);
    WidgetsBinding.instance.addPostFrameCallback((_) => _linked());
  }

  @override
  void dispose() {
    widget.links?.removeListener(_linked);
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.myInvitations();
      if (mounted) setState(() => _invitations = r.invitations);
    } on Object {
      // None shown; read again next time.
    }
  }

  /// The email's link to confirm this phone (§5.4), opened in the app.
  Future<void> _linked() async {
    final page = widget.links?.waiting == true ? widget.links!.take() : null;
    if (page == null || !mounted) return;
    final token = Uri.parse(page).queryParameters['token'];
    if (!page.startsWith('/second-proof') || token == null) return;
    try {
      await widget.auth.api.secondProofEmailVerify(body: SecondProofEmailVerifyRequest(token: token));
      await widget.auth.refreshMe();
      if (mounted) showTkToast(context, 'Done: this device is yours now. Your businesses are open to you here.');
    } on Object catch (e) {
      if (mounted) showTkToast(context, _say(e), error: true);
    }
  }

  Future<void> _join(Invitation inv) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final r = await widget.auth.api.acceptInvitation(tenant: inv.tenantId);
      // Moved into the business: the app opens on it.
      if (r.token != null) return await widget.auth.signedInWith(r.token);
      await widget.auth.refreshMe();
      await _load();
      if (mounted) {
        showTkToast(context, 'You joined ${inv.tenant}. Open it from Switch.');
      }
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _decline(Invitation inv) async {
    setState(() => _busy = true);
    try {
      await widget.auth.api.declineInvitation(tenant: inv.tenantId);
      await _load();
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final auth = widget.auth;
    final t = TkTheme.tokens(context);
    final businesses = auth.me?.businesses ?? const <BusinessEntry>[];
    return ListenableBuilder(
      listenable: auth,
      builder: (context, _) => TkScreen(
        title: 'Chats',
        back: false,
        head: Row(
          children: [
            Text('Chats', style: tkDisplay(size: 30, letterSpacing: -1.1, color: t.text)),
            const SizedBox(width: 8),
            Text('You', style: tkText(size: 13, color: t.muted)),
          ],
        ),
        onRefresh: () async {
          await auth.refreshMe();
          await _load();
        },
        actions: [
          if (businesses.isNotEmpty) TkHeadButton(icon: 'loop', label: 'Switch', onTap: () => showSwitcher(context, auth)),
          const SizedBox(width: 6),
          TkHeadButton(icon: 'settings', label: 'Settings', onTap: () => tkPush<void>(context, YouSettingsScreen(auth: auth))),
        ],
        children: [
          if (_invitations.isNotEmpty)
            TkSection(
              title: 'Invitations',
              count: _invitations.length,
              children: [
                TkPanel(
                  inset: 16,
                  children: [for (final inv in _invitations) _InvitationRow(inv: inv, busy: _busy, onJoin: () => _join(inv), onDecline: () => _decline(inv))],
                ),
              ],
            ),
          if (_error != null) TkErrorBox(_error!),
          TkPanel(
            children: [
              TkEmpty(
                icon: 'chat',
                title: 'No chats yet',
                line: 'Chats with businesses will show here.',
              ),
            ],
          ),
          TkButton(
            label: 'Start something',
            primary: true,
            minHeight: 44,
            onPressed: () => tkPush<void>(context, StartSomethingScreen(auth: auth)),
          ),
          Text('A business, or workflows to automate your work.', textAlign: TextAlign.center, style: tkHint(t)),
        ],
      ),
    );
  }
}

class _InvitationRow extends StatelessWidget {
  const _InvitationRow({required this.inv, required this.busy, required this.onJoin, required this.onDecline});
  final Invitation inv;
  final bool busy;
  final VoidCallback onJoin;
  final VoidCallback onDecline;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final roles = inv.roles.isEmpty ? '' : ' as ${inv.roles.join(', ')}';
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(inv.tenant, style: tkText(size: 15, weight: 600, color: t.text)),
          Text('Invited you$roles', style: tkHint(t)),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(child: TkButton(label: 'Join', primary: true, enabled: !busy, onPressed: onJoin)),
              const SizedBox(width: 8),
              Expanded(child: TkButton(label: 'Decline', enabled: !busy, onPressed: onDecline)),
            ],
          ),
        ],
      ),
    );
  }
}

/// Settings on **You** (§7): who the person is, their phone with Change
/// number, and signing out.
class YouSettingsScreen extends StatelessWidget {
  const YouSettingsScreen({super.key, required this.auth});
  final Auth auth;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ListenableBuilder(
      listenable: auth,
      builder: (context, _) {
        final user = auth.me?.user;
        final phone = user?.phone as String?;
        final email = user?.emailPlaceholder == true ? null : user?.email;
        Widget line(String label, String value) => Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [Text(label, style: tkHint(t)), Text(value, style: tkText(size: 15, weight: 500, color: t.text))],
              ),
            );
        return TkScreen(
          title: 'Settings',
          children: [
            TkPanel(
              inset: 16,
              children: [
                line('Name', user?.name ?? ''),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 12, 12, 12),
                  child: Row(
                    children: [
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [Text('Phone', style: tkHint(t)), Text(phone ?? 'No phone yet: add one to sign in with a code.', style: tkText(size: 15, weight: 500, color: t.text))],
                        ),
                      ),
                      TkButton(
                        label: phone == null ? 'Add a phone' : 'Change number',
                        onPressed: () => tkPush<void>(context, ChangeNumberScreen(auth: auth)),
                      ),
                    ],
                  ),
                ),
                if (email != null) line('Email', email),
              ],
            ),
            TkButton(label: 'Sign out of this phone', minHeight: 44, onPressed: auth.signOut),
          ],
        );
      },
    );
  }
}

/// Settings > Phone > Change number (§5.5): the new number and its code,
/// and a code to the old number where the server asks for one.
class ChangeNumberScreen extends StatefulWidget {
  const ChangeNumberScreen({super.key, required this.auth});
  final Auth auth;

  @override
  State<ChangeNumberScreen> createState() => _ChangeNumberScreenState();
}

class _ChangeNumberScreenState extends State<ChangeNumberScreen> {
  final _phone = TextEditingController();
  final _code = TextEditingController();
  final _old = TextEditingController();
  PhoneChangeStartResponse? _sent;
  bool _needsOld = false;
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _phone.dispose();
    _code.dispose();
    _old.dispose();
    super.dispose();
  }

  /// Whether this session will need the old number's code: made by a code
  /// to the phone or chat on a phone not yet confirmed (the server says).
  bool get _oldLikely {
    final me = widget.auth.me;
    return (me?.authMethod == 'phone' || me?.authMethod == 'chat') && me?.secondProof?.deviceProved != true && me?.user?.phone != null;
  }

  Future<void> _run(Future<void> Function() f) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await f();
    } on ApiError catch (e) {
      if (e.has('old_code_required')) _needsOld = true;
      _error = e.message;
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  void _send({bool confirmOld = false}) {
    final phone = _phone.text.trim();
    if (phone.isEmpty) return setState(() => _error = 'Enter the new number, with its country code if it is not Nigerian.');
    _run(() async {
      final r = await widget.auth.api.phoneChangeStart(body: PhoneChangeStartRequest(phone: phone, confirmOld: confirmOld || _oldLikely ? true : null));
      _sent = r;
      _needsOld = r.needsOldCode;
    });
  }

  void _verify() {
    final phone = _phone.text.trim();
    _run(() async {
      final r = await widget.auth.api.phoneChangeVerify(
        body: PhoneChangeVerifyRequest(code: _code.text.trim(), oldCode: _needsOld ? _old.text.trim() : null, phone: phone),
      );
      await widget.auth.refreshMe();
      if (!mounted) return;
      showTkToast(context, 'Your number is now ${r.phone}');
      unawaited(Navigator.of(context).maybePop());
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final sent = _sent;
    return TkScreen(
      title: 'Change your number',
      children: [
        TkPanel(
          padding: const EdgeInsets.all(16),
          children: [
            Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Your account keeps everything: your chats, orders and businesses.', style: tkHint(t)),
                const SizedBox(height: 14),
                if (sent == null) ...[
                  TkKitField(label: 'New phone number', child: TkTextBox(key: const ValueKey('new-phone'), controller: _phone, phone: true)),
                  const SizedBox(height: 14),
                  TkButton(label: _busy ? 'Sending…' : 'Send a code', primary: true, minHeight: 44, enabled: !_busy, onPressed: _send),
                ] else ...[
                  TkKitField(
                    label: 'Code sent to your new number',
                    child: TkTextBox(key: const ValueKey('new-code'), controller: _code, digits: 6),
                  ),
                  if (_needsOld) ...[
                    const SizedBox(height: 14),
                    TkKitField(
                      label: 'Code sent to your old number',
                      hint: sent.oldSentVia == null ? 'Still have your old number? We’ll send it a code too.' : null,
                      child: TkTextBox(key: const ValueKey('old-code'), controller: _old, digits: 6),
                    ),
                    if (sent.oldSentVia == null)
                      TkLinkButton(label: 'Send a code to my old number', onPressed: _busy ? null : () => _send(confirmOld: true)),
                  ],
                  const SizedBox(height: 14),
                  TkButton(label: _busy ? 'Checking…' : 'Confirm the new number', primary: true, minHeight: 44, enabled: !_busy, onPressed: _verify),
                  TkLinkButton(
                    label: 'Use another number',
                    onPressed: () => setState(() {
                      _sent = null;
                      _code.clear();
                      _old.clear();
                      _error = null;
                    }),
                  ),
                ],
              ],
            ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}
