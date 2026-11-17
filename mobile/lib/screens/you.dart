// You: a person's own side (docs/spec/taskiem-for-everyone.md §6, §7). A
// personal session opens here: the chats with businesses (§8.3, with the
// archived ones apart), orders made on businesses' shops with this phone
// (§13: "These are mine", and "Chat with {business} about this order"),
// **Start something**, invitations waiting, the switcher when the person
// is in businesses, and Settings: name, phone (Change number, §5.5) and
// signing out. A business's chat link opens its card here (§8.2).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/applinks.dart';
import '../app/auth.dart';
import '../app/phone.dart';
import '../chat/approve.dart' show TkTextBox;
import '../chat/door.dart' show yourOrderStatus;
import '../chat/my_chat.dart';
import '../chat/words.dart' show naira, listTime;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkConfirm, showTkToast;
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'chat_link.dart';
import 'second_proof.dart';
import 'start_something.dart';
import 'switcher.dart';

/// The words for an error, as the sign-in screens say them.
String _say(Object e) => switch (e) {
      ApiError() => e.message,
      Offline() => 'You’re offline. Try again when you’re back online.',
      _ => 'Something went wrong. Try again.',
    };

class YouHome extends StatefulWidget {
  const YouHome({super.key, required this.auth, this.links, this.now = DateTime.now});
  final Auth auth;

  /// Links that open the app: a second proof's link opened on this phone,
  /// a business's chat link.
  final LinkInbox? links;
  final DateTime Function() now;

  @override
  State<YouHome> createState() => _YouHomeState();
}

class _YouHomeState extends State<YouHome> {
  List<Invitation> _invitations = const [];
  String? _error;
  bool _busy = false;
  late final MyChats _chats = MyChats(widget.auth);
  MyStorefrontOrdersResponse? _orders;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _load();
    widget.links?.addListener(_linked);
    WidgetsBinding.instance.addPostFrameCallback((_) => _linked());
    // New messages show in the list while it is open.
    _poll = Timer.periodic(const Duration(seconds: 10), (_) {
      if (mounted && ModalRoute.of(context)?.isCurrent != false) _chats.load();
    });
  }

  @override
  void dispose() {
    _poll?.cancel();
    widget.links?.removeListener(_linked);
    _chats.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    await Future.wait([
      _chats.load(),
      () async {
        try {
          final r = await widget.auth.api.myInvitations();
          if (mounted) setState(() => _invitations = r.invitations);
        } on Object {
          // None shown; read again next time.
        }
      }(),
      _loadOrders(),
    ]);
  }

  Future<void> _loadOrders() async {
    try {
      final r = await widget.auth.api.myStorefrontOrders(limit: '20');
      if (mounted) setState(() => _orders = r);
    } on Object {
      // None shown; read again next time (no phone yet: none to find).
    }
  }

  /// A chat opened, and the list read again when it is closed.
  Future<void> _open(PersonChat c, {String? notice, String draft = ''}) async {
    await tkPush<void>(context, MyChatScreen(data: MyChatData(widget.auth, c.id, notice: notice, now: widget.now), draft: draft));
    await _chats.load();
  }

  /// "Chat with {business} about this order" (§8.1): the chat with the
  /// business of a shop order, started if there is none, the order named
  /// in the box.
  Future<void> _chatAbout(PhoneOrder o) async {
    try {
      final c = await widget.auth.api.startMyChat(body: StartMyChatRequest(businessId: o.businessId, from: 'order'));
      if (!mounted) return;
      await _open(c, notice: c.notice, draft: 'About my order ${o.reference}: ');
    } on Object catch (e) {
      if (mounted) showTkToast(context, _say(e), error: true);
    }
  }

  /// "These are mine": orders made with this phone before it was on the
  /// account (§13).
  Future<void> _theseAreMine() async {
    try {
      await widget.auth.api.confirmStorefrontOrders();
      await _loadOrders();
      if (mounted) showTkToast(context, 'Done: these orders are on your account now.');
    } on Object catch (e) {
      if (mounted) showTkToast(context, _say(e), error: true);
    }
  }

  Future<void> _order(PhoneOrder o) async {
    final yes = await showTkConfirm(
      context,
      title: 'Order ${o.reference}',
      body: '${o.business} · ${yourOrderStatus(o.state)} · ${naira(o.totalKobo)}',
      ok: 'Chat with ${o.business} about this order',
      cancel: 'Close',
    );
    if (yes) await _chatAbout(o);
  }

  /// The email's link to confirm this phone (§5.4), or a business's chat
  /// link (§8.2), opened in the app.
  Future<void> _linked() async {
    final page = widget.links?.waiting == true ? widget.links!.take() : null;
    if (page == null || !mounted) return;
    final code = chatCodeOf(page);
    if (code != null) {
      // Seen on the way in: the chat starts at once.
      final auto = widget.links!.shownSignedOut;
      widget.links!.shownSignedOut = false;
      await tkPush<void>(
        context,
        ChatLinkScreen(
          auth: widget.auth,
          code: code,
          autoStart: auto,
          onStarted: (c) {
            Navigator.of(context).pop();
            _open(c, notice: c.notice);
          },
        ),
      );
      await _chats.load();
      return;
    }
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
      final j = await widget.auth.transport.send('POST', '/v1/me/invitations/${Uri.encodeComponent(inv.tenantId)}/accept');
      // Read as it came: the ways may name only those the person has.
      final r = (j as Map).cast<String, dynamic>();
      final token = r['token'] as String?;
      // Moved into the business: the app opens on it.
      if (token != null) return await widget.auth.signedInWith(token);
      await widget.auth.refreshMe();
      await _load();
      final need = r['sign_in'] as String?;
      if (!mounted) return;
      if (need != null && need != 'session_ended') {
        // A member now, but the business asks for a step first (its
        // sign-in rules, or a second proof on a new phone): that step,
        // then the business opens. The session stays on You till then.
        setState(() => _busy = false);
        await showBusinessStep(
          context,
          widget.auth,
          to: BusinessEntry(id: inv.tenantId, name: inv.tenant, locked: true, lock: need),
          need: need,
          ways: ProofWays.of(r['ways']),
        );
        return;
      }
      showTkToast(context, 'You joined ${inv.tenant}. Open it from Switch.');
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  /// A new phone's businesses stay locked until it is proved (§5.4).
  Future<void> _prove() => showSecondProof(
        context,
        widget.auth,
        ways: ProofWays.ofMe(widget.auth.me),
        onProved: () async {
          await widget.auth.refreshMe();
          if (mounted) showTkToast(context, 'Done: this device is yours now. Your businesses are open to you here.');
        },
      );

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

  /// Orders made on businesses' shops with this phone (§13): those made
  /// before the phone was on the account wait for "These are mine".
  Widget? _ordersSection(TkTokens t) {
    final r = _orders;
    if (r == null || (r.orders.isEmpty && r.waitingCount == 0)) return null;
    // Orders made before the phone was on the account: only a count until
    // the person says they are theirs (§13), so a reissued number never
    // sees the earlier holder's orders.
    final waiting = r.waitingCount;
    final mine = r.orders;
    return TkSection(
      title: 'Your shop orders',
      count: r.orders.length,
      children: [
        if (waiting > 0)
          TkPanel(
            padding: const EdgeInsets.all(16),
            children: [
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    waiting == 1
                        ? 'We found an order made with your number before it was on your account. Is it yours?'
                        : 'We found $waiting orders made with your number before it was on your account. Are they yours?',
                    style: tkText(size: 14, color: t.text),
                  ),
                  const SizedBox(height: 10),
                  TkButton(label: 'These are mine', primary: true, onPressed: _theseAreMine),
                ],
              ),
            ],
          ),
        if (mine.isNotEmpty)
          TkPanel(
            children: [
              for (final o in mine)
                TkListRow(
                  title: o.business,
                  sub: '${o.reference} · ${yourOrderStatus(o.state)} · ${listTime(o.madeAt, widget.now())}',
                  letters: o.business,
                  amount: naira(o.totalKobo),
                  semanticsLabel: 'Order ${o.reference} with ${o.business}, ${yourOrderStatus(o.state)}, ${naira(o.totalKobo)}',
                  onTap: () => _order(o),
                ),
            ],
          ),
      ],
    );
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
          if (auth.me?.secondProof?.required == true)
            TkPanel(
              padding: const EdgeInsets.all(16),
              children: [
                Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Semantics(
                      liveRegion: true,
                      child: Text(
                        'You signed in on a new device, so your businesses are locked until you prove it\'s you.',
                        style: tkText(size: 14, color: t.text),
                      ),
                    ),
                    const SizedBox(height: 10),
                    TkButton(label: 'Prove it\'s you', onPressed: _prove),
                  ],
                ),
              ],
            ),
          ListenableBuilder(
            listenable: _chats,
            builder: (context, _) {
              final chats = _chats.chats;
              final archived = _chats.archived;
              return Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (_chats.error != null && chats == null) TkErrorBox(_chats.error!),
                  if (chats != null && chats.isEmpty && archived.isEmpty)
                    const TkPanel(
                      children: [
                        TkEmpty(
                          icon: 'chat',
                          title: 'No chats yet',
                          line: 'Chats with businesses will show here.',
                        ),
                      ],
                    ),
                  if (chats != null && (chats.isNotEmpty || archived.isNotEmpty))
                    TkPanel(
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      inset: 0,
                      children: [
                        if (archived.isNotEmpty)
                          TkListRow(
                            title: 'Archived',
                            icon: 'archive',
                            amount: '${archived.length}',
                            semanticsLabel: 'Archived chats, ${archived.length}',
                            onTap: () => tkPush<void>(context, ArchivedChatsScreen(data: _chats, now: widget.now, onOpen: (c) => _open(c))),
                          ),
                        MyChatRows(chats: chats, now: widget.now(), onOpen: (c) => _open(c)),
                      ],
                    ),
                ],
              );
            },
          ),
          ?_ordersSection(t),
          TkButton(
            label: 'Start something',
            primary: true,
            minHeight: 44,
            onPressed: () => tkPush<void>(context, StartSomethingScreen(auth: auth)),
          ),
          Text('A business, or workflows to automate your work.', textAlign: TextAlign.center, style: tkHint(t)),
          // The web's Your businesses: the switcher's rows, on You.
          if (businesses.isNotEmpty)
            TkSection(
              title: 'Your businesses',
              children: [
                TkPanel(
                  children: [
                    for (final b in businesses)
                      TkListRow(
                        title: b.name,
                        sub: businessLine(b),
                        icon: b.kind == 'workflows' ? 'workflows' : (b.locked ? 'lock' : null),
                        letters: b.name,
                        semanticsLabel: '${b.name}, ${b.kind == 'workflows' ? 'workflows' : 'business'}${b.locked ? ', ${lockWords(b.lock)}' : ''}',
                        onTap: () => showSwitcher(context, auth, open: b),
                      ),
                  ],
                ),
              ],
            ),
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
                          children: [Text('Phone', style: tkHint(t)), Text(phone == null ? 'No phone yet: add one to sign in with a code.' : showPhone(phone), style: tkText(size: 15, weight: 500, color: t.text))],
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
            TkButton(label: 'Sign out', minHeight: 44, onPressed: auth.signOut),
          ],
        );
      },
    );
  }
}

/// Settings > Phone > Change number (§5.5), as the web's (ChangeNumber.tsx):
/// the new number and a code to it; where the server says this phone must
/// also prove it is the person's, it asks first whether they still have the
/// old number: **Yes** sends it a code too, **No** proves it another way.
class ChangeNumberScreen extends StatefulWidget {
  const ChangeNumberScreen({super.key, required this.auth});
  final Auth auth;

  @override
  State<ChangeNumberScreen> createState() => _ChangeNumberScreenState();
}

enum _Step { number, old, code }

class _ChangeNumberScreenState extends State<ChangeNumberScreen> {
  final _phone = TextEditingController();
  final _code = TextEditingController();
  final _old = TextEditingController();
  _Step _step = _Step.number;
  String _e164 = '';
  PhoneChangeStartResponse? _sent;
  bool _needsOld = false;
  bool _busy = false;
  bool _done = false;
  String? _error;
  String? _note;
  int _left = 0;
  Timer? _tick;

  @override
  void dispose() {
    _tick?.cancel();
    _phone.dispose();
    _code.dispose();
    _old.dispose();
    super.dispose();
  }

  void _wait() {
    _tick?.cancel();
    _left = resendAfter;
    _tick = Timer.periodic(const Duration(seconds: 1), (t) {
      if (!mounted) return t.cancel();
      setState(() => _left--);
      if (_left <= 0) t.cancel();
    });
  }

  Future<void> _run(Future<void> Function() f) async {
    if (_busy || _done) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await f();
    } on ApiError catch (e) {
      _error = codeProblem(e);
    } on Object catch (e) {
      _error = _say(e);
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<PhoneChangeStartResponse> _start(String phone, {bool confirmOld = false, bool sms = false}) async {
    final r = await widget.auth.api.phoneChangeStart(body: PhoneChangeStartRequest(phone: phone, confirmOld: confirmOld ? true : null, via: sms ? 'sms' : null));
    _sent = r;
    _code.clear();
    _old.clear();
    _wait();
    return r;
  }

  void _send() {
    final phone = toE164(_phone.text);
    if (phone == null) return setState(() => _error = notAPhone);
    _note = null;
    _run(() async {
      final r = await _start(phone);
      _e164 = phone;
      _needsOld = r.needsOldCode;
      _step = r.needsOldCode ? _Step.old : _Step.code;
    });
  }

  /// Yes, the old number: a code to it too.
  void _yes() => _run(() async {
        await _start(_e164, confirmOld: true);
        _step = _Step.code;
      });

  /// No: another proof (a passkey, the email's link, the password, the
  /// chat PIN); then only the new number's code.
  Future<void> _no() => showSecondProof(
        context,
        widget.auth,
        ways: ProofWays.ofMe(widget.auth.me),
        onProved: () async {
          await widget.auth.refreshMe();
          if (mounted) {
            setState(() {
              _needsOld = false;
              _step = _Step.code;
            });
          }
        },
      );

  void _verify() {
    if (_code.text.length != 6 || (_needsOld && _old.text.length != 6)) return;
    _run(() async {
      try {
        final r = await widget.auth.api.phoneChangeVerify(
          body: PhoneChangeVerifyRequest(code: _code.text, oldCode: _needsOld ? _old.text : null, phone: _e164),
        );
        _done = true;
        await widget.auth.refreshMe();
        if (!mounted) return;
        showTkToast(context, 'Your number is now ${showPhone(r.phone)}');
        unawaited(Navigator.of(context).maybePop());
      } on ApiError catch (e) {
        // Run out: a new code, from the number.
        if (!e.has('ticket_expired')) rethrow;
        _note = codeExpired;
        _step = _Step.number;
        _sent = null;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final sent = _sent;
    final old = widget.auth.me?.user?.phone as String?;
    final List<Widget> body = switch (_step) {
      _Step.number => [
          if (_note != null) ...[Semantics(liveRegion: true, child: Text(_note!, style: tkText(size: 14, color: t.text))), const SizedBox(height: 10)],
          TkKitField(
            label: 'New phone number',
            hint: 'A Nigerian number can start with 0. For another country, start with + and its code.',
            child: TkTextBox(key: const ValueKey('new-phone'), controller: _phone, phone: true),
          ),
          const SizedBox(height: 14),
          TkButton(label: _busy ? 'Sending…' : 'Send a code', primary: true, minHeight: 44, enabled: !_busy, onPressed: _send),
        ],
      _Step.old => [
          Text(
            'To keep your account safe, we also check it\'s you. Do you still have your old number${old == null ? '' : ', ${showPhone(old)}'}?',
            style: tkText(size: 15, color: t.text),
          ),
          const SizedBox(height: 14),
          TkButton(label: 'Yes, send it a code', primary: true, minHeight: 44, enabled: !_busy, onPressed: _yes),
          const SizedBox(height: 8),
          TkButton(label: 'No, prove it\'s me another way', minHeight: 44, enabled: !_busy, onPressed: _no),
        ],
      _Step.code => [
          Semantics(
            liveRegion: true,
            child: Text(
              '${sentWords(sent?.sentVia ?? 'whatsapp', _e164)}${_needsOld && old != null && sent?.oldSentVia != null ? ' And one to your old number, ${showPhone(old)}.' : ''}',
              style: tkText(size: 14, color: t.text),
            ),
          ),
          const SizedBox(height: 14),
          TkKitField(
            label: 'Code sent to your new number',
            child: TkTextBox(key: const ValueKey('new-code'), controller: _code, digits: 6),
          ),
          if (_left > 0)
            Padding(padding: const EdgeInsets.only(top: 6), child: Text('You can ask for a new code in ${countdown(_left)}.', style: tkHint(t)))
          else
            TkLinkButton(label: 'Send a new code', onPressed: _busy ? null : () => _run(() => _start(_e164, confirmOld: _needsOld))),
          if (sent?.sentVia != 'sms')
            TkLinkButton(label: 'Send by SMS instead', onPressed: _busy ? null : () => _run(() => _start(_e164, confirmOld: _needsOld, sms: true))),
          if (_needsOld) ...[
            const SizedBox(height: 14),
            TkKitField(label: 'Code sent to your old number', child: TkTextBox(key: const ValueKey('old-code'), controller: _old, digits: 6)),
          ],
          const SizedBox(height: 14),
          TkButton(label: _busy || _done ? 'Checking…' : 'Confirm the new number', primary: true, minHeight: 44, enabled: !_busy && !_done, onPressed: _verify),
        ],
    };
    return TkScreen(
      title: old == null ? 'Add your phone' : 'Change your number',
      children: [
        TkPanel(
          padding: const EdgeInsets.all(16),
          children: [
            Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Your account keeps everything: your chats, orders and businesses.', style: tkHint(t)),
                const SizedBox(height: 14),
                ...body,
              ],
            ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}
