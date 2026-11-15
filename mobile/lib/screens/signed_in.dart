// Signed in: the app's own settings (docs/spec/mobile-app.md §3.6) with Your
// phones (§5.2). Until the chat comes (M3) this is the screen the app opens
// on; then it moves behind the chat's menu.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/lock.dart';
import '../app/notify.dart';
import '../app/passkey.dart';
import '../app/prefs.dart';
import '../kit/forms.dart' show TkSwitchRow;
import '../kit/kit.dart';
import '../kit/modal.dart' show showTkToast;
import '../theme/theme.dart';
import 'settings_more.dart';

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
  List<ListBusinessesResponseBusinessesItem> _businesses = const [];
  String? _error;

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
    ]);
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

  Future<void> _switch(ListBusinessesResponseBusinessesItem b) =>
      _try(() async {
        await widget.auth.switchTo(b.id);
        await _load();
      });

  Future<void> _remove(Phone p) => _try(() async {
    await widget.auth.api.removeDevice(id: p.id);
    await _load();
  });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final me = widget.auth.me;
    final phones = _phones;
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final prefs = widget.prefs;
    // The bar over the app reads on the page: dark icons on light.
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ColoredBox(
        color: t.bg,
        child: SafeArea(
          child: ListView(
            padding: const EdgeInsets.fromLTRB(16, 24, 16, 32),
            children: [
              Text(
                'Settings',
                style: tkText(
                  size: 24,
                  weight: 700,
                  lineHeight: 1.2,
                  letterSpacing: -0.24,
                  color: t.text,
                ),
              ),
              if (me?.tenantName != null)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                    'Signed in to ${me!.tenantName}',
                    style: tkHint(t),
                  ),
                ),
              const SizedBox(height: 4),
              YouCard(auth: widget.auth),
              const SizedBox(height: 16),
              if (_businesses.length > 1) ...[
                _Card(
                  title: 'Your businesses',
                  children: [
                    for (final b in _businesses)
                      Padding(
                        padding: const EdgeInsets.symmetric(vertical: 8),
                        child: Row(
                          children: [
                            Expanded(
                              child: Text(
                                b.name,
                                style: tkText(
                                  size: 14,
                                  weight: 500,
                                  color: t.text,
                                ),
                              ),
                            ),
                            if (b.current == true)
                              Text('Here now', style: tkHint(t))
                            else
                              TkButton(
                                label: 'Switch',
                                onPressed: () => _switch(b),
                              ),
                          ],
                        ),
                      ),
                  ],
                ),
                const SizedBox(height: 16),
              ],
              _Card(
                title: 'Your phones',
                children: [
                  if (_error != null) TkErrorBox(_error!),
                  if (phones == null && _error == null)
                    Text('Loading…', style: tkHint(t)),
                  if (phones != null)
                    for (final p in phones)
                      _PhoneRow(
                        phone: p,
                        onRemove: p.this_ == true ? null : () => _remove(p),
                      ),
                ],
              ),
              if (widget.notify != null) NotificationsCard(notify: widget.notify!),
              PasskeysCard(auth: widget.auth),
              if (prefs != null) LockCard(prefs: prefs, unlocker: widget.unlocker),
              if (prefs != null) ...[
                const SizedBox(height: 16),
                ListenableBuilder(
                  listenable: prefs,
                  builder: (context, _) => _Card(
                    title: 'Appearance',
                    children: [
                      for (final c in const [
                        (ThemeChoice.light, 'Light'),
                        (ThemeChoice.dark, 'Dark'),
                        (ThemeChoice.system, 'Match this device'),
                      ])
                        TkChoiceRow(
                          control: TkRadio(checked: prefs.theme == c.$1),
                          label: Text(
                            c.$2,
                            style: tkText(size: 14, color: t.text),
                          ),
                          onTap: () => prefs.setTheme(c.$1),
                        ),
                    ],
                  ),
                ),
              ],
              // Owners and admins turn chat tools on for everyone.
              if (widget.auth.can('member.manage')) ChatToolsCard(auth: widget.auth),
              // The chats you have linked (Account on the web).
              WhatsAppCard(auth: widget.auth),
              TelegramCard(auth: widget.auth),
              SlackCard(auth: widget.auth),
              const SizedBox(height: 16),
              TkButton(
                label: 'Sign out of this phone',
                minHeight: 44,
                onPressed: widget.auth.signOut,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Card extends StatelessWidget {
  const _Card({required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    // The web's `.card`: the panel, its border, the radius; 20 px by 22.
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 20),
      decoration: BoxDecoration(
        color: t.panel,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(Tk.radius),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Text(
              title,
              style: tkText(
                size: 16,
                weight: 600,
                lineHeight: 1.3,
                color: t.text,
              ),
            ),
          ),
          ...children,
        ],
      ),
    );
  }
}

class _PhoneRow extends StatelessWidget {
  const _PhoneRow({required this.phone, this.onRemove});
  final Phone phone;
  final VoidCallback? onRemove;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final kind = phone.platform == 'ios' ? 'iPhone' : 'Android';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  phone.name,
                  style: tkText(size: 14, weight: 500, color: t.text),
                ),
                Text(
                  phone.this_ == true
                      ? '$kind · this phone'
                      : '$kind · last used ${_when(phone.lastUsedAt)}',
                  style: tkHint(t),
                ),
              ],
            ),
          ),
          if (onRemove != null)
            TkButton(label: 'Sign out', onPressed: onRemove),
        ],
      ),
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
        padding: const EdgeInsets.only(bottom: 16),
        child: _Card(
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
                hint: 'Messages, orders and what needs you, even when Taskiem is closed.',
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
              if (others.isNotEmpty) ...[
                const SizedBox(height: 16),
                Text('Your other devices', style: tkText(size: 14, weight: 600, color: t.text)),
                for (final d in others)
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
                        TkButton(label: 'Remove', onPressed: n.busy ? null : () => n.remove(d.id)),
                      ],
                    ),
                  ),
              ],
            ],
          ],
        ),
      );
    },
  );
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
      padding: const EdgeInsets.only(bottom: 16),
      child: _Card(
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
