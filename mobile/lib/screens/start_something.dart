// Start something (docs/spec/taskiem-for-everyone.md §18.1): a business,
// with its name and what it does, or a workflows workspace, with an
// optional name. POST /v1/me/start makes it, its owner this person, and
// moves this phone's session into it, so the app opens on it.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/kit.dart';
import '../chat/approve.dart' show TkTextBox;
import '../kit/icon.dart';
import '../kit/mobile.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkCta;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'sign_up.dart' show KindTile, signupKinds;

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
    final look = TkLook.of(context);
    final person = widget.auth.me?.user?.name?.trim();
    // As the app's own pages: a choice is a row with its tile, ticked when
    // chosen; a business's kind is main's sign-up tiles; a name is a field
    // in a form group; the app's main button.
    Widget choice(String id, String title, String line, String icon, Color color) => TkListRow(
          title: title,
          sub: line,
          icon: icon,
          square: color,
          subLines: 2,
          chevron: false,
          semanticsLabel: '$title${_kind == id ? ', chosen' : ''}',
          trailing: _kind == id ? TkIcon('b-check', size: 20, stroke: 2.4, color: look.limeInk) : const SizedBox(width: 20),
          onTap: () => setState(() {
            _kind = id;
            _error = null;
          }),
        );
    return TkPageScreen(
      title: 'Start something',
      titleSize: 30,
      backLabel: 'Back to You',
      children: [
        TkSection(
          title: 'What do you want to start?',
          inset: 56,
          children: [
            choice('business', 'A business', 'Sell, take orders and bookings, send invoices, with your staff.', 'b-store', look.lime),
            choice('workflows', 'Workflows', 'Automate work for yourself or a team: forms, reminders, approvals, your other apps.', 'b-flow', look.violet),
          ],
        ),
        if (_kind == 'business') ...[
          TkFormGroup(children: [TkKitField(key: const ValueKey('business-name'), label: 'Business name', child: TkTextBox(controller: _name, max: 120, size: 15, height: 46))]),
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 0),
            child: Semantics(header: true, child: Text('What does your business do?', style: tkDisplay(size: 20, letterSpacing: -0.4, color: t.text))),
          ),
          LayoutBuilder(
            builder: (context, box) {
              final w = (box.maxWidth - 8) / 2;
              return Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final k in signupKinds.where((k) => k.id != 'workflows'))
                    SizedBox(width: w, child: KindTile(kind: k, on: _does == k.id, onTap: () => setState(() => _does = k.id))),
                ],
              );
            },
          ),
          Text('This sets up what you see first. Workflows, forms and chat are always there, and you can change it any time.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
        ],
        if (_kind == 'workflows')
          TkFormGroup(
            children: [
              TkKitField(
                key: const ValueKey('workflows-name'),
                label: 'Name (you can leave it empty)',
                hint: 'Left empty, it\'s called “${person == null || person.isEmpty ? 'My workflows' : '$person\'s workflows'}”. You can rename it later.',
                child: TkTextBox(controller: _name, max: 120, size: 15, height: 46),
              ),
            ],
          ),
        if (_error != null) TkErrorBox(_error!),
        if (_kind != null) TkCta(label: _busy ? 'Starting…' : (_kind == 'business' ? 'Start the business' : 'Start workflows'), onTap: _busy ? null : _start),
      ],
    );
  }
}
