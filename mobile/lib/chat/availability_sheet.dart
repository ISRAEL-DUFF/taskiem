// Availability (web/src/pages/ChatAvailability.tsx; docs/spec/chat-riders.md
// phase 3): a rider's business says whether it is free, busy till a time,
// or off. Every business that uses it as a rider sees the state in its
// riders list and when it offers a delivery.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/sheet.dart';
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkChips;
import '../theme/look.dart' show TkLook;
import 'give_task.dart' show TkPillChoice;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'tools.dart' show availabilityWord;

/// Opens Availability; [onChanged] once the state is changed (the tile reads
/// it again).
Future<void> showAvailabilitySheet(BuildContext context, {required Auth auth, VoidCallback? onChanged}) =>
    showTkSheet<void>(context, (_) => AvailabilitySheet(auth: auth, onChanged: onChanged));

/// What the server answers for the business that rides: its state, until
/// when, deliveries it holds, and how many businesses use it.
class AvailabilitySheet extends StatefulWidget {
  const AvailabilitySheet({super.key, required this.auth, this.onChanged});
  final Auth auth;
  final VoidCallback? onChanged;

  @override
  State<AvailabilitySheet> createState() => _AvailabilitySheetState();
}

class _AvailabilitySheetState extends State<AvailabilitySheet> {
  Map<String, dynamic>? _a;
  String? _loadError;
  final _until = TextEditingController();
  bool _busy = false;
  String? _error;
  String? _saved;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.transport.send('GET', '/v1/availability');
      if (mounted) setState(() => _a = (r as Map).cast<String, dynamic>());
    } on Object catch (e) {
      if (mounted) setState(() => _loadError = errorWords(e));
    }
  }

  @override
  void dispose() {
    _until.dispose();
    super.dispose();
  }

  /// The Busy till chip last chosen (as drawn: 1 hour, 2 hours, Till 5pm,
  /// Rest of today), or 'other' with a time typed.
  String? _till;

  /// on, off or busy (the words the business's WhatsApp takes too); busy
  /// till [until] when given, else till the time typed, if any.
  Future<void> _say(String state, {String? until}) async {
    final till = until ?? (_till == 'other' ? _until.text.trim() : '');
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.auth.transport.send(
        'PUT',
        '/v1/availability',
        body: {'state': state, if (state == 'busy' && till.isNotEmpty) 'until': till},
      );
      final next = (r as Map).cast<String, dynamic>();
      widget.onChanged?.call();
      if (!mounted) return;
      setState(() => (_a = next, _saved = availabilityWord(next), _busy = false));
      // The tick, then the sheet as it is now (the web's stays open).
      Future<void>.delayed(const Duration(milliseconds: 1300), () {
        if (mounted) setState(() => _saved = null);
      });
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  /// HH:MM in Lagos, [h] hours from now.
  static String _inHours(int h) {
    final l = DateTime.now().toUtc().add(Duration(hours: 1 + h));
    return '${'${l.hour}'.padLeft(2, '0')}:${'${l.minute}'.padLeft(2, '0')}';
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final a = _a;
    final n = (a?['businesses'] as num?)?.toInt() ?? 0;
    final open = (a?['open'] as num?)?.toInt() ?? 0;
    final lagosHour = DateTime.now().toUtc().add(const Duration(hours: 1)).hour;
    final tills = <(String, String, String)>[
      ('1h', '1 hour', _inHours(1)),
      ('2h', '2 hours', _inHours(2)),
      if (lagosHour < 16) ('5pm', 'Till 5pm', '5pm'),
      ('rest', 'Rest of today', ''),
    ];
    return TkAppLook(
      child: TkSheet(
        title: 'Availability',
        overlay: _saved == null ? null : TkSavedTick(words: _saved!),
        body: a == null
            ? ListView(
                children: [
                  if (_loadError != null)
                    Padding(padding: const EdgeInsets.symmetric(horizontal: 20), child: TkErrorBox(_loadError!))
                  else
                    const TkLoading(rows: 2, label: 'Checking your availability…'),
                ],
              )
            : TkFormBody(
                children: [
                  // As drawn: the state, large, and who sees it.
                  Semantics(
                    container: true,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(availabilityWord(a), style: tkDisplay(size: 28, letterSpacing: -0.8, color: t.text)),
                        const SizedBox(height: 6),
                        Text(
                          n == 0
                              ? 'No business has added you as its rider yet. Once one does, what you choose here is what they see.'
                              : n == 1
                              ? 'The business you deliver for sees this when it offers a delivery.'
                              : 'The $n businesses you deliver for see this when they offer a delivery.',
                          style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted),
                        ),
                      ],
                    ),
                  ),
                  if (_error != null) TkErrorBox(_error!),
                  TkChips<String>(
                    label: 'Say whether you are free',
                    padding: EdgeInsets.zero,
                    chips: const [('free', 'Free'), ('busy', 'Busy'), ('off', 'Off')],
                    on: a['state'] as String? ?? 'free',
                    onPick: (v) {
                      if (_busy) return;
                      _say(v == 'free' ? 'on' : v);
                    },
                  ),
                  Semantics(
                    container: true,
                    label: 'Busy till',
                    explicitChildNodes: true,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        ExcludeSemantics(child: Text('Busy till', style: tkText(size: 13, weight: 600, color: t.muted))),
                        const SizedBox(height: 8),
                        Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          children: [
                            for (final (k, words, until) in tills)
                              TkPillChoice(
                                label: words,
                                semantics: 'Busy for $words'.replaceFirst('Busy for Till', 'Busy till').replaceFirst('Busy for Rest', 'Busy the rest'),
                                on: _till == k && a['state'] == 'busy',
                                onTap: () {
                                  if (_busy) return;
                                  setState(() => _till = k);
                                  _say('busy', until: until);
                                },
                              ),
                            TkPillChoice(label: 'Another time', on: _till == 'other', onTap: () => setState(() => _till = 'other')),
                          ],
                        ),
                        if (_till == 'other') ...[
                          const SizedBox(height: 10),
                          // Not changed while a change is sent (the web's disabled).
                          Opacity(
                            opacity: _busy ? 0.6 : 1,
                            child: TkTextBox(controller: _until, hint: 'A time later today, like 3pm or 15:30', max: 20, enabled: !_busy, size: 15, height: 46),
                          ),
                          const SizedBox(height: 6),
                          Text('Then press Busy.', style: tkText(size: 12.5, color: t.muted)),
                        ],
                      ],
                    ),
                  ),
                  if (open > 0)
                    TkCallout(
                      leading: TkSquare('b-truck', color: look.orange),
                      title: 'You hold $open ${open == 1 ? 'delivery' : 'deliveries'} now',
                      sub: 'So you are busy until ${open == 1 ? 'it is' : 'they are'} delivered, whatever you choose.',
                      color: look.orange,
                    ),
                ],
              ),
      ),
    );
  }
}
