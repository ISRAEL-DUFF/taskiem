// Start something (docs/spec/taskiem-for-everyone.md §18.1): a business,
// with its name and what it does, or a workflows workspace, with an
// optional name. POST /v1/me/start makes it, its owner this person, and
// moves this phone's session into it, so the app opens on it.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../theme/theme.dart';
import 'sign_up.dart' show signupKinds;

class StartSomethingScreen extends StatefulWidget {
  const StartSomethingScreen({super.key, required this.auth, this.kind});
  final Auth auth;

  /// Chosen already (a test, a picture): `business` or `workflows`.
  final String? kind;

  @override
  State<StartSomethingScreen> createState() => _StartSomethingScreenState();
}

class _StartSomethingScreenState extends State<StartSomethingScreen> {
  late String? _kind = widget.kind;
  String? _does;
  final _name = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  Future<void> _start() async {
    final kind = _kind;
    if (kind == null || _busy) return;
    final name = _name.text.trim();
    if (kind == 'business' && name.isEmpty) return setState(() => _error = 'Give your business a name.');
    if (kind == 'business' && _does == null) return setState(() => _error = 'Choose what your business does.');
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.auth.startSomething(kind: kind, name: name, kinds: kind == 'business' ? [_does!] : null);
      // The session is now the new one's: the app opens on it.
      return;
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
    final t = TkTheme.tokens(context);
    final words = tkText(size: 14, color: t.text);
    final person = widget.auth.me?.user?.name?.trim();
    Widget choice(String id, String title, String line) => TkChoiceRow(
          control: TkRadio(checked: _kind == id),
          label: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(title, style: tkText(size: 15, weight: 600, color: t.text)),
              Text(line, style: tkHint(t)),
            ],
          ),
          onTap: () => setState(() {
            _kind = id;
            _error = null;
          }),
        );
    return TkScreen(
      title: 'Start something',
      children: [
        TkPanel(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
          children: [
            Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.only(bottom: 14),
                  child: Text('What do you want to start?', style: tkText(size: 17, weight: 700, color: t.text)),
                ),
                choice('business', 'A business', 'Sell, take orders and bookings, send invoices, with your staff.'),
                choice('workflows', 'Workflows', 'Automate work for yourself or a team: forms, reminders, approvals, your other apps.'),
              ],
            ),
          ],
        ),
        if (_kind == 'business')
          TkPanel(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
            children: [
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  TkField(key: const ValueKey('business-name'), label: 'Business name', controller: _name, minHeight: 44),
                  const SizedBox(height: 16),
                  Padding(
                    padding: const EdgeInsets.only(bottom: 10),
                    child: Text('What does your business do?', style: tkText(size: 13, weight: 600, color: t.muted)),
                  ),
                  Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: Text('This sets up what you see first. Workflows, forms and chat are always there, and you can change it any time.', style: tkHint(t)),
                  ),
                  for (final k in signupKinds.where((k) => k.id != 'workflows'))
                    TkChoiceRow(
                      control: TkRadio(checked: _does == k.id),
                      label: Text(k.label, style: words),
                      onTap: () => setState(() => _does = k.id),
                    ),
                ],
              ),
            ],
          ),
        if (_kind == 'workflows')
          TkPanel(
            padding: const EdgeInsets.all(16),
            children: [
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  TkField(key: const ValueKey('workflows-name'), label: 'Name (you can leave it empty)', controller: _name, minHeight: 44),
                  Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text(
                      'Left empty, it\'s called “${person == null || person.isEmpty ? 'My workflows' : '$person\'s workflows'}”. You can rename it later.',
                      style: tkHint(t),
                    ),
                  ),
                ],
              ),
            ],
          ),
        if (_error != null) TkErrorBox(_error!),
        if (_kind != null)
          TkButton(label: _busy ? 'Starting…' : (_kind == 'business' ? 'Start the business' : 'Start workflows'), primary: true, minHeight: 44, enabled: !_busy, onPressed: _start),
      ],
    );
  }
}
