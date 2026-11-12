// Signed in: the app's own settings (docs/spec/mobile-app.md §3.6) with Your
// phones (§5.2). Until the chat comes (M3) this is the screen the app opens
// on; then it moves behind the chat's menu.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/lock.dart';
import '../app/prefs.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'settings_more.dart';

class SignedInScreen extends StatefulWidget {
  const SignedInScreen({super.key, required this.auth, this.prefs, this.unlocker});
  final Auth auth;
  final Prefs? prefs;

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
