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
import '../kit/tap_area.dart';
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

  /// on, off or busy (the words the business's WhatsApp takes too).
  Future<void> _say(String state) async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.auth.transport.send(
        'PUT',
        '/v1/availability',
        body: {'state': state, if (state == 'busy' && _until.text.trim().isNotEmpty) 'until': _until.text.trim()},
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

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = _a;
    final n = (a?['businesses'] as num?)?.toInt() ?? 0;
    final open = (a?['open'] as num?)?.toInt() ?? 0;
    return TkSheet(
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
                // `.tk-avail-now`: the state, large, and who sees it.
                Semantics(
                  container: true,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(availabilityWord(a), style: tkText(size: 18, weight: 700, color: t.text)),
                      const SizedBox(height: 4),
                      Text(
                        n == 0
                            ? 'No business has added you as its rider yet. Once one does, what you choose here is what they see.'
                            : n == 1
                            ? 'The business you deliver for sees this when it offers a delivery.'
                            : 'The $n businesses you deliver for see this when they offer a delivery.',
                        style: tkText(size: 13, weight: 500, color: t.muted),
                      ),
                    ],
                  ),
                ),
                if (_error != null) TkErrorBox(_error!),
                Semantics(
                  container: true,
                  label: 'Say whether you are free',
                  explicitChildNodes: true,
                  child: TkSegmented(
                    choices: const [('on', 'Free', 'free'), ('busy', 'Busy', 'busy'), ('off', 'Off', 'off')],
                    on: a['state'] as String? ?? 'free',
                    onPick: _busy ? null : _say,
                  ),
                ),
                TkKitField(
                  label: 'Busy till (a time later today, like 3pm or 15:30)',
                  // Not changed while a change is sent (the web's disabled).
                  child: Opacity(
                    opacity: _busy ? 0.6 : 1,
                    child: TkTextBox(controller: _until, hint: 'Leave empty for the rest of the day (an hour at least)', max: 20, enabled: !_busy),
                  ),
                ),
                if (open > 0)
                  Text(
                    'You hold $open ${open == 1 ? 'delivery' : 'deliveries'} now, so you are busy until they are delivered, whatever you choose.',
                    style: tkHint(t),
                  ),
              ],
            ),
    );
  }
}

/// styles.css `.segmented`: pill buttons on a soft track, the one chosen
/// filled; on a phone each takes an equal share. A choice is (what it
/// sends, its words, the state it shows as chosen).
class TkSegmented extends StatelessWidget {
  const TkSegmented({super.key, required this.choices, required this.on, required this.onPick});
  final List<(String, String, String)> choices;
  final String on;
  final ValueChanged<String>? onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Container(
      padding: const EdgeInsets.all(4),
      decoration: BoxDecoration(color: t.codeBg, borderRadius: BorderRadius.circular(999)),
      child: Row(
        children: [
          for (var i = 0; i < choices.length; i++) ...[
            if (i > 0) const SizedBox(width: 4),
            Expanded(
              child: Builder(
                builder: (context) {
                  final (send, words, shows) = choices[i];
                  final chosen = on == shows;
                  final go = onPick == null ? null : () => onPick!(send);
                  return TapArea(
                    child: Semantics(
                      button: true,
                      toggled: chosen,
                      enabled: go != null,
                      label: words,
                      onTap: go,
                      excludeSemantics: true,
                      child: GestureDetector(
                        behavior: HitTestBehavior.opaque,
                        onTap: go,
                        child: Container(
                          constraints: const BoxConstraints(minHeight: 36),
                          alignment: Alignment.center,
                          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                          decoration: BoxDecoration(
                            color: chosen ? (dark ? t.brandDark : t.brand) : null,
                            borderRadius: BorderRadius.circular(999),
                          ),
                          child: Text(
                            words,
                            textAlign: TextAlign.center,
                            style: tkText(
                              size: 15,
                              weight: 600,
                              color: chosen ? (dark ? t.brandDarkText : const Color(0xFFFFFFFF)) : (go == null ? t.muted : t.navText),
                            ),
                          ),
                        ),
                      ),
                    ),
                  );
                },
              ),
            ),
          ],
        ],
      ),
    );
  }
}
