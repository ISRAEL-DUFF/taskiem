// Signed in: the app's own settings (docs/spec/mobile-app.md §3.6) with Your
// phones (§5.2). Until the chat comes (M3) this is the screen the app opens
// on; then it moves behind the chat's menu.

import '../chat/chat_link_page.dart' show ChatLinkPage;
import '../chat/discover_settings.dart' show DiscoverSettingsPage;
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/lock.dart';
import '../app/notify.dart';
import '../app/passkey.dart';
import '../app/prefs.dart';
import '../kit/forms.dart' show TkSwitchRow;
import '../kit/page.dart' show TkPageScreen;
import '../kit/kit.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/forms.dart' show TkSwitch;
import '../kit/list.dart' show TkLoading;
import '../kit/mobile.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'settings_more.dart';
import 'switcher.dart' show lockWords, showSwitcher;

class SignedInScreen extends StatefulWidget {
  const SignedInScreen({super.key, required this.auth, this.prefs, this.unlocker, this.notify});
  final Auth auth;
  final Prefs? prefs;

  /// Notifications on this phone; none where the app has none.
  final PhoneNotifications? notify;

  /// The phone's own unlock: turning the app lock off asks for it.
  final Unlocker? unlocker;

  @override
  State<SignedInScreen> createState() => _SignedInScreenState();
}

class _SignedInScreenState extends State<SignedInScreen> {
  List<Phone>? _phones;
  List<BusinessEntry> _businesses = const [];
  String? _error;
  // Each read on its own; one the server has off, or that failed, is not shown.
  WhatsAppBinding? _wa;
  TelegramLink? _tg;
  SlackLink? _slack;
  List<Map<String, dynamic>>? _keys;

  @override
  void initState() {
    super.initState();
    _load();
  }

  /// Your phones and Your businesses, each on its own: one failing leaves
  /// the other shown.
  Future<void> _load() async {
    await Future.wait([
      _try(() async {
        final r = await widget.auth.api.listDevices();
        if (mounted) setState(() => _phones = r.devices);
      }),
      _try(() async {
        final b = await widget.auth.api.listBusinesses();
        if (mounted) setState(() => _businesses = b.businesses);
      }),
      _quiet(() async {
        final w = await widget.auth.api.getWhatsApp();
        if (mounted) setState(() => _wa = w);
      }),
      _quiet(() async {
        final g = await widget.auth.api.getTelegram();
        if (mounted) setState(() => _tg = g);
      }),
      _quiet(() async {
        final k = await widget.auth.api.getSlack();
        if (mounted) setState(() => _slack = k);
      }),
      _quiet(() async {
        final r = (await widget.auth.transport.send('GET', '/v1/me/passkeys') as Map)['passkeys'] as List;
        if (mounted) setState(() => _keys = [for (final k in r) (k as Map).cast<String, dynamic>()]);
      }),
    ]);
  }

  /// A row's own read: failing, the row is not shown (its page says why).
  Future<void> _quiet(Future<void> Function() f) async {
    try {
      await f();
    } on Object {
      // Off on this server, or offline.
    }
  }

  /// Runs [f], its failure said in the error box, never lost.
  Future<void> _try(Future<void> Function() f) async {
    try {
      await f();
    } on ApiError catch (e) {
      if (mounted) setState(() => _error = e.message);
    } on Offline {
      if (mounted) {
        setState(() => _error = 'You’re offline. This shows when you’re back online.');
      }
    } catch (_) {
      if (mounted) setState(() => _error = 'Something went wrong. Try again.');
    }
  }

  /// Into a business through the switcher, which asks first for what a
  /// locked one needs (taskiem-for-everyone §5.4, §6); back, read again.
  Future<void> _switch(BusinessEntry? b) async {
    await showSwitcher(context, widget.auth, open: b);
    if (mounted) await _load();
  }

  /// The lock's switch: on locks at once, off turns it off; a phone
  /// without a lock of its own is told.
  Future<void> _lock(Prefs prefs, bool on) async {
    final why = await chooseLock(prefs, widget.unlocker, on ? Duration.zero : null);
    if (why != null && mounted) showTkToast(context, why, error: true);
  }

  /// A page of its own, slid in; back, what it changed is read again.
  Future<void> _open(Widget page) async {
    await Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));
    if (mounted) await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final phones = _phones;
    final prefs = widget.prefs;
    final notify = widget.notify;
    final here = phones?.where((p) => p.this_ == true).firstOrNull;
    final wa = _wa;
    final tg = _tg;
    final slack = _slack;
    final waNumber = wa?.number is String ? wa!.number as String : null;
    final keys = _keys;
    String? since(Object? d) => d is String ? 'since ${_short(DateTime.parse(d))}' : (d is DateTime ? 'since ${_short(d)}' : null);
    Widget quiet(String words) => Text(words, style: tkText(size: 14, color: t.muted));
    // One list (docs/spec/mobile-app-look.md, "Settings"): you, your
    // businesses, this phone, signing in, the chats you work from and what
    // is for everyone; each that has more opens its own page.
    return TkPageScreen(
      title: 'Settings',
      titleSize: 30,
      backLabel: 'Back to Chats',
      children: [
        YouCard(auth: widget.auth),
        if (_error != null) TkErrorBox(_error!),
        // Every business and workflows workspace, a locked one with what it
        // needs; then You or any of them through the switcher.
        if (_businesses.isNotEmpty)
          TkSection(
            title: 'Your businesses',
            inset: 56,
            children: [
              for (final b in _businesses)
                TkListRow(
                  title: b.name,
                  sub: b.current == true
                      ? (b.kind == 'workflows' ? 'Workflows · here now' : 'Here now')
                      : (b.locked ? lockWords(b.lock) : (b.kind == 'workflows' ? 'Workflows' : '')),
                  icon: b.kind == 'workflows' ? 'b-flow' : 'b-store',
                  square: b.current == true ? look.lime : look.violet,
                  trailing: b.current == true ? null : TkSmallButton(label: 'Switch', semantics: 'Switch to ${b.name}', onPressed: () => _switch(b)),
                ),
              TkListRow(title: 'Switch to You or a business', icon: 'loop', square: look.teal, onTap: () => _switch(null)),
            ],
          ),
        TkSection(
          title: 'This phone',
          inset: 56,
          children: [
            if (notify != null) ..._phoneRows(notify, look),
            if (prefs != null)
              ListenableBuilder(
                listenable: prefs,
                // A switch, as drawn: on locks at once; the row opens when
                // it locks (at once, after a minute, after half an hour).
                builder: (context, _) => TkListRow(
                  title: 'App lock',
                  sub: prefs.lock
                      ? (prefs.lockAfter == Duration.zero ? 'Every time it opens · tap for when' : '${lockTimeLabel(prefs.lockAfter!)} away · tap for when')
                      : 'Fingerprint, face or PIN to open',
                  icon: 'b-lock',
                  square: look.blue,
                  trailing: TkSwitch(label: 'App lock', on: prefs.lock, onChanged: (on) => _lock(prefs, on)),
                  onTap: () => _open(SettingsPage(title: 'App lock', children: [LockCard(prefs: prefs, unlocker: widget.unlocker)])),
                ),
              ),
          ],
        ),
        if (prefs != null) _Appearance(prefs: prefs),
        if (keys != null || phones != null)
          TkSection(
            title: 'Signing in',
            inset: 56,
            children: [
              if (keys != null)
                TkListRow(
                  title: 'Passkeys',
                  sub: keys.isEmpty ? 'None yet' : '${keys.first['name']}${keys.first['synced'] == true ? ' · synced' : ''}',
                  icon: 'b-key',
                  square: look.violet,
                  trailing: keys.isEmpty ? null : quiet('${keys.length}'),
                  onTap: () => _open(SettingsPage(title: 'Passkeys', children: [PasskeysCard(auth: widget.auth)])),
                ),
              if (phones != null)
                TkListRow(
                  title: 'Your phones',
                  sub: here != null ? '${here.name} · this phone' : '',
                  icon: 'b-device',
                  square: look.teal,
                  trailing: quiet('${phones.length}'),
                  onTap: () => _open(_PhonesPage(auth: widget.auth)),
                ),
            ],
          ),
        if ((wa?.enabled ?? false) || (tg?.enabled ?? false) || (slack?.enabled ?? false))
          TkSection(
            title: 'Chats you work from',
            inset: 56,
            children: [
              if (wa != null && wa.enabled)
                TkListRow(
                  title: 'WhatsApp',
                  sub: waNumber != null ? [waNumber, ?since(wa.verifiedAt)].join(' · ') : 'Not linked',
                  icon: 'b-whatsapp',
                  square: const Color(0xFF25D366),
                  trailing: waNumber != null ? quiet(wa.dormant == true ? 'Resting' : 'Linked') : null,
                  onTap: () => _open(SettingsPage(title: 'WhatsApp', children: [WhatsAppCard(auth: widget.auth)])),
                ),
              if (tg != null && tg.enabled)
                TkListRow(
                  title: 'Telegram',
                  sub: tg.linked ? [if ((tg.username ?? '').isNotEmpty) '@${tg.username}', ?since(tg.verifiedAt)].join(' · ') : 'Not linked',
                  icon: 'b-telegram',
                  square: const Color(0xFF2AABEE),
                  trailing: tg.linked ? quiet(tg.dormant == true ? 'Resting' : 'Linked') : null,
                  onTap: () => _open(SettingsPage(title: 'Telegram', children: [TelegramCard(auth: widget.auth)])),
                ),
              if (slack != null && slack.enabled)
                TkListRow(
                  title: 'Slack',
                  sub: slack.linked ? ((slack.workspace ?? '').isNotEmpty ? slack.workspace! : 'In your workspace') : (slack.pending ? 'Waiting for your Confirm' : 'Not linked'),
                  icon: 'b-hash',
                  square: const Color(0xFF611F69),
                  trailing: slack.linked ? quiet('Linked') : null,
                  onTap: () => _open(SettingsPage(title: 'Slack', children: [SlackCard(auth: widget.auth)])),
                ),
            ],
          ),
        // Customers chatting in the app (taskiem-for-everyone §8.2): the chat
        // link, its QR code and the chat settings, on a page of their own.
        if (widget.auth.can('storefront.manage|order.handle'))
          TkSection(
            title: 'Customers',
            inset: 56,
            children: [
              TkListRow(
                title: 'Customer chats',
                sub: 'Your chat link, its QR code and how you answer',
                icon: 'b-chat',
                square: look.teal,
                onTap: () => _open(ChatLinkPage(auth: widget.auth)),
              ),
              // Discover (taskiem-for-everyone §10): found by the public in
              // the Taskiem app, apart from Explore.
              if (widget.auth.can('storefront.manage'))
                TkListRow(
                  title: 'Discover',
                  sub: 'Found by the public in the Taskiem app',
                  icon: 'b-search',
                  square: look.blue,
                  onTap: () => _open(DiscoverSettingsPage(auth: widget.auth)),
                ),
            ],
          ),
        // Owners and admins turn chat tools on for everyone.
        if (widget.auth.can('member.manage')) ChatToolsCard(auth: widget.auth),
        TkSection(
          inset: 56,
          children: [
            TkListRow(title: 'Sign out of this phone', icon: 'b-out', square: look.red, onTap: widget.auth.signOut),
          ],
        ),
      ],
    );
  }

  /// This phone's notifications: on or off, whether they say what came,
  /// and a row to the rest (what it tells you, your other devices).
  List<Widget> _phoneRows(PhoneNotifications n, TkLook look) => [
    ListenableBuilder(
      listenable: n,
      builder: (context, _) {
        if (n.available != true && !n.noGoogle) return const SizedBox.shrink();
        final mine = n.mine;
        final more = SettingsPage(title: 'Notifications on this phone', children: [NotificationsCard(notify: n)]);
        if (n.noGoogle) {
          return TkListRow(title: 'Notifications', sub: 'This phone cannot get them', icon: 'b-bell', square: look.orange, onTap: () => _open(more));
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkListRow(
              title: 'Notify me on this phone',
              sub: 'Messages, orders and what needs you',
              icon: 'b-bell',
              square: look.orange,
              trailing: TkSwitch(label: 'Notify me on this phone', on: n.isOn, onChanged: n.busy ? null : (v) => v ? n.turnOn() : n.turnOff()),
              onTap: n.busy ? null : () => n.isOn ? n.turnOff() : n.turnOn(),
            ),
            if (n.isOn && mine != null) ...[
              _line(context),
              TkListRow(
                title: 'Show what the message says',
                icon: 'b-eye',
                square: look.orange,
                trailing: TkSwitch(label: 'Show what the message says', on: mine.preview, onChanged: n.busy ? null : n.setPreview),
                onTap: n.busy ? null : () => n.setPreview(!mine.preview),
              ),
              _line(context),
              TkListRow(title: 'What this phone tells you', icon: 'b-bell', square: look.orange, onTap: () => _open(more)),
            ],
          ],
        );
      },
    ),
  ];

  static Widget _line(BuildContext context) => Padding(padding: const EdgeInsets.only(left: 56), child: Container(height: 1, color: TkLook.of(context).line));

  static String _short(DateTime d) {
    const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    final l = d.toLocal();
    return '${l.day} ${months[l.month - 1]}';
  }
}

/// Appearance: light, dark or as this phone, on its own card.
class _Appearance extends StatelessWidget {
  const _Appearance({required this.prefs});
  final Prefs prefs;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: prefs,
    builder: (context, _) => Container(
      decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(20)),
      padding: const EdgeInsets.only(bottom: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkListRow(title: 'Appearance', icon: 'b-moon', square: const Color(0xFF3A3B3A)),
          Padding(
            padding: const EdgeInsets.fromLTRB(0, 0, 0, 4),
            child: TkChips<ThemeChoice>(
              label: 'Appearance',
              chips: const [(ThemeChoice.light, 'Light'), (ThemeChoice.dark, 'Dark'), (ThemeChoice.system, 'Match this phone')],
              on: prefs.theme,
              onPick: prefs.setTheme,
            ),
          ),
        ],
      ),
    ),
  );
}

/// Your phones: each signed in, this one first; another signed out here.
class _PhonesPage extends StatefulWidget {
  const _PhonesPage({required this.auth});
  final Auth auth;

  @override
  State<_PhonesPage> createState() => _PhonesPageState();
}

class _PhonesPageState extends State<_PhonesPage> {
  List<Phone>? _phones;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listDevices();
      if (mounted) setState(() => (_phones = r.devices, _error = null));
    } on ApiError catch (e) {
      if (mounted) setState(() => _error = e.message);
    } on Offline {
      if (mounted) setState(() => _error = 'You’re offline. This shows when you’re back online.');
    } on Object {
      if (mounted) setState(() => _error = 'Something went wrong. Try again.');
    }
  }

  Future<void> _remove(Phone p) async {
    try {
      await widget.auth.api.removeDevice(id: p.id);
      await _load();
    } on Object {
      if (mounted) setState(() => _error = 'That phone could not be signed out. Try again.');
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final phones = [...?_phones]..sort((a, b) => (b.this_ == true ? 1 : 0) - (a.this_ == true ? 1 : 0));
    return SettingsPage(
      title: 'Your phones',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (_phones == null && _error == null) const TkLoading(label: 'Loading your phones…'),
        if (_phones != null)
          TkSection(
            inset: 56,
            children: [
              for (final p in phones)
                TkListRow(
                  title: p.name,
                  sub: '${p.platform == 'ios' ? 'iPhone' : 'Android'} · ${p.this_ == true ? 'this phone' : 'last used ${_when(p.lastUsedAt)}'}',
                  icon: 'b-device',
                  square: look.teal,
                  trailing: p.this_ == true ? null : TkSmallButton(label: 'Sign out', semantics: 'Sign out ${p.name}', onPressed: () => _remove(p)),
                ),
            ],
          ),
        const SettingsNote('Signing a phone out here ends Taskiem on it; it asks to sign in again.'),
      ],
    );
  }

  static String _when(DateTime d) {
    final ago = DateTime.now().difference(d);
    if (ago.inMinutes < 2) return 'just now';
    if (ago.inHours < 1) return '${ago.inMinutes} minutes ago';
    if (ago.inDays < 1) return '${ago.inHours} hours ago';
    if (ago.inDays == 1) return 'yesterday';
    return '${ago.inDays} days ago';
  }
}

/// Notifications on this phone (docs/spec/mobile-app-m4.md N4): the web's
/// ChatPush.tsx, its words. Shown once the server and the phone say they
/// can, or that this phone cannot.
class NotificationsCard extends StatelessWidget {
  const NotificationsCard({super.key, required this.notify});
  final PhoneNotifications notify;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: notify,
    builder: (context, _) {
      final t = TkTheme.tokens(context);
      final n = notify;
      if (n.available == null && !n.noGoogle) return const SizedBox.shrink();
      if (n.available != true && !n.noGoogle) return const SizedBox.shrink();
      final mine = n.mine;
      final others = (n.view?.devices ?? const <PushDevice>[]).where((d) => d.id != mine?.id).toList();
      final can = notify.auth.can;
      final offered = (n.view?.kinds ?? pushKinds).where(
        (k) => (k != 'business' || can('chat.trade')) && (k != 'customers' || can('storefront.manage|order.handle')),
      );
      return Padding(
        padding: EdgeInsets.zero,
        child: SettingsCard(
          title: 'Notifications on this phone',
          children: [
            if (n.error != null) TkErrorBox(n.error!),
            if (n.noGoogle)
              Text(
                'This phone cannot get Taskiem’s notifications (it has no Google services). WhatsApp or Telegram can tell you instead: link them below.',
                style: tkHint(t),
              )
            else ...[
              TkSwitchRow(
                label: 'Notify me on this phone',
                hint: n.personal ? 'When a business you buy from answers you, even when Taskiem is closed.' : 'Messages, orders and what needs you, even when Taskiem is closed.',
                on: n.isOn,
                onChanged: n.busy ? null : (v) => v ? n.turnOn() : n.turnOff(),
              ),
              if (n.blocked && !n.isOn)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(
                    'Notifications are off for Taskiem in this phone’s settings. Turn them on there (Settings, Apps, Taskiem, Notifications), then try again.',
                    style: tkHint(t),
                  ),
                ),
              if (n.isOn && mine != null) ...[
                const SizedBox(height: 12),
                TkSwitchRow(
                  label: 'Show what the message says',
                  hint: mine.preview ? null : 'Off: the notification says only that something came, not what it says.',
                  on: mine.preview,
                  onChanged: n.busy ? null : n.setPreview,
                ),
                if (offered.length > 1) ...[
                  const SizedBox(height: 16),
                  Text('What this phone tells you', style: tkText(size: 14, weight: 600, color: t.text)),
                  for (final k in offered)
                    Padding(
                      padding: const EdgeInsets.only(top: 12),
                      child: TkSwitchRow(
                        label: kindWords[k]!.$1,
                        hint: kindWords[k]!.$2,
                        on: mine.kinds.contains(k),
                        onChanged: n.busy ? null : (v) => n.setKind(k, v),
                      ),
                    ),
                ],
              ],
              // From a business: the person's own chats with businesses
              // (You's), on this phone too.
              if (!n.personal) ...[
                const SizedBox(height: 16),
                TkSwitchRow(
                  label: kindWords[youKind]!.$1,
                  hint: 'When a business you buy from answers you, on You. They come here while you work in a business too.',
                  on: n.youOn,
                  onChanged: n.busy ? null : n.setYou,
                ),
              ],
              _Devices(
                title: n.personal ? 'Your other devices' : 'Your other devices here',
                devices: others,
                busy: n.busy,
                onRemove: n.remove,
              ),
              if (!n.personal)
                _Devices(
                  title: 'Your devices for You',
                  devices: (n.youView?.devices ?? const <PushDevice>[]).where((d) => d.id != n.youMine?.id).toList(),
                  busy: n.busy,
                  onRemove: n.removeYou,
                ),
            ],
          ],
        ),
      );
    },
  );
}

/// A list of the person's other devices that are notified, each with
/// Remove.
class _Devices extends StatelessWidget {
  const _Devices({required this.title, required this.devices, required this.busy, required this.onRemove});
  final String title;
  final List<PushDevice> devices;
  final bool busy;
  final ValueChanged<String> onRemove;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (devices.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 16),
        Text(title, style: tkText(size: 14, weight: 600, color: t.text)),
        for (final d in devices)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(d.label, style: tkText(size: 14, weight: 500, color: t.text)),
                      Text(
                        d.kind == 'fcm' ? (d.platform == 'ios' ? 'Taskiem app on an iPhone' : 'Taskiem app on Android') : 'Browser',
                        style: tkHint(t),
                      ),
                    ],
                  ),
                ),
                TkSmallButton(label: 'Remove', semantics: 'Remove ${d.label}', onPressed: busy ? null : () => onRemove(d.id)),
              ],
            ),
          ),
      ],
    );
  }
}

/// Passkeys (docs/spec/mobile-app-m4.md N6; the web's Account, Passkeys):
/// the person's passkeys, and adding one on this phone. Shown where the
/// server has passkeys on.
class PasskeysCard extends StatefulWidget {
  const PasskeysCard({super.key, required this.auth});
  final Auth auth;

  @override
  State<PasskeysCard> createState() => _PasskeysCardState();
}

class _PasskeysCardState extends State<PasskeysCard> {
  List<Map<String, dynamic>>? _keys;
  // Whether this phone can make or use a passkey; said ahead, not after.
  bool _phoneCan = true;
  final _proof = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _proof.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final can = await passkeys.available();
    if (mounted) setState(() => _phoneCan = can);
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/me/passkeys') as Map)['passkeys'] as List;
      if (mounted) setState(() => _keys = [for (final k in r) (k as Map).cast<String, dynamic>()]);
    } on Object {
      // Off on this server (501), or offline: the card is not shown.
    }
  }

  Future<void> _add() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      // What the server asks before a new way in: a code, the password, or
      // the passkey the person already has.
      final Map<String, dynamic> proof = switch (proofAsked(widget.auth)) {
        'totp' => {'totp': _proof.text.trim()},
        'password' => {'password': _proof.text},
        null => {'passkey': await passkeyProof(widget.auth, 'account.reauth', 'passkey.add')},
        _ => const {},
      };
      await addPasskey(widget.auth, proof);
      _proof.clear();
      await _load();
      // me.factors now has a passkey.
      await widget.auth.refreshMe();
      if (mounted) showTkToast(context, 'Passkey added');
    } on PasskeyCancelled {
      // Closed the phone's sheet.
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    } on Object {
      _error = 'Something went wrong. Try again.';
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final keys = _keys;
    if (keys == null) return const SizedBox.shrink();
    final t = TkTheme.tokens(context);
    final asked = proofAsked(widget.auth);
    return Padding(
      padding: EdgeInsets.zero,
      child: SettingsCard(
        title: 'Passkeys',
        children: [
          Text(
            'Sign in and approve with your phone’s fingerprint, face or screen lock instead of a password. The same passkey works on the web.',
            style: tkHint(t),
          ),
          const SizedBox(height: 8),
          for (final k in keys)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Text('${k['name']}${k['synced'] == true ? ' · synced' : ''}', style: tkText(size: 14, weight: 500, color: t.text)),
            ),
          if (_error != null) TkErrorBox(_error!),
          if (!_phoneCan)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                'This phone cannot use passkeys (it needs a screen lock, and Android 9 or later). Your passkeys on other devices still work.',
                style: tkHint(t),
              ),
            ),
          if (_phoneCan && (asked == 'totp' || asked == 'password'))
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: TkField(
                label: asked == 'totp' ? 'Authenticator code, to add a passkey' : 'Your password, to add a passkey',
                controller: _proof,
                obscure: asked == 'password',
                digits: asked == 'totp' ? 6 : null,
                keyboard: asked == 'totp' ? TextInputType.number : null,
              ),
            ),
          if (_phoneCan) ...[
            const SizedBox(height: 8),
            TkButton(label: _busy ? 'Adding…' : 'Add a passkey on this phone', enabled: !_busy, onPressed: _add),
          ],
        ],
      ),
    );
  }
}
